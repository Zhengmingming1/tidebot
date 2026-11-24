package capability

import (
	"context"
	"testing"
	"time"
)

// Capability lists order by created_at as text. Every timestamp must be fixed
// width so a whole second does not sort after a later fraction of that second.
func TestStampFixedWidthOrdersAsText(t *testing.T) {
	base := time.Date(2026, 11, 24, 1, 2, 5, 0, time.FixedZone("x", 8*3600))
	whole, later := Stamp(base), Stamp(base.Add(300*time.Millisecond))
	if whole != "2026-11-23T17:02:05.000000000Z" || len(later) != len(whole) {
		t.Fatalf("stamp: %q %q", whole, later)
	}
	if !(whole < later) {
		t.Fatalf("text order broken: %q !< %q", whole, later)
	}
	if old := base.UTC().Format(time.RFC3339Nano); old < base.Add(300*time.Millisecond).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("RFC3339Nano no longer shows the bug, test is stale: %q", old)
	}
	if _, err := time.Parse(time.RFC3339Nano, whole); err != nil {
		t.Fatal(err)
	}
}

// Rows written by the store (capability, version, events, proposals) use the
// fixed layout, and the SQL-side "now" used for proposal expiry has the same
// shape so text comparisons against expires_at are correct.
func TestCapabilityWritesFixedWidthTimes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, err := s.Install(ctx, testLock(), testFiles())
	if err != nil {
		t.Fatal(err)
	}
	insertChatSession(t, s, "chat_ts")
	p, err := s.CreateProposal(ctx, Proposal{SessionID: "chat_ts", Kind: "install", Lock: testLock()})
	if err != nil {
		t.Fatal(err)
	}
	fixed := func(what, v string) {
		t.Helper()
		if len(v) != len(TimeLayout) {
			t.Errorf("%s not fixed width: %q", what, v)
			return
		}
		if _, err := time.Parse(TimeLayout, v); err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
	fixed("record created", r.CreatedAt)
	fixed("record updated", r.UpdatedAt)
	fixed("proposal created", p.CreatedAt)
	fixed("proposal expires", p.ExpiresAt)
	for _, q := range []string{
		`SELECT created_at FROM capabilities`, `SELECT updated_at FROM capabilities`,
		`SELECT created_at FROM capability_versions`, `SELECT created_at FROM capability_events`,
		`SELECT created_at FROM capability_proposals`, `SELECT expires_at FROM capability_proposals`,
	} {
		var v string
		if err := s.DB.QueryRow(q + ` LIMIT 1`).Scan(&v); err != nil {
			t.Fatal(q, err)
		}
		fixed(q, v)
	}
	var sqlNowV string
	if err := s.DB.QueryRow(`SELECT ` + sqlNow).Scan(&sqlNowV); err != nil {
		t.Fatal(err)
	}
	fixed("sqlNow", sqlNowV)
	if d := time.Since(mustParse(t, sqlNowV)); d < -time.Minute || d > time.Minute {
		t.Fatalf("sqlNow far from now: %s", sqlNowV)
	}
}

func mustParse(t *testing.T, v string) time.Time {
	t.Helper()
	tm, err := time.Parse(TimeLayout, v)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

// Version history and the event log keep newest first when two rows share a
// second; before the fix the whole-second row sorted above the later one.
func TestVersionsAndEventsOrderWithinOneSecond(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, err := s.Install(ctx, testLock(), testFiles())
	if err != nil {
		t.Fatal(err)
	}
	// Later than the real install so these two rows lead both lists.
	base := time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []struct {
		id string
		at time.Time
	}{{"capver_whole", base}, {"capver_later", base.Add(300 * time.Millisecond)}} {
		exec(`INSERT INTO capability_versions(id,capability_id,lock_json,lock_sha256,files_json,created_at) SELECT ?,capability_id,lock_json,lock_sha256,files_json,? FROM capability_versions WHERE id=?`, v.id, Stamp(v.at), r.VersionID)
	}
	exec(`INSERT INTO capability_events(id,capability_id,version_id,actor,action,result,created_at) VALUES('capevt_z',?,?,'ui','install','ok',?),('capevt_0',?,?,'ui','rollback','ok',?)`,
		r.ID, r.VersionID, Stamp(base), r.ID, r.VersionID, Stamp(base.Add(300*time.Millisecond)))
	vs, err := s.Versions(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 || vs[0].VersionID != "capver_later" || vs[1].VersionID != "capver_whole" {
		t.Fatalf("versions order: %+v", vs)
	}
	ev, err := s.Events(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) < 3 || ev[0].ID != "capevt_0" || ev[1].ID != "capevt_z" {
		t.Fatalf("events order: %+v", ev)
	}
}
