package plugin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const schemaYAML = `
type: object
required: [region, token]
properties:
  region: {type: string, title: Region, enum: [eu, us]}
  retries: {type: integer, minimum: 0, maximum: 5, default: 2}
  verbose: {type: boolean}
  labels: {type: array, items: {enum: [a, b, c]}}
  token: {type: string, format: password}
`

func schema(t *testing.T) map[string]any {
	var m map[string]any
	if err := yaml.Unmarshal([]byte(schemaYAML), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestConfigSchemaValidation(t *testing.T) {
	fields, err := ParseSchema(schema(t))
	if err != nil || len(fields) != 5 {
		t.Fatalf("parse: %v %d", err, len(fields))
	}
	ok := map[string]any{"region": "eu", "retries": float64(3), "verbose": true, "labels": []any{"a", "c"}, "token": "t0k"}
	plain, sec, err := ValidateConfig(fields, ok)
	if err != nil || sec["token"] != "t0k" || plain["token"] != nil || plain["region"] != "eu" {
		t.Fatalf("valid: %v %v %v", err, plain, sec)
	}
	bad := []map[string]any{
		{"region": "asia"},                      // enum
		{"region": "eu", "retries": float64(9)}, // range
		{"region": "eu", "retries": 1.5},        // integer
		{"region": "eu", "labels": []any{"z"}},  // items enum
		{"region": "eu", "nope": "x"},           // unknown key
		{"retries": float64(1)},                 // required
		{"region": "eu", "verbose": "yes"},      // type
	}
	for i, b := range bad {
		if _, _, err := ValidateConfig(fields, b); err == nil {
			t.Fatalf("case %d accepted: %v", i, b)
		}
	}
	if _, err := ParseSchema(map[string]any{"properties": map[string]any{"x": map[string]any{"type": "object"}}}); err == nil {
		t.Fatal("nested object accepted")
	}
}

func TestConfigStoreNeverReturnsSecrets(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	dir := writePlugin(t, "1.0.0", "kv", false)
	y, _ := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	indented := "config_schema:\n" + indent(schemaYAML)
	os.WriteFile(filepath.Join(dir, "plugin.yaml"), append(y, []byte(indented)...), 0o644)
	v, err := s.Stage(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConfig(ctx, "demo-plugin", map[string]any{"region": "us"}); err == nil {
		t.Fatal("required secret missing but accepted")
	}
	if err := s.SetConfig(ctx, "demo-plugin", map[string]any{"region": "us", "token": "very-secret"}); err != nil {
		t.Fatal(err)
	}
	// later saves may omit the secret and keep it
	if err := s.SetConfig(ctx, "demo-plugin", map[string]any{"region": "eu"}); err != nil {
		t.Fatal(err)
	}
	cv, err := s.GetConfig(ctx, "demo-plugin")
	if err != nil || !cv.SecretsSet["token"] || cv.Values["token"] != nil || cv.Values["region"] != "eu" {
		t.Fatalf("view: %v %+v", err, cv)
	}
	var cj string
	_ = s.DB.QueryRow(`SELECT config_json FROM plugins WHERE id='demo-plugin'`).Scan(&cj)
	if strings.Contains(cj, "very-secret") {
		t.Fatal("secret stored in config_json")
	}
	if s.ActivationConfig(ctx, "demo-plugin")["token"] != "very-secret" {
		t.Fatal("plugin did not get its secret")
	}
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.Trim(s, "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}
	return b.String()
}

func TestGrantSubsetAndRevoke(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	v, _ := s.Stage(ctx, writePlugin(t, "1.0.0", "kv, notify.send", false), "")
	if err := s.Grant(ctx, v.ID, []string{"host_api:shell"}); err == nil {
		t.Fatal("granted a permission the plugin never asked for")
	}
	if err := s.Grant(ctx, v.ID, []string{"host_api:kv"}); err != nil {
		t.Fatal(err)
	}
	in, _ := s.Get(ctx, "demo-plugin")
	m := in.Current.Manifest.Restrict(in.Current.Approved)
	if !allowed(m, "kv") || allowed(m, "notify.send") || len(m.Permissions.Secrets) != 0 {
		t.Fatalf("restrict: %+v", m.Permissions)
	}
	if err := s.SetPermission(ctx, "demo-plugin", "host_api:notify.send", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPermission(ctx, "demo-plugin", "host_api:kv", false); err != nil {
		t.Fatal(err)
	}
	in, _ = s.Get(ctx, "demo-plugin")
	m = in.Current.Manifest.Restrict(in.Current.Approved)
	if allowed(m, "kv") || !allowed(m, "notify.send") {
		t.Fatalf("after toggle: %v", in.Current.Approved)
	}
	if err := s.SetPermission(ctx, "demo-plugin", "network:evil.com", true); err == nil {
		t.Fatal("granted unrequested permission")
	}
}

func TestRevokedSecretNotPassedToProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("host tests exec the test binary without .exe")
	}
	t.Setenv("TIDEBOT_TEST_PLUGIN", "1")
	s := newStore(t)
	installRunnable(t, s, "kv")
	_ = s.SetSecret(context.Background(), "demo-plugin", "token", "s3cret")
	if err := s.SetPermission(context.Background(), "demo-plugin", "secret:token", false); err != nil {
		t.Fatal(err)
	}
	h := NewHost(s, &fakeAPI{}, nil)
	defer h.StopAll()
	res, err := h.Call("demo-plugin", "test.secret", nil)
	if err != nil || strings.Contains(string(res), "s3cret") {
		t.Fatalf("revoked secret still visible: %s %v", res, err)
	}
}
