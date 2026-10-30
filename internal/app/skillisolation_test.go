package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// snapshotTree records every path under root with its type, mode and content hash.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		fi, _ := d.Info()
		v := fi.Mode().String()
		if d.Type().IsRegular() {
			b, _ := os.ReadFile(p)
			s := sha256.Sum256(b)
			v += " " + hex.EncodeToString(s[:])
		}
		out[rel] = v
		return nil
	})
	return out
}

func sameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for p, v := range after {
		if before[p] != v {
			t.Errorf("%s: %s added or changed", what, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Errorf("%s: %s removed", what, p)
		}
	}
}

// userProject makes a folder that looks like the owner's own project, with the
// files Claude Code reads on its own.
func userProject(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	for p, c := range map[string]string{
		"main.go":                      "package main\n",
		"CLAUDE.md":                    "# project notes\n",
		".mcp.json":                    "{}\n",
		".claude/settings.json":        "{}\n",
		".claude/settings.local.json":  "{}\n",
		".claude/skills/mine/SKILL.md": "my own skill\n",
	} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(ws, p)), 0o755)
		if err := os.WriteFile(filepath.Join(ws, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

// fakeHome points HOME (and the Windows profile vars) at a folder holding a
// Claude Code setup, so a test sees any write to ~/.claude or ~/.claude.json.
func fakeHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	for p, c := range map[string]string{
		".claude.json":              "{}\n",
		".claude/settings.json":     "{}\n",
		".claude/CLAUDE.md":         "# mine\n",
		".claude/skills/x/SKILL.md": "x\n",
	} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(h, p)), 0o755)
		_ = os.WriteFile(filepath.Join(h, p), []byte(c), 0o644)
	}
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	return h
}

func importTestSkill(t *testing.T, a *App, name string) string {
	t.Helper()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: test skill\n---\nDo it: $ARGUMENTS\n"), 0o600)
	sk, err := a.Skills.ImportDir(context.Background(), name, dir, "user")
	if err != nil {
		t.Fatal(err)
	}
	return sk.ID
}

// A task bound to a skill and set to the owner's own project must not leave
// anything in that project or in ~/.claude: the skill is staged in tidebot's
// data folder, passed with --plugin-dir, and removed when the run ends.
func TestTaskSkillsStayOutOfUserFolders(t *testing.T) {
	a := newTestApp(t)
	home := fakeHome(t)
	ws := userProject(t)
	ctx := context.Background()
	skillID := importTestSkill(t, a, "review")
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "t", Prompt: "check", WorkspaceDir: ws, Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Skills.PinOnTask(ctx, tv.Task.ID, skillID, "", "user"); err != nil {
		t.Fatal(err)
	}
	beforeWS, beforeHome := snapshotTree(t, ws), snapshotTree(t, home)
	argsFile, envFile := filepath.Join(t.TempDir(), "args"), filepath.Join(t.TempDir(), "env")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	t.Setenv("TIDEBOT_MOCK_ENV_FILE", envFile)
	task, _ := a.Store.GetTask(ctx, tv.Task.ID)
	if err := a.runTaskTrigger(ctx, *task, "manual", ""); err != nil {
		t.Fatal(err)
	}
	sameTree(t, "workspace", beforeWS, snapshotTree(t, ws))
	sameTree(t, "home", beforeHome, snapshotTree(t, home))
	b, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	plugin := argAfter(args, "--plugin-dir")
	if plugin == "" || !strings.HasPrefix(plugin, a.skillStageRoot()+string(os.PathSeparator)) {
		t.Fatalf("skills not loaded from tidebot's stage: %q", args)
	}
	if p := argAfter(args, "--append-system-prompt-file"); !strings.HasPrefix(p, plugin) {
		t.Fatalf("task skills not attached: %q", args)
	}
	for _, flag := range []string{"--settings", "--mcp-config", "--append-system-prompt-file", "--add-dir", "--plugin-dir"} {
		if v := argAfter(args, flag); v != "" && !strings.HasPrefix(v, a.Cfg.DataDir) {
			t.Errorf("%s points outside tidebot's data: %s", flag, v)
		}
	}
	if _, err := os.Stat(plugin); !os.IsNotExist(err) {
		t.Fatal("stage not removed after the run")
	}
	env, _ := os.ReadFile(envFile)
	if !strings.Contains(string(env), "CLAUDE_CONFIG_DIR="+a.claudeConfigDir()) {
		t.Fatalf("Claude Code state not kept in tidebot's folder: %s", env)
	}
	if !strings.Contains(string(b), "--no-session-persistence") {
		t.Fatal("sessions would be saved")
	}
}

// Buttons run a skill as its slash command from the stage.
func TestButtonSkillRunInvokesStagedSkill(t *testing.T) {
	a := newTestApp(t)
	home := fakeHome(t)
	beforeHome := snapshotTree(t, home)
	skillID := importTestSkill(t, a, "summarize")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("TIDEBOT_MOCK_ARGS_FILE", argsFile)
	runID, err := a.startQuickRunSkill("the README", "", skillID, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		var st string
		_ = a.DB.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&st)
		ents, _ := os.ReadDir(a.skillStageRoot())
		return st != "" && st != "queued" && st != "running" && len(ents) == 0
	})
	b, _ := os.ReadFile(argsFile)
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	if argAfter(args, "-p") != "/tidebot:summarize the README" || argAfter(args, "--plugin-dir") == "" {
		t.Fatalf("%q", args)
	}
	sameTree(t, "home", beforeHome, snapshotTree(t, home))
}

// Stages a crash left behind are removed at startup; old button scratch too.
func TestPurgeSkillStages(t *testing.T) {
	a := newTestApp(t)
	_ = os.MkdirAll(filepath.Join(a.skillStageRoot(), "run-1", "skills"), 0o700)
	_ = os.MkdirAll(filepath.Join(a.Cfg.DataDir, "button-1"), 0o700)
	a.purgeSkillStages()
	if ents, _ := os.ReadDir(a.skillStageRoot()); len(ents) != 0 {
		t.Fatal("stage left")
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "button-1")); !os.IsNotExist(err) {
		t.Fatal("button scratch left")
	}
}

// The one-time cleanup removes tidebot's old copies from task workspaces, keeps
// the owner's skills, and doesn't run again once done.
func TestCleanLegacySkillCopiesInTaskWorkspaces(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	ws := userProject(t)
	skillID := importTestSkill(t, a, "review")
	tv, err := a.Tasks.Create(ctx, tasksvc.CreateInput{Name: "t", Prompt: "check", WorkspaceDir: ws, Actor: "user"})
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Skills.PinOnTask(ctx, tv.Task.ID, skillID, "", "user")
	// What tidebot 0.1.9 wrote there.
	files, _ := a.Skills.Files(ctx, mustCurrent(t, a, skillID))
	for p, c := range files {
		dst := filepath.Join(ws, ".claude", "skills", "review", p)
		_ = os.MkdirAll(filepath.Dir(dst), 0o700)
		_ = os.WriteFile(dst, c, 0o600)
	}
	a.cleanLegacySkillCopies(ctx)
	if _, err := os.Stat(filepath.Join(ws, ".claude", "skills", "review")); !os.IsNotExist(err) {
		t.Fatal("tidebot copy kept")
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude", "skills", "mine", "SKILL.md")); err != nil {
		t.Fatal("owner's skill removed")
	}
	// Done once: a copy appearing later is not touched by tidebot again.
	_ = os.MkdirAll(filepath.Join(ws, ".claude", "skills", "review"), 0o700)
	for p, c := range files {
		_ = os.WriteFile(filepath.Join(ws, ".claude", "skills", "review", p), c, 0o600)
	}
	a.cleanLegacySkillCopies(ctx)
	if _, err := os.Stat(filepath.Join(ws, ".claude", "skills", "review", "SKILL.md")); err != nil {
		t.Fatal("cleanup ran twice")
	}
}

func mustCurrent(t *testing.T, a *App, skillID string) string {
	t.Helper()
	v, err := a.Skills.ResolveVersion(context.Background(), tasksvc.SkillBinding{SkillID: skillID, Mode: "follow"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
