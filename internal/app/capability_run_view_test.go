package app

import (
	"context"
	"testing"
)

func TestCapabilityRunViewUsesPinnedHistory(t *testing.T) {
	a, r := installedCap(t)
	ctx := context.Background()
	if _, err := a.DB.Exec(`INSERT INTO runs(id,created_at,status) VALUES('view-run',?,'succeeded')`, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec(`INSERT INTO capability_run_bindings(run_id,capability_id,version_id) VALUES('view-run',?,?)`, r.ID, r.VersionID); err != nil {
		t.Fatal(err)
	}
	values, err := a.capabilityRunBindings(ctx, "view-run")
	if err != nil || len(values) != 1 || values[0].VersionID != r.VersionID || values[0].Title != r.Lock.Title {
		t.Fatal(values, err)
	}
	empty, err := a.capabilityRunBindings(ctx, "other-run")
	if err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
}
