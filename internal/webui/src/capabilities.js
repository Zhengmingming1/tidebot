  // Extensions use server-produced, commit-pinned proposals. Model output is never an install instruction.
  function capList() { return api("/api/capabilities", { fresh: true }).then(function (r) { return r.capabilities || []; }); }
  function capLock(c) { return c.lock || c; }
  function capText(k) { return state.dict["cap." + k] ? T("cap." + k) : k; }
  function capTabs(tab) {
    return sectionTabs("cap-tabs", T("nav.plugins"), ["installed", "discover", "tools", "sources", "plugins"].map(function (t) {
      return { href: "#/plugins/" + t, label: T("cap.tab_" + t), active: t === tab };
    }));
  }
  function capCanConnect(component) {
    return !component.disabled && component.type === "mcp" && component.mcp && ["http", "streamable-http", "sse"].indexOf(component.mcp.type) >= 0;
  }
  function capSummary(l) {
    var src = l.source || {}, scan = l.scan || {}, req = l.requires || {}, resolved = l.resolved || {};
    var deps = (req.tools || []).concat(req.runtimes || []).map(function (d) {
      var r = resolved[d.name] || {};
      return '<span class="pill plain sm" data-tip="' + esc(r.path || r.note || "") + '">' + esc(d.name + (d.version ? " " + d.version : "") + " · " + capText(r.status || "unchecked")) + '</span>';
    }).join(" ");
    return '<dl class="cap-meta"><div><dt>' + esc(T("cap.source")) + '</dt><dd>' + esc(src.url || src.publisher || "") + ' <span class="pill plain sm">' + esc(capText(src.trust || "unknown")) + '</span><small>' + esc(src.commit || src.ref || "") + (src.license ? ' · ' + esc(src.license) : '') + '</small></dd></div>' +
      '<div><dt>' + esc(T("cap.contents")) + '</dt><dd>' + esc((l.components || []).map(function (c) { return c.name + (c.disabled ? ' (' + T('cap.disabled') + ')' : '') + (c.mcp ? ' · ' + (c.mcp.url || [c.mcp.command].concat(c.mcp.args || []).join(' ')) + (Object.keys(c.mcp.env || {}).length ? ' · env: ' + Object.keys(c.mcp.env).join(', ') : '') : ''); }).join(', ')) + '</dd></div>' +
      '<div><dt>' + esc(T("cap.requires")) + '</dt><dd>' + (deps || esc(T("cap.none"))) + '</dd></div>' +
      '<div><dt>' + esc(T("cap.scan")) + '</dt><dd>' + esc(capText(scan.verdict || "unchecked")) + ' · ' + esc(scan.scanner || "") + '</dd></div><div><dt>' + esc(T("cap.permissions")) + '</dt><dd>' + esc(((l.permissions || {}).tools || []).join(', ') || T("cap.none")) + ((l.permissions || {}).side_effects ? ' · ' + esc(T("cap.side_effects")) : '') + '</dd></div><div><dt>' + esc(T("cap.network")) + '</dt><dd>' + esc(((l.permissions || {}).network || []).join(', ') || T("cap.none")) + '</dd></div></dl>';
  }
  function capProposalHtml(p) {
    var l = capLock(p), policy = p.policy || {}, pending = p.status === "pending", optimize = p.kind === "skill_optimize", uninstall = p.kind === "uninstall";
    var expired = p.expires_at && Date.parse(p.expires_at) <= Date.now();
    var findings = ((l.scan || {}).findings || []).map(function (f) { return '<li>' + esc(f.file + (f.line ? ':' + f.line : '') + ' · ' + f.note) + '</li>'; }).join("");
    return '<article class="card cap-proposal" data-cap-proposal="' + esc(p.id) + '"><div class="card-head"><span class="ico">' + ic("box") + '</span><h3>' + esc(l.title || l.id || p.id) + '</h3><span class="pill plain sm">' + esc(capText(expired && pending ? "expired" : p.status)) + '</span></div>' + capSummary(l) +
      ((p.dependency_plan || []).length ? '<details><summary>' + esc(T("cap.dependency_plan")) + '</summary><ul class="cap-findings">' + p.dependency_plan.map(function(d){var recipe=d.recipe || {};return '<li>' + esc([d.name,capText(d.status),recipe.version,recipe.backend,d.note].filter(Boolean).join(' · ')) + capDependencyManual(d) + '</li>';}).join('') + '</ul></details>' : '') +
      (uninstall ? '<p class="muted sm">' + esc(T("cap.uninstall_review")) + '</p><p class="muted sm">' + esc(T("cap.version") + ": " + (p.base_version_id || "")) + '</p><pre class="cap-file">' + esc(JSON.stringify(p.uninstall_preview || {}, null, 2)) + '</pre>' : '') +
      ((p.skill_reconciliation || []).some(function(x){return x.locally_modified;}) ? '<fieldset><legend>' + esc(T("cap.local_changes")) + '</legend><p class="muted sm">' + esc(T("cap.local_choices_note")) + '</p>' + (p.skill_reconciliation || []).filter(function(x){return x.locally_modified;}).map(function(x){return '<label class="field"><span>'+esc(x.name+' · '+x.current_version_id+(x.removed?' · '+T('cap.removed_upstream'):''))+'</span><select data-cap-local-choice="'+esc(x.skill_id)+'"'+(!pending?' disabled':'')+'><option value="">'+esc(T('cap.choose'))+'</option>'+['keep_local','copy','adopt_upstream'].map(function(v){return '<option value="'+v+'">'+esc(T('cap.'+v))+'</option>';}).join('')+'</select></label>';}).join('')+'</fieldset>' : '') +
      (p.diff ? '<details><summary>' + esc(T("cap.diff")) + '</summary><pre class="cap-file">' + esc(JSON.stringify(p.diff,null,2)) + '</pre></details>' : '') +
      (findings ? '<details><summary>' + esc(T("cap.findings")) + '</summary><ul class="cap-findings">' + findings + '</ul></details>' : '') +
      (optimize ? '<p class="pill warn">' + esc(T("cap.optimize_cost")) + '</p>' : '') +
      '<p class="muted sm">' + esc(T(uninstall ? "cap.uninstall_review" : optimize ? "cap.optimize_proposal_note" : ((p.dependency_plan || []).some(function (d) { return d.status === "download"; }) ? "cap.confirm_download_note" : "cap.confirm_note"))) + '</p>' +
      (pending && !expired && (p.dependency_plan || []).some(function(d){return d.status === "download";}) ? '<label class="cap-check"><input type="checkbox" data-cap-download> ' + esc(T("cap.download_deps")) + '</label>' : '') +
      (pending && !expired && policy.review_required ? '<label class="cap-check"><input type="checkbox" data-cap-reviewed> ' + esc(T("cap.reviewed")) + '</label>' : '') +
      (pending && !expired && policy.second_confirmation_required ? '<label class="cap-check"><input type="checkbox" data-cap-risk> ' + esc(T("cap.risk_confirm")) + '</label>' : '') +
      (policy.allowed === false ? '<p class="field-err">' + esc(policy.reason || T("cap.blocked")) + '</p>' : '') +
      '<p class="field-err" data-cap-error role="alert"></p><div class="actions">' + (uninstall ? '' : '<button type="button" class="btn sm ghost" data-cap-files>' + esc(T("cap.files")) + '</button>') +
      (pending && !expired ? '<button type="button" class="btn sm ghost" data-cap-dismiss>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn sm primary" data-cap-confirm' + (policy.allowed === false || policy.review_required || policy.second_confirmation_required ? ' disabled' : '') + '>' + esc(T(uninstall ? "cap.uninstall" : optimize ? "cap.analyze_paid" : (p.kind === "update" ? "cap.adopt_update" : "cap.install"))) + '</button>' : '') + '</div></article>';
  }
  function capFilesDialog(p) {
    var prefix = "/api/capabilities/proposals/" + enc(p.id), sid = "session_id=" + enc(p.session_id);
    return api(prefix + "/files?" + sid, { fresh: true }).then(function (r) {
      var files = r.files || [], names = files.map(function (x) { return typeof x === "string" ? x : x.path; });
      modal('<div class="card-head"><h2>' + esc(T("cap.files")) + '</h2></div><label class="field"><span>' + esc(T("cap.file")) + '</span><select id="cap-file-select">' + names.map(function (n) { return '<option value="' + esc(n) + '">' + esc(n) + '</option>'; }).join("") + '</select></label><pre class="cap-file" id="cap-file-text"></pre><p class="muted sm">' + esc(T("cap.untrusted")) + '</p><div class="actions"><button type="button" class="btn" data-close>' + esc(T("btn.close")) + '</button></div>', function (root) {
        var sel = $("#cap-file-select", root), output = $("#cap-file-text", root), seq = 0;
        var load = function () { var n = ++seq; output.textContent = T("cap.loading"); api(prefix + "/file?" + sid + "&path=" + enc(sel.value), { fresh: true }).then(function (v) { if (n === seq && document.contains(output)) output.textContent = v.text || ""; }).catch(function (e) { if (n === seq) output.textContent = e.message; }); };
        sel.addEventListener("change", load); if (names.length) load();
      });
    });
  }
  function capWireProposals(root, proposals, done) {
    $$("[data-cap-proposal]", root).forEach(function (el) {
      var p = proposals.filter(function (x) { return x.id === el.getAttribute("data-cap-proposal"); })[0]; if (!p) return;
      var btn = $("[data-cap-confirm]", el), reviewed = $("[data-cap-reviewed]", el), risk = $("[data-cap-risk]", el);
      var sync = function () { if (btn && !el._busy) btn.disabled = (p.policy || {}).allowed === false || !!(reviewed && !reviewed.checked) || !!(risk && !risk.checked) || $$("[data-cap-local-choice]",el).some(function(x){return !x.value;}); };
      $$("[data-cap-local-choice]",el).forEach(function(x){x.addEventListener("change",sync);}); sync();
      if (reviewed) reviewed.addEventListener("change", sync); if (risk) risk.addEventListener("change", sync);
      var action = function (kind) {
        if (el._busy) return; el._busy = true; $$("button", el).forEach(function (b) { b.disabled = true; });
        var localChoices={};$$("[data-cap-local-choice]",el).forEach(function(x){if(x.value)localChoices[x.getAttribute("data-cap-local-choice")]=x.value;});
        api("/api/capabilities/proposals/" + enc(p.id) + "/" + kind, { method: "POST", body: { session_id: p.session_id, local_choices:localChoices, version_id: p.base_version_id || "", reviewed: !!(reviewed && reviewed.checked), secondary_confirm: !!(risk && risk.checked), install_dependencies: !!($("[data-cap-download]", el) && $("[data-cap-download]", el).checked) } }).then(function (v) { toast(T(kind === "confirm" ? (p.kind === "uninstall" ? "cap.uninstalled" : p.kind === "skill_optimize" ? "cap.analysis_ready" : "cap.installed") : "cap.dismissed")); done(); if (kind === "confirm" && p.kind !== "skill_optimize" && p.kind !== "uninstall" && v && v.id) capAutoTest(v.id); if (v && /^#\/skills\/[\w-]+$/.test(v.navigation_path || "")) location.hash = v.navigation_path; }).catch(function (e) {
          el._busy = false; $$("button", el).forEach(function (b) { b.disabled = false; }); sync(); $("[data-cap-error]", el).textContent = e.message;
        });
      };
      if (btn) btn.addEventListener("click", function () { action("confirm"); });
      var dismiss = $("[data-cap-dismiss]", el); if (dismiss) dismiss.addEventListener("click", function () { action("dismiss"); });
      var filesButton = $("[data-cap-files]", el); if (filesButton) filesButton.addEventListener("click", function () { capFilesDialog(p).catch(fail); });
    });
  }
  // Keyed by card: only a card whose HTML changed is replaced, so ticked boxes and
  // chosen options on the other cards survive a refresh. An unchanged list comes
  // back as {unchanged} (the server answers from an index, no payloads).
  function capPaintProposals(box, sec, sid) {
    var ps = box._ps || [], now = Date.now(), next = 0, keep = {};
    ps.forEach(function (p, i) {
      var h = capProposalHtml(p), el = null, exp = p.status === "pending" && p.expires_at ? Date.parse(p.expires_at) : 0;
      if (exp > now && (!next || exp < next)) next = exp;
      $$("[data-cap-proposal]", box).forEach(function (x) { if (x.getAttribute("data-cap-proposal") === p.id) el = x; });
      keep[p.id] = 1;
      if (!el || el._capH !== h) {
        var tmp = document.createElement("div"); tmp.innerHTML = h;
        var fresh = tmp.firstChild; fresh._capH = h;
        capWireProposals(tmp, [p], function () { box._sig = ""; capRefreshSession(sec, sid, true); });
        if (el) box.replaceChild(fresh, el); else box.appendChild(fresh);
        el = fresh;
      }
      if (box.children[i] !== el) box.insertBefore(el, box.children[i] || null);
    });
    $$("[data-cap-proposal]", box).forEach(function (x) { if (!keep[x.getAttribute("data-cap-proposal")]) x.remove(); });
    // a pending card turns "expired" by the clock alone: repaint it then, no request
    clearTimeout(box._expT);
    if (next) box._expT = setTimeout(function () { if (document.contains(box)) capPaintProposals(box, sec, sid); }, Math.min(next - now + 50, 2147483000));
  }
  function capRefreshSession(sec, sid, force) {
    var box = $(".cap-session-proposals", sec);
    if (!box) { box = document.createElement("div"); box.className = "cap-session-proposals"; var form = $("#rc-form", sec); sec.insertBefore(box, form); }
    if (box._busy) { if (force) box._force = true; return; }
    if (!force && Date.now() - (box._last || 0) < 1200) return;
    box._busy = true; box._last = Date.now();
    api("/api/capabilities/proposals?session_id=" + enc(sid) + (box._sig ? "&since=" + enc(box._sig) : ""), { fresh: true }).then(function (r) {
      if (!document.contains(box)) return;
      if (r.unchanged && r.sig === box._sig) return;
      if (box._failed) { box.textContent = ""; box._failed = false; }
      box._ps = r.proposals || []; box._sig = r.sig || "";
      capPaintProposals(box, sec, sid);
    }).catch(function (e) { if (!e.auth && document.contains(box)) { clearTimeout(box._expT); box.textContent = T("cap.load_error"); box._failed = true; box._sig = ""; box._ps = []; } }).finally(function () { box._busy = false; if (box._force && document.contains(box)) { box._force = false; capRefreshSession(sec, sid, true); } });
  }
  // capInNewSession: a card needs a session to live in, so a blank one is made first. When
  // the request after it fails (already installed, bad source, offline, invalid bundle) the
  // session holds nothing and is dropped again, like a blank chat whose first send fails;
  // otherwise every failed click left an empty session in 会话.
  function capInNewSession(work) {
    return api("/api/chat-sessions", { method: "POST", body: { origin: "chat" } }).then(function (s) {
      return Promise.resolve(s).then(work).catch(function (e) {
        api("/api/chat-sessions/" + enc(s.id), { method: "DELETE" }).catch(function () {});
        throw e;
      });
    });
  }
  function capPropose(source) {
    return capInNewSession(function (s) {
      return api("/api/capabilities/proposals", { method: "POST", body: { session_id: s.id, source: source } }).then(function () { location.hash = "#/chat/" + enc(s.id); });
    });
  }
  function renderExtensions(tab, id) {
    var routeVersion = routeSeq;
    tab = ["installed", "discover", "tools", "sources", "plugins"].indexOf(tab) >= 0 ? tab : "installed";
    if (tab === "tools") return renderCapabilityStorage();
    if (tab === "plugins" || tab === "sources") {
      return (tab === "plugins" ? renderPlugins() : renderSkillRepositories(id, true)).then(function () {
        if (routeVersion !== routeSeq) return;
        if (tab === "sources" && !id) return capSourceManager(page());
      });
    }
    var fetch = tab === "discover" ? api("/api/capabilities/search?q=", { fresh: true }).then(function (r) { return r.results || []; }) : capList();
    if (tab === "tools") fetch = fetch.then(function (rows) { return Promise.all(rows.map(function (c) { return api("/api/capabilities/check", { method: "POST", body: { source: c.id } }).then(function (r) { c.lock.resolved = r.resolved || {}; return c; }); })); });
    return fetch.then(function (rows) {
      if (routeVersion !== routeSeq) return;
      var body = '<p id="cap-search-warnings" class="muted sm" role="status"></p><div id="cap-results"></div>';
      if (tab === "installed") body = '<label class="btn sm cap-import">' + esc(T("cap.import")) + '<input type="file" id="cap-import" accept=".json,application/json" class="sr-only"></label>' + body;
      if (tab === "discover") body = '<form id="cap-search" class="cap-search"><label class="sr-only" for="cap-query">' + esc(T("cap.search")) + '</label><input id="cap-query" maxlength="200" placeholder="' + esc(T("cap.search")) + '"><button class="btn" type="submit">' + esc(T("cap.search_btn")) + '</button><button class="btn ghost" type="button" id="cap-registry-search">' + esc(T("cap.search_registry")) + '</button><button class="btn ghost" type="button" id="cap-source-preview">' + esc(T("cap.source_preview")) + '</button><button class="btn ghost" type="button" id="cap-marketplace">' + esc(T("cap.marketplace")) + '</button><label class="cap-check"><input type="checkbox" id="cap-public-search"> '+esc(T('cap.public_search'))+'</label></form>' + body;
      mount('<div class="page-head"><div><h1>' + esc(T("nav.plugins")) + '</h1><p>' + esc(T("cap.subtitle")) + '</p></div></div>' + capTabs(tab) + body, "plugins", [[T("nav.plugins"), "#/plugins"], [T("cap.tab_" + tab)]]);
      var results = $("#cap-results"), searchSeq = 0;
      var paint = function (items) {
        if (!document.contains(results)) return;
        results.innerHTML = items.length ? items.map(function (c) {
          var l = capLock(c);
          if (tab === "discover") return '<section class="card cap-result"><div class="card-head"><h2>' + esc(c.title || c.id) + '</h2></div><p class="muted sm">' + esc(c.description || "") + '</p><p class="pill plain sm">'+esc(capText((c.source || {}).trust || 'community'))+'</p><p>' + esc(((c.source || {}).url) || c.url || "") + '</p><button type="button" class="btn sm" ' + ((c.labels||[]).indexOf('repository_choose_skill')>=0 ? 'data-cap-repository="'+esc((c.source||{}).url||c.url||'')+'"' : 'data-cap-propose="'+esc(c.id)+'"') + '>' + esc(T((c.labels||[]).indexOf('repository_choose_skill')>=0?'cap.choose_skill':'cap.preview')) + '</button></section>';
          if (tab === "tools") {
            var rs = l.resolved || {};
            return '<section class="card cap-result"><h2>' + esc(l.title || l.id) + '</h2>' + (Object.keys(rs).length ? Object.keys(rs).map(function (n) { var d = rs[n]; return '<p><strong>' + esc(n) + '</strong> · ' + esc(capText(d.status) + ' · ' + (d.version || '') + ' · ' + (d.source || '')) + '<br><span class="muted sm">' + esc(d.path || d.note || '') + '</span></p>'; }).join('') : '<p class="muted">' + esc(T("cap.none")) + '</p>') + '</section>';
          }
          return '<section class="card cap-result"><div class="card-head"><h2>' + esc(l.title || l.id) + '</h2></div>' + (c.locally_modified ? '<span class="pill warn">'+esc(T('cap.locally_modified'))+'</span>' : '') + capSummary(l) + '<div class="actions">' + (((l.requires || {}).secrets || []).length ? '<button class="btn sm" data-cap-secrets="' + esc(c.id) + '">' + esc(T("cap.credentials")) + '</button>' : '') + ((l.source || {}).publisher === 'DingTalk-Real-AI' ? '<button class="btn sm" data-cap-login="' + esc(c.id) + '">' + esc(T("cap.cli_login")) + '</button>' : '') + (l.components || []).filter(capCanConnect).map(function (component) { return '<button type="button" class="btn sm" data-cap-oauth="' + esc(c.id) + '" data-cap-component="' + esc(component.name) + '">' + esc(T("cap.connect")) + ' ' + esc(component.name) + '</button>'; }).join('') + '<button type="button" class="btn sm ghost" data-cap-test="' + esc(c.id) + '">' + esc(T("cap.test")) + '</button><button type="button" class="btn sm" data-cap-bind="' + esc(c.id) + '">' + esc(T("cap.bindings")) + '</button><button type="button" class="btn sm ghost" data-cap-manage="' + esc(c.id) + '">' + esc(T("cap.manage")) + '</button></div></section>';
        }).join("") : '<p class="muted cap-empty">' + esc(T(tab === "discover" ? "cap.no_results" : "cap.no_installed")) + '</p>';
        $$("[data-cap-secrets]",results).forEach(function(b){b.addEventListener("click",function(){capSecretsDialog(b.getAttribute("data-cap-secrets")).catch(fail);});});
        $$("[data-cap-login]",results).forEach(function(b){b.addEventListener("click",function(){capDeviceLoginDialog(b.getAttribute("data-cap-login"));});});
        $$("[data-cap-test]", results).forEach(function(b){b.addEventListener("click",function(){b.disabled=true;api("/api/capabilities/test",{method:"POST",body:{source:b.getAttribute("data-cap-test")}}).then(function(v){modal('<div class="card-head"><h2>'+esc(T("cap.test"))+'</h2></div><pre class="cap-file">'+esc(JSON.stringify(v,null,2))+'</pre><div class="actions"><button class="btn" data-close>'+esc(T("btn.close"))+'</button></div>',function(){});}).catch(fail).finally(function(){b.disabled=false;});});});
        $$("[data-cap-oauth]", results).forEach(function (b) { b.addEventListener("click", function () { capOAuthDialog(b.getAttribute("data-cap-oauth"), b.getAttribute("data-cap-component")).catch(fail); }); });
        $$("[data-cap-manage]", results).forEach(function (b) { b.addEventListener("click", function () { capManageDialog(b.getAttribute("data-cap-manage")).catch(fail); }); });
        $$("[data-cap-bind]", results).forEach(function (b) { b.addEventListener("click", function () { capBindingsDialog(b.getAttribute("data-cap-bind")).catch(fail); }); });
        $$("[data-cap-repository]",results).forEach(function(b){b.addEventListener("click",function(){b.disabled=true;capChooseSourceSkills(b.getAttribute("data-cap-repository")).catch(fail).finally(function(){b.disabled=false;});});});
        $$("[data-cap-propose]", results).forEach(function (b) { b.addEventListener("click", function () { b.disabled = true; capPropose(b.getAttribute("data-cap-propose")).catch(function (e) { b.disabled = false; fail(e); }); }); });
      };
      paint(rows); if(tab === "installed") capLoadLegacyInventory(results);
      if (tab === "installed") $("#cap-import").addEventListener("change", function (e) { capImportFile(e.target.files[0]).catch(fail); });
      if (tab === "discover") $("#cap-source-preview").addEventListener("click",function(e){var b=e.currentTarget,source=$("#cap-query").value.trim();if(!source)return;b.disabled=true;capPropose(source).catch(fail).finally(function(){b.disabled=false;});});
      if (tab === "discover") $("#cap-marketplace").addEventListener("click",function(e){var b=e.currentTarget,n=++searchSeq,source=$("#cap-query").value.trim();if(!source)return;b.disabled=true;api("/api/capabilities/marketplace",{method:"POST",body:{source:source}}).then(function(r){if(n===searchSeq)paint((r.entries||[]).map(function(entry){return {id:entry.source,title:entry.name,description:[entry.trust,entry.commit||entry.ref,entry.path].filter(Boolean).join(' · '),url:entry.repository};}));}).catch(fail).finally(function(){b.disabled=false;});});
      if (tab === "discover") $("#cap-registry-search").addEventListener("click", function(e){var b=e.currentTarget,n=++searchSeq;b.disabled=true;api("/api/capabilities/registry/search?q="+enc($("#cap-query").value),{fresh:true}).then(function(r){if(n===searchSeq)paint(r.results||[]);}).catch(fail).finally(function(){b.disabled=false;});});
      if (tab === "discover") $("#cap-search").addEventListener("submit", function (e) {
        e.preventDefault(); var n = ++searchSeq;
        api("/api/capabilities/search?q=" + enc($("#cap-query").value) + ($("#cap-public-search").checked ? "&providers=skills.sh,github" : ""), { fresh: true }).then(function (r) { if (n === searchSeq) {paint(r.results || []);$("#cap-search-warnings").textContent=(r.warnings || []).join(" · ");} }).catch(fail);
      });
    });
  }
  function capBindingsDialog(capID) {
    return Promise.all([api("/api/capability-bindings/targets", { fresh: true }), api("/api/capability-bindings?capability_id=" + enc(capID), { fresh: true })]).then(function (data) {
      var targets = data[0] || [], bindings = data[1] || [];
      modal('<div class="card-head"><h2>' + esc(T("cap.bindings")) + '</h2></div><p class="muted sm">' + esc(T("cap.bindings_note")) + '</p><div id="cap-bound">' + bindings.map(function (b, i) {
        var target = targets.filter(function (t) { return t.kind === b.owner_kind && t.id === b.owner_id; })[0];
        return '<div class="cap-bound-row"><span>' + esc((target && target.name) || b.owner_id) + ' · ' + esc(capText(b.owner_kind)) + '</span><button type="button" class="btn sm ghost" data-cap-unbind="' + i + '">' + esc(T("cap.unbind")) + '</button></div>';
      }).join("") + '</div><label class="field"><span>' + esc(T("cap.bind_target")) + '</span><select id="cap-bind-target"><option value="">' + esc(T("cap.choose_target")) + '</option>' + targets.map(function (t, i) { return '<option value="' + i + '">' + esc(t.name + ' · ' + capText(t.kind)) + '</option>'; }).join("") + '</select></label><p class="field-err" id="cap-bind-error" role="alert"></p><div class="actions"><button class="btn ghost" type="button" data-close>' + esc(T("btn.close")) + '</button><button class="btn primary" type="button" id="cap-bind-save">' + esc(T("cap.bind")) + '</button></div>', function (root, close) {
        $("#cap-bind-save", root).addEventListener("click", function (e) {
          var value = $("#cap-bind-target", root).value, target = value !== "" && targets[Number(value)]; if (!target) return;
          var b = e.currentTarget; b.disabled = true;
          api("/api/capability-bindings", { method: "PUT", body: { owner_kind: target.kind, owner_id: target.id, capability_id: capID } }).then(function () { close(); toast(T("cap.bound")); }).catch(function (err) { b.disabled = false; $("#cap-bind-error", root).textContent = err.message; });
        });
        $$("[data-cap-unbind]", root).forEach(function (b) { b.addEventListener("click", function () {
          b.disabled = true; var value = bindings[Number(b.getAttribute("data-cap-unbind"))];
          api("/api/capability-bindings", { method: "DELETE", body: value }).then(function () { b.closest(".cap-bound-row").remove(); toast(T("cap.unbound")); }).catch(function (err) { b.disabled = false; $("#cap-bind-error", root).textContent = err.message; });
        }); });
      });
    });
  }
  function capManageDialog(capID) {
    return Promise.all([api("/api/capabilities/" + enc(capID) + "/versions", { fresh: true }), api("/api/capabilities/" + enc(capID) + "/events", { fresh: true }), api("/api/capabilities/defaults", { fresh: true })]).then(function (r) {
      var defaults = Array.isArray(r[2]) ? r[2] : [];
      var versions = Array.isArray(r[0]) ? r[0] : r[0].versions || [], events = Array.isArray(r[1]) ? r[1] : r[1].events || [];
      modal('<div class="card-head"><h2>' + esc(T("cap.manage")) + '</h2></div><label class="cap-check"><input type="checkbox" id="cap-default"' + (defaults.indexOf(capID) >= 0 ? ' checked' : '') + '> ' + esc(T("cap.default_new")) + '</label><p class="muted sm">' + esc(T("cap.default_note")) + '</p><h3>' + esc(T("cap.versions")) + '</h3><div>' + versions.map(function (v) {
        return '<div class="cap-bound-row"><span>' + esc(v.version_id) + '<small>' + esc(((v.lock || {}).source || {}).commit || '') + '</small></span><button class="btn sm ghost" type="button" data-cap-rollback="' + esc(v.version_id) + '">' + esc(T("cap.rollback")) + '</button></div>';
      }).join('') + '</div><details><summary>' + esc(T("cap.audit")) + '</summary><ol class="cap-findings">' + events.map(function (e) { return '<li>' + esc([e.created_at, e.actor, e.action, e.result].join(' · ')) + '</li>'; }).join('') + '</ol></details><p class="field-err" id="cap-manage-error" role="alert"></p><div class="actions"><a class="btn sm" href="/api/capabilities/' + enc(capID) + '/export" download>' + esc(T("cap.export")) + '</a><button class="btn sm" type="button" id="cap-rescan">' + esc(T("cap.rescan")) + '</button><button class="btn sm" type="button" id="cap-update">' + esc(T("cap.update")) + '</button><button class="btn sm" type="button" id="cap-smoke">' + esc(T("cap.smoke")) + '</button><button class="btn sm" type="button" id="cap-deactivate">' + esc(T("cap.deactivate")) + '</button><button class="btn sm" type="button" id="cap-clean-snapshots">' + esc(T("cap.clean_snapshots")) + '</button><button class="btn sm danger" type="button" id="cap-clear-credentials">' + esc(T("cap.clear_credentials")) + '</button><button class="btn sm danger" type="button" id="cap-uninstall">' + esc(T("cap.uninstall")) + '</button><button class="btn ghost" type="button" data-close>' + esc(T("btn.close")) + '</button></div>', function (root, close) {
        var error = function (e) { var el = $("#cap-manage-error", root); if (el) el.textContent = e.message; else toast(e.message, true); };
        $$("[data-cap-rollback]", root).forEach(function (b) { b.addEventListener("click", function () {
          confirmDialog({ title: T("cap.rollback"), body: T("cap.rollback_note"), ok: T("cap.rollback") }).then(function (yes) { if (!yes) return; b.disabled = true;
            return api("/api/capabilities/" + enc(capID) + "/rollback", { method: "POST", body: { version_id: b.getAttribute("data-cap-rollback") } }).then(function () { close(); refresh(); });
          }).catch(function (e) { b.disabled = false; error(e); });
        }); });
        $("#cap-rescan", root).addEventListener("click", function (e) { var b=e.currentTarget;b.disabled=true;
          capInNewSession(function(session){return api("/api/capabilities/"+enc(capID)+"/rescan",{method:"POST",body:{session_id:session.id}}).then(function(){close();location.hash="#/chat/"+enc(session.id);});}).catch(error).finally(function(){b.disabled=false;});
        });
        $("#cap-update", root).addEventListener("click", function (e) { var b = e.currentTarget; b.disabled = true;
          capInNewSession(function (session) {
            return api("/api/capabilities/" + enc(capID) + "/update", { method: "POST", body: { session_id: session.id } }).then(function () { close(); location.hash = "#/chat/" + enc(session.id); });
          }).catch(function (e) { b.disabled = false; error(e); });
        });
        $("#cap-default", root).addEventListener("change", function (e) { var b=e.currentTarget,value=b.checked;b.disabled=true;
          api("/api/capabilities/"+enc(capID)+"/default", {method:"POST",body:{default_enabled:value}}).catch(function(err){b.checked=!value;error(err);}).finally(function(){b.disabled=false;});
        });
        $("#cap-deactivate", root).addEventListener("click", function(e){var b=e.currentTarget;
          confirmDialog({title:T("cap.deactivate"),body:T("cap.deactivate_note"),ok:T("cap.deactivate")}).then(function(yes){if(!yes)return;b.disabled=true;return api("/api/capabilities/"+enc(capID)+"/deactivate",{method:"POST",body:{reviewed:true}}).then(function(){close();refresh();});}).catch(error).finally(function(){b.disabled=false;});
        });
        $("#cap-smoke", root).addEventListener("click", function(e){var b=e.currentTarget;b.disabled=true;
          capInNewSession(function(session){return api("/api/chat-sessions/"+enc(session.id)+"/bindings",{method:"PUT",body:{skills:[],capabilities:[capID]}}).then(function(){draftSave("cs_"+session.id,T("cap.smoke_prompt"));draftFlush();close();location.hash="#/chat/"+enc(session.id);toast(T("cap.smoke_note"));});}).catch(error).finally(function(){b.disabled=false;});
        });
        $("#cap-clean-snapshots", root).addEventListener("click", function (e) { var b = e.currentTarget;
          confirmDialog({ title: T("cap.clean_snapshots"), body: T("cap.clean_snapshots_note"), ok: T("cap.clean_snapshots") }).then(function (yes) { if (!yes) return; b.disabled = true;
            return api("/api/capabilities/" + enc(capID) + "/gc", { method: "POST", body: { reviewed: true } }).then(function () { close(); refresh(); });
          }).catch(error).finally(function () { b.disabled = false; });
        });
        $("#cap-clear-credentials", root).addEventListener("click", function (e) { var b = e.currentTarget;
          confirmDialog({ title: T("cap.clear_credentials"), body: T("cap.clear_credentials_note"), ok: T("cap.clear_credentials"), danger: true }).then(function (yes) { if (!yes) return; b.disabled = true;
            return api("/api/capabilities/" + enc(capID) + "/credentials_clear", { method: "POST", body: { reviewed: true } }).then(function () { toast(T("cap.credentials_cleared")); });
          }).catch(error).finally(function () { b.disabled = false; });
        });
        $("#cap-uninstall", root).addEventListener("click", function (e) { var b = e.currentTarget;
          confirmDialog({ title: T("cap.uninstall"), body: T("cap.uninstall_note"), ok: T("cap.uninstall"), danger: true }).then(function (yes) { if (!yes) return; b.disabled = true;
            return api("/api/capabilities/" + enc(capID) + "/uninstall", { method: "POST", body: {} }).then(function (report) { close(); refresh(); capUninstallReport(report); });
          }).catch(function (e) { b.disabled = false; error(e); });
        });
      });
    });
  }
  function capImportFile(file) {
    if (!file || file.size > 90 * 1024 * 1024) return Promise.reject(new Error(T("cap.import_size")));
    return file.text().then(function (text) { var bundle = JSON.parse(text); return capInNewSession(function (s) {
      return api("/api/capabilities/import", { method: "POST", body: { session_id: s.id, bundle: bundle } }).then(function () { location.hash = "#/chat/" + enc(s.id); });
    }); });
  }
  function capOAuthDialog(capID, component) {
    var base = "/api/capabilities/" + enc(capID) + "/oauth/";
    return api(base + "prepare", { method: "POST", body: { component: component } }).then(function (p) {
      modal('<div class="card-head"><h2>' + esc(T("cap.connect")) + '</h2></div><dl class="cap-meta"><div><dt>' + esc(T("cap.resource")) + '</dt><dd>' + esc(p.resource) + '</dd></div><div><dt>' + esc(T("cap.issuer")) + '</dt><dd>' + esc(p.issuer) + '</dd></div></dl><p>' + esc(T("cap.oauth_note")) + '</p><div>' + (p.scopes || []).map(function (scope) { return '<label class="cap-check"><input type="checkbox" data-cap-scope value="' + esc(scope) + '" checked> ' + esc(scope) + '</label>'; }).join('') + '</div><label class="field"><span>' + esc(T("cap.client_id")) + '</span><input id="cap-client-id" value="' + esc(p.client_id || '') + '" autocomplete="off"></label>' + (p.registration_endpoint ? '<label class="cap-check"><input type="checkbox" id="cap-register"> ' + esc(T("cap.register_client")) + '</label><p class="muted sm">' + esc(p.registration_endpoint) + '</p>' : '') + '<div id="cap-oauth-link"></div><p class="field-err" id="cap-oauth-error" role="alert"></p><div class="actions"><button class="btn ghost" type="button" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary" type="button" id="cap-oauth-start">' + esc(T("cap.authorize")) + '</button></div>', function (root) {
        $("#cap-oauth-start", root).addEventListener("click", function (e) {
          var b = e.currentTarget, reg = $("#cap-register", root), scopes = $$("[data-cap-scope]", root).filter(function (x) { return x.checked; }).map(function (x) { return x.value; }); b.disabled = true;
          api(base + "start", { method: "POST", body: { preparation_id: p.preparation_id, client_id: $("#cap-client-id", root).value.trim(), register_client: !!(reg && reg.checked), scopes: scopes } }).then(function (v) {
            var url = new URL(v.authorization_url); if (url.protocol !== "https:") throw new Error(T("cap.invalid_auth_url"));
            $("#cap-oauth-link", root).innerHTML = '<a class="btn primary" target="_blank" rel="noopener noreferrer" href="' + esc(url.href) + '">' + esc(T("cap.open_auth")) + '</a><p class="muted sm">' + esc(T("cap.auth_return")) + '</p>';
          }).catch(function (err) { b.disabled = false; var el = $("#cap-oauth-error", root); if (el) el.textContent = err.message; });
        });
      });
    });
  }
  function capSecretsDialog(id) {
    return api("/api/capability-secrets?capability_id="+enc(id),{fresh:true}).then(function(fields){
      modal('<div class="card-head"><h2>'+esc(T("cap.credentials"))+'</h2></div><p class="muted sm">'+esc(T("cap.credentials_note"))+'</p>'+fields.map(function(f,i){return '<label class="field"><span>'+esc(f.name)+(f.set?' · '+esc(T("cap.secret_set")):'')+'</span><input type="password" data-cap-secret="'+i+'" autocomplete="new-password" maxlength="16384"></label>';}).join('')+'<p class="field-err" id="cap-secret-error"></p><div class="actions"><button class="btn ghost" data-close>'+esc(T("btn.cancel"))+'</button><button class="btn primary" id="cap-secret-save">'+esc(T("btn.save"))+'</button></div>',function(root,close){
        $("#cap-secret-save",root).addEventListener("click",function(e){var b=e.currentTarget,inputs=$$("[data-cap-secret]",root).filter(function(x){return x.value!=="";});b.disabled=true;
          inputs.reduce(function(p,x){return p.then(function(){return api("/api/capability-secrets",{method:"POST",body:{capability_id:id,name:fields[Number(x.getAttribute("data-cap-secret"))].name,value:x.value}}).then(function(){x.value="";});});},Promise.resolve()).then(function(){close();toast(T("cap.credentials_saved"));}).catch(function(err){b.disabled=false;$("#cap-secret-error",root).textContent=err.message;});
        });
      });
    });
  }
  function capGrantsHtml(groups) {
    return '<div class="card-head"><span class="ico">' + ic("box") + '</span><h2>' + esc(T("cap.external_tools")) + '</h2></div>' +
      '<div class="cap-grant-body"><p class="cap-grant-intro" id="cap-grant-intro">' + esc(T("cap.grant_purpose")) + '</p>' +
      '<aside class="cap-grant-warning"><strong>' + esc(T("cap.grant_review")) + '</strong><p>' + esc(T("cap.external_warning")) + '</p></aside>' +
      (groups.length ? '<p class="muted sm">' + esc(T("cap.grant_select")) + '</p>' + groups.map(function (g, i) {
        return '<section class="cap-grant-group" data-cap-grant="' + i + '" aria-labelledby="cap-grant-title-' + i + '"><h3 id="cap-grant-title-' + i + '">' + esc(g.component) + '</h3>' +
          '<details class="cap-grant-version"><summary>' + esc(T("cap.grant_version")) + '</summary><p>' + esc(g.capability_id + ' · ' + g.version_id) + '</p></details>' +
          '<div class="cap-grant-tools">' + (g.tools || []).map(function (tool, j) {
            var description = 'cap-tool-description-' + i + '-' + j;
            return '<label class="cap-grant-tool"><input type="checkbox" data-cap-tool value="' + esc(tool.name) + '" aria-describedby="' + description + '"' + ((g.granted_tools || []).indexOf(tool.name) >= 0 ? ' checked' : '') + '><span><strong>' + esc(tool.name) + '</strong><span class="cap-grant-description" id="' + description + '">' + esc(tool.description || T("cap.grant_no_description")) + '</span></span></label>';
          }).join('') + '</div><div class="actions"><button type="button" class="btn sm primary" data-cap-grant-save>' + esc(T("cap.save_grant")) + '</button><button type="button" class="btn sm ghost" data-cap-grant-revoke>' + esc(T("cap.revoke_grant")) + '</button></div><p class="field-err" data-cap-grant-error role="alert"></p></section>';
      }).join('') : '<section class="cap-grant-empty"><h3>' + esc(T("cap.grant_empty_title")) + '</h3><p>' + esc(T("cap.grant_optional")) + '</p><ol><li>' + esc(T("cap.grant_step_bind")) + '</li><li>' + esc(T("cap.grant_step_test")) + '</li><li>' + esc(T("cap.grant_step_select")) + '</li></ol><button type="button" class="btn" data-cap-open-extensions>' + esc(T("cap.grant_open_extensions")) + '</button></section>') +
      '</div><div class="actions"><button type="button" class="btn" data-close>' + esc(T("btn.close")) + '</button></div>';
  }
  function capGrantsDialog(sid) {
    return api("/api/capability-grants?session_id=" + enc(sid), { fresh: true }).then(function (groups) {
      modal(capGrantsHtml(groups), function (root, close) {
        var box = $(".modal", root), open = $("[data-cap-open-extensions]", root);
        box.classList.add("cap-grants-modal"); box.setAttribute("aria-describedby", "cap-grant-intro");
        if (open) open.addEventListener("click", function () { close(); location.hash = "#/plugins/installed"; });
        $$("[data-cap-grant]", root).forEach(function (section) {
          var g = groups[Number(section.getAttribute("data-cap-grant"))], busy = false;
          var write = function (revoke) {
            if (busy) return;
            busy = true; section.setAttribute("aria-busy", "true");
            $("[data-cap-grant-error]", section).textContent = "";
            $$("button,input", section).forEach(function (b) { b.disabled = true; });
            var body = { session_id: sid, capability_id: g.capability_id, version_id: g.version_id, component: g.component, discovery_hash: g.discovery_hash, tools: $$("[data-cap-tool]", section).filter(function (t) { return !revoke && t.checked; }).map(function (t) { return t.value; }) };
            api("/api/capability-grants", { method: revoke ? "DELETE" : "PUT", body: body }).then(function () {
              toast(T("cap.grants_saved"));
              if (revoke) $$("[data-cap-tool]", section).forEach(function (t) { t.checked = false; });
            }).catch(function (e) { $("[data-cap-grant-error]", section).textContent = e.message; }).finally(function () {
              busy = false; section.removeAttribute("aria-busy"); $$("button,input", section).forEach(function (b) { b.disabled = false; });
            });
          };
          $("[data-cap-grant-save]", section).addEventListener("click", function () { write(false); });
          $("[data-cap-grant-revoke]", section).addEventListener("click", function () { write(true); });
        });
      });
    });
  }
  function capLoginLabel(status) {
    if (status === "starting") return T("cap.login_starting");
    if (status === "pending") return T("cap.login_pending");
    if (status === "cancelled") return T("cap.login_cancelled");
    if (status === "failed") return T("cap.login_failed");
    if (status === "completed") return T("cap.login_completed");
    return T("cap.login_idle");
  }
  // Device login: one poll chain, Start works again after fail/cancel, stale replies are dropped.
  function capBindDeviceLogin(root, base) {
    var output = $("#cap-device-state", root);
    var startBtn = $("#cap-device-start", root);
    var cancelBtn = $("#cap-device-cancel", root);
    var errEl = $("#cap-device-error", root);
    var timer = null, reqId = 0, closed = false;
    function stopPoll() { clearTimeout(timer); timer = null; }
    function nextReq() { stopPoll(); return ++reqId; }
    function alive(id) { return !closed && id === reqId && document.contains(output); }
    function failLogin(e) { if (errEl) errEl.textContent = e.message; }
    function syncButtons(status) {
      var busy = status === "starting" || status === "pending";
      if (startBtn) startBtn.disabled = busy;
      if (cancelBtn) cancelBtn.disabled = !busy;
    }
    function paint(v) {
      if (!document.contains(output)) return;
      var status = v.status || "idle";
      output.textContent = capLoginLabel(status) + (v.user_code ? " · " + v.user_code : "");
      var note = status === "failed" ? T("cap.login_failed_note") : status === "completed" ? T("cap.login_completed_note") : "";
      if (note) {
        var p = document.createElement("p");
        p.className = "muted sm";
        p.textContent = note;
        output.appendChild(p);
      }
      if (v.verification_uri) {
        try {
          var u = new URL(v.verification_uri);
          if (u.protocol === "https:") {
            var a = document.createElement("a");
            a.href = u.href; a.target = "_blank"; a.rel = "noopener noreferrer"; a.className = "btn sm";
            a.textContent = T("cap.open_auth");
            output.appendChild(a);
          }
        } catch (ignore) {}
      }
      syncButtons(status);
      if (status === "starting" || status === "pending") {
        stopPoll();
        timer = setTimeout(poll, 1500);
      } else stopPoll();
    }
    function poll() {
      if (closed || !document.contains(output)) return;
      var id = nextReq();
      api(base, { fresh: true }).then(function (v) { if (alive(id)) paint(v); }).catch(function (e) { if (alive(id)) failLogin(e); });
    }
    if (startBtn) startBtn.addEventListener("click", function () {
      if (closed) return;
      startBtn.disabled = true;
      if (errEl) errEl.textContent = "";
      var id = nextReq();
      api(base, { method: "POST", body: { reviewed: true } }).then(function (v) {
        if (alive(id)) paint(v);
      }).catch(function (e) {
        if (!alive(id)) return;
        failLogin(e);
        syncButtons("idle");
        poll();
      });
    });
    if (cancelBtn) cancelBtn.addEventListener("click", function () {
      if (closed) return;
      if (errEl) errEl.textContent = "";
      var id = nextReq();
      api(base + "/cancel", { method: "POST", body: {} }).then(function (v) {
        if (alive(id)) paint(v);
      }).catch(function (e) {
        if (!alive(id)) return;
        failLogin(e);
        syncButtons("idle");
        poll();
      });
    });
    poll();
    return function () { closed = true; reqId++; stopPoll(); };
  }
  function capDeviceLoginDialog(id) {
    var stop = function () {};
    modal('<div class="card-head"><h2>' + esc(T("cap.cli_login")) + '</h2></div><p>' + esc(T("cap.cli_login_note")) + '</p><div id="cap-device-state"></div><p class="field-err" id="cap-device-error" role="alert"></p><div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.close")) + '</button><button type="button" class="btn ghost" id="cap-device-cancel">' + esc(T("cap.cancel_login")) + '</button><button type="button" class="btn primary" id="cap-device-start">' + esc(T("cap.start_login")) + '</button></div>', function (root) {
      stop = capBindDeviceLogin(root, "/api/capabilities/" + enc(id) + "/login");
    }, function () { stop(); });
  }

  function capStorageBytes(n) { return (Number(n || 0) / (1024 * 1024)).toFixed(1) + " MiB"; }
  function renderCapabilityStorage() {
    var seq = routeSeq;
    return api("/api/capabilities/storage", { fresh: true }).then(function (s) {
      if (seq !== routeSeq) return;
      var usage = s.usage || {}, limits = s.limits || {}, envs = usage.environments || {};
      var envBytes = Object.keys(envs).reduce(function (sum, key) { return sum + Number(envs[key] || 0); }, 0);
      mount(capTabs("tools") + '<div class="page-head"><div><h1>' + esc(T("cap.tab_tools")) + '</h1><p>' + esc(T("cap.subtitle")) + '</p></div><div class="actions"><button type="button" class="btn" id="cap-dependency-preferences">'+esc(T('cap.dependency_preferences'))+'</button></div></div>' +
        '<section class="card cap-result"><h2>' + esc(T("cap.storage")) + '</h2><dl class="cap-meta"><div><dt>' + esc(T("cap.managed_tools")) + '</dt><dd>' + esc(capStorageBytes(usage.tools_and_runtimes) + " / " + capStorageBytes(limits.tools_and_runtimes)) + '</dd></div><div><dt>' + esc(T("cap.cache")) + '</dt><dd>' + esc(capStorageBytes(usage.cache) + " / " + capStorageBytes(limits.cache)) + '</dd></div><div><dt>' + esc(T("cap.environments")) + '</dt><dd>' + esc(capStorageBytes(envBytes)) + '</dd></div><div><dt>' + esc(T("cap.snapshots")) + '</dt><dd>' + esc(capStorageBytes(s.snapshot_bytes)) + '</dd></div></dl>' +
        ((s.warnings || []).length ? '<p class="field-err">' + esc(T("cap.quota_warning")) + '</p>' : '') + '<p class="muted sm">' + esc(T("cap.gc_note")) + '</p><button type="button" class="btn" id="cap-storage-gc">' + esc(T("cap.clean_storage")) + '</button><p id="cap-storage-result" role="status"></p></section>' +
        (s.tools || []).map(function (tool) { return '<section class="card cap-result"><h3>' + esc(tool.name) + '</h3><p>' + esc([tool.source, tool.version, capStorageBytes(tool.bytes)].filter(Boolean).join(' · ')) + '</p><p class="muted sm">' + esc(tool.path || '') + '</p></section>'; }).join(''), "plugins", [[T("nav.plugins"), "#/plugins"], [T("cap.tab_tools")]]);
      $("#cap-dependency-preferences").addEventListener("click",function(){capDependencyPreferences().catch(fail);});
      $("#cap-storage-gc").addEventListener("click", function (e) { var b = e.currentTarget, result = $("#cap-storage-result");
        confirmDialog({ title: T("cap.clean_storage"), body: T("cap.gc_note"), ok: T("cap.clean_storage") }).then(function (yes) { if (!yes) return; b.disabled = true;
          return api("/api/capabilities/storage/gc", { method: "POST", body: { reviewed: true } }).then(function (r) { if (document.contains(result)) result.textContent = T("cap.storage_freed") + ' ' + capStorageBytes(r.freed_bytes); });
        }).catch(function (err) { if (document.contains(result)) result.textContent = err.message; }).finally(function () { b.disabled = false; });
      });
    });
  }

  function capAutoTest(id) {
    return api("/api/capabilities/test", { method: "POST", body: { source: id } }).then(function (r) {
      var deps = r.dependencies || {};
      var needsAttention = (r.cli_auth || []).some(function (m) { return m.status !== "ready"; }) || (r.mcp || []).some(function (m) { return m.status !== "ready"; }) || Object.keys(deps).some(function (key) { return deps[key].status !== "ready"; });
      toast(T(needsAttention ? "cap.auto_test_attention" : "cap.auto_test_ready"));
    }).catch(function () { toast(T("cap.auto_test_attention"), true); });
  }

  function capLoadSkillBadges(root) {
    if (!root) return;
    capList().then(function(caps){
      if (!document.contains(root)) return;
      var bySkill={};caps.forEach(function(c){(capLock(c).components||[]).forEach(function(component){if(component.skill_id)bySkill[component.skill_id]=c;});});
      $$("[data-cap-skill]",root).forEach(function(el){var c=bySkill[el.getAttribute("data-cap-skill")];if(!c)return;var b=document.createElement("button");b.type="button";b.className="btn sm ghost";b.textContent=T("cap.from")+" "+(c.title||c.id);b.addEventListener("click",function(e){e.stopPropagation();capManageDialog(c.id).catch(fail);});el.appendChild(b);});
    }).catch(function(){});
  }

  function capLoadRunBindings(id, root) {
    if (!root) return;
    api("/api/capabilities/run-bindings?run_id="+enc(id),{fresh:true}).then(function(rows){
      if (!document.contains(root) || !rows.length) return;
      var section=document.createElement("section");
      section.innerHTML='<h3>'+esc(T("cap.run_versions"))+'</h3><ul class="cap-findings">'+rows.map(function(row){return '<li>'+esc(row.title||row.id)+'<br><small>'+esc(row.version_id)+' · '+esc(row.source_version||'')+'</small></li>';}).join('')+'</ul>';
      root.appendChild(section);
    }).catch(function(){});
  }

  function capUninstallReport(report) {
    modal('<div class="card-head"><h2>'+esc(T("cap.uninstall"))+'</h2></div><p>'+esc(T("cap.removed_skills"))+' '+esc((report.removed_skills||[]).length)+'</p><ul class="cap-findings">'+(report.retained||[]).map(function(item){return '<li>'+esc(item.kind+' · '+item.reason)+'</li>';}).join('')+'</ul><div class="actions"><button class="btn" data-close>'+esc(T("btn.close"))+'</button></div>',function(){});
  }

  function capLoadLegacyInventory(root) {
    rpc("plugins.list").then(function(rows){
      if(!document.contains(root)||!rows.length)return;
      root.insertAdjacentHTML("beforeend",'<section class="card cap-result"><h2>'+esc(T('cap.legacy_plugins'))+'</h2><p class="muted sm">'+esc(T('cap.legacy_note'))+'</p>'+(rows||[]).map(function(p){return '<p><strong>'+esc(p.name||p.id)+'</strong> · '+esc((p.current||{}).version||'')+' · '+esc(T(p.enabled?'pl.enabled':'cap.disabled'))+'</p>';}).join('')+'<a class="btn sm" href="#/plugins/plugins">'+esc(T('cap.manage'))+'</a></section>');
    }).catch(function(e){if(document.contains(root)){var p=document.createElement('p');p.className='field-err';p.textContent=e.message;root.appendChild(p);}});
  }
  function capSourceManager(root) {
    if(!root)return Promise.resolve();
    var box=document.createElement('section');box.className='card cap-result';root.appendChild(box);
    box.innerHTML='<h2>'+esc(T('cap.saved_sources'))+'</h2><p class="muted sm">'+esc(T('cap.sources_note'))+'</p><form id="cap-add-source"><label class="field"><span>'+esc(T('cap.source_name'))+'</span><input name="name" required maxlength="120"></label><label class="field"><span>'+esc(T('cap.source_kind'))+'</span><select name="source_kind"><option value="marketplace">'+esc(T('cap.marketplace'))+'</option><option value="mcp_registry">MCP Registry</option></select></label><label class="field"><span>HTTPS URL</span><input name="source" type="url" required maxlength="2000"></label><button class="btn" type="submit">'+esc(T('cap.save_source'))+'</button></form><p data-source-error class="field-err" role="alert"></p><div data-source-list></div>';
    var list=$('[data-source-list]',box),err=$('[data-source-error]',box);
    function refresh(){return api('/api/capabilities/sources',{fresh:true}).then(function(r){if(!document.contains(box))return;list.innerHTML=(r.sources||[]).map(function(x){return '<section><h3>'+esc(x.name||x.id)+'</h3><p>'+esc(x.source_kind+' · '+x.source)+'</p><p class="muted sm">'+esc(x.refreshed_at||'')+'</p>'+(x.read_only?'':'<button class="btn sm" data-source-refresh="'+esc(x.id)+'">'+esc(T('cap.refresh_source'))+'</button><button class="btn sm danger" data-source-delete="'+esc(x.id)+'">'+esc(T('cap.remove_source'))+'</button>')+'</section>';}).join('');
      $$('[data-source-refresh]',list).forEach(function(b){b.addEventListener('click',function(){b.disabled=true;api('/api/capabilities/sources/'+enc(b.getAttribute('data-source-refresh'))+'/refresh',{method:'POST',body:{}}).then(refresh).catch(function(e){err.textContent=e.message;b.disabled=false;});});});
      $$('[data-source-delete]',list).forEach(function(b){b.addEventListener('click',function(){confirmDialog({title:T('cap.remove_source'),body:T('cap.remove_source_note'),ok:T('cap.remove_source'),danger:true}).then(function(yes){if(!yes)return;b.disabled=true;return api('/api/capabilities/sources/'+enc(b.getAttribute('data-source-delete'))+'/delete',{method:'POST',body:{}}).then(refresh);}).catch(function(e){err.textContent=e.message;b.disabled=false;});});});
    });}
    $('#cap-add-source',box).addEventListener('submit',function(e){e.preventDefault();var form=e.currentTarget,b=$('button',form);b.disabled=true;err.textContent='';var data={name:form.elements.name.value,source_kind:form.elements.source_kind.value,source:form.elements.source.value};api('/api/capabilities/sources',{method:'POST',body:data}).then(function(r){form.reset();return refresh();}).catch(function(e){err.textContent=e.message;}).finally(function(){b.disabled=false;});});
    return refresh();
  }
  function capDependencyPreferences() {
    return api('/api/capabilities/dependency-preferences',{fresh:true}).then(function(r){
      modal('<h2>'+esc(T('cap.dependency_preferences'))+'</h2><p class="muted sm">'+esc(T('cap.preferences_note'))+'</p><form id="cap-preference-form"><label class="field"><span>'+esc(T('cap.tool_name'))+'</span><input name="name" required maxlength="128"></label><label class="field"><span>'+esc(T('cap.executable_path'))+'</span><input name="path" maxlength="4096"></label><label class="cap-check"><input type="checkbox" name="prefer_managed"> '+esc(T('cap.prefer_managed'))+'</label><button type="submit" class="btn">'+esc(T('cap.save_preference'))+'</button></form><div data-preferences>'+ (r.preferences||[]).map(function(p){return '<p>'+esc(p.name+' · '+(p.path||'')+' · '+(p.prefer_managed?T('cap.prefer_managed'):T('cap.system_first')))+'</p><button type="button" class="btn sm ghost" data-preference-remove="'+esc(p.name)+'">'+esc(T('cap.reset_preference'))+'</button>';}).join('')+'</div><p data-preference-error class="field-err" role="alert"></p><div class="actions"><button class="btn ghost" data-close>'+esc(T('btn.close'))+'</button></div>',function(root,close){
        var err=$('[data-preference-error]',root);
        $('#cap-preference-form',root).addEventListener('submit',function(e){e.preventDefault();var f=e.currentTarget,b=$('button',f);b.disabled=true;api('/api/capabilities/dependency-preferences',{method:'PUT',body:{name:f.elements.name.value,path:f.elements.path.value,prefer_managed:f.elements.prefer_managed.checked}}).then(function(){close();return capDependencyPreferences();}).catch(function(e){err.textContent=e.message;b.disabled=false;});});
        $$('[data-preference-remove]',root).forEach(function(b){b.addEventListener('click',function(){b.disabled=true;api('/api/capabilities/dependency-preferences',{method:'DELETE',body:{name:b.getAttribute('data-preference-remove')}}).then(function(){close();return capDependencyPreferences();}).catch(function(e){err.textContent=e.message;b.disabled=false;});});});
      });
    });
  }

  function capDependencyManual(d) {
    var resolved=d.resolved||{},preference=d.preference||{};
    return (resolved.path?'<p class="muted sm">'+esc([resolved.source,resolved.version,resolved.path].filter(Boolean).join(' · '))+'</p>':'')+
      (preference.path||preference.prefer_managed?'<p class="muted sm">'+esc(preference.path||T('cap.prefer_managed'))+'</p>':'')+
      (d.manual||[]).map(function(m){var docs=/^https:\/\/[^\s]+$/.test(m.docs||'')?'<a href="'+esc(m.docs)+'" target="_blank" rel="noopener noreferrer">'+esc(T('cap.official_instructions'))+'</a>':'';return '<details><summary>'+esc(m.os)+' · '+esc(T('cap.manual_instructions'))+'</summary><p>'+esc(m.note||'')+'</p>'+docs+(m.commands||[]).map(function(command){return '<pre class="cap-file">'+esc(command)+'</pre>';}).join('')+'</details>';}).join('');
  }

  function capChooseSourceSkills(source) {
    return api('/api/capabilities/source-skills',{method:'POST',body:{source:source}}).then(function(r){
      modal('<h2>'+esc(T('cap.choose_skill'))+'</h2><p class="muted sm">'+esc(r.commit||'')+'</p>'+(r.results||[]).map(function(c,i){return '<section><h3>'+esc(c.title||c.id)+'</h3><p>'+esc(((c.source||{}).subpaths||[]).join(', '))+'</p><button type="button" class="btn sm" data-source-skill="'+i+'">'+esc(T('cap.preview'))+'</button></section>';}).join('')+(!(r.results||[]).length?'<p>'+esc(T('cap.no_results'))+'</p>':'')+'<p data-source-skill-error class="field-err" role="alert"></p><div class="actions"><button class="btn ghost" data-close>'+esc(T('btn.close'))+'</button></div>',function(root,close){$$('[data-source-skill]',root).forEach(function(b){b.addEventListener('click',function(){b.disabled=true;capPropose(r.results[Number(b.getAttribute('data-source-skill'))].id).then(close).catch(function(e){$('[data-source-skill-error]',root).textContent=e.message;b.disabled=false;});});});});
    });
  }
