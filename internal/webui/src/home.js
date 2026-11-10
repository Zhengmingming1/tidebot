  // ---------- HOME ----------
  var HOME_BEATS = 10;
  function beatsBar(beats, extra) {
    var cells = [];
    beats = (beats || []).slice(-HOME_BEATS);
    for (var i = 0; i < HOME_BEATS - beats.length; i++) cells.push('<span class="hb hb-none"></span>');
    beats.forEach(function (b) {
      var cls = b.status === "succeeded" ? "ok" : (b.status === "running" || b.status === "queued") ? "run" : b.status === "skipped" ? "skip" : "bad";
      var tip = when(b.at) + " · " + T("st." + b.status) + " · " + dur(b.duration_ms) + " · " + usd(b.cost_usd, 3);
      cells.push('<a class="hb ' + cls + '" href="#/runs/' + enc(b.id) + '" data-tip="' + esc(tip) + '" aria-label="' + esc(tip) + '"></a>');
    });
    return '<div class="hbar' + (extra ? " " + extra : "") + '">' + cells.join("") + "</div>";
  }
  // Task list "recent runs" cell: the last 10 runs as a small strip (each links to its run,
  // details on hover), then when the newest one started and how many of the finished ones succeeded.
  function taskBeatsCell(t) {
    var bs = t.beats || [];
    if (!bs.length) return '<span class="muted">' + esc(T("tasks.never")) + "</span>";
    // Rate over runs that finished on their own: stopped (canceled) and budget-skipped runs are not failures.
    var done = bs.filter(function (b) { return /^(succeeded|failed|timeout)$/.test(b.status); });
    var ok = done.filter(function (b) { return b.status === "succeeded"; }).length;
    var rate = done.length ? T("tl.ok_of", { ok: ok, n: done.length }) : "";
    var last = bs[bs.length - 1];
    return '<div class="tl-beats" role="group" aria-label="' + esc(T("tl.beats_aria", { n: bs.length, ok: ok, total: done.length, status: T("st." + last.status) })) + '">' + beatsBar(bs, "sm") +
      "<small>" + agoHtml(t.last_run_at || last.at) + (rate ? ' · <a class="' + (ok < done.length ? "tl-rate bad" : "tl-rate") + '" href="#/runs?task=' + enc(t.id) + '" data-tip="' + esc((t.fail_streak ? T("tl.streak_tip", { n: t.fail_streak }) + "\n" : "") + T("tl.ok_tip") + "\n" + T("tl.rate_open")) + '">' + esc(rate) + "</a>" : "") + "</small></div>";
  }
  function homePendingHtml(data) {
    if (!data) return '<p class="hc-empty">' + ic("info", "sm") + esc(T("home.pending_unavailable")) + "</p>";
    return (data.pending || []).slice(0, 4).map(function (p) {
      return '<a class="home-pending" href="#/tasks/' + enc(p.task_id) + '/thread">' + ic("sparkle", "sm") + '<span>' + esc(T("nc.pending_amend", { name: p.task_name || shortId(p.task_id), n: p.count })) + '</span>' + ic("chev", "sm") + '</a>';
    }).join("");
  }
  function homeQuickHtml() {
    return '<section class="home-composer"><form id="home-quick"><div class="home-compose-head"><label for="home-prompt">' + esc(T("home.compose_title")) + '</label>' + draftTagHtml() + '</div>' +
      '<textarea id="home-prompt" name="prompt" rows="2" maxlength="8000" required placeholder="' + esc(T("qr.placeholder")) + '"></textarea>' +
      '<div class="home-compose-actions"><label class="home-provider"><span class="sr-only">' + esc(T("f.provider")) + '</span><select name="provider" aria-label="' + esc(T("f.provider")) + '" disabled><option>' + esc(T("home.loading_provider")) + '</option></select></label>' +
      '<button class="btn ghost sm" type="button" id="home-advanced">' + ic("settings", "sm") + esc(T("home.run_options")) + '</button><span class="spacer"></span><button class="btn primary" id="home-submit" type="submit" disabled>' + ic("play", "sm") + esc(T("btn.run")) + '</button></div>' +
      '<p class="hint home-compose-hint">' + esc(T("qr.hint")) + ' <span>' + esc(T("qr.run_key", { k: keyLabel("Mod+Enter") })) + '</span></p><p class="field-err" id="home-run-error" role="alert"></p>' +
      '<div id="home-recent-prompts" class="home-recent-prompts"></div></form></section>';
  }
  function syncHomeQuickDraft() {
    var form = $("#home-quick");
    if (!form) return;
    draftFlush();
    var ta = $("#home-prompt", form), d = draftLoad("qr");
    ta.value = d ? d.text : "";
    try { var p = localStorage.getItem("tb_qr_provider"); if (p && Array.from(form.provider.options).some(function (o) { return o.value === p; })) form.provider.value = p; } catch (e) {}
  }
  function bindHomeQuick() {
    var form = $("#home-quick"), ta = $("#home-prompt"), submit = $("#home-submit"), advanced = $("#home-advanced"), err = $("#home-run-error");
    if (!form) return;
    wireDraft(form, ta, "qr");
    api("/api/settings").then(function (st) {
      if (!document.contains(form)) return;
      var last = "";
      try { last = localStorage.getItem("tb_qr_provider") || ""; } catch (e) {}
      var ps = readyProviders(st), chosen = ps.some(function (p) { return p.name === last; }) ? last : smartProvider(st);
      form.provider.innerHTML = providerOptions(st, chosen, false, false);
      form.provider.disabled = !ps.length; submit.disabled = !ps.length;
      if (!ps.length) err.textContent = T("cc.no_provider");
    }).catch(function () { if (document.contains(form)) err.textContent = T("home.provider_unavailable"); });
    api("/api/runs/recent-prompts").then(function (d) {
      if (!document.contains(form)) return;
      var prompts = (d && d.prompts || []).slice(0, 3), box = $("#home-recent-prompts", form);
      box.innerHTML = prompts.length ? '<span class="muted">' + esc(T("qr.recent")) + '</span>' + prompts.map(function (p, i) { return '<button type="button" class="chip" data-home-prompt="' + i + '" title="' + esc(p) + '">' + esc(p.length > 30 ? p.slice(0, 30) + "…" : p) + '</button>'; }).join("") : "";
      box.addEventListener("click", function (e) { var b = e.target.closest("[data-home-prompt]"); if (!b) return; ta.value = prompts[+b.dataset.homePrompt]; draftFlush(); draftSave("qr", ta.value); ta.dispatchEvent(new Event("input", { bubbles: true })); ta.focus(); });
    }).catch(function () { /* recent prompts are optional; the form remains usable */ });
    advanced.addEventListener("click", function () {
      draftFlush(); draftSave("qr", ta.value);
      if (!form.provider.disabled) try { localStorage.setItem("tb_qr_provider", form.provider.value); } catch (e) {}
      quickRunModal();
    });
    ta.addEventListener("keydown", function (e) { if (submitKey(e) && !e.repeat) { e.preventDefault(); if (!submit.disabled) form.requestSubmit(); } });
    form.addEventListener("submit", function (e) {
      e.preventDefault(); if (submit.disabled || !ta.value.trim()) return;
      var body = { prompt: ta.value, provider: form.provider.value };
      submit.disabled = true; advanced.disabled = true; err.textContent = "";
      draftFlush(); draftSave("qr", ta.value);
      try { localStorage.setItem("tb_qr_provider", body.provider); } catch (ex) {}
      api("/api/runs/quick", { method: "POST", body: body }).then(function (r) {
        draftClear("qr"); openStarted(r, "toast.quick_started");
      }).catch(function (ex) { submit.disabled = false; advanced.disabled = false; err.textContent = ex.message || String(ex); });
    });
  }
  function renderHome() {
    draftFlush();
    var askedH = Date.now();
    return Promise.all([api("/api/overview"), api("/api/dashboard")]).then(function (r) {
      var o = r[0], d = r[1];
      renderStatus(o);
      // Presets retain their own colours and actions; the separate composer owns quick runs.
      var qa = (o.buttons || []).slice(0, 5).map(function (b) {
        return '<button class="qb" data-color="' + esc(b.color || "") + '" data-hue="' + esc(iconHue(btnIcon(b))) + '" data-btn="' + esc(b.id) + '" data-tip="' + esc(b.description || b.label) + '"><span class="qb-ico">' + ic(btnIcon(b), "sm") + '</span><span class="qb-copy"><strong class="qb-txt">' + esc(b.label) + '</strong><small>' + esc(b.description || T("home.quick_label")) + '</small></span><span class="qb-play">' + ic("play", "sm") + "</span></button>";
      });
      var noTasks = !d.tasks.length;
      mount('<div class="home home-workspace"><header class="home-heading"><div><span class="home-eyebrow">' + esc(fmtTime(new Date().toISOString(), { month: "long", day: "numeric", weekday: "long" })) + '</span><h1>' + esc(T("home.welcome")) + '</h1></div><a class="btn sm" href="#/tasks">' + ic("tasks", "sm") + esc(T("home.view_tasks")) + '</a></header><div class="kstrip" id="kpis"></div>' +
        homeQuickHtml() + '<section class="home-shortcuts"><div class="card-head"><h2>' + esc(T("home.shortcuts")) + '</h2><a class="link" href="#/buttons">' + ic("plus", "sm") + esc(T("home.add_button")) + '</a></div><div class="qbar">' + (qa.length ? qa.join("") : '<a class="home-shortcut-empty" href="#/buttons">' + esc(T("home.add_button_sub")) + '</a>') + '</div></section>' +
        (noTasks ? '<section class="card">' + '<div class="empty">' + ic("tasks") + "<strong>" + esc(T("home.empty_title")) + "</strong>" + esc(T("home.empty_sub")) + '<div class="mt"><button class="btn primary" id="home-new">' + ic("plus", "sm") + esc(T("tf.new")) + "</button></div></div></section>" : "") +
        '<div class="home-cols' + (noTasks ? " one" : "") + '">' +
        '<section class="card flush hc"><div class="card-head"><h2>' + esc(T("home.activity")) + '</h2><span class="muted run-n" id="run-n"></span><a class="link" href="#/runs">' + esc(T("home.view_runs")) + '</a></div><div id="running"></div><div id="home-pending"></div></section>' +
        '<section class="card flush hc"><div class="card-head"><h2>' + esc(T("home.next24")) + '</h2><button type="button" class="icon-btn sm spz-home" id="spz-home" aria-haspopup="menu" aria-expanded="false"></button></div>' +
        '<div id="spz-banner"></div><div id="next24"></div><div class="hc-foot" data-tip="' + esc(T("home.tz_tip")) + '">' + ic("info", "sm") + "<span>" + esc(tzLabel()) + "</span></div></section></div>" +
        (noTasks ? "" : '<details class="home-health"><summary>' + ic("pulse", "sm") + esc(T("nav.tasks")) + '<span class="muted">' + esc(T("home.health")) + '</span></summary><div id="beats"></div></details>') +
        '<section class="card flush home-recent"><div class="card-head"><h2>' + esc(T("home.recent")) + '</h2><a class="link" href="#/runs">' + esc(T("home.view_runs")) + " " + ic("chev", "sm") + '</a></div><div id="recent"></div></section></div>', "home", []);
      applyColors(page());
      var paint = function (o, d, asked) {
        var k = d.kpi, rate = k.runs_today ? Math.round(k.succeeded_today / k.runs_today * 100) + "%" : "—", left = k.budget_usd ? Math.max(0, k.budget_usd - k.spent_today) : null;
        var bp = k.budget_usd ? Math.min(100, k.spent_today / k.budget_usd * 100) : 0;
        // One strip of four figures; the line under each never truncates (details in the tooltip).
        $("#kpis").innerHTML = [[T("home.k_runs"), num(k.runs_today), T("home.k_runs_sub", { n: k.succeeded_today }), T("home.k_runs_tip")],
          [T("home.k_rate"), rate, T("time.today"), ""],
          [T("home.k_spent"), usd(k.spent_today, 2), k.budget_usd ? T("home.of_budget", { v: usd(k.budget_usd, 2) }) : T("home.no_budget_short"), k.budget_usd ? T("home.pct_budget", { p: Math.round(bp) }) : T("home.no_budget")],
          [T("home.k_left"), left == null ? "∞" : usd(left, 2), T("sp.global_daily"), ""]].map(function (x, i) {
          // "today's runs" opens the runs list for today (same day boundary as this count)
          var tag = i === 0 ? 'a class="stat stat-link" href="#/runs?range=today"' : 'div class="stat"';
          return "<" + tag + (x[3] ? ' data-tip="' + esc(x[3]) + '"' : "") + "><small>" + esc(x[0]) + '</small><strong class="num">' + esc(x[1]) + "</strong><span>" + esc(x[2]) + "</span>" + ((i === 3 || i === 2) && k.budget_usd ? '<div class="progress' + (bp >= 100 ? " bad" : bp >= 80 ? " warn" : "") + '"><span data-w="' + (i === 2 ? bp : Math.max(0, 100 - bp)) + '"></span></div>' : "") + (i === 0 ? "</a>" : "</div>");
        }).join("");
        $("#run-n").textContent = d.running.length ? T("home.n_running", { n: d.running.length }) : "";
        setDocTitle({ running: d.running.filter(function (x) { return x.status === "running"; }).length });
        $("#running").innerHTML = d.running.length ? '<ul class="live-list">' + d.running.map(function (x) {
          return '<li data-href="#/runs/' + enc(x.id) + '"><span class="spin" aria-hidden="true"></span><div><strong>' + esc(x.task_name || T("run.adhoc")) + "</strong><small>" + (x.status === "queued" ? esc(x.queue_pos ? T("st.queued_n", { n: x.queue_pos }) : T("st.queued")) : tAgo("home.started_ago", null, "t", x.started_at || x.created_at)) + '</small></div><span class="num muted" data-since="' + esc(x.started_at || "") + '">' + esc(x.started_at ? dur(Date.now() - new Date(x.started_at).getTime()) : "—") + '</span><strong class="num">' + esc(usd(x.cost_usd, 3)) + "</strong></li>";
        }).join("") + "</ul>" : '<p class="hc-empty" data-tip="' + esc(T("home.idle_sub")) + '"><span class="idle-dot" aria-hidden="true"></span><span>' + esc(T("home.idle")) + "</span></p>";
        $("#next24").innerHTML = d.next.length ? '<ul class="timeline tl-sk">' + d.next.slice(0, 4).map(next24Row).join("") + "</ul>" : '<p class="hc-empty">' + ic("calendar", "sm") + "<span><strong>" + esc(T("home.no_sched")) + "</strong> · " + esc(T("home.no_sched_sub")) + "</span></p>";
        wireNext24(d.next);
        if (d.sched_pause && spAdopt(d.sched_pause, asked || Date.now())) paintCC();
        paintHomePause();
        if ($("#beats")) $("#beats").innerHTML = '<div class="beats">' + d.tasks.slice(0, 6).map(function (t) {
          var last = (t.beats || [])[(t.beats || []).length - 1], st = last ? (last.status === "succeeded" ? "ok" : last.status === "running" || last.status === "queued" ? "run" : last.status === "skipped" ? "skip" : "bad") : "none";
          return '<div class="beat-row"><a href="#/tasks/' + enc(t.id) + '" class="bn"><span class="sdot ' + st + '" aria-hidden="true"></span><span class="bn-t">' + esc(t.name) + "</span>" + (t.status === "paused" ? taskPill("paused", t, true) : "") + "</a>" + beatsBar(t.beats) + '<span class="b-ago">' + (last ? agoHtml(last.at) : "—") + "</span></div>";
        }).join("") + "</div>";
        $("#recent").innerHTML = runsTable((o.runs || []).slice(0, 5));
        applySizes(page()); wireRows(page());
      };
      paint(o, d, askedH);
      bindHomeQuick();
      var pendingHost = $("#home-pending");
      var loadPending = function () { return api("/api/notifications/center").then(function (n) { if (document.contains(pendingHost)) pendingHost.innerHTML = homePendingHtml(n); }).catch(function () { if (document.contains(pendingHost)) pendingHost.innerHTML = homePendingHtml(null); }); };
      loadPending();
      var spb = $("#spz-home"); if (spb) spb.addEventListener("click", function (e) { e.stopPropagation(); spMenu(spb, "down"); });
      $$("[data-btn]").forEach(function (b) { b.addEventListener("click", function () { runButton(b.getAttribute("data-btn"), b); }); });
      if ($("#home-new")) $("#home-new").addEventListener("click", function () { taskFormModal(null); });
      var ticks = 0, gate = revGate();
      livePoll(function () {
        ticks++;
        // elapsed counters tick locally every second; the data is refetched only when /api/rev moved
        $$("[data-since]").forEach(function (el) { var t = el.getAttribute("data-since"); if (t) el.textContent = dur(Date.now() - new Date(t).getTime()); });
        if (ticks % liveEvery(!!$("#running .spin")) === 0) {
          return gate(function () { return Promise.all([api("/api/overview"), api("/api/dashboard")]).then(function (r2) { if ((location.hash || "#/") === "#/") { paint(r2[0], r2[1]); return loadPending(); } }); });
        }
      }, 1000);
    });
  }
  function scheduleRefresh(hash, ms) {
    clearTimeout(scheduleRefresh._t);
    scheduleRefresh._t = setTimeout(function () { if ((location.hash || "#/") === hash && !$("#modal-root").innerHTML) refresh(); }, ms);
  }

  // The Run dialog (quick run, merged): a prompt, optionally with a skill and a working
  // folder. Recent prompts come back as chips (filtered as you type), the last provider is
  // preselected, and Ctrl/⌘+Enter runs. Picking a skill fills in the folder, permission and
  // provider of its last run; the folder shows its git branch and uncommitted changes. With
  // a folder the run is read-only unless "Can edit files" is chosen.
  // quickRunModal(pre): pre.prompt set = "edit & rerun" from a run page (the old run's
  // fields are filled in, "Revert" appears once you change them, the original run is left
  // alone); pre = {skill_id} alone opens it with that skill picked (skills page, palette).
  function quickRunModal(pre) {
    var edit = pre && pre.prompt ? pre : null;
    pre = pre || {};
    Promise.all([api("/api/settings"), api("/api/runs/recent-prompts").catch(function () { return { prompts: [] }; }), api("/api/skills").catch(function () { return []; })]).then(function (res) {
      var st = res[0], recent = edit ? [] : (res[1] && res[1].prompts) || [], skills = res[2] || [], lastProv = "";
      try { lastProv = localStorage.getItem("tb_qr_provider") || ""; } catch (e) {}
      if (edit && edit.provider) lastProv = edit.provider;
      if (!edit && !readyProviders(st).some(function (p) { return p.name === lastProv; })) lastProv = smartProvider(st);

      modal('<div class="card-head"><span class="ico">' + ic(edit ? "edit" : "play") + "</span><h2>" + esc(T(edit ? "qr.edit_title" : "rn.title")) + "</h2></div>" +
        '<form id="qr" class="rn-form">' +
        '<input type="hidden" name="skill_id" id="rn-skill"><div id="rn-bindings"></div><div class="rn-two">' +
        '<label class="field"><span>' + esc(T("f.provider")) + '</span><select name="provider">' + providerOptions(st, lastProv, !!(edit && edit.provider), false) + "</select></label></div>" +
        '<div class="field ws-field rn-ws"><span class="rn-ws-lbl"><span id="rn-wlbl">' + esc(T("rn.folder")) + "</span>" + tip(T("rn.folder_tip")) + '<span class="pill sm hidden" id="rn-ws-st"></span><span class="spacer"></span>' +
          modeSegHtml("mode", "exec", st.sandbox) + "</span>" +
          '<div class="in-group ws-in"><input name="workspace" class="mono" spellcheck="false" autocomplete="off" autocapitalize="off" maxlength="1024" aria-labelledby="rn-wlbl" placeholder="' + esc(T("rn.folder_ph")) + '">' +
          '<button type="button" class="icon-btn sm hidden" id="rn-ws-pick" data-tip="' + esc(T("tf.ws_pick")) + '" aria-label="' + esc(T("tf.ws_pick")) + '">' + ic("folder", "sm") + "</button>" +
          '<button type="button" class="icon-btn sm hidden" id="rn-ws-clear" data-tip="' + esc(T("rn.folder_clear")) + '" aria-label="' + esc(T("rn.folder_clear")) + '">' + ic("x", "sm") + "</button></div>" +
          '<div class="qr-recent rn-dirs hidden" id="rn-dirs" role="group" aria-label="' + esc(T("rn.recent_dirs")) + '"><span class="muted">' + esc(T("rn.recent_dirs")) + "</span></div></div>" +
        '<div class="field"><span class="qr-lbl"><span id="qr-lbl">' + esc(T("f.prompt")) + '</span><span class="muted rn-arg hidden" id="rn-arg"></span>' + (edit ? '<button type="button" class="linkish qr-reset hidden" id="qr-reset" data-tip="' + esc(T("qr.revert_tip")) + '">' + ic("rollback", "sm") + esc(T("qr.revert")) + "</button>" : draftTagHtml()) + '</span><textarea name="prompt" aria-labelledby="qr-lbl" required maxlength="8000" placeholder="' + esc(T("qr.placeholder")) + '"></textarea></div>' +
        '<div class="qr-recent hidden" id="qr-recent" role="group" aria-label="' + esc(T("qr.recent")) + '"><span class="muted">' + esc(T("qr.recent")) + "</span></div>" +
        '<p class="hint rn-hint" id="rn-hint"></p><p class="field-err" id="rn-err"></p>' +
        '<div class="actions"><button type="button" class="btn" id="rn-dependencies">' + esc(T("ss.dependencies")) + '</button><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary" type="submit" data-tip="' + esc(T("qr.run_key", { k: keyLabel("Mod+Enter") })) + '">' + ic("play", "sm") + esc(T("btn.run")) + "</button></div></form>",
        function (root, close) {
          var form = $("#qr", root), ta = form.prompt, box = $("#qr-recent", root), reset = $("#qr-reset", root), ws = form.workspace;
          var initial = pre.skill_bindings || (pre.skill_id ? [{skill_id:pre.skill_id,mode:pre.version_id?"pinned":"follow",version_id:pre.version_id||"",role:"primary"}] : []);
          var editor = skillBindingEditor($("#rn-bindings",root),skills,initial,function(bs){form.skill_id.value=bs.length?bs[0].skill_id:"";form.skill_id.dispatchEvent(new Event("change",{bubbles:true}));},{source:pre.skill_set_source,checkRequest:function(){return {kind:"draft",prompt:ta.value,provider:form.provider.value,skill_bindings:editor.value(),workspace:ws.value.trim(),mode:sentMode()};}});
          form.skill_id.value=editor.value().length?editor.value()[0].skill_id:"";
          $("#rn-dependencies",root).addEventListener("click",function(){dependencyCheck({kind:"draft",prompt:ta.value,provider:form.provider.value,skill_bindings:editor.value(),workspace:ws.value.trim(),mode:ws.value.trim()?modeVal():(modeVal()==="exec"?"exec":"edit")});});
          var pill = $("#rn-ws-st", root), pick = $("#rn-ws-pick", root), clear = $("#rn-ws-clear", root), dirsBox = $("#rn-dirs", root), hint = $("#rn-hint", root), err = $("#rn-err", root), argEl = $("#rn-arg", root);
          var wsState = { problem: "", git: null }, seq = 0, timer = 0, wsTouched = false, recentDirs = [];
          var modeVal = function () { var c = form.querySelector('input[name="mode"]:checked'); return c ? c.value : "read"; };
          var setMode = function (m) { $$('input[name="mode"]', form).forEach(function (i) { i.checked = i.value === (m === "edit" || m === "exec" ? m : "read"); }); };
          // without a folder: 只读 = tidebot's scratch folder (""), 可执行命令 = a scratch folder that runs commands
          var sentMode = function () { var m = modeVal(); return ws.value.trim() ? m : (m === "exec" ? "exec" : ""); };
          var cur = function () { return { prompt: ta.value, provider: form.provider.value, skill_id: form.skill_id.value, skill_bindings: editor.value(), skill_set_source: editor.source(), workspace: ws.value.trim(), mode: sentMode() }; };
          var orig = edit ? { prompt: edit.prompt, provider: form.provider.value, skill_id: form.skill_id.value, skill_bindings: editor.value(), skill_set_source: editor.source(), workspace: edit.workspace || "", mode: edit.workspace ? (edit.mode || "read") : (edit.mode === "exec" ? "exec" : "") } : null;
          // the hint under the form says what the run may do; a dirty git folder warns before "Can edit"
          var paintHint = function () {
            var dir = ws.value.trim(), g = wsState.git, warn = false, txt, sb = st.sandbox || {};
            // 可修改 needs a folder; 只读 and 可执行命令 also work in a scratch folder
            $$('input[name="mode"]', form).forEach(function (i) { i.disabled = !dir && i.value === "edit"; });
            if (!dir && modeVal() === "edit") setMode("exec");
            var m = modeVal();
            if (m === "exec" && sb.status === "missing") { txt = T("rn.hint_exec_missing", { m: (sb.missing || []).join(", ") }); warn = true; }
            else if (!dir && m === "exec") txt = T("rn.hint_exec_scratch");
            else if (!dir) txt = T(form.skill_id.value ? "rn.hint_skill" : (edit ? "qr.edit_hint" : "qr.hint"));
            else if (m === "edit" || m === "exec") { txt = T(m === "exec" ? "rn.hint_exec" : "rn.hint_edit"); if (g && g.dirty > 0) { txt = T("rn.hint_dirty", { n: g.dirty }); warn = true; } }
            else txt = T("rn.hint_read");
            hint.textContent = txt;
            hint.classList.toggle("warn", warn);
          };
          var paintWs = function (r) {
            wsState = { problem: r.problem || "", git: r.git || null };
            var cls = "", txt = "", tipTxt = "";
            if (!ws.value.trim()) { /* nothing */ }
            else if (wsState.problem) { cls = "bad"; txt = T("rn.ws_" + wsState.problem); tipTxt = T("rn.ws_" + wsState.problem + "_tip"); }
            else if (wsState.git) { cls = wsState.git.dirty > 0 ? "warn" : "ok"; txt = (wsState.git.branch || "git") + (wsState.git.dirty > 0 ? " · " + T("rn.dirty", { n: wsState.git.dirty }) : ""); tipTxt = r.path; }
            else { cls = "ok"; txt = T("rn.ws_ok"); tipTxt = r.path; }
            pill.className = "pill sm " + (cls || "hidden");
            pill.textContent = txt;
            if (tipTxt) pill.setAttribute("data-tip", tipTxt); else pill.removeAttribute("data-tip");
            ws.title = r.path || "";
            paintHint();
          };
          var check = function () {
            var my = ++seq, v = ws.value.trim();
            clear.classList.toggle("hidden", !v);
            if (!v) { paintWs({}); return; }
            api("/api/tasks/workspace-check?mode=run&path=" + enc(v)).then(function (r) { if (my === seq) paintWs(r || {}); }).catch(function () { /* keep last state */ });
          };
          var setWs = function (v) { ws.value = v; clearTimeout(timer); err.textContent = ""; check(); syncEdit(); };
          var drawDirs = function () {
            $$(".rn-dir", dirsBox).forEach(function (b) { b.remove(); });
            var v = ws.value.trim(), shown = recentDirs.filter(function (d) { return d !== v; }).slice(0, 4);
            dirsBox.insertAdjacentHTML("beforeend", shown.map(function (d) { var nm = d.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || d; return '<button type="button" class="chip qr-chip rn-dir" data-dir="' + esc(d) + '" data-tip="' + esc(d) + '">' + ic("folder", "sm") + esc(nm) + "</button>"; }).join(""));
            dirsBox.classList.toggle("hidden", !shown.length);
          };
          // defaults: the picked skill's last folder, permission and provider (only into fields you haven't touched)
          var loadDefaults = function (fill) {
            var sk = form.skill_id.value;
            api("/api/runs/defaults" + (sk ? "?skill_id=" + enc(sk) : "")).then(function (d) {
              if (form.skill_id.value !== sk) return;
              recentDirs = (d && d.recent_dirs) || [];
              drawDirs();
              var l = d && d.last;
              if (fill && sk && l && !wsTouched && !ws.value.trim()) {
                setWs(l.workspace || ""); setMode(l.mode); drawDirs();
                if (l.provider && readyProviders(st).some(function (p) { return p.name === l.provider; })) form.provider.value = l.provider;
                paintHint();
              }
            }).catch(function () {});
          };
          var paintSkill = function () {
            var k = skills.filter(function (x) { return x.id === form.skill_id.value; })[0];
            argEl.textContent = k ? T("rn.arg_as", { name: k.name }) : "";
            argEl.classList.toggle("hidden", !k);
            if (k) argEl.setAttribute("data-tip", k.description || ""); else argEl.removeAttribute("data-tip");
            ta.placeholder = k ? T("rn.ph_skill", { name: k.name }) : T("qr.placeholder");
          };
          box.insertAdjacentHTML("beforeend", recent.map(function (p, i) { return '<button type="button" class="chip qr-chip" data-i="' + i + '" data-tip="' + esc(p.length > 280 ? p.slice(0, 280) + "…" : p) + '">' + esc(p.length > 36 ? p.slice(0, 36) + "…" : p) + "</button>"; }).join(""));
          var filterChips = function () {
            var q = ta.value.trim().toLowerCase(), shown = 0;
            $$("#qr-recent .qr-chip", root).forEach(function (b) { var p = recent[+b.dataset.i], on = !q || (p.toLowerCase().indexOf(q) >= 0 && p.trim().toLowerCase() !== q); b.classList.toggle("hidden", !on || shown >= 4); if (on) shown++; });
            box.classList.toggle("hidden", !shown);
          };
          var syncEdit = function () { if (reset) reset.classList.toggle("hidden", !rnEdited(orig, cur())); };
          var sync = function () { fitTextarea(ta); syncEdit(); };
          if (edit) {
            ta.value = edit.prompt; ta.setSelectionRange(ta.value.length, ta.value.length);
            if (edit.workspace) { ws.value = edit.workspace; setMode(edit.mode); wsTouched = true; }
            else setMode(edit.mode === "exec" ? "exec" : "read");
          } else wireDraft(root, ta, "qr", function () { filterChips(); sync(); });
          paintSkill(); filterChips(); sync(); check(); loadDefaults(!edit && !!pre.skill_id);
          ta.addEventListener("input", function () { filterChips(); sync(); });
          form.provider.addEventListener("change", sync);
          form.skill_id.addEventListener("change", function () { paintSkill(); paintHint(); loadDefaults(true); syncEdit(); });
          ws.addEventListener("input", function () { wsTouched = true; err.textContent = ""; clear.classList.toggle("hidden", !ws.value.trim()); clearTimeout(timer); timer = setTimeout(check, 300); syncEdit(); drawDirs(); });
          $$('input[name="mode"]', form).forEach(function (i) { i.addEventListener("change", function () { paintHint(); syncEdit(); }); });
          clear.addEventListener("click", function () { wsTouched = true; setWs(""); drawDirs(); ws.focus(); });
          dirsBox.addEventListener("click", function (e) { var b = e.target.closest(".rn-dir"); if (!b) return; wsTouched = true; setWs(b.getAttribute("data-dir")); drawDirs(); });
          if (reset) reset.addEventListener("click", function () { ta.value = orig.prompt; form.provider.value = orig.provider; editor.set(orig.skill_bindings, orig.skill_set_source); form.skill_id.value = orig.skill_id; setWs(orig.workspace); setMode(orig.mode); paintSkill(); sync(); ta.focus(); });
          box.addEventListener("click", function (e) { var b = e.target.closest(".qr-chip"); if (!b) return; ta.value = recent[+b.dataset.i]; filterChips(); sync(); ta.focus(); });
          folderPickable().then(function (ok) {
            if (!ok || !root.contains(pick)) return;
            pick.classList.remove("hidden");
            pick.addEventListener("click", function () {
              pick.disabled = true;
              api("/api/desktop/pick-folder", { method: "POST", body: { start: ws.value.trim() } }).then(function (r) { if (r && r.path) { wsTouched = true; setWs(r.path); drawDirs(); } })
                .catch(fail).then(function () { pick.disabled = false; });
            });
          });
          ta.addEventListener("keydown", function (e) { if (submitKey(e)) { e.preventDefault(); form.requestSubmit(); } });
          form.addEventListener("submit", function (e) {
            e.preventDefault();
            var body = cur();
            if (!readyProviders(st).some(function (p) { return p.name === body.provider; })) { err.textContent = T("cc.no_provider"); form.provider.focus(); return; }
            if (body.workspace && wsState.problem) { err.textContent = T("rn.ws_" + wsState.problem + "_tip"); ws.focus(); return; }
            var btn = form.querySelector('button[type="submit"]');
            btn.disabled = true;
            try { localStorage.setItem("tb_qr_provider", body.provider); } catch (e2) {}
            (body.mode === "exec" ? execFirstUse(st.sandbox, $(".modal", root) || root) : Promise.resolve(true)).then(function (go) {
              if (!go) { btn.disabled = false; return Promise.reject(null); }
              return api("/api/runs/quick", { method: "POST", body: body });
            }).then(function (r) {
              if (!edit) draftClear("qr");
              close(); openStarted(r, "toast.quick_started");
            }).catch(function (ex) {
              btn.disabled = false;
              if (ex === null) return; // the first-use explanation was closed
              var pr = ex && ex.data && ex.data.problem;
              if (pr) { err.textContent = T("rn.ws_" + pr + "_tip"); return; }
              fail(ex);
            });
          });
        }, syncHomeQuickDraft);
    }).catch(fail);
  }
  // modeSegHtml: the three permission levels (只读 / 可修改 / 可执行命令) as a
  // segmented control; radios named name, cur checked. sb is the sandbox status from
  // /api/settings: on a platform with none (Windows) 可执行命令 carries a small
  // 「无系统沙箱」 mark, with sandbox parts missing a warning mark (details in tooltips).
  function modeSegHtml(name, cur, sb) {
    sb = sb || {};
    var opt = function (v, icon, extra) {
      return '<label data-tip="' + esc(T("rn." + v + "_tip")) + '"><input type="radio" name="' + esc(name) + '" value="' + v + '"' + (cur === v ? " checked" : "") + "><span>" + ic(icon, "sm") + esc(T("rn." + v)) + (extra || "") + "</span></label>";
    };
    var mark = sb.status === "none" ? '<b class="rn-nosb" data-tip="' + esc(T("rn.nosb_tip")) + '">' + esc(T("rn.nosb")) + "</b>" :
      sb.status === "missing" ? '<b class="rn-nosb bad" data-tip="' + esc(T("pl.sb_missing_tip", { m: (sb.missing || []).join(", ") })) + '">' + esc(T("rn.sb_missing")) + "</b>" : "";
    return '<span class="rn-mode" role="radiogroup" aria-label="' + esc(T("rn.mode")) + '">' + opt("read", "eye") + opt("edit", "edit") + opt("exec", "terminal", mark) + "</span>";
  }
  // execFirstUse: on a platform without a system sandbox (native Windows) the first
  // command run explains once what protects it instead; after that 可执行命令 is
  // used normally. Inside an open dialog (box) the explanation takes the place of
  // its buttons; elsewhere it is an in-app dialog. Resolves false if cancelled.
  function execFirstUse(sb, box) {
    var key = "tb_exec_nosb_ok";
    try { if (!sb || sb.status !== "none" || localStorage.getItem(key)) return Promise.resolve(true); } catch (e) { return Promise.resolve(true); }
    var ack = function (ok) { if (ok) { try { localStorage.setItem(key, "1"); } catch (e) {} } return ok; };
    if (!box) return confirmDialog({ title: T("rn.nosb_title"), body: T("rn.nosb_body"), ok: T("rn.nosb_ok"), icon: "shield" }).then(ack);
    return new Promise(function (resolve) {
      var all = $$(".actions", box), footer = all.length ? all[all.length - 1] : null;
      var bar = document.createElement("div");
      bar.className = "nosb-box"; bar.setAttribute("role", "alertdialog"); bar.setAttribute("aria-labelledby", "nosb-t");
      bar.innerHTML = '<div class="nosb-head"><span class="ico warn">' + ic("shield", "sm") + '</span><strong id="nosb-t">' + esc(T("rn.nosb_title")) + "</strong></div>" +
        '<p class="nosb-body">' + esc(T("rn.nosb_body")) + '</p><div class="actions"><button type="button" class="btn ghost" id="nosb-no">' + esc(T("btn.cancel")) + '</button><button type="button" class="btn primary" id="nosb-ok">' + esc(T("rn.nosb_ok")) + "</button></div>";
      if (footer) { footer.classList.add("hidden"); footer.parentNode.insertBefore(bar, footer.nextSibling); } else box.appendChild(bar);
      var done = function (ok) { bar.remove(); if (footer) footer.classList.remove("hidden"); resolve(ack(ok)); };
      $("#nosb-ok", bar).addEventListener("click", function () { done(true); });
      $("#nosb-no", bar).addEventListener("click", function () { done(false); });
      $("#nosb-ok", bar).focus();
    });
  }
  // rnEdited: has the edit-&-rerun form moved away from the original run? Trailing
  // whitespace and CRLF vs LF don't count (pasting on Windows adds \r\n).
  function rnEdited(orig, c) {
    if (!orig) return false;
    var bindingsChanged=JSON.stringify(c.skill_bindings||[])!==JSON.stringify(orig.skill_bindings||[]) || JSON.stringify(c.skill_set_source)!==JSON.stringify(orig.skill_set_source);
    return bindingsChanged || qrEdited(orig, c.prompt, c.provider) || (c.skill_id || "") !== (orig.skill_id || "") || (c.workspace || "") !== (orig.workspace || "") || (c.workspace ? c.mode : "") !== (orig.workspace ? orig.mode : "");
  }
  // qrEdited: has the edit-&-rerun form moved away from the original run? Trailing
  // whitespace and CRLF vs LF don't count (pasting on Windows adds \r\n).
  function qrEdited(orig, prompt, provider) {
    if (!orig) return false;
    var norm = function (s) { return String(s || "").replace(/\r\n?/g, "\n").replace(/\s+$/, ""); };
    return norm(prompt) !== norm(orig.prompt) || (provider || "") !== (orig.provider || "");
  }
  // fitTextarea grows a textarea with its content up to 40% of the window (then it scrolls),
  // so a long prompt is readable without dragging the corner. The dialog body scrolls too.
  function fitTextarea(ta) {
    var max = Math.max(96, Math.round(window.innerHeight * 0.4));
    ta.style.height = "";
    ta.style.height = Math.min(max, ta.scrollHeight + 2) + "px";
  }
  // Prompt drafts (idea: Open WebUI Chat.svelte saveDraft / restoreChatInput / clearDraft):
  // what you type in a fresh quick run or new task is kept in sessionStorage (this window only,
  // gone when it closes or on sign-out), written 400 ms after you stop typing and right away
  // when the dialog closes or the page hides. Opening the dialog again brings it back with a
  // small "Draft restored · Clear" tag; a successful submit removes it.
  var DRAFT_MAX = 32000, DRAFT_TTL = 7 * 864e5, draftPend = null;
  function draftKey(kind) { return "tb_draft_" + kind; }
  function draftLoad(kind, now) {
    try {
      var d = JSON.parse(sessionStorage.getItem(draftKey(kind)) || "null");
      if (d && typeof d.text === "string" && d.text.trim() && typeof d.at === "number" && (now || Date.now()) - d.at < DRAFT_TTL) return d;
    } catch (e) { /* ignore */ }
    return null;
  }
  function draftSave(kind, text, now) {
    try {
      if (!String(text).trim() || text.length > DRAFT_MAX) sessionStorage.removeItem(draftKey(kind));
      else sessionStorage.setItem(draftKey(kind), JSON.stringify({ text: text, at: now || Date.now() }));
    } catch (e) { /* storage full or disabled: drafts are a nicety */ }
  }
  function draftClear(kind) { if (draftPend && draftPend.kind === kind) { clearTimeout(draftPend.t); draftPend = null; } try { sessionStorage.removeItem(draftKey(kind)); } catch (e) { /* ignore */ } }
  function draftFlush() { var p = draftPend; draftPend = null; if (p) { clearTimeout(p.t); p.fn(); } }
  function draftsClearAll() { draftPend = null; try { Object.keys(sessionStorage).filter(function (k) { return /^tb_draft_/.test(k); }).forEach(function (k) { sessionStorage.removeItem(k); }); } catch (e) { /* ignore */ } }
  window.addEventListener("pagehide", draftFlush);
  document.addEventListener("visibilitychange", function () { if (document.hidden) draftFlush(); });
  // draftTagHtml goes at the right end of the prompt label row; wireDraft fills the textarea
  // from the draft (if any) and keeps saving. onReset runs after "Clear" empties the field.
  function draftTagHtml() {
    return '<span class="draft-tag hidden" id="dr-tag"><span class="dr-txt" tabindex="0" data-tip="">' + esc(T("dr.restored")) + '</span><button type="button" class="linkish" id="dr-clear">' + esc(T("dr.clear")) + "</button></span>";
  }
  function wireDraft(root, ta, kind, onReset) {
    var tag = $("#dr-tag", root), d = draftLoad(kind);
    if (d && !ta.value.trim()) {
      ta.value = d.text; ta.setSelectionRange(ta.value.length, ta.value.length);
      $(".dr-txt", tag).setAttribute("data-tip", T("dr.tip", { t: when(new Date(d.at).toISOString()) }));
      tag.classList.remove("hidden");
    }
    var save = function () { draftSave(kind, ta.value); };
    ta.addEventListener("input", function (e) {
      if (!e.isTrusted) return;
      if (!ta.value.trim()) { draftClear(kind); tag.classList.add("hidden"); return; }
      if (draftPend) clearTimeout(draftPend.t);
      draftPend = { kind: kind, fn: save, t: setTimeout(draftFlush, 400) };
    });
    $("#dr-clear", root).addEventListener("click", function () {
      draftClear(kind); ta.value = ""; tag.classList.add("hidden");
      if (onReset) onReset();
      ta.focus();
    });
  }

