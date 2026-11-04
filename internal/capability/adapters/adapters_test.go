package adapters

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRegistryPackagesAndRemotes(t *testing.T) {
	b := []byte(`{"name":"io.example/server","packages":[{"registryType":"npm","identifier":"@example/mcp","version":"1.2.3","transport":{"type":"stdio"},"environmentVariables":[{"name":"API_TOKEN","isSecret":true,"isRequired":true,"value":"must-not-leak"}]}],"remotes":[{"type":"streamable-http","url":"https://mcp.example.com/mcp","headers":[{"name":"Authorization","value":"must-not-leak"}]}]}`)
	result, err := ParseRegistry(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 2 || result.Requires.Tools[0].Recipes[0].Backend != "npm" {
		t.Fatalf("bad normalization: %+v", result)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "must-not-leak") || strings.Contains(string(raw), "npx") {
		t.Fatal("source credential or auto-install command leaked")
	}
	if result.Components[0].MCP.Command != "" || result.Components[1].MCP.Type != "http" {
		t.Fatal("bad transport normalization")
	}
}
func TestRegistryRejectsUnsafe(t *testing.T) {
	for _, p := range []string{
		`{"registryType":"npm","identifier":"x","version":"latest"}`,
		`{"registryType":"npm","identifier":"https://evil.example/x","version":"1.0.0"}`,
		`{"registryType":"pypi","identifier":"x","version":">=1.0"}`,
		`{"registryType":"oci","identifier":"example/image:latest"}`,
		`{"registryType":"npm","identifier":"x","version":"1.0.0","runtimeArguments":[{"value":"--eval"}]}`,
	} {
		if _, err := ParseRegistry([]byte(`{"name":"x","packages":[` + p + `]}`)); err == nil {
			t.Fatalf("accepted %s", p)
		}
	}
	for _, url := range []string{"http://example.com/mcp", "https://127.0.0.1/mcp", "https://user:pass@example.com/mcp", "https://[::1]/mcp"} {
		if _, err := ParseRegistry([]byte(`{"name":"x","remotes":[{"type":"http","url":"` + url + `"}]}`)); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
}
func TestClaudePluginDefaultsAndCustomPaths(t *testing.T) {
	files := map[string][]byte{
		"plug/.claude-plugin/plugin.json": []byte(`{"name":"example","skills":"./extra","hooks":{"PreToolUse":[]},"userConfig":{"API_TOKEN":{"sensitive":true,"required":true}}}`),
		"plug/skills/a/SKILL.md":          []byte("---\nname: a\n---\nA"),
		"plug/extra/b/SKILL.md":           []byte("B"),
		"plug/commands/check.md":          []byte("Check"),
		"plug/agents/reviewer.md":         []byte("Review"),
		"outside/skills/no/SKILL.md":      []byte("No"),
		"plug/.mcp.json":                  []byte(`{"mcpServers":{"tool":{"command":"npx","args":["-y","@example/mcp@1.2.3"],"env":{"API_TOKEN":"${secret:API_TOKEN}"}}}}`),
	}
	result, err := ParseClaudePlugin(files, "plug")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 6 {
		t.Fatalf("want six, got %+v", result.Components)
	}
	for _, c := range result.Components {
		if c.Type == "hooks" && !c.Disabled {
			t.Fatal("hooks enabled")
		}
		if strings.HasPrefix(c.Path, "outside") {
			t.Fatal("escaped plugin")
		}
	}
	one, _ := json.Marshal(result)
	for i := 0; i < 10; i++ {
		again, err := ParseClaudePlugin(files, "plug")
		if err != nil {
			t.Fatal(err)
		}
		two, _ := json.Marshal(again)
		if string(one) != string(two) {
			t.Fatal("non-deterministic output")
		}
	}
}
func TestClaudeRejectsTraversalAndExecution(t *testing.T) {
	for _, manifest := range []string{`{"name":"x","skills":"../outside"}`, `{"name":"x","agents":"/etc"}`, `{"name":"x","commands":"C:\\tmp"}`, `{"name":"x","mcpServers":{"bad":{"command":"sh","args":["-c","echo bad"]}}}`, `{"name":"x","mcpServers":{"bad":{"command":"npx","args":["x@latest"]}}}`, `{"name":"x","mcpServers":{"bad":{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer plaintext"}}}}`} {
		files := map[string][]byte{".claude-plugin/plugin.json": []byte(manifest)}
		if _, err := ParseClaudePlugin(files, ""); err == nil {
			t.Fatalf("accepted %s", manifest)
		}
	}
}
func TestMarketplace(t *testing.T) {
	result, err := ParseMarketplace([]byte(`{"plugins":[{"name":"local","source":"./plugins/local"}]}`))
	if err != nil || len(result) != 1 || result[0].Source != "plugins/local" {
		t.Fatalf("bad marketplace %+v %v", result, err)
	}
	for _, source := range []string{`"../bad"`, `{"source":"github","repo":"org/repo","ref":"--upload-pack=evil"}`} {
		if _, err := ParseMarketplace([]byte(`{"plugins":[{"name":"x","source":` + source + `}]}`)); err == nil {
			t.Fatal("unsafe source accepted")
		}
	}
}
