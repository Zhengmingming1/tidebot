package managed

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func archiveZip(t *testing.T, name string, mode os.FileMode) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: name, Method: zip.Deflate}
	h.SetMode(mode)
	w, e := z.CreateHeader(h)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = w.Write([]byte("fixture-binary"))
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func fixtureInstaller(t *testing.T, body []byte) (*Installer, ArchiveRecipe) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(body)
	i := &Installer{DataDir: t.TempDir(), GOOS: "linux", GOARCH: "amd64"}
	r := ArchiveRecipe{Name: "fixture", Version: "1.2.3", Backend: "github-release", Assets: map[string]Asset{"linux-amd64": {URL: srv.URL, SHA256: hex.EncodeToString(sum[:]), Format: "zip"}}, Executables: map[string]string{"tool": "bin/tool"}}
	return i, r
}
func TestArchiveInstallsAtomically(t *testing.T) {
	i, r := fixtureInstaller(t, archiveZip(t, "bin/tool", 0755))
	got, e := i.InstallArchive(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if got.Verification != "sha256-integrity-only" || got.Source != "managed" {
		t.Fatalf("metadata: %+v", got)
	}
	b, e := os.ReadFile(got.Executables["tool"])
	if e != nil || string(b) != "fixture-binary" {
		t.Fatalf("installed binary %q: %v", b, e)
	}
	if _, e = i.InstallArchive(context.Background(), r); !errors.Is(e, ErrAlreadyInstalled) {
		t.Fatalf("must not overwrite: %v", e)
	}
	entries, _ := os.ReadDir(filepath.Dir(got.Directory))
	if len(entries) != 1 {
		t.Fatalf("staging leaked: %v", entries)
	}
}
func TestArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
	}{{"../escape", 0600}, {"/absolute", 0600}, {"C:/evil", 0600}, {"bin\\evil", 0600}, {"link", os.ModeSymlink | 0777}, {"device", os.ModeDevice | 0600}} {
		t.Run(tc.name, func(t *testing.T) {
			i, r := fixtureInstaller(t, archiveZip(t, tc.name, tc.mode))
			if _, e := i.InstallArchive(context.Background(), r); e == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, e := os.Stat(filepath.Join(i.DataDir, "tools", "fixture", "1.2.3")); !os.IsNotExist(e) {
				t.Fatal("partial install retained")
			}
		})
	}
}
func TestArchiveRejectsTarLink(t *testing.T) {
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	w := tar.NewWriter(g)
	_ = w.WriteHeader(&tar.Header{Name: "bin/tool", Typeflag: tar.TypeLink, Linkname: "/etc/passwd"})
	_ = w.Close()
	_ = g.Close()
	i, r := fixtureInstaller(t, b.Bytes())
	a := r.Assets["linux-amd64"]
	a.Format = "tar.gz"
	r.Assets["linux-amd64"] = a
	if _, e := i.InstallArchive(context.Background(), r); e == nil {
		t.Fatal("hardlink accepted")
	}
}
func TestArchiveBadHashAndSignature(t *testing.T) {
	for _, variant := range []string{"hash", "signature", "version", "platform", "missing-executable"} {
		t.Run(variant, func(t *testing.T) {
			i, r := fixtureInstaller(t, archiveZip(t, "bin/tool", 0755))
			a := r.Assets["linux-amd64"]
			switch variant {
			case "hash":
				a.SHA256 = strings.Repeat("0", 64)
			case "signature":
				a.Signature = &Signature{Kind: "cosign"}
			case "version":
				r.Version = "latest"
			case "platform":
				i.GOARCH = "arm64"
			case "missing-executable":
				r.Executables["tool"] = "absent"
			}
			r.Assets["linux-amd64"] = a
			if _, e := i.InstallArchive(context.Background(), r); e == nil {
				t.Fatal("unsafe recipe accepted")
			}
		})
	}
}
func TestArchiveContextAndSymlink(t *testing.T) {
	i, r := fixtureInstaller(t, archiveZip(t, "bin/tool", 0755))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := i.InstallArchive(ctx, r); e == nil {
		t.Fatal("cancellation ignored")
	}
	target := t.TempDir()
	if e := os.Symlink(target, filepath.Join(i.DataDir, "tools")); e != nil {
		t.Skip(e)
	}
	if _, e := i.InstallArchive(context.Background(), r); e == nil {
		t.Fatal("symlink destination accepted")
	}
}

type fakeExecutor struct {
	calls []Command
	fail  bool
}

func (e *fakeExecutor) Run(_ context.Context, c Command) error {
	e.calls = append(e.calls, c)
	if e.fail {
		return errors.New("fixture failure")
	}
	return nil
}

type fakeChecker struct {
	calls []string
	fail  bool
}

func (c *fakeChecker) Check(_ context.Context, ecosystem, name, version string) error {
	c.calls = append(c.calls, ecosystem+":"+name+"@"+version)
	if c.fail {
		return errors.New("malicious fixture")
	}
	return nil
}
func npmFixture() PackageRecipe {
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))
	lock, _ := json.Marshal(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{"dependencies": map[string]string{"fixture": "1.2.3"}}, "node_modules/fixture": map[string]string{"version": "1.2.3", "resolved": "https://registry.npmjs.org/fixture/-/fixture-1.2.3.tgz", "integrity": integrity}}})
	return PackageRecipe{CapabilityID: "cap", VersionID: "rev1", NPMPath: filepath.Join(os.TempDir(), "npm"), Package: "fixture", Version: "1.2.3", LockJSON: lock}
}
func TestNPMPrivateInstall(t *testing.T) {
	e := &fakeExecutor{}
	c := &fakeChecker{}
	i := &Installer{DataDir: t.TempDir(), Executor: e, MalwareChecker: c}
	got, err := i.InstallNPM(context.Background(), npmFixture())
	if err != nil {
		t.Fatal(err)
	}
	if len(e.calls) != 1 || !contains(e.calls[0].Args, "--ignore-scripts") || e.calls[0].Args[0] != "ci" {
		t.Fatalf("unsafe command: %+v", e.calls)
	}
	if len(c.calls) != 1 || got.Verification != "lockfile-integrity-and-osv" {
		t.Fatal("missing malware/integrity check")
	}
	if _, err = os.Stat(filepath.Join(got.Directory, "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	for _, v := range e.calls[0].Env {
		if strings.HasPrefix(v, "NPM_CONFIG_USERCONFIG=") && v != "NPM_CONFIG_USERCONFIG="+os.DevNull {
			t.Fatal("user config inherited")
		}
	}
}
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func TestNPMLockFailClosed(t *testing.T) {
	for _, variant := range []string{"missing", "unpinned", "link", "integrity", "root", "url", "name"} {
		t.Run(variant, func(t *testing.T) {
			r := npmFixture()
			var lock map[string]any
			_ = json.Unmarshal(r.LockJSON, &lock)
			packages := lock["packages"].(map[string]any)
			p := packages["node_modules/fixture"].(map[string]any)
			switch variant {
			case "missing":
				delete(packages, "node_modules/fixture")
			case "unpinned":
				p["version"] = "latest"
			case "link":
				p["link"] = true
			case "integrity":
				delete(p, "integrity")
			case "root":
				packages[""] = map[string]any{}
			case "url":
				p["resolved"] = "file:///tmp/evil"
			case "name":
				p["name"] = "../../evil"
			}
			r.LockJSON, _ = json.Marshal(lock)
			if _, err := validateLock(r); err == nil {
				t.Fatal("unsafe lock accepted")
			}
		})
	}
}
func TestPackageFailureRollsBack(t *testing.T) {
	i := &Installer{DataDir: t.TempDir(), Executor: &fakeExecutor{fail: true}, MalwareChecker: &fakeChecker{}}
	if _, err := i.InstallNPM(context.Background(), npmFixture()); err == nil {
		t.Fatal("failure ignored")
	}
	entries, _ := os.ReadDir(filepath.Join(i.DataDir, "envs", "cap"))
	if len(entries) != 0 {
		t.Fatalf("partial environment: %v", entries)
	}
}
func TestUVHashOnlyPrivateEnvironment(t *testing.T) {
	e := &fakeExecutor{}
	i := &Installer{DataDir: t.TempDir(), Executor: e, MalwareChecker: &fakeChecker{}, GOOS: "linux"}
	r := PythonRecipe{CapabilityID: "cap", VersionID: "rev1", UVPath: filepath.Join(os.TempDir(), "uv"), PythonPath: filepath.Join(os.TempDir(), "python"), Packages: []PythonPackage{{Name: "fixture", Version: "1.2.3", SHA256: []string{strings.Repeat("a", 64)}}}}
	got, err := i.InstallUV(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.calls) != 3 || !contains(e.calls[0].Args, "--relocatable") {
		t.Fatalf("unsafe venv: %+v", e.calls)
	}
	for _, arg := range []string{"--require-hashes", "--no-deps", "--only-binary"} {
		if !contains(e.calls[1].Args, arg) {
			t.Fatalf("missing %s", arg)
		}
	}
	b, err := os.ReadFile(filepath.Join(got.Directory, "requirements.txt"))
	if err != nil || !strings.Contains(string(b), "fixture==1.2.3 --hash=sha256:") {
		t.Fatal("missing requirements")
	}
}
func TestOSVMalwareChecks(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		bad    bool
	}{{`{"vulns":[]}`, 200, false}, {`{"vulns":[{"id":"GHSA-test","aliases":["MAL-2026-123"]}]}`, 200, true}, {`{"vulns":[{"id":"CVE-2026-123"}]}`, 200, false}, {`invalid`, 200, true}, {`{}`, 503, true}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				t.Error("must POST")
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		err := (&OSVClient{Endpoint: srv.URL}).Check(context.Background(), "npm", "fixture", "1.2.3")
		srv.Close()
		if (err != nil) != tc.bad {
			t.Fatalf("body=%s err=%v", tc.body, err)
		}
	}
}

type fakeResolver struct{ found bool }

func (r fakeResolver) ResolveCompatible(context.Context, string, string) (Result, bool, error) {
	return Result{Executables: map[string]string{"fixture": "/system/fixture"}}, r.found, nil
}
func TestSystemFirstPolicy(t *testing.T) {
	calls := 0
	install := func(context.Context) (Result, error) { calls++; return Result{Source: "managed"}, nil }
	r, err := ResolveSystemFirst(context.Background(), fakeResolver{true}, "fixture", ">=1", false, install)
	if err != nil || calls != 0 || r.Source != "system" {
		t.Fatal("system was not reused")
	}
	r, err = ResolveSystemFirst(context.Background(), fakeResolver{true}, "fixture", ">=1", true, install)
	if err != nil || calls != 1 || !reflect.DeepEqual(r, Result{Source: "managed"}) {
		t.Fatal("managed pin ignored")
	}
}

type fakeSignature struct {
	called bool
	fail   bool
}

func (s *fakeSignature) Verify(context.Context, string, Signature) error {
	s.called = true
	if s.fail {
		return errors.New("invalid signature")
	}
	return nil
}
func TestSignatureCannotBeClaimedWithoutVerification(t *testing.T) {
	for _, fail := range []bool{false, true} {
		i, r := fixtureInstaller(t, archiveZip(t, "bin/tool", 0755))
		v := &fakeSignature{fail: fail}
		i.Signatures = v
		a := r.Assets["linux-amd64"]
		a.Signature = &Signature{Kind: "fixture", Identity: "trusted-fixture"}
		r.Assets["linux-amd64"] = a
		got, err := i.InstallArchive(context.Background(), r)
		if !v.called || (err != nil) != fail {
			t.Fatalf("signature verification called=%v error=%v", v.called, err)
		}
		if !fail && got.Verification != "sha256-and-signature" {
			t.Fatal("wrong verification metadata")
		}
	}
}
func TestDownloadSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "268435457")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	i := &Installer{}
	var b bytes.Buffer
	if err := i.download(context.Background(), srv.URL, &b, MaxDownloadBytes); err == nil {
		t.Fatal("oversized download accepted")
	}
}
func TestSafeRelativePortable(t *testing.T) {
	for _, path := range []string{"CON", "aux.exe", "a/NUL", "LPT1", "bin/tool.", "bin/tool ", "../x", "a/../../x", "C:/x", "a\\b"} {
		if safeRelative(path) {
			t.Errorf("unsafe portable path %q", path)
		}
	}
	for _, path := range []string{"bin/tool", "./bin/tool", "node-v1/bin/node"} {
		if !safeRelative(path) {
			t.Errorf("valid path rejected %q", path)
		}
	}
}
func TestNPMRejectsEscapingBinLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "tool")); err != nil {
		t.Skip(err)
	}
	if err := rejectExternalLinks(root); err == nil {
		t.Fatal("escaping link accepted")
	}
}

func TestSuppliedHTTPClientRejectsDowngrade(t *testing.T) {
	reached := false
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; _, _ = w.Write([]byte("unsafe")) }))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, plain.URL, http.StatusFound) }))
	defer tls.Close()
	client := tls.Client()
	installer := Installer{Client: client}
	var body bytes.Buffer
	if err := installer.download(context.Background(), tls.URL, &body, 100); err == nil {
		t.Fatal("accepted HTTPS downgrade")
	}
	if reached {
		t.Fatal("contacted insecure redirect destination")
	}
	if client.CheckRedirect != nil {
		t.Fatal("mutated shared client")
	}
}
