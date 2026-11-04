package webui

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSelectControlsClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "testdata/select-controls.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("select controls: %v\n%s", err, out)
	}
}

func TestCompoundInputFocus(t *testing.T) {
	css, err := Static.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		".search-in input:focus, .search-in input:focus-visible { outline: none; border: 0 !important; box-shadow: none; }",
		".search-in:focus-within { border-color: var(--accent); box-shadow: 0 0 0 3px",
		".select-popup-option.active",
		"@media (forced-colors: active)",
	} {
		if !strings.Contains(string(css), fragment) {
			t.Errorf("missing focus/menu style %q", fragment)
		}
	}
}
