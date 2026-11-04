package webui

import (
	"os/exec"
	"testing"
)

func TestCapabilityGrantsClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "testdata/capability-grants.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("capability grants: %v\n%s", err, out)
	}
}
