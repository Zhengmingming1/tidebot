  // ---------- LogView: categories, filters, search, windowed rendering, SSE resume ----------
  var LOG_WINDOW = 1500;
  function classifyLog(text) {
    var m = /^\[(\d\d:\d\d:\d\d)\]\s(.*)$/.exec(text), ts = "", body = text;
    if (m) { ts = m[1]; body = m[2]; }
    var kind = "sys";
    if (/^result error|run finished: (failed|timeout|canceled)|^error/i.test(body)) kind = "err";
    else if (/^user: tool result/.test(body)) kind = "result";
    else if (/^assistant: tool /.test(body)) kind = "tool";
    else if (/^assistant: /.test(body)) kind = "text";
    return { ts: ts, kind: kind, text: body };
  }
  function hlText(s, q) {
    if (!q) return esc(s);
    var low = s.toLowerCase(), out = "", i = 0, j;
    while ((j = low.indexOf(q, i)) >= 0) { out += esc(s.slice(i, j)) + "<mark>" + esc(s.slice(j, j + q.length)) + "</mark>"; i = j + q.length; }
    return out + esc(s.slice(i));
  }
  function logView(host, runId, live, onEnd) {
    var KINDS = ["all", "text", "tool", "result", "sys", "err"];
    var lines = [], view = [], filter = "all", q = "", matches = [], cur = -1, start = 0, unseen = 0, pending = false, lastId = "", ended = false, retry = 0;
    host.innerHTML = '<div class="lv-bar"><div class="filters lv-f">' + KINDS.map(function (k) { return '<button data-k="' + k + '" class="' + (k === "all" ? "active" : "") + '">' + esc(T("lv.k_" + k)) + ' <span class="cnt" data-c="' + k + '">0</span></button>'; }).join("") + "</div>" +
      '<span class="grow"></span><label class="search-in sm">' + ic("search", "sm") + '<input id="lv-q" type="search" maxlength="200" placeholder="' + esc(T("lv.search")) + '" aria-label="' + esc(T("lv.search")) + '"></label><span class="muted num" id="lv-pos"></span>' +
      '<button class="icon-btn sm" id="lv-prev" aria-label="' + esc(T("lv.prev")) + '" data-tip="' + esc(T("lv.prev")) + ' · N">' + ic("chevUp", "sm") + '</button><button class="icon-btn sm" id="lv-next" aria-label="' + esc(T("lv.next")) + '" data-tip="' + esc(T("lv.next")) + ' · n">' + ic("chevDown", "sm") + '</button></div>' +
      '<div class="lv-wrap"><div class="log" id="log" aria-live="off" tabindex="0"><button class="lv-more hidden" id="lv-more"></button><div id="lv-rows"></div></div><button class="lv-new hidden" id="lv-new"></button></div>';
    // Copy / download / full screen sit in the card header next to the live state, so the
    // filter + search bar stays on one row even in the two-column run page at 1024px.
    var tools = document.createElement("span"), sec = host.closest("section"), head = sec && $(".card-head", sec);
    tools.className = "lv-tools";
    tools.innerHTML = '<span class="lv-sep"></span>' + '<button class="icon-btn sm" id="lv-copy" aria-label="' + esc(T("lv.copy")) + '" data-tip="' + esc(T("lv.copy")) + '">' + ic("copy", "sm") + '</button><button class="icon-btn sm" id="lv-dl" aria-label="' + esc(T("lv.download")) + '" data-tip="' + esc(T("lv.download")) + '">' + ic("download", "sm") + '</button><button class="icon-btn sm" id="lv-full" aria-label="' + esc(T("lv.full")) + '" data-tip="' + esc(T("lv.full")) + '">' + ic("maximize", "sm") + "</button>";
    if (head) { $$(".lv-tools", head).forEach(function (el) { el.remove(); }); head.appendChild(tools); } else $(".lv-bar", host).appendChild(tools);
    var log = $("#log", host), rows = $("#lv-rows", host), more = $("#lv-more", host), newBtn = $("#lv-new", host);
    // Hidden panes have zero layout metrics. Buffer their output without resetting the
    // reader's scroll position; resume following only if they were already at the end.
    var followOnShow = true, savedTop = 0;
    var atBottom = function () { return !host.closest("[hidden]") && log.scrollTop + log.clientHeight >= log.scrollHeight - 24; };
    // Rows read like a transcript: the "assistant:" / "user:" prefixes become the left
    // marker, a timestamp repeated from the line above is dimmed out, and a tool call shows
    // its tool as a chip plus a one-line preview of the input (click for the full input).
    var lastTs = "";
    var toolPreview = function (raw) {
      try { var o = JSON.parse(raw); if (o && typeof o === "object") return String(o.command || o.file_path || o.path || o.pattern || o.url || o.query || o.description || raw); } catch (e) {}
      return raw;
    };
    var rowHtml = function (l, idx) {
      var body = l.text, extra = "", tool = "";
      if (l.kind === "tool") {
        var mm = /^assistant: tool (\S+)\s?(.*)$/.exec(body) || [null, "", body];
        tool = mm[1]; extra = mm[2]; body = "";
      } else if (l.kind === "text") body = body.replace(/^assistant: /, "");
      else if (l.kind === "result") body = "↳ " + body.replace(/^user: /, "");
      var same = l.ts && l.ts === lastTs; lastTs = l.ts || lastTs;
      return '<div class="ln k-' + l.kind + (idx === matches[cur] ? " cur" : "") + (extra ? " fold" : "") + '" data-i="' + idx + '">' + '<span class="ts' + (same ? " same" : "") + '">' + esc(l.ts || "") + "</span>" +
        '<span class="tx">' + (tool ? '<span class="tool-chip">' + esc(tool) + "</span>" + (extra ? '<span class="args-pre">' + hlText(toolPreview(extra), q) + '</span><span class="args">' + hlText(extra, q) + "</span>" : "") : hlText(body, q)) + "</span></div>";
    };
    var counts = function () {
      var c = { all: lines.length, text: 0, tool: 0, result: 0, sys: 0, err: 0 };
      lines.forEach(function (l) { c[l.kind]++; });
      KINDS.forEach(function (k) { var el = $('[data-c="' + k + '"]', host); if (el) el.textContent = c[k]; });
    };
    var keep = function (l) { return filter === "all" || l.kind === filter; };
    var computeMatches = function () {
      matches = [];
      if (q) view.forEach(function (li, vi) { var l = lines[li]; if (l.text.toLowerCase().indexOf(q) >= 0) matches.push(li); });
      $("#lv-pos", host).textContent = q ? (matches.length ? (cur + 1) + "/" + matches.length : T("lv.none")) : "";
    };
    var paint = function (keepScroll) {
      view = []; lines.forEach(function (l, i) { if (keep(l)) view.push(i); });
      computeMatches();
      if (start > Math.max(0, view.length - LOG_WINDOW)) start = Math.max(0, view.length - LOG_WINDOW);
      var end = Math.min(view.length, start + LOG_WINDOW);
      lastTs = "";
      rows.innerHTML = view.slice(start, end).map(function (li) { return rowHtml(lines[li], li); }).join("") || '<div class="muted ln">' + esc(T(lines.length ? "lv.empty_filter" : "lv.waiting")) + "</div>";
      more.classList.toggle("hidden", start === 0);
      more.textContent = T("lv.earlier", { n: Math.min(start, LOG_WINDOW) });
      if (!keepScroll) log.scrollTop = log.scrollHeight;
    };
    var flush = function () {
      pending = false;
      var stick = atBottom(), added = [];
      for (var i = flush.from; i < lines.length; i++) if (keep(lines[i])) { view.push(i); added.push(i); }
      flush.from = lines.length;
      counts();
      if (!added.length) return;
      if (q) computeMatches();
      if (stick) {
        if (rows.firstChild && rows.firstChild.classList && rows.firstChild.classList.contains("muted")) rows.innerHTML = "";
        rows.insertAdjacentHTML("beforeend", added.map(function (li) { return rowHtml(lines[li], li); }).join(""));
        var extra = rows.childElementCount - LOG_WINDOW;
        if (extra > 200) { for (var k = 0; k < extra; k++) rows.firstChild.remove(); start += extra; more.classList.remove("hidden"); more.textContent = T("lv.earlier", { n: Math.min(start, LOG_WINDOW) }); }
        log.scrollTop = log.scrollHeight;
      } else {
        unseen += added.length;
        newBtn.textContent = "↓ " + T("lv.new_n", { n: unseen });
        newBtn.classList.remove("hidden");
      }
    };
    flush.from = 0;
    var push = function (text) { lines.push(classifyLog(text)); if (!pending) { pending = true; requestAnimationFrame(flush); } };
    var toBottom = function () { unseen = 0; newBtn.classList.add("hidden"); start = Math.max(0, view.length - LOG_WINDOW); paint(false); };
    host._logVisibility = function (visible) {
      if (!visible) { followOnShow = atBottom(); savedTop = log.scrollTop; }
      else if (followOnShow) toBottom();
      else log.scrollTop = savedTop;
    };
    newBtn.addEventListener("click", toBottom);
    log.addEventListener("scroll", function () { if (atBottom() && unseen) { unseen = 0; newBtn.classList.add("hidden"); if (start + rows.childElementCount < view.length) toBottom(); } });
    more.addEventListener("click", function () { var h = log.scrollHeight; start = Math.max(0, start - LOG_WINDOW); paint(true); log.scrollTop += log.scrollHeight - h; });
    rows.addEventListener("click", function (e) { var ln = e.target.closest(".ln.fold"); if (ln) ln.classList.toggle("open"); });
    $$("[data-k]", host).forEach(function (b) { b.addEventListener("click", function () { filter = b.getAttribute("data-k"); $$("[data-k]", host).forEach(function (x) { x.classList.toggle("active", x === b); }); start = Infinity; cur = -1; paint(false); }); });
    var jump = function (d) {
      if (!matches.length) return;
      cur = (cur + d + matches.length) % matches.length;
      var vi = view.indexOf(matches[cur]);
      if (vi < start || vi >= start + LOG_WINDOW) start = Math.max(0, vi - Math.floor(LOG_WINDOW / 2));
      paint(true);
      var el = $('[data-i="' + matches[cur] + '"]', rows);
      if (el) el.scrollIntoView({ block: "center" });
    };
    imeInput($("#lv-q", host), 150, function (val) { q = val.trim().toLowerCase(); cur = -1; paint(true); if (q) jump(1); });
    $("#lv-q", host).addEventListener("keydown", function (e) { if (e.key === "Enter") { e.preventDefault(); jump(e.shiftKey ? -1 : 1); } });
    $("#lv-next", host).addEventListener("click", function () { jump(1); });
    $("#lv-prev", host).addEventListener("click", function () { jump(-1); });
    host.addEventListener("keydown", function (e) {
      if (e.target.tagName === "INPUT") return;
      if (e.key === "n") { e.preventDefault(); jump(1); } else if (e.key === "N") { e.preventDefault(); jump(-1); }
      else if (e.key === "/") { e.preventDefault(); $("#lv-q", host).focus(); }
    });
    var jsonl = function () { return lines.map(function (l) { return JSON.stringify({ time: l.ts, kind: l.kind, text: l.text }); }).join("\n") + "\n"; };
    $("#lv-copy", tools).addEventListener("click", function () { navigator.clipboard.writeText(jsonl()).then(function () { toast(T("lv.copied", { n: lines.length })); }, fail); });
    $("#lv-dl", tools).addEventListener("click", function () {
      saveBlob(new Blob([jsonl()], { type: "application/x-ndjson" }), runId + ".jsonl").catch(fail);
    });
    $("#lv-full", tools).addEventListener("click", function () { sec.classList.toggle("log-full"); log.focus(); });
    document.addEventListener("keydown", function esc1(e) { if (!document.contains(host)) { document.removeEventListener("keydown", esc1); return; } if (e.key === "Escape" && sec.classList.contains("log-full")) sec.classList.remove("log-full"); });
    paint(false);
    // SSE with resume: the browser resends Last-Event-ID on its own reconnects; if it gives up we reconnect with ?from=.
    function connect() {
      stopStream();
      var es = new EventSource("/api/runs/" + enc(runId) + "/events" + (lastId ? "?from=" + enc(lastId) : ""));
      state.es = es;
      es.onmessage = function (ev) { if (ev.lastEventId) lastId = ev.lastEventId; retry = 0; push(ev.data); };
      es.addEventListener("end", function () { ended = true; es.close(); state.es = null; if (onEnd) onEnd(); });
      es.addEventListener("idle", function () { es.close(); state.es = null; state.esPaused = true; if (onEnd) onEnd("idle"); });
      es.onerror = function () {
        if (ended) return;
        netCheck();
        if (es.readyState === 2 && document.contains(host)) { retry++; setTimeout(function () { if (!ended && document.contains(host)) connect(); }, Math.min(10000, 1000 * retry)); }
      };
    }
    state.esResume = function () { if (!ended && document.contains(host)) connect(); };
    connect();
    return { push: push, lines: lines };
  }
  // Run dialog runs: which skill (and version) ran, in which folder, read-only or not.
  function runSkillKv(r) {
    var out = "";
    if(r.skill_set_source)out += '<dt>'+esc(T("ss.source"))+'</dt><dd><a href="#/skill-sets">'+esc(r.skill_set_source.set_id)+'</a><br><code>'+esc(r.skill_set_source.version_id)+'</code> · '+esc(T("ss.state_"+r.skill_set_source.state))+'</dd>';
    if((r.skill_evidence||[]).length)out += '<dt>'+esc(T("ss.evidence"))+'</dt><dd><p class="hint">'+esc(T("ss.evidence_hint"))+'</p><ul class="list-plain">'+r.skill_evidence.map(function(e){var b=(r.skill_bindings||[]).filter(function(x){return x.skill_id===e.skill_id;})[0]||{};return '<li>'+esc(b.skill_name||e.skill_id)+' · '+esc(T(e.role==="primary"?"ss.primary":"ss.auxiliary"))+' · '+esc(T("ss.loaded"))+'<br><code>'+esc(e.version_id)+'</code></li>';}).join('')+'</ul></dd>';
    if (r.skill_id) out += "<dt>" + esc(T("rn.skill")) + '</dt><dd><a href="#/skills/' + enc(r.skill_id) + '">' + esc(r.skill_name || r.skill_id) + "</a>" + (r.skill_version ? ' <span class="tag perm">v' + esc(r.skill_version) + "</span>" : "") + "</dd>";
    if (r.workspace) out += "<dt>" + esc(T("rn.folder")) + '</dt><dd class="rn-kv-ws"><code class="mono" data-tip="' + esc(r.workspace) + '">' + esc(r.workspace) + "</code>" + runModeTag(r.run_mode) + "</dd>";
    else if (r.run_mode === "exec") out += "<dt>" + esc(T("rn.mode")) + '</dt><dd class="rn-kv-ws">' + runModeTag("exec") + "</dd>";
    return out;
  }
  // This section is explicitly current task configuration; run metadata above it is a snapshot.
  function runTaskInspectorHtml(t, items, form) {
    var pending = items && items.filter(function (i) { return i.status === "pending" || i.status === "needs_decision"; });
    return '<h3>' + esc(T("rw.current_task")) + '</h3><dl class="kv"><dt>' + esc(T("rw.current_version")) + '</dt><dd><a href="#/tasks/' + enc(t.id) + '/history">v' + esc(t.version_no || "—") + '</a></dd>' +
      '<dt>' + esc(T("col.schedule")) + '</dt><dd>' + esc(cronHuman(t.cron)) + '<small>' + esc(tzLabel()) + '</small></dd><dt>' + esc(T("rw.task_budget")) + '</dt><dd>' + (form ? esc(form.budget_usd ? usd(form.budget_usd, 2) : T("tf.no_cap")) : "—") + '</dd></dl>' +
      (pending === null ? '<p class="muted">' + esc(T("rw.pending_unknown")) + '</p>' : pending.length ? '<div class="rw-pending"><strong>' + ic("sparkle", "sm") + esc(T("rw.pending_n", { n: pending.length })) + '</strong><ul>' + pending.slice(0, 3).map(function (i) {
        var target = (t.constraints || [])[(t.constraint_ids || []).indexOf(i.target_spec_item)] || i.target_spec_item;
        return '<li>' + esc(i.content || target) + '<small>' + esc(T(i.status === "needs_decision" ? "req.conflict" : i.scope === "next_run" ? "req.next_only" : "req.permanent")) + '</small></li>';
      }).join("") + '</ul><a class="btn primary sm" href="#/tasks/' + enc(t.id) + '/thread">' + esc(T("rw.review")) + '</a></div>' : '') +
      '<a class="rw-task-link" href="#/tasks/' + enc(t.id) + '">' + esc(T("rw.open_task")) + ic("chev", "sm") + '</a>';
  }
  var runWorkspaceResizeOff = null;
  function bindRunWorkspace(r, live) {
    var ws = $("#run-workspace"), layout = $("#rw-layout"), aside = $("#rd-inspector"), toggle = $("#rd-inspect-toggle");
    var tabs = $$("[data-run-view]", ws), answer = $("#rw-answer"), log = $("#rw-log"), loaded = false;
    // Keep both panels mounted: changing views must not discard the SSE stream or its scroll state.
    function paintTabs(view) {
      var lv = $("#lv", log), wasHidden = log.hidden;
      if (view !== "log" && !wasHidden && lv && lv._logVisibility) lv._logVisibility(false);
      answer.hidden = view === "log"; log.hidden = view !== "log";
      if (view === "log" && wasHidden && lv && lv._logVisibility) lv._logVisibility(true);
      answer.setAttribute("aria-labelledby", "rw-tab-" + (view === "diff" ? "diff" : "answer"));
      tabs.forEach(function (b) { var on = b.getAttribute("data-run-view") === view; b.setAttribute("aria-selected", String(on)); b.tabIndex = on ? 0 : -1; b.classList.toggle("active", on); });
    }
    function select(view, focus) {
      paintTabs(view);
      if (view !== "log") {
        var mode = $('[data-ans-mode="' + (view === "diff" ? "diff" : "preview") + '"]', answer);
        if (mode && mode.getAttribute("aria-checked") !== "true") mode.click();
      }
      if (focus) $("#rw-tab-" + view).focus();
    }
    tabs.forEach(function (b) { b.addEventListener("click", function () { select(b.getAttribute("data-run-view"), true); }); });
    $(".rw-tabs", ws).addEventListener("keydown", function (e) {
      if (["ArrowLeft", "ArrowRight", "Home", "End"].indexOf(e.key) < 0) return;
      var enabled = tabs.filter(function (b) { return !b.disabled; }), i = enabled.indexOf(e.target);
      if (i < 0) return;
      e.preventDefault();
      i = e.key === "Home" ? 0 : e.key === "End" ? enabled.length - 1 : (i + (e.key === "ArrowRight" ? 1 : enabled.length - 1)) % enabled.length;
      select(enabled[i].getAttribute("data-run-view"), true);
    });
    // Answer radios redraw and focus the selected control for both mouse and arrow keys.
    answer.addEventListener("focusin", function (e) { var b = e.target.closest("[data-ans-mode]"); if (b && b.getAttribute("aria-checked") === "true") paintTabs(b.getAttribute("data-ans-mode") === "diff" ? "diff" : "answer"); });
    paintTabs(state.chgJump === r.id ? "answer" : live || !r.result ? "log" : ansMode(r) === "diff" ? "diff" : "answer");
    if (!$("#rd-calc")) answer.insertAdjacentHTML("beforeend", '<section id="rd-calc" hidden></section>');
    function inspect(open) {
      if (!open && aside.contains(document.activeElement)) toggle.focus();
      aside.hidden = !open; layout.classList.toggle("inspector-open", open); toggle.setAttribute("aria-expanded", String(open)); toggle.classList.toggle("active", open);
      if (!open || !r.task_id || loaded) return;
      loaded = true;
      var host = $("#rw-current-task"), base = "/api/tasks/" + enc(r.task_id);
      Promise.all([api(base), api(base + "/amendments").then(function (items) { return items || []; }).catch(function () { return null; }), api(base + "/form").catch(function () { return null; })]).then(function (d) {
        if (document.contains(host)) host.innerHTML = runTaskInspectorHtml(d[0], d[1], d[2] && d[2].form);
      }).catch(function (e) { if (document.contains(host)) { host.innerHTML = '<h3>' + esc(T("rw.current_task")) + '</h3>' + errorState(e.message); loaded = false; } });
    }
    toggle.addEventListener("click", function () { inspect(aside.hidden); });
    aside.addEventListener("keydown", function (e) { if (e.key === "Escape") { e.preventDefault(); inspect(false); toggle.focus(); } });
    if (runWorkspaceResizeOff) runWorkspaceResizeOff();
    var media = window.matchMedia("(min-width: 1101px)");
    var resize = function () { if (document.contains(ws)) inspect(media.matches); else { runWorkspaceResizeOff(); runWorkspaceResizeOff = null; } };
    media.addEventListener("change", resize);
    runWorkspaceResizeOff = function () { media.removeEventListener("change", resize); };
    inspect(media.matches);
  }
  function renderRunDetail(id) {
    return api("/api/runs/" + enc(id)).then(runChatPreload).then(function (r) {
      var live = r.status === "running" || r.status === "queued", queued = r.status === "queued";
      // a session's later turn: rerun / edit / save as task make no sense without the session
      var turn = r.trigger === "session" || !!(r.chat_session && r.chat_session.turn > 0);
      mount('<div class="run-workspace" id="run-workspace"><header class="rw-head"><div class="rw-title"><h1>' + esc(runTitle(r)) + '</h1><div>' + statusPill(r.status, r.queue_pos, r.error) + '<span class="muted">' + esc(when(r.started_at || r.created_at)) + '</span>' + runChatOfHtml(r) + '</div></div><div class="rw-actions">' +
        (live ? '<button type="button" class="btn sm danger rd-stop" id="rd-stop" data-tip="' + esc(T(queued ? "run.dequeue_tip" : "run.stop_tip")) + '">' + ic("stop", "sm") + esc(T(queued ? "run.dequeue" : "run.stop")) + "</button>" : "") +
        (r.task_id ? "" : '<button type="button" class="btn sm" id="rd-save-task" data-tip="' + esc(T("rd.save_task_tip")) + '">' + ic("tasks", "sm") + esc(T("rd.save_task")) + "</button>") +
        (turn ? "" : '<button type="button" class="btn sm" id="rd-rerun"' + (live ? " disabled" : "") + ' data-tip="' + esc(T(r.task_id ? "rd.rerun_tip" : "rd.rerun_tip_q")) + ' · r">' + ic("refresh", "sm") + esc(T("runs.rerun")) + "</button>") +
        (r.task_id || turn ? "" : '<button type="button" class="icon-btn sm" id="rd-edit" aria-label="' + esc(T("qr.edit_title")) + '" data-tip="' + esc(T("rd.edit_tip")) + ' · e">' + ic("edit", "sm") + "</button>") +
        '<button type="button" class="btn sm" id="rd-inspect-toggle" aria-expanded="false" aria-controls="rd-inspector">' + ic("info", "sm") + esc(T("rw.inspector")) + '</button></div></header><div class="grid g4 rw-stats">' +
        '<div class="card stat"><small>' + esc(T("col.status")) + "</small><strong>" + statusPill(r.status, r.queue_pos, r.error) + "</strong><span class=\"ell\"" + (r.workspace ? ' data-tip="' + esc(r.workspace) + '"' : "") + ">" + esc(runCtxText(r, true)) + "</span></div>" +
        '<div class="card stat"><small>' + esc(T("col.duration")) + "</small><strong>" + esc(queued ? T("rd.waiting") : live ? T("rd.running") : r.started_at ? dur(r.duration_ms) : "—") + "</strong><span>" + esc(r.started_at || !r.finished_at ? T(queued ? "rd.queued_at" : "rd.started", { t: when(r.started_at || r.created_at) }) : T("rd.never_started")) + "</span></div>" +
        '<div class="card stat" id="rd-cost">' + runCostStat(r, live) + "</div>" +
        '<div class="card stat" id="rd-tok">' + runTokStat(r, live) + "</div></div>" +
        '<div class="rw-layout" id="rw-layout"><div class="rw-main"><div class="rw-tabs" role="tablist" aria-label="' + esc(T("rw.views")) + '">' +
        '<button type="button" role="tab" id="rw-tab-answer" data-run-view="answer" aria-controls="rw-answer">' + esc(T("rd.answer")) + '</button>' +
        '<button type="button" role="tab" id="rw-tab-log" data-run-view="log" aria-controls="rw-log">' + esc(T("rd.log")) + '</button>' +
        '<button type="button" role="tab" id="rw-tab-diff" data-run-view="diff" aria-controls="rw-answer"' + (!r.result || !r.answer_prev ? ' disabled data-tip="' + esc(T("rw.no_previous")) + '"' : "") + '>' + esc(T("rd.ans_diff")) + '</button></div><div class="rw-panel" id="rw-answer" role="tabpanel" aria-labelledby="rw-tab-answer">' +
        runAgentCfgHtml(r) + runAnswerHtml(r, ansMode(r)) +
        runChangesHtml(r) +
        costCalcHtml(r) + contextPlanHtml(r) + costPolicySnapshotHtml(r) +
        (!r.result ? '<div class="rw-no-answer">' + emptyState("chat", T("rw.no_answer"), T(live ? "rw.answer_running" : "rw.answer_missing")) + "</div>" : "") + '</div>' +
        '<section class="rw-panel card" id="rw-log" role="tabpanel" aria-labelledby="rw-tab-log"><div class="card-head"><span class="ico">' + ic("terminal") + '</span><h2>' + esc(T("rd.log")) + '</h2><span class="link" id="live-ind">' + (live ? '<span class="live">' + esc(T("rd.live")) + "</span>" : '<span class="muted">' + esc(T("rd.finished")) + "</span>") + '</span></div><div id="lv"></div></section>' + runChatHtml(r) + '</div>' +
        '<aside class="rw-inspector" id="rd-inspector" aria-label="' + esc(T("rw.inspector")) + '" hidden><section><div class="card-head rd-head"><h3>' + esc(T("rw.run_snapshot")) + "</h3>" + idChip(T("rd.run_id"), r.id) + '</div><dl class="kv"><dt>' + esc(T("col.task")) + "</dt><dd>" + (r.task_id ? '<a href="#/tasks/' + enc(r.task_id) + '">' + esc(r.task_name) + "</a>" : esc(T("run.adhoc"))) + "</dd>" +
        (r.version_no ? '<dt>' + esc(T("rw.run_version")) + '</dt><dd>v' + esc(r.version_no) + '</dd>' : '') +
        runSkillKv(r) + "<dt>" + esc(T("f.provider")) + "</dt><dd>" + esc(r.provider || "—") + "</dd><dt>" + esc(T("f.model")) + "</dt><dd>" + esc(r.model || "—") + "</dd>" + runPromptHtml(r) + (r.error ? "<dt>" + esc(T("rd.error")) + '</dt><dd class="err">' + esc(runErr(r.error)) + "</dd>" : "") + "</dl></section>" +
        (r.task_id ? '<section id="rw-current-task"><h3>' + esc(T("rw.current_task")) + '</h3>' + skeleton("list") + '</section>' : '') + '</aside></div></div>',
        "runs", [[T("nav.runs"), "#/runs"], [runTitle(r)]]);
      bumpIdle();
      if ($("#rd-rerun")) $("#rd-rerun").addEventListener("click", function (e) {
        var b = e.currentTarget; b.disabled = true;
        api("/api/runs/" + enc(id) + "/rerun", { method: "POST", body: {} }).then(function (res) {
          openStarted(res, "runs.rerun_started");
        }).catch(function (err) { b.disabled = false; startFail(err); });
      });
      if ($("#rd-stop")) $("#rd-stop").addEventListener("click", function (e) {
        var b = e.currentTarget; b.disabled = true;
        stopRun(id, r.status).then(function (y) { if (y) refresh(); else b.disabled = false; }).catch(function (err) { b.disabled = false; fail(err); });
      });
      if ($("#rd-ans")) bindRunAnswer($("#rd-ans"), r);
      if ($("#rd-chg")) bindRunChanges($("#rd-chg"), r);
      bindRunWorkspace(r, live);
      capLoadRunBindings(id, $("#rd-inspector"));
      // opened from a list row's change badge: bring the card into view
      if (state.chgJump === id && $("#rd-chg")) $("#rd-chg").scrollIntoView({ block: "start" });
      state.chgJump = null;
      bindRunPrompt(r);
      bindRunChat(r);
      if ($("#rd-edit")) $("#rd-edit").addEventListener("click", function () { quickRunModal({ prompt: r.prompt, provider: r.provider, skill_id: r.skill_id, skill_bindings: r.skill_bindings, skill_set_source: cleanSetSource(r.skill_set_source), workspace: r.workspace, mode: r.run_mode }); });
      // 存为任务: the run's request, folder, permission level and skills pinned to the
      // versions it used; a run with a session takes the session's (a version adopted
      // there is pinned instead), still with this run's own request.
      if ($("#rd-save-task")) $("#rd-save-task").addEventListener("click", function (e) {
        var b = e.currentTarget, own = { prompt: r.prompt, provider: r.provider, skill_id: r.skill_id, skill_bindings: r.skill_bindings, skill_set_source: cleanSetSource(r.skill_set_source), workspace: r.workspace, run_mode: r.run_mode || "" };
        var cs = r.chat_session;
        if (!cs || !/^[\w-]{1,64}$/.test(cs.id || "")) { taskFormModal(null, own); return; }
        b.disabled = true;
        api("/api/chat-sessions/" + enc(cs.id) + "/task-preset?from=run", { fresh: true }).then(function (p) {
          taskFormModal(null, Object.assign({}, p, { prompt: r.prompt || p.prompt, provider: r.provider || p.provider, skill_set_source: own.skill_set_source }));
        }, function () { taskFormModal(null, own); }).then(function () { b.disabled = false; });
      });
      logView($("#lv"), id, live, function (why) {
        if (why === "idle") { $("#live-ind").innerHTML = '<span class="muted">' + esc(T("rd.idle")) + "</span>"; return; }
        if (live) setTimeout(function () { if (location.hash === "#/runs/" + id) refresh(); }, 300);
      });
      // While it runs, the core saves usage every few seconds: keep the two cards current
      // in place (no remount, so no flicker). Paused while the tab is hidden.
      if (live) livePoll(function () { return liveRunStats(id, r.status); }, queued ? 2000 : RUN_STATS_MS);
    });
  }
  var RUN_STATS_MS = 5000;
  // Run prompt in the details card: the whole text (the list only has a 300-byte preview),
  // line breaks kept, folded to four lines with a fade when longer; "展开全部" or a click on
  // the text opens it in place (scrolls inside past 40vh), copy takes the full text. Size in
  // the label's tooltip. Idea: cline/cline apps/vscode/webview-ui/src/components/chat/
  // task-header/TaskHeader.tsx (clamped task text, overflow measured, click to expand, copy).
  function runPromptHtml(r) {
    var p = r.prompt || "", meta = T("rd.prompt_meta", { c: num(Array.from(p).length), n: num(p.split("\n").length) });
    return '<dt><span data-tip="' + esc(meta) + '">' + esc(T("f.prompt")) + '</span></dt><dd class="rd-prompt-dd"><div class="rd-prompt" id="rd-prompt">' + esc(p) + "</div>" +
      '<div class="rd-prompt-acts"><button type="button" class="linkish hidden" id="rd-prompt-more" aria-controls="rd-prompt" aria-expanded="false">' + esc(T("rd.prompt_more")) + "</button>" +
      '<button type="button" class="icon-btn sm" id="rd-prompt-copy" aria-label="' + esc(T("rd.prompt_copy")) + '" data-tip="' + esc(T("rd.prompt_copy")) + '">' + ic("copy", "sm") + "</button></div></dd>";
  }
  function bindRunPrompt(r) {
    var box = $("#rd-prompt"), more = $("#rd-prompt-more");
    if (!box) return;
    $("#rd-prompt-copy").addEventListener("click", function () { copyText(r.prompt || "").catch(fail); });
    var open = false;
    var set = function (o) {
      open = o;
      box.classList.toggle("open", o);
      more.setAttribute("aria-expanded", String(o));
      more.textContent = T(o ? "rd.prompt_less" : "rd.prompt_more");
      if (o) box.setAttribute("tabindex", "0"); else { box.removeAttribute("tabindex"); box.scrollTop = 0; }
    };
    // measured right after mount (layout is synchronous here), so a short prompt never shows a fade
    var fits = function () { return open || box.scrollHeight <= box.clientHeight + 1; };
    var sync = function () {
      var cut = !fits();
      box.classList.toggle("clamped", cut && !open);
      more.classList.toggle("hidden", !cut && !open);
    };
    sync();
    more.addEventListener("click", function () { set(!open); sync(); });
    box.addEventListener("click", function () { if (!open && box.classList.contains("clamped") && !String(window.getSelection() || "")) { set(true); sync(); } });
    if (window.ResizeObserver) { var ro = new ResizeObserver(function () { if (!document.contains(box)) { ro.disconnect(); return; } sync(); }); ro.observe(box); }
  }
  // Final answer card (idea: cline/cline apps/vscode/webview-ui/src/components/chat/CompletionOutputRow.tsx —
  // the turn's last response in its own quiet container, rendered Markdown plus a copy button).
  // Preview / source like skill files, but its own remembered choice; relative links and front
  // matter are not interpreted (mdHtml with base null). The body scrolls inside the card.
  function ansMode(r) {
    var m = "preview";
    try { m = localStorage.getItem("tb_ans_view") || "preview"; } catch (e) {}
    // "compare" only where there is an earlier answer; elsewhere the page shows the preview
    return m === "source" || (m === "diff" && r && r.answer_prev) ? m : "preview";
  }
  function ansModes(r) { return r.answer_prev ? ["preview", "source", "diff"] : ["preview", "source"]; }
  function runAnswerHtml(r, mode) {
    if (!r.result) return "";
    var lines = r.result.split("\n").length;
    var meta = T("rd.ans_meta", { n: num(lines), s: fmtBytes(new Blob([r.result]).size) });
    var label = { preview: T("fb.preview"), source: T("fb.source"), diff: T("rd.ans_diff") };
    var tip = { preview: T("rd.ans_preview_tip"), source: T("rd.ans_source_tip"), diff: r.answer_prev ? T("rd.ans_diff_tip", { t: when(r.answer_prev.at) }) : "" };
    var seg = '<div class="seg sm rd-ans-mode" role="radiogroup" aria-label="' + esc(T("fb.view_as")) + '">' + ansModes(r).map(function (x) {
      var on = x === mode;
      return '<button type="button" role="radio" data-ans-mode="' + x + '" aria-checked="' + on + '" tabindex="' + (on ? 0 : -1) + '" class="' + (on ? "active" : "") + '" data-tip="' + esc(tip[x]) + '">' + esc(label[x]) + "</button>";
    }).join("") + "</div>";
    return '<section class="card rd-ans" id="rd-ans"><div class="card-head"><span class="ico">' + ic("chat") + '</span><h3 data-tip="' + esc(meta) + '">' + esc(T("rd.answer")) + "</h3>" +
      (r.result_truncated ? '<span class="pill warn sm" data-tip="' + esc(T("rd.ans_cut_tip")) + '">' + esc(T("rd.ans_cut")) + "</span>" : "") +
      // same answer as the task's previous succeeded run: a quiet chip that opens that run
      (r.answer_same && /^[\w-]{1,64}$/.test(r.answer_same.run_id) ? '<a class="pill plain sm rd-ans-same" href="#/runs/' + enc(r.answer_same.run_id) + '" data-tip="' + esc(T("rd.ans_same_tip", { t: when(r.answer_same.at) })) + '">' + ic("check", "sm") + esc(T("rd.ans_same")) + "</a>" : "") +
      '<span class="rd-acts">' + seg +
      '<button type="button" class="icon-btn sm" data-ans-copy aria-label="' + esc(T("rd.ans_copy")) + '" data-tip="' + esc(T("rd.ans_copy")) + '">' + ic("copy", "sm") + "</button>" +
      '<button type="button" class="icon-btn sm" data-ans-dl aria-label="' + esc(T("rd.ans_dl")) + '" data-tip="' + esc(T("rd.ans_dl")) + '">' + ic("download", "sm") + "</button></span></div>" +
      (mode === "diff" ? '<div class="rd-ans-body rd-ans-diff" tabindex="0" aria-live="polite">' + (r._ansDiff ? ansDiffHtml(r, r._ansDiff) : skeleton("list")) + "</div>" :
        mode === "source" ? '<pre class="rd-ans-body rd-ans-src" tabindex="0">' + esc(r.result) + "</pre>" : '<div class="rd-ans-body md" tabindex="0">' + mdHtml(r.result, null) + "</div>") + "</section>";
  }
  function bindRunAnswer(card, r) {
    var redraw = function (m) {
      try { localStorage.setItem("tb_ans_view", m); } catch (e) {}
      var tmp = document.createElement("div");
      tmp.innerHTML = runAnswerHtml(r, m);
      var next = tmp.firstChild;
      card.parentNode.replaceChild(next, card);
      bindRunAnswer(next, r);
      var on = $('[data-ans-mode][aria-checked="true"]', next); if (on) on.focus();
    };
    $$("[data-ans-mode]", card).forEach(function (b) { b.addEventListener("click", function () { if (b.getAttribute("aria-checked") !== "true") redraw(b.getAttribute("data-ans-mode")); }); });
    $(".rd-ans-mode", card).addEventListener("keydown", function (e) {
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      e.preventDefault();
      var ms = ansModes(r), i = ms.indexOf(ansMode(r));
      redraw(ms[(i + (e.key === "ArrowRight" ? 1 : ms.length - 1)) % ms.length]);
    });
    var dbox = $(".rd-ans-diff", card);
    if (dbox && !r._ansDiff) {
      api("/api/runs/" + enc(r.id) + "/answer-diff").then(function (d) {
        r._ansDiff = d;
        if (document.contains(dbox)) dbox.innerHTML = ansDiffHtml(r, d);
      }).catch(function (e) { if (document.contains(dbox)) dbox.innerHTML = errorState(e.message); });
    }
    // a folded stretch of unchanged lines opens in place
    if (dbox) dbox.addEventListener("click", function (e) {
      var g = e.target.closest("[data-ans-gap]");
      if (!g || !r._ansDiff) return;
      (r._ansOpen = r._ansOpen || {})[g.getAttribute("data-ans-gap")] = true;
      var top = dbox.scrollTop;
      dbox.innerHTML = ansDiffHtml(r, r._ansDiff);
      dbox.scrollTop = top;
      dbox.focus({ preventScroll: true });
    });
    $("[data-ans-copy]", card).addEventListener("click", function () { copyText(r.result).catch(fail); });
    $("[data-ans-dl]", card).addEventListener("click", function () { saveBlob(new Blob([r.result.replace(/\n?$/, "\n")], { type: "text/markdown" }), r.id + ".md").catch(fail); });
    card.addEventListener("click", function (e) {
      var a = e.target.closest("a[data-md-anchor]"), body = $(".rd-ans-body", card);
      if (!a || !body) return;
      e.preventDefault();
      var v = a.getAttribute("data-md-anchor"), h = body.querySelector('[data-md-id="' + (window.CSS && CSS.escape ? CSS.escape(v) : v) + '"]');
      if (h) body.scrollTop = h.offsetTop - 8;
    });
  }
  // Files an edit run changed in its folder (git work trees; snapshots taken in tidebot's own
  // folder, the repo is never written). One row per file, its diff opens in place and is
  // fetched on first open; the patch can be copied or saved for `git apply`. Idea:
  // cline/cline apps/vscode/src/core/controller/checkpoints/checkpointViewLatestChanges.ts
  // (what the agent changed since the checkpoint, per file) and siteboon/claudecodeui
  // server/modules/git/git-parsing.service.ts (status letter + counts per file).
  // Agent config a command run's after-run check found changed (~/.claude, a project's
  // .claude/, .mcp.json, CLAUDE.md …): listed here, never undone by tidebot (putting files
  // back would itself change your config). First on the page: it needs a look.
  function runAgentCfgHtml(r) {
    var c = r.agent_config_changes;
    if (!c || !c.length) return "";
    var st = { added: "warn", removed: "bad", modified: "warn" }, max = 50;
    var rows = c.slice(0, max).map(function (f) {
      var k = st[f.change] ? f.change : "modified";
      return '<li><span class="pill sm ' + st[k] + '">' + esc(T("diff.st_" + k)) + '</span><code class="ell" data-tip="' + esc(f.path) + '">' + esc(f.path) + "</code></li>";
    }).join("");
    return '<section class="card rd-acc" id="rd-acc" role="alert"><div class="card-head"><span class="ico warn">' + ic("shield") + '</span><h3 data-tip="' + esc(T("rd.acc_tip")) + '">' + esc(T("rd.acc")) + '</h3><span class="pill warn sm">' + esc(T("diff.files_n", { n: num(c.length) })) + "</span></div>" +
      '<ul class="rd-acc-list">' + rows + "</ul>" + (c.length > max ? '<p class="muted rd-chg-more">' + esc(T("rd.chg_more", { n: num(c.length - max) })) + "</p>" : "") +
      '<p class="muted rd-acc-note">' + esc(T("rd.acc_note")) + "</p></section>";
  }
  // runModeTag: a run's permission level as a small tag (details in the tooltip).
  function runModeTag(m) {
    var k = m === "edit" || m === "exec" ? m : "read";
    return '<span class="tag ' + (k === "read" ? "perm" : "warn") + '" data-tip="' + esc(T("rn." + k + "_tip")) + '">' + ic({ read: "eye", edit: "edit", exec: "terminal" }[k], "sm") + esc(T("rn." + k)) + "</span>";
  }
  function runChangesHtml(r) {
    var c = r.file_changes;
    if (!c || r.run_mode !== "edit" && r.run_mode !== "exec") return "";
    var files = c.files || [], st = { A: ["added", "ok"], D: ["removed", "bad"], M: ["modified", "info"], T: ["modified", "info"] };
    var undone = !!r.changes_undone_at, gone = {};
    (undone ? [] : r.changes_undone_files || []).forEach(function (p) { gone[p] = 1; });
    var nGone = Object.keys(gone).length, partly = nGone > 0;
    var head = '<div class="card-head"><span class="ico">' + ic("git") + '</span><h3 data-tip="' + esc(T("rd.chg_tip")) + '">' + esc(T("rd.chg")) + "</h3>" +
      (c.count ? '<span class="rd-chg-sum"><span class="pill plain sm">' + esc(T("diff.files_n", { n: num(c.count) })) + '</span><span class="add">+' + esc(num(c.add)) + '</span><span class="del">−' + esc(num(c.del)) + "</span>" +
        (undone ? '<span class="pill warn sm" data-tip="' + esc(T("rd.chg_undone_tip", { t: when(r.changes_undone_at) })) + '">' + esc(T("rd.chg_undone")) + "</span>" : "") +
        (partly ? '<span class="pill warn sm" data-tip="' + esc(T("rd.chg_partly_tip")) + '">' + esc(T("rd.chg_partly", { n: num(nGone), m: num(c.count) })) + "</span>" : "") + "</span>" +
        '<span class="rd-acts">' + chgUndoBtn(r, c, undone, partly) + '<button type="button" class="icon-btn sm" data-chg-copy aria-label="' + esc(T("rd.chg_copy")) + '" data-tip="' + esc(T("rd.chg_copy")) + '">' + ic("copy", "sm") + "</button>" +
        '<button type="button" class="icon-btn sm" data-chg-dl aria-label="' + esc(T("rd.chg_dl")) + '" data-tip="' + esc(T(c.patch_cut ? "rd.chg_dl_cut" : "rd.chg_dl")) + '">' + ic("download", "sm") + "</button></span>"
        : '<span class="pill plain sm">' + ic("check", "sm") + esc(T("rd.chg_none")) + "</span>") + "</div>";
    if (!c.count) return '<section class="card rd-chg" id="rd-chg">' + head + "</section>";
    var undoneCls = undone ? " undone" : "";
    var one = chgFileUndoOK(r, c);
    var rows = files.map(function (f, i) {
      var s2 = st[f.status] || st.M, n = f.binary ? '<span class="muted">' + esc(T("rd.chg_bin")) + "</span>" :
        (f.add ? '<span class="add">+' + esc(num(f.add)) + "</span>" : "") + (f.del ? '<span class="del">−' + esc(num(f.del)) + "</span>" : "");
      var out = undone || gone[f.path];
      // one file's own undo / put back: on hover or focus (always on touch and on undone rows)
      var fb = one ? '<button type="button" class="icon-btn sm rd-chg-fx' + (out ? " on" : "") + '" data-chg-file="' + (out ? "redo" : "undo") + '" aria-label="' + esc(T(out ? "rd.chg_f_redo" : "rd.chg_f_undo")) +
        '" data-tip="' + esc(T(out ? "rd.chg_f_redo" : "rd.chg_f_undo")) + '">' + ic(out ? "redo" : "undo", "sm") + "</button>" : "";
      return '<details class="diff-file' + (gone[f.path] ? " gone" : "") + '" data-chg="' + i + '"><summary><span class="pill sm ' + s2[1] + '">' + esc(T("diff.st_" + s2[0])) + '</span><code class="ell" data-tip="' + esc(f.path) + '">' + esc(f.path) + '</code>' +
        (gone[f.path] ? '<span class="pill warn sm rd-chg-gone">' + esc(T("rd.chg_undone")) + "</span>" : "") + '<span class="grow"></span><span class="rd-chg-n">' + n + "</span>" + fb + "</summary>" +
        '<div class="rd-chg-body"></div></details>';
    }).join("");
    var more = c.count > files.length ? '<p class="muted rd-chg-more">' + esc(T("rd.chg_more", { n: num(c.count - files.length) })) + "</p>" : "";
    return '<section class="card rd-chg' + undoneCls + '" id="rd-chg">' + head + '<div class="rd-chg-list">' + rows + more + "</div></section>";
  }
  // "Undo changes": take this run's own hunks back out of the folder (git apply -R in the
  // core; every file is checked first, nothing is written unless all still match), and
  // "Put back" to apply them again. Not offered while the run goes or when the patch was
  // cut at 512 KB. Idea: cline/cline sdk/packages/core/src/session/checkpoint-restore.ts
  // (restore the workspace to before the agent's step) — tidebot reverses only the run's
  // hunks instead of resetting the whole tree, so your other edits stay.
  function chgLive(r) { return r.status === "running" || r.status === "queued" || r.status === "pending"; }
  // One file at a time (idea: siteboon/claudecodeui src/modules/git-panel/changes/FileChangeItem.tsx,
  // a discard button on each changed file's row — theirs restores the file from HEAD, which also
  // drops edits made before the agent; tidebot takes back only this run's part of that file).
  // Not offered while running, for a cut patch, or when more files changed than are listed.
  function chgFileUndoOK(r, c) { return !chgLive(r) && !c.patch_cut && (c.files || []).length === c.count; }
  function chgUndoBtn(r, c, undone, partly) {
    var off = chgLive(r) ? T("rd.undo_off_live") : c.patch_cut ? T("rd.undo_off_cut") : "";
    var k = undone ? "rd.chg_redo" : partly ? "rd.chg_undo_rest" : "rd.chg_undo";
    return '<button type="button" class="btn sm" data-chg-undo="' + (undone ? "redo" : "undo") + '"' + (off ? " disabled" : "") +
      ' data-tip="' + esc(off || T(k + "_tip")) + '">' + ic(undone ? "redo" : "undo", "sm") + esc(T(k)) + "</button>";
  }
  // git's refusal ("error: a.txt: patch does not apply") → the file names, for the message
  function undoConflictFiles(detail) {
    var seen = {}, out = [];
    String(detail || "").split("\n").forEach(function (l) {
      var m = /^error: (?:patch failed: )?(.+?)(?::\d+)?(?:: (?:patch does not apply|already exists in working directory|does not exist in index|No such file or directory))?$/.exec(l.trim());
      if (m && !seen[m[1]] && !/^(patch|while|could not)\b/.test(m[1])) { seen[m[1]] = 1; out.push(m[1]); }
    });
    return out.slice(0, 3);
  }
  function undoErrText(e) {
    var d = e.data || {}, code = d.code;
    if (code === "conflict") {
      var f = undoConflictFiles(d.detail);
      return f.length ? T("rd.undo_err_conflict_f", { f: f.join(", ") }) : T("rd.undo_err_conflict");
    }
    return code && state.dict["rd.undo_err_" + code] ? T("rd.undo_err_" + code) : e.message;
  }
  function runUndoChanges(r, redo, btn) {
    var c = r.file_changes || {}, n = c.count || 0;
    var gone = r.changes_undone_at ? [] : r.changes_undone_files || [];
    if (gone.length) n = redo ? gone.length : Math.max(0, n - gone.length);
    var go = function () {
      if (btn) btn.disabled = true;
      return api("/api/runs/" + enc(r.id) + "/undo-changes", { method: "POST", body: { redo: redo } }).then(function () {
        if (location.hash === "#/runs/" + r.id) refresh();
        if (redo) toast(T("rd.chg_redone", { n: num(n) }));
        else toast(T("rd.chg_undone_t", { n: num(n) }), false, function () { runUndoChanges(r, true); }, T("rd.chg_redo"));
      }).catch(function (e) {
        if (btn && document.contains(btn)) btn.disabled = false;
        if (e.auth) return;
        toast(undoErrText(e), true);
      });
    };
    if (redo) return go();
    return confirmDialog({ title: T("rd.undo_title", { n: num(n) }), body: T("rd.undo_body", { dir: r.workspace || "" }), ok: T("rd.chg_undo"), danger: true, icon: "undo" })
      .then(function (y) { if (y) return go(); });
  }
  // one file of the run: confirm names the file; put back goes straight away
  function runUndoFile(r, path, redo, btn) {
    var go = function () {
      if (btn) btn.disabled = true;
      return api("/api/runs/" + enc(r.id) + "/undo-changes", { method: "POST", body: { redo: redo, path: path } }).then(function () {
        if (location.hash === "#/runs/" + r.id) refresh();
        if (redo) toast(T("rd.chg_f_redone", { f: path }));
        else toast(T("rd.chg_f_undone", { f: path }), false, function () { runUndoFile(r, path, true); }, T("rd.chg_redo"));
      }).catch(function (e) {
        if (btn && document.contains(btn)) btn.disabled = false;
        if (e.auth) return;
        toast(undoErrText(e), true);
      });
    };
    if (redo) return go();
    return confirmDialog({ title: T("rd.undo_f_title", { f: path }), body: T("rd.undo_f_body", { dir: r.workspace || "" }), ok: T("rd.chg_undo"), danger: true, icon: "undo" })
      .then(function (y) { if (y) return go(); });
  }
  // Before anyone clicks: ask the core (git apply --check, nothing written) which undo
  // buttons would work with the folder as it is now. A file edited since the run gets an
  // "已改动" mark and its button is turned off with the reason in the tooltip; the main
  // button too when its action would be refused. Asked when the card is drawn and again
  // when the window comes back to the front (you may have been editing those files).
  // Idea: cline/cline apps/vscode/webview-ui/src/components/chat/UserMessage.tsx — "Restore"
  // availability looked up when the user gets to it (pending → available / unavailable +
  // reason tooltip), not trusted from when the turn ended.
  function watchUndoCheck(card, r) {
    if (!$("[data-chg-undo]:not(:disabled), [data-chg-file]", card)) return;
    var last = 0, busy = false;
    var ask = function () {
      if (!document.contains(card)) { window.removeEventListener("focus", ask); document.removeEventListener("visibilitychange", ask); return; }
      if (document.hidden || busy || Date.now() - last < 2000) return;
      busy = true; last = Date.now();
      api("/api/runs/" + enc(r.id) + "/undo-check", { fresh: true }).then(function (x) {
        if (document.contains(card)) applyUndoCheck(card, r, x || {});
      }).catch(function () {}).then(function () { busy = false; });
    };
    window.addEventListener("focus", ask);
    document.addEventListener("visibilitychange", ask);
    ask();
  }
  var UNDO_CK_CODES = { dir: 1, busy: 1, binary: 1 };
  function undoCkSet(b, off, tip) {
    if (!b.hasAttribute("data-tip0")) b.setAttribute("data-tip0", b.getAttribute("data-tip") || "");
    if (off) { b.setAttribute("aria-disabled", "true"); b.setAttribute("data-tip", tip); b.classList.add("ck-off"); }
    else { b.removeAttribute("aria-disabled"); b.setAttribute("data-tip", b.getAttribute("data-tip0")); b.classList.remove("ck-off"); }
  }
  function applyUndoCheck(card, r, x) {
    var files = (r.file_changes || {}).files || [], fc = x.files || {}, code = UNDO_CK_CODES[x.code] ? x.code : "";
    var ub = $("[data-chg-undo]", card);
    if (ub && !ub.disabled) {
      var wf = (x.whole_files || []).slice(0, 3);
      undoCkSet(ub, !!code || x.whole === "conflict", code ? T("rd.undo_err_" + code) :
        T(ub.getAttribute("data-chg-undo") === "redo" ? (wf.length ? "rd.undo_ck_redo_f" : "rd.undo_ck_redo") : (wf.length ? "rd.undo_ck_whole_f" : "rd.undo_ck_whole"), { f: wf.join(", ") }));
    }
    $$("details[data-chg]", card).forEach(function (d) {
      var f = files[+d.getAttribute("data-chg")], b = $("[data-chg-file]", d), sm = $("summary", d);
      if (!f || !b) return;
      var ck = fc[f.path], bad = !!code || (ck && !ck.ok), stale = !code && ck && !ck.ok;
      undoCkSet(b, bad, code ? T("rd.undo_err_" + code) : T(b.getAttribute("data-chg-file") === "redo" ? "rd.undo_ck_f_redo" : "rd.undo_ck_f"));
      var mark = $(".rd-chg-stale", sm);
      if (stale && !mark) {
        mark = document.createElement("span");
        mark.className = "pill warn sm rd-chg-stale";
        mark.textContent = T("rd.chg_stale");
        sm.insertBefore(mark, $(".grow", sm));
      }
      if (mark && !stale) mark.remove();
      if (mark && stale) mark.setAttribute("data-tip", T("rd.chg_stale_tip") + (ck.detail ? "\n" + ck.detail : ""));
    });
  }
  function chgDiffHtml(d) {
    if (!d) return '<p class="muted pad">' + esc(T("rd.chg_cut")) + "</p>";
    if (d.binary && !d.lines.length) return '<p class="muted pad">' + esc(T("diff.st_binary")) + "</p>";
    if (!d.lines.length) return '<p class="muted pad">' + esc(T("rd.chg_mode")) + "</p>";
    return '<table class="diff"><colgroup><col class="ln"><col class="ln"><col class="sg"><col></colgroup><tbody>' + d.lines.map(function (l) {
      if (l.op === "@") return '<tr class="gap"><td colspan="4">' + esc(l.text) + "</td></tr>";
      if (l.op === "\\") return '<tr class="gap"><td colspan="4">' + esc(T("rd.chg_noeol")) + "</td></tr>";
      var c = l.op === "+" ? "ins" : l.op === "-" ? "del" : "";
      return '<tr class="' + c + '"><td class="ln-a">' + (+l.a || "") + '</td><td class="ln-b">' + (+l.b || "") + '</td><td class="sg">' + (l.op === "+" ? "+" : l.op === "-" ? "−" : "") + '</td><td class="tx">' + esc(l.text) + "</td></tr>";
    }).join("") + "</tbody></table>" + (d.cut ? '<p class="muted pad">' + esc(T("rd.chg_cut")) + "</p>" : "");
  }
  function bindRunChanges(card, r) {
    var got = null;
    var load = function () {
      if (!got) got = api("/api/runs/" + enc(r.id) + "/changes").catch(function (e) { got = null; throw e; });
      return got;
    };
    // a list that doesn't scroll lets the row tooltips out of its box
    var list = $(".rd-chg-list", card);
    var fits = function () { if (list) list.classList.toggle("fits", list.scrollHeight <= list.clientHeight + 1 && !$("details[open]", list)); };
    fits();
    $$("details[data-chg]", card).forEach(function (d) {
      d.addEventListener("toggle", function () {
        fits();
        var body = $(".rd-chg-body", d);
        if (!d.open || body.firstChild) return;
        body.innerHTML = skeleton("list");
        load().then(function (x) {
          if (document.contains(body)) body.innerHTML = chgDiffHtml((x.diffs || [])[+d.getAttribute("data-chg")]);
        }).catch(function (e) { if (document.contains(body)) body.innerHTML = errorState(e.message); });
      });
    });
    $$("[data-chg-file]", card).forEach(function (b) {
      b.addEventListener("click", function (e) {
        e.preventDefault(); e.stopPropagation(); // inside <summary>: don't open / close the diff
        if (b.getAttribute("aria-disabled") === "true") return; // the check says it would not apply now
        var d = b.closest("details[data-chg]"), f = d && (r.file_changes.files || [])[+d.getAttribute("data-chg")];
        if (f) runUndoFile(r, f.path, b.getAttribute("data-chg-file") === "redo", b);
      });
      b.addEventListener("keydown", function (e) { if (e.key === " " || e.key === "Enter") e.stopPropagation(); });
    });
    var ub = $("[data-chg-undo]", card);
    if (ub) ub.addEventListener("click", function () { if (ub.getAttribute("aria-disabled") !== "true") runUndoChanges(r, ub.getAttribute("data-chg-undo") === "redo", ub); });
    watchUndoCheck(card, r);
    var cp = $("[data-chg-copy]", card), dl = $("[data-chg-dl]", card);
    if (cp) cp.addEventListener("click", function () { load().then(function (x) { return copyText(x.patch || ""); }).catch(fail); });
    if (dl) dl.addEventListener("click", function () {
      load().then(function (x) { return saveBlob(new Blob([x.patch || ""], { type: "text/x-diff" }), r.id + ".diff"); }).catch(fail);
    });
  }
  // "Compare with last time": this answer against the task's previous successful one. Long
  // unchanged stretches fold to one row that opens in place; inside a changed line the words
  // that differ are marked (server side). Idea: dgtlmoon/changedetection.io
  // changedetectionio/diff/__init__.py (render_nested_line_diff) — line colour plus a darker
  // mark on the changed words, so "63 → 66" is seen at a glance.
  var ANS_CTX = 3;
  function ansDiffHtml(r, d) {
    if (!d.prev) return '<p class="muted">' + esc(T("rd.ans_diff_none")) + "</p>";
    var L = d.lines || [], open = r._ansOpen || {};
    var prev = '<a href="#/runs/' + enc(d.prev.run_id) + '" data-tip="' + esc(T("rd.ans_diff_prev_tip", { t: when(d.prev.at) })) + '">' + esc(T("rd.ans_diff_prev")) + "</a>";
    if (!d.added && !d.removed) return '<p class="diff-sum">' + ic("check", "sm") + " " + esc(T("rd.ans_diff_same")) + " · " + prev + "</p>";
    var sum = '<p class="diff-sum"><span class="add">+' + esc(num(d.added)) + '</span> <span class="del">−' + esc(num(d.removed)) + "</span> · " + prev +
      (d.same ? ' · <span data-tip="' + esc(T("rd.ans_diff_ws_tip")) + '">' + esc(T("rd.ans_diff_ws")) + "</span>" : "") +
      (d.coarse ? ' · <span data-tip="' + esc(T("rd.ans_diff_coarse_tip")) + '">' + esc(T("rd.ans_diff_coarse")) + "</span>" : "") + "</p>";
    // which unchanged lines stay visible: ANS_CTX around each change; a fold hides 4 or more
    var near = L.map(function () { return false; });
    L.forEach(function (l, i) { if (l.op !== " ") for (var k = Math.max(0, i - ANS_CTX); k <= Math.min(L.length - 1, i + ANS_CTX); k++) near[k] = true; });
    var rows = [], i = 0;
    while (i < L.length) {
      if (near[i]) { rows.push(ansDiffRow(L[i])); i++; continue; }
      var j = i; while (j < L.length && !near[j]) j++;
      if (j - i < 4 || open[i]) { for (var k = i; k < j; k++) rows.push(ansDiffRow(L[k])); }
      else rows.push('<tr class="gap"><td colspan="4"><button type="button" class="linkish" data-ans-gap="' + i + '">' + esc(T("rd.ans_diff_fold", { n: num(j - i) })) + "</button></td></tr>");
      i = j;
    }
    return sum + '<table class="diff"><tbody>' + rows.join("") + "</tbody></table>";
  }
  function ansDiffRow(l) {
    var c = l.op === "+" ? "ins" : l.op === "-" ? "del" : "";
    var tx = l.segs && l.segs.length ? l.segs.map(function (g) { return g.x ? '<mark class="dx">' + esc(g.t) + "</mark>" : esc(g.t); }).join("") : esc(l.text);
    return '<tr class="' + c + '"><td class="ln-a">' + (+l.a || "") + '</td><td class="ln-b">' + (+l.b || "") + '</td><td class="sg">' + (l.op === "+" ? "+" : l.op === "-" ? "−" : "") + '</td><td class="tx">' + tx + "</td></tr>";
  }
  function liveRunStats(id, was) {
    return api("/api/runs/" + enc(id)).then(function (r) {
      var still = r.status === "running" || r.status === "queued";
      // A queued run got its slot (or was stopped): redraw the page once, in place.
      if (was === "queued" && r.status !== "queued") { stopPoll(); if (location.hash === "#/runs/" + id) refresh(); return; }
      var c = $("#rd-cost"), k = $("#rd-tok");
      if (!c || !k) return;
      // Compare with what we rendered last (not the browser's re-serialised HTML), so
      // unchanged cards are never touched.
      var prev = c._live || {}, next = { c: runCostStat(r, still), k: runTokStat(r, still), x: costCalcHtml(r) };
      if (prev.c !== next.c) c.innerHTML = next.c;
      if (prev.k !== next.k) k.innerHTML = next.k;
      if (prev.x !== next.x) {
        var calc = $("#rd-calc");
        if (calc) calc.outerHTML = next.x || '<section id="rd-calc" hidden></section>';
        else if (next.x) c.parentNode.insertAdjacentHTML("afterend", next.x);
      }
      c._live = next;
      if (!still) stopPoll();
    });
  }
  // Run detail stat cards. A live run shows usage so far; the tooltip says it updates.
  function runCostStat(r, live) {
    var foreign = r.cost_source && r.cost_currency && r.cost_currency !== state.currency;
    return "<small" + (live ? ' data-tip="' + esc(T("rd.usage_live_tip")) + '"' : "") + ">" + esc(T(live ? "rd.cost_so_far" : "col.cost")) + "</small><strong>" +
      esc(foreign ? money(r.cost_amount, r.cost_currency, 4) : usd(r.cost_usd, 4)) + costBadge(r.cost_source) + "</strong><span>" +
      esc(foreign ? T("rd.approx_in", { v: usd(r.cost_usd, 4) }) : T("rd.bucket", { b: r.bucket })) + "</span>";
  }
  function runTokStat(r, live) {
    var cr = r.cache_read_tokens || 0, cw = r.cache_write_tokens || 0;
    return "<small" + (live ? ' data-tip="' + esc(T("rd.usage_live_tip")) + '"' : "") + ">" + esc(T(live ? "rd.tokens_so_far" : "rd.tokens")) + "</small><strong>" +
      esc(num(r.input_tokens + r.output_tokens + cr + cw)) + "</strong><span>" +
      esc(T("rd.in_out", { i: num(r.input_tokens), o: num(r.output_tokens) }) + (cr || cw ? " · " + T("rd.cache_rw", { r: num(cr), w: num(cw) }) : "")) + "</span>";
  }
  // Run detail: how the cost was computed (tokens × price per 1M, per model).
  function costCalcHtml(r) {
    var c = r.cost_detail, src = r.cost_source;
    var head = '<section class="card" id="rd-calc"><div class="card-head"><span class="ico">' + ic("receipt") + '</span><h3>' + esc(T("cost.calc")) + "</h3>" + costBadge(src) + "</div>";
    if (!src) return "";
    if (src === "legacy") return head + '<p class="muted pad-x">' + esc(T("cost.legacy_note")) + "</p></section>";
    if (!c || !c.lines) return "";
    var parts = function (l) {
      var R = l.rates || {}, cur = l.currency || c.currency, x = [];
      [["input", "input", T("cost.in")], ["output", "output", T("cost.out")], ["cache_write", "cache_write", T("cost.cw")], ["cache_read", "cache_read", T("cost.cr")]].forEach(function (k) {
        if (l[k[0]]) x.push(esc(k[2]) + " " + esc(num(l[k[0]])) + " × " + esc(money(R[k[1]] || 0, cur, R[k[1]] < 0.1 ? 3 : 2)) + "/M");
      });
      return x.join("<br>");
    };
    var rows = c.lines.map(function (l) {
      var note = l.missing ? '<span class="pill warn sm">' + esc(T("cost.no_price")) + "</span>" : (l.approx ? '<span class="pill warn sm" title="' + esc(T("cost.approx_tip")) + '">' + esc(T("cost.est")) + "</span>" : "") + (l.tiered ? ' <span class="pill sm">' + esc(T("cost.tiered")) + "</span>" : "");
      return "<tr><td><code>" + esc(l.model || "—") + "</code> " + note + '<small class="muted">' + esc(T("cost.reqs", { n: l.requests || 0 })) + "</small></td><td class=\"mono calc\">" + (c.mode === "metered" && !l.missing ? parts(l) : esc(T("rd.in_out", { i: num(l.input), o: num(l.output) }))) + '</td><td class="right"><strong>' + esc(c.mode === "metered" && !l.missing ? money(l.amount, l.currency || c.currency, 4) : "—") + "</strong></td></tr>";
    }).join("");
    var foot = src === "claude" ? T("cost.claude_note") : src === "subscription" ? T("cost.sub_note") : src === "free" ? T("cost.free_note") : T("cost.snapshot_note");
    return head + '<table class="table"><thead><tr><th>' + esc(T("f.model")) + "</th><th>" + esc(T("cost.formula")) + '</th><th class="right">' + esc(T("col.cost")) + "</th></tr></thead><tbody>" + rows +
      '<tr><td colspan="2"><strong>' + esc(T("cost.total")) + '</strong></td><td class="right"><strong>' + esc(money(c.amount, c.currency, 4)) + "</strong></td></tr></tbody></table>" +
      '<p class="hint pad-x">' + esc(foot) + "</p></section>";
  }

  function contextPlanHtml(r) {
    var p=r.context_plan;if(!p)return '';
    return '<details class="card"><summary>'+esc(T('cp.audit'))+' · '+esc(p.estimated_tokens)+' / '+esc(p.budget_tokens)+'</summary><p class="hint">'+esc(T('cp.estimate'))+'</p><div class="table-wrap"><table><thead><tr><th>'+esc(T('cp.material'))+'</th><th>'+esc(T('cp.tokens'))+'</th><th>'+esc(T('cp.decision'))+'</th></tr></thead><tbody>'+
      (p.materials||[]).map(function(m){return '<tr><td>'+esc(m.id)+'</td><td>'+esc(m.estimated_tokens)+'</td><td>'+esc(T(m.kind==='reservation'?'cp.reserved':'cp.selected'))+(m.mode?' · '+esc(T('cp.mode_'+m.mode)):'')+'</td></tr>';}).join('')+
      (p.omitted||[]).map(function(m){return '<tr><td>'+esc(m.id)+'</td><td>—</td><td>'+esc(m.reason)+'</td></tr>';}).join('')+'</tbody></table></div><details><summary>'+esc(T('cp.details'))+'</summary><pre>'+esc(JSON.stringify(p,null,2))+'</pre></details></details>';
  }

  function costPolicySnapshotHtml(r) {
    var p = r.effective_cost_policy;
    return '<section class="card"><h3>' + esc(T("costmode.snapshot")) + '</h3>' + (p ? costModeBadge(p.mode) + '<p class="muted">' + esc(T(p.mode === "bypass" ? "costmode.warning" : p.mode === "global" ? "costmode.global_hint" : "costmode.task_hint")) + '</p><p>' + esc(T("costmode.run",{c:p.currency}) + ": " + (p.max_amount ? String(p.max_amount) : "∞") + " · " + p.accounting_day + " · " + p.timezone) + '</p>' : '<p class="muted">' + esc(T("costmode.historical")) + '</p>') + '</section>';
  }
