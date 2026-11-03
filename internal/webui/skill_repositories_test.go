package webui

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/skillrepo"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

type repositoryGitFixture struct {
	snapshot skillrepo.Snapshot
	files    map[string]map[string][]byte
	err      error
	reads    []string
}

func (f *repositoryGitFixture) Scan(context.Context, string, string, string, string) (skillrepo.Snapshot, error) {
	return f.snapshot, f.err
}
func (f *repositoryGitFixture) ReadSkill(_ context.Context, _ string, commit, path string) (map[string][]byte, error) {
	f.reads = append(f.reads, commit)
	files, ok := f.files[commit]
	if !ok {
		return nil, errors.New("missing fixture snapshot")
	}
	return files, nil
}
func (f *repositoryGitFixture) RemoveCache(context.Context, string) error { return nil }
func repositoryWebFixture(t *testing.T) (*client, *repositoryGitFixture, string) {
	t.Helper()
	c := login(t)
	commit := strings.Repeat("a", 40)
	f := &repositoryGitFixture{snapshot: skillrepo.Snapshot{Commit: commit, Candidates: []skillrepo.Candidate{{Path: ".", Name: "review", TreeHash: "one", Files: 2, Size: 60}}}, files: map[string]map[string][]byte{commit: {"SKILL.md": []byte("---\nname: review\ndescription: Review code\n---\n# First"), "scripts/check.py": []byte("print('ok')")}}}
	c.s.repositories.git = f
	code, out := c.json("POST", "/api/skill-repositories", map[string]any{"url": "https://git.company.example/group/skills.git", "ref": "team/main"})
	if code != 201 {
		t.Fatalf("create %d %v", code, out)
	}
	id := out["id"].(string)
	if code, out = c.json("POST", "/api/skill-repositories/"+id+"/refresh", nil); code != 200 {
		t.Fatalf("scan %d %v", code, out)
	}
	return c, f, id
}
func repositoryPreview(t *testing.T, c *client, repo, commit string) map[string]any {
	t.Helper()
	code, out := c.json("POST", "/api/skill-repositories/"+repo+"/preview", map[string]any{"path": ".", "commit": commit})
	if code != 200 {
		t.Fatalf("preview %d %v", code, out)
	}
	return out
}
func repositoryInstall(t *testing.T, c *client, repo string, p map[string]any) map[string]any {
	t.Helper()
	code, out := c.json("POST", "/api/skill-repositories/"+repo+"/install", map[string]any{"stage_id": p["stage_id"], "name": p["name"], "expected_current_version_id": p["expected_current_version_id"]})
	if code != 200 {
		t.Fatalf("install %d %v", code, out)
	}
	return out
}
func TestRepositoryHTTPRootSnapshotUpdateAndRemoval(t *testing.T) {
	c, f, repo := repositoryWebFixture(t)
	base := "/api/skill-repositories/" + repo
	old := f.snapshot.Commit
	p := repositoryPreview(t, c, repo, old)
	if p["files"] != float64(2) || !strings.Contains(p["skill_md"].(string), "First") {
		t.Fatal("incomplete preview", p)
	}
	next := strings.Repeat("b", 40)
	f.snapshot.Commit = next
	f.snapshot.Candidates[0].TreeHash = "two"
	f.files[next] = map[string][]byte{"SKILL.md": []byte("---\nname: review\n---\n# Second"), "scripts/check.py": []byte("print('new')")}
	if code, out := c.json("POST", base+"/refresh", nil); code != 200 {
		t.Fatalf("refresh %d %v", code, out)
	}
	installed := repositoryInstall(t, c, repo, p)
	sid := installed["skill_id"].(string)
	vid := installed["version_id"].(string)
	files, err := c.s.deps.Skills.Files(context.Background(), vid)
	if err != nil || !strings.Contains(string(files["SKILL.md"]), "First") {
		t.Fatal("installed moving HEAD instead of preview", err)
	}
	p = repositoryPreview(t, c, repo, next)
	if p["expected_current_version_id"] != vid || len(p["diff"].([]any)) != 2 {
		t.Fatal("missing update diff", p)
	}
	downloaded := repositoryInstall(t, c, repo, p)
	newVid := downloaded["version_id"].(string)
	current, _ := c.s.deps.Ver.GetPointer(context.Background(), version.EntitySkill, sid, version.PointerCurrent)
	if current.String != vid {
		t.Fatal("download advanced current")
	}
	if code, _ := c.json("POST", "/api/skills/"+sid+"/repository-apply", map[string]any{"version_id": newVid, "expected_current_version_id": "stale"}); code != 409 {
		t.Fatal("stale apply", code)
	}
	if code, out := c.json("POST", "/api/skills/"+sid+"/repository-apply", map[string]any{"version_id": newVid, "expected_current_version_id": vid}); code != 200 || out["local_modified"] != false {
		t.Fatalf("apply %d %v", code, out)
	}
	if code, _ := c.json("DELETE", base, nil); code != 200 {
		t.Fatal(code)
	}
	if code, out := c.json("GET", "/api/skills/"+sid+"/repository-source", nil); code != 200 || out["source_removed"] != true {
		t.Fatal("lost source provenance", code, out)
	}
	if _, err = c.s.deps.Skills.Files(context.Background(), newVid); err != nil {
		t.Fatal("removed skill with source", err)
	}
}
func TestRepositoryHTTPGuardsAndFailedScan(t *testing.T) {
	c, f, repo := repositoryWebFixture(t)
	base := "/api/skill-repositories/" + repo
	commit := f.snapshot.Commit
	for _, url := range []string{"file:///tmp/repo", "ext::evil", "https://token@github.com/a/b", "ssh://user:pass@host/a"} {
		if code, _ := c.json("POST", "/api/skill-repositories", map[string]string{"url": url}); code != 400 {
			t.Fatalf("accepted unsafe URL %q %d", url, code)
		}
	}
	if code, _ := c.json("POST", base+"/install", map[string]string{"path": ".", "commit": commit, "name": "bypass"}); code != 409 {
		t.Fatal("installed without server preview", code)
	}
	p := repositoryPreview(t, c, repo, commit)
	code, out := c.json("GET", base, nil)
	if code != 200 {
		t.Fatal(code)
	}
	out["name"] = "renamed"
	if code, out = c.json("PUT", base, out); code != 200 {
		t.Fatal(code, out)
	}
	if code, _ = c.json("POST", base+"/install", map[string]any{"stage_id": p["stage_id"], "name": "review", "expected_current_version_id": ""}); code != 409 {
		t.Fatal("old config accepted", code)
	}
	f.err = &skillrepo.Error{Code: "authentication", Message: "credentials missing"}
	if code, out = c.json("POST", base+"/refresh", nil); code != 400 || out["code"] != "repo_authentication" {
		t.Fatal(code, out)
	}
	if code, out = c.json("GET", base, nil); code != 200 || out["commit"] != commit || out["last_error"] != "credentials missing" {
		t.Fatal("failed scan erased index", code, out)
	}
	for _, suffix := range []string{"/refresh", "/preview", "/install"} {
		if rr := c.do(http.MethodGet, base+suffix, "", nil); rr.Code != 405 {
			t.Fatal("mutating GET", suffix, rr.Code)
		}
	}
	c.csrf = "bad"
	rr := c.do(http.MethodPost, base+"/refresh", "application/json", []byte(`{}`))
	if rr.Code != 403 {
		t.Fatal("missing CSRF protection", rr.Code)
	}
}
func TestRepositoryHTTPRemovedDirectoryRemainsVisible(t *testing.T) {
	c, f, repo := repositoryWebFixture(t)
	installed := repositoryInstall(t, c, repo, repositoryPreview(t, c, repo, f.snapshot.Commit))
	sid := installed["skill_id"].(string)
	f.snapshot.Candidates = []skillrepo.Candidate{}
	c.json("POST", "/api/skill-repositories/"+repo+"/refresh", nil)
	code, out := c.json("GET", "/api/skill-repositories/"+repo+"/skills", nil)
	if code != 200 {
		t.Fatal(code, out)
	}
	list := out["candidates"].([]any)
	if len(list) != 1 {
		t.Fatal("missing installed item", out)
	}
	row := list[0].(map[string]any)
	if row["removed"] != true || row["source"].(map[string]any)["skill_id"] != sid {
		t.Fatal(out)
	}
}
