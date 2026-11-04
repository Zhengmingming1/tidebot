package app

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/webui"
)

var dependencyPreferenceName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+-]{0,127}$`)

func (a *App) dependencyPreferences(ctx context.Context) (map[string]capability.DependencyPreference, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT name,path,prefer_managed FROM capability_dependency_preferences ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]capability.DependencyPreference{}
	for rows.Next() {
		var name string
		var p capability.DependencyPreference
		if err = rows.Scan(&name, &p.Path, &p.PreferManaged); err != nil {
			return nil, err
		}
		out[name] = p
	}
	return out, rows.Err()
}

func (a *App) CapabilityDependencyPreferenceRequest(ctx context.Context, method string, req webui.CapabilityDependencyPreferenceRequest) (any, error) {
	switch method {
	case http.MethodGet:
	case http.MethodPut, http.MethodDelete:
		if !dependencyPreferenceName.MatchString(req.Name) || req.Name == "." || req.Name == ".." {
			return nil, capError(400, "invalid_tool", "use a dependency command name")
		}
		if method == http.MethodPut {
			if len(req.Path) > 4096 || strings.ContainsAny(req.Path, "\x00\r\n") || req.Path != "" && !filepath.IsAbs(req.Path) {
				return nil, capError(400, "invalid_path", "configured executable path must be absolute")
			}
			tx, err := a.DB.BeginTx(ctx, nil)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM capability_dependency_preferences WHERE name<>?`, req.Name).Scan(&count); err != nil {
				return nil, err
			}
			if count >= 128 {
				return nil, capError(400, "preference_limit", "at most 128 dependency preferences")
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO capability_dependency_preferences(name,path,prefer_managed,updated_at) VALUES(?,?,?,?) ON CONFLICT(name) DO UPDATE SET path=excluded.path,prefer_managed=excluded.prefer_managed,updated_at=excluded.updated_at`, req.Name, req.Path, req.PreferManaged, nowUTC()); err != nil {
				return nil, err
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
		} else {
			if _, err := a.DB.ExecContext(ctx, `DELETE FROM capability_dependency_preferences WHERE name=?`, req.Name); err != nil {
				return nil, err
			}
		}
		a.capState.dependencyDetector.Invalidate()
	default:
		return nil, capError(405, "method_not_allowed", "unsupported dependency preference action")
	}
	preferences, err := a.dependencyPreferences(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]webui.CapabilityDependencyPreferenceRequest, 0, len(preferences))
	for name, pref := range preferences {
		out = append(out, webui.CapabilityDependencyPreferenceRequest{Name: name, Path: pref.Path, PreferManaged: pref.PreferManaged})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return map[string]any{"preferences": out, "note": "Applies to new dependency reviews. Existing proposals and installed versions keep their reviewed executable paths; create and confirm a rescan to change them. Prefer managed overrides the configured path but never authorizes a download. Login shell startup hooks are not executed."}, nil
}

// resolveCapabilityDependencies is inspection only. Choices and exact executable
// paths are frozen into the proposal. Installation and runtime never read mutable
// preferences to silently replace an already reviewed command.
func (a *App) resolveCapabilityDependencies(ctx context.Context, lock *capability.Lock, refresh bool) error {
	preferences, err := a.dependencyPreferences(ctx)
	if err != nil {
		return err
	}
	requirements := append(append([]capability.ToolRequirement{}, lock.Requires.Tools...), lock.Requires.Runtimes...)
	lock.DependencyPreferences = nil
	options := capability.DetectOptions{Paths: map[string]string{}}
	systemReqs := make([]capability.ToolRequirement, 0, len(requirements))
	for _, req := range requirements {
		pref, ok := preferences[req.Name]
		if ok {
			if lock.DependencyPreferences == nil {
				lock.DependencyPreferences = map[string]capability.DependencyPreference{}
			}
			lock.DependencyPreferences[req.Name] = pref
		}
		if !pref.PreferManaged {
			options.Paths[req.Name] = pref.Path
			systemReqs = append(systemReqs, req)
		}
	}
	if refresh {
		a.capState.dependencyDetector.Invalidate()
	}
	resolved := a.capState.dependencyDetector.Detect(ctx, systemReqs, options)
	var installed []capability.Record
	loaded := false
	for _, req := range requirements {
		pref := preferences[req.Name]
		if !pref.PreferManaged && resolved[req.Name].Status == "ready" {
			continue
		}
		if !loaded {
			installed, err = a.caps().List(ctx)
			if err != nil {
				return err
			}
			loaded = true
		}
		if pref.PreferManaged {
			resolved[req.Name] = capability.Resolved{Source: "managed", Status: "missing", Note: "Owner selected managed-only resolution; a download still needs separate confirmation."}
		}
		for _, record := range installed {
			candidate, exists := record.Lock.Resolved[req.Name]
			if !exists || candidate.Source != "managed" || candidate.Status != "ready" {
				continue
			}
			// Package environments belong to their capability; only binary/runtime
			// installations may be shared between unrelated capability snapshots.
			if record.ID != lock.ID && !sharedManagedDependency(a.Cfg.DataDir, candidate.Directory) {
				continue
			}
			if req.Name == "npm" && record.Lock.Resolved["node"].Source == "system" {
				node := record.Lock.Resolved["node"]
				selected := resolved["node"]
				if preferences["node"].PreferManaged || selected.Status != "ready" || selected.Path != node.Path || selected.Version != node.Version {
					continue
				}
			}
			if !managedDependencyRecipeMatches(req, record.Lock) {
				continue
			}
			compatible, e := capability.SatisfiesVersion(candidate.Version, req.Version)
			if e != nil || !compatible {
				continue
			}
			if e = a.caps().Verify(ctx, record.ID, record.VersionID); e != nil {
				continue
			}
			check := capability.Lock{Requires: capability.Requirements{Tools: []capability.ToolRequirement{req}}, Resolved: map[string]capability.Resolved{req.Name: candidate}}
			if e = a.verifyManagedCapabilityDependencies(ctx, check); e != nil {
				continue
			}
			resolved[req.Name] = candidate
			break
		}
	}
	lock.Resolved = resolved
	return ctx.Err()
}

// A package environment cannot stand in for a newly reviewed closure merely
// because it exposes the same command. Recipe changes require a new install.
func managedDependencyRecipeMatches(req capability.ToolRequirement, installed capability.Lock) bool {
	if len(req.Recipes) == 0 {
		return true
	}
	for _, previous := range append(append([]capability.ToolRequirement{}, installed.Requires.Tools...), installed.Requires.Runtimes...) {
		if previous.Name != req.Name {
			continue
		}
		for _, want := range req.Recipes {
			for _, got := range previous.Recipes {
				if reflect.DeepEqual(want, got) {
					return true
				}
			}
		}
	}
	return false
}

func sharedManagedDependency(root, directory string) bool {
	if !filepath.IsAbs(directory) {
		return false
	}
	rel, err := filepath.Rel(root, directory)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) == 3 && (parts[0] == "tools" || parts[0] == "runtimes") && parts[1] != "" && parts[1] != ".." && parts[2] != "" && parts[2] != ".."
}

// Checked before install starts, even when the owner declined optional downloads.
// Missing dependencies can stay missing, but a reviewed ready executable cannot
// silently become another PATH candidate between review and confirmation.
func (a *App) verifyReviewedCapabilityDependencies(ctx context.Context, lock capability.Lock) error {
	check := lock
	check.Requires = capability.Requirements{}
	for _, req := range append(append([]capability.ToolRequirement{}, lock.Requires.Tools...), lock.Requires.Runtimes...) {
		if lock.Resolved[req.Name].Status == "ready" {
			check.Requires.Tools = append(check.Requires.Tools, req)
		}
	}
	if err := a.verifyManagedCapabilityDependencies(ctx, check); err != nil {
		return errors.New("reviewed dependency changed; recheck and create a new proposal: " + err.Error())
	}
	return nil
}
