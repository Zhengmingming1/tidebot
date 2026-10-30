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
