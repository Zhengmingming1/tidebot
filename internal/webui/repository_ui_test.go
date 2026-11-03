package webui

import (
	"os/exec"
	"testing"
)

// Exercise the shipped JavaScript behavior without pretending this verifies a browser or Git host.
func TestRepositoryUIStateAndCancellation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := `
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const ctx = vm.createContext({ AbortController, Promise, setTimeout, clearTimeout, console });
ctx.window = { AbortController };
ctx.state = { csrf: 'csrf', dict: {'wfe.repo_authentication':'translated'} };
ctx.T = (k, p) => k;
ctx.enc = encodeURIComponent;
ctx.esc = v => String(v == null ? '' : v).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
vm.runInContext(fs.readFileSync('src/skill-repositories.js','utf8'), ctx);
assert.equal(ctx.repositoryLastError({last_error_code:'repo_authentication',last_error:'raw English'}),'wfe.repo_authentication');
assert.equal(ctx.repositoryLastError({last_error:'legacy error'}),'legacy error');
assert.equal(ctx.repositoryLastError({last_error_code:'unknown',last_error:'new error'}),'new error');
assert(ctx.repositoryUsageHtml({kind:'task',name:'<img src=x>',mode:'follow'}).includes('sr.usage_follow'));
assert(!ctx.repositoryUsageHtml({kind:'task',name:'<img src=x>',mode:'follow'}).includes('<img'));
assert(ctx.repositoryUsageHtml({kind:'skill_set',name:'frozen',mode:'pinned'}).includes('sr.usage_pinned'));
const states = c => Array.from(ctx.repositoryStates(c));
assert.deepEqual(states({}), ['new']);
const src = { skill_id: 's', repository_id:'r', current_version_id:'v1', latest_version_id:'v1', base_version_id:'v1', tree_hash:'tree1' };
assert.deepEqual(states({source:src,tree_hash:'tree1'}), ['installed']);
assert.deepEqual(states({source:{...src,local_modified:true},tree_hash:'tree2'}), ['modified','update']);
assert.deepEqual(states({source:{...src,latest_version_id:'v2'},tree_hash:'tree1'}), ['pending']);
assert.deepEqual(states({source:{...src,source_removed:true},removed:true}), ['removed']);
assert.deepEqual(states({source:{...src,update_available:true},tree_hash:'tree1'}), ['update']);
assert(states({error:'unsafe path'}).includes('unsupported'));
// The locally edited current version may already equal upstream content. Adoption still updates its baseline.
assert.deepEqual(states({source:{...src,current_version_id:'v2',latest_version_id:'v2',local_modified:true},tree_hash:'tree1'}), ['modified','pending']);
assert(ctx.repositorySourceHtml({...src,current_version_id:'v2',latest_version_id:'v2'}).includes('data-repository-apply'));
const malicious = ctx.repositorySourceHtml({...src,url:'<img src=x onerror=alert(1)>',path:'<script>x</script>',commit:'"bad"',ref:'<main>'});
assert(!malicious.includes('<img'));
assert(malicious.includes('&lt;script&gt;'));
assert(!ctx.repositorySourceHtml(src).includes('data-repository-apply'));
assert(ctx.repositorySourceHtml({...src,latest_version_id:'v2'}).includes('data-repository-apply'));
const removed = ctx.repositorySourceHtml({...src,source_removed:true});
assert(!removed.includes('href="#/skill-repositories/r"'));
let didAbort = false;
ctx.repositoryScan = {abort(){didAbort=true;}};
ctx.stopRepositoryScan(); assert(didAbort); assert.equal(ctx.repositoryScan,null);
(async () => {
 // A slow catalog response must not paint over a route the user navigated to.
 let resolve, mounts=0;
 ctx.api = () => new Promise(r => {resolve=r;});
 ctx.routeSeq=7; ctx.mount=()=>{mounts++;};
 const old=ctx.renderSkillRepositories(); ctx.routeSeq=8; resolve([]); await old;
 assert.equal(mounts,0);
 // The source panel only changes current after the user confirms, and sends the expected version.
 const btn={}; ctx.$=()=>btn; let calls=[], refreshed=0;
 ctx.confirmDialog=()=>Promise.resolve(false); ctx.api=(...a)=>{calls.push(a);return Promise.resolve({});};ctx.refresh=()=>{refreshed++;};ctx.toast=()=>{};ctx.fail=e=>{throw e;};
 ctx.wireRepositorySource({...src,latest_version_id:'v2'},{});btn.onclick();await new Promise(r=>setTimeout(r,0));assert.equal(calls.length,0);
 ctx.confirmDialog=()=>Promise.resolve(true);btn.onclick();await new Promise(r=>setTimeout(r,0));
 assert.equal(calls[0][0],'/api/skills/s/repository-apply');
 assert.equal(calls[0][1].body.version_id,'v2');assert.equal(calls[0][1].body.expected_current_version_id,'v1');assert.equal(refreshed,1);
 // Caller cancellation remains AbortError, while a timeout remains a timeout.
 const all=fs.readFileSync('src/api-connection.js','utf8');
 vm.runInContext(all.slice(all.indexOf('  function apiRaw('),all.indexOf('  // Toasts:')),ctx);
 let init;
 ctx.netFetch=(path,arg)=>{init=arg;return new Promise((resolve,reject)=>{const fail=()=>{let e=new Error('cancelled');e.name='AbortError';reject(e);};if(arg.signal.aborted)fail();else arg.signal.addEventListener('abort',fail,{once:true});});};
 let ac=new AbortController();let request=ctx.apiRaw('/scan',{method:'POST',body:{},signal:ac.signal});ac.abort();
 await assert.rejects(request,e=>e.name==='AbortError'&&!e.timeout);
 assert.equal(init.headers['X-CSRF-Token'],'csrf');
 ac=new AbortController();ac.abort();await assert.rejects(ctx.apiRaw('/scan',{method:'POST',signal:ac.signal}),e=>e.name==='AbortError');
 await assert.rejects(ctx.apiRaw('/scan',{method:'POST',timeout:2}),e=>e.timeout===true);
 console.log('repository UI state, escaping, stale response, explicit adoption, cancellation: passed');
})().catch(e=>{console.error(e);process.exitCode=1;});
`
	cmd := exec.Command(node, "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("repository UI checks: %v\n%s", err, out)
	}
}
