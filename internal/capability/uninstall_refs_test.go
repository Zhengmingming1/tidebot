package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// seedCurrentOwners adds n current task definitions (pointer + version) and n
// sessions that never mention the capability, so the uninstall reference check
// has to look at every one of them under the writer lock.
func seedCurrentOwners(t testing.TB, s *Store, n, size int) {
	t.Helper()
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("检查 <b>&</b> x ", size/16)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i := 0; i < n; i++ {
		raw, _ := json.Marshal(map[string]any{"name": fmt.Sprintf("task %d", i), "spec": map[string]any{"prompt": body, "schedule": map[string]any{"kind": "cron", "expr": "0 9 * * *"}, "skills": []string{fmt.Sprintf("sk_other_%d", i)}, "tags": []string{"a", "b"}}})
		vid := fmt.Sprintf("ver_cur_%d", i)
		if _, err = tx.Exec(`INSERT INTO versions(id,entity_type,entity_id,version_no,content_hash,content_json,source,created_at) VALUES(?,?,?,?,?,?,?,?)`,
			vid, "task", fmt.Sprintf("task_cur_%d", i), 1, "h", string(raw), "test", now); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO pointers(entity_type,entity_id,name,version_id,updated_at) VALUES(?,?,?,?,?)`, "task", fmt.Sprintf("task_cur_%d", i), "current", vid, now); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO chat_sessions(id,bindings_json,status,created_at,updated_at) VALUES(?,?,?,?,?)`,
			fmt.Sprintf("cs_%d", i), fmt.Sprintf(`{"skills":["sk_other_%d"],"capabilities":[]}`, i), "idle", now, now); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TIDEBOT_PERF=1 times an uninstall preview with many current tasks and sessions.
func TestUninstallReferencePerf(t *testing.T) {
	if os.Getenv("TIDEBOT_PERF") == "" {
		t.Skip("set TIDEBOT_PERF=1")
	}
	ctx := context.Background()
	s := testStore(t)
	r, _ := installOwnedSkill(t, s)
	seedCurrentOwners(t, s, 2000, 8192)
	for round := 0; round < 5; round++ {
		start := time.Now()
		if _, err := s.PreviewUninstall(ctx, r.ID, r.VersionID); err != nil {
			t.Fatal(err)
		}
		t.Logf("round %d: preview %v", round, time.Since(start))
	}
}

func TestReferencesGateKeepsVerdicts(t *testing.T) {
	skills := map[string]bool{"sk_0190ab": true}
	cases := []struct {
		name, raw string
		want      bool
	}{
		{"capability leaf", `{"capabilities":["cap_0190ab"]}`, true},
		{"skill leaf", `{"spec":{"skills":["sk_0190ab"]}}`, true},
		{"escaped skill leaf", `{"skills":["sk\u005f0190ab"]}`, true},
		{"escaped capability leaf", `["\u0063ap_0190ab"]`, true},
		{"no mention, html escapes", `{"prompt":"\u003cb\u003e \u0026 检查","skills":["sk_other"]}`, false},
		{"substring is not a leaf", `{"note":"see sk_0190ab-old"}`, false},
		{"id as a key only", `{"sk_0190ab":1}`, false},
		{"malformed without ids fails closed", `{"skills":[`, true},
		{"malformed with id fails closed", `{"skills":["sk_0190ab"`, true},
		{"truncated escape fails closed", `{"a":"\u00`, true},
		{"empty", ``, false},
		{"null", `null`, false},
	}
	for _, c := range cases {
		if got := references([]byte(c.raw), "cap_0190ab", skills); got != c.want {
			t.Errorf("%s: references(%s) = %v, want %v", c.name, c.raw, got, c.want)
		}
	}
	// An id a substring search can't vouch for always gets the full parse.
	odd := map[string]bool{"my skill": true}
	if !references([]byte(`["my skill"]`), "", odd) || references([]byte(`["my skills"]`), "", odd) {
		t.Fatal("non-plain id verdict changed")
	}
}

// The gate must not decode a definition that mentions no id.
func TestReferencesMissDoesNotAllocate(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"prompt": strings.Repeat("检查 <b>&</b> x ", 512), "skills": []string{"sk_other"}})
	skills := map[string]bool{"sk_0190ab": true}
	allocs := testing.AllocsPerRun(50, func() {
		if references(raw, "cap_0190ab", skills) {
			t.Fatal("false hit")
		}
	})
	if allocs > 1 {
		t.Fatalf("miss allocated %.0f times per call, want ≤1", allocs)
	}
}
