package app

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/capability"
	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestRemapPath(t *testing.T) {
	nh, nd := filepath.Join("new", "home"), filepath.Join("new", "home", "data")
	j := func(p ...string) string { return filepath.Join(append([]string{nh}, p...)...) }
	cases := []struct {
		name, p, old, want string
		ok                 bool
	}{
		{"itself", "/Users/a/.tidebot", "/Users/a/.tidebot", nh, true},
		{"inside", "/Users/a/.tidebot/workspaces/t1", "/Users/a/.tidebot", j("workspaces", "t1"), true},
		{"sibling with the same prefix", "/Users/a/.tidebot-old/proj", "/Users/a/.tidebot", "/Users/a/.tidebot-old/proj", false},
		{"user's own folder", "/Users/a/code/x", "/Users/a/.tidebot", "/Users/a/code/x", false},
		{"non-ASCII home", "/Users/张三/.tidebot/workspaces/t1", "/Users/张三/.tidebot", j("workspaces", "t1"), true},
		{"old home with trailing slash", "/Users/a/.tidebot/workspaces", "/Users/a/.tidebot/", j("workspaces"), true},
		{"Windows backup", `C:\Users\张三\.tidebot\workspaces\t1`, `C:\Users\张三\.tidebot`, j("workspaces", "t1"), true},
		{"Windows backup, other case", `c:\users\张三\.TIDEBOT\workspaces\t1`, `C:\Users\张三\.tidebot`, j("workspaces", "t1"), true},
		{"Windows sibling", `C:\Users\a\.tidebot2\x`, `C:\Users\a\.tidebot`, `C:\Users\a\.tidebot2\x`, false},
		{"Unix backup keeps a backslash in a name", "/h/.tidebot/a\\b", "/h/.tidebot", j(`a\b`), true},
		{"empty", "", "/h", "", false},
	}
	for _, c := range cases {
		got, ok := remapPath(c.p, []homeMove{{c.old, nh}})
		if got != c.want || ok != c.ok {
			t.Errorf("%s: remapPath(%q) = %q,%v want %q,%v", c.name, c.p, got, ok, c.want, c.ok)
		}
	}
	// a data folder of its own, outside the home folder; the longest match wins
	moves := []homeMove{{"/h/.tidebot", nh}, {"/h/.tidebot/data", nd}, {"/mnt/tb-data", nd}}
	for p, want := range map[string]string{
		"/mnt/tb-data/chat-sessions/s1/work": filepath.Join(nd, "chat-sessions", "s1", "work"),
		"/h/.tidebot/data/run-work/r1":       filepath.Join(nd, "run-work", "r1"),
		"/h/.tidebot/workspaces/t":           j("workspaces", "t"),
	} {
		if got, ok := remapPath(p, moves); !ok || got != want {
			t.Errorf("remapPath(%q) = %q want %q", p, got, want)
		}
	}
}

func TestRemapJSON(t *testing.T) {
	nh, nd := filepath.Join("new", "home"), filepath.Join("new", "home", "data")
	moves := []homeMove{{"/Users/张三/.tidebot", nh}, {"/Volumes/外接盘/tidebot-data", nd}}
	j := func(parts ...string) string { return filepath.Join(parts...) }
	lock := `{"resolved":{"node":{"directory":"/Volumes/外接盘/tidebot-data/runtimes/node/v1","path":"/Volumes/外接盘/tidebot-data/runtimes/node/v1/bin/node","source":"managed","status":"ready"},"sys":{"path":"/usr/local/bin/node","source":"system","status":"ready"}},"dependency_preferences":{"node":{"path":"/Users/张三/.tidebot/deps/node/bin/node","prefer_managed":false}},"components":[{"path":"skills/foo"}],"note":"/Users/张三/.tidebot-old/proj"}`
	got, ok := remapJSON(lock, moves)
	if !ok {
		t.Fatal("expected lock JSON to be remapped")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatal(err)
	}
	resolved := v["resolved"].(map[string]any)
	node := resolved["node"].(map[string]any)
	if node["directory"] != j(nd, "runtimes", "node", "v1") || node["path"] != j(nd, "runtimes", "node", "v1", "bin", "node") {
		t.Errorf("managed node paths = %#v", node)
	}
	sys := resolved["sys"].(map[string]any)
	if sys["path"] != "/usr/local/bin/node" {
		t.Errorf("system path remapped: %v", sys["path"])
	}
	pref := v["dependency_preferences"].(map[string]any)["node"].(map[string]any)
	if pref["path"] != j(nh, "deps", "node", "bin", "node") {
		t.Errorf("preference path = %v", pref["path"])
	}
	comp := v["components"].([]any)[0].(map[string]any)
	if comp["path"] != "skills/foo" {
		t.Errorf("relative path remapped: %v", comp["path"])
	}
	if v["note"] != "/Users/张三/.tidebot-old/proj" {
		t.Errorf("sibling path remapped: %v", v["note"])
	}

	same, ok := remapJSON(lock, nil)
	if ok || same != lock {
		t.Errorf("no moves should leave bytes alone: ok=%v", ok)
	}
	policy := `{"mode":"task","revision":1,"currency":"USD","timezone":"Asia/Shanghai"}`
	if got, ok := remapJSON(policy, moves); ok || got != policy {
		t.Errorf("cost policy without paths changed: ok=%v got=%s", ok, got)
	}
	if got, ok := remapJSON("not-json", moves); ok || got != "not-json" {
		t.Errorf("invalid JSON changed: %q %v", got, ok)
	}
	if got, ok := remapJSON("", moves); ok || got != "" {
		t.Errorf("empty JSON changed: %q %v", got, ok)
	}
	win, ok := remapJSON(`{"path":"C:\\Users\\张三\\.tidebot\\workspaces\\t1"}`, []homeMove{{`C:\Users\张三\.tidebot`, nh}})
	if !ok {
		t.Fatal("Windows JSON path not remapped")
	}
	var w struct{ Path string }
	if json.Unmarshal([]byte(win), &w) != nil || w.Path != j(nh, "workspaces", "t1") {
		t.Errorf("Windows JSON path = %q", w.Path)
	}
	// numbers stay exact (not float64) when a sibling path is rewritten
	got, ok = remapJSON(`{"line":9007199254740993,"path":"/Users/张三/.tidebot/workspaces/t1"}`, moves)
	if !ok || !strings.Contains(got, "9007199254740993") || strings.Contains(got, "9007199254740992") {
		t.Errorf("integer precision lost: ok=%v got=%s", ok, got)
	}
}

// A backup taken on another machine (home folder with a Chinese user name, a data
// folder of its own) is restored: every stored path under the old folders points
// into the new home, including paths nested in JSON; paths that only share a
// prefix are left alone.
func TestRestoreRemapsPathsFromAnotherMachine(t *testing.T) {
	oldHome, oldData := "/Users/张三/.tidebot", "/Volumes/外接盘/tidebot-data"
	src := filepath.Join(t.TempDir(), "src.db")
	sdb, err := db.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	now := nowUTC()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := sdb.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO tasks(id,name,workspace_dir,created_at,updated_at) VALUES('t1','a',?,?,?),('t2','b',?,?,?)`,
		oldHome+"/workspaces/t1", now, now, oldHome+"-old/proj", now, now)
	exec(`INSERT INTO runs(id,workspace_dir,created_at) VALUES('r1',?,?),('r2',?,?)`,
		oldData+"/run-work/run-1", now, "/Users/张三/code/app", now)
	exec(`INSERT INTO chat_sessions(id,workspace_dir,created_at,updated_at) VALUES('s1',?,?,?)`, oldHome+"/workspaces/t1", now, now)
	exec(`INSERT INTO backups(id,path,size_bytes,kind,created_at) VALUES('b1',?,1,'auto',?)`, oldHome+"/backups/x.agentbak", now)
	exec(`INSERT INTO capability_dependency_preferences(name,path,updated_at) VALUES('node',?,?)`, oldHome+"/deps/node/bin/node", now)
	lock := `{"format":"tidebot-capability/1","id":"cap1","resolved":{"node":{"directory":"` + oldData + `/runtimes/node/v1","path":"` + oldData + `/runtimes/node/v1/bin/node","source":"managed","status":"ready"},"sys":{"path":"/usr/local/bin/node","source":"system","status":"ready"}},"dependency_preferences":{"node":{"path":"` + oldHome + `/deps/node/bin/node","prefer_managed":false}},"components":[{"type":"skill","name":"s","path":"skills/foo"}]}`
	exec(`INSERT INTO capabilities(id,title,status,current_version_id,created_at,updated_at) VALUES('cap1','n','installed','v1',?,?)`, now, now)
	exec(`INSERT INTO capability_versions(id,capability_id,lock_json,lock_sha256,files_json,created_at) VALUES('v1','cap1',?,?, '{}',?)`, lock, sha256hex(lock), now)
	exec(`INSERT INTO capability_proposals(id,session_id,kind,payload_json,status,created_at,expires_at) VALUES('p1','s1','install',?,'pending',?,?)`, `{"lock":`+lock+`}`, now, now)
	exec(`UPDATE runs SET effective_cost_policy_json=?, agent_config_changes=? WHERE id='r1'`,
		`{"mode":"task","revision":1,"currency":"USD","timezone":"Asia/Shanghai"}`,
		`[{"path":"`+oldHome+`/workspaces/t1/CLAUDE.md","change":"modified"},{"path":"/Users/张三/.claude/settings.json","change":"modified"}]`)
	exec(`INSERT INTO trigger_bindings(id,kind,config_json,created_at) VALUES('tb1','file_change',?,?),('tb2','file_change',?,?)`,
		`{"path":"`+oldHome+`/workspaces/t1"}`, now, `{"path":"/Users/张三/code/app"}`, now)
	if _, err := sdb.Exec(`VACUUM INTO ?`, src+".snap"); err != nil {
		t.Fatal(err)
	}
	sdb.Close()

	bak := filepath.Join(t.TempDir(), "x.agentbak")
	f, _ := os.Create(bak)
	zw := zip.NewWriter(f)
	mw, _ := zw.Create("manifest.json")
	_ = json.NewEncoder(mw).Encode(BackupManifest{Format: BackupFormat, HomeDir: oldHome, DataDir: oldData})
	dw, _ := zw.Create("data/tidebot.db")
	raw, _ := os.ReadFile(src + ".snap")
	_, _ = dw.Write(raw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	home := t.TempDir()
	if _, err := RestoreBackup(bak, home, ""); err != nil {
		t.Fatal(err)
	}
	rdb, err := db.Open(filepath.Join(home, "data", "tidebot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	for q, want := range map[string]string{
		`SELECT workspace_dir FROM tasks WHERE id='t1'`:                filepath.Join(home, "workspaces", "t1"),
		`SELECT workspace_dir FROM tasks WHERE id='t2'`:                oldHome + "-old/proj",
		`SELECT workspace_dir FROM runs WHERE id='r1'`:                 filepath.Join(home, "data", "run-work", "run-1"),
		`SELECT workspace_dir FROM runs WHERE id='r2'`:                 "/Users/张三/code/app",
		`SELECT workspace_dir FROM chat_sessions WHERE id='s1'`:        filepath.Join(home, "workspaces", "t1"),
		`SELECT path FROM backups WHERE id='b1'`:                       filepath.Join(home, "backups", "x.agentbak"),
		`SELECT path FROM capability_dependency_preferences WHERE 1=1`: filepath.Join(home, "deps", "node", "bin", "node"),
	} {
		var got string
		if err := rdb.QueryRow(q).Scan(&got); err != nil || got != want {
			t.Errorf("%s = %q (%v), want %q", q, got, err, want)
		}
	}
	var lockJSON, hash, payload, policy, changes, tb1, tb2 string
	if err := rdb.QueryRow(`SELECT lock_json,lock_sha256 FROM capability_versions WHERE id='v1'`).Scan(&lockJSON, &hash); err != nil {
		t.Fatal(err)
	}
	if hash != sha256hex(lockJSON) {
		t.Errorf("lock_sha256 = %s, want sha256 of remapped lock_json", hash)
	}
	var parsed struct {
		Resolved map[string]struct{ Directory, Path, Source string }
		Prefs    map[string]struct{ Path string } `json:"dependency_preferences"`
		Comps    []struct{ Path string }          `json:"components"`
	}
	if err := json.Unmarshal([]byte(lockJSON), &parsed); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(home, "data", "runtimes", "node", "v1")
	wantBin := filepath.Join(wantDir, "bin", "node")
	if parsed.Resolved["node"].Directory != wantDir || parsed.Resolved["node"].Path != wantBin {
		t.Errorf("lock resolved node = %+v, want dir %q path %q", parsed.Resolved["node"], wantDir, wantBin)
	}
	if parsed.Resolved["sys"].Path != "/usr/local/bin/node" {
		t.Errorf("system path remapped: %q", parsed.Resolved["sys"].Path)
	}
	if parsed.Prefs["node"].Path != filepath.Join(home, "deps", "node", "bin", "node") {
		t.Errorf("lock preference path = %q", parsed.Prefs["node"].Path)
	}
	if len(parsed.Comps) != 1 || parsed.Comps[0].Path != "skills/foo" {
		t.Errorf("relative component path remapped: %+v", parsed.Comps)
	}
	if _, _, err := capabilityManagedReference(filepath.Join(home, "data"), capability.Resolved{Directory: parsed.Resolved["node"].Directory, Path: parsed.Resolved["node"].Path, Source: "managed"}); err != nil {
		t.Errorf("remapped managed reference rejected: %v", err)
	}
	if _, err := rdb.Exec(`UPDATE capability_versions SET lock_json=lock_json`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Errorf("immutability trigger missing after remap: %v", err)
	}
	if err := rdb.QueryRow(`SELECT payload_json FROM capability_proposals WHERE id='p1'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, wantBin) || strings.Contains(payload, oldData+"/runtimes") {
		t.Errorf("proposal payload still has old paths: %s", payload)
	}
	if err := rdb.QueryRow(`SELECT effective_cost_policy_json, agent_config_changes FROM runs WHERE id='r1'`).Scan(&policy, &changes); err != nil {
		t.Fatal(err)
	}
	if policy != `{"mode":"task","revision":1,"currency":"USD","timezone":"Asia/Shanghai"}` {
		t.Errorf("cost policy JSON rewritten: %s", policy)
	}
	wantClaude := filepath.Join(home, "workspaces", "t1", "CLAUDE.md")
	if !strings.Contains(changes, wantClaude) || !strings.Contains(changes, `/Users/张三/.claude/settings.json`) || strings.Contains(changes, oldHome+"/workspaces") {
		t.Errorf("agent_config_changes = %s", changes)
	}
	if err := rdb.QueryRow(`SELECT config_json FROM trigger_bindings WHERE id='tb1'`).Scan(&tb1); err != nil {
		t.Fatal(err)
	}
	if err := rdb.QueryRow(`SELECT config_json FROM trigger_bindings WHERE id='tb2'`).Scan(&tb2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tb1, filepath.Join(home, "workspaces", "t1")) || strings.Contains(tb1, oldHome+"/workspaces") {
		t.Errorf("file_change trigger under home = %s", tb1)
	}
	if tb2 != `{"path":"/Users/张三/code/app"}` {
		t.Errorf("user project trigger remapped: %s", tb2)
	}
}
