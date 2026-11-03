package webui

import (
	"os/exec"
	"testing"
)

func TestFreePoolUIConsentAndManagedProvider(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := `
const assert = require('node:assert/strict'), fs = require('node:fs'), vm = require('node:vm');
const el = () => ({listeners:{},classList:{add(){},remove(){}},addEventListener(k,f){this.listeners[k]=f;}});
let live=true, html='', closed=0, refreshed=0, calls=[], failModels=false;
const form=el(), ack=el(), model=el(), load=el(), submit=el(), error=el(), info=el(), status=el();
ack.checked=false; model.value='kilo-auto/free'; form.elements={acknowledge_prompt_training:ack,model};
const ctx=vm.createContext({Promise,console,setTimeout,clearTimeout});
ctx.T=(k)=>k;ctx.ic=()=>'';ctx.enc=encodeURIComponent;ctx.esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
ctx.document={contains:()=>live};ctx.fail=e=>{throw e};ctx.toast=()=>{};ctx.refresh=()=>refreshed++;
ctx.modal=(s,cb)=>{html=s;cb({},()=>{closed++;live=false;});};
const selectors={'#free-pool-form':form,'#fp-load':load,'button[type="submit"]':submit,'#fp-error':error,'#fp-model-info':info,'#fp-status':status};
ctx.$=s=>selectors[s]||el();
ctx.rpc=async(m,p)=>{calls.push([m,p]);if(m==='plugins.config_get')return {values:{}};if(m==='free_pool.status')return {ready:true};if(m==='free_pool.models'){if(failModels)throw Error('rate limited');return {models:[{id:'kilo-auto/free',name:'<unsafe>',context_length:123,max_output_tokens:456}]};}return {};};
vm.runInContext(fs.readFileSync('src/plugins.js','utf8'),ctx);
vm.runInContext(fs.readFileSync('src/providers.js','utf8'),ctx);
const tick=()=>new Promise(r=>setImmediate(r));
(async()=>{
 await ctx.freePoolConfigModal();
 assert.deepEqual(calls.map(x=>x[0]),['plugins.config_get','free_pool.status']);
 assert(load.disabled);assert(!html.includes('name="acknowledge_prompt_training" checked'));
 load.listeners.click();await tick();assert.equal(calls.length,2,'consent cannot be bypassed by calling the handler');
 ack.checked=true;ack.listeners.change();assert(!load.disabled);
 load.listeners.click();await tick();
 assert.deepEqual(calls.slice(2).map(x=>x[0]),['plugins.config_set','free_pool.models','free_pool.status']);
 assert.equal(calls[2][1].values.acknowledge_prompt_training,true);assert.equal(calls[3][1].refresh,true);
 assert(model.innerHTML.includes('&lt;unsafe&gt;'));assert(!model.innerHTML.includes('<unsafe>'));assert(!load.disabled);
 failModels=true;load.listeners.click();await tick();assert.equal(error.textContent,'rate limited');assert(!submit.disabled);
 ack.checked=false;ack.listeners.change();const before=calls.length;
 form.listeners.submit({preventDefault(){}});await tick();
 assert.equal(calls.length,before+1);assert.equal(calls[before][0],'plugins.config_set');assert.equal(calls[before][1].values.acknowledge_prompt_training,false);assert.equal(closed,1);assert.equal(refreshed,1);
 const host={};ctx.providerDetail(host,{name:'kilo-free',managed_by:'kilo-free-pool',model:'<model>'},null,{},()=>{});
 assert(host.innerHTML.includes('&lt;model&gt;'));assert(host.innerHTML.includes('id="fp-config"'));assert(!host.innerHTML.includes('name="api_key"'));assert(!host.innerHTML.includes('id="pd-del"'));assert(!host.innerHTML.includes('id="pd-dup"'));
 assert.equal(ctx.pvDisplay({name:'kilo-free',managed_by:'kilo-free-pool'},{}),'of.kilo-free-pool.n');
 // A normal user profile with the same name is not treated as plugin-owned.
 assert.equal(ctx.pvDisplay({name:'kilo-free'},{}),'kilo-free');
})().catch(e=>{console.error(e);process.exitCode=1;});
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("free pool UI: %v\n%s", err, out)
	}
}
