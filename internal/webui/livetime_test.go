package webui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestRelativeTimesStayLive: relative times ("5 min ago") are rendered through
// agoHtml()/tAgo() (full date in the tooltip, refreshed by the shared ticker) or
// carry data-ago themselves; a bare esc(ago(...)) would freeze on screen.
func TestRelativeTimesStayLive(t *testing.T) {
	src, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, "function agoHtml") || strings.Contains(line, "if (!full) return esc(ago(") {
			continue
		}
		for _, m := range regexp.MustCompile(`esc\(ago\(`).FindAllStringIndex(line, -1) {
			if !strings.Contains(line[:m[0]], "data-ago=") {
				t.Errorf("app.js:%d: relative time without data-ago (use agoHtml/tAgo)", i+1)
			}
		}
	}
	for _, want := range []string{"setInterval(function () { if (!document.hidden) tickAgo(); }", "setDocTitle({ page:", `data-tip="' + esc(full)`} {
		if !strings.Contains(string(src), want) {
			t.Errorf("app.js lost %q", want)
		}
	}
}
