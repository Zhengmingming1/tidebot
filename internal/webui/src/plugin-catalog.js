  // ---------- official catalog (card wall) ----------
  var OF_BRAND = { "rss-feed": "br-rss", "web-monitor": "br-web", wecom: "br-wecom", dingtalk: "br-ding", email: "br-mail", "daily-digest": "br-digest" };
  var catView = { cat: "all" };
  function catalogWall(cat) {
    var cats = [["all", "of.c_all"], ["provider", "of.c_provider"], ["trigger", "of.c_trigger"], ["connector", "of.c_connector"], ["pack", "of.c_pack"]];
    return '<div class="of-wall"><div class="of-filter seg sm" role="radiogroup">' + cats.map(function (c) { return '<button type="button" role="radio" data-ofc="' + c[0] + '" class="' + (catView.cat === c[0] ? "active" : "") + '" aria-checked="' + (catView.cat === c[0]) + '">' + esc(T(c[1])) + "</button>"; }).join("") + "</div>" +
      '<div class="of-grid">' + cat.map(function (e) {
        var hide = catView.cat !== "all" && e.category !== catView.cat;
        var act = e.installed ? (e.update ? '<button class="btn sm" data-of-install="' + esc(e.id) + '">' + ic("download", "sm") + esc(T("of.update")) + "</button>" : '<span class="pill ok sm">' + ic("check", "sm") + esc(T("of.installed")) + "</span>") : '<button class="btn sm" data-of-install="' + esc(e.id) + '">' + ic("plus", "sm") + esc(T("of.install")) + "</button>";
        return '<div class="ap-tile of-tile' + (hide ? " hidden" : "") + '" data-of="' + esc(e.id) + '"><span class="ap-logo ' + (OF_BRAND[e.id] || "br-other") + '" aria-hidden="true">' + ic(e.icon) + "</span>" +
          "<strong>" + esc(T("of." + e.id + ".n")) + '</strong><span class="ap-tag">' + esc(T("of.t_" + e.category)) + '</span><small class="muted of-desc">' + esc(T("of." + e.id + ".d")) + '</small><div class="of-foot"><span class="muted sm-t">' + esc(T("of.official")) + " · v" + esc(e.version) + "</span>" + act + "</div></div>";
      }).join("") + "</div></div>";
  }
  function officialInstall(id, entry) {
    rpc("plugins.install_official", { id: id }).then(function (r) {
      var ver = r.version || {}, req = ver.requested_permissions || [], fresh = r.new_permissions || [];
      modal('<div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>" + esc(T("of.install_title", { n: T("of." + id + ".n") })) + "</h2></div>" +
        '<p class="muted">' + esc(T("of." + id + ".d")) + "</p><h4>" + esc(T("pl.perms")) + "</h4>" + (req.length ? permChooser(req, fresh) : '<p class="muted">' + esc(T("pl.no_perms")) + "</p>") +
        '<p class="hint">' + esc(T("pl.grant_hint")) + '</p><div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn primary" id="of-ok">' + ic("shield", "sm") + esc(T("pl.approve")) + "</button></div>", function (root, close) {
        $("#of-ok", root).addEventListener("click", function (e) {
          e.currentTarget.disabled = true;
          var granted = $$("[data-perm]", root).filter(function (c) { return c.checked; }).map(function (c) { return c.value; });
          rpc("plugins.approve", { version_id: ver.id, permissions: granted }).then(function () {
            close(); toast(T("pl.installed_ok", { n: T("of." + id + ".n") }));
            var m = ver.manifest || {};
            if (m.config_schema && m.config_schema.properties) pluginConfigModal({ id: id, name: m.name, source: "official" }, id === "daily-digest" ? function () { applyPack(id); } : refresh);
            else refresh();
          }).catch(function (err) { e.target.disabled = false; fail(err); });
        });
      });
    }).catch(fail);
  }
  function applyPack(id) {
    rpc("plugins.apply_pack", { id: id }).then(function (r) {
      var miss = r.missing_plugins || [];
      toast(r.duplicate ? T("of.pack_dup") : T("of.pack_done"));
      if (miss.length) toast(T("of.pack_missing", { p: miss.map(function (x) { return T("of." + x + ".n"); }).join("、") }), true);
      if (r.task_ids && r.task_ids[0] && !r.duplicate) location.hash = "#/tasks/" + enc(r.task_ids[0]); else refresh();
    }).catch(fail);
  }

  // ---------- AI plugin builder ----------
  var AI_STEPS = ["describe", "spec", "build", "review"];
  function aiStepBar(cur) {
    var i = AI_STEPS.indexOf(cur);
    return '<ol class="ai-steps">' + AI_STEPS.map(function (s, k) { return '<li class="' + (k < i ? "done" : k === i ? "cur" : "") + '"><span>' + (k < i ? ic("check", "sm") : k + 1) + "</span>" + esc(T("ai.s_" + s)) + "</li>"; }).join("") + "</ol>";
  }
  function aiSpecCard(sp) {
    var perms = [].concat((sp.permissions.network || []).map(function (x) { return "network:" + x; }), (sp.permissions.host_api || []).map(function (x) { return "host_api:" + x; }), (sp.permissions.secrets || []).map(function (x) { return "secret:" + x; }));
    var props = ((sp.config_schema || {}).properties) || {};
    var items = (sp.triggers || []).map(function (t) { return '<span class="pill plain">' + ic("bolt", "sm") + esc((t.kind || "") + " · " + (t.title || t.id)) + "</span>"; }).concat((sp.tools || []).map(function (t) { return '<span class="pill plain" title="' + esc(t.description || "") + '">' + ic("terminal", "sm") + esc(t.name) + "</span>"; }));
    return '<div class="ai-spec"><div class="ai-spec-head"><span class="ap-logo br-ai">' + ic("sparkle") + '</span><div class="grow-col"><strong>' + esc(sp.name) + '</strong><small class="mono muted">' + esc(sp.id) + '</small></div><span class="ap-tag">' + esc(T("ai.type_" + sp.type)) + '</span><span class="ap-tag">' + esc(sp.runtime_language) + "</span></div>" +
      '<p class="ai-sum">' + esc(sp.summary || "") + "</p>" +
      '<dl class="ai-dl"><dt>' + esc(T("ai.contrib")) + "</dt><dd>" + (items.join(" ") || "—") + "</dd>" +
      "<dt>" + esc(T("pl.perms")) + "</dt><dd>" + (perms.length ? perms.map(function (p) { var r = permRisk(p), l = permLabel(p); return '<span class="pill plain">' + esc(T("perm.k_" + l.kind.replace(".", "_"))) + " <code>" + esc(l.val) + '</code> <span class="risk ' + r + '">' + esc(T("perm.r_" + r)) + "</span></span>"; }).join(" ") : esc(T("pl.no_perms"))) + "</dd>" +
      "<dt>" + esc(T("ai.config")) + "</dt><dd>" + (Object.keys(props).length ? Object.keys(props).map(function (k) { var x = props[k]; return '<span class="pill plain">' + (x.writeOnly ? ic("key", "sm") : "") + esc(x.title || k) + "</span>"; }).join(" ") : "—") + "</dd></dl></div>";
  }
  function aiLogPane(j) {
    var lines = (j.log || []).concat((j.run_log || []).map(function (l) { return "  " + l; }));
    return '<pre class="ai-log" id="ai-log">' + esc(lines.slice(-120).join("\n")) + "</pre>";
  }
  function aiReview(j) {
    var files = (j.files || []).filter(function (f) { return f.status !== "same"; }), all = j.files || [];
    var checks = '<ul class="ai-checks">' + (j.checks || []).map(function (c) { return '<li class="' + (c.ok ? "ok" : "bad") + '">' + ic(c.ok ? "check" : "x", "sm") + "<strong>" + esc(T("ai.ck_" + c.name)) + "</strong> <span class=\"muted\">" + esc(c.detail || "") + "</span></li>"; }).join("") + "</ul>";
    var flist = (files.length ? files : all);
    var fsel = '<div class="ai-files"><nav class="ai-flist">' + flist.map(function (f, i) { return '<button type="button" data-file="' + i + '" class="' + (i ? "" : "active") + '"><span class="fst ' + esc(f.status) + '">' + esc(T("ai.f_" + f.status)) + "</span>" + esc(f.path) + "</button>"; }).join("") + '</nav><pre class="ai-code" id="ai-code"></pre></div>';
    var perms = (j.permissions || []).length ? '<div class="perm-groups">' + groupPerms(j.permissions).map(function (g) {
      return '<fieldset class="perm-g"><legend>' + esc(T("perm.g_" + g.key)) + "</legend>" + g.items.map(function (p) { var r = permRisk(p), l = permLabel(p); return '<label class="perm-row"><input type="checkbox" data-perm value="' + esc(p) + '"><span class="grow"><span class="muted">' + esc(T("perm.k_" + l.kind.replace(".", "_"))) + "</span> <code>" + esc(l.val) + "</code>" + ((j.new_permissions || []).indexOf(p) >= 0 && j.kind === "iterate" ? ' <span class="pill warn sm">' + esc(T("pl.new_perm")) + "</span>" : "") + '</span><span class="risk ' + r + '">' + esc(T("perm.r_" + r)) + "</span></label>"; }).join("") + "</fieldset>";
    }).join("") + '</div><p class="hint">' + esc(T("ai.perm_hint")) + "</p>" : '<p class="muted">' + esc(T("pl.no_perms")) + "</p>";
    return '<div class="ai-review">' + (j.summary ? '<p class="ai-sum">' + esc(j.summary) + "</p>" : "") + "<h4>" + esc(T("ai.checks")) + (j.passed ? ' <span class="pill ok sm">' + esc(T("ai.passed")) + "</span>" : ' <span class="pill bad sm">' + esc(T("ai.failed_ck")) + "</span>") + "</h4>" + checks +
      "<h4>" + esc(T("ai.files")) + "</h4>" + fsel + "<h4>" + esc(T("pl.perms")) + "</h4>" + perms + "</div>";
  }
  function wireFiles(host, j) {
    var files = (j.files || []).filter(function (f) { return f.status !== "same"; });
    if (!files.length) files = j.files || [];
    var show = function (i) {
      var f = files[i] || {}, code = $("#ai-code", host); if (!code) return;
      $$("[data-file]", host).forEach(function (b) { b.classList.toggle("active", +b.getAttribute("data-file") === i); });
      if (f.binary) { code.textContent = T("ai.binary"); return; }
      if (f.diff && f.status === "modified") { code.innerHTML = f.diff.split("\n").map(function (l) { var c = l[0] === "+" ? "add" : l[0] === "-" ? "del" : l.indexOf("@@") === 0 ? "hunk" : ""; return '<span class="dl ' + c + '">' + esc(l) + "</span>"; }).join("\n"); }
      else code.textContent = f.content || "";
    };
    $$("[data-file]", host).forEach(function (b) { b.addEventListener("click", function () { show(+b.getAttribute("data-file")); }); });
    show(0);
  }
  // aiPanel renders a builder job into host and keeps it updated until it settles.
  function aiPanel(host, job, opts) {
    var timer = null, last = "", dead = false;
    var stop = function () { dead = true; if (timer) clearTimeout(timer); };
    var foot = opts.foot;
    var paint = function (j) {
      if (dead || !document.contains(host)) { stop(); return; }
      var key = j.status + "|" + (j.log || []).length + "|" + (j.run_log || []).length + "|" + j.cost_usd;
      if (key === last) return; last = key;
      var step = j.status === "drafting" ? "describe" : j.status === "spec_ready" ? "spec" : j.status === "review" || j.status === "installing" || j.status === "installed" ? "review" : "build";
      var busy = j.status === "drafting" || j.status === "generating" || j.status === "checking";
      var meta = '<p class="ai-meta muted">' + ic("wallet", "sm") + esc(T("ai.budget", { b: usd(j.budget_usd || 0, 2), c: usd(j.cost_usd || 0, 4) })) + (j.redacted ? " · " + ic("shield", "sm") + esc(T("ai.redacted", { n: j.redacted })) : "") + "</p>";
      var body;
      if (j.status === "failed") body = '<p class="notice bad">' + ic("x", "sm") + esc(j.error || "") + "</p>" + aiLogPane(j);
      else if (j.status === "canceled") body = '<p class="notice">' + ic("info", "sm") + esc(T("ai.canceled")) + "</p>";
      else if (j.status === "spec_ready") body = '<p class="hint">' + esc(T("ai.spec_hint")) + "</p>" + aiSpecCard(j.spec);
      else if (j.status === "review" || j.status === "installing") body = aiReview(j);
      else if (j.status === "installed") body = '<p class="notice ok">' + ic("check", "sm") + esc(T("ai.installed_ok")) + "</p>";
      else body = '<div class="ai-progress"><span class="spin" aria-hidden="true"></span><strong>' + esc(T("ai.st_" + j.status)) + "</strong></div>" + aiLogPane(j);
      host.innerHTML = (opts.steps === false ? "" : aiStepBar(step)) + meta + body;
      var lg = $("#ai-log", host); if (lg) lg.scrollTop = lg.scrollHeight;
      if (j.status === "review") wireFiles(host, j);
      var btns = "";
      if (busy) btns = '<button type="button" class="btn danger" id="ai-cancel">' + ic("x", "sm") + esc(T("ai.cancel")) + "</button>";
      else if (j.status === "spec_ready") btns = '<button type="button" class="btn ghost" id="ai-back">' + esc(T("ai.back")) + '</button><button type="button" class="btn primary" id="ai-gen">' + ic("sparkle", "sm") + esc(T("ai.generate")) + "</button>";
      else if (j.status === "review") btns = (j.kind === "create" ? '<button type="button" class="btn" id="ai-gen">' + ic("refresh", "sm") + esc(T("ai.regen")) + "</button>" : "") + '<span class="grow"></span><button type="button" class="btn primary" id="ai-install"' + (j.passed ? "" : " disabled") + ">" + ic("shield", "sm") + esc(T(j.kind === "iterate" ? "ai.install_ver" : "ai.install")) + "</button>";
      else if (j.status === "failed" || j.status === "canceled") btns = (j.spec && j.kind === "create" && j.spec.type ? '<button type="button" class="btn" id="ai-gen">' + ic("refresh", "sm") + esc(T("ai.retry")) + "</button>" : "") + '<button type="button" class="btn ghost" id="ai-back">' + esc(T("ai.back")) + "</button>";
      else if (j.status === "installed") btns = '<button type="button" class="btn primary" id="ai-done">' + esc(T("ai.done")) + "</button>";
      foot.innerHTML = '<span class="muted ai-foot-hint">' + ic("info", "sm") + esc(T("ai.foot")) + '</span><span class="grow"></span>' + btns;
      var on = function (sel, f) { var b = $(sel, foot); if (b) b.addEventListener("click", f); };
      on("#ai-cancel", function (e) { e.currentTarget.disabled = true; rpc("plugins.ai.cancel", { job_id: j.id }).then(poll).catch(fail); });
      on("#ai-back", function () { stop(); opts.back(); });
      on("#ai-gen", function (e) { e.currentTarget.disabled = true; rpc("plugins.ai.generate", { job_id: j.id }).then(function (x) { last = ""; paint(x); poll(); }).catch(fail); });
      on("#ai-install", function (e) {
        var granted = $$("[data-perm]", host).filter(function (c) { return c.checked; }).map(function (c) { return c.value; });
        e.currentTarget.disabled = true;
        rpc("plugins.ai.install", { job_id: j.id, permissions: granted }).then(function (r) { toast(T("ai.installed_v", { v: r.version })); last = ""; poll(); if (opts.installed) opts.installed(r); }).catch(function (err) { e.currentTarget && (e.currentTarget.disabled = false); fail(err); });
      });
      on("#ai-done", function () { stop(); opts.done(); });
    };
    var poll = function () {
      if (dead) return;
      rpc("plugins.ai.status", { job_id: job.id }).then(function (j) {
        paint(j);
        var busy = j.status === "drafting" || j.status === "generating" || j.status === "checking" || j.status === "installing";
        if (busy && !dead) timer = setTimeout(poll, 700);
      }).catch(function (e) { if (!dead) { stop(); fail(e); } });
    };
    paint(job); poll();
    return { stop: stop };
  }
  function aiCreateModal() {
    var examples = ["ai.ex1", "ai.ex2", "ai.ex3"];
    modal('<div class="ai-wiz"><div class="ap-head"><h2>' + ic("sparkle") + esc(T("ai.title")) + '</h2><button type="button" class="icon-btn" data-close aria-label="' + esc(T("btn.close")) + '">' + ic("x") + '</button></div><div class="ai-body" id="ai-body"></div><div class="ap-foot" id="ai-foot"></div></div>', function (root, close) {
      var body = $("#ai-body", root), foot = $("#ai-foot", root), panel = null, draft = "";
      var describe = function () {
        if (panel) panel.stop();
        body.innerHTML = aiStepBar("describe") + '<label class="field"><span>' + esc(T("ai.describe")) + '</span><textarea id="ai-desc" rows="5" maxlength="4000" placeholder="' + esc(T("ai.describe_ph")) + '">' + esc(draft) + '</textarea><small class="hint">' + esc(T("ai.no_secrets")) + "</small></label>" +
          '<div class="ai-examples"><span class="muted">' + esc(T("ai.examples")) + "</span>" + examples.map(function (k) { return '<button type="button" class="chip" data-ex="' + k + '">' + esc(T(k)) + "</button>"; }).join("") + "</div>" +
          '<ul class="ai-promise">' + ["ai.p1", "ai.p2", "ai.p3", "ai.p4"].map(function (k) { return "<li>" + ic("shield", "sm") + esc(T(k)) + "</li>"; }).join("") + "</ul>";
        foot.innerHTML = '<span class="muted ai-foot-hint">' + ic("wallet", "sm") + esc(T("ai.budget_pre", { b: usd(1.1, 2) })) + '</span><span class="grow"></span><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn primary" id="ai-next">' + esc(T("ai.draft")) + "</button>";
        $("[data-close]", foot).addEventListener("click", close);
        var ta = $("#ai-desc", body); ta.focus();
        $$("[data-ex]", body).forEach(function (b) { b.addEventListener("click", function () { ta.value = T(b.getAttribute("data-ex")); ta.focus(); }); });
        $("#ai-next", foot).addEventListener("click", function (e) {
          draft = ta.value.trim(); if (draft.length < 8) { ta.focus(); toast(T("ai.too_short"), true); return; }
          e.currentTarget.disabled = true;
          rpc("plugins.ai.draft", { description: draft }).then(function (j) { panel = aiPanel(body, j, { foot: foot, back: describe, done: function () { close(); refresh(); }, installed: function () {} }); }).catch(function (err) { e.target.disabled = false; fail(err); });
        });
      };
      describe();
    });
    var box = $("#modal-root .modal"); if (box) box.classList.add("ap-modal", "ai-modal");
  }
  function aiIterateDrawer(p) {
    openDrawer(function (dw) {
      var fill = function () {
        Promise.all([rpc("plugins.ai.messages", { id: p.id }), rpc("plugins.get", { id: p.id })]).then(function (r) {
          var msgs = r[0] || [], info = (r[1] || {}).plugin || {}, cur = info.current || {}, vers = (info.versions || []).slice().reverse();
          dw.innerHTML = '<div class="drawer-head"><span class="ap-logo br-ai sm">' + ic("sparkle") + '</span><div class="grow-col"><h2>' + esc(T("ai.iter_title", { n: plName(p) })) + '</h2><small class="muted">' + esc(T("ai.iter_sub")) + '</small></div><button type="button" class="icon-btn" data-drawer-close aria-label="' + esc(T("btn.close")) + '">' + ic("x") + "</button></div>" +
            '<div class="drawer-body"><h4 class="sec">' + esc(T("ai.versions")) + '</h4><ul class="ai-vers">' + vers.map(function (v) { var isCur = v.id === cur.id; return '<li class="' + (isCur ? "cur" : "") + '"><strong>v' + esc(v.version) + "</strong>" + (isCur ? ' <span class="pill ok sm">' + esc(T("ai.current")) + "</span>" : "") + ' <span class="muted">' + esc(fmtTime(v.created_at)) + '</span><span class="grow"></span>' + (isCur ? "" : '<button class="btn sm" data-rb="' + esc(v.id) + '">' + ic("rollback", "sm") + esc(T("ai.rollback")) + "</button>") + "</li>"; }).join("") + "</ul>" +
            '<h4 class="sec">' + esc(T("ai.chat")) + '</h4><div class="ai-chat">' + (msgs.length ? msgs.map(function (m) { return '<div class="ai-msg ' + esc(m.role) + '"><div>' + esc(m.body) + '</div><small class="muted">' + (m.version ? "v" + esc(m.version) + " · " : "") + esc(fmtTime(m.created_at)) + "</small></div>"; }).join("") : '<p class="muted">' + esc(T("ai.chat_empty")) + "</p>") + "</div>" +
            '<div id="ai-iter-panel"></div><label class="field"><span>' + esc(T("ai.iter_ask")) + '</span><textarea id="ai-iter-msg" rows="3" maxlength="4000" placeholder="' + esc(T("ai.iter_ph")) + '"></textarea><small class="hint">' + esc(T("ai.no_secrets")) + "</small></label></div>" +
            '<div class="drawer-foot" id="ai-iter-foot"><span class="grow"></span><button type="button" class="btn primary" id="ai-iter-go">' + ic("sparkle", "sm") + esc(T("ai.iter_go")) + "</button></div>";
          $$("[data-drawer-close]", dw).forEach(function (b) { b.addEventListener("click", function () { closeDrawer(); refresh(); }); });
          $$("[data-rb]", dw).forEach(function (b) { b.addEventListener("click", function () { rpc("plugins.rollback", { id: p.id, version_id: b.getAttribute("data-rb") }).then(function () { toast(T("ai.rolled_back")); fill(); }).catch(fail); }); });
          $("#ai-iter-go", dw).addEventListener("click", function (e) {
            var m = $("#ai-iter-msg", dw).value.trim(); if (!m) return;
            e.currentTarget.disabled = true;
            rpc("plugins.ai.iterate", { id: p.id, message: m }).then(function (j) {
              $("#ai-iter-msg", dw).closest(".field").classList.add("hidden");
              aiPanel($("#ai-iter-panel", dw), j, { steps: false, foot: $("#ai-iter-foot", dw), back: fill, done: fill, installed: function () {} });
            }).catch(function (err) { e.target.disabled = false; fail(err); });
          });
        }).catch(fail);
      };
      fill();
    });
  }
  function copyText(t) { return (navigator.clipboard && window.isSecureContext ? navigator.clipboard.writeText(t) : new Promise(function (ok) { var ta = document.createElement("textarea"); ta.value = t; document.body.appendChild(ta); ta.select(); document.execCommand("copy"); ta.remove(); ok(); })).then(function () { toast(T("tr.copied")); }); }
  function triggerCard(t, tname) {
    var url = location.origin + "/hooks/" + t.id;
    var curl = "curl -X POST " + url + " \\\n  -H \"Authorization: Bearer $TIDEBOT_HOOK_TOKEN\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"hello\":\"world\"}'";
    var res = t.last_result || "", good = res === "accepted";
    return '<div class="card trig' + (t.enabled ? "" : " off") + '"><div class="trig-head"><span class="ico">' + ic(t.kind === "webhook" ? "globe" : t.kind === "file_change" ? "folder" : "bolt") + '</span><div class="grow"><strong>' + esc(T("tr.k_" + (t.kind === "file_change" ? "file" : t.kind === "webhook" ? "webhook" : "plugin"))) + '</strong><small><a href="#/tasks/' + enc(t.task_id) + '">' + esc(tname[t.task_id] || t.task_id) + "</a>" + (t.plugin_id ? " · " + esc(t.plugin_id) : "") + "</small></div>" +
      '<button class="switch' + (t.enabled ? " on" : "") + '" data-trg-en="' + esc(t.id) + '" role="switch" aria-checked="' + !!t.enabled + '" aria-label="' + esc(T("tr.enabled")) + '"></button></div>' +
      '<p class="trig-last">' + (t.last_fired_at ? tAgo("tr.last", { n: t.fire_count }, "t", t.last_fired_at) + ' <span class="pill sm ' + (good ? "ok" : "bad") + '">' + esc(good ? T("tr.r_ok") : res) + "</span>" : '<span class="muted">' + esc(T("tr.never")) + "</span>") + "</p>" +
      (t.kind === "webhook" ? '<div class="copy-row"><code class="grow">' + esc(url) + '</code><button class="btn sm" data-copy="' + esc(url) + '">' + esc(T("tr.copy_url")) + '</button></div><details class="curl"><summary>' + esc(T("tr.curl")) + '</summary><pre class="code">' + esc(curl) + '</pre><button class="btn sm" data-copy="' + esc(curl) + '">' + esc(T("tr.copy_curl")) + '</button><small class="hint">' + esc(T("tr.curl_token")) + "</small></details>" : t.kind === "file_change" ? '<p class="mono muted">' + esc((t.config || {}).path || "") + "</p>" : t.plugin_id ? '<p class="mono muted trunc">' + esc((t.config || {}).url || (t.config || {}).folder || (t.config || {}).repo || "") + "</p>" : "") +
      '<div class="row-actions">' + (t.kind === "webhook" || t.kind === "file_change" ? '<button class="btn sm" data-trg-test="' + esc(t.id) + '"' + (t.enabled ? "" : " disabled") + ">" + ic("play", "sm") + esc(T("tr.test")) + "</button>" : "") + '<button class="btn sm ghost danger" data-trg-del="' + esc(t.id) + '">' + esc(T("pl.remove")) + "</button></div></div>";
  }

  // ---------- PLUGINS ----------
  // Pure reads: they neither drop the page cache nor wait for the network when cached.
  var RPC_READS = ["dashboard", "plugins.list", "plugins.catalog", "plugins.get", "plugins.config_get", "plugins.trigger_fields", "plugins.ai.status", "plugins.ai.messages", "triggers.list", "backup.list", "backups.list", "notify.overview", "pricing.get", "pricing.catalog.get", "storage.get", "power.get", "timezone.get", "network.get", "skills.proposals", "skills.compare", "scheduler.quiet.get", "scheduler.pause.get", "free_pool.status", "free_pool.models", "update.status"];
  function rpcKey(method, params) { return "rpc:" + method + ":" + JSON.stringify(params || {}); }
  function rpcCall(method, params, timeout, read) {
    return api("/api/rpc/" + method, { method: "POST", body: params || {}, timeout: timeout, read: read, cacheKey: read ? rpcKey(method, params) : "" }).then(function (r) { return r.result; });
  }
  function rpc(method, params, timeout) { return rpcCall(method, params, timeout, RPC_READS.indexOf(method) >= 0); }
  // For a method that reads or writes depending on its params (e.g. skills.optimize_settings).
  function rpcRead(method, params) { return rpcCall(method, params, undefined, true); }
  // Can command runs be sandboxed on this machine (Claude Code's OS sandbox): a pill in
  // Settings › Security; on Linux with bubblewrap / socat missing it opens the install help (tidebot
  // never runs sudo for you). Command runs themselves come with the third permission level.
  function sandboxPill(sb) {
    sb = sb || {};
    if (sb.status === "missing") return '<button type="button" class="pill warn sm pl-sb" id="pl-sb" data-tip="' + esc(T("pl.sb_missing_tip", { m: (sb.missing || []).join(", ") })) + '">' + ic("shield", "sm") + esc(T("pl.sb_missing")) + "</button>";
    if (sb.status === "none") return '<span class="pill plain sm pl-sb" tabindex="0" data-tip="' + esc(T("pl.sb_none_tip")) + '">' + ic("shield", "sm") + esc(T("pl.sb_none")) + "</span>";
    if (sb.status === "ok") return '<span class="pill ok sm pl-sb" tabindex="0" data-tip="' + esc(T("pl.sb_ok_tip")) + '">' + ic("shield", "sm") + esc(T("pl.sb_ok")) + "</span>";
    return "";
  }
  function sandboxHelp(sb) {
    sb = sb || {};
    var cmd = sb.install || "sudo apt-get install -y bubblewrap socat";
    modal('<div class="card-head"><span class="ico warn">' + ic("shield") + "</span><h2>" + esc(T("pl.sb_help_title")) + "</h2></div>" +
      '<p class="confirm-body">' + esc(T("pl.sb_help_body", { m: (sb.missing || []).join(", ") })) + "</p>" +
      '<div class="sb-cmd"><code class="mono" id="sb-cmd">' + esc(cmd) + '</code><button type="button" class="icon-btn sm" id="sb-copy" aria-label="' + esc(T("pl.sb_copy")) + '" data-tip="' + esc(T("pl.sb_copy")) + '">' + ic("copy", "sm") + "</button></div>" +
      '<p class="muted sb-after">' + esc(T("pl.sb_help_after")) + "</p>" +
      '<div class="actions"><button type="button" class="btn primary" data-close autofocus>' + esc(T("btn.close")) + "</button></div>", function (root) {
        $("#sb-copy", root).addEventListener("click", function () {
          (navigator.clipboard ? navigator.clipboard.writeText(cmd) : Promise.reject()).then(function () { toast(T("tr.copied")); }, function () {
            var r = document.createRange(); r.selectNodeContents($("#sb-cmd", root)); var sel = getSelection(); sel.removeAllRanges(); sel.addRange(r);
          });
        });
      });
  }
  function renderPlugins() {
    var seq = routeSeq;
    return Promise.all([rpc("dashboard"), rpc("plugins.list"), rpc("triggers.list"), rpc("backup.list"), api("/api/tasks").catch(function () { return []; }), rpc("plugins.catalog").catch(function () { return []; })]).then(function (res) {
      if (seq !== routeSeq) return;
      var d = res[0] || {}, plugins = res[1] || [], trig = res[2] || [], baks = res[3] || [], tname = {}, cat = res[5] || [];
      (res[4] || []).forEach(function (t) { tname[t.id] = t.name; });
      var stat = function (label, v) { return '<div class="stat"><span class="muted">' + esc(label) + "</span><strong>" + esc(v) + "</strong></div>"; };
      var r24 = d.runs_24h || {}, n24 = 0; Object.keys(r24).forEach(function (k) { n24 += r24[k]; });
      var health = '<section class="card"><div class="card-head"><span class="ico">' + ic("pulse") + "</span><h2>" + esc(T("pl.health")) + '</h2></div><div class="grid g4">' +
        stat(T("pl.runs24"), n24) + stat(T("pl.success7"), Math.round((d.success_rate_7d || 0) * 100) + "%") +
        stat(T("pl.p95"), ((d.duration_p95_ms || 0) / 1000).toFixed(1) + " s") + stat(T("pl.cost7"), usd(d.cost_7d_usd || 0, 2)) +
        stat(T("pl.tasks_active"), d.tasks_active || 0) + stat(T("pl.claude"), d.claude_version || "?") +
        stat(T("pl.disk"), ((d.disk_bytes || 0) / 1048576).toFixed(1) + " MB") + stat(T("pl.backup"), d.last_backup ? fmtTime(d.last_backup) : T("td.none")) + "</div></section>";
      var prow = plugins.length ? '<div class="grid g2 pl-cards">' + plugins.map(function (p) {
        var cur = p.current || {}, m = cur.manifest || {}, hasCfg = m.config_schema && m.config_schema.properties && Object.keys(m.config_schema.properties).length;
        var src = p.source || "local", logo = src === "official" ? '<span class="ap-logo sm ' + (OF_BRAND[p.id] || "br-other") + '">' + ic(((cat.filter(function (e) { return e.id === p.id; })[0]) || {}).icon || "plugins") + "</span>" : src === "ai-generated" ? '<span class="ap-logo sm br-ai">' + ic("sparkle") + "</span>" : '<span class="ico">' + ic("plugins") + "</span>";
        return '<div class="card plug' + (p.enabled ? "" : " off") + '"><div class="trig-head">' + logo + '<div class="grow"><strong>' + esc(plName(p)) + ' <span class="tag">' + esc(cur.version || "-") + '</span> <span class="pill sm src-' + esc(src) + '">' + esc(T("pl.src_" + src.replace("-", "_"))) + '</span></strong><small class="mono">' + esc(p.id) + '</small></div><button class="switch' + (p.enabled ? " on" : "") + '" data-pl-en="' + esc(p.id) + '" role="switch" aria-checked="' + !!p.enabled + '" aria-label="' + esc(T("pl.enabled")) + '"></button></div>' +
          '<div class="perm-box"><small class="muted">' + esc(T("perm.granted_title")) + "</small>" + permCard(p) + "</div>" +
          '<div class="row-actions">' + (hasCfg ? '<button class="btn sm" data-pl-cfg="' + esc(p.id) + '">' + ic("settings", "sm") + esc(T("cfg.open")) + "</button>" : '<span class="muted sm-t">' + esc(T("cfg.none")) + "</span>") +
          (p.id === "daily-digest" && src === "official" ? '<button class="btn sm" data-pl-pack="' + esc(p.id) + '">' + ic("calendar", "sm") + esc(T("of.apply_pack")) + "</button>" : "") +
          (src === "ai-generated" ? '<button class="btn sm" data-pl-ai="' + esc(p.id) + '">' + ic("sparkle", "sm") + esc(T("ai.iterate")) + "</button>" : "") + '<span class="grow"></span><button class="btn sm ghost danger" data-pl-uninstall="' + esc(p.id) + '">' + esc(T("pl.uninstall")) + "</button></div></div>";
      }).join("") + "</div>" : emptyState("plugins", T("pl.empty"), T("pl.empty_sub"));
      var trow = trig.length ? '<div class="grid g2 trig-cards">' + trig.map(function (t) { return triggerCard(t, tname); }).join("") + "</div>" : '<p class="muted">' + esc(T("pl.no_triggers")) + "</p>";
      var brow = '<p class="muted">' + esc(baks.length ? T("pl.backups_n", { n: baks.length }) : T("pl.no_backups")) + "</p>";
      mount(capTabs("plugins") + '<div class="page-head"><div><h1>' + esc(T("nav.plugins")) + "</h1><p>" + esc(T("pl.sub")) + '</p></div><div class="actions"><button class="btn ghost" id="bk-now">' + ic("download", "sm") + esc(T("pl.backup_now")) + '</button><button class="btn" id="pl-add">' + ic("upload", "sm") + esc(T("pl.install")) + '</button><button class="btn primary" id="pl-ai">' + ic("sparkle", "sm") + esc(T("ai.title")) + "</button></div></div>" +
        '<h2 class="sec-h">' + ic("star") + esc(T("of.title")) + '<span class="muted sec-sub">' + esc(T("of.sub")) + "</span></h2>" + catalogWall(cat) +
        '<h2 class="sec-h">' + ic("plugins") + esc(T("pl.installed")) + "</h2>" + prow +
        '<h2 class="sec-h">' + ic("globe") + esc(T("pl.triggers")) + '<span class="grow"></span><button class="btn sm" id="tr-add">' + ic("plus", "sm") + esc(T("tr.new")) + "</button></h2>" + trow +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("receipt") + "</span><h2>" + esc(T("pl.backups")) + "</h2></div>" + brow + "</section>", "plugins", [[T("nav.plugins"), "#/plugins"], [T("cap.tab_plugins")]]);
      $$("[data-pl-uninstall]").forEach(function (b) { b.addEventListener("click", function () {
        var pid = b.getAttribute("data-pl-uninstall");
        confirmDialog({ title: T("pl.uninstall_title"), body: T("pl.uninstall_confirm"), ok: T("pl.uninstall"), danger: true, typeName: pid }).then(function (y) {
          if (y) rpc("plugins.uninstall", { id: pid }).then(refresh).catch(fail);
        });
      }); });
      $$("[data-trg-del]").forEach(function (b) { b.addEventListener("click", function () {
        confirmDialog({ title: T("tr.del_title"), body: T("tr.confirm_del"), ok: T("btn.remove"), danger: true }).then(function (y) {
          if (y) rpc("triggers.delete", { id: b.getAttribute("data-trg-del") }).then(refresh).catch(fail);
        });
      }); });
      $("#pl-add").addEventListener("click", pluginInstallModal);
      $("#pl-ai").addEventListener("click", aiCreateModal);
      var byId = {}; plugins.forEach(function (p) { byId[p.id] = p; });
      $$("[data-of-install]").forEach(function (b) { b.addEventListener("click", function () { officialInstall(b.getAttribute("data-of-install")); }); });
      $$("[data-ofc]").forEach(function (b) { b.addEventListener("click", function () {
        catView.cat = b.getAttribute("data-ofc");
        $$("[data-ofc]").forEach(function (x) { x.classList.toggle("active", x === b); x.setAttribute("aria-checked", x === b); });
        $$("[data-of]").forEach(function (t) { var e = cat.filter(function (c) { return c.id === t.getAttribute("data-of"); })[0] || {}; t.classList.toggle("hidden", catView.cat !== "all" && e.category !== catView.cat); });
      }); });
      $$("[data-pl-pack]").forEach(function (b) { b.addEventListener("click", function () { applyPack(b.getAttribute("data-pl-pack")); }); });
      $$("[data-pl-ai]").forEach(function (b) { b.addEventListener("click", function () { aiIterateDrawer(byId[b.getAttribute("data-pl-ai")]); }); });
      $$("[data-pl-cfg]").forEach(function (b) { b.addEventListener("click", function () { var pp = byId[b.getAttribute("data-pl-cfg")]; pluginConfigModal(pp, pp.id === "daily-digest" ? refresh : null); }); });
      $$("[data-pl-en]").forEach(function (b) { b.addEventListener("click", function () { var on = !b.classList.contains("on"); rpc("plugins.enable", { id: b.getAttribute("data-pl-en"), enabled: on }).then(function () { toast(T(on ? "pl.enabled_on" : "pl.enabled_off")); refresh(); }).catch(fail); }); });
      $$("[data-perm-t]").forEach(function (b) { b.addEventListener("click", function () {
        var pid = b.getAttribute("data-perm-t"), perm = b.getAttribute("data-perm-v"), on = !!b.getAttribute("data-on");
        var go = function () { rpc("plugins.permission", { id: pid, permission: perm, granted: !on }).then(function () { toast(T(on ? "perm.revoked" : "perm.granted")); refresh(); }).catch(fail); };
        if (on) confirmDialog({ title: T("perm.revoke_title"), body: T("perm.revoke_body", { p: perm }), ok: T("perm.revoke"), danger: true }).then(function (y) { if (y) go(); }); else go();
      }); });
      $$("[data-trg-en]").forEach(function (b) { b.addEventListener("click", function () { var on = !b.classList.contains("on"); rpc("triggers.enable", { id: b.getAttribute("data-trg-en"), enabled: on }).then(function () { toast(T(on ? "tr.on" : "tr.off")); refresh(); }).catch(fail); }); });
      $$("[data-trg-test]").forEach(function (b) { b.addEventListener("click", function () { b.disabled = true; rpc("triggers.test", { id: b.getAttribute("data-trg-test") }).then(function () { toast(T("tr.test_sent")); setTimeout(refresh, 800); }).catch(function (e) { b.disabled = false; fail(e); }); }); });
      $$("[data-copy]").forEach(function (b) { b.addEventListener("click", function () { copyText(b.getAttribute("data-copy")).catch(fail); }); });
      $("#tr-add").addEventListener("click", function () { triggerModal(plugins); });
      $("#bk-now").addEventListener("click", function () {
        rpc("backup.create", {}).then(function (r) { toast(T("pl.backup_done", { path: r.path })); refresh(); }).catch(function (e) { toast(e.message, true); });
      });
    });
  }

