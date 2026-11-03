package skillrepo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"
	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
)

func TestValidateSources(t *testing.T) {
	valid := []string{"https://github.com/org/repo.git", "https://git.example:8443/group/sub/repo.git", "ssh://git@git.example:2222/group/repo.git", "git@company:team/skills.git", "company:team/skills.git", "ssh://git@[::1]:2222/repo.git"}
	for _, raw := range valid {
		if _, err := ValidateURL(raw); err != nil {
			t.Errorf("%q: %v", raw, err)
		}
	}
	invalid := []string{"", "--upload-pack=whoami", "/tmp/repo", "../repo", "C:/repo", "file:///tmp/repo", "file:/tmp/repo", "ext::whoami", "http://host/repo", "git://host/repo", "https://token@host/repo", "https://u:p@host/repo", "ssh://u:p@host/repo", "git@-oProxyCommand=x:repo", "git@host:repo\nnext", "https://host/repo?token=secret", "https://host/repo#fragment", "ssh://host/%0aevil", "ssh://host/../repo", "host::helper", "https://host:99999/repo"}
	for _, raw := range invalid {
		if _, err := ValidateURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, ref := range []string{"", "main", "feature/skill-repositories", "refs/tags/v1.0", strings.Repeat("a", 40)} {
		if err := ValidateRef(ref); err != nil {
			t.Errorf("ref %q: %v", ref, err)
		}
	}
	for _, ref := range []string{"-x", "main:refs/evil", "HEAD^{tree}", "a..b", "a\nb", "refs//x", "../main"} {
		if ValidateRef(ref) == nil {
			t.Errorf("accepted ref %q", ref)
		}
	}
	for _, p := range []string{"../x", "/x", "a/../b", "a\\b", "a//b"} {
		if _, err := ValidateRoot(p); err == nil {
			t.Errorf("accepted path %q", p)
		}
	}
}

func fixtureGit(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return b
}
func fixtureFile(t *testing.T, dir, p, content string) {
	t.Helper()
	dest := filepath.Join(dir, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (string, *Client) {
	t.Helper()
	repo := t.TempDir()
	fixtureGit(t, repo, "init", "-b", "main")
	c := New(t.TempDir())
	// Only the test seam substitutes the HTTPS remote. Public entry points still validate it.
	c.runOverride = func(ctx context.Context, dir, remote string, limit int, stdin []byte, args ...string) ([]byte, error) {
		args = append([]string{}, args...)
		for i, arg := range args {
			if arg == "https://fixture.invalid/skills.git" {
				args[i] = "file://" + filepath.ToSlash(repo)
			}
		}
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "protocol.file.allow=always"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(gitEnv(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		b, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		if len(b) > limit {
			return nil, errors.New("fixture output too large")
		}
		return b, nil
	}
	return repo, c
}
func commitFixture(t *testing.T, repo string) {
	t.Helper()
	fixtureGit(t, repo, "add", ".")
	fixtureGit(t, repo, "commit", "-m", "fixture")
}

func TestSnapshotDiscoveryAndSelectedFiles(t *testing.T) {
	repo, c := fixture(t)
	fixtureFile(t, repo, "skills/review/SKILL.md", "---\nname: review\ndescription: Code review\n---\nRead references/rules.md\n")
	fixtureFile(t, repo, "skills/review/references/rules.md", "preserve this resource")
	fixtureFile(t, repo, "skills/review/scripts/check.py", "print('ok')\n")
	fixtureFile(t, repo, ".agents/skills/explain/SKILL.md", "---\nname: explain\n---\nExplain\n")
	fixtureFile(t, repo, "plugins/wiki/skills/query/SKILL.md", "query ../shared\n")
	fixtureFile(t, repo, "README.md", "not a skill")
	commitFixture(t, repo)
	s, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "", ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Candidates) != 3 {
		t.Fatalf("candidates: %+v", s)
	}
	var review Candidate
	for _, x := range s.Candidates {
		if x.Path == "skills/review" {
			review = x
		}
	}
	if review.Name != "review" || review.Description != "Code review" || review.Files != 3 || review.Error != "" || review.TreeHash == "" {
		t.Fatalf("review: %+v", review)
	}
	files, err := c.ReadSkill(context.Background(), "repo1", s.Commit, review.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || string(files["references/rules.md"]) != "preserve this resource" || string(files["scripts/check.py"]) != "print('ok')\n" {
		t.Fatalf("files: %v", files)
	}
	if _, err = os.Stat(filepath.Join(c.CacheDir, "repo1", "skills")); !os.IsNotExist(err) {
		t.Fatalf("repository was checked out: %v", err)
	}
	filtered, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "main", ".agents/skills")
	if err != nil || len(filtered.Candidates) != 1 {
		t.Fatalf("filtered %+v: %v", filtered, err)
	}
	if _, err = c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "main", "absent"); err == nil {
		t.Fatal("missing root accepted")
	}
	if _, err = c.ReadSkill(context.Background(), "repo1", s.Commit, "skills"); err == nil {
		t.Fatal("parent without SKILL accepted")
	}
}

func TestSnapshotSurvivesRefreshAndUnrelatedChanges(t *testing.T) {
	repo, c := fixture(t)
	fixtureFile(t, repo, "skills/a/SKILL.md", "original")
	commitFixture(t, repo)
	first, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, repo, "README.md", "unrelated")
	commitFixture(t, repo)
	second, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Commit == second.Commit || first.Candidates[0].TreeHash != second.Candidates[0].TreeHash {
		t.Fatal("unrelated update changed skill identity")
	}
	fixtureFile(t, repo, "skills/a/SKILL.md", "updated")
	commitFixture(t, repo)
	third, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if third.Candidates[0].TreeHash == first.Candidates[0].TreeHash {
		t.Fatal("missed update")
	}
	old, err := c.ReadSkill(context.Background(), "repo1", first.Commit, "skills/a")
	if err != nil || string(old["SKILL.md"]) != "original" {
		t.Fatalf("snapshot changed: %v %v", old, err)
	}
	if err = c.RemoveCache(context.Background(), "repo1"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ReadSkill(context.Background(), "repo1", first.Commit, "skills/a"); err == nil {
		t.Fatal("removed snapshot accepted")
	}
}

func TestInvalidSkillsAreIsolated(t *testing.T) {
	repo, c := fixture(t)
	fixtureFile(t, repo, "good/SKILL.md", "valid")
	fixtureFile(t, repo, "bad/SKILL.md", "---\nname: [invalid\n---\n")
	fixtureFile(t, repo, "nested/SKILL.md", "parent")
	fixtureFile(t, repo, "nested/child/SKILL.md", "child")
	fixtureFile(t, repo, "lfs/SKILL.md", "valid")
	fixtureFile(t, repo, "lfs/assets.bin", "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 2\n")
	commitFixture(t, repo)
	s, err := c.Scan(context.Background(), "repo1", "https://fixture.invalid/skills.git", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Candidate{}
	for _, x := range s.Candidates {
		byPath[x.Path] = x
	}
	if byPath["good"].Error != "" || byPath["bad"].Error != "invalid_frontmatter" || byPath["nested"].Warning != "nested_skills" {
		t.Fatalf("candidates: %+v", byPath)
	}
	if _, err = c.ReadSkill(context.Background(), "repo1", s.Commit, "lfs"); err == nil {
		t.Fatal("LFS pointer imported as content")
	}
	parent, err := c.ReadSkill(context.Background(), "repo1", s.Commit, "nested")
	if err != nil || len(parent) != 2 {
		t.Fatalf("nested parent was silently trimmed: %v %v", parent, err)
	}
}

func TestUnsafeTreeModesAndLimits(t *testing.T) {
	base := entry{Path: "a/SKILL.md", Mode: "100644", Kind: "blob", OID: strings.Repeat("a", 40), Size: 1}
	for _, other := range []entry{
		{Path: "a/link", Mode: "120000", Kind: "blob", OID: base.OID},
		{Path: "a/sub", Mode: "160000", Kind: "commit", OID: base.OID},
		{Path: "a/../../bad", Mode: "100644", Kind: "blob", OID: base.OID},
		{Path: "a/.git/config", Mode: "100644", Kind: "blob", OID: base.OID},
		{Path: "a/skill.md", Mode: "100644", Kind: "blob", OID: base.OID},
		{Path: "a/large", Mode: "100644", Kind: "blob", OID: base.OID, Size: skillpkg.MaxFileBytes + 1},
	} {
		if x := summarize([]entry{base, other}, "a"); x.Error == "" {
			t.Errorf("accepted %+v", other)
		}
	}
	if _, err := New(t.TempDir()).ReadSkill(context.Background(), "../outside", strings.Repeat("a", 40), "."); err == nil {
		t.Fatal("cache path escaped")
	}
}

func TestOutputAndCacheLimits(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	_, err := runGit(context.Background(), dir, "", 1, nil, "version")
	var issue *Error
	if !errors.As(err, &issue) || issue.Code != "scan_limit" {
		t.Fatalf("unbounded Git output: %v", err)
	}
	f, err := os.Create(filepath.Join(dir, "large.pack"))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(MaxCacheBytes + 1)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cacheSize(dir); !errors.As(err, &issue) || issue.Code != "cache_limit" {
		t.Fatalf("unbounded cache: %v", err)
	}
}

func TestCancellationAndLocking(t *testing.T) {
	c := New(t.TempDir())
	dir, _ := c.directory("repo1")
	unlock, err := acquire(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = c.Scan(ctx, "repo1", "https://fixture.invalid/repo.git", "", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock did not cancel: %v", err)
	}
}

func TestRepositoryCrossProcessLock(t *testing.T) {
	if dir := os.Getenv("TIDEBOT_TEST_LOCK_CHILD"); dir != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		unlock, err := acquire(ctx, dir)
		if err == nil {
			unlock()
			t.Fatal("acquired another process's repository lock")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		return
	}
	dir := filepath.Join(t.TempDir(), "repo1")
	unlock, err := acquire(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRepositoryCrossProcessLock$")
	cmd.Env = append(os.Environ(), "TIDEBOT_TEST_LOCK_CHILD="+dir)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, b)
	}
}

func TestGitEnvironmentAndSafeErrors(t *testing.T) {
	orig := netproxy.Get()
	t.Cleanup(func() { _ = netproxy.Set(orig) })
	_ = netproxy.Set(netproxy.Settings{Mode: netproxy.ModeOff})
	t.Setenv("GIT_SSH_COMMAND", "unsafe")
	t.Setenv("GIT_SSL_NO_VERIFY", "true")
	t.Setenv("GIT_SSL_CAINFO", "/fixture/company-ca.pem")
	t.Setenv("GIT_DIR", "/other")
	t.Setenv("HTTPS_PROXY", "https://secret@proxy.invalid")
	t.Setenv("SSH_AUTH_SOCK", "/fixture/agent")
	env := map[string]string{}
	for _, e := range gitEnv() {
		k, v, _ := strings.Cut(e, "=")
		env[k] = v
	}
	if env["GIT_DIR"] != "" || env["HTTPS_PROXY"] != "" || env["GIT_SSL_NO_VERIFY"] != "false" || env["GIT_SSL_CAINFO"] != "/fixture/company-ca.pem" || env["SSH_AUTH_SOCK"] != "/fixture/agent" || !strings.Contains(env["GIT_SSH_COMMAND"], "StrictHostKeyChecking=yes") {
		t.Fatalf("unsafe env: %+v", env)
	}
	for _, raw := range []string{"Authentication failed for https://token@private/repo", "fatal certificate error https://secret@host/repo", "Failed to connect to private host"} {
		if strings.Contains(classify(raw).Error(), "secret") || strings.Contains(classify(raw).Error(), "token@") {
			t.Fatal("error leaks remote credentials")
		}
	}
	if !reflect.DeepEqual(classify("Permission denied (publickey)").(*Error).Code, "authentication") {
		t.Fatal("authentication not recognized")
	}
}

// Run explicitly with TIDEBOT_TEST_SKILL_REPO_URL; ordinary tests never need the network.
func TestPublicRepositoryOptIn(t *testing.T) {
	raw := os.Getenv("TIDEBOT_TEST_SKILL_REPO_URL")
	if raw == "" {
		t.Skip("live repository test is opt-in")
	}
	c := New(t.TempDir())
	s, err := c.Scan(context.Background(), "live", raw, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Candidates) == 0 {
		t.Fatal("no skills found")
	}
	for _, candidate := range s.Candidates {
		if candidate.Error != "" {
			continue
		}
		files, err := c.ReadSkill(context.Background(), "live", s.Commit, candidate.Path)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("commit=%s candidates=%d selected=%s files=%d", s.Commit, len(s.Candidates), candidate.Path, len(files))
		return
	}
	t.Fatal("no installable skill found")
}
