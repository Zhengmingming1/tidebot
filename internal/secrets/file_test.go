package secrets

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestFileBackend(t *testing.T) {
	d := t.TempDir()
	f := NewFileBackend(d)
	if _, err := f.Get("x"); err == nil {
		t.Fatal("empty")
	}
	if err := f.Set("tg_token", "123:abc-secret"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(f.Path)
	if strings.Contains(string(raw), "abc-secret") {
		t.Fatal("plaintext on disk")
	}
	st, _ := os.Stat(f.KeyPath)
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
	if v, _ := NewFileBackend(d).Get("tg_token"); v != "123:abc-secret" {
		t.Fatal(v)
	}
	_ = f.Delete("tg_token")
	if _, err := f.Get("tg_token"); err == nil {
		t.Fatal("deleted")
	}
}
