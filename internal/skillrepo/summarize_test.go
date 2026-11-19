package skillrepo

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
)

func blob(p string, size int64) entry {
	return entry{Path: p, Mode: "100644", Kind: "blob", OID: strings.Repeat("a", 40), Size: size}
}

func tree(p string) entry {
	return entry{Path: p, Mode: "040000", Kind: "tree", OID: strings.Repeat("b", 40)}
}

func mustSummarizeByRoot(t *testing.T, entries []entry, scanRoot string, roots []string) []Candidate {
	t.Helper()
	got, err := summarizeByRoot(context.Background(), entries, scanRoot, roots)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func sameSummary(t *testing.T, got, want Candidate) {
	t.Helper()
	if got.Path != want.Path || got.Files != want.Files || got.Size != want.Size || got.TreeHash != want.TreeHash || got.Error != want.Error {
		t.Fatalf("summary %+v, want %+v", got, want)
	}
}

func TestSummarizeByRootMatchesEachRoot(t *testing.T) {
	entries := []entry{
		tree("skills"),
		tree("skills/s000"),
		blob("skills/s000/SKILL.md", 20),
		blob("skills/s000/a.md", 3),
		tree("skills/s000-extra"),
		blob("skills/s000-extra/SKILL.md", 21),
		blob("skills/s000-extra/b.md", 4),
		tree("nested"),
		blob("nested/SKILL.md", 10),
		blob("nested/parent.md", 5),
		tree("nested/child"),
		blob("nested/child/SKILL.md", 11),
		blob("nested/child/c.md", 6),
		tree("outside"),
		blob("outside/SKILL.md", 7),
		blob("SKILL.md", 2),
		blob("root-only.md", 8),
	}
	roots := []string{".", "skills/s000", "skills/s000-extra", "nested", "nested/child", "outside"}
	got := mustSummarizeByRoot(t, entries, ".", roots)
	if len(got) != len(roots) {
		t.Fatalf("len %d", len(got))
	}
	for i, root := range roots {
		sameSummary(t, got[i], summarize(entries, root))
	}
	if got[1].Files != 2 || got[2].Files != 2 {
		t.Fatalf("prefix siblings leaked files: s000=%+v s000-extra=%+v", got[1], got[2])
	}
	if got[3].Files != 4 || got[4].Files != 2 {
		t.Fatalf("nested counts: parent=%+v child=%+v", got[3], got[4])
	}
	if got[1].TreeHash == got[2].TreeHash || got[3].TreeHash == got[4].TreeHash {
		t.Fatal("distinct trees shared a hash")
	}

	filtered := mustSummarizeByRoot(t, entries, "skills", []string{"skills/s000", "skills/s000-extra"})
	sameSummary(t, filtered[0], summarize(entries, "skills/s000"))
	sameSummary(t, filtered[1], summarize(entries, "skills/s000-extra"))
}

func TestSummarizeByRootPreservesErrors(t *testing.T) {
	oid := strings.Repeat("a", 40)
	entries := []entry{
		blob("a/SKILL.md", 1),
		{Path: "a/link", Mode: "120000", Kind: "blob", OID: oid},
		blob("b/SKILL.md", 1),
		{Path: "b/large", Mode: "100644", Kind: "blob", OID: oid, Size: skillpkg.MaxFileBytes + 1},
		blob("c/SKILL.md", 1),
		blob("c/.git/config", 1),
	}
	got := mustSummarizeByRoot(t, entries, ".", []string{"a", "b", "c"})
	for i, root := range []string{"a", "b", "c"} {
		sameSummary(t, got[i], summarize(entries, root))
		if got[i].Error == "" {
			t.Errorf("%s: expected an error", root)
		}
	}
}

func TestSummarizeByRootStopsAtScanRoot(t *testing.T) {
	entries := []entry{
		tree("keep"),
		blob("keep/SKILL.md", 1),
		blob("keep/in.md", 2),
		tree("keep/child"),
		blob("keep/child/SKILL.md", 3),
		tree("skip"),
		blob("skip/SKILL.md", 4),
		blob("skip/out.md", 5),
	}
	got := mustSummarizeByRoot(t, entries, "keep", []string{"keep", "keep/child"})
	sameSummary(t, got[0], summarize(entries, "keep"))
	sameSummary(t, got[1], summarize(entries, "keep/child"))
	if got[0].Files != 3 {
		t.Fatalf("parent files %d, leaked skip?", got[0].Files)
	}
}

func TestPrefixSiblingSkillsAreIsolated(t *testing.T) {
	repo, c := fixture(t)
	fixtureFile(t, repo, "skills/s000/SKILL.md", "---\nname: s000\n---\n")
	fixtureFile(t, repo, "skills/s000/a.md", "a")
	fixtureFile(t, repo, "skills/s000-extra/SKILL.md", "---\nname: s000-extra\n---\n")
	fixtureFile(t, repo, "skills/s000-extra/b.md", "b")
	commitFixture(t, repo)
	s, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "", ".")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Candidate{}
	for _, x := range s.Candidates {
		byPath[x.Path] = x
	}
	if len(s.Candidates) != 2 || byPath["skills/s000"].Files != 2 || byPath["skills/s000"].Name != "s000" {
		t.Fatalf("s000: %+v", s.Candidates)
	}
	if byPath["skills/s000-extra"].Files != 2 || byPath["skills/s000-extra"].Name != "s000-extra" {
		t.Fatalf("s000-extra: %+v", s.Candidates)
	}
	if byPath["skills/s000"].TreeHash == byPath["skills/s000-extra"].TreeHash {
		t.Fatal("prefix siblings shared a tree hash")
	}
	files, err := c.ReadSkill(context.Background(), "repo1", s.Commit, "skills/s000")
	if err != nil || len(files) != 2 || string(files["a.md"]) != "a" || files["b.md"] != nil {
		t.Fatalf("s000 files: %v %v", files, err)
	}
}

func TestSummarizeByRootHonorsCancel(t *testing.T) {
	entries := make([]entry, 0, 2000)
	roots := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		d := fmt.Sprintf("s%02d", i)
		roots = append(roots, d)
		entries = append(entries, blob(d+"/SKILL.md", 1))
		for j := 0; j < 128; j++ {
			entries = append(entries, blob(fmt.Sprintf("%s/f%03d.md", d, j), 1))
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := summarizeByRoot(ctx, entries, ".", roots); err != context.Canceled {
		t.Fatalf("canceled context: %v", err)
	}
}
