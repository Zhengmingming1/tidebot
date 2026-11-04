package netproxy

import "testing"

func TestRedactProxyCredentials(t *testing.T) {
	for _, tt := range []struct{ name, raw, want string }{
		{"empty", "", ""},
		{"plain host", "proxy.example:8080", "proxy.example:8080"},
		{"explicit scheme", "http://test-user:test-password@proxy.example:8080", "http://%2A%2A%2A@proxy.example:8080"},
		{"schemeless", "test-user:test-password@proxy.example:8080", "%2A%2A%2A@proxy.example:8080"},
		{"username only", "test-user@proxy.example:8080", "%2A%2A%2A@proxy.example:8080"},
		{"invalid escape", "http://test-user:test%password@proxy.example:8080", "[invalid proxy URL]"},
		{"invalid port", "http://test-user:test-password@proxy.example:bad", "[invalid proxy URL]"},
		{"control character", "http://test-user:test-password\n@proxy.example:8080", "[invalid proxy URL]"},
		{"malformed authority", "http:////test-user:test-password@proxy.example:8080", "[invalid proxy URL]"},
		{"password suffix in path", "http://test-user:test@pass/word@proxy.example:8080", "[invalid proxy URL]"},
		{"schemeless password suffix", "test-user:test@pass/word@proxy.example:8080", "[invalid proxy URL]"},
		{"password suffix in query", "http://test-user:test@pass?word@proxy.example:8080", "[invalid proxy URL]"},
		{"password suffix in fragment", "http://test-user:test@pass#word@proxy.example:8080", "[invalid proxy URL]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Redact(tt.raw); got != tt.want {
				t.Errorf("Redact() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveRedactsEnvironmentCredentials(t *testing.T) {
	const proxy = "test-user:test-password@proxy.example:8080"
	withEnv(t, map[string]string{"HTTPS_PROXY": proxy}, nil)
	if err := Set(Settings{Mode: ModeAuto}); err != nil {
		t.Fatal(err)
	}
	if got := Resolve().HTTPS; got != "%2A%2A%2A@proxy.example:8080" {
		t.Fatalf("displayed proxy = %q", got)
	}
	// Redaction is display-only: child processes must retain authentication.
	if !contains(Env(), "HTTPS_PROXY="+proxy) {
		t.Fatal("redaction changed the child process proxy")
	}
}
