package webui

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopSave(t *testing.T) {
	c := login(t)
	if code, out := c.json(http.MethodGet, "/api/desktop/save", nil); code != 200 || out["available"] != false {
		t.Fatalf("browser get %d %v", code, out)
	}
	if rr := c.do(http.MethodPost, "/api/desktop/save?name=a.csv", "application/octet-stream", []byte("x")); rr.Code != 501 {
		t.Fatalf("browser post %d", rr.Code)
	}
	dir := t.TempDir()
	var asked string
	c.s.SaveFile = func(name string) (string, error) {
		asked = name
		if name == "cancel.txt" {
			return "", nil
		}
		return filepath.Join(dir, name), nil
	}
	if _, out := c.json(http.MethodGet, "/api/desktop/save", nil); out["available"] != true {
		t.Fatalf("desktop get %v", out)
	}
	body := []byte("\xef\xbb\xbfdate,cost\n")
	rr := c.do(http.MethodPost, "/api/desktop/save?name="+"..%2F..%2Fetc%2Fspend.csv", "application/octet-stream", body)
	if rr.Code != 200 || asked != "spend.csv" {
		t.Fatalf("save %d %s asked=%q", rr.Code, rr.Body, asked)
	}
	got, err := os.ReadFile(filepath.Join(dir, "spend.csv"))
	if err != nil || string(got) != string(body) {
		t.Fatalf("written %q %v", got, err)
	}
	if rr := c.do(http.MethodPost, "/api/desktop/save?name=cancel.txt", "", []byte("x")); rr.Code != 200 || !strings.Contains(rr.Body.String(), "canceled") {
		t.Fatalf("cancel %d %s", rr.Code, rr.Body)
	}
	for in, want := range map[string]string{"": "tidebot-export", `C:\x\run.jsonl`: "run.jsonl", "a\nb.txt": "a_b.txt", "..": "tidebot-export"} {
		if g := saveName(in); g != want {
			t.Errorf("saveName(%q)=%q want %q", in, g, want)
		}
	}
}
