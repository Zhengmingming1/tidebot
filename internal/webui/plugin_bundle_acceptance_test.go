package webui

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// This opt-in acceptance test uses the unmodified external archive. It exercises
// storage and staging without calling its APIs, running its scripts, or touching Git.
func TestRealTrunkMergePluginAcceptance(t *testing.T) {
	archivePath := os.Getenv("TIDEBOT_PLUGIN_ACCEPTANCE_ZIP")
	if archivePath == "" {
		t.Skip("set TIDEBOT_PLUGIN_ACCEPTANCE_ZIP to the original trunk-merge.zip")
	}
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	original := trunkMergeOriginalFiles(t, archive)
	ctx := context.Background()
	c := login(t)
	p := uploadZip(t, c, filepath.Base(archivePath), archive)
	if p.Name != "trunk-merge" || p.SourceRoot != "trunk-merge" || p.Files != len(original)+1 {
		t.Fatalf("unexpected aggregate preview: name=%q source_root=%q files=%d; expected %d original files and one generated SKILL.md", p.Name, p.SourceRoot, p.Files, len(original))
	}
	if code, out := c.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p.StageID, "name": p.Name}); code != http.StatusOK {
		t.Fatalf("import returned %d: %v", code, out)
	}
	stored, err := c.s.deps.Skills.List(ctx)
	if err != nil || len(stored) != 1 || stored[0].CurrentVersion == nil {
		t.Fatalf("expected one aggregate skill: count=%d error=%v", len(stored), err)
	}
	versionID := stored[0].CurrentVersion.ID
	files, err := c.s.deps.Skills.Files(ctx, versionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(original)+1 {
		t.Fatalf("stored %d files, want %d originals plus generated SKILL.md", len(files), len(original))
	}
	for name, want := range original {
		if !bytes.Equal(files[name], want) {
			t.Errorf("original file was lost or changed in storage: %s", name)
		}
	}
	rootMD := files["SKILL.md"]
	command := original["commands/trunk-merge.md"]
	parts := bytes.SplitN(command, []byte("\n---\n"), 2)
	if len(parts) != 2 {
		t.Fatal("fixture's primary command has no closed frontmatter")
	}
	if !bytes.Contains(rootMD, bytes.TrimSpace(parts[1])) {
		t.Error("generated root instructions must include the primary command body so existing context checks account for it")
	}
	for _, skillPath := range []string{"skills/query-task-branch/SKILL.md", "skills/query-task-change-file/SKILL.md", "skills/query-task-stage/SKILL.md"} {
		if !bytes.Contains(rootMD, []byte(skillPath)) {
			t.Errorf("generated instructions do not identify auxiliary document %s", skillPath)
		}
	}
	stageRoot := filepath.Join(t.TempDir(), "stage")
	if persistentStage := os.Getenv("TIDEBOT_PLUGIN_ACCEPTANCE_STAGE"); persistentStage != "" {
		stageRoot, err = filepath.Abs(persistentStage)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(stageRoot); !os.IsNotExist(err) {
			t.Fatalf("persistent stage must be a new path: %s (error=%v)", stageRoot, err)
		}
	}
	staged, err := c.s.deps.Skills.Stage(ctx, stageRoot, []tasksvc.SkillBinding{{SkillID: stored[0].ID, Mode: "pinned", VersionID: versionID}})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage returned %d skills: %v", len(staged), err)
	}
	if staged[0].Command() != "/tidebot:trunk-merge" || staged[0].Dir != filepath.Join(stageRoot, "skills", "trunk-merge") {
		t.Fatalf("unexpected runtime entry point: command=%q directory=%q", staged[0].Command(), staged[0].Dir)
	}
	for name, want := range original {
		got, err := os.ReadFile(filepath.Join(staged[0].Dir, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("original resource did not survive staging at its original relative path: %s (error=%v)", name, err)
		}
	}
	if !bytes.Contains(staged[0].SkillMD, bytes.TrimSpace(parts[1])) {
		t.Error("primary command instructions were lost while staging")
	}
	if !bytes.Contains(staged[0].SkillMD, []byte("Package root: "+staged[0].Dir)) || bytes.Contains(staged[0].SkillMD, []byte("${CLAUDE_SKILL_DIR}")) || bytes.Contains(staged[0].SkillMD, []byte("${CLAUDE_PLUGIN_ROOT}")) {
		t.Error("staged instructions must resolve the package root to the aggregate skill's directory")
	}
	if stages, err := os.ReadDir(c.s.stagingDir()); err != nil || len(stages) != 0 {
		t.Errorf("committed upload staging was not cleaned: count=%d error=%v", len(stages), err)
	}
	exported := c.do(http.MethodGet, "/api/skills/"+stored[0].ID+"/export", "", nil)
	if exported.Code != http.StatusOK {
		t.Fatalf("export returned %d: %s", exported.Code, exported.Body.String())
	}
	// A fresh store checks that the generated root is portable and does not need
	// the original upload's staging metadata or a pre-existing skill entity.
	dest := login(t)
	p2 := uploadZip(t, dest, "trunk-merge-export.zip", exported.Body.Bytes())
	if p2.Name != p.Name || p2.Files != len(files) {
		t.Fatalf("exported archive changed its aggregate shape: name=%q files=%d", p2.Name, p2.Files)
	}
	if code, out := dest.json(http.MethodPost, "/api/skills/import", map[string]string{"stage_id": p2.StageID, "name": p2.Name}); code != http.StatusOK {
		t.Fatalf("round-trip import returned %d: %v", code, out)
	}
	reimported, err := dest.s.deps.Skills.List(ctx)
	if err != nil || len(reimported) != 1 || reimported[0].CurrentVersion == nil {
		t.Fatalf("round-trip aggregate missing: count=%d error=%v", len(reimported), err)
	}
	roundTripFiles, err := dest.s.deps.Skills.Files(ctx, reimported[0].CurrentVersion.ID)
	if err != nil || !reflect.DeepEqual(files, roundTripFiles) {
		t.Errorf("export and re-import did not preserve every stored byte: error=%v", err)
	}
	manifestJSON, err := os.ReadFile(filepath.Join(stageRoot, ".claude-plugin", "plugin.json"))
	var runtimeManifest struct{ Name string }
	if err != nil || json.Unmarshal(manifestJSON, &runtimeManifest) != nil || runtimeManifest.Name != "tidebot" {
		t.Errorf("unexpected outer runtime plugin manifest: error=%v", err)
	}
	t.Logf("accepted %d original archive files plus generated entry instructions; storage, stage resources, and export round trip preserved", len(original))
}

func trunkMergeOriginalFiles(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() || strings.HasPrefix(entry.Name, "__MACOSX/") || path.Base(entry.Name) == ".DS_Store" {
			continue
		}
		if !strings.HasPrefix(entry.Name, "trunk-merge/") {
			t.Fatalf("unexpected archive root: %s", entry.Name)
		}
		rc, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[strings.TrimPrefix(entry.Name, "trunk-merge/")] = data
	}
	if len(files) != 9 || len(files[".claude-plugin/plugin.json"]) == 0 || len(files["commands/trunk-merge.md"]) == 0 {
		t.Fatalf("expected the original nine-file trunk-merge fixture, found %d files", len(files))
	}
	return files
}
