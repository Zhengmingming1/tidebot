package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/capability/adapters"
	"github.com/Zhengmingming1/tidebot/internal/runner"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

var capabilityGrantToolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type capabilityGrantView struct {
	CapabilityID  string             `json:"capability_id"`
	VersionID     string             `json:"version_id"`
	Component     string             `json:"component"`
	DiscoveryHash string             `json:"discovery_hash"`
	Tools         []adapters.MCPTool `json:"tools"`
	GrantedTools  []string           `json:"granted_tools"`
	TestedAt      string             `json:"tested_at"`
}

// Discovery is inert review data. Server names and annotations never grant access.
func (a *App) saveCapabilityDiscovery(ctx context.Context, record capability.Record, component capability.Component, tools []adapters.MCPTool) error {
	seen := map[string]bool{}
	if len(tools) > 256 {
		return errors.New("too many discovered tools")
	}
	for _, t := range tools {
		if !capabilityGrantToolName.MatchString(t.Name) || seen[t.Name] {
			return errors.New("invalid discovered tool name")
		}
		seen[t.Name] = true
	}
	tools = append([]adapters.MCPTool{}, tools...)
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	raw, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	if len(raw) > 4<<20 {
		return errors.New("discovery exceeds size limit")
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM capability_tool_grants WHERE version_id=? AND component=? AND discovery_hash<>?`, record.VersionID, component.Name, hash); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO capability_tool_discovery(version_id,component,tools_json,discovery_hash,tested_at) VALUES(?,?,?,?,?) ON CONFLICT(version_id,component) DO UPDATE SET tools_json=excluded.tools_json,discovery_hash=excluded.discovery_hash,tested_at=excluded.tested_at`, record.VersionID, component.Name, string(raw), hash, nowUTC())
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) capabilityGrantViews(ctx context.Context, sid string) ([]capabilityGrantView, error) {
	session, err := a.chatSession(ctx, sid)
	if err != nil {
		return nil, err
	}
	out := []capabilityGrantView{}
	for _, id := range parseChatBindings(session.bindingsRaw).Capabilities {
		record, err := a.caps().Get(ctx, id)
		if err != nil {
			return nil, err
		}
		rows, err := a.DB.QueryContext(ctx, `SELECT d.component,d.discovery_hash,d.tools_json,d.tested_at,COALESCE(g.tools_json,'[]') FROM capability_tool_discovery d LEFT JOIN capability_tool_grants g ON g.version_id=d.version_id AND g.component=d.component AND g.discovery_hash=d.discovery_hash AND g.session_id=? AND g.capability_id=? WHERE d.version_id=? ORDER BY d.component`, sid, id, record.VersionID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			v := capabilityGrantView{CapabilityID: id, VersionID: record.VersionID}
			var raw, grants string
			if err = rows.Scan(&v.Component, &v.DiscoveryHash, &raw, &v.TestedAt, &grants); err != nil {
				rows.Close()
				return nil, err
			}
			if err = json.Unmarshal([]byte(raw), &v.Tools); err != nil {
				rows.Close()
				return nil, err
			}
			if err = json.Unmarshal([]byte(grants), &v.GrantedTools); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CapabilityGrantRequest is only exposed through the authenticated owner WebUI.
// Exact discovery hashes require the UI to review the same metadata it grants.
func (a *App) CapabilityGrantRequest(ctx context.Context, method string, req webui.CapabilityGrantRequest) (any, error) {
	if method == "GET" {
		return a.capabilityGrantViews(ctx, req.SessionID)
	}
	if method != "PUT" && method != "DELETE" {
		return nil, capError(405, "method", "unsupported method")
	}
	if req.SessionID == "" || req.CapabilityID == "" || req.VersionID == "" || req.Component == "" {
		return nil, capError(400, "invalid_grant", "grant scope required")
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var bindings, status string
	if err = tx.QueryRowContext(ctx, `SELECT bindings_json,status FROM chat_sessions WHERE id=?`, req.SessionID).Scan(&bindings, &status); err != nil {
		return nil, err
	}
	if status != "idle" {
		return nil, capError(409, "session_busy", "change grants when the session is idle")
	}
	if method == "DELETE" {
		_, err = tx.ExecContext(ctx, `DELETE FROM capability_tool_grants WHERE session_id=? AND capability_id=? AND version_id=? AND component=?`, req.SessionID, req.CapabilityID, req.VersionID, req.Component)
		if err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, tx.Commit()
	}
	bound := false
	for _, id := range parseChatBindings(bindings).Capabilities {
		if id == req.CapabilityID {
			bound = true
		}
	}
	if !bound {
		return nil, capError(409, "not_bound", "capability must be bound to this session")
	}
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT current_version_id FROM capabilities WHERE id=? AND status='installed'`, req.CapabilityID).Scan(&current); err != nil {
		return nil, err
	}
	if current != req.VersionID {
		return nil, capError(409, "version_changed", "review the current capability version")
	}
	var raw, hash string
	if err = tx.QueryRowContext(ctx, `SELECT tools_json,discovery_hash FROM capability_tool_discovery WHERE version_id=? AND component=?`, current, req.Component).Scan(&raw, &hash); err != nil {
		return nil, capError(409, "discovery_required", "test this capability before granting tools")
	}
	if req.DiscoveryHash == "" || hash != req.DiscoveryHash {
		return nil, capError(409, "discovery_changed", "review the latest discovered tools")
	}
	var tools []adapters.MCPTool
	if err = json.Unmarshal([]byte(raw), &tools); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, tool := range tools {
		known[tool.Name] = true
	}
	chosen := map[string]bool{}
	names := []string{}
	for _, name := range req.Tools {
		if !capabilityGrantToolName.MatchString(name) || !known[name] || chosen[name] {
			return nil, capError(400, "unknown_tool", "grant only exact discovered tool names")
		}
		chosen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	data, _ := json.Marshal(names)
	_, err = tx.ExecContext(ctx, `INSERT INTO capability_tool_grants(session_id,capability_id,version_id,component,discovery_hash,tools_json,granted_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(session_id,capability_id,version_id,component) DO UPDATE SET discovery_hash=excluded.discovery_hash,tools_json=excluded.tools_json,granted_at=excluded.granted_at`, req.SessionID, req.CapabilityID, current, req.Component, hash, string(data), nowUTC())
	if err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, tx.Commit()
}

type capabilityGrantSessionKey struct{}

func (a *App) reviewedCapabilityTools(ctx context.Context, record capability.Record, component capability.Component) (allowed, denied []string, err error) {
	sid, _ := ctx.Value(capabilityGrantSessionKey{}).(string)
	if sid == "" {
		return nil, nil, nil
	}
	views, err := a.capabilityGrantViews(ctx, sid)
	if err != nil {
		return nil, nil, err
	}
	for _, v := range views {
		if v.CapabilityID != record.ID || v.VersionID != record.VersionID || v.Component != component.Name {
			continue
		}
		grants := map[string]bool{}
		for _, name := range v.GrantedTools {
			if !capabilityGrantToolName.MatchString(name) {
				return nil, nil, errors.New("invalid persisted tool grant")
			}
			grants[name] = true
		}
		prefix := "mcp__cap_" + capKey(record.ID+"\x00"+component.Name) + "__"
		for _, tool := range v.Tools {
			if grants[tool.Name] {
				allowed = append(allowed, prefix+tool.Name)
			} else {
				denied = append(denied, prefix+tool.Name)
			}
		}
	}
	return
}

func appendCapabilityDeniedTools(opts *runner.Options, tools []string) {
	if len(tools) == 0 {
		return
	}
	for _, args := range []*[]string{&opts.ExtraArgs, &opts.ContextArgs} {
		for i := 0; i+1 < len(*args); i++ {
			if (*args)[i] == "--disallowedTools" {
				copyArgs := append([]string{}, (*args)...)
				copyArgs[i+1] += "," + strings.Join(tools, ",")
				*args = copyArgs
				return
			}
		}
	}
	opts.ContextArgs = append(opts.ContextArgs, "--disallowedTools", strings.Join(tools, ","))
}

// applySessionCapabilityMCP prepares the full session runtime for direct callers.
// Production turns enter through RunPromptEx after its environment is initialized.
func (a *App) applySessionCapabilityMCP(ctx context.Context, opts *runner.Options, sid string) (func(), error) {
	ctx = context.WithValue(ctx, capabilityOwnerKey{}, capabilityOwner{"session", sid})
	return a.applyCapabilityRuntime(ctx, opts)
}

// Apply exact reviewed MCP scopes to the same snapshots used by the runtime.
func (a *App) applySessionCapabilityGrants(ctx context.Context, opts *runner.Options, sid string, snapshots []capability.Record) (func(), func(context.Context, *sql.Tx) error, error) {
	ctx = context.WithValue(ctx, capabilityGrantSessionKey{}, sid)
	views, err := a.capabilityGrantViews(ctx, sid)
	if err != nil {
		return func() {}, nil, err
	}
	active := []capabilityGrantView{}
	records := map[string]capability.Record{}
	for _, record := range snapshots {
		for _, v := range views {
			if v.CapabilityID == record.ID && v.VersionID == record.VersionID && len(v.GrantedTools) > 0 {
				active = append(active, v)
				records[record.ID] = record
			}
		}
	}
	cleanups := []func(){}
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
	for _, record := range records {
		if e := a.caps().Verify(ctx, record.ID, record.VersionID); e != nil {
			cleanup()
			return func() {}, nil, e
		}
		close, e := a.applyCapabilityMCP(ctx, opts, record)
		if e != nil {
			cleanup()
			return func() {}, nil, e
		}
		cleanups = append(cleanups, close)
	}
	if len(active) == 0 {
		return cleanup, nil, nil
	}
	for i := 0; i+1 < len(opts.ContextArgs); i++ {
		if opts.ContextArgs[i] == "--append-system-prompt" {
			opts.ContextArgs[i+1] = strings.Replace(opts.ContextArgs[i+1], "Tools here are read-only.", "Local files are read-only. The owner has approved exact external tools for this session; those tools may modify external data. Use them only within the owner's request.", 1)
		}
	}
	// dontAsk denies tools absent from the exact allowlist, including tools newly
	// advertised after discovery. Explicit deny rules preserve all file restrictions.
	opts.ContextArgs = append(opts.ContextArgs, "--permission-mode", "dontAsk")
	validate := func(c context.Context, tx *sql.Tx) error {
		for _, v := range active {
			raw, _ := json.Marshal(v.GrantedTools)
			var n int
			e := tx.QueryRowContext(c, `SELECT count(*) FROM capability_tool_grants g JOIN capability_tool_discovery d ON d.version_id=g.version_id AND d.component=g.component AND d.discovery_hash=g.discovery_hash JOIN capabilities cap ON cap.id=g.capability_id AND cap.current_version_id=g.version_id AND cap.status='installed' JOIN chat_sessions s ON s.id=g.session_id WHERE g.session_id=? AND g.capability_id=? AND g.version_id=? AND g.component=? AND g.discovery_hash=? AND g.tools_json=? AND s.status!='closed' AND EXISTS(SELECT 1 FROM json_each(s.bindings_json,'$.capabilities') WHERE value=cap.id)`, sid, v.CapabilityID, v.VersionID, v.Component, v.DiscoveryHash, string(raw)).Scan(&n)
			if e != nil {
				return e
			}
			if n != 1 {
				return errors.New("capability tool grant changed while run waited")
			}
		}
		return nil
	}
	return cleanup, validate, nil
}
