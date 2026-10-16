package webui

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"testing"
)

func loadDict(t *testing.T, lang string) map[string]string {
	t.Helper()
	b, err := Static.ReadFile("static/i18n/" + lang + ".json")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", lang, err)
	}
	return m
}

var placeholderRe = regexp.MustCompile(`\{(\w+)\}`)

func placeholders(s string) []string {
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// TestI18nKeysComplete: every key exists in both locales, is non-empty, and uses the same placeholders.
func TestI18nKeysComplete(t *testing.T) {
	en, zh := loadDict(t, "en"), loadDict(t, "zh-CN")
	for k, v := range en {
		z, ok := zh[k]
		if !ok {
			t.Errorf("zh-CN missing key %q", k)
			continue
		}
		if v == "" || z == "" {
			t.Errorf("empty value for %q", k)
		}
		if a, b := placeholders(v), placeholders(z); len(a) != len(b) || (len(a) > 0 && a[0] != b[0]) {
			t.Errorf("placeholder mismatch for %q: %v vs %v", k, a, b)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("en missing key %q", k)
		}
	}
}

// TestI18nKeysUsed: every literal T("key") used by app.js exists in the dictionaries.
func TestI18nKeysUsed(t *testing.T) {
	src, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	en := loadDict(t, "en")
	re := regexp.MustCompile(`\bT\("([a-z0-9_.]+)"`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		if dynamicPrefix(m[1]) {
			continue
		}
		if _, ok := en[m[1]]; !ok {
			t.Errorf("app.js uses unknown key %q", m[1])
		}
	}
	// keys referenced indirectly (REQ_ST table, plural(), nav./st. prefixes) must also exist
	re2 := regexp.MustCompile(`"((?:req|sys|tasks|sp)\.[a-z0-9_]+)"`)
	for _, m := range re2.FindAllStringSubmatch(string(src), -1) {
		if dynamicPrefix(m[1]) {
			continue
		}
		if _, ok := en[m[1]]; !ok {
			t.Errorf("app.js references unknown key %q", m[1])
		}
	}
}

// dynamicPrefix reports keys built at runtime ("nav." + name); those fall back to raw values.
func dynamicPrefix(k string) bool { return k[len(k)-1] == '.' || k[len(k)-1] == '_' }
