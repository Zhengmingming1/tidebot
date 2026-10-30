package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func waitRunDone(t *testing.T, a *App, runID string) {
	t.Helper()
	waitFor(t, 10*time.Second, func() bool {
		var st string
		_ = a.DB.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&st)
		ents, _ := os.ReadDir(a.skillStageRoot())
		return st != "" && st != "queued" && st != "running" && len(ents) == 0
	})
}

// A read-only run with a skill in the owner's project: the skill comes from
// tidebot's stage, write tools are denied, and neither the project nor the
// home folder (~/.claude, ~/.claude.json) changes.
func TestStartRunReadOnlyLeavesFoldersAlone(t *testing.T) {
	a := newTestApp(t)
	home := fakeHome(t)
	ws := userProject(t)
	skillID := importTestSkill(t, a, "review")
	beforeWS, beforeHome := snapshotTree(t, ws), snapshotTree(t, home)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	runID, err := a.startRun(webui.RunRequest{Prompt: "only internal/runner", SkillID: skillID, Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, runID)
	sameTree(t, "workspace", beforeWS, snapshotTree(t, ws))
	sameTree(t, "home", beforeHome, snapshotTree(t, home))
	b, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	if argAfter(args, "-p") != "/tidebot:review only internal/runner" {
		t.Fatalf("prompt: %q", args)
	}
	if d := argAfter(args, "--disallowedTools"); !strings.Contains(d, "Edit") || !strings.Contains(d, "Write") {
		t.Fatalf("write tools not denied: %q", args)
	}
	if !strings.HasPrefix(argAfter(args, "--plugin-dir"), a.skillStageRoot()) || argAfter(args, "--restricted") == "" && !contains(args, "--restricted") {
		t.Fatalf("%q", args)
	}
	var prompt, wsDir, sk, ver, mode string
	_ = a.DB.QueryRow(`SELECT prompt, workspace_dir, skill_id, skill_version_id, run_mode FROM runs WHERE id=?`, runID).Scan(&prompt, &wsDir, &sk, &ver, &mode)
	real, _ := filepath.EvalSymlinks(ws)
	if prompt != "only internal/runner" || wsDir != real || sk != skillID || ver == "" || mode != "read" {
		t.Fatalf("row: %q %q %q %q %q", prompt, wsDir, sk, ver, mode)
	}
}

// Edit mode drops the deny list; the folder is taken until the run ends.
func TestStartRunEditMode(t *testing.T) {
	a := newTestApp(t)
	fakeHome(t)
	ws := userProject(t)
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	runID, err := a.startRun(webui.RunRequest{Prompt: "fix it", Workspace: ws, Mode: "edit"})
	if err != nil {
		t.Fatal(err)
	}
	waitRunDone(t, a, runID)
	b, _ := os.ReadFile(argsFile)
	if strings.Contains(string(b), "--disallowedTools") || strings.Contains(string(b), "--plugin-dir") {
		t.Fatalf("%s", b)
	}
	real, _ := filepath.EvalSymlinks(ws)
	waitFor(t, 5*time.Second, func() bool { return lockEditDir(real) }) // freed when the run ends
	defer unlockEditDir(real)
	if _, err := a.startRun(webui.RunRequest{Prompt: "again", Workspace: ws, Mode: "edit"}); !errors.Is(err, guard.ErrDirBusy) {
		t.Fatalf("second edit run in a busy folder: %v", err)
	}
	if id, err := a.startRun(webui.RunRequest{Prompt: "just read", Workspace: ws}); err != nil {
		t.Fatalf("read-only run blocked by an edit run: %v", err)
	} else {
		waitRunDone(t, a, id)
	}
}

func TestStartRunRefusesSensitiveFolders(t *testing.T) {
	a := newTestApp(t)
	home := fakeHome(t)
	_ = os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	link := filepath.Join(t.TempDir(), "innocent")
	_ = os.Symlink(filepath.Join(home, ".claude"), link)
	for _, c := range []struct{ dir, want string }{
		{filepath.Join(home, ".ssh"), guard.DirSensitive},
		{filepath.Join(home, ".claude"), guard.DirSensitive},
		{link, guard.DirSensitive},
		{home, guard.DirContainsSensitive},
		{"~", guard.DirContainsSensitive},
		{a.Cfg.DataDir, guard.DirInternal},
		{"relative/dir", guard.DirRelative},
		{filepath.Join(home, "missing"), guard.DirNotFound},
	} {
		_, err := a.startRun(webui.RunRequest{Prompt: "x", Workspace: c.dir})
		var de *guard.DirError
		if !errors.As(err, &de) || de.Problem != c.want {
			t.Errorf("%s: %v, want %s", c.dir, err, c.want)
		}
	}
	if _, err := a.startRun(webui.RunRequest{Prompt: "x", SkillID: "skl_missing"}); !errors.Is(err, webui.ErrUnknownSkill) {
		t.Errorf("unknown skill: %v", err)
	}
	if ents, _ := os.ReadDir(a.skillStageRoot()); len(ents) != 0 {
		t.Error("stage left after a refused run")
	}
	_ = context.Background()
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
