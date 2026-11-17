package guard

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCheckRunDir(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, ".tidebot")
	proj := filepath.Join(home, "code", "proj")
	for _, d := range []string{proj, filepath.Join(home, ".ssh", "keys"), filepath.Join(home, ".claude", "skills"), filepath.Join(app, "data")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(proj, "f.txt"), []byte("x"), 0o600)
	realProj, _ := filepath.EvalSymlinks(proj)
	cases := []struct {
		path, want string
	}{
		{proj, ""},
		{"code/proj", DirRelative},
		{filepath.Join(home, "nope"), DirNotFound},
		{filepath.Join(proj, "f.txt"), DirNotDir},
		{filepath.Join(home, ".ssh"), DirSensitive},
		{filepath.Join(home, ".ssh", "keys"), DirSensitive},
		{filepath.Join(home, ".claude"), DirSensitive},
		{home, DirContainsSensitive},
		{filepath.Join(app, "data"), DirInternal},
		{filepath.VolumeName(home) + string(filepath.Separator), DirRoot},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, struct{ path, want string }{"/etc", DirSensitive})
	} else {
		cases = append(cases, struct{ path, want string }{`C:\Windows`, DirSensitive})
	}
	for _, c := range cases {
		got, prob := CheckRunDir(c.path, home, app)
		if prob != c.want {
			t.Errorf("%s: problem %q, want %q", c.path, prob, c.want)
		}
		if c.want == "" && got != realProj {
			t.Errorf("%s: real path %q, want %q", c.path, got, realProj)
		}
	}
	// A symlink is judged by where it points.
	link := filepath.Join(home, "code", "keys-link")
	if err := os.Symlink(filepath.Join(home, ".ssh"), link); err == nil {
		if _, prob := CheckRunDir(link, home, app); prob != DirSensitive {
			t.Errorf("symlink to ~/.ssh: %q", prob)
		}
		ok := filepath.Join(home, "proj-link")
		_ = os.Symlink(proj, ok)
		if got, prob := CheckRunDir(ok, home, app); prob != "" || got != realProj {
			t.Errorf("symlink to a project: %q %q", got, prob)
		}
	}
}

func TestCheckPlannedDir(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, ".tidebot")
	proj := filepath.Join(home, "code", "proj")
	for _, d := range []string{proj, filepath.Join(home, ".ssh"), app} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	realProj, _ := filepath.EvalSymlinks(proj)
	// Existing project: same as CheckRunDir, including the resolved path.
	if got, prob := CheckPlannedDir(proj, home, app); prob != "" || got != realProj {
		t.Fatalf("existing project: %q %q", got, prob)
	}
	// A folder that does not exist yet, under a normal project: allowed (the
	// task creates it on first run). The ancestor is resolved.
	newProj := filepath.Join(proj, "new")
	wantNew := filepath.Join(realProj, "new")
	if got, prob := CheckPlannedDir(newProj, home, app); prob != "" || got != wantNew {
		t.Fatalf("new under project: %q %q want %q", got, prob, wantNew)
	}
	// Missing or existing sensitive paths, and the home folder itself, are refused
	// even before anything is created.
	for _, c := range []struct{ path, want string }{
		{filepath.Join(home, ".ssh"), DirSensitive},
		{filepath.Join(home, ".ssh", "new"), DirSensitive},
		{home, DirContainsSensitive},
		{app, DirInternal},
		{filepath.Join(app, "data", "x"), DirInternal},
		{"rel", DirRelative},
		{filepath.VolumeName(home) + string(filepath.Separator), DirRoot},
	} {
		if _, prob := CheckPlannedDir(c.path, home, app); prob != c.want {
			t.Errorf("%s: problem %q, want %q", c.path, prob, c.want)
		}
	}
	// A missing path under a symlink into ~/.ssh is refused (the ancestor is resolved).
	link := filepath.Join(home, "code", "keys-link")
	if err := os.Symlink(filepath.Join(home, ".ssh"), link); err != nil {
		t.Skip("no symlinks here")
	}
	if _, prob := CheckPlannedDir(filepath.Join(link, "new"), home, app); prob != DirSensitive {
		t.Errorf("new under symlink to ~/.ssh: %q", prob)
	}
	if _, prob := CheckPlannedDir(link, home, app); prob != DirSensitive {
		t.Errorf("symlink to ~/.ssh: %q", prob)
	}
	ok := filepath.Join(home, "proj-link")
	if err := os.Symlink(proj, ok); err != nil {
		t.Fatal(err)
	}
	if got, prob := CheckPlannedDir(filepath.Join(ok, "new"), home, app); prob != "" || got != wantNew {
		t.Errorf("new under symlink to a project: %q %q", got, prob)
	}
}
