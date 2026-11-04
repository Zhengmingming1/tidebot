// Optional browser regression: node internal/webui/testdata/responsive-workspaces.cjs
// Requires Playwright and a local Chromium (CHROMIUM_PATH overrides the default).
// Uses production renderers/CSS/controllers, with deterministic API data and shell helpers.
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '..');
const shots = process.env.LAYOUT_SCREENSHOTS || '/tmp/tidebot-layout-qa';
fs.mkdirSync(shots, { recursive: true });
const css = fs.readFileSync(path.join(root, 'static/styles.css'), 'utf8');
const sources = ['i18n-time.js', 'run-chat.js', 'chat-page.js', 'compositions-workflows.js', 'select-controls.js'].map(x => fs.readFileSync(path.join(root, 'src', x), 'utf8')).join('\n');
const viewports = [[390,844], [800,800], [1024,655], [1180,780], [1280,820], [1440,900]];
const failures = [], measurements = [];
function check(ok, message, details) { if (!ok) failures.push({ message, details }); }
async function start(page, width, height, lang) {
  await page.setViewportSize({width, height});
  await page.setContent(`<!doctype html><html lang="${lang}"><meta charset="utf-8"><style>${css}</style><body><div class="shell" data-nav-size="${width <= 960 ? 'compact' : 'expanded'}"><aside class="sidebar"><a class="brand" href="#/"><span class="brand-mark">T</span><span class="brand-text"><strong>tidebot</strong></span></a><nav class="nav" id="nav"></nav></aside><div class="main"><header class="topbar"><span class="crumbs">tidebot</span></header><main class="content" id="page"></main></div></div></body></html>`);
  await page.evaluate(({dict, lang}) => {
    window.state = {dict, lang, tz:'UTC'};
    window.$ = (s, r) => (r || document).querySelector(s);
    window.$$ = (s, r) => Array.from((r || document).querySelectorAll(s));
    window.esc = v => String(v == null ? '' : v).replace(/[&<>"']/g, c => ({'&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;', "'":'&#39;'}[c]));
    window.enc = encodeURIComponent;
    window.imeKey = e => e.isComposing || e.keyCode === 229;
    window.fieldHtml = (label, inner) => '<label class="field"><span>' + esc(label) + '</span>' + inner + '</label>';
    window.opt = (v, l, on) => '<option value="' + esc(v) + '"' + (on ? ' selected' : '') + '>' + esc(l) + '</option>';
    window.mount = html => { document.querySelector('#page').innerHTML = html; };
    window.mdHtml = s => '<p>' + esc(s) + '</p>';
    window.toast = () => {};
    window.fail = e => { throw e; };
    window.api = async url => {
      if (url === '/api/skills') return window.fixtureSkills;
      if (url === '/api/skill-sets') return [];
      if (url.includes('/versions')) return {versions:[]};
      if (url.startsWith('/api/workflow-runs')) return [];
      if (url.startsWith('/api/workflows/')) return window.fixtureWorkflow;
      throw Error('Unexpected fixture API: ' + url);
    };
  }, {dict:JSON.parse(fs.readFileSync(path.join(root, `static/i18n/${lang}.json`))), lang});
  await page.addScriptTag({content:sources});
}
async function rects(page, selectors) {
  return page.evaluate(selectors => {
    const result = {viewport:{width:innerWidth,height:innerHeight}, document:{width:document.documentElement.scrollWidth,client:document.documentElement.clientWidth}};
    for (const selector of selectors) {
      const e = document.querySelector(selector); if (!e) continue;
      const r = e.getBoundingClientRect(), c = getComputedStyle(e);
      result[selector] = {x:r.x,y:r.y,right:r.right,bottom:r.bottom,width:r.width,height:r.height,scrollWidth:e.scrollWidth,clientWidth:e.clientWidth,scrollHeight:e.scrollHeight,clientHeight:e.clientHeight,overflowX:c.overflowX,overflowY:c.overflowY,display:c.display,columns:c.gridTemplateColumns,position:c.position};
    }
    return result;
  }, selectors);
}
async function chat(page, width, height, lang, count, active) {
  await start(page, width, height, lang);
  await page.evaluate(({count,active}) => {
    const long = state.lang === 'zh-CN' ? '非常长的会话标题与最近的工作内容讨论'.repeat(10) : 'Very long conversation title about recent engineering work '.repeat(10);
    const list = Array.from({length:count}, (_, i) => ({id:'chat_' + i, title:i === 0 ? long : 'Conversation ' + i, origin:i % 2 ? 'run' : 'chat', turns:3, status:'idle', updated_at:'2026-11-03T08:00:00Z'}));
    const session = active ? {...list[0], created_at:'2026-11-03T08:00:00Z',turn_list:[{index:0,message:'Test question',answer:'Test answer',status:'succeeded',created_at:'2026-11-03T08:00:00Z'}]} : null;
    mount(csPageHtml(list,session));
  }, {count, active});
  let sizes = await rects(page, ['.cs','.cs-side','.cs-list','.cs-item','.cs-main','#rc-form']);
  const label = `chat ${lang} ${width} ${count} ${active ? 'active' : 'blank'}`;
  check(sizes.document.width <= sizes.document.client + 1, label + ': document overflow', sizes);
  if (count) {
    const list = sizes['.cs-list'], side = sizes['.cs-side'];
    check(list.bottom <= side.bottom + 1, label + ': list escapes sidebar', sizes);
    if (width > 640) {
      check(list.scrollWidth <= list.clientWidth + 1, label + ': history horizontal overflow', sizes);
      check(Math.abs(list.bottom - side.bottom) <= 2, label + ': list fails to fill sidebar', sizes);
      if (count === 200) check(list.scrollHeight > list.clientHeight, label + ': history not locally scrollable', sizes);
    }
    await page.locator('.cs-item').first().hover();
    await page.locator('.cs-item').first().focus();
    const hovered = await rects(page, ['.cs-list','.cs-item']);
    check(hovered['.cs-list'].scrollWidth === list.scrollWidth, label + ': hover/focus adds scroll width', hovered);
    check(await page.locator('.cs-item[data-tip]').count() === 0, label + ': history has layout-changing pseudo tooltip');
    if (count === 200) {
      await page.locator('.cs-item').last().focus();
      const visible = await page.locator('.cs-item').last().evaluate(e => {const a=e.getBoundingClientRect(),b=e.parentElement.getBoundingClientRect();return a.bottom<=b.bottom+1 && a.right<=b.right+1 && a.top>=b.top-1 && a.left>=b.left-1;});
      check(visible, label + ': final history cannot be focused visibly');
    }
  }
  check(sizes['#rc-form'].width > 0, label + ': composer missing', sizes);
  if (lang === 'en' && [390,1024,1440].includes(width) && count === 200 && !active) {
    await page.locator('.cs-item').first().focus();
    await page.screenshot({path:path.join(shots,`chat-${width}.png`)});
  }
  measurements.push({label,sizes});
}
async function workflow(page, width, height, lang, populated) {
  await start(page,width,height,lang);
  await page.evaluate(populated => {
    window.fixtureSkills = Array.from({length:10},(_,i)=>({id:'skill_'+i,name:i ? 'Skill '+i : 'Research and summarize documents'}));
    const nodes = populated ? Array.from({length:8},(_,i)=>({id:'step_'+i,name:'Research stage '+i,kind:'skill',prompt:'{{input}}',join:'all',skill_bindings:[{skill_id:'skill_0',role:'primary',mode:'follow'}],needs:i ? [{node_id:'step_'+(i-1)}] : [],position:{x:40+(i%4)*290,y:50+Math.floor(i/4)*150}})) : [];
    window.fixtureWorkflow = {id:'wf_test',name:'Research workflow',version:1,definition:{name:'Research workflow',description:'Fixture description',max_parallel:2,timeout_minutes:30,nodes}};
    return renderWorkflowEditor(populated ? 'wf_test' : 'new');
  },populated);
  const label = `workflow ${lang} ${width} ${populated ? 'populated' : 'empty'}`;
  const selectors = ['.wf-meta','.wf-options','.wf-designer','.wf-palette','.wf-canvas-panel','.wf-canvas-scroll','.wf-inspector'];
  let sizes = await rects(page,selectors);
  check(sizes.document.width <= sizes.document.client + 1,label + ': document overflow',sizes);
  check(!await page.locator('.wf-options').evaluate(e=>e.open),label + ': options not collapsed initially');
  check(sizes['.wf-palette'].display === 'flex',label + ': palette not horizontal',sizes);
  const canvas=sizes['.wf-canvas-panel'],inspector=sizes['.wf-inspector'],host=sizes['.wf-canvas-scroll'];
  if(width>=901) {
    check(inspector.x >= canvas.right - 1 && Math.abs(inspector.y-canvas.y)<=1,label + ': inspector not beside canvas',sizes);
    check(Math.abs(inspector.width-280)<=1,label + ': inspector is not 280px',sizes);
    check(host.y < height - 150,label + ': initial graph below first screen',sizes);
  } else check(inspector.y>=canvas.bottom-1,label + ': narrow inspector not stacked',sizes);
  check(host.scrollWidth >= host.clientWidth && host.overflowX === 'auto',label + ': graph not locally scrollable',sizes);
  if (populated) {
    check(await page.locator('#wf-canvas [data-node]').count()===8,label + ': missing nodes');
    // Exercise the production keyboard controller for the narrow zoom select.
    const sceneWidth=await page.locator('#wf-canvas').evaluate(e=>e.viewBox.baseVal.width);
    await page.locator('#wf-zoom').focus(); await page.keyboard.press('End'); await page.keyboard.press('Enter');
    check(await page.locator('#wf-zoom').inputValue()==='125',label + ': keyboard zoom did not select 125%');
    check(await page.locator('#wf-canvas').evaluate(e=>parseFloat(e.style.width))===sceneWidth*1.25,label + ': zoom did not resize graph');
    const afterZoom=await rects(page,selectors);
    check(afterZoom.document.width <= afterZoom.document.client + 1,label + ': zoom escapes page',afterZoom);
  }
  if(lang==='en' && [390,1024,1440].includes(width) && populated) await page.screenshot({path:path.join(shots,`workflow-${width}-collapsed.png`)});
  await page.locator('.wf-options summary').click();
  check(await page.locator('[name=max_parallel]').isVisible(),label + ': expanded options missing');
  let expanded=await rects(page,selectors);
  check(expanded.document.width<=expanded.document.client+1,label+': expanded options overflow',expanded);
  if(lang==='en' && [390,1024].includes(width) && populated) await page.screenshot({path:path.join(shots,`workflow-${width}-expanded.png`)});
  await page.locator('.wf-options summary').click();
  check(!await page.locator('[name=max_parallel]').isVisible(),label + ': options do not collapse again');
  measurements.push({label,sizes,expanded});
}
async function popup(page,width,height,lang,long) {
  await start(page,width,height,lang);
  await page.evaluate(long=>mount('<label>Provider<select id="provider" style="width:80px"><option value="free">kilo-free</option><option value="long">'+(long?'VeryLongUnbrokenProviderName'.repeat(30):'kilo-free-extended-provider')+'</option><option value="last">Zulu</option></select></label>'),long);
  await page.locator('#provider').focus(); await page.keyboard.press('ArrowDown');
  const label=`popup ${lang} ${width} ${long?'long':'short'}`;
  const s=await rects(page,['#provider','.select-popup','.select-popup-option']);
  const p=s['.select-popup'];
  check(p.x>=7 && p.right<=width-7 && p.y>=7 && p.bottom<=height-7,label+': popup escapes viewport',s);
  check(p.scrollWidth<=p.clientWidth+1,label+': popup horizontal overflow',s);
  check(p.width>s['#provider'].width,label+': popup constrained to narrow trigger',s);
  if(!long) check(await page.locator('.select-popup-option').first().evaluate(e=>e.clientHeight<50),label+': kilo-free wraps unnecessarily');
  await page.keyboard.press('End');await page.keyboard.press('Enter');
  check(await page.locator('#provider').inputValue()==='last',label+': keyboard choose failed');
  await page.keyboard.press('ArrowDown');await page.keyboard.press('Home');await page.keyboard.press('Escape');
  check(await page.locator('#provider').inputValue()==='last' && await page.locator('.select-popup').count()===0,label+': Escape changed value or left popup');
  await page.keyboard.press('ArrowDown');await page.keyboard.press('Tab');
  check(await page.locator('.select-popup').count()===0,label+': Tab left popup');
  if(width===390 && lang==='en') {await page.locator('#provider').focus();await page.keyboard.press('ArrowDown');await page.screenshot({path:path.join(shots,`popup-${long?'long':'short'}.png`)});}
}
(async()=>{
  const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH||'/usr/bin/chromium',headless:true,args:['--no-sandbox']});
  const page=await browser.newPage();
  page.on('pageerror', e=>failures.push({message:'Browser exception',details:e.message}));
  try {
    for(const lang of ['en','zh-CN']) for(const [width,height] of viewports) {
      for(const count of [0,3,200]) for(const active of count ? [false,true] : [false]) await chat(page,width,height,lang,count,active);
      for(const populated of [false,true]) await workflow(page,width,height,lang,populated);
      for(const long of [false,true]) await popup(page,width,height,lang,long);
    }
  } finally {await browser.close();fs.writeFileSync(path.join(shots,'measurements.json'),JSON.stringify({failures,measurements},null,2));}
  if(failures.length) {console.error(JSON.stringify(failures,null,2));process.exitCode=1;}
  else console.log(`PASS: chat/workflow/dropdown responsive checks in en/zh-CN at ${viewports.length} viewport sizes; screenshots in ${shots}`);
})().catch(e=>{console.error(e);process.exitCode=1;});
