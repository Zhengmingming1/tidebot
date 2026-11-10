package guard

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAgentConfigIntegrity(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	parent := filepath.Join(root, "code")
	work := filepath.Join(parent, "proj")
	own := filepath.Join(root, "tidebot")
	write(t, filepath.Join(home, ".claude", "settings.json"), `{}`)
	write(t, filepath.Join(home, ".claude", "skills", "x", "SKILL.md"), "x")
	write(t, filepath.Join(home, ".claude.json"), `{}`)
	write(t, filepath.Join(work, ".mcp.json"), `{"mcpServers":{}}`)
	write(t, filepath.Join(work, "CLAUDE.md"), "# p")
	write(t, filepath.Join(work, "README.md"), "r")
	write(t, filepath.Join(parent, "CLAUDE.md"), "# parent")
	write(t, filepath.Join(own, ".claude", "skills", "s", "SKILL.md"), "s")

	before := SnapshotAgentConfig(home, []string{work, own}, []string{own})
	if len(before.Diff(SnapshotAgentConfig(home, []string{work, own}, []string{own}))) != 0 {
		t.Fatal("no change must diff empty")
	}
	write(t, filepath.Join(work, ".mcp.json"), `{"mcpServers":{"x":{}}}`)    // modified
	write(t, filepath.Join(work, ".claude", "settings.local.json"), `{}`)    // added
	_ = os.Remove(filepath.Join(work, "CLAUDE.md"))                          // removed
	write(t, filepath.Join(parent, "CLAUDE.md"), "# parent, changed")        // a parent's file
	write(t, filepath.Join(home, ".claude", "skills", "x", "SKILL.md"), "y") // the owner's skill
	write(t, filepath.Join(home, ".claude.json"), `{"n":1}`)                 // not watched: Claude Code rewrites it
	write(t, filepath.Join(work, "README.md"), "changed")                    // not agent config
	write(t, filepath.Join(own, ".claude", "skills", "s", "SKILL.md"), "t")  // tidebot's own stage
	got := before.Diff(SnapshotAgentConfig(home, []string{work, own}, []string{own}))
	want := []ConfigChange{
		{filepath.Join(parent, "CLAUDE.md"), "modified"},
		{filepath.Join(work, ".claude", "settings.local.json"), "added"},
		{filepath.Join(work, ".mcp.json"), "modified"},
		{filepath.Join(work, "CLAUDE.md"), "removed"},
		{filepath.Join(home, ".claude", "skills", "x", "SKILL.md"), "modified"},
	}
	sortChanges(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func sortChanges(c []ConfigChange) {
	for i := range c {
		for j := i + 1; j < len(c); j++ {
			if c[j].Path < c[i].Path {
				c[i], c[j] = c[j], c[i]
			}
		}
	}
}
