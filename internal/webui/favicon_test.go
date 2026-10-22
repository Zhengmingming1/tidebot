package webui

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestFaviconBadge: the tab icon badge picks failed > alert > running, caps the count
// at "9+", and the background poll only runs in a hidden tab while something runs.
func TestFaviconBadge(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n") // Windows checkouts may use CRLF
	for _, want := range []string{
		"paintFavicon();\n  }\n  // ---------- favicon badge", // setDocTitle repaints it
		"if (document.hidden && !docTitle.running) return;",   // idle hidden tab: no requests
		`.then(noteNotifs)`,               // topbar + poll share one path
		"fav.failed = 0; paintFavicon();", // mark all read / reset clears it
		`if (key === fav.key) return;`,    // redraw only on change
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	fn := regexp.MustCompile(`(?s)function favBadge\(o\) \{.*?\n  \}\n`).FindString(src)
	if fn == "" {
		t.Fatal("favBadge not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := fn + `
var cases = [
  [{failed: 0, alert: false, running: 0}, "none"],
  [{failed: 0, alert: false, running: 2}, "running"],
  [{failed: 0, alert: true, running: 2}, "alert"],
  [{failed: 3, alert: true, running: 2}, "failed3"],
  [{failed: 12, alert: false, running: 0}, "failed9+"],
];
cases.forEach(function (c) {
  var b = favBadge(c[0]), got = b ? b.kind + b.text : "none";
  if (got !== c[1]) { console.error(JSON.stringify(c[0]) + " -> " + got + ", want " + c[1]); process.exitCode = 1; }
});
`
	cmd := exec.Command(node, "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("favBadge: %v\n%s", err, out)
	}
}
