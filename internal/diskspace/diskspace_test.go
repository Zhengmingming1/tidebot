package diskspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOfTempDir(t *testing.T) {
	in, err := Of(t.TempDir())
	if errors.Is(err, ErrUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if in.Total == 0 || in.Free > in.Total {
		t.Fatalf("implausible %+v", in)
	}
}

func TestOfMissingPath(t *testing.T) {
	if _, err := Of(filepath.Join(t.TempDir(), "nope", "x")); err == nil {
		t.Fatal("want error for a missing path")
	}
}

func TestOfFileAndUnicodePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "数据 dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "a.db")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, f} {
		if _, err := Of(p); err != nil && !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s: %v", p, err)
		}
	}
}
