package sessiontools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLifecycleToolSurfaceOnlyDrafts(t *testing.T) {
	found := false
	for _, tool := range Remote {
		raw, _ := json.Marshal(tool)
		text := string(raw)
		if strings.Contains(text, `"cap_propose"`) {
			found = true
			for _, action := range []string{"install | update | uninstall | skill_optimize", "Never executes"} {
				if !strings.Contains(text, action) {
					t.Fatalf("missing lifecycle safety description %q", action)
				}
			}
		}
	}
	if !found {
		t.Fatal("proposal tool missing")
	}
}
