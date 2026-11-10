const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const ctx = vm.createContext({console, URL, AbortController, window:{addEventListener(){}}, document:{addEventListener(){}}, location:{hash:'#/plugins/sources'}, state:{dict:{}}, routeSeq:1});
Object.assign(ctx,{T:k=>k, enc:encodeURIComponent, esc:s=>String(s).replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]))});
for (const file of ['shell-status','skill-repositories','capabilities','compositions-workflows']) vm.runInContext(fs.readFileSync('src/'+file+'.js','utf8'),ctx);
assert.equal(ctx.navSection('skill-repositories'),'skills');
assert.equal(ctx.navSection('skill-sets'),'skills');
assert.equal(ctx.navSection('workflow-runs'),'workflows');
assert.equal(ctx.navSection('plugins'),'plugins');
assert.equal(ctx.repositoryHref('a b'),'#/plugins/sources/a%20b');
assert.equal(ctx.repositoryHref(),'#/plugins/sources');
assert.equal(ctx.repositoryHref('a',false),'#/skill-repositories/a');assert.equal(ctx.repositoryHref('a'),'#/plugins/sources/a');
assert.deepEqual(JSON.parse(JSON.stringify(ctx.repositoryCrumbs({name:'Example'},true))),[['nav.plugins','#/plugins'],['cap.tab_sources','#/plugins/sources'],['Example']]);
assert.deepEqual(JSON.parse(JSON.stringify(ctx.repositoryCrumbs(null,true))),[['nav.plugins','#/plugins'],['cap.tab_sources']]);
ctx.location.hash='#/skill-repositories';assert.equal(ctx.repositoryHref('a'),'#/plugins/sources/a');
assert(!ctx.skillTabs('skills').includes('skill-repositories'));assert(!ctx.skillTabs('skills').includes('#/workflows'));
for (const [fn,arg] of [[ctx.capTabs,'sources'],[ctx.skillTabs,'skills']]) {
 const h=fn(arg);assert(h.includes('section-tabs'));assert.equal((h.match(/aria-current="page"/g)||[]).length,1);
}
const rendered=ctx.sectionTabs('test','<bad>',[{href:'#/x"',label:'<x>',active:true}]);
assert(!rendered.includes('<bad>'));assert(!rendered.includes('<x>'));assert(rendered.includes('href="#/x&quot;"'));
// Loading/error sidebar state uses the same canonical section as the final page.
const navItems=['home','skills','workflows','plugins'].map(name=>({name,attrs:{},classList:{toggle(k,v){this[k]=v}},getAttribute(){return name},setAttribute(k,v){this.attrs[k]=v},removeAttribute(k){delete this.attrs[k]}}));
ctx.$$=()=>navItems;ctx.setNav('skill-repositories');
assert.equal(navItems.filter(a=>a.attrs['aria-current']==='page')[0].name,'skills');
ctx.setNav('workflow-runs');
assert.equal(navItems.filter(a=>a.attrs['aria-current']==='page')[0].name,'workflows');
assert.equal(navItems.filter(a=>a.attrs['aria-current']==='page').length,1);
let mounted=false;ctx.api=()=>Promise.resolve({usage:{}});ctx.mount=()=>{mounted=true};
(async()=>{const p=ctx.renderCapabilityStorage();ctx.routeSeq++;await p;assert.equal(mounted,false);console.log('PASS section context, links, breadcrumbs, active tabs, escaping, stale storage response');

// Section links remain native links (Back/Forward and modified-click work).
const task=fs.readFileSync('src/task-detail.js','utf8');
assert(task.includes('<a data-tab='));
assert(task.includes('aria-current="page"'));
assert(!task.includes('history.replaceState(null, "", "#/tasks/'));
const router=fs.readFileSync('src/routing-appearance.js','utf8');
assert(router.includes('renderExtensions(parts[1], parts[2])'));
assert(!fs.readFileSync('src/capabilities.js','utf8').includes('pg.insertAdjacentHTML("afterbegin", capTabs(tab))'));

// Repository list/detail renders inherit the parent section in a single mount.
ctx.location.hash='#/plugins/sources';
ctx.ic=()=>'';ctx.emptyState=()=>'';ctx.fullTime=x=>x;ctx.opt=()=>'';ctx.$=()=>({});ctx.$$=()=>[];
let frame;
ctx.mount=(html,nav,crumbs)=>{frame={html,nav,crumbs}};
ctx.api=()=>Promise.resolve([]);
 await ctx.renderSkillRepositories(undefined,true);
 assert.equal(frame.nav,'plugins');assert(frame.html.includes('cap-tabs'));assert(!frame.html.includes('ss-tabs'));
 assert.equal(frame.crumbs[0][0],'nav.plugins');
 ctx.api=()=>Promise.resolve({repository:{id:'repo',name:'Example',url:'https://example.test'},candidates:[]});
 await ctx.renderSkillRepositories('repo',true);
 assert.equal(frame.nav,'plugins');assert.equal(frame.crumbs[1][1],'#/plugins/sources');
 assert(frame.html.includes('cap-tabs'));assert(!frame.html.includes('ss-tabs'));
 console.log('PASS repository list/detail share the extension shell and native task tab links');
})().catch(e=>{console.error(e);process.exitCode=1});
