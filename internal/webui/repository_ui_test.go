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

func TestRepositoryPublicPresets(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	script := `
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const ctx = vm.createContext({URL,AbortController,Promise,setTimeout,clearTimeout,console});
ctx.state={dict:{}};ctx.T=k=>k;ctx.enc=encodeURIComponent;
ctx.esc=v=>String(v==null?'':v).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
ctx.routeSeq=1;ctx.skillTabs=()=>'';ctx.ic=()=>'';ctx.emptyState=()=>'<p>empty</p>';ctx.fullTime=x=>x;
vm.runInContext(fs.readFileSync('src/skill-repositories.js','utf8'),ctx);
const presets=ctx.repositoryPresets;
assert.equal(presets.length,3);
assert.equal(presets[1].url,'https://github.com/openai/plugins.git');
assert.equal(presets[1].root,'plugins');
assert(!presets.some(p=>p.url==='https://github.com/openai/skills.git'));
const p=presets[0];
const existing={id:'existing',url:'git@github.com:Anthropics/Skills.git',ref:'refs/heads/main',root:''};
assert.equal(ctx.repositoryPresetMatch([existing],p).id,'existing');
assert(ctx.repositoryPresetMatch([{...existing,url:'https://github.com/anthropics/skills/',root:'skills'}],p));
assert(!ctx.repositoryPresetMatch([{...existing,ref:'feature/custom'}],p));
assert(!ctx.repositoryPresetMatch([{...existing,root:'skills/pdf'}],p));
assert(!ctx.repositoryPresetMatch([{...existing,url:'https://git.example.com/anthropics/skills'}],p));
assert(!ctx.repositoryPresetMatch([{...existing,url:'https://github.com:8443/anthropics/skills'}],p));
let html=ctx.repositoryPresetsHtml([existing]);
assert(html.includes('sr.presets'));assert(!html.includes('data-sr-preset="0"'));assert(html.includes('#/skill-repositories/existing'));
const oldName=p.name;p.name='<img src=x onerror=bad>';
assert(!ctx.repositoryPresetsHtml([]).includes('<img'));p.name=oldName;
let page='',calls=[],refreshes=0,dialogs=0,progress,close;
ctx.mount=h=>{page=h;};ctx.$=()=>({});ctx.$$=()=>[];ctx.api=()=>Promise.resolve([]);
ctx.document={contains:()=>true};ctx.location={hash:'#/skill-repositories'};ctx.refresh=()=>{refreshes++;};
ctx.modal=(markup,onMount,onClose)=>{dialogs++;progress={innerHTML:markup};close=()=>{ctx.document.contains=()=>false;onClose();};ctx.$=()=>progress;onMount({},close);};
const tick=()=>new Promise(r=>setTimeout(r,0));
(async()=>{
 // The shortcuts remain visible both with an empty library and with custom sources.
 await ctx.renderSkillRepositories();assert(page.includes('Anthropic Skills')&&page.includes('OpenAI Plugin Skills')&&page.includes('Vercel Agent Skills'));
 ctx.api=()=>Promise.resolve([{id:'custom',url:'https://company.example/skills.git',name:'Private',candidates:[]}]);
 await ctx.renderSkillRepositories();assert(page.includes('sr.presets')&&page.includes('Private'));
 // Adding an existing standard SSH/HTTPS spelling performs no POST or scan.
 ctx.api=(url,opts)=>{calls.push([url,opts]);return Promise.resolve([existing]);};
 assert.equal((await ctx.ensureRepositoryPreset(p,new AbortController().signal)).id,'existing');assert.equal(calls.length,1);
 // If another tab adds the same URL after our fresh lookup, reuse its row after the conflict.
 calls=[];let lookup=0;
 ctx.api=(url,opts)=>{calls.push([url,opts]);if(opts.method==='POST')return Promise.reject({status:400,message:'duplicate'});return Promise.resolve(lookup++? [existing]:[]);};
 assert.equal((await ctx.ensureRepositoryPreset(p,new AbortController().signal)).id,'existing');assert.equal(calls.filter(x=>x[1].method==='POST').length,1);
 // A scan failure retains the newly saved row and offers its local detail page.
 calls=[];ctx.document.contains=()=>true;ctx.$=()=>progress;
 ctx.api=(url,opts)=>{calls.push([url,opts]);if(url.endsWith('/refresh'))return Promise.reject(new Error('<offline>'));return Promise.resolve(opts.method==='POST'?{id:'saved'}:[]);};
 ctx.repositoryAddPreset(p);ctx.repositoryAddPreset(p);assert.equal(dialogs,1);await tick();await tick();
 assert.equal(calls.filter(x=>x[1].method==='POST'&&!x[0].endsWith('/refresh')).length,1);
 assert(progress.innerHTML.includes('#/skill-repositories/saved'));assert(progress.innerHTML.includes('&lt;offline&gt;'));assert(!calls.some(x=>x[1].method==='DELETE'));assert.equal(ctx.location.hash,'#/skill-repositories');
 close();assert.equal(refreshes,1);assert.equal(ctx.repositoryScan,null);
 // Navigation away while adding never starts a delayed scan or redirects the new page.
 let resolve;calls=[];ctx.document.contains=()=>true;ctx.routeSeq=4;
 ctx.api=(url,opts)=>{calls.push([url,opts]);return new Promise(r=>resolve=r);};
 ctx.repositoryAddPreset(p);ctx.routeSeq=5;ctx.stopRepositoryScan();ctx.document.contains=()=>false;resolve([]);await tick();await tick();
 // An aborted first request cannot lead to a create when api respects the passed signal.
 assert.equal(calls.length,1);assert(calls.every(x=>x[1].signal.aborted));assert(!calls.some(x=>x[0].endsWith('/refresh')));assert.equal(ctx.location.hash,'#/skill-repositories');
 // Form hints are visible descriptions rather than tooltips inside the clipped dialog.
 let formHtml='';ctx.fieldHtml=(label,inner,hint)=>{assert.equal(hint,undefined);return '<label>'+inner+'</label>';};
 ctx.modal=markup=>{formHtml=markup;};ctx.repositoryEditModal(null);
 for(const name of ['url','ref','root']){assert(formHtml.includes('aria-describedby="sr-'+name+'-help"'));assert(formHtml.includes('id="sr-'+name+'-help"'));}
 assert(!formHtml.includes('data-tip'));
 console.log('public repository shortcuts: catalog, matching, duplicate add, failures, navigation and field help passed');
})().catch(e=>{console.error(e);process.exitCode=1;});
`
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("repository preset checks: %v\n%s", err, out)
	}
}
