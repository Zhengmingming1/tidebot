package updater

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// silentListener accepts connections and never answers, like a proxy node that
// took the CONNECT and went quiet. Transport timeouts don't fire for it.
func silentListener(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	return "http://" + ln.Addr().String()
}

func TestFetchFromStalledSourceTimesOutPerAttempt(t *testing.T) {
	old := AttemptTimeout
	AttemptTimeout = 300 * time.Millisecond
	t.Cleanup(func() { AttemptTimeout = old })
	good := newFixture(t, nil)
	srcs := []Source{{Name: "stalled", Manifest: silentListener(t)}, {Name: "good", Manifest: good.srv.URL}}
	start := time.Now()
	m, used, err := FetchFrom(context.Background(), Client(), srcs, good.pub)
	if err != nil || used != 1 || m.Version != "0.2.0" {
		t.Fatal(used, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %s; the stalled source should give up after AttemptTimeout", d)
	}
	_, _, err = FetchFrom(context.Background(), Client(), srcs[:1], good.pub)
	var fe *FetchError
	if !errors.As(err, &fe) || fe.Code() != "timeout" {
		t.Fatalf("want a classified timeout, got %v", err)
	}
}

func TestApplyReportsProgressThenInstall(t *testing.T) {
	f := newFixture(t, nil)
	m, err := Fetch(context.Background(), Client(), f.srv.URL, f.pub)
	if err != nil {
		t.Fatal(err)
	}
	dir, in := setupInstall(t)
	c := Evaluate(m, "0.1.2", in, "linux", "amd64")
	var last, total int64
	installed := false
	_, err = ApplyWithProgress(context.Background(), Client(), c, t.TempDir(), nil, Progress{
		Bytes: func(d, tot int64) {
			last, total = d, tot
			if installed {
				t.Error("bytes after install")
			}
		},
		Install: func() { installed = true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !installed || last != int64(len(f.asset)) || total != int64(len(f.asset)) {
		t.Fatalf("installed=%v progress=%d/%d want %d", installed, last, total, len(f.asset))
	}
	if read(t, filepath.Join(dir, "agentd")) != "NEW-agentd" {
		t.Fatal("not swapped")
	}
}

func TestDownloadStallAborts(t *testing.T) {
	old := IdleTimeout
	IdleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { IdleTimeout = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.Write(make([]byte, 1000))
		w.(http.Flusher).Flush()
		select { // then nothing: a stalled transfer
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()
	start := time.Now()
	_, err := download(context.Background(), Client(), srv.URL, 1<<20, 0, nil)
	if !errors.Is(err, ErrStalled) || Classify(err) != "timeout" {
		t.Fatalf("want stalled, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("stall watchdog too slow")
	}
}
