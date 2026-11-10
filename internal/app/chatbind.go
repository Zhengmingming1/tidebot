package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/plugin"
	"github.com/Zhengmingming1/tidebot/internal/sessiontools"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// A session is a binding object (capability plan §5.1): skills and
// capabilities bound to it are part of every later turn. Skills are staged per
// turn (follow mode: the current version) next to the skills of the run the
// session continues (pinned: the versions that run used), loaded the same way
// as in any run (--plugin-dir, the stage as an --add-dir, instructions in the
// system prompt). A session turn stays read-only whatever is bound: binding
// gives the model a skill's instructions and files and a capability's
// read-only tools, never a command or a side-effecting tool.

const chatBindMax = 16

type chatBindings struct {
	Skills       []string `json:"skills,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// parseChatBindings reads bindings_json ("[]" is the column's old default).
func parseChatBindings(raw string) chatBindings {
	var b chatBindings
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		_ = json.Unmarshal([]byte(raw), &b)
	}
	return b
}

// chatBindingViews names a session's bindings for the API. A skill or
// capability that was deleted since is shown as missing (and not staged).
func (a *App) chatBindingViews(ctx context.Context, raw string) []webui.ChatBinding {
	b := parseChatBindings(raw)
	out := []webui.ChatBinding{}
	for _, id := range b.Skills {
		v := webui.ChatBinding{Kind: "skill", ID: id}
		if err := a.DB.QueryRowContext(ctx, `SELECT name FROM skills WHERE id=?`, id).Scan(&v.Name); err != nil {
			v.Missing = true
		}
		out = append(out, v)
	}
	for _, id := range b.Capabilities {
		v := webui.ChatBinding{Kind: "capability", ID: id}
		if name, ok := a.capabilityTitle(ctx, id); ok {
			v.Name = name
		} else {
			v.Missing = true
		}
		out = append(out, v)
	}
	return out
}

// SetChatBindings replaces what a session is bound to; it applies from the
// next turn. Changes require an idle session so grant revocations are effective.
func (a *App) SetChatBindings(ctx context.Context, sid string, in webui.ChatBindingsInput) (webui.ChatSession, error) {
	if _, err := a.chatSession(ctx, sid); err != nil {
		return webui.ChatSession{}, err
	}
	b := chatBindings{}
	seen := map[string]bool{}
	for _, id := range in.Skills {
		id = strings.TrimSpace(id)
		if id == "" || seen["s"+id] {
			continue
		}
		seen["s"+id] = true
		var name string
		if err := a.DB.QueryRowContext(ctx, `SELECT name FROM skills WHERE id=?`, id).Scan(&name); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return webui.ChatSession{}, fmt.Errorf("%w: unknown skill", webui.ErrChatInvalid)
			}
			return webui.ChatSession{}, err
		}
		b.Skills = append(b.Skills, id)
	}
	for _, id := range in.Capabilities {
		id = strings.TrimSpace(id)
		if id == "" || seen["c"+id] {
			continue
		}
		seen["c"+id] = true
		if _, ok := a.capabilityTitle(ctx, id); !ok {
			return webui.ChatSession{}, fmt.Errorf("%w: unknown capability", webui.ErrChatInvalid)
		}
		b.Capabilities = append(b.Capabilities, id)
	}
	if len(b.Skills) > chatBindMax || len(b.Capabilities) > chatBindMax {
		return webui.ChatSession{}, fmt.Errorf("%w: too many bindings", webui.ErrChatInvalid)
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return webui.ChatSession{}, err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM chat_sessions WHERE id=?`, sid).Scan(&status); err != nil {
		return webui.ChatSession{}, err
	}
	if status != "idle" {
		return webui.ChatSession{}, capError(409, "session_busy", "change bindings when the session is idle")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM capability_bindings WHERE owner_kind='session' AND owner_id=?`, sid); err != nil {
		return webui.ChatSession{}, err
	}
	for _, capID := range b.Capabilities {
		result, e := tx.ExecContext(ctx, `INSERT INTO capability_bindings(owner_kind,owner_id,capability_id,version_id,created_at) SELECT 'session',?,id,'',? FROM capabilities WHERE id=? AND status='installed'`, sid, nowUTC(), capID)
		if e != nil {
			return webui.ChatSession{}, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return webui.ChatSession{}, e
		}
		if n != 1 {
			return webui.ChatSession{}, fmt.Errorf("%w: unknown capability", webui.ErrChatInvalid)
		}
	}
	// Removed bindings revoke their grants permanently; re-binding needs review.
	if _, err = tx.ExecContext(ctx, `DELETE FROM capability_tool_grants WHERE session_id=? AND capability_id NOT IN (SELECT capability_id FROM capability_bindings WHERE owner_kind='session' AND owner_id=?)`, sid, sid); err != nil {
		return webui.ChatSession{}, err
	}
	raw, _ := json.Marshal(b)
	if _, err = tx.ExecContext(ctx, `UPDATE chat_sessions SET bindings_json=?, updated_at=? WHERE id=?`, string(raw), nowUTC(), sid); err != nil {
		return webui.ChatSession{}, err
	}
	if err = tx.Commit(); err != nil {
		return webui.ChatSession{}, err
	}
	c, err := a.chatSession(ctx, sid)
	return c.ChatSession, err
}

// chatTurnSkills are the skills a turn stages: the run the session continues
// used (pinned), then the session's own (follow), without repeats.
func (a *App) chatTurnSkills(ctx context.Context, c chatRow) []tasksvc.SkillBinding {
	var out []tasksvc.SkillBinding
	seen := map[string]bool{}
	if c.OriginRunID != "" && a.Skills != nil {
		if rb, err := a.Skills.RunBindings(ctx, c.OriginRunID); err == nil {
			for _, x := range rb {
				if !seen[x.SkillID] {
					seen[x.SkillID] = true
					out = append(out, tasksvc.SkillBinding{SkillID: x.SkillID, Mode: "pinned", VersionID: x.VersionID})
				}
			}
		}
	}
	for _, id := range parseChatBindings(c.bindingsRaw).Skills {
		if !seen[id] {
			seen[id] = true
			out = append(out, tasksvc.SkillBinding{SkillID: id, Mode: "follow"})
		}
	}
	for _, id := range parseChatBindings(c.bindingsRaw).Capabilities {
		rec, err := a.caps().Get(ctx, id)
		if err != nil || a.caps().Verify(ctx, id, rec.VersionID) != nil {
			continue
		}
		for _, component := range rec.Lock.Components {
			if component.Disabled || component.Type != "skill" && component.Type != "command" || seen[component.SkillID] {
				continue
			}
			files, e := a.Skills.Files(ctx, component.VersionID)
			if e != nil {
				continue
			}
			_, hash, e := capability.HashFiles(files)
			want := component.FilesSHA256
			if want == "" && rec.Lock.Source.Adapter == "skill-dir" {
				want = rec.Lock.FilesSHA256
			}
			if e != nil || want == "" || hash != want {
				continue
			}
			seen[component.SkillID] = true
			out = append(out, tasksvc.SkillBinding{SkillID: component.SkillID, Mode: "pinned", VersionID: component.VersionID})
		}
	}
	if len(out) > chatBindMax {
		out = out[:chatBindMax]
	}
	return out
}

// stageChatSkills stages a turn's skills. A pinned version of the origin run
// that is gone since doesn't block the turn: the session's own skills are
// staged without it, and failing that the turn runs without skills.
func (a *App) stageChatSkills(ctx context.Context, c chatRow) *skillStage {
	all := a.chatTurnSkills(ctx, c)
	if len(all) == 0 {
		return nil
	}
	st, err := a.stageSkills(ctx, all)
	if err == nil {
		return st
	}
	var follow []tasksvc.SkillBinding
	for _, b := range all {
		if b.Mode == "follow" {
			follow = append(follow, b)
		}
	}
	if len(follow) > 0 && len(follow) < len(all) {
		if st, err2 := a.stageSkills(ctx, follow); err2 == nil {
			return st
		}
	}
	a.Log.Warn("session turn: skills not staged", "session", c.ID, "error", err)
	return nil
}

const chatSkillNote = "\nSkills are attached to this session for reference: read their instructions and files, but this session cannot run their commands. If a skill's procedure needs a command, say so: the owner can switch the session to 「可执行命令」 or run it as a task."

// chatSkillExecNote: in a command session a skill's scripts can run.
const chatSkillExecNote = "\nSkills are attached to this session: follow their instructions; their scripts may be run as commands in the working folder."

// chatToolsServer is the session's read-only "tidebot" MCP server: find and
// search files in the folders the turn may read (and, with a token, the
// capability tools).
func chatToolsServer(roots []sessiontools.Root, env map[string]string) map[string]any {
	if plugin.BuiltinExe == "" {
		return nil
	}
	e := map[string]any{sessiontools.EnvRoots: sessiontools.FormatRoots(roots)}
	for k, v := range env {
		e[k] = v
	}
	return map[string]any{"command": plugin.BuiltinExe, "args": []string{sessiontools.Arg}, "env": e}
}

func chatRoots(ws string, st *skillStage) []sessiontools.Root {
	roots := []sessiontools.Root{{Label: "work", Dir: ws}}
	if st != nil && st.Dir != "" {
		if fi, err := os.Stat(filepath.Join(st.Dir, "skills")); err == nil && fi.IsDir() {
			roots = append(roots, sessiontools.Root{Label: "skills", Dir: filepath.Join(st.Dir, "skills")})
		}
	}
	return roots
}
