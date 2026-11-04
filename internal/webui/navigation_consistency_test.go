package webui

import (
	"os/exec"
	"testing"
)

func TestNavigationConsistencyClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "testdata/navigation-consistency.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("navigation consistency: %v\n%s", err, out)
	}
}
