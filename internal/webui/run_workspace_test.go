package webui

import (
	"os/exec"
	"testing"
)

// Switching presentation must preserve the mounted stream and reading position. The
// inspector starts closed at the supported small window size, and tabs work by keyboard.
func TestRunWorkspaceViews(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	src := liveSrc(t)
	script := regexpFunc(t, src, "  function esc(v) {") + regexpFunc(t, src, "  function runTaskInspectorHtml(t, items, form) {") + regexpFunc(t, src, "  function bindRunWorkspace(r, live) {") + `
const assert = require("node:assert/strict");
const els = {}, tabs = [], state = {es: {connected: true}}, window = {innerWidth: 1024};
const document = {contains: () => true, activeElement:null};
let focused = null, mode = "preview", apiMode = "none", runWorkspaceResizeOff = null, lastMedia;
window.matchMedia = () => (lastMedia={matches:window.innerWidth>1100,addEventListener(k,fn){this.listener=fn;},removeEventListener(){this.listener=null;}});
function el(id, attrs = {}) {
  const e = {id, attrs, hidden:false, disabled:false, scrollTop:147, handlers:{},
    setAttribute(k,v){this.attrs[k]=v;}, getAttribute(k){return this.attrs[k];},
    addEventListener(k,f){this.handlers[k]=f;}, focus(){focused=this;document.activeElement=this;}, contains(node){return node===this;},
    classList:{toggle(){}}, insertAdjacentHTML(){throw Error("unexpected content replacement");},
    click(){if(!this.disabled && this.handlers.click)this.handlers.click({target:this});}};
  els["#"+id]=e; return e;
}
["run-workspace","rw-layout","rd-inspector","rd-inspect-toggle","rw-answer","rw-log","rd-calc"].forEach(x=>el(x));
els[".rw-tabs"] = el("tabs");
["answer","log","diff"].forEach(x=>tabs.push(el("rw-tab-"+x,{"data-run-view":x})));
const modes = {};
["preview","diff"].forEach(x=>{modes[x]={getAttribute:()=>String(mode===x),click:()=>{mode=x;}};});
function $(s){const m=/data-ans-mode="(.*?)"/.exec(s);return m?modes[m[1]]:els[s]||null;}
function $$(s){return s==="[data-run-view]"?tabs:[];}
function ansMode(){return mode;}
function T(k,v){return k+(v?JSON.stringify(v):"");}
function enc(s){return encodeURIComponent(s);} function ic(){return "";}
function cronHuman(s){return s;} function tzLabel(){return "Asia/Shanghai";} function usd(n){return "$"+n;}
function errorState(s){return "ERROR:"+s;}
function api(path){
  if(apiMode==="none") throw Error("small inspector should load lazily");
  if(path.endsWith("/amendments")) return apiMode==="failed"?Promise.reject(Error("offline")):Promise.resolve(null);
  if(path.endsWith("/form")) return Promise.resolve({form:{budget_usd:0}});
  return Promise.resolve({id:"task-one",version_no:9,cron:"0 9 * * *"});
}
const answer=els["#rw-answer"], log=els["#rw-log"], stream=state.es;
bindRunWorkspace({id:"r1",result:"hello",answer_prev:{}},true);
assert.equal(answer.hidden,true); assert.equal(log.hidden,false);
assert.equal(els["#rd-inspector"].hidden,true);
assert.equal(els["#rd-inspect-toggle"].attrs["aria-expanded"],"false");
tabs[0].click(); assert.equal(answer.hidden,false); assert.equal(log.hidden,true);
tabs[2].click(); assert.equal(mode,"diff"); assert.equal(answer.attrs["aria-labelledby"],"rw-tab-diff");
assert.equal(tabs[2].attrs["aria-selected"],"true");
// The inner answer radio's arrow-key handler redraws and focuses without firing click.
mode="source";
answer.handlers.focusin({target:{closest:()=>({getAttribute:k=>k==="aria-checked"?"true":"source"})}});
assert.equal(tabs[0].attrs["aria-selected"],"true"); assert.equal(answer.attrs["aria-labelledby"],"rw-tab-answer");
mode="diff";
answer.handlers.focusin({target:{closest:()=>({getAttribute:k=>k==="aria-checked"?"true":"diff"})}});
assert.equal(tabs[2].attrs["aria-selected"],"true"); assert.equal(answer.attrs["aria-labelledby"],"rw-tab-diff");
assert.equal(state.es,stream); assert.equal(log.scrollTop,147); assert.equal(answer.scrollTop,147);
els[".rw-tabs"].handlers.keydown({key:"Home",target:tabs[2],preventDefault(){}});
assert.equal(focused,tabs[0]); assert.equal(mode,"preview");
tabs[2].disabled=true;
els[".rw-tabs"].handlers.keydown({key:"End",target:tabs[0],preventDefault(){}});
assert.equal(focused,tabs[1]); assert.equal(log.hidden,false);
els["#rd-inspect-toggle"].click(); assert.equal(els["#rd-inspector"].hidden,false);
els["#rd-inspector"].handlers.keydown({key:"Escape",preventDefault(){}});
assert.equal(els["#rd-inspector"].hidden,true); assert.equal(focused,els["#rd-inspect-toggle"]);
// Crossing window breakpoints updates only the inspector, keeping the stream, panels and reading position.
lastMedia.matches=true; lastMedia.listener(); assert.equal(els["#rd-inspector"].hidden,false);
els["#rd-inspector"].focus(); lastMedia.matches=false; lastMedia.listener();
assert.equal(els["#rd-inspector"].hidden,true); assert.equal(focused,els["#rd-inspect-toggle"]);
assert.equal(state.es,stream); assert.equal(log.scrollTop,147); assert.equal(answer.scrollTop,147);
// The API represents an empty amendment slice as a successful JSON null; a rejected
// request must remain unknown. Exercise the real asynchronous binding, not just HTML.
(async function(){
  const host=el("rw-current-task"); window.innerWidth=1440; apiMode="empty";
  bindRunWorkspace({id:"r2",task_id:"task-one",result:"done"},false);
  await new Promise(setImmediate);
  assert.ok(host.innerHTML.includes("rw.current_version"));
  assert.ok(!host.innerHTML.includes("rw.pending_unknown"));
  assert.ok(!host.innerHTML.includes("rw.pending_n"));
  apiMode="failed";
  bindRunWorkspace({id:"r2",task_id:"task-one",result:"done"},false);
  await new Promise(setImmediate);
  assert.ok(host.innerHTML.includes("rw.pending_unknown"));
})().catch(e=>{console.error(e);process.exitCode=1;});
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("workspace behavior: %v\n%s", err, out)
	}
}

// Current task requirements must retain their scope, escape user input and never turn
// a failed read into "nothing pending". A run's saved version is shown separately.
func TestRunWorkspaceInspector(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	src := liveSrc(t)
	script := regexpFunc(t, src, "  function esc(v) {") + regexpFunc(t, src, "  function runTaskInspectorHtml(t, items, form) {") + `
const assert=require("node:assert/strict");
function T(k,v){return k+(v?JSON.stringify(v):"");}
function enc(s){return encodeURIComponent(s);}
function cronHuman(s){return s;} function tzLabel(){return "Asia/Shanghai";}
function usd(n){return "$"+n;} function ic(){return "";}
const task={id:"task-one",version_no:9,cron:"0 9 * * *"};
const h=runTaskInspectorHtml(task,[{status:"pending",content:"<script>x</script>",scope:"next_run"},{status:"applied",content:"old"},{status:"needs_decision",content:"Conflict",scope:"permanent"}],{budget_usd:2});
assert.ok(h.includes("v9")); assert.ok(h.includes("rw.current_version"));
assert.ok(h.includes("req.next_only")); assert.ok(h.includes("req.conflict"));
assert.ok(h.includes("&lt;script&gt;")); assert.ok(!h.includes("<script>"));
assert.ok(!h.includes(">old<")); assert.ok(h.includes("/thread"));
const failed=runTaskInspectorHtml(task,null,null);
assert.ok(failed.includes("rw.pending_unknown")); assert.ok(!failed.includes("tf.no_cap"));
const empty=runTaskInspectorHtml(task,[],{budget_usd:0});
assert.ok(!empty.includes("rw.pending_n")); assert.ok(empty.includes("tf.no_cap"));
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("inspector: %v\n%s", err, out)
	}
}

// A hidden pane reports zero scroll metrics. SSE output must remain buffered instead
// of treating those zeros as "at the bottom" and overwriting the reader's position.
func TestRunWorkspaceHiddenLog(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	src := liveSrc(t)
	script := regexpFunc(t, src, "  function logView(host, runId, live, onEnd) {") + `
const assert=require("node:assert/strict");
const els={}, panel={hidden:false};
function el(id){
  const classes=new Set();
  const e={id,innerHTML:"",textContent:"",handlers:{},childElementCount:0,firstChild:null,
    addEventListener(k,f){this.handlers[k]=f;},appendChild(){},focus(){},
    insertAdjacentHTML(p,s){this.innerHTML+=s;this.childElementCount++;},
    classList:{add(k){classes.add(k);},remove(k){classes.delete(k);},contains:k=>classes.has(k),toggle(k,on){if(on)classes.add(k);else classes.delete(k);}}};
  if(id)els["#"+id]=e;return e;
}
["log","lv-rows","lv-more","lv-new","lv-pos","lv-q","lv-next","lv-prev","lv-copy","lv-dl","lv-full"].forEach(el);
const head=el(), section=el(), host=el();
host.closest=s=>s==="section"?section:s==="[hidden]"&&panel.hidden?panel:null;
const document={createElement:()=>el(),addEventListener(){},removeEventListener(){},contains:()=>true};
const state={}, LOG_WINDOW=1500;
function $(s){return s===".card-head"?head:els[s]||null;}
function $$(){return [];}
function esc(s){return String(s);} function T(s){return s;} function ic(){return "";}
function enc(s){return s;} function imeInput(){} function stopStream(){}
function classifyLog(text){return {kind:"text",text,ts:""};}
function hlText(s){return s;} function requestAnimationFrame(fn){fn();}
function EventSource(){this.addEventListener=function(){};this.close=function(){};}
let pos=0,writes=0;
Object.defineProperties(els["#log"],{
  scrollTop:{get:()=>panel.hidden?0:pos,set:v=>{pos=v;writes++;}},
  clientHeight:{get:()=>panel.hidden?0:200},scrollHeight:{get:()=>panel.hidden?0:1000}
});
const viewer=logView(host,"run",true), log=els["#log"], rows=els["#lv-rows"];
viewer.push("first"); log.scrollTop=300;
host._logVisibility(false); panel.hidden=true;
const before=writes, html=rows.innerHTML;
viewer.push("while hidden");
assert.equal(writes,before); assert.equal(rows.innerHTML,html);
assert.equal(els["#lv-new"].classList.contains("hidden"),false);
panel.hidden=false;host._logVisibility(true);assert.equal(log.scrollTop,300);
// A user who was following the tail resumes at the latest output when returning.
log.scrollTop=800;host._logVisibility(false);panel.hidden=true;
viewer.push("latest");panel.hidden=false;host._logVisibility(true);
assert.ok(rows.innerHTML.includes("latest"));assert.equal(log.scrollTop,1000);
assert.equal(els["#lv-new"].classList.contains("hidden"),true);
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("hidden log: %v\n%s", err, out)
	}
}
