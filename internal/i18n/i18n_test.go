package i18n

import (
	"strings"
	"testing"
)

func TestAllKeysInAllLocales(t *testing.T) {
	en, err := Load("en")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range Supported {
		d, err := Load(l)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range en {
			got, ok := d[k]
			if !ok || got == "" {
				t.Errorf("%s missing key %q", l, k)
				continue
			}
			if strings.Count(got, "%") != strings.Count(v, "%") {
				t.Errorf("%s key %q: format verbs differ", l, k)
			}
		}
		for k := range d {
			if _, ok := en[k]; !ok {
				t.Errorf("en missing key %q (present in %s)", k, l)
			}
		}
	}
}

func TestDetectAndT(t *testing.T) {
	t.Setenv("TIDEBOT_LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "zh_CN.UTF-8")
	if Detect() != "zh-CN" {
		t.Fatalf("detect %s", Detect())
	}
	t.Setenv("LANG", "C")
	if Detect() != "en" {
		t.Fatal("fallback en")
	}
	SetLang("zh-CN")
	if got := T("cli.required", "--id"); got != "缺少 --id" {
		t.Fatalf("got %q", got)
	}
	SetLang("en")
	if got := T("cli.required", "--id"); got != "--id required" {
		t.Fatalf("got %q", got)
	}
	if T("no.such.key") != "no.such.key" {
		t.Fatal("unknown key should fall back to key")
	}
}
