package skillrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// prodReads routes every Git call except the fixture fetch through the
// production runGit (process setup, output bounds, cache checks). Production
// refuses file:// remotes, so only the fetch keeps the test seam.
func prodReads(c *Client, calls map[string]int) {
	fetch := c.runOverride
	c.runOverride = func(ctx context.Context, dir, remote string, limit int, stdin []byte, args ...string) ([]byte, error) {
		calls[args[0]]++
		if args[0] == "fetch" {
			return fetch(ctx, dir, remote, limit, stdin, args...)
		}
		return runGit(ctx, dir, remote, limit, stdin, args...)
	}
}

func manySkills(t *testing.T, skills, files int) (*Client, map[string]int) {
	t.Helper()
	repo, c := fixture(t)
	for i := 0; i < skills; i++ {
		d := fmt.Sprintf("skills/s%03d", i)
		fixtureFile(t, repo, d+"/SKILL.md", fmt.Sprintf("---\nname: s%03d\ndescription: skill %d\n---\nbody\n", i, i))
		for j := 1; j < files; j++ {
			fixtureFile(t, repo, fmt.Sprintf("%s/ref/f%03d.md", d, j), fmt.Sprintf("file %d of %d\n", j, i))
		}
	}
	commitFixture(t, repo)
	calls := map[string]int{}
	prodReads(c, calls)
	return c, calls
}

func total(calls map[string]int) int {
	n := 0
	for _, v := range calls {
		n += v
	}
	return n
}

// Git processes per scan or install must not grow with the number of skills or files.
func TestScanAndReadUseConstantGitProcesses(t *testing.T) {
	c, calls := manySkills(t, 40, 30)
	s, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "", ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Candidates) != 40 || s.Candidates[7].Name != "s007" || s.Candidates[7].Description != "skill 7" || s.Candidates[7].Files != 30 {
		t.Fatalf("candidates: %d %+v", len(s.Candidates), s.Candidates[7])
	}
	if calls["cat-file"] != 1 || total(calls) > 6 {
		t.Fatalf("scan spawned %v", calls)
	}
	clear(calls)
	files, err := c.ReadSkill(context.Background(), "repo1", s.Commit, "skills/s012")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 30 || string(files["ref/f029.md"]) != "file 29 of 12\n" || !strings.Contains(string(files["SKILL.md"]), "name: s012") {
		t.Fatalf("files: %d %q", len(files), files["ref/f029.md"])
	}
	if calls["cat-file"] != 1 || total(calls) != 3 {
		t.Fatalf("read spawned %v", calls)
	}
}

// Large skills are read in bounded batches and still come back byte for byte.
func TestReadSkillBatchesLargeFiles(t *testing.T) {
	repo, c := fixture(t)
	fixtureFile(t, repo, "big/SKILL.md", "---\nname: big\n---\n")
	want := map[string]string{}
	for i := 0; i < 4; i++ {
		body := strings.Repeat(string(rune('a'+i)), 3<<20) + "\n"
		want[fmt.Sprintf("data/%d.txt", i)] = body
		fixtureFile(t, repo, fmt.Sprintf("big/data/%d.txt", i), body)
	}
	commitFixture(t, repo)
	calls := map[string]int{}
	prodReads(c, calls)
	s, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "", ".")
	if err != nil {
		t.Fatal(err)
	}
	clear(calls)
	files, err := c.ReadSkill(context.Background(), "repo1", s.Commit, "big")
	if err != nil {
		t.Fatal(err)
	}
	for p, body := range want {
		if string(files[p]) != body {
			t.Fatalf("%s: %d bytes", p, len(files[p]))
		}
	}
	if calls["cat-file"] < 2 || calls["cat-file"] > 3 {
		t.Fatalf("12 MiB in %d batches", calls["cat-file"])
	}
}

// A Git answer that disagrees with the tree listing is rejected, never truncated or padded.
func TestReadBlobsRejectsMismatchedObjects(t *testing.T) {
	oid := strings.Repeat("a", 40)
	list := []entry{{Path: "SKILL.md", Mode: "100644", Kind: "blob", OID: oid, Size: 3}}
	for _, reply := range []string{
		oid + " missing\n",
		oid + " blob 4\nabcd\n",
		strings.Repeat("b", 40) + " blob 3\nabc\n",
		oid + " tree 3\nabc\n",
		oid + " blob 3\nabc",
		oid + " blob 3\nabc\nextra",
	} {
		c := New(t.TempDir())
		c.runOverride = func(ctx context.Context, dir, remote string, limit int, stdin []byte, args ...string) ([]byte, error) {
			if string(stdin) != oid+"\n" {
				t.Fatalf("stdin %q", stdin)
			}
			return []byte(reply), nil
		}
		err := c.readBlobs(context.Background(), t.TempDir(), list, func(int, []byte) error { return nil })
		var issue *Error
		if !errors.As(err, &issue) || issue.Code != "invalid_snapshot" {
			t.Errorf("%q: %v", reply, err)
		}
	}
}

// Read-only Git calls skip the cache walk; commands that write still enforce the cap.
func TestCacheLimitOnlyWalksForWrites(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "large.pack"))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(MaxCacheBytes + 1)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(context.Background(), dir, "", 4096, nil, "version"); err != nil {
		t.Fatalf("read walked the cache: %v", err)
	}
	var issue *Error
	if _, err := runGit(context.Background(), dir, "", 4096, nil, "init", "--bare", "--template=", dir); !errors.As(err, &issue) || issue.Code != "cache_limit" {
		t.Fatalf("write ignored the cache limit: %v", err)
	}
}

// Opt-in timing: TIDEBOT_PERF=1 go test -run TestRepositoryScanTiming -v ./internal/skillrepo
func TestRepositoryScanTiming(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("timing is opt-in")
	}
	for _, shape := range [][2]int{{50, 10}, {200, 10}, {20, 300}} {
		c, calls := manySkills(t, shape[0], shape[1])
		start := time.Now()
		s, err := c.Scan(context.Background(), "perf", "https://fixture.invalid/skills.git", "", ".")
		if err != nil {
			t.Fatal(err)
		}
		scan, scanCalls := time.Since(start), fmt.Sprint(calls)
		clear(calls)
		start = time.Now()
		files, err := c.ReadSkill(context.Background(), "perf", s.Commit, s.Candidates[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%d skills x %d files: scan %v %s; read %d files %v %v", shape[0], shape[1], scan.Round(time.Millisecond), scanCalls, len(files), time.Since(start).Round(time.Millisecond), calls)
	}
}
