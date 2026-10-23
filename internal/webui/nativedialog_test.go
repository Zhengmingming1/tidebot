package webui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestNoNativeDialogs: window.confirm/alert/prompt are silently dead in the macOS
// desktop app (Wails' WKWebView has no JS panel delegate, so confirm() returns
// false without showing anything). Use confirmDialog()/toast() instead.
func TestNoNativeDialogs(t *testing.T) {
	call := regexp.MustCompile(`(^|[^.\w$])(window\.)?(confirm|alert|prompt)\s*\(`)
	comment := regexp.MustCompile(`(^|\s)//.*$`)
	n := 0
	err := fs.WalkDir(Static, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".html")) {
			return err
		}
		b, _ := Static.ReadFile(p)
		for i, line := range strings.Split(string(b), "\n") {
			code := comment.ReplaceAllString(line, "")
			if m := call.FindString(code); m != "" {
				t.Errorf("%s:%d: native dialog %q; use confirmDialog() or toast()", p, i+1, strings.TrimSpace(m))
			}
		}
		n++
		return nil
	})
	if err != nil || n == 0 {
		t.Fatal("no static files scanned", err)
	}
	src, _ := Static.ReadFile("static/app.js")
	for _, want := range []string{
		`rpc("update.check", { async: true }, 15000)`,
		`rpc("update.apply", { async: true }, 15000)`,
		`confirmDialog({ title: T("up.confirm_title"`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("app.js: missing %s", want)
		}
	}
}
