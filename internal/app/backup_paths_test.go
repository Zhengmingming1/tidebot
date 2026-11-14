package app

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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

// A backup taken on another machine (home folder with a Chinese user name, a data
// folder of its own) is restored: every stored path under the old folders points
// into the new home, paths that only share a prefix are left alone.
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
}
