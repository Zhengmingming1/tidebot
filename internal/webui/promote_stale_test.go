package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// skills.promote answers 409 proposal_stale when the skill's current version
// is no longer the one the owner confirmed, so the page can refresh and ask again.
func TestPromoteStaleRPCCode(t *testing.T) {
	c := login(t)
	c.s.deps.Ext = func(_ context.Context, method string, raw json.RawMessage) (any, error) {
		if method != "skills.promote" {
			t.Fatalf("method %s", method)
		}
		var r struct {
			ProposalID    string `json:"proposal_id"`
			ExpectCurrent string `json:"expect_current"`
		}
		if err := json.Unmarshal(raw, &r); err != nil || r.ProposalID != "prop_1" || r.ExpectCurrent != "ver_3" {
			t.Fatalf("params %s", raw)
		}
		return nil, ErrProposalStale
	}
	code, out := c.json(http.MethodPost, "/api/rpc/skills.promote", map[string]any{"proposal_id": "prop_1", "expect_current": "ver_3"})
	if code != 409 || out["code"] != "proposal_stale" || out["error"] == nil {
		t.Fatalf("got %d %v", code, out)
	}
}

// The skills page names the current version it would replace and sends it back;
// a proposal_stale answer refreshes the page.
func TestPromoteStaleUI(t *testing.T) {
	js := appJS(t)
	i := strings.Index(js, "function promoteStale(")
	if i < 0 {
		t.Fatal("no promoteStale")
	}
	j := strings.Index(js[i:], "\n  }\n")
	if j < 0 {
		t.Fatal("promoteStale body")
	}
	fn := js[i : i+j+4]
	for _, want := range []string{
		`expect_current: cur`,
		`T("opt.promote_stale"`,
		`e.data.code === "proposal_stale"`,
		`T("opt.promote_stale_err")`,
		`danger: !!over`,
		`icon: "skills"`,
		`data-base="`,
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, lang := range []string{"zh-CN", "en"} {
		b, err := Static.ReadFile("static/i18n/" + lang + ".json")
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{`"opt.promote_stale"`, `"opt.promote_stale_err"`} {
			if !strings.Contains(string(b), k) {
				t.Fatalf("%s: missing %s", lang, k)
			}
		}
	}
	runNode(t, `eval(FN);
var bad = [];
function ok(name, p, current, want) { if (promoteStale(p, current) !== want) bad.push(name); }
ok("same", { base_version_id: "v1", version_id: "v2" }, "v1", false);
ok("already this version", { base_version_id: "v1", version_id: "v2" }, "v2", false);
ok("changed elsewhere", { base_version_id: "v1", version_id: "v2" }, "v3", true);
ok("old proposal", { version_id: "v2" }, "v3", false);
ok("old server", {}, "v3", false);
ok("no proposal", null, "v3", false);
if (bad.length) { console.error(bad.join("\\n")); process.exitCode = 1; }
`, map[string]string{"FN": fn})
}
