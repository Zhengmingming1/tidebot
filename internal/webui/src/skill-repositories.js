  // ---------- skill repositories ----------
  var repositoryScan = null;
  function repositoryInExtensions() { return typeof location !== "undefined" && /^#\/plugins\/sources(?:\/|$)/.test(location.hash); }
  function repositoryHref(id, extension) {
    if (extension === undefined) extension = repositoryInExtensions();
    return (extension ? "#/plugins/sources" : "#/skill-repositories") + (id ? "/" + enc(id) : "");
  }
  function repositoryCrumbs(repo, extension) {
    var crumbs = extension ? [[T("nav.plugins"), "#/plugins"], [T("cap.tab_sources"), repositoryHref(null, true)]] :
      [[T("nav.skills"), "#/skills"], [T("sr.title"), repositoryHref(null, false)]];
    if (repo) crumbs.push([repo.name || repo.url]); else crumbs[crumbs.length - 1].pop();
    return crumbs;
  }
  function stopRepositoryScan() {
    if (repositoryScan) { repositoryScan.abort(); repositoryScan = null; }
  }
  function repositoryNeedsAdoption(s) {
    return !!(s && s.latest_version_id && (s.latest_version_id !== s.current_version_id || s.base_version_id && s.latest_version_id !== s.base_version_id));
  }
  function repositoryMessage(value, warning) {
    var key = warning ? 'sr.warning_' + value : 'wfe.repo_' + value;
    return state.dict[key] == null ? value : T(key);
  }
  function repositoryLastError(repo) {
    var key = 'wfe.' + repo.last_error_code;
    return repo.last_error_code && state.dict[key] != null ? T(key) : repo.last_error || '';
  }
  function repositoryUsageHtml(u) {
    var kinds = { task: 'sr.kind_task', skill_set: 'sr.kind_skill_set', workflow_node: 'sr.kind_workflow_node', button: 'sr.kind_button' };
    var mode = u.mode === 'follow' ? 'sr.usage_follow' : u.mode === 'pinned' ? 'sr.usage_pinned' : 'sr.usage_unknown';
    return '<li>' + esc((kinds[u.kind] ? T(kinds[u.kind]) : u.kind) + ': ' + (u.name || u.id)) + ' <span class="tag">' + esc(T(mode)) + '</span></li>';
  }
  function repositoryStates(c) {
    var s = c.source, out = [];
    if (c.error) out.push("unsupported");
    if (!s) return out.concat("new");
    if (!s.repository_id || s.source_removed || c.missing || c.removed) out.push("removed");
    if (s.local_modified) out.push("modified");
    if (s.update_available || c.tree_hash && s.tree_hash && c.tree_hash !== s.tree_hash) out.push("update");
    if (repositoryNeedsAdoption(s)) out.push("pending");
    return out.length ? out : ["installed"];
  }
  function repositoryBadges(c) {
    return repositoryStates(c).map(function (st) { return '<span class="pill sm ' + (st === "installed" ? "ok" : st === "new" ? "info" : "warn") + '">' + esc(T("sr.st_" + st)) + '</span>'; }).join(" ");
  }
  function repositoryButton(key, attr, primary) {
    return '<button type="button" class="btn sm' + (primary ? ' primary' : '') + '" ' + attr + '>' + esc(T(key)) + '</button>';
  }
  function repositorySourceHtml(s) {
    if (!s) return "";
    return '<section class="card sr-source"><h3>' + esc(T("sr.source")) + '</h3><dl class="kv"><dt>' + esc(T("sr.url")) + '</dt><dd><code>' + esc(s.url) + '</code></dd><dt>' + esc(T("sr.path")) + '</dt><dd><code>' + esc(s.path || ".") + '</code></dd><dt>' + esc(T("sr.ref")) + '</dt><dd>' + esc(s.ref || T("sr.default_ref")) + '</dd><dt>' + esc(T("sr.commit")) + '</dt><dd><code>' + esc(s.commit || "—") + '</code></dd></dl>' +
      (!s.repository_id || s.source_removed ? '<p class="notice">' + esc(T("sr.source_removed")) + '</p>' : '<a class="btn sm" href="' + repositoryHref(s.repository_id) + '">' + esc(T("sr.open")) + '</a>') +
      (s.local_modified ? '<p class="notice">' + esc(T("sr.local_modified")) + '</p>' : '') +
      (repositoryNeedsAdoption(s) ? '<p class="notice">' + esc(T("sr.pending_hint")) + '</p><div class="row-actions"><a class="btn sm" href="#/skills/' + enc(s.skill_id) + '?a=' + enc(s.current_version_id) + '&b=' + enc(s.latest_version_id) + '">' + esc(T("diff.compare")) + '</a>' + repositoryButton("sr.apply", 'data-repository-apply', true) + '</div>' : '') + '</section>';
  }
  function wireRepositorySource(s, scope) {
    var b = $('[data-repository-apply]', scope);
    if (!b) return;
    b.onclick = function () {
      confirmDialog({ title: T("sr.apply"), body: T("sr.apply_hint") + (s.local_modified ? " " + T("sr.local_modified") : ""), ok: T("sr.apply") }).then(function (yes) {
        if (!yes) return;
        b.disabled = true;
        api('/api/skills/' + enc(s.skill_id) + '/repository-apply', { method: 'POST', body: { version_id: s.latest_version_id, expected_current_version_id: s.current_version_id } }).then(function () { toast(T("sr.applied")); refresh(); }).catch(function (e) { b.disabled = false; fail(e); });
      });
    };
  }
  // Public source shortcuts are visible without a network request; adding/scanning is explicit.
  var repositoryPresets = [
    { id: 'anthropic', name: 'Anthropic Skills', owner: 'Anthropic', url: 'https://github.com/anthropics/skills.git', page: 'https://github.com/anthropics/skills', root: 'skills', default_ref: 'main' },
    { id: 'openai', name: 'OpenAI Plugin Skills', owner: 'OpenAI', url: 'https://github.com/openai/plugins.git', page: 'https://github.com/openai/plugins', root: 'plugins', default_ref: 'main' },
    { id: 'vercel', name: 'Vercel Agent Skills', owner: 'Vercel Labs', url: 'https://github.com/vercel-labs/agent-skills.git', page: 'https://github.com/vercel-labs/agent-skills', root: 'skills', default_ref: 'main' }
  ];
  var repositoryPresetPending = Object.create(null);
  function repositoryPresetURL(raw) {
    try {
      var u = new URL(String(raw || '').trim().replace(/^git@github\.com:/i, 'ssh://git@github.com/'));
      if (u.hostname.toLowerCase() !== 'github.com' || ['https:', 'ssh:'].indexOf(u.protocol) < 0 || u.search || u.hash || u.port && u.port !== (u.protocol === 'ssh:' ? '22' : '443')) return '';
      return u.hostname.toLowerCase() + u.pathname.replace(/\/+$/, '').replace(/\.git$/i, '').toLowerCase();
    } catch (_) { return ''; }
  }
  function repositoryPresetMatch(repos, preset) {
    return (repos || []).filter(function (r) {
      var root = (r.root || '').replace(/^\/+|\/+$/g, ''); if (root === '.') root = '';
      var ref = (r.ref || '').replace(/^refs\/heads\//, '');
      return repositoryPresetURL(r.url) === repositoryPresetURL(preset.url) && (!ref || ref === preset.default_ref) && (!root || root === preset.root || preset.root.indexOf(root + '/') === 0);
    })[0] || null;
  }
  function repositoryPresetsHtml(repos) {
    return '<section class="card sr-presets" aria-labelledby="sr-presets-title"><div class="card-head"><h2 id="sr-presets-title">' + esc(T('sr.presets')) + '</h2></div><p class="hint">' + esc(T('sr.presets_hint')) + '</p><div class="sr-preset-grid">' + repositoryPresets.map(function (p, i) {
      var existing = repositoryPresetMatch(repos, p);
      return '<article class="sr-preset"><small>' + esc(p.owner) + '</small><h3>' + esc(p.name) + '</h3><p>' + esc(T('sr.preset_' + p.id)) + '</p><div class="row-actions"><a class="btn sm ghost" href="' + esc(p.page) + '" target="_blank" rel="noopener noreferrer">' + esc(T('sr.source_page')) + '</a>' +
        (existing ? '<a class="btn sm" href="' + repositoryHref(existing.id) + '">' + esc(T('sr.browse_added')) + '</a>' : repositoryButton('sr.add_scan', 'data-sr-preset="' + i + '"', true)) + '</div></article>';
    }).join('') + '</div></section>';
  }
  async function ensureRepositoryPreset(preset, signal) {
    var repos = await api('/api/skill-repositories', {fresh:true,signal:signal});
    if (signal.aborted) throw Object.assign(new Error('cancelled'), {name:'AbortError'});
    var existing = repositoryPresetMatch(repos, preset);
    if (existing) return existing;
    try {
      return await api('/api/skill-repositories', {method:'POST',body:{name:preset.name,url:preset.url,ref:'',root:preset.root},signal:signal});
    } catch (e) {
      if (signal.aborted || e.status !== 400 && e.status !== 409) throw e;
      // Another tab may have added the same preset while this request was in flight.
      repos = await api('/api/skill-repositories', {fresh:true,signal:signal});
      existing = repositoryPresetMatch(repos, preset);
      if (existing) return existing;
      throw e;
    }
  }
  function repositoryAddPreset(preset) {
    if (repositoryPresetPending[preset.id]) return;
    var ac = new AbortController(), seq = routeSeq, saved = null, navigating = false;
    repositoryPresetPending[preset.id] = ac;
    stopRepositoryScan(); repositoryScan = ac;
    modal('<div class="card-head"><h2>' + esc(T('sr.add_scan')) + ' · ' + esc(preset.name) + '</h2></div><div id="sr-preset-progress" role="status" aria-live="polite"><p class="notice">' + esc(T('sr.preset_adding')) + '</p></div><p class="hint">' + esc(T('sr.presets_hint')) + '</p><div class="actions"><button class="btn ghost" data-close>' + esc(T('btn.cancel')) + '</button></div>', function (root, close) {
      var progress = $('#sr-preset-progress', root);
      function alive() { return seq === routeSeq && document.contains(progress) && !ac.signal.aborted; }
      (async function () {
        try {
          saved = await ensureRepositoryPreset(preset, ac.signal);
          if (!alive()) return;
          progress.innerHTML = '<p class="notice">' + esc(T('sr.scanning')) + '</p>';
          await api('/api/skill-repositories/' + enc(saved.id) + '/refresh', {method:'POST',signal:ac.signal});
          if (!alive()) return;
          if (repositoryScan === ac) repositoryScan = null;
          navigating = true; close(); location.hash = repositoryHref(saved.id);
        } catch (e) {
          if (!alive()) return;
          progress.innerHTML = '<p class="notice bad">' + esc(e.message) + '</p>' + (saved ? '<p>' + esc(T('sr.preset_saved')) + '</p><a class="btn" href="' + repositoryHref(saved.id) + '">' + esc(T('sr.open')) + '</a>' : '');
        } finally {
          if (repositoryPresetPending[preset.id] === ac) delete repositoryPresetPending[preset.id];
          // Keep the page stable while the result dialog remains open.
        }
      })();
    }, function () {
      ac.abort(); if (repositoryScan === ac) repositoryScan = null;
      if (repositoryPresetPending[preset.id] === ac) delete repositoryPresetPending[preset.id];
      if (saved && !navigating && seq === routeSeq) refresh();
    });
  }
  function renderSkillRepositories(id, extension) {
    var seq = routeSeq;
    if (id) return api('/api/skill-repositories/' + enc(id) + '/skills', { fresh: true }).then(function (data) {
      if (seq !== routeSeq) return;
      renderRepositoryContents(data.repository, data.candidates || [], extension);
    });
    return api('/api/skill-repositories', { fresh: true }).then(function (repos) {
      if (seq !== routeSeq) return;
      repos = repos || [];
      mount('<div class="page-head"><div><h1>' + esc(T("sr.title")) + '</h1><p>' + esc(T("sr.subtitle")) + '</p></div><div class="actions">' + repositoryButton('sr.add', 'id="sr-add"', true) + '</div></div>' + (extension ? capTabs('sources') : skillTabs('skill-repositories')) +
        repositoryPresetsHtml(repos) + '<p class="hint">' + esc(T('sr.transport_hint')) + '</p><div class="stack">' + (repos.length ? repos.map(function (r, i) {
          return '<section class="card"><div class="card-head"><span class="ico">' + ic('git') + '</span><div class="grow"><h2><a href="' + repositoryHref(r.id) + '">' + esc(r.name || r.url) + '</a></h2><small class="sr-path">' + esc(r.url) + '</small></div><div class="row-actions"><a class="btn sm" href="' + repositoryHref(r.id) + '">' + esc(T('sr.open')) + '</a>' + repositoryButton('btn.edit', 'data-sr-edit="' + i + '"') + repositoryButton('sr.remove', 'data-sr-remove="' + i + '"') + '</div></div><p class="muted">' + esc(T('sr.count', {n: Array.isArray(r.candidates) ? r.candidates.length : 0})) + ' · ' + esc(r.ref || T('sr.default_ref')) + ' · <code>' + esc(r.root || '.') + '</code> · ' + esc(r.last_scanned_at ? T('sr.scanned_at', { t: fullTime(r.last_scanned_at) }) : T('sr.not_scanned')) + '</p>' + (r.last_error || r.last_error_code ? '<p class="notice bad">' + esc(repositoryLastError(r)) + '</p>' : '') + '</section>';
        }).join('') : emptyState('git', T('sr.empty'), T('sr.empty_hint'))) + '</div>', extension ? 'plugins' : 'skills', repositoryCrumbs(null, extension));
      $('#sr-add').onclick = function () { repositoryEditModal(null); };
      $$('[data-sr-preset]').forEach(function (b) { b.onclick = function () { repositoryAddPreset(repositoryPresets[+b.getAttribute('data-sr-preset')]); }; });
      $$('[data-sr-edit]').forEach(function (b) { b.onclick = function () { repositoryEditModal(repos[+b.getAttribute('data-sr-edit')]); }; });
      $$('[data-sr-remove]').forEach(function (b) { b.onclick = function () { repositoryRemove(repos[+b.getAttribute('data-sr-remove')]); }; });
    });
  }
  function repositoryEditModal(repo) {
    var r = repo || {};
    modal('<div class="card-head"><h2>' + esc(T(repo ? 'sr.edit' : 'sr.add')) + '</h2></div><form id="sr-form">' +
      fieldHtml(T('sr.url'), '<input name="url" required maxlength="2048" autocomplete="off" spellcheck="false" aria-describedby="sr-url-help" value="' + esc(r.url || '') + '" placeholder="https://git.example.com/team/skills.git"><small id="sr-url-help" class="hint sr-field-help">' + esc(T('sr.url_hint')) + '</small>') +
      fieldHtml(T('tf.name'), '<input name="name" maxlength="120" value="' + esc(r.name || '') + '">') +
      '<div class="form-row">' + fieldHtml(T('sr.ref'), '<input name="ref" maxlength="255" aria-describedby="sr-ref-help" value="' + esc(r.ref || '') + '"><small id="sr-ref-help" class="hint sr-field-help">' + esc(T('sr.default_ref')) + '</small>') + fieldHtml(T('sr.root'), '<input name="root" maxlength="1024" aria-describedby="sr-root-help" value="' + esc(r.root || '') + '"><small id="sr-root-help" class="hint sr-field-help">' + esc(T('sr.root_hint')) + '</small>') + '</div>' +
      '<p class="hint">' + esc(T('sr.auth_hint')) + '</p>' + (repo ? '<p class="notice">' + esc(T('sr.edit_hint')) + '</p>' : '') + '<p class="field-err" id="sr-save-error" role="alert"></p><div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T('btn.cancel')) + '</button><button type="submit" class="btn primary">' + esc(T('btn.save')) + '</button></div></form>', function (root, close) {
      var f = $('#sr-form', root);
      f.onsubmit = function (ev) {
        ev.preventDefault(); if (!f.reportValidity()) return;
        var b = $('button[type=submit]', f); b.disabled = true;
        api('/api/skill-repositories' + (repo ? '/' + enc(repo.id) : ''), { method: repo ? 'PUT' : 'POST', body: { name: f.name.value.trim(), url: f.url.value.trim(), ref: f.ref.value.trim(), root: f.root.value.trim(), revision: r.revision || 0 } }).then(function (saved) {
          close(); if (location.hash === repositoryHref(saved.id)) refresh(); else location.hash = repositoryHref(saved.id);
        }).catch(function (e) { if (!document.contains(f)) return; b.disabled = false; $('#sr-save-error', root).textContent = e.message; });
      };
    });
  }
  function repositoryRemove(repo) {
    confirmDialog({ title: T('sr.remove'), body: T('sr.remove_hint', { name: repo.name || repo.url }), ok: T('sr.remove'), danger: true }).then(function (yes) {
      if (yes) api('/api/skill-repositories/' + enc(repo.id), { method: 'DELETE' }).then(function () { toast(T('sr.removed')); if (location.hash === repositoryHref()) refresh(); else location.hash = repositoryHref(); }).catch(fail);
    });
  }
  function renderRepositoryContents(repo, candidates, extension) {
    var selected = Object.create(null), query = '', filter = '';
    mount('<div class="page-head"><div><h1>' + esc(repo.name || T('sr.title')) + '</h1><p class="sr-path">' + esc(repo.url) + '</p></div><div class="actions">' + repositoryButton('sr.scan', 'id="sr-scan"', true) + repositoryButton('btn.edit', 'id="sr-edit"') + repositoryButton('sr.clear_cache', 'id="sr-clear-cache"') + repositoryButton('sr.remove', 'id="sr-remove"') + '</div></div>' + (extension ? capTabs('sources') : skillTabs('skill-repositories')) +
      '<p class="muted">' + esc(repo.ref || T('sr.default_ref')) + ' · <code>' + esc(repo.root || '.') + '</code> · ' + esc(repo.last_scanned_at ? T('sr.scanned_at', { t: fullTime(repo.last_scanned_at) }) : T('sr.not_scanned')) + '</p><div id="sr-progress" role="status" aria-live="polite"></div>' +
      (repo.last_error || repo.last_error_code ? '<p class="notice bad">' + esc(T('sr.scan_failed')) + ' ' + esc(repositoryLastError(repo)) + '</p>' : '') +
      '<section class="card flush"><div class="toolbar"><input id="sr-search" type="search" class="grow" placeholder="' + esc(T('sr.search')) + '" aria-label="' + esc(T('sr.search')) + '"><select id="sr-filter" aria-label="' + esc(T('sr.filter')) + '">' + opt('', T('sr.all'), true) + ['new','installed','update','pending','modified','unsupported','removed'].map(function (st) { return opt(st, T('sr.st_' + st)); }).join('') + '</select>' + repositoryButton('sr.select_visible', 'id="sr-select"') + repositoryButton('sr.batch', 'id="sr-batch" disabled', true) + '</div><div id="sr-candidates" class="sr-table"></div></section><p class="hint">' + esc(T('sr.scan_hint')) + '</p>', extension ? 'plugins' : 'skills', repositoryCrumbs(repo, extension));
    var scope = $('#sr-candidates');
    function visible() { return candidates.filter(function (c) { return (!filter || repositoryStates(c).indexOf(filter) >= 0) && (!query || [c.name,c.description,c.path].join(' ').toLowerCase().indexOf(query) >= 0); }); }
    function draw() {
      var rows = visible(), count = Object.keys(selected).length;
      $('#sr-batch').disabled = count === 0;
      $('#sr-batch').textContent = T('sr.batch_n', { n: count });
      scope.innerHTML = rows.length ? '<table class="table"><thead><tr><th class="ck"></th><th>' + esc(T('col.skill')) + '</th><th>' + esc(T('sr.status')) + '</th><th></th></tr></thead><tbody>' + rows.map(function (c) {
        var i = candidates.indexOf(c);
        return '<tr><td class="ck"><input type="checkbox" data-sr-check="' + i + '" aria-label="' + esc(T('sr.select_skill', { name: c.name || c.path })) + '"' + (selected[c.path] ? ' checked' : '') + (c.error || c.missing ? ' disabled' : '') + '></td><td><strong>' + esc(c.name || c.path) + '</strong><small class="sr-path">' + esc(c.path || '.') + '</small><small>' + esc(c.description || T('sk.no_desc_short')) + '</small></td><td>' + repositoryBadges(c) + '</td><td class="right">' + repositoryButton(c.error ? 'sr.details' : 'sr.preview', 'data-sr-preview="' + i + '"') + '</td></tr>';
      }).join('') + '</tbody></table>' : '<p class="muted pad">' + esc(T(repo.commit ? 'sr.no_matches' : 'sr.scan_first')) + '</p>';
      $$('[data-sr-check]', scope).forEach(function (el) { el.onchange = function () { var c = candidates[+el.getAttribute('data-sr-check')]; if (el.checked) selected[c.path] = true; else delete selected[c.path]; draw(); }; });
      $$('[data-sr-preview]', scope).forEach(function (b) { b.onclick = function () { var c = candidates[+b.getAttribute('data-sr-preview')]; if (c.error) repositoryUnavailableModal(c); else repositoryPreviewModal(repo, [c]); }; });
    }
    $('#sr-search').oninput = function () { query = this.value.trim().toLowerCase(); draw(); };
    $('#sr-filter').onchange = function () { filter = this.value; draw(); };
    $('#sr-select').onclick = function () { var cs = visible().filter(function (c) { return !c.error && !c.missing; }), all = cs.every(function (c) { return selected[c.path]; }); cs.forEach(function (c) { if (all) delete selected[c.path]; else selected[c.path] = true; }); draw(); };
    $('#sr-batch').onclick = function () { repositoryPreviewModal(repo, candidates.filter(function (c) { return selected[c.path]; })); };
    $('#sr-edit').onclick = function () { repositoryEditModal(repo); };
    $('#sr-remove').onclick = function () { repositoryRemove(repo); };
    $('#sr-clear-cache').onclick = function () {
      confirmDialog({ title: T('sr.clear_cache'), body: T('sr.clear_cache_hint'), ok: T('sr.clear_cache') }).then(function (yes) {
        if (yes) api('/api/skill-repositories/' + enc(repo.id) + '/clear-cache', {method:'POST'}).then(function () { toast(T('sr.cache_cleared')); refresh(); }).catch(fail);
      });
    };
    $('#sr-scan').onclick = function () {
      var b = this, progress = $('#sr-progress'), ac = new AbortController();
      stopRepositoryScan(); repositoryScan = ac; b.disabled = true;
      progress.innerHTML = '<p class="notice">' + esc(T('sr.scanning')) + ' ' + repositoryButton('btn.cancel', 'data-sr-cancel') + '</p>';
      $('[data-sr-cancel]', progress).onclick = function () { ac.abort(); };
      api('/api/skill-repositories/' + enc(repo.id) + '/refresh', { method: 'POST', signal: ac.signal }).then(function () {
        if (repositoryScan === ac) repositoryScan = null;
        if (document.contains(progress)) refresh();
      }).catch(function (e) {
        if (!document.contains(progress)) return;
        progress.innerHTML = '<p class="notice' + (ac.signal.aborted ? '' : ' bad') + '">' + esc(ac.signal.aborted ? T('sr.cancelled') : e.message) + '</p>';
      }).finally(function () { if (repositoryScan === ac) repositoryScan = null; b.disabled = false; });
    };
    draw();
  }
  function repositoryUnavailableModal(c) {
    modal('<div class="card-head"><h2>' + esc(c.name || c.path) + '</h2></div><p class="notice bad">' + esc(c.removed ? T('sr.source_removed') : repositoryMessage(c.error)) + '</p>' +
      (c.source ? '<a class="btn" href="#/skills/' + enc(c.source.skill_id) + '">' + esc(T('sr.view_skill')) + '</a>' : '') +
      '<div class="actions"><button class="btn ghost" data-close>' + esc(T('btn.close')) + '</button></div>');
  }
  function repositoryDiff(files) {
    files = files || [];
    return diffView({ files: files, added: files.reduce(function (n, f) { return n + (f.added || 0); }, 0), removed: files.reduce(function (n, f) { return n + (f.removed || 0); }, 0) });
  }
  function repositoryPreviewModal(repo, candidates) {
    var ac = new AbortController(), rows = [], changed = false;
    modal('<div class="card-head"><h2>' + esc(T('sr.preview')) + '</h2></div><p class="hint">' + esc(T('sr.install_hint')) + '</p><div id="sr-previews" class="stack" aria-live="polite">' + esc(T('up.reading')) + '</div><div class="actions"><button class="btn ghost" data-close>' + esc(T('btn.close')) + '</button>' + repositoryButton('sr.install_selected', 'id="sr-install" disabled', true) + '</div>', function (root) {
      var host = $('#sr-previews', root), install = $('#sr-install', root);
      function alive() { return document.contains(host) && !ac.signal.aborted; }
      function draw() {
        if (!alive()) return;
        host.innerHTML = rows.map(function (row, i) {
          var p = row.preview, c = row.candidate, source = p && p.source;
          return '<section class="sr-preview"><h3>' + esc(c.name || c.path) + '</h3><p class="sr-path"><code>' + esc(c.path || '.') + '</code></p>' +
            (row.error ? '<p class="notice bad" role="alert">' + esc(row.error) + '</p>' : '') +
            (p ? (p.error ? '<p class="notice bad">' + esc(repositoryMessage(p.error)) + '</p>' : '') + (p.warning ? '<p class="notice">' + esc(repositoryMessage(p.warning, true)) + '</p>' : '') +
              fieldHtml(T('sr.local_name'), '<input data-sr-name="' + i + '" required maxlength="64" value="' + esc(row.name) + '"' + (source && !row.copy || row.result ? ' disabled' : '') + '>') +
              (source && !row.result ? '<label class="check"><input type="checkbox" data-sr-copy="' + i + '"' + (row.copy ? ' checked' : '') + '> ' + esc(T('sr.copy')) + '</label><p class="hint">' + esc(T('sr.download_hint')) + '</p>' : '') +
              (source && source.local_modified ? '<p class="notice">' + esc(T('sr.local_modified')) + '</p>' : '') +
              '<details' + (rows.length === 1 ? ' open' : '') + '><summary>SKILL.md · ' + esc(T('fb.sum', { n: p.files, size: fmtBytes(p.size || 0) })) + '</summary><div class="md sr-markdown">' + mdHtml(p.skill_md || '', null) + '</div><div class="ftree">' + fileTree(p.tree || []) + '</div></details>' +
              (source ? '<details><summary>' + esc(T('diff.compare')) + '</summary>' + repositoryDiff(p.diff) + '</details>' : '') +
              (p.used_by && p.used_by.length ? '<details><summary>' + esc(T('sr.usage')) + '</summary><ul>' + p.used_by.map(repositoryUsageHtml).join('') + '</ul></details>' : '') : '') +
            (row.result ? '<p class="notice ok">' + esc(T(row.applied ? 'sr.applied' : source && !row.copy ? 'sr.downloaded' : 'sr.installed')) + '</p><div class="row-actions"><a class="btn sm" href="#/skills/' + enc(row.result.skill_id) + '">' + esc(T('sr.view_skill')) + '</a>' + repositoryButton('dep.check', 'data-sr-deps="' + i + '"') + repositoryButton('btn.run', 'data-sr-run="' + i + '"') +
              (!row.result.applied && row.result.source && !row.copy && !row.applied ? repositoryButton('sr.apply', 'data-sr-apply="' + i + '"', true) : '') + '</div>' + (source && !row.copy && !row.applied ? '<p class="hint">' + esc(T('sr.apply_hint')) + '</p>' : '') : '') + '</section>';
        }).join('');
        install.disabled = !rows.some(function (row) { return row.preview && !row.preview.error && !row.result; });
        $$('[data-sr-name]', host).forEach(function (el) { el.oninput = function () { rows[+el.getAttribute('data-sr-name')].name = el.value; }; });
        $$('[data-sr-copy]', host).forEach(function (el) { el.onchange = function () { var row = rows[+el.getAttribute('data-sr-copy')]; row.copy = el.checked; row.name = row.copy ? (row.preview.name || row.candidate.name).slice(0, 58) + '-copy' : row.preview.name || row.candidate.name; draw(); }; });
        $$('[data-sr-deps]', host).forEach(function (el) { el.onclick = function () { var r = rows[+el.getAttribute('data-sr-deps')].result; dependencyCheck({kind:'skill',id:r.skill_id}); }; });
        $$('[data-sr-run]', host).forEach(function (el) { el.onclick = function () { var r = rows[+el.getAttribute('data-sr-run')].result; quickRunModal({skill_id:r.skill_id}); }; });
        $$('[data-sr-apply]', host).forEach(function (el) { el.onclick = function () {
          var row = rows[+el.getAttribute('data-sr-apply')]; el.disabled = true;
          api('/api/skills/' + enc(row.result.skill_id) + '/repository-apply', {method:'POST',body:{version_id:row.result.version_id,expected_current_version_id:row.preview.expected_current_version_id}}).then(function () { row.applied = true; row.error = ''; changed = true; if (alive()) { toast(T('sr.applied')); draw(); } }).catch(function (e) { row.error = e.message; draw(); });
        }; });
      }
      // Bound concurrency keeps a large selection from flooding the Git reader.
      var next = 0;
      async function loadNext() {
        while (next < candidates.length && alive()) {
          var i = next++, c = candidates[i], row = rows[i] = {candidate:c,name:c.name || '',copy:false};
          try { row.preview = await api('/api/skill-repositories/' + enc(repo.id) + '/preview', {method:'POST',read:true,body:{path:c.path,commit:repo.commit},signal:ac.signal}); row.name = row.preview.name || row.name; }
          catch (e) { if (!ac.signal.aborted) row.error = e.message; }
        }
      }
      Promise.all([loadNext(),loadNext()]).then(draw);
      install.onclick = async function () {
        install.disabled = true;
        $$('input', host).forEach(function (el) { el.disabled = true; });
        for (var i = 0; i < rows.length && alive(); i++) {
          var row = rows[i]; if (!row.preview || row.preview.error || row.result) continue;
          if (!row.name.trim()) { row.error = T('sr.name_required'); continue; }
          try {
            row.result = await api('/api/skill-repositories/' + enc(repo.id) + '/install', {method:'POST',body:{stage_id:row.preview.stage_id,path:row.candidate.path,commit:repo.commit,name:row.name.trim(),expected_current_version_id:row.preview.expected_current_version_id || '',copy:!!row.copy},signal:ac.signal});
            row.error = ''; changed = true;
          } catch (e) {
            if (ac.signal.aborted) break;
            row.error = e.message;
            if (e.data && e.data.suggested_name) row.name = e.data.suggested_name;
          }
        }
        draw();
      };
    }, function () { ac.abort(); if (changed) refresh(); });
  }
