package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/contextplan"
	"github.com/Zhengmingming1/tidebot/internal/cost"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

type capabilityOwner struct{ Kind, ID string }
type capabilityOwnerKey struct{}

func (a *App) capabilityRuntimeRecords(ctx context.Context, opts *runner.Options) ([]capability.Record, error) {
	owners := []capabilityOwner{}
	if opts.TaskID != "" {
		owners = append(owners, capabilityOwner{"task", opts.TaskID})
	}
	if owner, ok := ctx.Value(capabilityOwnerKey{}).(capabilityOwner); ok {
		owners = append(owners, owner)
	}
	out := []capability.Record{}
	seen := map[string]string{}
	for _, o := range owners {
		rows, err := a.DB.QueryContext(ctx, `SELECT capability_id,version_id FROM capability_bindings WHERE owner_kind=? AND owner_id=? ORDER BY capability_id`, o.Kind, o.ID)
		if err != nil {
			return nil, err
		}
		bs := []capability.Binding{}
		for rows.Next() {
			b := capability.Binding{}
			if err = rows.Scan(&b.CapabilityID, &b.VersionID); err != nil {
				rows.Close()
				return nil, err
			}
			bs = append(bs, b)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, b := range bs {
			r, e := a.caps().ResolveBinding(ctx, b)
			if e != nil {
				return nil, e
			}
			if v, ok := seen[r.ID]; ok {
				if v != r.VersionID {
					return nil, errors.New("conflicting capability version bindings")
				}
				continue
			}
			seen[r.ID] = r.VersionID
			out = append(out, r)
		}
	}
	if len(out) > 16 {
		return nil, errors.New("at most 16 capabilities may be bound")
	}
	return out, nil
}

var capabilityEnvName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

func allowedCapabilityEnv(n string) bool {
	if !capabilityEnvName.MatchString(n) {
		return false
	}
	for _, p := range []string{"CLAUDE_", "ANTHROPIC_", "TIDEBOT_", "LD_", "DYLD_", "GIT_", "NODE_OPTIONS", "PYTHONPATH", "PYTHONHOME"} {
		if strings.HasPrefix(n, p) {
			return false
		}
	}
	switch n {
	case "PATH", "HOME", "SHELL", "ENV", "BASH_ENV", "ZDOTDIR", "COMSPEC", "SYSTEMROOT", "USERPROFILE":
		return false
	}
	return true
}
func setCapabilityEnv(env []string, n, v string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, n+"=") {
			out = append(out, e)
		}
	}
	return append(out, n+"="+v)
}
func appendCapabilityTools(opts *runner.Options, tools []string) {
	if len(tools) == 0 {
		return
	}
	for _, args := range []*[]string{&opts.ExtraArgs, &opts.ContextArgs} {
		for i := 0; i+1 < len(*args); i++ {
			if (*args)[i] == "--allowedTools" {
				copyArgs := append([]string{}, (*args)...)
				copyArgs[i+1] += "," + strings.Join(tools, ",")
				*args = copyArgs
				return
			}
		}
	}
	opts.ContextArgs = append(opts.ContextArgs, "--allowedTools", strings.Join(tools, ","))
}

// applyCapabilityRuntime adds only explicitly bound snapshots to this invocation.
// The caller keeps cleanup alive through queued execution and process exit.
func (a *App) applyCapabilityRuntime(ctx context.Context, opts *runner.Options) (func(), error) {
	cleanups := []func(){}
	noop := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
	success := false
	defer func() {
		if !success {
			noop()
		}
	}()
	records, err := a.capabilityRuntimeRecords(ctx, opts)
	if err != nil {
		return noop, err
	}
	if len(records) == 0 {
		return noop, nil
	}
	owner, _ := ctx.Value(capabilityOwnerKey{}).(capabilityOwner)
	var validateGrants func(context.Context, *sql.Tx) error
	if owner.Kind == "session" {
		cleanup, validate, err := a.applySessionCapabilityGrants(ctx, opts, owner.ID, records)
		if err != nil {
			return noop, err
		}
		cleanups = append(cleanups, cleanup)
		validateGrants = validate
	}
	bindings := []tasksvc.SkillBinding{}
	paths := []string{}
	seenSkills := map[string]string{}
	if opts.ContextRequest != nil {
		for _, m := range opts.ContextRequest.Materials {
			if m.Kind == "skill" && strings.HasPrefix(m.ID, "skill:") {
				seenSkills[strings.TrimPrefix(m.ID, "skill:")] = m.Version
			}
		}
	}
	tools := []string{}
	for _, r := range records {
		if e := a.verifyManagedCapabilityDependencies(ctx, r.Lock); e != nil {
			return noop, e
		}
		if owner.Kind != "session" {
			cleanup, e := a.applyCapabilityMCP(ctx, opts, r)
			if e != nil {
				return noop, e
			}
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}
		}
		for _, c := range r.Lock.Components {
			if c.Disabled || (c.Type != "skill" && c.Type != "command") {
				continue
			}
			if version, ok := seenSkills[c.SkillID]; ok {
				if version != c.VersionID {
					return noop, errors.New("conflicting bound skill versions")
				}
				continue
			}
			files, e := a.Skills.Files(ctx, c.VersionID)
			if e != nil {
				return noop, e
			}
			_, hash, e := capability.HashFiles(files)
			if e != nil {
				return noop, e
			}
			// Single-skill snapshots use the lock's hash. Multi-component snapshots
			// must provide per-component hashes before they can be launched.
			expected := r.Lock.FilesSHA256
			if c.FilesSHA256 != "" {
				expected = c.FilesSHA256
			}
			if hash != expected {
				return noop, fmt.Errorf("capability %s skill files changed", r.ID)
			}
			seenSkills[c.SkillID] = c.VersionID
			bindings = append(bindings, tasksvc.SkillBinding{SkillID: c.SkillID, Mode: "pinned", VersionID: c.VersionID})
		}
		for _, resolved := range r.Lock.Resolved {
			if resolved.Path == "" {
				continue
			}
			if resolved.Status != "ready" {
				continue
			}
			if resolved.Source != "system" && resolved.Source != "managed" {
				return noop, errors.New("unknown capability tool source")
			}
			if !filepath.IsAbs(resolved.Path) {
				return noop, errors.New("capability command path must be absolute")
			}
			// System commands are reused; managed resolution is restricted to data.
			if resolved.Source == "managed" {
				rel, e := filepath.Rel(a.Cfg.DataDir, resolved.Path)
				if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
					return noop, errors.New("managed command outside tidebot data")
				}
			}
			paths = append(paths, filepath.Dir(resolved.Path))
		}
		if !opts.KeepSession && opts.RunMode != "read" {
			tools = append(tools, r.Lock.Permissions.Tools...)
		}
		for _, s := range r.Lock.Requires.Secrets {
			if capabilityMCPReferencesSecret(r.Lock, s.Name) {
				continue
			}
			if !allowedCapabilityEnv(s.Name) {
				return noop, fmt.Errorf("reserved capability environment name %s", s.Name)
			}
			if a.Secrets == nil {
				if s.Optional {
					continue
				}
				return noop, errors.New("secret store unavailable")
			}
			v, e := a.Secrets.Get("cap:" + r.ID + ":" + s.Name)
			if e != nil || v == "" {
				if s.Optional {
					continue
				}
				return noop, fmt.Errorf("capability %s requires secret %s", r.ID, s.Name)
			}
			opts.EnvBase = setCapabilityEnv(opts.EnvBase, s.Name, v)
		}
	}
	st, err := a.stageSkills(ctx, bindings)
	if err != nil {
		return noop, err
	}
	if st != nil {
		cleanups = append(cleanups, st.Close)
		manifest, _ := json.Marshal(map[string]string{"name": "tidebot-capabilities", "description": "Reviewed capability skills for this run"})
		if e := os.WriteFile(filepath.Join(st.Dir, ".claude-plugin", "plugin.json"), manifest, 0600); e != nil {
			return noop, e
		}
		args, e := st.CheckedArgs(a.modernClaude(), false)
		if e != nil {
			st.Close()
			return noop, e
		}
		opts.ContextArgs = append(opts.ContextArgs, args...)
		req := st.contextRequest(a.modernClaude(), false)
		if req != nil {
			for i := range req.Materials {
				if req.Materials[i].ID == "skill-wrappers" {
					req.Materials[i].ID = "capability-skill-wrappers"
				}
			}
			if opts.ContextRequest == nil {
				opts.ContextRequest = &contextplan.Request{Scope: "execution"}
			} else {
				v := *opts.ContextRequest
				v.Materials = append([]contextplan.Material{}, v.Materials...)
				opts.ContextRequest = &v
			}
			opts.ContextRequest.Materials = append(opts.ContextRequest.Materials, req.Materials...)
		}
	}
	if len(paths) > 0 {
		old := os.Getenv("PATH")
		for _, e := range opts.EnvBase {
			if strings.HasPrefix(e, "PATH=") {
				old = strings.TrimPrefix(e, "PATH=")
			}
		}
		if old != "" {
			paths = append(paths, old)
		}
		opts.EnvBase = setCapabilityEnv(opts.EnvBase, "PATH", strings.Join(paths, string(os.PathListSeparator)))
	}
	appendCapabilityTools(opts, tools)
	previous := opts.AtStart
	opts.AtStart = func(c context.Context, o *runner.Options) error {
		for _, r := range records {
			if e := a.verifyManagedCapabilityDependencies(c, r.Lock); e != nil {
				return e
			}
			if e := a.caps().Verify(c, r.ID, r.VersionID); e != nil {
				return e
			}
		}
		// Preserve the caller's launch validation before recording run provenance.
		if previous != nil {
			if e := previous(c, o); e != nil {
				return e
			}
		}
		tx, e := a.DB.BeginTx(c, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		// Grants and binding pins share one transaction: revocation cannot
		// interleave between validating the reviewed tools and recording the run.
		if validateGrants != nil {
			if e := validateGrants(c, tx); e != nil {
				return e
			}
		}
		owner, _ := ctx.Value(capabilityOwnerKey{}).(capabilityOwner)
		for _, r := range records {
			result, err := tx.ExecContext(c, `INSERT INTO capability_run_bindings(run_id,capability_id,version_id)
             SELECT ?,c.id,? FROM capabilities c WHERE c.id=? AND c.status='installed' AND EXISTS(
              SELECT 1 FROM capability_bindings b WHERE b.capability_id=c.id
              AND ((b.owner_kind='task' AND b.owner_id=?) OR (b.owner_kind=? AND b.owner_id=?))
              AND ((b.version_id='' AND c.current_version_id=?) OR b.version_id=?)
              AND (b.owner_kind!='session' OR EXISTS(SELECT 1 FROM chat_sessions s WHERE s.id=b.owner_id AND s.status!='closed'
               AND EXISTS(SELECT 1 FROM json_each(s.bindings_json,'$.capabilities') WHERE value=c.id))))`, o.RunID, r.VersionID, r.ID, o.TaskID, owner.Kind, owner.ID, r.VersionID, r.VersionID)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return errors.New("capability binding was revoked while run waited")
			}
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		return nil
	}
	success = true
	return noop, nil
}

// Button bindings apply in addition to a linked task's own bindings.
func (a *App) startCapabilityButton(id, taskID, prompt, provider, skillID, versionID string) (string, error) {
	ctx := context.WithValue(context.Background(), capabilityOwnerKey{}, capabilityOwner{"button", id})
	if taskID != "" {
		task, err := a.Store.GetTask(ctx, taskID)
		if err != nil {
			return "", err
		}
		return launch(startWait, func(start func(string)) error {
			return a.runTaskTriggerOn(ctx, *task, "manual", cost.BucketProduction, start)
		}, func(err error) { a.Log.Warn("button task", "error", err) })
	}
	if _, err := a.ResolveProfile(provider); err != nil {
		return "", err
	}
	var st *skillStage
	if skillID != "" {
		mode := "follow"
		if versionID != "" {
			mode = "pinned"
		}
		var err error
		st, err = a.stageSkills(ctx, []tasksvc.SkillBinding{{SkillID: skillID, Mode: mode, VersionID: versionID}})
		if err != nil {
			return "", err
		}
	}
	return launch(startWait, func(start func(string)) error {
		defer st.Close()
		opts := runner.Options{Prompt: prompt, DisplayPrompt: prompt, TriggerKind: "button", CostBucket: cost.BucketProduction, OnStart: start}
		if st != nil {
			args, e := st.CheckedArgs(a.modernClaude(), true)
			if e != nil {
				return e
			}
			opts.ContextArgs = args
			opts.Prompt = st.Invoke(a.modernClaude(), prompt)
			opts.ContextRequest = st.contextRequest(a.modernClaude(), true)
			opts.OnStart = a.skillStart(st, start)
		}
		_, err := a.RunPromptEx(ctx, opts, provider, 0)
		return err
	}, func(err error) { a.Log.Warn("capability button", "error", err) })
}

// CapabilityBindingRequest is owner-only; it is never in the session MCP dispatch.
func (a *App) CapabilityBindingRequest(ctx context.Context, method string, b capability.Binding) (any, error) {
	if method == "GET" {
		rows, e := a.DB.QueryContext(ctx, `SELECT owner_kind,owner_id,capability_id,version_id FROM capability_bindings WHERE capability_id=? ORDER BY owner_kind,owner_id`, b.CapabilityID)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		out := []capability.Binding{}
		for rows.Next() {
			v := capability.Binding{}
			if e = rows.Scan(&v.OwnerKind, &v.OwnerID, &v.CapabilityID, &v.VersionID); e != nil {
				return nil, e
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
	if b.OwnerKind != "task" && b.OwnerKind != "workflow_node" && b.OwnerKind != "button" {
		return nil, capError(400, "invalid_owner", "invalid capability binding owner")
	}
	if method == "DELETE" {
		return map[string]bool{"ok": true}, a.caps().Unbind(ctx, b)
	}
	if _, err := a.caps().Get(ctx, b.CapabilityID); err != nil {
		return nil, err
	}
	targets, err := a.CapabilityBindingTargets(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, t := range targets {
		if t.Kind == b.OwnerKind && t.ID == b.OwnerID {
			found = true
			break
		}
	}
	if !found {
		return nil, capError(400, "invalid_owner", "binding owner does not exist")
	}
	var count int
	if err = a.DB.QueryRowContext(ctx, `SELECT count(*) FROM capability_bindings WHERE owner_kind=? AND owner_id=? AND capability_id<>?`, b.OwnerKind, b.OwnerID, b.CapabilityID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 16 {
		return nil, capError(400, "binding_limit", "at most 16 capabilities per owner")
	}
	return map[string]bool{"ok": true}, a.caps().Bind(ctx, b)
}
func (a *App) CapabilityBindingTargets(ctx context.Context) ([]webui.CapabilityTarget, error) {
	out := []webui.CapabilityTarget{}
	rows, err := a.DB.QueryContext(ctx, `SELECT id,name FROM tasks WHERE status <> 'deleted'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		t := webui.CapabilityTarget{Kind: "task"}
		if err = rows.Scan(&t.ID, &t.Name); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	rows, err = a.DB.QueryContext(ctx, `SELECT w.id,w.name,v.definition_json FROM workflows w JOIN workflow_versions v ON v.workflow_id=w.id AND v.version=w.version WHERE w.deleted=0`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name, raw string
		if err = rows.Scan(&id, &name, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var d struct {
			Nodes []struct{ ID, Name, Kind string }
		}
		if json.Unmarshal([]byte(raw), &d) == nil {
			for _, n := range d.Nodes {
				if n.Kind == "skill" {
					out = append(out, webui.CapabilityTarget{Kind: "workflow_node", ID: id + ":" + n.ID, Name: name + " / " + n.Name})
				}
			}
		}
	}
	rows.Close()
	raw, err := a.Auth.GetSetting(ctx, "buttons")
	if err != nil {
		return nil, err
	}
	var buttons []struct{ ID, Label string }
	if json.Unmarshal([]byte(raw), &buttons) == nil {
		for _, b := range buttons {
			out = append(out, webui.CapabilityTarget{Kind: "button", ID: b.ID, Name: b.Label})
		}
	}
	return out, nil
}
