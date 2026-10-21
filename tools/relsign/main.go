// Command relsign builds and signs latest.json for the public releases repo.
//
//	relsign keygen <private-key-file>          writes a new ed25519 key (0600), prints only the public key
//	relsign manifest <tag> <dist-dir> <notes>  writes dist/latest.json + latest.json.sig + SHA256SUMS
//
// The signing key comes from $UPDATE_SIGNING_KEY (base64 of the 64-byte ed25519 private key).
// It is never printed.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/updater"
)

func main() {
	if len(os.Args) < 3 {
		fail("usage: relsign keygen <file> | relsign manifest <tag> <dist> <notes.md>")
	}
	switch os.Args[1] {
	case "keygen":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		check(err)
		check(os.WriteFile(os.Args[2], []byte(base64.StdEncoding.EncodeToString(priv)), 0o600))
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
	case "manifest":
		if len(os.Args) < 5 {
			fail("relsign manifest <tag> <dist> <notes.md>")
		}
		manifest(os.Args[2], os.Args[3], os.Args[4])
	default:
		fail("unknown command")
	}
}

var (
	reServer = regexp.MustCompile(`^tidebot-server_[^_]+_(linux|darwin|windows)_(amd64|arm64)\.(tar\.gz|zip)$`)
	reAppImg = regexp.MustCompile(`^tidebot-desktop-\d[^-]*-Linux-x64\.AppImage$`)
	reMacApp = regexp.MustCompile(`^tidebot-desktop-\d[^-]*-macOS-app\.tar\.gz$`)
	reWinZip = regexp.MustCompile(`^tidebot-desktop-\d[^-]*-Windows-x64\.zip$`)
)

func manifest(tag, dist, notesFile string) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("UPDATE_SIGNING_KEY")))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		fail("UPDATE_SIGNING_KEY is missing or malformed")
	}
	priv := ed25519.PrivateKey(key)
	notes, _ := os.ReadFile(notesFile)
	base := "https://github.com/" + updater.ReleasesRepo + "/releases/download/" + tag + "/"
	if v := os.Getenv("RELSIGN_ASSET_BASE"); v != "" { // local test feeds only (scripts/e2e-update.sh)
		base = strings.TrimRight(v, "/") + "/"
	}
	m := updater.Manifest{Version: strings.TrimPrefix(tag, "v"), Date: time.Now().UTC().Format("2006-01-02"), Channel: "stable",
		Notes: changelog(string(notes)), NotesURL: "https://github.com/" + updater.ReleasesRepo + "/releases/tag/" + tag, Assets: map[string]updater.Asset{}}
	entries, err := os.ReadDir(dist)
	check(err)
	var sums []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == "SHA256SUMS" || n == "checksums.txt" || strings.HasPrefix(n, "latest.json") {
			continue
		}
		sum, size := hashFile(filepath.Join(dist, n))
		sums = append(sums, sum+"  "+n)
		a := updater.Asset{URL: base + n, SHA256: sum, Size: size}
		switch {
		case reServer.MatchString(n):
			g := reServer.FindStringSubmatch(n)
			a.Kind = "server-tar"
			if g[3] == "zip" {
				a.Kind = "server-zip"
			}
			m.Assets[updater.AssetKey("server", g[1], g[2])] = a
		case reAppImg.MatchString(n):
			a.Kind = "appimage"
			m.Assets["desktop-linux-amd64"] = a
		case reMacApp.MatchString(n):
			a.Kind = "app-tar" // universal binary
			m.Assets["desktop-darwin-arm64"] = a
			m.Assets["desktop-darwin-amd64"] = a
		case reWinZip.MatchString(n):
			a.Kind = "desktop-zip"
			m.Assets["desktop-windows-amd64"] = a
		}
	}
	sort.Strings(sums)
	check(os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte(strings.Join(sums, "\n")+"\n"), 0o644))
	body, _ := json.MarshalIndent(m, "", "  ")
	sig := updater.Sign(priv, body)
	pub := priv.Public().(ed25519.PublicKey)
	if err := updater.Verify(base64.StdEncoding.EncodeToString(pub), body, sig); err != nil {
		fail("self-verify failed")
	}
	if updater.PublicKey != base64.StdEncoding.EncodeToString(pub) {
		fail("UPDATE_SIGNING_KEY does not match the public key embedded in internal/updater")
	}
	check(os.WriteFile(filepath.Join(dist, "latest.json"), body, 0o644))
	check(os.WriteFile(filepath.Join(dist, "latest.json.sig"), []byte(sig+"\n"), 0o644))
	fmt.Printf("latest.json: %s, %d assets\n", m.Version, len(m.Assets))
}

// changelog keeps the per-version part of the notes (after the last "---"), not the download table.
func changelog(s string) string {
	if i := strings.LastIndex(s, "\n---\n"); i >= 0 {
		s = s[i+5:]
	}
	if len(s) > 4000 {
		s = s[:4000]
	}
	return strings.TrimSpace(s)
}

func hashFile(p string) (string, int64) {
	f, err := os.Open(p)
	check(err)
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	check(err)
	return hex.EncodeToString(h.Sum(nil)), n
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "relsign:", msg)
	os.Exit(1)
}
