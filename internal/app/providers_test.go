package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/webui"
)

func strp(s string) *string { return &s }

func TestProviderCRUDAndSecretNonDisclosure(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	var logs bytes.Buffer
	a.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pa := providerAdmin{a}
	const secret = "sk-test-SUPERSECRET-9f3a"

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("authorization")
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"max_tokens":1`) || r.URL.Path != "/v1/messages" {
			w.WriteHeader(400)
			return
		}
		if strings.HasSuffix(gotAuth, secret) {
			_, _ = w.Write([]byte(`{"type":"message"}`))
			return
		}
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"bad key ` + strings.TrimPrefix(gotAuth, "Bearer ") + `"}`))
	}))
	defer srv.Close()

	// validation
	for _, in := range []webui.ProviderInput{
		{Name: "Bad Name", BaseURL: srv.URL},
		{Name: "x", BaseURL: "http://example.com"},
		{Name: "x", ExtraEnv: map[string]string{"OPENAI_API_KEY": "x"}},
		{Name: "x", Preset: "no-such-preset"},
		{Name: "kimi-coding"}, // duplicate of a built-in
	} {
		if _, err := pa.SaveProvider(ctx, in); err == nil {
			t.Errorf("accepted %+v", in)
		}
	}

	v, err := pa.SaveProvider(ctx, webui.ProviderInput{Name: "mygw", BaseURL: srv.URL + "/", Model: "m1", APIKey: strp(secret)})
	if err != nil {
		t.Fatal(err)
	}
	if !v.KeySet || v.KeyLast4 != "9f3a" || v.KeySource != "store" || v.BaseURL != srv.URL {
		t.Fatalf("view %+v", v)
	}
	all, _ := json.Marshal(pa.ListProviders(ctx))
	if strings.Contains(string(all), secret) || strings.Contains(string(all), "SUPERSECRET") {
		t.Fatal("list leaks the key")
	}
	if k, _ := a.Secrets.Get("provider:mygw"); k != secret {
		t.Fatal("key not in secret store")
	}

	res, err := pa.TestProvider(ctx, "mygw")
	if err != nil || res["ok"] != true || gotAuth != "Bearer "+secret {
		t.Fatalf("test %v %v", res, err)
	}

	// edit without key keeps it; rename moves it
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{OriginalName: "mygw", Name: "mygw2", BaseURL: srv.URL, Model: "m2"}); err != nil {
		t.Fatal(err)
	}
	if k, _ := a.Secrets.Get("provider:mygw2"); k != secret {
		t.Fatal("rename lost the key")
	}
	if k, _ := a.Secrets.Get("provider:mygw"); k != "" {
		t.Fatal("old key left behind")
	}
	if _, ok := a.Registry.Get("mygw"); ok {
		t.Fatal("old name still registered")
	}

	// wrong key: error from upstream is masked
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{OriginalName: "mygw2", Name: "mygw2", BaseURL: srv.URL, APIKey: strp("sk-wrong-OTHERSECRET-1234")}); err != nil {
		t.Fatal(err)
	}
	res, _ = pa.TestProvider(ctx, "mygw2")
	b, _ := json.Marshal(res)
	if res["ok"] == true || strings.Contains(string(b), "OTHERSECRET") {
		t.Fatalf("test result leaks or passes: %s", b)
	}

	// default + delete rules
	if err := pa.SetDefaultProvider(ctx, "mygw2"); err != nil {
		t.Fatal(err)
	}
	if err := pa.DeleteProvider(ctx, "mygw2"); err == nil {
		t.Fatal("deleted the default provider")
	}
	_ = pa.SetDefaultProvider(ctx, "kimi-coding")
	if err := pa.DeleteProvider(ctx, "mygw2"); err != nil {
		t.Fatal(err)
	}
	if k, _ := a.Secrets.Get("provider:mygw2"); k != "" {
		t.Fatal("delete kept the key")
	}

	// persisted: a fresh load sees user providers
	if _, err := pa.SaveProvider(ctx, webui.ProviderInput{Name: "persist", BaseURL: "https://api.example.com"}); err != nil {
		t.Fatal(err)
	}
	a.Registry.Delete("persist")
	a.applyProviderSettings(ctx)
	if _, ok := a.Registry.Get("persist"); !ok {
		t.Fatal("user provider not reloaded")
	}

	if strings.Contains(logs.String(), "SECRET") {
		t.Fatalf("secret in logs: %s", logs.String())
	}
}
