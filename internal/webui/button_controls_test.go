package webui

import (
	"os/exec"
	"strings"
	"testing"
)

func TestButtonControlsClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	if out, err := exec.Command(node, "testdata/button-controls.cjs").CombinedOutput(); err != nil {
		t.Fatalf("button controls: %v\n%s", err, out)
	}
}

// Main actions have one density, while compact card/table actions retain theirs.
func TestButtonActionLayout(t *testing.T) {
	css, err := Static.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{
		".page-head .actions > .btn, .modal > .actions > .btn, .modal form > .actions > .btn",
		"height: var(--ctl-h); padding: 0 14px; gap: 6px; font-size: 13px;",
		".btn > .i { flex: none; }",
		".btn:focus-visible, .icon-btn:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }",
		"white-space: normal; overflow-wrap: anywhere; text-align: center;",
	} {
		if !strings.Contains(string(css), rule) {
			t.Errorf("missing shared button treatment %q", rule)
		}
	}
}
