package webui

import (
	"os"
	"strings"
	"testing"
)

// P5: 技能 → 会话 is the main line; the optimizer sits folded under 进阶 (open only when
// in use); Git repositories have one home under 扩展 › 来源; the home composer can hand
// its text to a new session.
func TestConvergedEntriesUI(t *testing.T) {
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		`function startSkillChat(skillId, btn) {`,
		`"/bindings", { method: "PUT", body: { skills: [skillId], capabilities: [] } }`,
		`data-sk-chat="`,
		`$("#sk-chat").onclick=function(){startSkillChat(id,this);};`,
		`<details id="sk-adv"' + (oset.mode !== "off" || props.some(`,
		`case "skill-repositories": location.replace(repositoryHref(parts[1], true)); p = null; break;`,
		`draftSave("cs_new", ta.value); draftClear("qr");`,
		`if (p.review && !p.existing && !importHigh(p.review) && SKILL_NAME_RE.test(p.name || ""))`,
		`confirm_risk:!!row.riskOk`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	if strings.Contains(src, `["skill-repositories", "sr.title"]`) || strings.Contains(src, `skillTabs('workflows')`) {
		t.Error("duplicate skill tabs are back")
	}
}
