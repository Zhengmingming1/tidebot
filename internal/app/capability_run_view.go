package app

import (
	"context"
	"encoding/json"
	"github.com/Zhengmingming1/tidebot/internal/capability"
)

type capabilityRunView struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	VersionID     string `json:"version_id"`
	SourceVersion string `json:"source_version"`
}

func (a *App) capabilityRunBindings(ctx context.Context, runID string) ([]capabilityRunView, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT b.capability_id,b.version_id,v.lock_json FROM capability_run_bindings b JOIN capability_versions v ON v.id=b.version_id AND v.capability_id=b.capability_id WHERE b.run_id=? ORDER BY b.capability_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []capabilityRunView{}
	for rows.Next() {
		var v capabilityRunView
		var raw string
		if err = rows.Scan(&v.ID, &v.VersionID, &raw); err != nil {
			return nil, err
		}
		var lock capability.Lock
		if err = json.Unmarshal([]byte(raw), &lock); err != nil {
			return nil, err
		}
		v.Title = lock.Title
		v.SourceVersion = lock.Source.Commit
		if v.SourceVersion == "" {
			v.SourceVersion = lock.Source.Ref
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
