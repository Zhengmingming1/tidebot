package webui

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/osopen"
)

func TestDesktopOpen(t *testing.T) {
	c := login(t)
	if code, out := c.json(http.MethodGet, "/api/desktop/open", nil); code != 200 || out["available"] != false {
		t.Fatalf("browser get %d %v", code, out)
	}
	if code, _ := c.json(http.MethodPost, "/api/desktop/open", map[string]string{"target": "data"}); code != 501 {
		t.Fatalf("browser post %d", code)
	}
	type call struct {
		p      string
		reveal bool
	}
	var calls []call
	c.s.OpenPath = func(p string, reveal bool) error {
		calls = append(calls, call{p, reveal})
		if filepath.Base(p) == "gone.csv" {
			return osopen.ErrNotFound
		}
		return nil
	}
	dir := t.TempDir()
	c.s.SaveFile = func(name string) (string, error) { return filepath.Join(dir, name), nil }
	if code, _ := c.json(http.MethodPost, "/api/desktop/open", map[string]string{"target": "data"}); code != 200 || len(calls) != 1 || calls[0].p != c.s.deps.Cfg.DataDir || calls[0].reveal {
		t.Fatalf("data %d %v", code, calls)
	}
	rr := c.do(http.MethodPost, "/api/desktop/save?name=spend.csv", "application/octet-stream", []byte("x"))
	var sv map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &sv)
	if sv["reveal"] == "" {
		t.Fatalf("no reveal id: %s", rr.Body)
	}
	if code, _ := c.json(http.MethodPost, "/api/desktop/open", map[string]string{"target": "saved", "id": sv["reveal"]}); code != 200 || calls[1].p != filepath.Join(dir, "spend.csv") || !calls[1].reveal {
		t.Fatalf("saved %d %v", code, calls)
	}
	// Unknown ids, paths and targets never reach the file manager.
	n := len(calls)
	for _, body := range []map[string]string{{"target": "saved", "id": "nope"}, {"target": "saved"}, {"target": "/etc"}, {"target": "path", "id": "/etc"}} {
		if code, _ := c.json(http.MethodPost, "/api/desktop/open", body); code != 404 && code != 400 {
			t.Fatalf("%v → %d", body, code)
		}
	}
	if len(calls) != n {
		t.Fatalf("file manager called for bad input: %v", calls[n:])
	}
	// Only the last few saves are remembered.
	var first string
	for i := 0; i < maxSavedFiles+1; i++ {
		id := c.s.saved.add(filepath.Join(dir, "x.csv"))
		if i == 0 {
			first = id
		}
	}
	if c.s.saved.get(first) != "" {
		t.Fatal("oldest saved id should be forgotten")
	}
	gone := c.s.saved.add(filepath.Join(dir, "gone.csv"))
	if code, _ := c.json(http.MethodPost, "/api/desktop/open", map[string]string{"target": "saved", "id": gone}); code != 404 {
		t.Fatalf("missing file %d", code)
	}
}
