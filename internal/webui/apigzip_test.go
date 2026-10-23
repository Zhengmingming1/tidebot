package webui

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

func apiGet(c *client, h http.Handler, path, enc string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	if enc != "" {
		req.Header.Set("Accept-Encoding", enc)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Large JSON answers are gzipped for clients that accept it; the bytes decode to exactly
// what an identity client gets. Small answers and refusing clients get identity.
func TestAPIJSONGzip(t *testing.T) {
	c := login(t)
	now := time.Now()
	for i := 0; i < 60; i++ {
		seedRun(t, c, fmt.Sprintf("z%02d", i), strings.Repeat("summarize the news ", 4), "succeeded", 0.01, now.Add(time.Duration(i)*time.Minute))
	}
	h := c.s.routes()

	plain := apiGet(c, h, "/api/runs", "")
	if plain.Code != 200 || plain.Header().Get("Content-Encoding") != "" {
		t.Fatalf("identity: %d %q", plain.Code, plain.Header().Get("Content-Encoding"))
	}
	if cl := plain.Header().Get("Content-Length"); cl != strconv.Itoa(plain.Body.Len()) {
		t.Fatalf("identity Content-Length %q for %d bytes", cl, plain.Body.Len())
	}
	if plain.Body.Len() < gzipMinJSON {
		t.Fatalf("seed too small: %d", plain.Body.Len())
	}

	gz := apiGet(c, h, "/api/runs", "gzip, deflate, br")
	if gz.Code != 200 || gz.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip: %d %q", gz.Code, gz.Header().Get("Content-Encoding"))
	}
	if gz.Header().Get("Content-Length") != "" {
		t.Fatal("gzip answer must not carry the identity Content-Length")
	}
	if !strings.Contains(gz.Header().Get("Vary"), "Accept-Encoding") || !strings.Contains(plain.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatal("Vary: Accept-Encoding missing")
	}
	if !strings.HasPrefix(gz.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("type %q", gz.Header().Get("Content-Type"))
	}
	if got := gunzip(t, gz.Body.Bytes()); !bytes.Equal(got, plain.Body.Bytes()) {
		t.Fatal("gzip body differs from identity body")
	}
	if gz.Body.Len()*3 > plain.Body.Len() {
		t.Fatalf("expected at least 3x smaller: %d -> %d", plain.Body.Len(), gz.Body.Len())
	}

	// q=0 refuses gzip
	if r := apiGet(c, h, "/api/runs", "gzip;q=0, identity"); r.Header().Get("Content-Encoding") != "" || !bytes.Equal(r.Body.Bytes(), plain.Body.Bytes()) {
		t.Fatal("gzip;q=0 must get identity")
	}
	// a small answer stays identity even when gzip is accepted
	small := apiGet(c, h, "/api/me", "gzip")
	if small.Code != 200 || small.Header().Get("Content-Encoding") != "" || small.Body.Len() >= gzipMinJSON {
		t.Fatalf("small answer: %d %q %d bytes", small.Code, small.Header().Get("Content-Encoding"), small.Body.Len())
	}
	// errors are JSON too and stay readable
	if r := apiGet(c, h, "/api/runs?q="+strings.Repeat("a", 101), "gzip"); r.Code != 400 || r.Header().Get("Content-Encoding") != "" || !strings.Contains(r.Body.String(), "error") {
		t.Fatalf("error answer: %d %q", r.Code, r.Body.String())
	}
	// non-API pages are untouched (index has its own no-store handling)
	if r := apiGet(c, h, "/", "gzip"); r.Header().Get("Content-Encoding") != "" || strings.Contains(r.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("index changed: %q %q", r.Header().Get("Content-Encoding"), r.Header().Get("Vary"))
	}
	// the auth layer's 401 is not affected
	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 401 || rr.Header().Get("Content-Encoding") != "" {
		t.Fatalf("unauthenticated: %d %q", rr.Code, rr.Header().Get("Content-Encoding"))
	}
}

// The desktop window's webview gets identity bytes for API answers too.
func TestDesktopAPIIdentity(t *testing.T) {
	c := login(t)
	for i := 0; i < 40; i++ {
		seedRun(t, c, fmt.Sprintf("d%02d", i), strings.Repeat("p", 80), "succeeded", 0.01, time.Now())
	}
	h, err := c.s.DesktopHandler(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 || rr.Header().Get("Content-Encoding") != "" || rr.Body.Len() < gzipMinJSON || rr.Body.Bytes()[0] != '[' {
		t.Fatalf("desktop: %d %q %d", rr.Code, rr.Header().Get("Content-Encoding"), rr.Body.Len())
	}
}

// The live log (SSE) still streams uncompressed and flushes per event for gzip clients.
func TestSSENotGzipped(t *testing.T) {
	c := login(t)
	p := filepath.Join(t.TempDir(), "s.log")
	if err := os.WriteFile(p, []byte("[10:00:00] hello sse\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.s.deps.LogPath = func(string) string { return p }
	c.s.logPoll = 20 * time.Millisecond
	seedRun(t, c, "ss1", "x", "running", 0, time.Now())
	ts := httptest.NewServer(c.s.routes())
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/runs/ss1/events", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	req.Header.Set("Accept-Encoding", "gzip") // set by hand: the transport won't decode for us
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Encoding") != "" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("sse: %d %q %q", res.StatusCode, res.Header.Get("Content-Encoding"), res.Header.Get("Content-Type"))
	}
	waitData(t, bufio.NewReader(res.Body), "hello sse", 2*time.Second)
}

// writeJSON sets no body length for 204 and keeps buffers bounded.
func TestWriteJSONStatusAndPool(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(&apiGzip{rr}, http.StatusNoContent, map[string]string{"a": strings.Repeat("x", 4096)})
	if rr.Header().Get("Content-Encoding") != "" || rr.Header().Get("Content-Length") != "" {
		t.Fatalf("204: %v", rr.Header())
	}
	big := strings.Repeat("y", maxPooledBuf+10)
	rr = httptest.NewRecorder()
	writeJSON(&apiGzip{rr}, 200, map[string]string{"b": big})
	if rr.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(string(gunzip(t, rr.Body.Bytes())), big) {
		t.Fatal("big answer not gzipped correctly")
	}
	// the pool hands back only bounded buffers
	for i := 0; i < 4; i++ {
		if b := jsonBufPool.Get().(*bytes.Buffer); b.Cap() > maxPooledBuf {
			t.Fatalf("oversized buffer pooled: %d", b.Cap())
		}
	}
	// no-transform is respected
	rr = httptest.NewRecorder()
	rr.Header().Set("Cache-Control", "no-store, no-transform")
	writeJSON(&apiGzip{rr}, 200, map[string]string{"c": strings.Repeat("z", 4096)})
	if rr.Header().Get("Content-Encoding") != "" {
		t.Fatal("no-transform ignored")
	}
}
