package webui

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestEditAndRerun: an ad-hoc run's page offers "edit & rerun" (icon + the e key), which
// opens the quick-run dialog with the run's prompt and provider; "Revert" shows only once
// something changed, and CRLF / trailing whitespace alone isn't a change.
func TestEditAndRerun(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n") // Windows checkouts may use CRLF
	for _, want := range []string{
		`(r.task_id ? "" : '<button type="button" class="icon-btn sm" id="rd-edit"`,
		`quickRunModal({ prompt: r.prompt, provider: r.provider })`,
		`if (pre && pre.provider) lastProv = pre.provider;`,
		`reset.classList.toggle("hidden", !qrEdited(orig, ta.value, form.provider.value))`,
		`e: ["#t-edit", "#rd-edit"]`,
		`if (hit && !hit.disabled)`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	fn := regexp.MustCompile(`(?s)  function qrEdited\(orig, prompt, provider\) \{.*?\n  \}\n`).FindString(src)
	if fn == "" {
		t.Fatal("qrEdited not found")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := fn + `
var o = { prompt: "line 1\nline 2", provider: "kimi" }, bad = [];
if (qrEdited(null, "x", "y")) bad.push("no original must not count as edited");
if (qrEdited(o, "line 1\r\nline 2", "kimi")) bad.push("CRLF counted as a change");
if (qrEdited(o, "line 1\nline 2  \n", "kimi")) bad.push("trailing whitespace counted as a change");
if (!qrEdited(o, "line 1\nline 3", "kimi")) bad.push("prompt change missed");
if (!qrEdited(o, "line 1\nline 2", "deepseek")) bad.push("provider change missed");
if (!qrEdited(o, "  line 1\nline 2", "kimi")) bad.push("leading whitespace is a real change");
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("qrEdited: %v\n%s", err, out)
	}
}
