package webui

import (
	"os/exec"
	"testing"
)

// A second upload supersedes the entire first preview, including its import action.
// Exercise the real dialog handlers with deliberately out-of-order responses.
func TestSkillImportLatestPreview(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := regexpFunc(t, liveSrc(t), "  function esc(v) {") + regexpFunc(t, liveSrc(t), "  function skillImportModal(done) {") + `
const assert=require("node:assert/strict"),fs=require("node:fs");
const dicts={zh:JSON.parse(fs.readFileSync("static/i18n/zh-CN.json","utf8")),en:JSON.parse(fs.readFileSync("static/i18n/en.json","utf8"))};
let lang="zh",ctx,imports=[],done=0,failures=[];
function T(key,p){return (dicts[lang][key]||key).replace(/\{(\w+)\}/g,(_,k)=>p&&p[k]!=null?String(p[k]):"{"+k+"}");}
function ic(){return "<i></i>";}function dropZone(){return "";}
function fieldHtml(label,input){return input;}function fmtBytes(n){return String(n);}
function fileTree(files){return files.map(f=>esc(f.path)).join(" ");}
function toast(){}function fail(e){failures.push(e);}
function El(){this.events={};this.disabled=false;this.html="";}
El.prototype.addEventListener=function(k,fn){this.events[k]=fn;};
El.prototype.focus=function(){};El.prototype.setSelectionRange=function(){};
Object.defineProperty(El.prototype,"innerHTML",{get(){return this.html;},set(html){
  this.html=html;this.buttons={};
  for(const id of ["sk-go","sk-ver"])if(html.includes('id="'+id+'"'))this.buttons[id]=new El();
  this.cancel=html.includes("data-close")?new El():null;
  if(html.includes('id="sk-form"')){
    this.form=new El();this.form.name=new El();this.form.name.value=(html.match(/name="name"[^>]*value="([^"]*)"/)||[])[1]||"";
    this.form.reportValidity=()=>true;
  }else this.form=null;
}});
function $(q,root){
  if(q==="#sk-prev")return ctx.prev;if(q==="#sk-foot")return ctx.foot;
  if(q==="#sk-form")return root.form;
  if(q==="#sk-go"||q==="#sk-ver")return root.buttons[q.slice(1)]||null;
  throw Error("unexpected selector "+q);
}
function $$(q,root){assert.equal(q,"[data-close]");return root.cancel?[root.cancel]:[];}
const document={contains:root=>root.alive};
function modal(html,onMount){
  ctx={root:{alive:true},prev:new El(),foot:new El(),requests:[],importReplies:[],closed:0};
  onMount(ctx.root,()=>{ctx.root.alive=false;ctx.closed++;});
}
function wireDrop(root,id,fn){assert.equal(id,"sk-drop");ctx.choose=fn;}
function upload(path,form){assert.equal(path,"/api/skills/upload");return new Promise((resolve,reject)=>ctx.requests.push({form,resolve,reject}));}
function api(path,opt){
  assert.equal(path,"/api/skills/import");imports.push(opt.body);
  if(ctx.deferImport)return new Promise((resolve,reject)=>ctx.importReplies.push({resolve,reject}));
  return Promise.resolve({id:"skill"});
}
function preview(name,source_root=".",existing){return {stage_id:name,name,description:"description",files:2,size:20,tree:[{path:"SKILL.md"},{path:"deepwiki-api.py"}],checks:[],source_root,existing};}
function uploadError(code,candidates){const e=Error("technical English fallback");e.status=400;e.data={code,candidates};return e;}
const flush=async()=>{await Promise.resolve();await Promise.resolve();await Promise.resolve();};
function open(){skillImportModal(()=>done++);return ctx;}
(async function(){
  // A valid preview must lose its import button as soon as a replacement upload starts.
  let c=open();c.choose("A");c.requests[0].resolve(preview("A"));await flush();
  assert.ok(c.foot.buttons["sk-go"]);
  assert.ok(!c.prev.innerHTML.includes("识别到的技能目录"),"a root skill needs no wrapper notice");
  c.choose("B");assert.ok(c.foot.cancel);assert.ok(!c.foot.buttons["sk-go"]);assert.ok(!c.foot.buttons["sk-ver"]);
  c.requests[1].reject(uploadError("skill_root_missing"));await flush();
  assert.ok(c.prev.innerHTML.includes("未找到 SKILL.md"));assert.ok(c.prev.innerHTML.includes("标准 skills/&lt;名称&gt;/"));
  assert.ok(!c.prev.innerHTML.includes("technical English fallback"));assert.equal(imports.length,0);

  // C finishes after D; only D's path, file preview and stage ID can be imported.
  c.choose("C");c.choose("D");
  c.requests[3].resolve(preview("D","bundle/skills/<chosen>"));await flush();const latest=c.prev.innerHTML;
  assert.ok(latest.includes("bundle/skills/&lt;chosen&gt;"));assert.ok(latest.includes("不会启用插件 hooks"));
  c.requests[2].resolve(preview("C"));await flush();assert.equal(c.prev.innerHTML,latest);
  c.foot.buttons["sk-go"].events.click();await flush();
  assert.deepEqual(imports[0],{stage_id:"D",name:"D",mode:""});assert.equal(done,1);

  // A late error from E cannot replace successful F. Canceling detaches the dialog too.
  c=open();c.choose("E");c.choose("F");c.requests[1].resolve(preview("F"));await flush();
  const good=c.prev.innerHTML;c.requests[0].reject(uploadError("skill_root_missing"));await flush();assert.equal(c.prev.innerHTML,good);
  c.choose("G");const checking=c.prev.innerHTML;c.foot.cancel.events.click();c.requests[2].resolve(preview("G"));await flush();
  assert.equal(c.prev.innerHTML,checking);assert.ok(!c.foot.buttons["sk-go"]);

  // A stale success also cannot revive an import button after the newer upload failed.
  c=open();c.choose("old");c.choose("multi");
  c.requests[1].reject(uploadError("multiple_skills",["skills/a",'<img src=x onerror="bad">',42,"skills/a&b"]));await flush();
  const bad=c.prev.innerHTML;assert.ok(bad.includes("检测到多个技能"));assert.ok(bad.includes("技能子目录"));
  assert.ok(bad.includes("skills/a&amp;b"));assert.ok(bad.includes('&lt;img src=x onerror=&quot;bad&quot;&gt;'));assert.ok(!bad.includes("<img"));
  c.requests[0].resolve(preview("old"));await flush();assert.equal(c.prev.innerHTML,bad);assert.ok(!c.foot.buttons["sk-go"]);

  // Existing-name confirmation still uses the explicit version mode; English errors remain friendly.
  lang="en";c=open();c.choose("version");c.requests[0].resolve(preview("Existing","skills/existing",{version_no:1,next_no:2}));await flush();
  assert.ok(c.prev.innerHTML.includes("Detected skill folder"));assert.ok(c.foot.buttons["sk-ver"]);
  c.foot.buttons["sk-ver"].events.click();await flush();assert.equal(imports[1].mode,"version");
  c=open();c.choose("missing");c.requests[0].reject(uploadError("skill_root_missing"));await flush();assert.ok(c.prev.innerHTML.includes("SKILL.md was not found"));
  c.choose("multi");c.requests[1].reject(uploadError("multiple_skills",["skills/one","skills/two"]));await flush();assert.ok(c.prev.innerHTML.includes("Import one at a time"));

  // An old import mutation may finish after the next upload. Its 409/success must not replace or close the new preview.
  c=open();c.deferImport=true;c.choose("import-A");c.requests[0].resolve(preview("import-A"));await flush();
  c.foot.buttons["sk-go"].events.click();
  c.choose("import-B");c.requests[1].resolve(preview("import-B"));await flush();const beforeConflict=c.prev.innerHTML,doneBefore=done;
  const conflict=Error("exists");conflict.status=409;conflict.data={existing:{version_no:7,next_no:8}};
  c.importReplies[0].reject(conflict);await flush();
  assert.equal(c.prev.innerHTML,beforeConflict);assert.ok(c.foot.buttons["sk-go"]);assert.ok(!c.foot.buttons["sk-ver"]);
  assert.equal(c.root.alive,true);assert.equal(done,doneBefore);
  c.foot.buttons["sk-go"].events.click();
  c.choose("import-C");c.requests[2].resolve(preview("import-C"));await flush();const beforeSuccess=c.prev.innerHTML;
  c.importReplies[1].resolve({id:"import-B"});await flush();
  assert.equal(c.prev.innerHTML,beforeSuccess);assert.equal(c.root.alive,true);assert.equal(c.closed,0);assert.equal(done,doneBefore);

  for(const outcome of ["success","conflict"]){
    c=open();c.deferImport=true;c.choose("closing");c.requests[0].resolve(preview("closing"));await flush();
    c.foot.buttons["sk-go"].events.click();const beforeClose=c.prev.innerHTML,finished=done;
    c.foot.cancel.events.click();
    if(outcome==="success")c.importReplies[0].resolve({id:"closing"});else c.importReplies[0].reject(conflict);
    await flush();assert.equal(c.prev.innerHTML,beforeClose);assert.equal(c.closed,1);assert.equal(done,finished);
  }
  assert.deepEqual(failures,[]);
  console.log("skill import preview checks passed");
})().catch(e=>{console.error(e);process.exitCode=1;});
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("skill import client: %v\n%s", err, out)
	}
}
