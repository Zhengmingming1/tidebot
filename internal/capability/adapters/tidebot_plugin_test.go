package adapters

import (
	"strings"
	"testing"
)

func TestTidebotPluginAdapterInert(t *testing.T) {
	raw := `id: sample
name: Sample
version: 1.2.3
runtime:
  command: bin/plugin
contributes:
  connectors:
    - id: remote
      side_effects: true
      mcp:
        command: bin/mcp
        env:
          TOKEN: ${secret:token}
  triggers:
    - id: incoming
      kind: webhook
permissions:
  network: [example.com]
  secrets: [token]
`
	out, err := ParseTidebotPlugin(map[string][]byte{"plugin.yaml": []byte(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Components) != 3 || !out.Permissions.SideEffects || len(out.Requires.Secrets) != 0 {
		t.Fatalf("unexpected metadata %+v", out)
	}
	for _, c := range out.Components {
		if c.Type != "tidebot-plugin" || !c.Disabled || c.MCP != nil {
			t.Fatalf("executable component %+v", c)
		}
	}
	if len(out.Findings) != 1 || !strings.Contains(out.Findings[0].Note, "does not authorize") {
		t.Fatal("missing approval boundary")
	}
	for _, bad := range []string{strings.Replace(raw, "bin/plugin", "../escape", 1), raw + "\n---\nid: hidden\n", raw + "\nunknown: true\n"} {
		if _, err = ParseTidebotPlugin(map[string][]byte{"plugin.yaml": []byte(bad)}); err == nil {
			t.Fatal("unsafe manifest accepted")
		}
	}
}
