package workflow

import (
	"strings"
	"testing"
)

func TestContextSelectionDeduplicatesReferencesWithoutExpanding(t *testing.T) {
	n := skill("b", Dependency{NodeID: "a"})
	n.Prompt = strings.Repeat("{{steps.a.output}}", 1000)
	n.Context = &ContextPolicy{Mode: "summary"}
	states := map[string]*NodeRun{"a": {NodeID: "a", Status: "succeeded", Output: strings.Repeat("x", MaxOutputBytes)}}
	prepared, prompt, e := prepareContext(n, "run-one", "", states)
	if e != nil || len(prepared.ContextArtifacts) != 1 || len(prompt) > MaxPromptBytes || prepared.ContextArtifacts[0].Ref != "workflow:run-one/node:a" {
		t.Fatal(prepared, e)
	}
	states["a"].Output = "changed"
	if prepared.ContextArtifacts[0].Body == "changed" {
		t.Fatal("mutable state leaked")
	}
}

func TestContextPolicyRejectsUnrelatedAndInvalidInputs(t *testing.T) {
	for _, p := range []*ContextPolicy{{Sources: []ContextSource{{NodeID: "other"}}}, {Mode: "model-summary"}, {MaxTokens: 1}, {Sources: []ContextSource{{NodeID: "a"}, {NodeID: "a"}}}} {
		n := skill("b", Dependency{NodeID: "a"})
		n.Context = p
		d := Definition{Name: "x", Nodes: []Node{skill("a"), n, skill("other")}}
		if e := Validate(&d); e == nil {
			t.Fatal("invalid context accepted", p)
		}
	}
}

func TestContextAnyJoinMarksMissingBranch(t *testing.T) {
	n := skill("b")
	n.Join = "any"
	n.Prompt = "{{steps.a.output}}"
	n.Context = &ContextPolicy{}
	prepared, prompt, e := prepareContext(n, "run", "", map[string]*NodeRun{"a": {Status: "skipped"}})
	if e != nil || len(prepared.ContextArtifacts) != 0 || !strings.Contains(prompt, "branch not selected") {
		t.Fatal(prepared, prompt, e)
	}
}

func BenchmarkContextRepeatedOutput(b *testing.B) {
	states := map[string]*NodeRun{"a": {NodeID: "a", Status: "succeeded", Output: strings.Repeat("x", 64<<10)}}
	n := Node{ID: "b", Prompt: strings.Repeat("{{steps.a.output}}\n", 3), Join: "all", Context: &ContextPolicy{Mode: "full"}}
	b.Run("inline", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := render(n.Prompt, "", states, "all"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("selected_once", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, _, err := prepareContext(n, "run", "", states); err != nil {
				b.Fatal(err)
			}
		}
	})
}
