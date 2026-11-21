package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeOld(t *testing.T, path string, n int, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age)
	_ = os.Chtimes(path, ts, ts)
}

func callStorage(t *testing.T, a *App, method string, in any) (json.RawMessage, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	out, err := a.CallExt(context.Background(), method, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(out)
	return b, nil
}

func TestStorageUsagePreviewAndCleanup(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	logs := filepath.Join(a.Cfg.DataDir, "runs")
	day := 24 * time.Hour
	writeOld(t, filepath.Join(logs, "r_old.log"), 100, 40*day)
	writeOld(t, filepath.Join(logs, "r_live.log"), 50, 40*day) // still running: never deleted
	writeOld(t, filepath.Join(logs, "r_new.log"), 10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "artifacts", "r_old", "report.md"), 300, 40*day)
	if _, err := a.DB.Exec(`INSERT INTO runs(id, status, created_at) VALUES('r_live','running',?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	// keep_per_task=0 so only age matters; artifacts kept forever.
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"keep_per_task": 0, "artifact_days": 0}); err != nil {
		t.Fatal(err)
	}
	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.Parts["logs"].Bytes != 160 || v.Parts["logs"].Count != 3 || v.Parts["artifacts"].Bytes != 300 || v.Parts["database"].Bytes == 0 {
		t.Fatalf("usage %+v", v.Parts)
	}
	if v.Preview.Files != 1 || v.Preview.Bytes != 100 || v.Policy.LogDays != 30 || v.Policy.ArtifactDays != 0 {
		t.Fatalf("preview %+v policy %+v", v.Preview, v.Policy)
	}
	if v.Last != nil {
		t.Fatal("no cleanup yet")
	}
	// The preview must not have touched anything.
	if _, err := os.Stat(filepath.Join(logs, "r_old.log")); err != nil {
		t.Fatal("preview deleted a file")
	}
	b, err := callStorage(t, a, "storage.cleanup", nil)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Report  RetentionReport `json:"report"`
		Storage StorageView     `json:"storage"`
	}
	_ = json.Unmarshal(b, &res)
	if res.Report.DeletedFiles != 1 || res.Report.FreedBytes != 100 || res.Storage.Preview.Files != 0 || res.Storage.Last == nil || res.Storage.Last.Auto {
		t.Fatalf("cleanup %s", b)
	}
	for _, keep := range []string{"r_live.log", "r_new.log"} {
		if _, err := os.Stat(filepath.Join(logs, keep)); err != nil {
			t.Fatalf("%s deleted", keep)
		}
	}
	if _, err := os.Stat(filepath.Join(a.Cfg.DataDir, "artifacts", "r_old")); err != nil {
		t.Fatal("artifact deleted although kept forever")
	}
}

func TestStorageSettingsValidateAndPersist(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if got := a.RetentionSettings(ctx); got != DefaultRetention {
		t.Fatalf("default %+v", got)
	}
	for _, bad := range []map[string]any{{"log_days": -1}, {"log_days": 5000}, {"workflow_run_days": -1}, {"workflow_run_days": 5000}, {"keep_per_task": 2000}, {"max_total_bytes": -5}, {"log_days": "x"}} {
		if _, err := callStorage(t, a, "storage.settings", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"log_days": 7, "max_total_bytes": 2 << 30}); err != nil {
		t.Fatal(err)
	}
	got := a.RetentionSettings(ctx)
	if got.LogDays != 7 || got.MaxTotalBytes != 2<<30 || got.ArtifactDays != DefaultRetention.ArtifactDays || got.KeepPerTask != DefaultRetention.KeepPerTask || got.WorkflowRunDays != DefaultRetention.WorkflowRunDays {
		t.Fatalf("partial update %+v", got)
	}
	for _, days := range []int{0, 90, 3650} {
		if _, err := callStorage(t, a, "storage.settings", map[string]any{"workflow_run_days": days}); err != nil {
			t.Fatalf("workflow_run_days %d: %v", days, err)
		}
		if got := a.RetentionSettings(ctx); got.WorkflowRunDays != days || got.LogDays != 7 {
			t.Fatalf("workflow_run_days %d: %+v", days, got)
		}
	}
	// A stored policy written before this field existed keeps the 90-day default.
	_ = a.Auth.SetSetting(ctx, setRetention, `{"log_days":14,"artifact_days":90,"keep_per_task":20,"max_total_bytes":10737418240}`)
	if got := a.RetentionSettings(ctx); got.WorkflowRunDays != 90 || got.LogDays != 14 {
		t.Fatalf("omitted workflow_run_days %+v", got)
	}
	// A corrupt stored value falls back to the defaults instead of deleting everything.
	_ = a.Auth.SetSetting(ctx, setRetention, `{"log_days":-3}`)
	if got := a.RetentionSettings(ctx); got != DefaultRetention {
		t.Fatalf("corrupt fallback %+v", got)
	}
}

func TestStorageGetIncludesWorkflowRunDaysDefault(t *testing.T) {
	a := newTestApp(t)
	v, err := a.StorageUsage(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if v.Policy.WorkflowRunDays != 90 || v.Defaults.WorkflowRunDays != 90 {
		t.Fatalf("policy %+v defaults %+v", v.Policy, v.Defaults)
	}
}

func TestAutomaticCleanupUsesStoredPolicy(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	logs := filepath.Join(a.Cfg.DataDir, "runs")
	writeOld(t, filepath.Join(logs, "r_a.log"), 10, 10*24*time.Hour)
	a.retentionTick(ctx, time.Now()) // default 30 days: nothing to do, nothing recorded
	if v, _ := a.StorageUsage(ctx, time.Now()); v.Last != nil || v.Parts["logs"].Count != 1 {
		t.Fatalf("default policy deleted or recorded: %+v", v)
	}
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"log_days": 7, "keep_per_task": 0}); err != nil {
		t.Fatal(err)
	}
	a.retentionTick(ctx, time.Now())
	v, _ := a.StorageUsage(ctx, time.Now())
	if v.Parts["logs"].Count != 0 || v.Last == nil || !v.Last.Auto || v.Last.FreedBytes != 10 {
		t.Fatalf("auto cleanup %+v last %+v", v.Parts, v.Last)
	}
}

func TestShrinkDatabase(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if _, err := a.DB.Exec(`CREATE TABLE junk(b BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		_, _ = a.DB.Exec(`INSERT INTO junk VALUES(?)`, make([]byte, 64<<10))
	}
	_, _ = a.DB.Exec(`DELETE FROM junk`)
	_, _ = a.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	v, _ := a.StorageUsage(ctx, time.Now())
	if v.Reclaimable < 1<<20 {
		t.Fatalf("expected reclaimable space, got %d", v.Reclaimable)
	}
	b, err := callStorage(t, a, "storage.vacuum", nil)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Saved   int64       `json:"saved_bytes"`
		Storage StorageView `json:"storage"`
	}
	_ = json.Unmarshal(b, &res)
	if res.Saved < 1<<20 || res.Storage.Reclaimable >= 1<<20 {
		t.Fatalf("vacuum %s", b)
	}
}

// Finished runs' logs are gzipped by the periodic sweep; usage reports them, retention
// still counts their age and deletes them by run id.
func TestRetentionTickPacksFinishedLogs(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	logs := filepath.Join(a.Cfg.DataDir, "runs")
	day := 24 * time.Hour
	text := func(p string, age time.Duration) {
		t.Helper()
		os.MkdirAll(logs, 0o755)
		os.WriteFile(p, []byte(strings.Repeat("[09:00:00] 读取 README.md → ok\n", 2000)), 0o600)
		ts := time.Now().Add(-age)
		os.Chtimes(p, ts, ts)
	}
	text(filepath.Join(logs, "r_done.log"), 2*day)
	text(filepath.Join(logs, "r_old.log"), 40*day)
	text(filepath.Join(logs, "r_run.log"), 2*day)
	if _, err := a.DB.Exec(`INSERT INTO runs(id, status, created_at) VALUES('r_run','running',?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if _, err := callStorage(t, a, "storage.settings", map[string]any{"keep_per_task": 0}); err != nil {
		t.Fatal(err)
	}
	a.retentionTick(ctx, time.Now())
	exists := func(n string) bool { _, err := os.Stat(filepath.Join(logs, n)); return err == nil }
	if !exists("r_done.log.gz") || exists("r_done.log") || !exists("r_run.log") || exists("r_old.log") || exists("r_old.log.gz") {
		ents, _ := os.ReadDir(logs)
		t.Fatalf("after tick: %v", ents)
	}
	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	lp := v.Parts["logs"]
	if lp.Count != 2 || lp.Packed != 1 || lp.PackedRaw <= lp.PackedOn || lp.PackedOn <= 0 {
		t.Fatalf("logs part %+v", lp)
	}
	// Retention ages the packed log like the plain one and finds it by run id.
	plan, err := a.planRetention(ctx, RetentionPolicy{LogDays: 1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range plan.del {
		if f.runID == "r_done" && f.packed {
			found = true
		}
	}
	if !found {
		t.Fatalf("packed log not planned for deletion: %+v", plan.del)
	}
}

// Session folders and tidebot-owned Claude Code transcripts are part of the
// usage bar total. A fixture with known sizes makes the undercount visible:
// without them the bar would report only logs+database.
func TestStorageUsageIncludesSessionsAndTranscripts(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	placeSessionDir(t, a, "chat_live_u", 8<<10)    // work+draft = 16 KiB
	placeSessionDir(t, a, "chat_orphan_u", 32<<10) // 64 KiB
	insertChatRow(t, a, "chat_live_u")

	csid := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeee0001"
	placeChatTranscript(t, a, csid, "proj-usage", 40<<10, time.Hour) // jsonl 40 KiB + sidecar 20 KiB
	writeOld(t, filepath.Join(a.Cfg.DataDir, "runs", "r_usage.log"), 100, time.Hour)

	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wantSessions := int64((16 << 10) + (64 << 10) + (40 << 10) + (20 << 10))
	sp := v.Parts["sessions"]
	if sp.Bytes != wantSessions || sp.Folders != 2 || sp.Transcripts != 1 || sp.Count != 3 {
		t.Fatalf("sessions part %+v, want bytes=%d folders=2 transcripts=1 count=3", sp, wantSessions)
	}
	if v.Parts["logs"].Bytes != 100 {
		t.Fatalf("logs %+v", v.Parts["logs"])
	}
	var sum int64
	for _, p := range v.Parts {
		sum += p.Bytes
	}
	if v.TotalBytes != sum || v.TotalBytes != wantSessions+v.Parts["logs"].Bytes+v.Parts["database"].Bytes+v.Parts["artifacts"].Bytes+v.Parts["backups"].Bytes+v.Parts["capabilities"].Bytes+v.Parts["other"].Bytes {
		t.Fatalf("total %d sum %d parts %+v", v.TotalBytes, sum, v.Parts)
	}
	// The previous bar total (logs+artifacts+database+backups) under-counted
	// by the whole sessions slice. Recorded so the backlog can cite a real number.
	oldTotal := v.TotalBytes - sp.Bytes
	if oldTotal <= 0 || v.TotalBytes-oldTotal != wantSessions {
		t.Fatalf("old total %d new %d sessions %d", oldTotal, v.TotalBytes, wantSessions)
	}
	t.Logf("usage bar without sessions: %d bytes; with sessions: %d bytes (sessions %d, undercount %d)", oldTotal, v.TotalBytes, wantSessions, wantSessions)

	// Orphan session folder is already in the cleanup preview (live folder stays).
	if v.Preview.Bytes < 64<<10 {
		t.Fatalf("preview should include the orphan session folder, got %+v", v.Preview)
	}
}

func TestStorageUsageIncludesCapabilities(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	// Known sizes under the same trees Extensions › storage already measures.
	writeOld(t, filepath.Join(a.Cfg.DataDir, "tools", "demo-cli", "v1", "bin"), 30<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "runtimes", "node", "v20", "node"), 12<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "envs", "python", "3.12", "lib"), 80<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "cache", "capability-git", "blob"), 8<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "capabilities", "cap_demo", "files", "skill.md"), 5<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "runs", "r_cap_usage.log"), 100, time.Hour)

	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wantCap := int64((30 << 10) + (12 << 10) + (80 << 10) + (8 << 10) + (5 << 10))
	cp := v.Parts["capabilities"]
	if cp.Bytes != wantCap || cp.Tools != (30<<10)+(12<<10) || cp.Environments != 80<<10 || cp.Cache != 8<<10 || cp.Snapshots != 5<<10 {
		t.Fatalf("capabilities part %+v, want bytes=%d tools=%d env=%d cache=%d snapshots=%d", cp, wantCap, (30<<10)+(12<<10), 80<<10, 8<<10, 5<<10)
	}
	if v.Parts["logs"].Bytes != 100 {
		t.Fatalf("logs %+v", v.Parts["logs"])
	}
	var sum int64
	for _, p := range v.Parts {
		sum += p.Bytes
	}
	if v.TotalBytes != sum || v.TotalBytes != wantCap+v.Parts["logs"].Bytes+v.Parts["database"].Bytes+v.Parts["artifacts"].Bytes+v.Parts["backups"].Bytes+v.Parts["sessions"].Bytes+v.Parts["other"].Bytes {
		t.Fatalf("total %d sum %d parts %+v", v.TotalBytes, sum, v.Parts)
	}
	oldTotal := v.TotalBytes - cp.Bytes
	if oldTotal <= 0 || v.TotalBytes-oldTotal != wantCap {
		t.Fatalf("old total %d new %d capabilities %d", oldTotal, v.TotalBytes, wantCap)
	}
	t.Logf("usage bar without capabilities: %d bytes; with capabilities: %d bytes (capabilities %d, undercount %d)", oldTotal, v.TotalBytes, wantCap, wantCap)

	// A second call should reuse CheckQuota's cache (and still see the same sizes).
	start := time.Now()
	again, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cached := time.Since(start)
	if again.Parts["capabilities"].Bytes != wantCap {
		t.Fatalf("cached capabilities %+v", again.Parts["capabilities"])
	}
	t.Logf("StorageUsage second call (quota cache warm): %s", cached)
}

func TestStorageUsageIncludesOther(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()

	before, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	base := before.Parts["other"]

	// Known sizes under trees no other usage-bar segment already measures.
	writeOld(t, filepath.Join(a.Cfg.DataDir, "skill-repositories", "demo.git", "HEAD"), 48<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "skillstage", "run-stage", "plugin.json"), 16<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "runsnap", "r1", "snap"), 20<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "run-mcp", "r1", "mcp.json"), 4<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "run-work", "r1", "out.txt"), 8<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "run-sandbox", "r1", "settings.json"), 2<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "dependency-check", "analysis-9", "notes"), 6<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "runs", "r_other_usage.log"), 100, time.Hour)
	// Already counted elsewhere: must not land in "other".
	writeOld(t, filepath.Join(a.Cfg.DataDir, "tools", "demo-cli", "v1", "bin"), 30<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "chat-sessions", "chat_other", "draft.md"), 12<<10, time.Hour)
	writeOld(t, filepath.Join(a.Cfg.DataDir, "artifacts", "r_other", "out.md"), 3<<10, time.Hour)

	v, err := a.StorageUsage(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wantRepos := int64(48 << 10)
	wantStage := int64(16 << 10)
	wantScratch := int64((20 << 10) + (4 << 10) + (8 << 10) + (2 << 10))
	wantRestAdded := int64(6 << 10)
	wantRest := base.Rest + wantRestAdded
	wantOther := base.Bytes + wantRepos + wantStage + wantScratch + wantRestAdded
	op := v.Parts["other"]
	if op.Bytes != wantOther || op.Repositories != base.Repositories+wantRepos || op.Staging != base.Staging+wantStage || op.Scratch != base.Scratch+wantScratch || op.Rest != wantRest {
		t.Fatalf("other part %+v, baseline %+v, want bytes=%d repos=%d staging=%d scratch=%d rest=%d", op, base, wantOther, base.Repositories+wantRepos, base.Staging+wantStage, base.Scratch+wantScratch, wantRest)
	}
	if v.Parts["logs"].Bytes != 100 {
		t.Fatalf("logs %+v", v.Parts["logs"])
	}
	if v.Parts["capabilities"].Tools < 30<<10 {
		t.Fatalf("tools should stay in capabilities, got %+v", v.Parts["capabilities"])
	}
	if v.Parts["sessions"].Bytes < 12<<10 {
		t.Fatalf("session folder should stay in sessions, got %+v", v.Parts["sessions"])
	}
	if v.Parts["artifacts"].Bytes < 3<<10 {
		t.Fatalf("artifact should stay in artifacts, got %+v", v.Parts["artifacts"])
	}
	var sum int64
	for _, p := range v.Parts {
		sum += p.Bytes
	}
	if v.TotalBytes != sum || v.TotalBytes != wantOther+v.Parts["logs"].Bytes+v.Parts["database"].Bytes+v.Parts["artifacts"].Bytes+v.Parts["backups"].Bytes+v.Parts["sessions"].Bytes+v.Parts["capabilities"].Bytes {
		t.Fatalf("total %d sum %d parts %+v", v.TotalBytes, sum, v.Parts)
	}
	oldTotal := v.TotalBytes - op.Bytes
	if oldTotal <= 0 || v.TotalBytes-oldTotal != wantOther {
		t.Fatalf("old total %d new %d other %d", oldTotal, v.TotalBytes, wantOther)
	}
	t.Logf("usage bar without other: %d bytes; with other: %d bytes (other %d = repos %d + staging %d + scratch %d + rest %d, undercount %d)", oldTotal, v.TotalBytes, wantOther, wantRepos, wantStage, wantScratch, wantRest, wantOther)
}
