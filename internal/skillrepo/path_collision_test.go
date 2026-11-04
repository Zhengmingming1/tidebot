package skillrepo

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSummarizeRejectsPathComponentCollisions(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{"Unicode sigma directory", []string{"Σ/one.md", "ς/two.md"}, "path_collision"},
		{"Unicode long s file", []string{"S.md", "ſ.md"}, "path_collision"},
		{"directory case", []string{"Refs/one.md", "refs/two.md"}, "path_collision"},
		{"nested directory case", []string{"refs/API/one.md", "refs/api/two.md"}, "path_collision"},
		{"file before directory", []string{"Refs", "refs/one.md"}, "path_collision"},
		{"directory before file", []string{"refs/one.md", "Refs"}, "path_collision"},
		{"exact file directory", []string{"refs", "refs/one.md"}, "path_collision"},
		{"file case", []string{"refs/A.md", "refs/a.md"}, "path_collision"},
		{"shared directory", []string{"refs/one.md", "refs/two.md"}, ""},
		{"separate directories", []string{"refs/one.md", "scripts/one.md"}, ""},
		{"prefix is not component", []string{"ref", "refs/one.md"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				entries := []entry{{Path: "skill/SKILL.md", Mode: "100644", Kind: "blob", OID: strings.Repeat("a", 40), Size: 1}}
				for i := range tt.paths {
					j := i
					if reverse {
						j = len(tt.paths) - 1 - i
					}
					entries = append(entries, entry{Path: "skill/" + tt.paths[j], Mode: "100644", Kind: "blob", OID: strings.Repeat("b", 40), Size: 1})
				}
				got := summarize(entries, "skill")
				if got.Error != tt.want {
					t.Errorf("reverse=%v: error=%q, want %q", reverse, got.Error, tt.want)
				}
			}
		})
	}
}

// Both discovery and reading must reject a conflicting immutable tree before
// fetching any contents, even if a caller bypasses the discovery result.
func TestSnapshotRejectsPathComponentCollisions(t *testing.T) {
	for _, paths := range [][]string{{"Refs/one.md", "refs/two.md"}, {"Refs", "refs/one.md"}, {"Σ/one.md", "ς/two.md"}} {
		t.Run(strings.Join(paths, "+"), func(t *testing.T) {
			oid := strings.Repeat("a", 40)
			tree := "100644 blob " + oid + " 1\tskill/SKILL.md\x00"
			for _, p := range paths {
				tree += "100644 blob " + oid + " 1\tskill/" + p + "\x00"
			}
			c := New(t.TempDir())
			c.runOverride = func(_ context.Context, _, _ string, _ int, _ []byte, args ...string) ([]byte, error) {
				switch args[0] {
				case "init", "fetch", "update-ref":
					return nil, nil
				case "rev-parse":
					return []byte(oid + "\n"), nil
				case "ls-tree":
					return []byte(tree), nil
				default:
					t.Fatalf("unexpected Git command (must not read rejected blobs): %v", args)
					return nil, nil
				}
			}
			snapshot, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "main", ".")
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Candidates) != 1 || snapshot.Candidates[0].Error != "path_collision" {
				t.Fatalf("candidates: %+v", snapshot.Candidates)
			}
			files, err := c.ReadSnapshot(context.Background(), "repo1", snapshot.Commit, "skill")
			var gitErr *Error
			if !errors.As(err, &gitErr) || gitErr.Code != "path_collision" || files != nil {
				t.Fatalf("files=%v, error=%v", files, err)
			}
		})
	}
}
