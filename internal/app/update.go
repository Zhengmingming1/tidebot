package app

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
)

func execOutput(ctx context.Context, bin string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	return string(out), err
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// UpdateInfo describes the latest release relative to this build.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Asset     string `json:"asset"`
	URL       string `json:"url"`
	Notes     string `json:"notes"`
	assetURL  string
	sumsURL   string
}

// ServerAssetName is the release asset for the server binaries on this platform.
func ServerAssetName(ver, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("tidebot-server_%s_%s_%s%s", strings.TrimPrefix(ver, "v"), goos, goarch, ext)
}

// UpdateAPIBase can be overridden in tests.
var UpdateAPIBase = "https://api.github.com"

// CheckUpdate asks GitHub for the latest release.
func CheckUpdate(ctx context.Context, hc *http.Client) (*UpdateInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, UpdateAPIBase+"/repos/"+buildinfo.Repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github: %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	info := &UpdateInfo{Current: buildinfo.Version, Latest: strings.TrimPrefix(rel.TagName, "v"), URL: rel.HTMLURL, Notes: rel.Body}
	info.Asset = ServerAssetName(info.Latest, runtime.GOOS, runtime.GOARCH)
	for _, a := range rel.Assets {
		switch a.Name {
		case info.Asset:
			info.assetURL = a.URL
		case "checksums.txt":
			info.sumsURL = a.URL
		}
	}
	info.Available = buildinfo.Version != "dev" && compareSemver(info.Latest, buildinfo.Version) > 0 && info.assetURL != ""
	return info, nil
}

// ApplyUpdate downloads the server archive, verifies its sha256 against checksums.txt,
// and atomically replaces agentd/agentctl next to the running binary, keeping *.prev.
// The caller restarts the service afterwards.
func ApplyUpdate(ctx context.Context, hc *http.Client, dir string) (map[string]any, error) {
	info, err := CheckUpdate(ctx, hc)
	if err != nil {
		return nil, err
	}
	if !info.Available {
		return map[string]any{"updated": false, "latest": info.Latest}, nil
	}
	if info.sumsURL == "" {
		return nil, errors.New("release has no checksums.txt; refusing to update")
	}
	sums, err := download(ctx, hc, info.sumsURL, 1<<20)
	if err != nil {
		return nil, err
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == info.Asset {
			want = f[0]
		}
	}
	if want == "" {
		return nil, errors.New("asset missing from checksums.txt")
	}
	archive, err := download(ctx, hc, info.assetURL, 200<<20)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(archive)
	if hex.EncodeToString(h[:]) != strings.ToLower(want) {
		return nil, errors.New("checksum mismatch; update aborted")
	}
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		dir = filepath.Dir(exe)
	}
	bins, err := extractBinaries(info.Asset, archive)
	if err != nil {
		return nil, err
	}
	var replaced []string
	for name, data := range bins {
		if err := replaceFile(filepath.Join(dir, name), data); err != nil {
			return nil, fmt.Errorf("replace %s: %w", name, err)
		}
		replaced = append(replaced, name)
	}
	return map[string]any{"updated": true, "from": info.Current, "to": info.Latest, "replaced": replaced, "restart_required": true}, nil
}

func download(ctx context.Context, hc *http.Client, url string, max int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

var updatable = map[string]bool{"agentd": true, "agentctl": true, "agentd.exe": true, "agentctl.exe": true}

func extractBinaries(name string, archive []byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if base := filepath.Base(f.Name); updatable[base] {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				b, err := io.ReadAll(io.LimitReader(rc, 200<<20))
				rc.Close()
				if err != nil {
					return nil, err
				}
				out[base] = b
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(gz)
		for {
			hd, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if base := filepath.Base(hd.Name); hd.Typeflag == tar.TypeReg && updatable[base] {
				b, err := io.ReadAll(io.LimitReader(tr, 200<<20))
				if err != nil {
					return nil, err
				}
				out[base] = b
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("archive has no binaries")
	}
	return out, nil
}

// replaceFile writes data next to dst, keeps the old file as dst.prev, then renames.
func replaceFile(dst string, data []byte) error {
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(dst); err == nil {
		_ = os.Remove(dst + ".prev")
		if err := os.Rename(dst, dst+".prev"); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, dst)
}
