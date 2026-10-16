package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func tgz(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, b := range files {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(b)), Typeflag: tar.TypeReg})
		tw.Write([]byte(b))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

type fixture struct {
	srv           *httptest.Server
	pub           string
	manifest, sig []byte
	asset         []byte
}

func newFixture(t *testing.T, mutate func(*Manifest)) *fixture {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := &fixture{pub: base64.StdEncoding.EncodeToString(pub)}
	f.asset = tgz(t, map[string]string{"tidebot/agentd": "NEW-agentd", "tidebot/agentctl": "NEW-agentctl"})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			w.Write(f.manifest)
		case "/latest.json.sig":
			w.Write(f.sig)
		case "/asset.tar.gz":
			w.Write(f.asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	h := sha256.Sum256(f.asset)
	m := Manifest{Version: "0.2.0", Date: "2026-10-16", Channel: "stable", Notes: "- faster",
		Assets: map[string]Asset{"server-linux-amd64": {URL: f.srv.URL + "/asset.tar.gz", SHA256: hex.EncodeToString(h[:]), Kind: "server-tar"}}}
	if mutate != nil {
		mutate(&m)
	}
	f.manifest, _ = json.Marshal(m)
	f.sig = []byte(Sign(priv, f.manifest))
	return f
}

func TestSignatureTamperRejected(t *testing.T) {
	f := newFixture(t, nil)
	if _, err := Fetch(context.Background(), Client(), f.srv.URL, f.pub); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	f.manifest = bytes.Replace(f.manifest, []byte("0.2.0"), []byte("9.9.9"), 1)
	if _, err := Fetch(context.Background(), Client(), f.srv.URL, f.pub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered manifest: got %v", err)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	f2 := newFixture(t, nil)
	if _, err := Fetch(context.Background(), Client(), f2.srv.URL, base64.StdEncoding.EncodeToString(other)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("wrong key: got %v", err)
	}
}

func setupInstall(t *testing.T) (dir string, in Install) {
	dir = t.TempDir()
	os.WriteFile(filepath.Join(dir, "agentd"), []byte("OLD-agentd"), 0o755)
	os.WriteFile(filepath.Join(dir, "agentctl"), []byte("OLD-agentctl"), 0o755)
	return dir, Install{Product: "server", Kind: "tar", Mode: ModeApply, Target: filepath.Join(dir, "agentd")}
}

func read(t *testing.T, p string) string { b, _ := os.ReadFile(p); return string(b) }

func TestChecksumMismatchLeavesFilesAlone(t *testing.T) {
	f := newFixture(t, func(m *Manifest) {
		a := m.Assets["server-linux-amd64"]
		a.SHA256 = "00" + a.SHA256[2:]
		m.Assets["server-linux-amd64"] = a
	})
	m, err := Fetch(context.Background(), Client(), f.srv.URL, f.pub)
	if err != nil {
		t.Fatal(err)
	}
	dir, in := setupInstall(t)
	c := Evaluate(m, "0.1.2", in, "linux", "amd64")
	if !c.CanApply {
		t.Fatal("expected applicable")
	}
	if _, err := Apply(context.Background(), Client(), c, dir); !errors.Is(err, ErrChecksum) {
		t.Fatalf("got %v", err)
	}
	if read(t, filepath.Join(dir, "agentd")) != "OLD-agentd" {
		t.Fatal("binary changed despite bad checksum")
	}
	if _, err := os.Stat(filepath.Join(dir, "agentd.prev")); err == nil {
		t.Fatal("backup created on failed update")
	}
}

func TestApplyConfirmAndRollback(t *testing.T) {
	f := newFixture(t, nil)
	m, _ := Fetch(context.Background(), Client(), f.srv.URL, f.pub)
	dir, in := setupInstall(t)
	state := t.TempDir()
	c := Evaluate(m, "0.1.2", in, "linux", "amd64")
	if _, err := Apply(context.Background(), Client(), c, state); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "agentd")) != "NEW-agentd" || read(t, filepath.Join(dir, "agentd.prev")) != "OLD-agentd" {
		t.Fatal("swap wrong")
	}
	// New build fails to come up MaxAttempts times → next boot rolls back.
	for i := 0; i < MaxAttempts; i++ {
		if bs := Boot(state); bs.Pending == nil {
			t.Fatalf("attempt %d: no pending", i)
		}
	}
	if bs := Boot(state); !bs.RolledBack {
		t.Fatal("expected rollback")
	}
	if read(t, filepath.Join(dir, "agentd")) != "OLD-agentd" || read(t, filepath.Join(dir, "agentctl")) != "OLD-agentctl" {
		t.Fatal("rollback did not restore")
	}
	if r := LastResult(state); r == nil || r.Status != "rolled_back" {
		t.Fatalf("result %+v", r)
	}

	// Happy path: apply, boot once, confirm → keeps exactly one backup.
	if _, err := Apply(context.Background(), Client(), c, state); err != nil {
		t.Fatal(err)
	}
	Boot(state)
	if r := Confirm(state); r == nil || r.Status != "ok" {
		t.Fatal("confirm")
	}
	if Boot(state).Pending != nil {
		t.Fatal("marker should be gone")
	}
	if read(t, filepath.Join(dir, "agentd.prev")) != "OLD-agentd" {
		t.Fatal("one backup should remain")
	}
	// In-process health failure.
	os.WriteFile(filepath.Join(dir, "agentd"), []byte("OLD-agentd"), 0o755)
	Apply(context.Background(), Client(), c, state)
	if err := RollbackNow(state, "health check failed"); err != nil || read(t, filepath.Join(dir, "agentd")) != "OLD-agentd" {
		t.Fatalf("rollback now: %v", err)
	}
}

func TestEvaluateAndDetect(t *testing.T) {
	m := &Manifest{Version: "v0.1.10", Assets: map[string]Asset{"server-linux-amd64": {}}}
	if !Evaluate(m, "0.1.9", Install{Product: "server", Mode: ModeApply}, "linux", "amd64").CanApply {
		t.Fatal("0.1.10 > 0.1.9")
	}
	if Evaluate(m, "0.1.10", Install{Product: "server", Mode: ModeApply}, "linux", "amd64").Available {
		t.Fatal("same version")
	}
	if Evaluate(m, "0.1.0", Install{Product: "server", Mode: ModeNotify}, "linux", "amd64").CanApply {
		t.Fatal("notify mode must not apply")
	}
	t.Setenv("TIDEBOT_MANAGED", "1")
	if Detect("server", "/opt/x/agentd").Mode != ModeNotify {
		t.Fatal("managed should notify")
	}
	t.Setenv("TIDEBOT_MANAGED", "")
	if Detect("server", "/usr/bin/agentd").Mode != ModeNotify {
		t.Fatal("package should notify")
	}
}
