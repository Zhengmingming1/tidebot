package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFileBackendPreservesInvalidKey(t *testing.T) {
	for _, size := range []int{0, 1, 31, 33} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			f := NewFileBackend(t.TempDir())
			invalid := bytes.Repeat([]byte{7}, size)
			if err := os.WriteFile(f.KeyPath, invalid, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := f.MasterKey(); err == nil {
				t.Fatal("invalid key accepted/replaced")
			}
			if err := f.Set("fixture", "fake-value"); err == nil {
				t.Fatal("write accepted invalid key")
			}
			got, err := os.ReadFile(f.KeyPath)
			if err != nil || !bytes.Equal(got, invalid) {
				t.Fatalf("key changed: %v", err)
			}
			if _, err := os.Stat(f.Path); !os.IsNotExist(err) {
				t.Fatalf("created ciphertext: %v", err)
			}
		})
	}
}

func TestFileBackendMissingKeyPreservesStore(t *testing.T) {
	for _, op := range []string{"get", "set", "delete", "master"} {
		t.Run(op, func(t *testing.T) {
			f := NewFileBackend(t.TempDir())
			if err := f.Set("fixture", "fake-value"); err != nil {
				t.Fatal(err)
			}
			key, err := os.ReadFile(f.KeyPath)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(f.KeyPath); err != nil {
				t.Fatal(err)
			}
			switch op {
			case "get":
				_, err = f.Get("fixture")
			case "set":
				err = f.Set("fixture", "new-fake-value")
			case "delete":
				err = f.Delete("fixture")
			case "master":
				_, err = f.MasterKey()
			}
			if err == nil {
				t.Fatal("missing key accepted")
			}
			if _, err := os.Stat(f.KeyPath); !os.IsNotExist(err) {
				t.Fatalf("replacement key created: %v", err)
			}
			after, err := os.ReadFile(f.Path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("ciphertext changed: %v", err)
			}
			if err := os.WriteFile(f.KeyPath, key, 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := f.Get("fixture"); err != nil || got != "fake-value" {
				t.Fatalf("restore: %q, %v", got, err)
			}
		})
	}
}

func TestFileBackendConcurrentKeyCreation(t *testing.T) {
	dir := t.TempDir()
	start := make(chan struct{})
	results := make(chan []byte, 32)
	for range 32 {
		go func() {
			<-start
			// Separate instances model callers without a shared mutex.
			k, _ := NewFileBackend(dir).MasterKey()
			results <- k
		}()
	}
	close(start)
	var keys [][]byte
	for range 32 {
		if k := <-results; k != nil {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		t.Fatal("no creator succeeded")
	}
	persisted, err := NewFileBackend(dir).MasterKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if !bytes.Equal(k, persisted) {
			t.Fatal("returned a key that was overwritten")
		}
	}
}

func TestFileBackendKeyReadError(t *testing.T) {
	f := NewFileBackend(t.TempDir())
	if err := os.Mkdir(f.KeyPath, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.MasterKey(); err == nil || !strings.Contains(err.Error(), "read secrets key") {
		t.Fatalf("expected original read failure, got %v", err)
	}
	if info, err := os.Stat(f.KeyPath); err != nil || !info.IsDir() {
		t.Fatalf("key path altered: %v", err)
	}
}

func TestFileBackendInitialKeyIsStable(t *testing.T) {
	f := NewFileBackend(t.TempDir())
	first, err := f.MasterKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Set("fixture", "fake-value"); err != nil {
		t.Fatal(err)
	}
	second, err := NewFileBackend(filepath.Dir(f.Path)).MasterKey()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("initialization changed master key: %v", err)
	}
}

func TestFileBackendDoesNotFollowDanglingKeyLink(t *testing.T) {
	f := NewFileBackend(t.TempDir())
	target := filepath.Join(filepath.Dir(f.Path), "missing-target")
	if err := os.Symlink(target, f.KeyPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := f.MasterKey(); err == nil {
		t.Fatal("created key through dangling symlink")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("created link target: %v", err)
	}
	if got, err := os.Readlink(f.KeyPath); err != nil || got != target {
		t.Fatalf("changed key link: %v", err)
	}
}
