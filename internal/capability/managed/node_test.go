package managed

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nodeDoer func(*http.Request) (*http.Response, error)

func (f nodeDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }
func nodeFixture(t *testing.T, link string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	root := "node-v24.1.2-linux-x64/"
	for _, name := range []string{"bin/node", "lib/node_modules/npm/bin/npm-cli.js", "lib/node_modules/npm/bin/npx-cli.js", "lib/node_modules/npm/lib/cli.js"} {
		body := []byte("fixture " + name)
		if err := tw.WriteHeader(&tar.Header{Name: root + name, Mode: 0755, Typeflag: tar.TypeReg, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		tw.Write(body)
	}
	if err := tw.WriteHeader(&tar.Header{Name: root + "bin/npm", Mode: 0777, Typeflag: tar.TypeSymlink, Linkname: link}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func TestManagedNodePreservesLayoutAndReconstructsLauncher(t *testing.T) {
	body := nodeFixture(t, "../lib/node_modules/npm/bin/npm-cli.js")
	sum := sha256.Sum256(body)
	i := &Installer{DataDir: t.TempDir(), GOOS: "linux", GOARCH: "amd64", Client: nodeDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
	})}
	asset := Asset{URL: "https://nodejs.org/dist/v24.1.2/node-v24.1.2-linux-x64.tar.gz", SHA256: hex.EncodeToString(sum[:]), Format: "tar.gz"}
	result, err := i.InstallNode(context.Background(), "24.1.2", asset)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(result.Executables["npm"])
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("launcher must be generated regular file", err)
	}
	launcher, _ := os.ReadFile(result.Executables["npm"])
	if !strings.Contains(string(launcher), `exec "$basedir/node"`) {
		t.Fatal("missing relative Node launcher")
	}
	if _, err := os.Stat(filepath.Join(result.Directory, "node-v24.1.2-linux-x64/lib/node_modules/npm/lib/cli.js")); err != nil {
		t.Fatal(err)
	}
}
func TestManagedNodeRejectsUntrustedLinksOriginAndHash(t *testing.T) {
	for _, mode := range []string{"link", "origin", "hash"} {
		t.Run(mode, func(t *testing.T) {
			link := "../lib/node_modules/npm/bin/npm-cli.js"
			if mode == "link" {
				link = "../../../../outside"
			}
			body := nodeFixture(t, link)
			sum := sha256.Sum256(body)
			i := &Installer{DataDir: t.TempDir(), GOOS: "linux", GOARCH: "amd64", Client: nodeDoer(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
			})}
			asset := Asset{URL: "https://nodejs.org/dist/v24.1.2/node-v24.1.2-linux-x64.tar.gz", SHA256: hex.EncodeToString(sum[:]), Format: "tar.gz"}
			if mode == "origin" {
				asset.URL = "https://example.com/node.tar.gz"
			}
			if mode == "hash" {
				asset.SHA256 = strings.Repeat("0", 64)
			}
			if _, err := i.InstallNode(context.Background(), "24.1.2", asset); err == nil {
				t.Fatal("unsafe bundle accepted")
			}
		})
	}
}
func TestNPMUsesExplicitManagedNode(t *testing.T) {
	e := &fakeExecutor{}
	i := &Installer{DataDir: t.TempDir(), Executor: e, MalwareChecker: &fakeChecker{}}
	r := npmFixture()
	r.NodePath = filepath.Join(i.DataDir, "node", "bin", "node")
	r.NPMCLI = filepath.Join(i.DataDir, "node", "lib", "node_modules", "npm", "bin", "npm-cli.js")
	if _, err := i.InstallNPM(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if len(e.calls) != 1 || e.calls[0].Path != r.NodePath || e.calls[0].Args[0] != r.NPMCLI {
		t.Fatalf("wrong Node invocation: %+v", e)
	}
}

func TestManagedNodeWindowsBundleLayout(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	root := "node-v24.1.2-win-arm64/"
	for _, name := range []string{"node.exe", "npm.cmd", "node_modules/npm/bin/npm-cli.js", "node_modules/npm/bin/npx-cli.js"} {
		w, e := z.Create(root + name)
		if e != nil {
			t.Fatal(e)
		}
		w.Write([]byte("fixture"))
	}
	z.Close()
	sum := sha256.Sum256(b.Bytes())
	i := &Installer{DataDir: t.TempDir(), GOOS: "windows", GOARCH: "arm64", Client: nodeDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b.Bytes())), Request: r}, nil
	})}
	result, err := i.InstallNode(context.Background(), "24.1.2", Asset{URL: "https://nodejs.org/dist/v24.1.2/node-v24.1.2-win-arm64.zip", Format: "zip", SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(result.Executables["node"], "node.exe") || !strings.HasSuffix(result.Executables["npm"], "npm.cmd") {
		t.Fatalf("layout %+v", result)
	}
}
