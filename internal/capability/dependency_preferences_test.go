package capability

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDependencyPreferencesNeverExportOrImport(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	lock := testLock()
	lock.DependencyPreferences = map[string]DependencyPreference{"tool": {Path: "/private/tool", PreferManaged: true}}
	lock.Resolved = map[string]Resolved{"tool": {Path: "/private/tool", Status: "ready", Source: "system", Version: "1.2.3"}}
	raw, err := lock.Export()
	if err != nil {
		t.Fatal(err)
	}
	var exported Lock
	if err = json.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	if len(exported.DependencyPreferences) > 0 || len(exported.Resolved) > 0 {
		t.Fatal("machine paths exported", exported)
	}
	record, err := store.Install(ctx, lock, testFiles())
	if err != nil {
		t.Fatal(err)
	}
	raw, err = store.ExportBundle(ctx, record.ID, record.VersionID)
	if err != nil {
		t.Fatal(err)
	}
	var bundle Bundle
	if err = json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	if len(bundle.Lock.DependencyPreferences) > 0 {
		t.Fatal("bundle exported preferences")
	}
	bundle.Lock.DependencyPreferences = lock.DependencyPreferences
	bundle.Lock.Resolved = lock.Resolved
	raw, err = json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	imported, _, err := ParseBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.DependencyPreferences) > 0 || len(imported.Resolved) > 0 {
		t.Fatal("untrusted bundle set machine paths")
	}
}
