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

// cssSource is the unminified stylesheet. Tests that inspect comments or
// exact authoring form read this, not the shipped (minified) static/styles.css.
func cssSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("src", "styles.css"))
	if err != nil {
		t.Fatal(err)
	}
	return string(bytesCRLFToLF(raw))
}

// TestWebUIBundleCurrent: the checked-in static/app.js and static/styles.css
// match the minified assembly of src/ (same check CI runs via scripts/build-webui.py --check).
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

func TestWebUICSSMinify(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		py, err = exec.LookPath("python")
		if err != nil {
			t.Skip("python3 not installed")
		}
	}
	cmd := exec.Command(py, filepath.Join("..", "..", "scripts", "build-webui.py"), "--self-test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CSS minify self-test failed:\n%s", out)
	}
	src := cssSource(t)
	shipped, err := os.ReadFile(filepath.Join("static", "styles.css"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(bytesCRLFToLF(shipped))
	if len(got) >= len(src) {
		t.Fatalf("shipped styles.css is not smaller than the source: %d vs %d", len(got), len(src))
	}
}

// TestWebUIBundleParses: the shipped bundle (and the source assembly) must be
// valid JavaScript. String-inspect tests do not catch quote mistakes that only
// show up after minify, which would leave the WebUI stuck on the splash.
func TestWebUIBundleParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	check := func(path string) {
		t.Helper()
		cmd := exec.Command(node, "--check", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s is not valid JavaScript:\n%s", path, out)
		}
	}
	check(filepath.Join("static", "app.js"))
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "app.js")
	if err := os.WriteFile(srcPath, []byte(appJSSource(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	check(srcPath)
}
