package skillstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
	"github.com/Zhengmingming1/tidebot/internal/version"
)

func TestRollbackRejectsUnrelatedVersions(t *testing.T) {
	for _, tc := range []struct{ name, entityType, entityID string }{
		{"other skill", version.EntitySkill, "skill_b"},
		{"other entity type with same ID", version.EntityTask, "skill_a"},
		{"missing version", "", ""},
		{"empty version", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			ctx := context.Background()
			ver := version.New(sqlDB)
			svc := New(sqlDB, ver, nil, "")
			original, err := ver.Commit(ctx, version.CommitInput{EntityType: version.EntitySkill, EntityID: "skill_a", Content: Manifest{Name: "a"}})
			if err != nil {
				t.Fatal(err)
			}
			target := "ver_missing"
			if tc.name == "empty version" {
				target = ""
			}
			if tc.entityType != "" {
				foreign, err := ver.Commit(ctx, version.CommitInput{EntityType: tc.entityType, EntityID: tc.entityID, Content: Manifest{Name: "b"}})
				if err != nil {
					t.Fatal(err)
				}
				target = foreign.Version.ID
			}
			before, err := ver.ListOpLog(ctx, version.EntitySkill, "skill_a", 100)
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.Rollback(ctx, "skill_a", target, "user"); err == nil {
				t.Error("rollback accepted an unrelated or missing version")
			}
			current, err := ver.CurrentVersion(ctx, version.EntitySkill, "skill_a")
			if err != nil {
				t.Fatalf("original current pointer lost: %v", err)
			}
			if current.ID != original.Version.ID {
				t.Error("rejected rollback changed current pointer")
			}
			after, err := ver.ListOpLog(ctx, version.EntitySkill, "skill_a", 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Error("rejected rollback appended an operation log entry")
			}
		})
	}
}
