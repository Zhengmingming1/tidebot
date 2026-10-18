package catalog

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/pricing"
)

func TestBundledCatalogIsValid(t *testing.T) {
	b := Bundled()
	p, m := b.Count()
	if p < 20 || m < 50 {
		t.Fatalf("bundled catalog too small: %d presets, %d models", p, m)
	}
	for _, id := range []string{"anthropic", "kimi-cn", "deepseek", "zhipu", "qwen"} {
		if len(b.Preset(id)) == 0 {
			t.Fatalf("bundled catalog has no prices for preset %s", id)
		}
		for _, mp := range b.Preset(id) {
			if mp.Source == "" || mp.Fetched == "" {
				t.Fatalf("%s/%s: official price without source/date", id, mp.Model)
			}
		}
	}
	if len(b.Sources) == 0 {
		t.Fatal("no sources")
	}
}

func testCat(gen string, models ...string) *Catalog {
	c := &Catalog{Schema: Schema, Generated: gen, Presets: map[string][]pricing.ModelPrice{}, Models: map[string]pricing.ModelPrice{}}
	for _, m := range models {
		c.Models[m] = pricing.ModelPrice{Model: m, Currency: pricing.USD, Input: pricing.F(1), Output: pricing.F(2)}
	}
	b, _ := json.Marshal(c)
	out, err := Parse(b)
	if err != nil {
		panic(err)
	}
	return out
}

func TestLookupFallbackByPrefix(t *testing.T) {
	c := testCat("2026-10-01T00:00:00Z", "claude-sonnet-4-5", "deepseek-v4-flash", "gpt-4", "kimi-k3")
	cases := map[string]string{
		"claude-sonnet-4-5":                   "claude-sonnet-4-5",
		"Claude-Sonnet-4-5[1m]":               "claude-sonnet-4-5",
		"anthropic/claude-sonnet-4-5":         "claude-sonnet-4-5",
		"claude-sonnet-4-5-20250929":          "claude-sonnet-4-5",
		"us.anthropic.claude-sonnet-4-5-v1:0": "",
		"deepseek-v4-flash-0901":              "deepseek-v4-flash",
		"moonshotai/kimi-k3:free":             "kimi-k3",
		"gpt-4o":                              "", // not a word boundary
		"gpt-4-turbo":                         "gpt-4",
		"unknown-model":                       "",
	}
	for in, want := range cases {
		got := c.Lookup(in)
		switch {
		case want == "" && got != nil:
			t.Errorf("%s: want no match, got %s", in, got.Model)
		case want != "" && (got == nil || got.Model != want):
			t.Errorf("%s: want %s, got %v", in, want, got)
		}
	}
}

func TestAcceptGuards(t *testing.T) {
	cur := testCat("2026-10-01T00:00:00Z", "a", "b", "c", "d")
	if err := Accept(cur, testCat("2026-09-01T00:00:00Z", "a", "b", "c", "d")); err != ErrNotNewer {
		t.Fatalf("older catalog: %v", err)
	}
	if err := Accept(cur, testCat("2026-11-01T00:00:00Z", "a")); err != ErrShrunk {
		t.Fatalf("shrunk catalog: %v", err)
	}
	if err := Accept(cur, testCat("2026-11-01T00:00:00Z", "a", "b", "c")); err != nil {
		t.Fatalf("newer catalog refused: %v", err)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	for _, s := range []string{`{}`, `{"schema":2,"generated":"2026-01-01T00:00:00Z"}`, `{"schema":1,"generated":"yesterday"}`,
		`{"schema":1,"generated":"2026-01-01T00:00:00Z","models":{"x":{"model":"x","currency":"EUR"}}}`} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
}

func TestInstallVerifyLoad(t *testing.T) {
	defer Reset()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	b := Bundled()
	next := *b
	next.Generated = b.Time().Add(24 * time.Hour).Format(time.RFC3339)
	next.Models = map[string]pricing.ModelPrice{}
	for k, v := range b.Models {
		next.Models[k] = v
	}
	next.Models["tidebot-test-model"] = pricing.ModelPrice{Model: "tidebot-test-model", Currency: pricing.USD, Input: pricing.F(9), Output: pricing.F(9)}
	body, _ := json.Marshal(next)
	sig := Sign(priv, body)
	dir := t.TempDir()

	// tampered body / update-manifest-style signature without the context are refused
	if _, err := Install(dir, pubB64, append([]byte(" "), body...), sig); err == nil {
		t.Fatal("tampered catalog accepted")
	}
	plain := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body))
	if _, err := Install(dir, pubB64, body, plain); err == nil {
		t.Fatal("signature without domain context accepted")
	}
	if _, err := Install(dir, pubB64, body, sig); err != nil {
		t.Fatal(err)
	}
	if Active().Lookup("tidebot-test-model") == nil {
		t.Fatal("installed catalog not active")
	}
	Reset()
	if Active() != Bundled() {
		t.Fatal("reset")
	}
	if c, err := Load(dir, pubB64); err != nil || c == nil {
		t.Fatalf("load: %v %v", c, err)
	}
	if Active().Lookup("tidebot-test-model") == nil {
		t.Fatal("loaded catalog not active")
	}
	// a saved file edited on disk fails verification
	Reset()
	_ = os.WriteFile(filepath.Join(dir, FileName), append(body, ' '), 0o600)
	if _, err := Load(dir, pubB64); err == nil {
		t.Fatal("edited saved catalog accepted")
	}
}
