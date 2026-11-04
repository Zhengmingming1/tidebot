package webui

import (
	"os/exec"
	"testing"
)

func TestPluginArrayEnumRoundTrip(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	src := liveSrc(t)
	script := regexpFunc(t, src, "  function schemaEnumEqual(a, b) {") + regexpFunc(t, src, "  function esc(v) {") + regexpFunc(t, src, "  function schemaForm(fields, values, secretsSet, ns) {") + regexpFunc(t, src, "  function readSchemaForm(root, fields) {") + `
const assert = require("node:assert/strict");
const state = {dict:{}};
function T(s){return s;}
function fieldHtml(label, inner){return inner;}
function $(q,root){throw Error("unexpected selector");}
function $$(q,root){return root;}
const f={key:"choices",type:"array",items_enum:[1,"1",true,"true",null,"",["a","b"],{b:2,a:1}]};
const wanted=["1",true,null,"",["a","b"],{a:1,b:2}];
const html=schemaForm([f],{choices:wanted},{},"");
const inputs=Array.from(html.matchAll(/<input\b([^>]+)>/g),m=>({value:(m[1].match(/value="([^"]*)"/)||[])[1],checked:/\schecked(?:\s|$)/.test(m[1])}));
assert.equal(inputs.length,8);
assert.deepEqual(inputs.map(x=>x.checked),[false,true,true,false,true,true,true,true]);
assert.deepEqual(readSchemaForm(inputs,[f]).choices,["1",true,null,"",["a","b"],{b:2,a:1}]);
// Reopening and saving is stable; deselection preserves an explicit empty array.
const reopened=schemaForm([f],readSchemaForm(inputs,[f]),{},"");
assert.equal(reopened,html);
inputs.forEach(x=>x.checked=false);
assert.deepEqual(readSchemaForm(inputs,[f]),{choices:[]});
`
	cmd := exec.Command(node, "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("enum round trip: %v\n%s", err, out)
	}
}
