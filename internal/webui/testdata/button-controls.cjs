const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict'), path = require('node:path');
const src = name => fs.readFileSync(path.join(__dirname, '../src', name), 'utf8');
const nodes = {};
function node(id) { return nodes[id] = { disabled: false, value: '', addEventListener(k, f) { this[k] = f; }, click() { this.clicks = (this.clicks || 0) + 1; } }; }
for (const id of ['#ss-new', '#ss-import', '#ss-import-open']) node(id);
let html = '', failUpload, uploadCalls = 0;
const ctx = vm.createContext({
  T: k => k, esc: String, ic: k => '<svg aria-hidden="true">' + k + '</svg>',
  api: () => Promise.resolve([]), sectionTabs: () => '', emptyState: () => '',
  mount: s => { html = s; }, $: s => nodes[s], $$: () => [],
  FormData: class { append() {} }, toast() {}, fail() {},
  upload: () => { uploadCalls++; return new Promise((resolve, reject) => { failUpload = reject; }); }
});
vm.runInContext(src('skill-repositories.js') + src('compositions-workflows.js'), ctx);
(async () => {
  assert.match(ctx.repositoryButton('edit', '', false), /class="btn sm"/);
  const primary = ctx.repositoryButton('add', '', true, { regular: true, icon: 'plus' });
  assert.match(primary, /class="btn primary"/);
  assert.match(primary, /aria-hidden="true">plus/);
  await ctx.renderSkillSets();
  assert.match(html, /<button type="button" class="btn" id="ss-import-open">/);
  assert.doesNotMatch(html, /<label class="btn">/);
  nodes['#ss-import-open'].onclick();
  assert.equal(nodes['#ss-import'].clicks, 1);
  nodes['#ss-import'].change({target: {files: []}});
  assert.equal(uploadCalls, 0, 'cancelled chooser must not upload');
  const input = nodes['#ss-import']; input.files = [{}];
  input.change({target: input});
  assert.equal(nodes['#ss-import-open'].disabled, true);
  assert.equal(input.disabled, true);
  failUpload(new Error('fixture failure'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(nodes['#ss-import-open'].disabled, false);
  assert.equal(input.disabled, false);
  assert.equal(input.value, '');
  input.change({target: input});
  assert.equal(uploadCalls, 2, 'failed import must remain retryable');
  console.log('PASS button density, import activation, cancel, pending, failure and retry');
})().catch(e => { console.error(e); process.exitCode = 1; });
