package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/id"
)

// CapabilityDefaults lists owner-selected defaults, not tool permissions.
func (a *App) CapabilityDefaults(ctx context.Context) ([]string, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT d.capability_id FROM capability_session_defaults d JOIN capabilities c ON c.id=d.capability_id WHERE c.status='installed' ORDER BY d.capability_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (a *App) SetCapabilityDefault(ctx context.Context, capID string, enabled bool) (any, error) {
	capID = strings.TrimSpace(capID)
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var version string
	if err = tx.QueryRowContext(ctx, `SELECT current_version_id FROM capabilities WHERE id=? AND status='installed'`, capID).Scan(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, capError(404, "not_found", "installed capability not found")
		}
		return nil, err
	}
	if enabled {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM capability_session_defaults WHERE capability_id<>?`, capID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= chatBindMax {
			return nil, capError(400, "binding_limit", "at most 16 default capabilities")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO capability_session_defaults(capability_id,created_at) VALUES(?,?) ON CONFLICT(capability_id) DO NOTHING`, capID, nowUTC())
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM capability_session_defaults WHERE capability_id=?`, capID)
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO capability_events(id,capability_id,version_id,actor,action,result,created_at) VALUES(?,?,?,?,?,?,?)`, id.New("ce"), capID, version, "ui", "session_default", map[bool]string{true: "enabled", false: "disabled"}[enabled], nowUTC()); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true, "enabled": enabled}, tx.Commit()
}

// applyCapabilityDefaults runs inside the same transaction that creates the
// session. An installed package is attached, but no MCP grant is copied.
func applyCapabilityDefaults(ctx context.Context, tx *sql.Tx, sid string) error {
	rows, err := tx.QueryContext(ctx, `SELECT d.capability_id FROM capability_session_defaults d JOIN capabilities c ON c.id=d.capability_id WHERE c.status='installed' ORDER BY d.capability_id`)
	if err != nil {
		return err
	}
	b := chatBindings{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		b.Capabilities = append(b.Capabilities, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(b.Capabilities) > chatBindMax {
		return capError(400, "binding_limit", "too many default capabilities")
	}
	for _, capID := range b.Capabilities {
		if _, err = tx.ExecContext(ctx, `INSERT INTO capability_bindings(owner_kind,owner_id,capability_id,version_id,created_at) VALUES('session',?,?,'',?)`, sid, capID, nowUTC()); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE chat_sessions SET bindings_json=? WHERE id=?`, string(raw), sid)
	return err
}

// DeactivateCapability removes future access atomically while preserving files,
// installed versions and historical run pins. Running consumers must finish first.
func (a *App) DeactivateCapability(ctx context.Context, capID string) (any, error) {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var version string
	if err = tx.QueryRowContext(ctx, `SELECT current_version_id FROM capabilities WHERE id=? AND status='installed'`, capID).Scan(&version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, capError(404, "not_found", "installed capability not found")
		}
		return nil, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM capability_run_bindings b JOIN runs r ON r.id=b.run_id WHERE b.capability_id=? AND r.status IN ('running','queued','pending')`, capID).Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, capError(409, "capability_busy", "wait for capability runs to finish before deactivating")
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.bindings_json,s.status,EXISTS(SELECT 1 FROM capability_bindings b WHERE b.owner_kind='session' AND b.owner_id=s.id AND b.capability_id=?) FROM chat_sessions s`, capID)
	if err != nil {
		return nil, err
	}
	type change struct{ id, raw string }
	changes := []change{}
	for rows.Next() {
		var sid, raw, status string
		var normalized bool
		if err = rows.Scan(&sid, &raw, &status, &normalized); err != nil {
			rows.Close()
			return nil, err
		}
		b := parseChatBindings(raw)
		keep := []string{}
		found := normalized
		for _, v := range b.Capabilities {
			if v == capID {
				found = true
			} else {
				keep = append(keep, v)
			}
		}
		if !found {
			continue
		}
		if status != "idle" && status != "closed" {
			rows.Close()
			return nil, capError(409, "session_busy", "wait for bound sessions to become idle before deactivating")
		}
		b.Capabilities = keep
		data, e := json.Marshal(b)
		if e != nil {
			rows.Close()
			return nil, e
		}
		changes = append(changes, change{sid, string(data)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		if _, err = tx.ExecContext(ctx, `UPDATE chat_sessions SET bindings_json=?,updated_at=? WHERE id=?`, c.raw, nowUTC(), c.id); err != nil {
			return nil, err
		}
	}
	for _, q := range []string{`DELETE FROM capability_tool_grants WHERE capability_id=?`, `DELETE FROM capability_bindings WHERE capability_id=?`, `DELETE FROM capability_session_defaults WHERE capability_id=?`} {
		if _, err = tx.ExecContext(ctx, q, capID); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO capability_events(id,capability_id,version_id,actor,action,result,created_at) VALUES(?,?,?,?,?,?,?)`, id.New("ce"), capID, version, "ui", "deactivate", "ok", nowUTC()); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, tx.Commit()
}
