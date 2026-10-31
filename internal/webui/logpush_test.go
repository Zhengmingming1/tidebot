package webui

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// openStream starts GET /api/runs/<id>/events on a real listener and returns a line reader.
func openStream(t *testing.T, c *client, id string) (*bufio.Reader, context.CancelFunc) {
	t.Helper()
	ts := httptest.NewServer(c.s.routes())
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/runs/"+id+"/events", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.sid})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		cancel()
		t.Fatalf("status %d", res.StatusCode)
	}
	t.Cleanup(func() { res.Body.Close() })
	return bufio.NewReader(res.Body), cancel
}

// waitData reads SSE lines until one contains want, or fails after d.
func waitData(t *testing.T, br *bufio.Reader, want string, d time.Duration) {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		var seen strings.Builder
		for {
			l, err := br.ReadString('\n')
			seen.WriteString(l)
			if strings.Contains(l, want) {
				got <- ""
				return
			}
			if err != nil {
				got <- seen.String()
				return
			}
		}
	}()
	select {
	case s := <-got:
		if s != "" {
			t.Fatalf("stream ended before %q: %q", want, s)
		}
	case <-time.After(d):
		t.Fatalf("%q not received within %v", want, d)
	}
}

func appendLog(t *testing.T, w interface{ Write([]byte) (int, error) }, s string) {
	t.Helper()
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

// With LogWait the live log is pushed as soon as the run writes, even though the
// fallback poll is an hour away, and the stream leaves no bell behind.
func TestLiveLogPushedOnWrite(t *testing.T) {
	c := login(t)
	rn := runner.New(nil, nil, 1)
	p := filepath.Join(t.TempDir(), "live.log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := rn.LogWriter("lv1", f)
	appendLog(t, w, "[10:00:00] run lv1 started\n")
	c.s.deps.LogPath = func(string) string { return p }
	c.s.deps.LogWait = rn.LogWait
	c.s.logPoll = time.Hour
	c.s.logGap = 10 * time.Millisecond
	seedRun(t, c, "lv1", "x", "running", 0, time.Now())

	br, cancel := openStream(t, c, "lv1")
	defer cancel()
	waitData(t, br, "run lv1 started", 2*time.Second)

	for i := 0; i < 3; i++ {
		appendLog(t, w, "[10:00:01] step "+string(rune('a'+i))+"\n")
	}
	waitData(t, br, "step c", 2*time.Second)

	// A partial line waits for its newline.
	appendLog(t, w, "[10:00:02] half")
	appendLog(t, w, " done\r\n")
	waitData(t, br, "half done", 2*time.Second)

	// The end marker (CRLF tolerated) closes the stream.
	appendLog(t, w, "[[end]]\r\n")
	waitData(t, br, "event: end", 2*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for rn.LogWatchers() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := rn.LogWatchers(); n != 0 {
		t.Fatalf("bells left after the stream ended: %d", n)
	}
}

// Closing the page (client disconnect) drops the wait too.
func TestLiveLogDisconnectReleases(t *testing.T) {
	c := login(t)
	rn := runner.New(nil, nil, 1)
	p := filepath.Join(t.TempDir(), "live.log")
	os.WriteFile(p, []byte("hello\n"), 0o600)
	c.s.deps.LogPath = func(string) string { return p }
	c.s.deps.LogWait = rn.LogWait
	c.s.logPoll = time.Hour
	seedRun(t, c, "lv2", "x", "running", 0, time.Now())
	br, cancel := openStream(t, c, "lv2")
	waitData(t, br, "hello", 2*time.Second)
	// the stream sends what's already in the log before it takes the bell
	for wait := time.Now().Add(2 * time.Second); rn.LogWatchers() != 1 && time.Now().Before(wait); {
		time.Sleep(5 * time.Millisecond)
	}
	if rn.LogWatchers() != 1 {
		t.Fatalf("watchers while open: %d", rn.LogWatchers())
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for rn.LogWatchers() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := rn.LogWatchers(); n != 0 {
		t.Fatalf("bell left after disconnect: %d", n)
	}
}

// Without LogWait (or for writes from elsewhere) the stream still polls the file.
func TestLiveLogFallbackPoll(t *testing.T) {
	c := login(t)
	p := filepath.Join(t.TempDir(), "live.log")
	os.WriteFile(p, []byte("first\n"), 0o600)
	c.s.deps.LogPath = func(string) string { return p }
	seedRun(t, c, "lv3", "x", "running", 0, time.Now())
	br, cancel := openStream(t, c, "lv3")
	defer cancel()
	waitData(t, br, "first", 2*time.Second)
	f, _ := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
	f.WriteString("second\n")
	f.Close()
	waitData(t, br, "second", 3*time.Second)
	if _, err := c.s.deps.DB.Exec(`UPDATE runs SET status='succeeded' WHERE id='lv3'`); err != nil {
		t.Fatal(err)
	}
	waitData(t, br, "event: end", 3*time.Second)
}
