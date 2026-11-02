package webui

import (
	"os/exec"
	"testing"
)

// The home form shares the existing quick-run draft, but owns its submission lock.
// Run its real handlers with a small DOM and controllable network replies.
func TestHomeWorkspaceClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := `
const assert = require("node:assert/strict");
const src = require("node:fs").readFileSync("static/app.js", "utf8");
function grab(name) {
  const m=src.match(new RegExp("  function " + name + "\\([^]*?\\n  \\}"));
  assert.ok(m, "missing helper " + name); return m[0];
}
function esc(v) { return String(v == null ? "" : v).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c])); }
function enc(v) { return encodeURIComponent(v); }
function ic() { return "<i></i>"; }
function shortId(v) { return v.slice(0,8); }
function T(k, d) { return k === "nc.pending_amend" ? d.name + " : " + d.n : k; }
function keyLabel() { return "Cmd+Enter"; }
function draftTagHtml() { return ""; }
let ctx;
const document = {contains:node => node.alive !== false};
function El() { this.events={}; this.value=""; this.disabled=false; this.children=[]; this.options=[]; this.textContent=""; this.alive=true; }
El.prototype.addEventListener=function(type, fn) { this.events[type]=fn; };
El.prototype.dispatchEvent=function(e) { if(this.events[e.type]) this.events[e.type](e); };
El.prototype.focus=function() { this.dispatchEvent({type:"focus"}); };
Object.defineProperty(El.prototype, "innerHTML", {
  get:function(){return this.html||"";},
  set:function(value){
    this.html=value;
    if(this.isSelect) {
      this.options=Array.from(value.matchAll(/<option value="([^"]+)"( selected)?>/g), m=>({value:m[1],selected:!!m[2]}));
      this.value=(this.options.find(o=>o.selected)||this.options[0]||{}).value||"";
    }
  }
});
function $(q) { assert.ok(ctx.nodes[q], "unexpected selector " + q); return ctx.nodes[q]; }
function draftSave(k, text) { ctx.drafts[k]={text:text}; }
function draftLoad(k) { return ctx.drafts[k]||null; }
function draftClear(k) { delete ctx.drafts[k]; ctx.cleared++; }
function draftFlush() { if(ctx.pendingDraft) { draftSave("qr",ctx.pendingDraft);ctx.pendingDraft=null; } }
// The production helper ignores synthetic input events. Preserve that rule here.
function wireDraft(root, ta, kind) {
  if(draftLoad(kind)) ta.value=draftLoad(kind).text;
  ta.addEventListener("input", e=>{if(e.isTrusted) draftSave(kind,ta.value);});
}
const localStorage={getItem:k=>ctx.store[k]||null, setItem:(k,v)=>{ctx.store[k]=v;}};
function submitKey(e) { return e.key==="Enter" && e.metaKey; }
function quickRunModal() { ctx.modalCalls.push({prompt:draftLoad("qr").text,provider:ctx.store.tb_qr_provider}); ctx.modal.children=[{}]; }
function openStarted(r, key) { ctx.started.push({r:r,key:key}); }
function api(path, opt) {
  if(path==="/api/settings") return Promise.resolve({providers:[{name:"alpha",key_set:true},{name:"beta",key_set:true}],default_provider:"alpha"});
  if(path==="/api/runs/recent-prompts") return Promise.resolve({prompts:["Selected recent prompt"]});
  if(path==="/api/overview" || path==="/api/dashboard") return new Promise(()=>{});
  assert.equal(path,"/api/runs/quick");
  assert.equal(opt.method,"POST");
  ctx.posts.push(opt.body); return new Promise((resolve,reject)=>{ctx.reply={resolve:resolve,reject:reject};});
}
eval(grab("homePendingHtml") + "\n" + grab("homeQuickHtml") + "\n" + grab("syncHomeQuickDraft") + "\n" + grab("bindHomeQuick") + "\n" + grab("renderHome"));
const flush=async()=>{await Promise.resolve();await Promise.resolve();await Promise.resolve();};
async function setup(text) {
  ctx={nodes:{},drafts:{qr:{text:text}},store:{},posts:[],modalCalls:[],started:[],cleared:0};
  for(const q of ["#home-quick","#home-prompt","#home-submit","#home-advanced","#home-run-error","#home-recent-prompts","#modal-root"]) ctx.nodes[q]=new El();
  ctx.form=ctx.nodes["#home-quick"]; ctx.ta=ctx.nodes["#home-prompt"]; ctx.submit=ctx.nodes["#home-submit"]; ctx.advanced=ctx.nodes["#home-advanced"]; ctx.modal=ctx.nodes["#modal-root"];
  ctx.form.provider=new El(); ctx.form.provider.isSelect=true; ctx.form.provider.disabled=true; ctx.submit.disabled=true;
  ctx.form.requestSubmit=()=>ctx.form.events.submit({preventDefault:()=>{}});
  bindHomeQuick(); await flush(); return ctx;
}
function userText(text) { ctx.ta.value=text; ctx.ta.dispatchEvent({type:"input",isTrusted:true}); }
(async function() {
  // Duplicate submits are locked; failure keeps the prompt and permits one retry.
  let c=await setup("existing draft"); userText("Run this once"); c.form.provider.value="beta";
  c.ta.events.keydown({key:"Enter",metaKey:true,preventDefault:()=>{}});
  c.ta.events.keydown({key:"Enter",metaKey:true,preventDefault:()=>{}});
  c.form.requestSubmit();
  assert.equal(c.posts.length,1,"a double submit must not create two runs");
  assert.deepEqual(c.posts[0],{prompt:"Run this once",provider:"beta"});
  assert.equal(c.submit.disabled,true); assert.equal(c.advanced.disabled,true);
  c.reply.reject(new Error("offline")); await flush();
  assert.equal(c.submit.disabled,false); assert.equal(c.advanced.disabled,false);
  assert.equal(draftLoad("qr").text,"Run this once"); assert.equal(c.ta.value,"Run this once");
  assert.equal(c.nodes["#home-run-error"].textContent,"offline"); assert.equal(c.cleared,0);
  c.form.requestSubmit(); assert.equal(c.posts.length,2);
  c.reply.resolve({id:"r1"}); await flush();
  assert.equal(draftLoad("qr"),null); assert.equal(c.cleared,1); assert.equal(c.started.length,1);

  // Opening advanced options and returning from them preserves prompt and provider both ways.
  c=await setup("old draft"); userText("Prompt for advanced"); c.form.provider.value="beta";
  c.advanced.focus(); c.advanced.events.click();
  assert.deepEqual(c.modalCalls,[{prompt:"Prompt for advanced",provider:"beta"}]);
  draftSave("qr","Changed in advanced"); localStorage.setItem("tb_qr_provider","alpha"); c.modal.children=[];
  syncHomeQuickDraft();
  assert.equal(c.ta.value,"Changed in advanced"); assert.equal(c.form.provider.value,"alpha");

  // The keyboard/palette call quickRunModal directly, without ever focusing advanced.
  // Closing either entry uses the same callback, including an intentionally cleared draft.
  c=await setup("Homepage prompt");
  quickRunModal(); draftSave("qr","Edited from command palette"); localStorage.setItem("tb_qr_provider","beta"); c.modal.children=[];
  syncHomeQuickDraft();
  assert.equal(c.ta.value,"Edited from command palette"); assert.equal(c.form.provider.value,"beta");
  quickRunModal(); draftClear("qr"); c.modal.children=[]; syncHomeQuickDraft();
  assert.equal(c.ta.value,"", "a cleared modal draft also clears the home composer");
  assert.ok(grab("quickRunModal").includes("}, syncHomeQuickDraft)"),"all quick-run entries must install the close callback");

  c=await setup("older draft"); c.pendingDraft="Input still inside the debounce window";
  renderHome();
  assert.equal(draftLoad("qr").text,"Input still inside the debounce window","soft refresh flushes before replacing home input");

  // A recent-prompt chip fires a synthetic input. Focusing advanced must not restore an older draft over it.
  c=await setup("old draft");
  c.nodes["#home-recent-prompts"].events.click({target:{closest:()=>({dataset:{homePrompt:"0"}})}});
  assert.equal(c.ta.value,"Selected recent prompt");
  c.advanced.focus(); c.advanced.events.click();
  assert.equal(c.modalCalls[0].prompt,"Selected recent prompt");

  // Unknown notification data is visibly different from a verified empty pending list; user text is escaped.
  assert.ok(homePendingHtml(null).includes("home.pending_unavailable"));
  assert.equal(homePendingHtml({pending:[]}),"");
  const pending=homePendingHtml({pending:[{task_id:"t 1/#",task_name:'<img src=x onerror="bad">',count:2}]});
  assert.ok(pending.includes('#/tasks/t%201%2F%23/thread'));
  assert.ok(pending.includes('&lt;img src=x onerror=&quot;bad&quot;&gt;'));
  assert.ok(!pending.includes('<img'));
  assert.equal((homePendingHtml({pending:Array.from({length:6},(_,i)=>({task_id:"t"+i,count:1}))}).match(/class="home-pending"/g)||[]).length,4);
  const html=homeQuickHtml();
  assert.ok(html.includes('id="home-prompt"') && html.includes('maxlength="8000" required'));
  assert.ok(html.includes('id="home-run-error" role="alert"'));
  console.log("home workspace behavior checks passed");
})().catch(e=>{console.error(e);process.exitCode=1;});
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("home workspace client: %v\n%s", err, out)
	}
}
