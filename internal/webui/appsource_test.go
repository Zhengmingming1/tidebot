package webui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// appJSSource is the unminified concatenation of feature sources in bundle
// order. Tests that inspect or eval UI logic read this, not the shipped
// (minified) static/app.js.
func appJSSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("src", "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, name := range names {
		chunk, err := os.ReadFile(filepath.Join("src", name))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(bytesCRLFToLF(chunk))
	}
	return b.String()
}

func bytesCRLFToLF(b []byte) []byte {
	return []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
}

// TestWebUIBundleCurrent: the checked-in static/app.js matches the minified
// assembly of src/ (same check CI runs via scripts/build-webui.py --check).
func TestWebUIBundleCurrent(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		py, err = exec.LookPath("python")
		if err != nil {
			t.Skip("python3 not installed")
		}
	}
	cmd := exec.Command(py, filepath.Join("..", "..", "scripts", "build-webui.py"), "--check")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("WebUI bundle is stale: run python3 scripts/build-webui.py\n%s", out)
	}
}
