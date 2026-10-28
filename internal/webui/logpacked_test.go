package webui

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/runner"
)

// A finished run whose log was gzipped streams the same lines (and offsets) as before.
func TestRunEventsPackedLog(t *testing.T) {
	c := login(t)
	p := filepath.Join(t.TempDir(), "pk1.log")
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("[10:00:00] 第 " + strconv.Itoa(i) + " 行：读取文件 notes.md\n")
	}
	b.WriteString("last line without newline\n[[end]]\n")
	os.WriteFile(p, []byte(b.String()), 0o600)
	if _, err := runner.CompressLogFile(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("not compressed")
	}
	c.s.deps.LogPath = func(string) string { return p }
	seedRun(t, c, "pk1", "x", "succeeded", 0, time.Now())
	plain := c.do(http.MethodGet, "/api/runs/pk1/events", "", nil)
	body := plain.Body.String()
	if !strings.Contains(body, "data: [10:00:00] 第 0 行") || !strings.Contains(body, "data: last line without newline") ||
		!strings.Contains(body, "event: end\ndata: succeeded") || strings.Contains(body, "[[end]]") {
		t.Fatalf("stream %q", body[:200])
	}
	// The ids are offsets into the original text, so a reconnect continues from there.
	off := strings.Index(b.String(), "[10:00:00] 第 299 行")
	rec := c.do(http.MethodGet, "/api/runs/pk1/events?from="+strconv.Itoa(off), "", nil)
	if r := rec.Body.String(); strings.Contains(r, "第 298 行") || !strings.Contains(r, "第 299 行") || !strings.Contains(r, "event: end") {
		t.Fatalf("resume %q", r)
	}
	// Same answer as the plain file gave.
	os.WriteFile(p, []byte(b.String()), 0o600)
	os.Remove(p + ".gz")
	if again := c.do(http.MethodGet, "/api/runs/pk1/events", "", nil).Body.String(); again != body {
		t.Fatal("packed stream differs from the plain one")
	}
}

// Settings › Storage says how many logs are compressed (in the tooltip, both languages).
func TestStoragePackedTipUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	if !strings.Contains(string(js), `T("st.packed", { n: x.packed, raw: fmtBytes(x.packed_raw), on: fmtBytes(x.packed_bytes) })`) {
		t.Error("storage tooltip lacks the packed line")
	}
	for _, lang := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + lang + ".json")
		if !strings.Contains(string(b), `"st.packed": "`) || !strings.Contains(string(b), "{raw} → {on}") {
			t.Errorf("%s lacks st.packed", lang)
		}
	}
}
