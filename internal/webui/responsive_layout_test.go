package webui

import (
	"os/exec"
	"testing"
)

// Window resizing changes navigation presentation and keeps table scrolling local,
// without replacing controls or requiring the page to reload.
func TestResponsiveNavigationAndTables(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	src := liveSrc(t)
	script := regexpFunc(t, src, "  function syncShellNav() {") + regexpFunc(t, src, "  function wireShellNav() {") + regexpFunc(t, src, "  function fitTables(root) {") + `
const assert=require("node:assert/strict");
let shellNavChoice=null,shellNavOff=null,focused=null,media;
function el(){
  const e={attrs:{},handlers:{},children:[],className:"",setAttribute(k,v){this.attrs[k]=v;},getAttribute(k){return this.attrs[k];},
    addEventListener(k,fn){this.handlers[k]=fn;},removeEventListener(k){delete this.handlers[k];},focus(){focused=this;},
    insertBefore(child,before){this.children.splice(this.children.indexOf(before),0,child);child.parentElement=this;child.parentNode=this;},
    appendChild(child){if(child.parentElement){const p=child.parentElement;p.children.splice(p.children.indexOf(child),1);}this.children.push(child);child.parentElement=this;child.parentNode=this;}};
  e.classList={contains:k=>e.className.split(" ").includes(k)};return e;
}
const shell=el(),side=el(),toggle=el(),nodes={"#shell":shell,"#shell .sidebar":side,"#nav-toggle":toggle};
function $(q){return nodes[q]||null;} function T(k){return k;}
const window={innerWidth:1440,matchMedia:()=>media={addEventListener(k,fn){this.listener=fn;},removeEventListener(){this.listener=null;}}};
for(const width of [480,640,800,1024,1180,1440,1920]){
  window.innerWidth=width;shellNavChoice=null;syncShellNav();
  assert.equal(shell.attrs["data-nav-size"],width<=960?"compact":"expanded");
  assert.equal(toggle.attrs["aria-expanded"],String(width>960));
}
window.innerWidth=480;wireShellNav();
shellNavChoice=false;syncShellNav();assert.equal(shell.attrs["data-nav-size"],"expanded");
side.handlers.click({target:{closest:()=>({})}});assert.equal(shell.attrs["data-nav-size"],"compact");
window.innerWidth=1440;media.listener();assert.equal(shell.attrs["data-nav-size"],"expanded");
side.handlers.keydown({key:"Escape"});assert.equal(focused,toggle);assert.equal(shell.attrs["data-nav-size"],"compact");
window.innerWidth=800;media.listener();assert.equal(shell.attrs["data-nav-size"],"compact");

const parent=el(),table=el(),handler=()=>{};table.handlers.click=handler;parent.appendChild(table);
let creations=0;const document={createElement:()=>{creations++;return el();}};
function $$(q){assert.equal(q,"table.table");return [table];}
fitTables(parent);const wrapper=table.parentElement;
assert.equal(wrapper.className,"table-scroll");assert.equal(wrapper.tabIndex,0);assert.equal(wrapper.attrs.role,"region");
assert.equal(wrapper.children[0],table);assert.equal(table.handlers.click,handler,"wrapping must not replace table controls");
fitTables(parent);assert.equal(creations,1,"refreshing data must not nest wrappers");
console.log("responsive navigation and table checks passed");
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("responsive layout: %v\n%s", err, out)
	}
}
