package official

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// A page whose text is over the 200 KB cut is cut on a character boundary.
// A cut through a Chinese character came back from the saved state (JSON) as
// U+FFFD, so after a restart the unchanged page differed from the saved text:
// for a one-paragraph page that was a 100 % "change" notice.
func TestWebMonitorLongCJKPageSurvivesRestart(t *testing.T) {
	page := `<html><body><div id="t">x` + strings.Repeat("潮", 70000) + `</div></body></html>` // one 210 KB line
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, page)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := map[string]any{"url": srv.URL + "/p", "selector": "#t", "threshold_pct": 1.0}
	st := loadState(dir, "b1")
	if _, err := pollWeb(context.Background(), cfg, st, true); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(st.Text) || len(st.Text) > 200<<10 || len(st.Text) < 200<<10-3 {
		t.Fatalf("cut text: %d bytes, valid=%v", len(st.Text), utf8.ValidString(st.Text))
	}
	st.Seeded = true
	st.save()
	again := loadState(dir, "b1") // what the plugin reads after a restart
	if again.path != filepath.Join(dir, "state-b1.json") || again.Text != st.Text {
		t.Fatalf("saved text differs after reload (%d vs %d bytes)", len(again.Text), len(st.Text))
	}
	if it, err := pollWeb(context.Background(), cfg, again, false); err != nil || it != nil {
		t.Fatalf("unchanged page reported as changed after restart: %v %v", it, err)
	}
}
