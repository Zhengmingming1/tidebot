package webui

import (
	"strings"
	"testing"
)

// TestRouteErrorNotCoveredBySkeleton: a page that fails fast (404 for a deleted run, task or
// workflow) shows the error with a retry. The 200 ms skeleton timer used to fire after the
// refusal and leave the page on a skeleton with aria-busy for good; a failure from a route the
// user already left must not paint over the page they went to.
func TestRouteErrorNotCoveredBySkeleton(t *testing.T) {
	js := appJS(t)
	i := strings.Index(js, "\n  function route(soft) {")
	if i < 0 {
		t.Fatal("route() not found")
	}
	body := js[i : i+strings.Index(js[i:], "\n  }\n")]
	if !strings.Contains(body, "var rs = ++routeSeq;") || !strings.Contains(body, "routeFailed(e, rs);") {
		t.Fatal("route() must number itself and hand failures to routeFailed")
	}
	if strings.Contains(body, "errorState(") {
		t.Error("route() paints errors itself again; use routeFailed")
	}
	if strings.Index(body, "++routeSeq") > strings.Index(body, "switch (parts[0])") {
		t.Error("routeSeq must be taken before the page renderers run")
	}
	start := strings.Index(js, "\n  function routeFailed(e, rs) {")
	if start < 0 {
		t.Fatal("routeFailed() not found")
	}
	end := start + strings.Index(js[start:], "\n  }\n") + 4
	runNode(t, `
var listeners = 0, pg = { attrs: { "aria-busy": "true" }, innerHTML: "",
  removeAttribute: function (k) { delete this.attrs[k]; }, setAttribute: function (k, v) { this.attrs[k] = v; } };
function page() { return pg; }
var activeNav = "workflows";
function $(sel) { if (sel === "#nav a.active") return activeNav ? { getAttribute: function () { return activeNav; } } : null; return { addEventListener: function () { listeners++; } }; }
function T(k) { return "{" + k + "}"; }
function esc(s) { return s; }
function errorState(m) { return "<err>" + m + "</err>"; }
function renderTopbar() {}
var mountSeq = 0, routeSeq = 0, lastHash = "#/x";
function route() {}
eval(SECTION);
var bad = [];
// fast 404: the skeleton timer armed with the old mountSeq must not fire afterwards
var seq = mountSeq, rs = ++routeSeq;
routeFailed(Object.assign(new Error("workflow run not found"), { status: 404 }), rs);
if (pg.innerHTML !== "<err>{err.not_found}</err>") bad.push("404 text: " + pg.innerHTML);
if (pg.attrs["aria-busy"]) bad.push("aria-busy left on");
if (mountSeq === seq) bad.push("skeleton timer not cancelled");
if (listeners !== 1) bad.push("retry not wired");
if (renderTopbar.nav !== "workflows") bad.push("top bar names " + renderTopbar.nav);
activeNav = ""; renderTopbar.nav = "home";
// other errors keep the server's message
routeFailed(Object.assign(new Error("boom"), { status: 500 }), rs);
if (pg.innerHTML !== "<err>boom</err>") bad.push("500 text: " + pg.innerHTML);
// stale: the user moved on before the old route failed
pg.innerHTML = "new page"; var old = rs; ++routeSeq;
routeFailed(new Error("late"), old);
if (pg.innerHTML !== "new page") bad.push("stale failure painted over the new page");
// sign-in errors are handled by the login screen
routeFailed(Object.assign(new Error("sign in"), { auth: true }), routeSeq);
if (pg.innerHTML !== "new page") bad.push("auth error painted");
if (bad.length) { console.error(bad.join("\n")); process.exitCode = 1; }
`, map[string]string{"SECTION": js[start:end]})
}
