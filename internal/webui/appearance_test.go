package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

// The saved look reaches the page in the index response (no flash before a script could
// apply it), only whitelisted values are stored, and the API needs a session and CSRF.
func TestAppearanceSavedAndInPage(t *testing.T) {
	s, _ := testServer(t)
	h := s.routes()
	index := func() string {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
		return rr.Body.String()
	}
	if !strings.Contains(index(), `<html lang="en" data-theme="graphite" data-mode="system">`) {
		t.Fatal("default look missing on <html>")
	}

	body, _ := json.Marshal(map[string]string{"password": "testpass99"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body)))
	var lr map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &lr)
	csrf, _ := lr["csrf_token"].(string)
	var sid string
	for _, c := range rr.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			sid = c.Value
		}
	}
	put := func(b string, withCSRF bool) int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/settings/appearance", strings.NewReader(b))
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sid})
		if withCSRF {
			req.Header.Set(auth.CSRFHeader, csrf)
		}
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if c := put(`{"theme":"tide","mode":"dark"}`, false); c != 403 {
		t.Fatalf("no csrf: %d", c)
	}
	for _, bad := range []string{`{"theme":"neon","mode":"dark"}`, `{"theme":"tide","mode":"dim"}`, `{"theme":"\"><script>","mode":"dark"}`, `not json`} {
		if c := put(bad, true); c != 400 {
			t.Fatalf("%s: %d", bad, c)
		}
	}
	if c := put(`{"theme":"midnight","mode":"dark"}`, true); c != 200 {
		t.Fatalf("save: %d", c)
	}
	if got := s.Appearance(context.Background()); got != (Appearance{"midnight", "dark"}) {
		t.Fatalf("saved %+v", got)
	}
	if !strings.Contains(index(), `<html lang="en" data-theme="midnight" data-mode="dark">`) {
		t.Fatal("saved look not on <html>")
	}
	if r, g, b := s.WindowBackground(context.Background(), false); fmt.Sprintf("#%02x%02x%02x", r, g, b) != "#0d0d10" {
		t.Fatalf("window bg %02x%02x%02x", r, g, b)
	}
}

// Every theme has a light block, a forced-dark block and a follow-the-system dark block,
// and the desktop window colours match each palette's --bg.
func TestThemeTokens(t *testing.T) {
	raw, err := Static.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	bg := func(sel string) string {
		i := strings.Index(css, "\n"+sel+" {")
		if i < 0 {
			i = strings.Index(css, "\n"+sel+",")
		}
		if i < 0 {
			t.Fatalf("missing %s", sel)
		}
		m := regexp.MustCompile(`--bg: (#[0-9a-f]{6});`).FindStringSubmatch(css[i:])
		if m == nil {
			t.Fatalf("%s: no --bg", sel)
		}
		return m[1]
	}
	for th, c := range windowBG {
		light := fmt.Sprintf(`html[data-theme="%s"]`, th)
		if th == "graphite" {
			light = `:root, html[data-theme="graphite"]`
		}
		if got, want := bg(light), fmt.Sprintf("#%02x%02x%02x", c[0][0], c[0][1], c[0][2]); got != want {
			t.Errorf("%s light: css %s, window %s", th, got, want)
		}
		if got, want := bg(fmt.Sprintf(`html[data-theme="%s"][data-mode="dark"]`, th)), fmt.Sprintf("#%02x%02x%02x", c[1][0], c[1][1], c[1][2]); got != want {
			t.Errorf("%s dark: css %s, window %s", th, got, want)
		}
		if !strings.Contains(css, fmt.Sprintf("@media (prefers-color-scheme: dark) {\nhtml[data-theme=%q][data-mode=\"system\"] {", th)) {
			t.Errorf("%s: no follow-the-system dark block", th)
		}
	}
	// colours on accent / tooltips come from tokens, so dark palettes stay legible
	for _, s := range []string{"background: var(--tip-bg); color: var(--tip-fg);", ".btn.primary { background: var(--primary-bg)", "input.tgl { -webkit-appearance: none;"} {
		if !strings.Contains(css, s) {
			t.Errorf("styles.css lacks %q", s)
		}
	}
}
