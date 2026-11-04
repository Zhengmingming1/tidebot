package webui

import (
	"os/exec"
	"testing"
)

func TestExtensionsLoadingClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "testdata/extensions-loading.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("extension loading: %v\n%s", err, out)
	}
}
