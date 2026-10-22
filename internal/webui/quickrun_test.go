package webui

import (
	"os"
	"strings"
	"testing"
)

// TestQuickRunMemory: the quick-run dialog offers recent prompts (escaped), keeps only the
// provider name in localStorage, and Ctrl/⌘+Enter submits.
func TestQuickRunMemory(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`api("/api/runs/recent-prompts")`,
		`localStorage.setItem("tb_qr_provider", f.provider.value)`,
		`if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); form.requestSubmit(); }`,
		`'">' + esc(p.length > 36 ? p.slice(0, 36) + "…" : p) + "</button>"`,
		`ta.value = recent[+b.dataset.i]`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
}
