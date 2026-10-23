package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/updater"
)

// The WebUI's "Check now" must answer at once and then show progress: the check
// runs in the background and update.status reports phase / checked_at / error.
func TestUpdateCheckAsyncReportsPhases(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	man, _ := json.Marshal(updater.Manifest{Version: "0.0.1", Date: "2026-10-23", Channel: "stable",
		Assets: map[string]updater.Asset{"server-linux-amd64": {URL: "http://x/a.tgz", SHA256: "00", Kind: "server-tar"}}})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			<-release
			w.Write(man)
		case "/latest.json.sig":
			w.Write([]byte(updater.Sign(priv, man)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("TIDEBOT_UPDATE_URL", srv.URL)
	t.Setenv("TIDEBOT_UPDATE_PUBKEY", base64.StdEncoding.EncodeToString(pub))
	a := newTestApp(t)
	ctx := context.Background()

	start := time.Now()
	out, err := a.CallExt(ctx, "update.check", json.RawMessage(`{"async":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("async check blocked")
	}
	if st := out.(map[string]any); st["phase"] != UpdChecking {
		t.Fatalf("phase %v", st["phase"])
	}
	if err := a.StartUpdateApply(); err == nil {
		t.Fatal("a second job must not start while checking")
	}
	close(release)
	st := waitIdle(t, a)
	if st["checked_at"] == "" || st["error"] != "" || st["last"] == nil {
		t.Fatalf("after check: %+v", st)
	}
}

func TestUpdateCheckStalledSourceErrorsInTime(t *testing.T) {
	old := updater.AttemptTimeout
	updater.AttemptTimeout = 300 * time.Millisecond
	t.Cleanup(func() { updater.AttemptTimeout = old })
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer srv.Close()
	defer close(stop)
	t.Setenv("TIDEBOT_UPDATE_URL", srv.URL)
	a := newTestApp(t)
	a.StartUpdateCheck()
	st := waitIdle(t, a)
	if st["error_code"] != "timeout" || st["checked_at"] == "" {
		t.Fatalf("want a timeout error, got %+v", st)
	}
}

func waitIdle(t *testing.T, a *App) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := a.UpdateStatus(context.Background())
		if st["phase"] == UpdIdle {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("update job never finished")
	return nil
}
