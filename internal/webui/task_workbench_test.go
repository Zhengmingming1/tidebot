package webui

import (
	"os/exec"
	"testing"
)

// Exercise navigation without remounting the detail pane: filters, responsive collapse,
// focus, escaping and late responses must not disturb an in-progress requirement review.
func TestTaskWorkbenchNavigation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := `
const assert = require("node:assert/strict");
const fs = require("node:fs");
const src = fs.readFileSync("static/app.js", "utf8");
function grab(name) {
  const match = src.match(new RegExp("  function " + name + "\\([^]*?\\n  \\}"));
  assert.ok(match, "missing helper " + name);
  return match[0];
}
function esc(v) { return String(v == null ? "" : v).replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c])); }
function enc(v) { return encodeURIComponent(v); }
function T(k) { return k; }
function ic(k) { return "<i>" + k + "</i>"; }
function opt(v, label, selected) { return '<option value="' + esc(v) + '"' + (selected ? ' selected' : '') + '>' + esc(label) + '</option>'; }
function taskMatchesStatus(t, status) { return !status || (status === "failing" ? !!t.failing : t.status === status); }
function taskPill(status) { return "<small>" + esc(status) + "</small>"; }
function runIcon() { return "tasks"; }
function cronHuman(c) { return c || "manual"; }
function skeleton() { return "loading"; }
function imeInput(el, ms, fn) { el.search = fn; }
function taskFormModal() {}
const TASK_FILTERS = [["", "all"], ["active", "active"], ["paused", "paused"]];
var taskRailView = { q: "", status: "", open: null, off: null };
function El() {
  this.attrs = {}; this.events = {}; this.innerHTML = ""; this.alive = true;
  this.classList = { toggle: (k, v) => { this.attrs[k] = v; } };
}
El.prototype.setAttribute = function(k, v) { this.attrs[k] = v; };
El.prototype.getAttribute = function(k) { return this.attrs[k]; };
El.prototype.addEventListener = function(k, fn) { this.events[k] = fn; };
El.prototype.focus = function() { document.activeElement = this; };
El.prototype.contains = function(node) { return this === node || (this.links || []).includes(node); };
Object.defineProperty(El.prototype, "innerHTML", {
  get:function(){return this.html || "";},
  set:function(html){
    this.html=html;
    this.links=Array.from(html.matchAll(/<a[^>]*data-href="([^"]+)"/g), m=>{const a=new El();a.attrs["data-href"]=m[1];return a;});
  }
});
const box = new El(), rail = new El(), toggle = new El(), search = new El(), list = new El(), count = new El(), filter = new El(), add = new El();
const nodes = {".task-workspace":box, "#task-rail":rail, "#task-rail-toggle":toggle, "#task-rail-search":search, "#task-rail-list":list, "#task-rail-count":count, "#task-rail-filter":filter, "#task-rail-new":add};
function $(q) { assert.ok(nodes[q], "unexpected DOM access " + q); return nodes[q]; }
function $$(q, root) { assert.equal(q,"[data-href]");return root.links || []; }
const document = { activeElement: null, contains: node => node.alive };
rail.contains = function(node) { return node === search; };
const media = { matches:false, addEventListener:(k, fn) => { media.listener=fn; }, removeEventListener:(k, fn) => { if(media.listener===fn) media.listener=null; } };
const window = { matchMedia:() => media };
const tasks = [
  {id:"t 1",name:"Report <script>",status:"active",cron:"daily",tags:["工作"],prompt:"Review repo"},
  {id:"t2",name:"Backup",status:"paused",cron:"weekly",tags:["ops"],prompt:"Archive",failing:true},
  {id:"gone",name:"Archived",status:"archived",tags:[]}
];
let requests=0, resolve;
function api(path) { assert.equal(path, "/api/tasks"); requests++; return new Promise(r => { resolve=r; }); }
const apiRaw=api;
eval(grab("keepFocus") + "\n" + grab("taskRailTasks") + "\n" + grab("taskWorkspaceHtml") + "\n" + grab("wireTaskWorkspace"));
(async function() {
  assert.deepEqual(taskRailTasks(tasks, "工作", "active").map(x=>x.id), ["t 1"]);
  assert.deepEqual(taskRailTasks(tasks, "REVIEW", "").map(x=>x.id), ["t 1"]);
  assert.deepEqual(taskRailTasks(tasks, "", "failing").map(x=>x.id), ["t2"]);
  assert.deepEqual(taskRailTasks(tasks, "", "").map(x=>x.id), ["t 1", "t2"]);
  const refreshRail=wireTaskWorkspace("t 1", "thread");
  assert.equal(requests, 0, "collapsed rail must not delay detail with a request");
  assert.equal(rail.hidden, true);
  toggle.events.click();
  assert.equal(requests, 1);
  assert.equal(document.activeElement, search);
  resolve(tasks); await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
  assert.ok(list.innerHTML.includes('#/tasks/t%201/thread'));
  assert.ok(list.innerHTML.includes('aria-current="page"'));
  assert.ok(list.innerHTML.includes('Report &lt;script&gt;'));
  assert.ok(!list.innerHTML.includes('<script>'));
  filter.events.change({target:{value:"paused"}});
  assert.ok(list.innerHTML.includes("Backup"));
  assert.ok(!list.innerHTML.includes("Report"));
  search.search("ops");
  assert.equal(taskRailView.q, "ops");
  toggle.events.click();
  assert.equal(rail.hidden, true);
  assert.equal(document.activeElement, toggle, "closing returns focus out of the hidden rail");
  assert.equal(taskRailView.q, "ops");
  toggle.events.click();
  assert.equal(requests, 1, "opening again reuses the current navigation data");
  // A revision affecting another task refreshes only the rail, retaining search and focused row.
  filter.events.change({target:{value:""}});
  list.links[0].focus();
  const previousFocus=document.activeElement;
  const updated=[tasks[0],Object.assign({},tasks[1],{status:"active"}),{id:"t3",name:"New backup",status:"paused",tags:["ops"]}];
  const refreshed=refreshRail(); resolve(updated); await refreshed;
  assert.equal(requests,2);
  assert.ok(list.innerHTML.includes("New backup"));
  assert.ok(list.innerHTML.includes("<small>active</small>"));
  assert.equal(taskRailView.q,"ops"); assert.equal(taskRailView.status,"");
  assert.notEqual(document.activeElement,previousFocus);
  assert.equal(document.activeElement.getAttribute("data-href"),"#/tasks/t2/thread");
  media.matches=true; media.listener();
  assert.equal(toggle.getAttribute("aria-expanded"), "true");
  media.matches=false; media.listener();
  assert.equal(toggle.getAttribute("aria-expanded"), "false");
  assert.equal(taskRailView.q, "ops", "responsive changes keep filters");
  await refreshRail(); assert.equal(requests,2,"a hidden rail does not fetch");
  toggle.events.click(); assert.equal(requests,3,"opening after a hidden revision reloads stale data");
  resolve(updated); await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
  taskRailView.q='"<draft>';
  assert.ok(taskWorkspaceHtml().includes('&quot;&lt;draft&gt;'));
  taskRailView.open=true;
  wireTaskWorkspace("t2", "overview");
  const before=list.innerHTML; box.alive=false;
  resolve(tasks); await Promise.resolve(); await Promise.resolve();
  assert.equal(list.innerHTML, before, "late response must not paint a detached workspace");
  console.log("task workspace navigation checks passed");
})().catch(e => { console.error(e); process.exitCode=1; });
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("task workspace client: %v\n%s", err, out)
	}
}
