package webui

import (
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestLiveFollowStableReads: the task list and a task page repaint only when their data differs,
// so reading it twice with nothing changed must give the same bytes (no clock-derived fields),
// and a change (pausing the task) must show in both.
func TestLiveFollowStableReads(t *testing.T) {
	c := login(t)
	_, tk := c.json(http.MethodPost, "/api/tasks", map[string]any{"name": "Watch", "prompt": "p", "provider": "anthropic", "cron": "0 9 * * *", "enabled": true})
	id, _ := tk["id"].(string)
	if id == "" {
		t.Fatalf("create %v", tk)
	}
	paths := []string{"/api/tasks", "/api/tasks/" + id, "/api/tasks/" + id + "/amendments", "/api/tasks/" + id + "/versions", "/api/tasks/" + id + "/runs?limit=6"}
	read := func() []string {
		var out []string
		for _, p := range paths {
			rr := c.do(http.MethodGet, p, "", nil)
			if rr.Code != 200 {
				t.Fatalf("%s %d", p, rr.Code)
			}
			out = append(out, rr.Body.String())
		}
		return out
	}
	a, b := read(), read()
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("%s differs between two reads:\n%s\n%s", paths[i], a[i], b[i])
		}
	}
	if code, out := c.json(http.MethodPost, "/api/tasks/"+id+"/pause", map[string]any{}); code != 200 {
		t.Fatalf("pause %d %v", code, out)
	}
	d := read()
	if d[0] == a[0] || d[1] == a[1] {
		t.Fatal("pausing the task did not change the list or the task")
	}
	if rr := c.do(http.MethodDelete, "/api/tasks/"+id, "", nil); rr.Code/100 != 2 {
		t.Fatalf("delete %d", rr.Code)
	}
	// A task deleted in another window reads back as archived (the open page then says so and
	// goes to the list) and leaves the list.
	if code, out := c.json(http.MethodGet, "/api/tasks/"+id, nil); code != 200 || out["status"] != "archived" {
		t.Fatalf("deleted task %d %v", code, out["status"])
	}
	if rr := c.do(http.MethodGet, "/api/tasks", "", nil); strings.Contains(rr.Body.String(), id) {
		t.Fatal("deleted task still listed")
	}
}

func liveSrc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// TestLiveFollowWiring: both pages follow, the list keeps its poll across filter changes, the
// task page redraws from the fetched data, drops its cache on a 404 and leaves for the
// list when the task was deleted elsewhere.
func TestLiveFollowWiring(t *testing.T) {
	src := liveSrc(t)
	for _, want := range []string{
		`livePoll(function () { return evsLive() || going(tasks) ? check() : null; }, 2000, function (h) { return /^#\/tasks(\?|$)/.test(h); });`,
		`livePoll(function () { return evsLive() || going ? check() : null; }, 2000);`,
		`var here = same || function (h) { return h === poller.hash; };`,
		`if (Array.isArray(d)) d.forEach(function (x, i) { swrPut(paths[i], x); });`,
		`else paths.forEach(function (k) { delete swr.cache[k]; });`,
		`keepFocus(body, rows);`,
		`if (Array.isArray(d) && d[0] && d[0].status === "archived" && t.status !== "archived") { stopPoll(); toast(T("td.gone")); location.hash = "#/tasks"; return; }`,
		`if (keep && $("#ov-next") && keep.k ===`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lost %q", want)
		}
	}
	// No native dialogs crept in.
	if regexp.MustCompile(`\b(window\.)?(confirm|alert|prompt)\(`).MatchString(regexp.MustCompile(`(?s)  function liveQuiet.*?  document.addEventListener\("visibilitychange"`).FindString(src)) {
		t.Error("native dialog in the follow helpers")
	}
}

// TestLiveFollowClient runs the helpers under node with a tiny DOM.
func TestLiveFollowClient(t *testing.T) {
	src := liveSrc(t)
	grab := func(re string) string {
		s := regexp.MustCompile(re).FindString(src)
		if s == "" {
			t.Fatalf("not found: %s", re)
		}
		return s
	}
	helpers := grab(`(?s)  function revGate\(\) \{.*?\n  \}\n`) +
		grab(`(?s)  function liveQuiet\(box\) \{.*?\n  \}\n`) +
		grab(`(?s)  function keepFocus\(box, paint\) \{.*?\n  \}\n`) +
		grab(`(?s)  function liveFollow\(load, shown, apply, quiet\) \{.*?\n  \}\n`)
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; static checks only")
	}
	script := `
function El(tag, attrs){ this.tagName=tag; this.attrs=attrs||{}; this.kids=[]; this.parent=null; this.isContentEditable=false; }
El.prototype.add=function(k){ k.parent=this; this.kids.push(k); return k; };
El.prototype.contains=function(n){ for(var x=n;x;x=x.parent) if(x===this) return true; return false; };
El.prototype.getAttribute=function(n){ return n in this.attrs ? this.attrs[n] : null; };
El.prototype.all=function(){ var o=[]; this.kids.forEach(function(k){ o.push(k); o=o.concat(k.all()); }); return o; };
El.prototype.focus=function(){ document.activeElement=this; };
var root=new El("BODY"), modalRoot=root.add(new El("DIV",{id:"modal-root"})); modalRoot.innerHTML="";
var page=root.add(new El("DIV")), sel=null;
var document={ activeElement:root, contains:function(n){ return root.contains(n); } };
var window={ getSelection:function(){ return sel; } };
function $(q, box){ if(q==="#modal-root") return modalRoot; if(q==="#drawer") return extra.drawer||null; if(q===".popover[role=menu]") return extra.menu||null; if(q===".editing") return (box||root).all().filter(function(e){return e.attrs["class"]==="editing";})[0]||null; return null; }
function $$(q, box){ var m=/^\[([\w-]+)\]$/.exec(q); return (box||root).all().filter(function(e){ return m && m[1] in e.attrs; }); }
var extra={}, rev="1.1", state={}, net={down:false};
function evsLive(){ return true; }
var evs={st:{rev:rev}};
function apiRaw(){ throw new Error("rev comes from the stream here"); }
` + helpers + `
var bad=[], data={v:1}, loads=0, applied=[], quiet=true;
function load(){ loads++; return Promise.resolve(JSON.parse(JSON.stringify(data))); }
var check=liveFollow(load, {v:1}, function(d){ applied.push(d.v); }, function(){ return quiet; });
(async function(){
  await check(); if(loads!==1||applied.length) bad.push("same data must not repaint: "+loads+" "+applied);
  await check(); if(loads!==1) bad.push("unchanged rev refetched: "+loads);
  evs.st.rev="1.2"; await check(); if(loads!==2||applied.length) bad.push("rev moved, data same: "+loads+" "+applied);
  data={v:2}; evs.st.rev="1.3"; quiet=false; await check(); if(loads!==3||applied.length) bad.push("busy user must hold: "+applied);
  await check(); await check(); if(loads!==3) bad.push("held data refetched: "+loads);
  quiet=true; await check(); if(applied.join()!=="2"||loads!==3) bad.push("held data not painted once quiet: "+applied+" "+loads);
  await check(); if(applied.length!==1||loads!==3) bad.push("painted twice: "+applied+" "+loads);
  data={v:3}; evs.st.rev="1.4"; await check(); if(applied.join()!=="2,3") bad.push("next change: "+applied);
  var fails=0; var c2=liveFollow(function(){ fails++; return Promise.reject(new Error("net")); }, {}, function(){ bad.push("applied on error"); }, function(){ return true; });
  try{ await c2(); }catch(e){} try{ await c2(); }catch(e){} if(fails!==2) bad.push("failed load not retried: "+fails);

  // liveQuiet
  var inp=page.add(new El("INPUT")), row=page.add(new El("TR",{"data-href":"#/tasks/a"}));
  if(!liveQuiet(page)) bad.push("idle page not quiet");
  inp.focus(); if(liveQuiet(page)) bad.push("typing counts as quiet");
  row.focus(); if(!liveQuiet(page)) bad.push("focused row is not typing");
  modalRoot.innerHTML="<div>"; if(liveQuiet(page)) bad.push("open dialog"); modalRoot.innerHTML="";
  extra.menu={}; if(liveQuiet(page)) bad.push("open menu"); extra.menu=null;
  extra.drawer={}; if(liveQuiet(page)) bad.push("open drawer"); extra.drawer=null;
  sel={isCollapsed:false, anchorNode:row}; if(liveQuiet(page)) bad.push("selected text"); sel={isCollapsed:true};
  var ed=page.add(new El("H1",{"class":"editing"})); if(liveQuiet(page)) bad.push("inline edit"); page.kids.pop();
  if(liveQuiet(new El("DIV"))) bad.push("detached box");

  // keepFocus: the same row control gets the focus back after a repaint
  var body=page.add(new El("TBODY")), ck=body.add(new El("INPUT",{"data-pick":"t2"}));
  ck.focus();
  keepFocus(body, function(){ body.kids=[]; body.add(new El("INPUT",{"data-pick":"t1"})); body.add(new El("INPUT",{"data-pick":"t2"})); });
  if(document.activeElement===ck||document.activeElement.getAttribute("data-pick")!=="t2"||document.activeElement.parent!==body) bad.push("focus not restored");
  var r2=body.kids[0]; r2.focus(); keepFocus(body, function(){ body.kids=[]; });
  if(document.activeElement!==r2) bad.push("gone row: focus should stay put, not jump");
  if(bad.length){ console.error(bad.join("\n")); process.exitCode=1; }
})();
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("live follow: %v\n%s", err, out)
	}
}
