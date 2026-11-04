package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/id"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

const capabilitySourcesLimit = 32

type capabilitySavedSource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"source_kind"`
	Source      string `json:"source"`
	ReadOnly    bool   `json:"read_only,omitempty"`
	RefreshedAt string `json:"refreshed_at,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	ResultCount int    `json:"result_count"`
	cached      []capability.CatalogEntry
}

func scanCapabilitySavedSource(row interface{ Scan(...any) error }) (capabilitySavedSource, error) {
	var s capabilitySavedSource
	var raw string
	err := row.Scan(&s.ID, &s.Name, &s.Kind, &s.Source, &raw, &s.RefreshedAt, &s.CreatedAt)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &s.cached)
		s.ResultCount = len(s.cached)
	}
	return s, err
}

const capabilitySourceColumns = `id,name,kind,source,cached_json,refreshed_at,created_at`

func (a *App) savedCapabilitySources(ctx context.Context) ([]capabilitySavedSource, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT `+capabilitySourceColumns+` FROM capability_sources ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []capabilitySavedSource{}
	for rows.Next() {
		s, e := scanCapabilitySavedSource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (a *App) capabilitySourcesRequest(ctx context.Context, action string, r webui.CapabilityRequest) (any, error) {
	switch action {
	case "sources":
		sources, err := a.savedCapabilitySources(ctx)
		if err != nil {
			return nil, err
		}
		repos, err := a.Skills.ListRepositories(ctx)
		if err != nil {
			return nil, err
		}
		for _, repo := range repos {
			sources = append(sources, capabilitySavedSource{ID: "repository:" + repo.ID, Name: repo.Name, Kind: "skill_repository", Source: repo.URL, ReadOnly: true, RefreshedAt: repo.LastScannedAt, CreatedAt: repo.CreatedAt})
		}
		return map[string]any{"sources": sources}, nil
	case "source_save":
		return a.saveCapabilitySource(ctx, r)
	case "source_delete":
		result, err := a.DB.ExecContext(ctx, `DELETE FROM capability_sources WHERE id=?`, r.SourceID)
		if err != nil {
			return nil, err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			return nil, capError(404, "not_found", "saved source not found")
		}
		return map[string]bool{"ok": true}, nil
	case "source_refresh":
		source, err := scanCapabilitySavedSource(a.DB.QueryRowContext(ctx, `SELECT `+capabilitySourceColumns+` FROM capability_sources WHERE id=?`, r.SourceID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, capError(404, "not_found", "saved source not found")
		}
		if err != nil {
			return nil, err
		}
		entries := []capability.CatalogEntry{}
		if source.Kind == "marketplace" {
			refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			listing, err := a.DiscoverCapabilityMarketplace(refreshCtx, source.Source)
			if err != nil {
				return nil, err
			}
			for _, entry := range listing.Entries {
				src, reqs, err := parseCapabilityMarketplaceSource(entry.Source)
				if err != nil {
					return nil, err
				}
				entries = append(entries, capability.CatalogEntry{ID: entry.Source, Title: entry.Name, Source: src, Requires: reqs, DiscoveryProvider: source.ID, Labels: []string{"unknown"}})
			}
		} else {
			// A successful empty query validates reachability. Search results are not
			// persisted as a global inventory, and registration conveys no trust.
			if _, err := a.searchCapabilityRegistry(ctx, ""); err != nil {
				return nil, err
			}
		}
		raw, err := json.Marshal(entries)
		if err != nil {
			return nil, err
		}
		source.RefreshedAt = nowUTC()
		source.ResultCount = len(entries)
		result, err := a.DB.ExecContext(ctx, `UPDATE capability_sources SET cached_json=?,refreshed_at=? WHERE id=? AND source=?`, string(raw), source.RefreshedAt, source.ID, source.Source)
		if err != nil {
			return nil, err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return nil, capError(409, "source_changed", "saved source changed during refresh")
		}
		return source, nil
	}
	return nil, capError(400, "invalid_action", "unknown source action")
}

func normalizeSavedCapabilitySource(kind, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2048 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", capError(400, "invalid_source", "invalid source URL")
	}
	switch kind {
	case "marketplace":
		if _, _, _, _, err := marketplaceRepository(raw); err != nil {
			return "", capError(400, "invalid_source", err.Error())
		}
		u, _ := url.Parse(raw)
		u.Host = strings.ToLower(u.Host)
		u.Path = strings.TrimSuffix(u.Path, "/")
		return u.String(), nil
	case "mcp_registry":
		// The registry reader pins this origin. Allowing arbitrary registry hosts
		// would require separate transport, identity and snapshot trust policies.
		switch strings.TrimSuffix(raw, "/") {
		case capabilityRegistryOrigin, capabilityRegistryServers, capabilityRegistryOrigin + "/v0/servers":
			return capabilityRegistryServers, nil
		}
		return "", capError(400, "invalid_source", "use the official public MCP registry URL")
	default:
		return "", capError(400, "invalid_source_kind", "source_kind must be marketplace or mcp_registry")
	}
}

func (a *App) saveCapabilitySource(ctx context.Context, r webui.CapabilityRequest) (capabilitySavedSource, error) {
	var empty capabilitySavedSource
	source, err := normalizeSavedCapabilitySource(r.SourceKind, r.Source)
	if err != nil {
		return empty, err
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = source
	}
	if len([]rune(name)) > 128 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return empty, capError(400, "invalid_name", "source name must contain 1 to 128 printable characters")
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	existing, err := scanCapabilitySavedSource(tx.QueryRowContext(ctx, `SELECT `+capabilitySourceColumns+` FROM capability_sources WHERE kind=? AND source=?`, r.SourceKind, source))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM capability_sources`).Scan(&count); err != nil {
		return empty, err
	}
	if count >= capabilitySourcesLimit {
		return empty, capError(400, "source_limit", "at most 32 saved discovery sources")
	}
	saved := capabilitySavedSource{ID: id.New("capsrc"), Name: name, Kind: r.SourceKind, Source: source, CreatedAt: nowUTC()}
	_, err = tx.ExecContext(ctx, `INSERT INTO capability_sources(id,name,kind,source,created_at) VALUES(?,?,?,?,?)`, saved.ID, saved.Name, saved.Kind, saved.Source, saved.CreatedAt)
	if err != nil {
		return empty, err
	}
	return saved, tx.Commit()
}
