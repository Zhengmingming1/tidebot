package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type fakeModelsAdmin struct {
	ProviderAdmin
	name    string
	refresh bool
}

func (f *fakeModelsAdmin) ListModels(_ context.Context, name string, refresh bool) (map[string]any, error) {
	f.name, f.refresh = name, refresh
	return map[string]any{"ok": true, "models": []map[string]string{{"id": "m-1"}}, "host": "gw.example", "path": "/v1/models"}, nil
}

// POST /api/providers/{name}/models goes to the admin with the decoded name and refresh flag,
// and like every write it needs the CSRF header.
func TestProviderModelsRoute(t *testing.T) {
	c := login(t)
	fa := &fakeModelsAdmin{}
	c.s.deps.Providers = fa
	code, out := c.json(http.MethodPost, "/api/providers/my%2Egw/models", map[string]any{"refresh": true})
	if code != 200 || out["ok"] != true || fa.name != "my.gw" || !fa.refresh {
		t.Fatalf("%d %v %+v", code, out, fa)
	}
	if code, _ := c.json(http.MethodGet, "/api/providers/my.gw/models", nil); code != 404 && code != 405 {
		t.Fatalf("GET answered %d", code)
	}
	c.csrf = "wrong"
	if code, _ := c.json(http.MethodPost, "/api/providers/my.gw/models", map[string]any{}); code != 403 {
		t.Fatalf("no CSRF answered %d", code)
	}
}

func TestProviderModelPickerUI(t *testing.T) {
	js, _ := Static.ReadFile("static/app.js")
	src := strings.ReplaceAll(string(js), "\r\n", "\n")
	for _, s := range []string{
		`if (!creating) wireModelPicker(host, f, v, models);`,
		`api("/api/providers/" + enc(v.name) + "/models", { method: "POST", body: { refresh: !!refresh } })`,
		`!$("#modal-root").innerHTML && !$(".mdl-pop", bg)`,
		`else if (e.key === "Escape" && pop) { e.preventDefault(); e.stopPropagation(); hide(); }`,
		`input.dispatchEvent(new Event("input", { bubbles: true }));`,
		`pop.addEventListener("mousedown", function (e) { e.preventDefault(); });`,
		`return i < 0 ? esc(id) : esc(id.slice(0, i)) + "<b>" + esc(id.slice(i, i + q.length)) + "</b>" + esc(id.slice(i + q.length));`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("app.js lacks %s", s)
		}
	}
	body := src[strings.Index(src, "function wireModelPicker("):]
	body = body[:strings.Index(body, "// ---------- provider pricing")]
	for _, bad := range []string{"confirm(", "alert(", "prompt(", "innerHTML = m.id", "+ m.id +"} {
		if strings.Contains(body, bad) {
			t.Errorf("model picker uses %s", bad)
		}
	}
	css, _ := Static.ReadFile("static/styles.css")
	for _, s := range []string{".mdl-pop {", ".mdl-pop.up {", "max-height: 208px;", "overscroll-behavior: contain;", ".model-row .in-group { position: relative; }"} {
		if !strings.Contains(string(css), s) {
			t.Errorf("styles.css lacks %s", s)
		}
	}
	for _, f := range []string{"zh-CN", "en"} {
		b, _ := Static.ReadFile("static/i18n/" + f + ".json")
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"pv.fetch_models", "pv.fetching", "pv.models_n", "pv.models_tip", "pv.models_none", "pv.models_unsupported", "pv.models_failed", "pv.models_nokey", "pv.models_unlisted", "pv.models_empty"} {
			if m[k] == "" {
				t.Errorf("%s lacks %s", f, k)
			}
		}
	}
}
