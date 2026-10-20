package provider

import "testing"

func TestMatchBaseURL(t *testing.T) {
	ps := BuiltIn()
	cases := []struct {
		in, preset, url, name string
		fixed                 bool
	}{
		{"https://api.deepseek.com/anthropic", "deepseek", "https://api.deepseek.com/anthropic", "deepseek", false},
		{"  api.deepseek.com/anthropic/v1/messages ", "deepseek", "https://api.deepseek.com/anthropic", "deepseek", true},
		{"https://api.deepseek.com", "deepseek", "https://api.deepseek.com/anthropic", "deepseek", true},
		{"https://api.moonshot.cn/anthropic/", "kimi-cn", "https://api.moonshot.cn/anthropic", "kimi-cn", false},
		{"https://api.kimi.com/coding", "kimi-coding", "https://api.kimi.com/coding/", "kimi-coding", false},
		{"https://coding.dashscope.aliyuncs.com/apps/anthropic", "qwen-coding", "https://coding.dashscope.aliyuncs.com/apps/anthropic", "qwen-coding", false},
		{"https://api.anthropic.com/v1", "anthropic", "", "anthropic", true},
		{"https://llm.example-gw.com/claude/v1?x=1#f", "", "https://llm.example-gw.com/claude", "example-gw", true},
		{"localhost:3425", "", "http://localhost:3425", "local-3425", true},
		{"http://127.0.0.1:3425", "magpie", "http://127.0.0.1:3425", "magpie", false},
	}
	for _, c := range cases {
		m := MatchBaseURL(c.in, ps)
		id := ""
		if m.Preset != nil {
			id = m.Preset.ID
		}
		if id != c.preset || m.BaseURL != c.url || m.Name != c.name || m.Fixed != c.fixed {
			t.Errorf("%q → preset %q url %q name %q fixed %v; want %q %q %q %v", c.in, id, m.BaseURL, m.Name, m.Fixed, c.preset, c.url, c.name, c.fixed)
		}
	}
	if m := MatchBaseURL("", ps); m.Preset != nil || m.BaseURL != "" {
		t.Fatalf("empty %+v", m)
	}
}

func TestNameFromHostAndKey(t *testing.T) {
	for in, want := range map[string]string{"api.foo-ai.com": "foo-ai", "open.bigmodel.cn": "bigmodel", "www.API.Example.io": "example", "api.com": "api", "localhost": "local", "my_gw!.dev": "my_gw"} {
		if got := NameFromHost(in); got != want {
			t.Errorf("%s → %s want %s", in, got, want)
		}
	}
	if KeyPreset("sk-ant-a") != "anthropic" || KeyPreset("sk-1234") != "" {
		t.Fatal("key prefix")
	}
}
