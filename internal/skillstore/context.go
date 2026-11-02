package skillstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zhengmingming1/tidebot/internal/contextplan"
	"github.com/Zhengmingming1/tidebot/internal/tasksvc"
)

// ContextMaterials only reads immutable metadata initially. The planner loads
// SKILL.md on demand; scripts and reference documents are not loaded here.
// Callers pass ownership-checked, frozen bindings from ResolveBindings.
func (s *Service) ContextMaterials(ctx context.Context, bindings []tasksvc.SkillBinding, scope string) ([]contextplan.Material, error) {
	var out []contextplan.Material
	totalBytes := 0
	for i, b := range bindings {
		var hash string
		err := s.DB.QueryRowContext(ctx, `SELECT f.sha256 FROM version_files f JOIN versions v ON v.id=f.version_id WHERE v.entity_type='skill' AND v.entity_id=? AND v.id=? AND f.path='SKILL.md'`, b.SkillID, b.VersionID).Scan(&hash)
		if errors.Is(err, sql.ErrNoRows) {
			v, e := s.Ver.GetVersion(ctx, b.VersionID)
			if e != nil {
				return nil, e
			}
			if v.EntityID != b.SkillID || v.EntityType != "skill" {
				return nil, contextplan.ErrAccess
			}
			var m Manifest
			if e = json.Unmarshal([]byte(v.ContentJSON), &m); e != nil {
				return nil, e
			}
			hash = m.Files["SKILL.md"]
		} else if err != nil {
			return nil, err
		}
		if hash == "" {
			return nil, fmt.Errorf("skill %s has no instruction snapshot", b.SkillID)
		}
		var size int
		if err = s.DB.QueryRowContext(ctx, `SELECT length(data) FROM blobs WHERE sha256=?`, hash).Scan(&size); err != nil {
			return nil, err
		}
		if size > (256<<10)-totalBytes {
			return nil, fmt.Errorf("skill instructions exceed the 256 KiB context budget; instructions were not truncated")
		}
		totalBytes += size
		out = append(out, contextplan.Material{ID: "skill:" + b.SkillID, Kind: "skill", Scope: scope, Allowed: true, Version: b.VersionID, Hash: hash, Ref: "skill:" + b.SkillID + "@" + b.VersionID, Required: true, Order: i, Load: func(c context.Context) (string, error) {
			if err := c.Err(); err != nil {
				return "", err
			}
			data, err := s.getBlob(c, hash)
			return string(data), err
		}})
	}
	return out, nil
}
