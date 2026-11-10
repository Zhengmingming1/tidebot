package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/contextplan"
	"github.com/Zhengmingming1/tidebot/internal/guard"
	"github.com/Zhengmingming1/tidebot/internal/skillstore"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/textclip"
)

// Skills tidebot manages are only ever loaded into runs tidebot starts. Each run
// gets its own stage directory under <DataDir>/skillstage/, laid out as a Claude
// Code plugin and passed with --plugin-dir; it is removed when the run ends.
// Nothing is written into the run's working directory (no <dir>/.claude/skills),
// and Claude Code's own state for tidebot runs lives in <DataDir>/claude-config
// (CLAUDE_CONFIG_DIR), so the Claude Code the owner uses directly never sees
// tidebot's skills and none of its files (~/.claude, ~/.claude.json, a
// project's .claude/settings*.json, .mcp.json, CLAUDE.md) are touched.

// skillStage is one run's staged skills.
type skillStage struct {
	Dir    string
	Skills []skillstore.StagedSkill
}

func (a *App) skillStageRoot() string { return filepath.Join(a.Cfg.DataDir, "skillstage") }

// claudeConfigDir is the CLAUDE_CONFIG_DIR of every run: Claude Code keeps its
// state (.claude.json, sessions, skill usage) there instead of the owner's home.
// Runs are --bare, which never reads the owner's login, so nothing is lost.
func (a *App) claudeConfigDir() string { return filepath.Join(a.Cfg.DataDir, "claude-config") }

// stageSkills writes bindings into a fresh stage directory. Close removes it.
func (a *App) stageSkills(ctx context.Context, bindings []tasksvc.SkillBinding) (*skillStage, error) {
	return a.stageSkillsWith(ctx, bindings, nil)
}

// stageSkillsWith stages bindings with some skills' files taken from drafts
// (skill id → files; see skillstore.StageWith).
func (a *App) stageSkillsWith(ctx context.Context, bindings []tasksvc.SkillBinding, drafts map[string]map[string][]byte) (*skillStage, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	if a.Skills == nil {
		return nil, fmt.Errorf("skills unavailable")
	}
	// Resolve follow bindings once so dependency validation and staging inspect
	// the same immutable versions even if a skill is updated concurrently.
	bindings, err := a.Skills.ResolveBindings(ctx, bindings)
	if err != nil {
		return nil, err
	}
	if err := a.Skills.ValidateDependencyBindings(ctx, bindings); err != nil {
		return nil, err
	}
	materials, err := a.Skills.ContextMaterials(ctx, bindings, "skill-stage")
	if err != nil {
		return nil, err
	}
	if _, err = (contextplan.Deterministic{}).Plan(ctx, contextplan.Request{Scope: "skill-stage", Budget: contextplan.Budget{MaxInputTokens: 2000000}, Materials: materials}); err != nil {
		return nil, err
	}
	root := a.skillStageRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return nil, err
	}
	skills, err := a.Skills.StageWith(ctx, dir, bindings, drafts)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &skillStage{Dir: dir, Skills: skills}, nil
}

// Close removes the stage directory (nil-safe).
func (st *skillStage) Close() {
	if st != nil && st.Dir != "" {
		_ = os.RemoveAll(st.Dir)
	}
}

// modernClaude reports whether the installed Claude Code loads skills from
// --plugin-dir in --bare runs and knows --no-session-persistence (2.1+; an
// unknown version is taken as current).
func (a *App) modernClaude() bool {
	if isMockBin(a.Cfg.ClaudeBin) {
		return true
	}
	v := a.claudeVersion()
	return v == "" || compareSemver(v, "2.1.0") >= 0
}

// Args loads the stage into a run. explicit means the prompt invokes the first
// skill as a slash command (see Invoke); otherwise every skill's instructions
// are appended to the system prompt, because --bare runs have no Skill tool the
// model could use to pick one itself. The stage is also an --add-dir so the
// skill's own files (scripts, references) can be read; it is tidebot's copy.
func (st *skillStage) Args(modern, explicit bool) []string {
	if st == nil || len(st.Skills) == 0 {
		return nil
	}
	return st.argsWithText(modern, explicit, st.contextText(modern, explicit))
}

func (st *skillStage) contextText(modern, explicit bool) string {
	if modern && explicit {
		return st.supportPrompt()
	}
	return st.systemPrompt()
}

func (st *skillStage) argsWithText(modern, explicit bool, text string) []string {
	args := []string{"--add-dir", filepath.Join(st.Dir, "skills")}
	if modern {
		args = append(args, "--plugin-dir", st.Dir)
	}
	if explicit && modern && len(st.Skills) == 1 {
		return args
	}
	if modern {
		p := filepath.Join(st.Dir, "skills-system-prompt.md")
		if os.WriteFile(p, []byte(text), 0o600) == nil {
			return append(args, "--append-system-prompt-file", p)
		}
	}
	const maxArg = 16 << 10 // command lines are limited (32K on Windows)
	if len(text) > maxArg {
		text = textclip.Head(text, maxArg) + "\n…(truncated)"
	}
	return append(args, "--append-system-prompt", text)
}

// Invoke prefixes prompt with the first skill's slash command (modern Claude
// Code) so Claude Code loads exactly that skill and passes the rest as
// $ARGUMENTS. Older versions get the skill through the system prompt instead.
func (st *skillStage) Invoke(modern bool, prompt string) string {
	if st == nil || len(st.Skills) == 0 || !modern {
		return prompt
	}
	return strings.TrimSpace(st.Skills[0].Command() + " " + prompt)
}

func (st *skillStage) systemPrompt() string {
	var b strings.Builder
	b.WriteString("# Skills attached by tidebot\n\nThe owner attached these skills to this run. Follow a skill's instructions when the task calls for it. A skill's own files are in its base directory.\n")
	if len(st.Skills) > 1 {
		fmt.Fprintf(&b, "\nPrimary skill: %s. Other skills provide auxiliary instructions within the same task; follow the primary procedure.\n", st.Skills[0].Name)
	}
	for _, s := range st.Skills {
		fmt.Fprintf(&b, "\n## Skill: %s\n\nBase directory for this skill: %s\n\n%s\n", s.Name, s.Dir, strings.TrimSpace(string(s.SkillMD)))
	}
	return b.String()
}

// isolationEnv keeps Claude Code's own state for tidebot runs out of the
// owner's home: CLAUDE_CONFIG_DIR points at tidebot's directory.
func (a *App) isolationEnv() []string {
	d := a.claudeConfigDir()
	_ = os.MkdirAll(d, 0o700)
	return []string{"CLAUDE_CONFIG_DIR=" + d}
}

// purgeSkillStages removes stage directories a crashed or force-quit core left
// behind. Called at startup, before any run can start.
func (a *App) purgeSkillStages() {
	ents, err := os.ReadDir(a.skillStageRoot())
	if err != nil {
		return
	}
	for _, e := range ents {
		_ = os.RemoveAll(filepath.Join(a.skillStageRoot(), e.Name()))
	}
	if len(ents) > 0 {
		a.Log.Info("removed leftover skill stages", "count", len(ents))
	}
	// Button runs of older versions used scratch folders next to the data; a
	// crash during a capability skill import (.cap-skill-*) or a dependency
	// check (dependency-check/analysis-*) leaves its scratch folder too. Each is
	// removed by its owner on return, so at startup none is in use.
	for _, pattern := range []string{"button-*", ".cap-skill-*", filepath.Join("dependency-check", "analysis-*")} {
		old, _ := filepath.Glob(filepath.Join(a.Cfg.DataDir, pattern))
		for _, p := range old {
			if fi, err := os.Lstat(p); err == nil && fi.IsDir() {
				_ = os.RemoveAll(p)
			}
		}
	}
}

// legacyCleanupKey marks that the one-time cleanup of skill copies older
// versions wrote into workspaces finished.
const legacyCleanupKey = "skills.legacy_cleanup_v1"

// cleanLegacySkillCopies removes skill folders tidebot up to 0.1.9 wrote into
// task workspaces (<dir>/.claude/skills/<name>/), where the Claude Code the
// owner runs in that folder would pick them up. Only exact, unmodified copies of
// a skill version tidebot stores are removed; everything else stays. Each
// decision is logged. It runs once; a workspace that can't be read (e.g. an
// unmounted drive) makes it run again at the next start.
func (a *App) cleanLegacySkillCopies(ctx context.Context) {
	if a.Skills == nil || a.DB == nil {
		return
	}
	var done bool
	a.loadJSONSetting(ctx, legacyCleanupKey, &done)
	if done {
		return
	}
	dirs := a.legacyWorkspaces(ctx)
	complete := true
	removed := 0
	for _, d := range dirs {
		res, err := a.Skills.CleanLegacyCopies(ctx, d)
		if err != nil {
			complete = false
			a.Log.Warn("legacy skill cleanup: workspace not readable", "workspace", d, "error", err)
			continue
		}
		for _, r := range res {
			if r.Removed {
				removed++
				a.Log.Info("legacy skill cleanup: removed tidebot's copy", "path", r.Path, "version", r.VersionID)
			} else {
				a.Log.Info("legacy skill cleanup: kept", "path", r.Path, "why", r.Reason)
			}
		}
	}
	if removed > 0 {
		a.Log.Info("legacy skill cleanup finished", "removed", removed, "workspaces", len(dirs))
	}
	if complete {
		_ = a.saveJSONSetting(ctx, legacyCleanupKey, true)
	}
}

// legacyWorkspaces lists every folder a task may have run in: each task's
// own folder, and every workspace_dir any version of a task's spec named.
func (a *App) legacyWorkspaces(ctx context.Context) []string {
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" || !filepath.IsAbs(d) {
			return
		}
		d = filepath.Clean(d)
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT id, COALESCE(workspace_dir,'') FROM tasks`)
	if err == nil {
		for rows.Next() {
			var id, ws string
			if rows.Scan(&id, &ws) == nil {
				add(ws)
				add(guard.TaskWorkspace(a.Cfg.HomeDir, id))
			}
		}
		rows.Close()
	}
	rows, err = a.DB.QueryContext(ctx, `SELECT DISTINCT COALESCE(json_extract(content_json,'$.workspace_dir'),'') FROM versions WHERE entity_type='task'`)
	if err == nil {
		for rows.Next() {
			var ws string
			if rows.Scan(&ws) == nil {
				add(ws)
			}
		}
		rows.Close()
	}
	return out
}

// CheckedArgs never silently drops instructions from a composition.
func (st *skillStage) CheckedArgs(modern, explicit bool) ([]string, error) {
	if st == nil || len(st.Skills) == 0 {
		return nil, nil
	}
	var instructionBytes int
	for _, skill := range st.Skills {
		instructionBytes += len(skill.SkillMD)
	}
	if instructionBytes > skillContextMaxBytes {
		return nil, fmt.Errorf("skill instructions exceed the %d KiB context budget (%d bytes); shorten SKILL.md or split the workflow; instructions were not truncated", skillContextMaxBytes>>10, instructionBytes)
	}
	text := st.contextText(modern, explicit)
	if !modern && len(text) > 16<<10 {
		return nil, fmt.Errorf("skill instructions exceed the legacy Claude limit; update Claude Code; instructions were not truncated")
	}
	args := st.argsWithText(modern, explicit, text)
	if modern && (!explicit || len(st.Skills) > 1) {
		found := false
		for _, arg := range args {
			if arg == "--append-system-prompt-file" {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("cannot write skill context file")
		}
	}
	return args, nil
}

func (st *skillStage) supportPrompt() string {
	if st == nil || len(st.Skills) < 2 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Skill composition\nThe primary skill is %s (%s). Complete the owner's task using that skill as the main procedure. The following auxiliary skills are available in this same run; use their instructions and supporting files when needed. They are supporting capabilities, not additional independent tasks. Do not assume a Skill tool is available; their instructions are provided below. Runtime permissions still apply.\n", st.Skills[0].Name, st.Skills[0].Command())
	for _, sk := range st.Skills[1:] {
		fmt.Fprintf(&b, "\n## Auxiliary skill: %s\nBase directory: %s\n\n%s\n", sk.Name, sk.Dir, string(sk.SkillMD))
	}
	return b.String()
}

func (st *skillStage) Bindings() []tasksvc.SkillBinding {
	if st == nil {
		return nil
	}
	out := make([]tasksvc.SkillBinding, 0, len(st.Skills))
	for _, sk := range st.Skills {
		if sk.Draft { // not a stored version: the run record names only real ones
			continue
		}
		out = append(out, tasksvc.SkillBinding{SkillID: sk.SkillID, SkillName: sk.DisplayName, VersionID: sk.VersionID, Mode: "pinned", Role: sk.Role})
	}
	return out
}

func (a *App) skillStart(st *skillStage, next func(string)) func(string) {
	return func(runID string) {
		if st != nil && len(st.Skills) > 0 {
			if err := a.Skills.RecordRunBindings(context.Background(), runID, st.Bindings()); err != nil {
				a.Log.Error("record skill snapshot", "run_id", runID, "error", err)
				a.Runner.Cancel(runID)
			}
		}
		if next != nil {
			next(runID)
		}
	}
}
