package app

import (
	"context"
	"errors"
	"sort"

	"github.com/Zhengmingming1/tidebot/internal/auth"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

// clearCapabilityCredentials is owner-UI only. Revoke each remote grant before
// deleting local credentials; a failed revocation retains its local recovery data.
func (a *App) clearCapabilityCredentials(ctx context.Context, r webui.CapabilityRequest) (any, error) {
	if !r.Reviewed {
		return nil, capError(400, "review_required", "confirm credential removal")
	}
	if session := auth.SessionFromContext(ctx); session == nil || session.ID == "" {
		return nil, capError(403, "owner_required", "authenticated owner session required")
	}
	record, err := a.caps().Get(ctx, r.CapabilityID)
	if err != nil {
		return nil, err
	}
	if a.Secrets == nil {
		return nil, errors.New("secret store unavailable")
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT kind,name FROM capability_credential_keys WHERE capability_id=? ORDER BY kind,name`, record.ID)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	components := map[string]bool{}
	for rows.Next() {
		var kind, name string
		if err = rows.Scan(&kind, &name); err != nil {
			rows.Close()
			return nil, err
		}
		if kind == "oauth" {
			components[name] = true
		} else {
			names[name] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// OAuth credentials are keyed by capability and component, not version.
	// Removed or renamed components can still have live provider grants. Revoke
	// each distinct key once using its stored issuer/client metadata, not the
	// current manifest's endpoint or component lookup.
	ordered := make([]string, 0, len(components))
	for name := range components {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		if err = a.revokeStoredCapabilityOAuth(ctx, record.ID, name); err != nil {
			return nil, err
		}
	}
	for name := range names {
		key := "cap:" + record.ID + ":" + name
		if err = a.Secrets.Delete(key); err != nil {
			return nil, err
		}
	}
	// A running helper must not retain a now-revoked authorization scope.
	a.capState.mu.Lock()
	for key, scope := range a.capState.headerTokens {
		if scope.Record.ID == record.ID {
			delete(a.capState.headerTokens, key)
		}
	}
	a.capState.mu.Unlock()
	return map[string]bool{"cleared": true}, nil
}

// Remember before writing a credential so a failed index write cannot orphan it.
func (a *App) rememberCapabilityCredential(ctx context.Context, id, kind, name string) error {
	_, err := a.DB.ExecContext(ctx, `INSERT OR IGNORE INTO capability_credential_keys(capability_id,kind,name) VALUES(?,?,?)`, id, kind, name)
	return err
}
