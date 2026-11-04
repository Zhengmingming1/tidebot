const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
function deferred() { let resolve, reject; const promise = new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}; }
function setup() {
  const nodes = {}, mounts = [];
  const node = sel => nodes[sel] || (nodes[sel] = {innerHTML:'',value:'',checked:false,events:{},addEventListener(k,fn){this.events[k]=fn}});
  const ctx = vm.createContext({console, routeSeq:7, state:{dict:{}}, document:{contains:()=>true},
    T:k=>k, esc:String, enc:encodeURIComponent, ic:()=>'', $:node, $$:()=>[],
    sectionTabs:()=>'<nav>tabs</nav>', mount:(...args)=>mounts.push(args), fail:e=>{throw e}});
  vm.runInContext(fs.readFileSync('src/capabilities.js','utf8'),ctx);
  ctx.capLoadLegacyInventory=()=>{};
  return {ctx,nodes,mounts};
}
(async()=>{
  // Both entry tabs must mount after an ordinary successful API response.
  for (const tab of [undefined,'installed','discover']) {
    const {ctx,nodes,mounts}=setup();
    ctx.api=()=>Promise.resolve({capabilities:[],results:[]});
    await ctx.renderExtensions(tab);
    assert.equal(mounts.length,1, String(tab)+' must leave the loading skeleton');
    assert.equal(mounts[0][1],'plugins');
    assert(nodes['#cap-results'].innerHTML.includes(tab==='discover'?'cap.no_results':'cap.no_installed'));
  }
  // Nonempty lists render as well.
  const full=setup(); full.ctx.api=()=>Promise.resolve({capabilities:[{id:'example',title:'Example'}]});
  await full.ctx.renderExtensions('installed');
  assert(full.nodes['#cap-results'].innerHTML.includes('Example'));
  // Late responses must not repaint after leaving the page, including Back/Forward.
  for (const tab of ['installed','discover']) {
    const {ctx,mounts}=setup(), d=deferred();ctx.api=()=>d.promise;
    const pending=ctx.renderExtensions(tab);ctx.routeSeq++;d.resolve({capabilities:[],results:[]});
    await pending;assert.equal(mounts.length,0);
    ctx.api=()=>Promise.resolve({capabilities:[],results:[]});
    await ctx.renderExtensions(tab);assert.equal(mounts.length,1);
  }
  // Errors must still reach the router's retry/error UI rather than be swallowed.
  const failed=setup();failed.ctx.api=()=>Promise.reject(new Error('unavailable'));
  await assert.rejects(failed.ctx.renderExtensions('installed'),/unavailable/);
  assert.equal(failed.mounts.length,0);
  // Search response ordering is independent from the route's stale-response guard.
  const {ctx,nodes}=setup();ctx.api=()=>Promise.resolve({results:[]});
  await ctx.renderExtensions('discover');
  const old=deferred(), latest=deferred();let requests=0;
  ctx.api=()=>++requests===1?old.promise:latest.promise;
  const submit=nodes['#cap-search'].events.submit;
  submit({preventDefault(){}});submit({preventDefault(){}});
  latest.resolve({results:[{id:'new',title:'Latest'}]});await new Promise(setImmediate);
  old.resolve({results:[{id:'old',title:'Obsolete'}]});await new Promise(setImmediate);
  assert(nodes['#cap-results'].innerHTML.includes('Latest'));
  assert(!nodes['#cap-results'].innerHTML.includes('Obsolete'));
  console.log('PASS extension loading, empty/populated tabs, stale navigation, return, errors and search ordering');
})().catch(e=>{console.error(e);process.exitCode=1});
