package webui

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/auth"
)

// Turning 2FA off re-checks the password; wrong guesses share the sign-in lockout.
func TestTwoFADisableRateLimited(t *testing.T) {
	c := login(t)
	ctx := context.Background()
	_, out := c.json(http.MethodPost, "/api/auth/2fa/begin", nil)
	secret, _ := out["secret"].(string)
	if code, _ := c.json(http.MethodPost, "/api/auth/2fa/confirm", map[string]any{"code": auth.TOTPCodeAt(secret, time.Now())}); code != 200 {
		t.Fatalf("confirm %d", code)
	}
	for i := 0; i < auth.LoginMaxFails; i++ {
		if code, _ := c.json(http.MethodPost, "/api/auth/2fa/disable", map[string]any{"password": "guess-" + string(rune('a'+i))}); code != 403 {
			t.Fatalf("guess %d: %d", i, code)
		}
	}
	rr := c.do(http.MethodPost, "/api/auth/2fa/disable", "application/json", []byte(`{"password":"testpass99"}`))
	if rr.Code != 429 || rr.Header().Get("Retry-After") == "" || !strings.Contains(rr.Body.String(), `"retry_after"`) {
		t.Fatalf("want 429, got %d %s", rr.Code, rr.Body.String())
	}
	if on, _ := c.s.deps.Auth.TOTPEnabled(ctx); !on {
		t.Fatal("2fa turned off while rate limited")
	}
}

func TestTwoFADisableUI(t *testing.T) {
	js, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	for _, want := range []string{`id="tf-off-form"`, `autocomplete="current-password"`, `x.status === 429`, `"bad_password"`, `twoFADisable(st.password_set !== false)`} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
	for _, lang := range []string{"en", "zh-CN"} {
		b, err := os.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{`"tf.disable_sub_nopw"`, `"tf.need_pw"`, `"pw.too_many"`, `"tf.bad_pw"`} {
			if !strings.Contains(string(b), k) {
				t.Errorf("%s missing %s", lang, k)
			}
		}
	}
}
