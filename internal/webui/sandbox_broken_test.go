package webui

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/guard"
)

// Settings reports the sandbox the app actually probed (bubblewrap refused by
// the kernel shows as "broken" with its message), and the broken finding's
// texts are localized like the "missing" ones.
func TestSettingsSandboxBroken(t *testing.T) {
	c := login(t)
	msg := "bwrap: setting up uid map: Permission denied"
	c.s.deps.Sandbox = func() guard.SandboxStatus {
		return guard.SandboxStatus{Platform: "linux", Status: guard.SandboxBroken, Error: msg}
	}
	_, st := c.json(http.MethodGet, "/api/settings", nil)
	sb, _ := st["sandbox"].(map[string]any)
	if sb == nil || sb["status"] != "broken" || sb["error"] != msg {
		t.Fatalf("settings sandbox %v", st["sandbox"])
	}
	if info := explainDepText(DepSandboxBrokenEvidence(msg)); info == nil || info.Code != "dep_sandbox_broken_ev" || info.Params["error"] != msg {
		t.Fatalf("evidence %+v", info)
	}
	for code, text := range map[string]string{"dep_reason_sandbox_broken": DepReasonSandboxBroken, "dep_suggest_sandbox_userns": DepSuggestSandboxUserns} {
		if info := explainDepText(text); info == nil || info.Code != code {
			t.Fatalf("%s: %+v", code, info)
		}
	}
}

// The Settings pill, the help dialog (in-app, bubblewrap's message escaped), the
// 可执行命令 mark and the Run dialog hint all handle "broken".
func TestSandboxBrokenUI(t *testing.T) {
	js := appJSSource(t)
	for _, want := range []string{
		`sb.status === "broken" ? '<b class="rn-nosb bad" data-tip="' + esc(T("pl.sb_broken_tip"))`,
		`if (sb.status === "broken") return '<button type="button" class="pill warn sm pl-sb" id="pl-sb"`,
		`'<div class="sb-cmd"><code class="mono">' + esc(sb.error) + "</code></div>"`,
		`else if (m === "exec" && sb.status === "broken") { txt = T("rn.hint_exec_broken"); warn = true; }`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app source lacks %s", want)
		}
	}
}
