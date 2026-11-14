  // ---------- run page: continue the conversation (会话 P0) ----------
  // A finished run gets a message box under it. The first message makes the run a session
  // (POST /api/chat-sessions, the same run always gives the same session), every message is
  // one turn (POST …/turns) that runs `claude -p --resume` read-only. The turns show under
  // the run in order: question, answer, cost; the one being answered shows a status line
  // with elapsed time and Stop and the run's live log lines. Ideas: siteboon/claudecodeui
  // src/modules/chat/composer/ActivityIndicator.tsx (inline "working · 12s · stop" line
  // above the composer while a turn runs) and QueuedMessageCard.tsx (composer state says
  // why it can't send); tidebot keeps one turn at a time (409 busy) instead of a queue.
  var RC_LIVE_MAX = 200, RC_POLL_MS = 1500, rcEs = null, rcEsRun = "", rcTick = null;
  function stopChatStream() { if (rcEs) { rcEs.close(); rcEs = null; rcEsRun = ""; } }
  function rcDone(st) { return st === "succeeded" || st === "failed" || st === "timeout" || st === "canceled"; }
  function rcBusy(st) { return st === "running" || st === "queued"; }
  function rcDraftKey(id) { return "rc_" + id; }
  // Stable codes from /api/chat-sessions → the viewer's language; anything else as sent.
  function rcErrText(e) {
    var code = e && e.data && e.data.code;
    if (code === "dir") {
      var tip = "rn.ws_" + String(e.data.problem || "") + "_tip";
      return T("rc.err_dir", { why: state.dict[tip] ? T(tip) : T("rn.ws_not_found_tip") });
    }
    return code && state.dict["rc.err_" + code] ? T("rc.err_" + code) : (e && e.message) || "";
  }
  // The title row of a turn's own run page: which session it is part of, linking back.
  function runChatOfHtml(r) {
    var cs = r.chat_session;
    if (!cs || !(cs.turn > 0) || !/^[\w-]{1,64}$/.test(cs.origin_run_id || "")) return "";
    return '<a class="pill plain sm rc-of" href="#/runs/' + enc(cs.origin_run_id) + '" data-tip="' + esc(T("rc.of_tip")) + '">' + ic("chat", "sm") + esc(T("rc.of", { n: num(cs.turn) })) + "</a>";
  }
  // The run a session continues: its turns come with the page (one paint, no skeleton).
  function runChatPreload(r) {
    var cs = r.chat_session;
    if (!cs || cs.turn !== 0 || !rcDone(r.status)) return r;
    return api("/api/chat-sessions/" + enc(cs.id), { fresh: true }).then(function (s) { r._chat = s; return r; }, function () { return r; });
  }
  function rcTurns(s) { return ((s && s.turn_list) || []).filter(function (t) { return t.index > 0; }); }
  // The section under a finished run (not under a session's later turns: their thread is on
  // the run they continue).
  function runChatHtml(r) {
    var cs = r.chat_session;
    if ((cs && cs.turn > 0) || r.trigger === "session" || !rcDone(r.status)) return "";
    var pre = rcTurns(r._chat);
    return '<section class="rc" id="rc" aria-label="' + esc(T("rc.title")) + '"><div class="rc-sep" id="rc-sep"' + (pre.length || (cs && !r._chat) ? "" : " hidden") + "><span>" + esc(T("rc.title")) + '</span>' + rcOpenHtml(cs && cs.id) + '</div><div class="rc-thread" id="rc-thread" aria-live="polite">' +
      (r._chat ? pre.map(rcTurnHtml).join("") : cs ? skeleton("list") : "") + "</div>" +
      rcBoxHtml({ mode: (r._chat && r._chat.permission_mode) || r.run_mode || "read" }) + "</section>";
  }
  // The message box (run page and session page). o.ph / o.label / o.read_tip: other words;
  // o.tags: extra tags before 只读 (the session page's folder).
  function rcBoxHtml(o) {
    return '<form class="rc-box" id="rc-form" novalidate><label class="sr-only" for="rc-in">' + esc(T(o.label || "rc.label")) + "</label>" +
      '<textarea id="rc-in" rows="1" maxlength="16384" data-noguard placeholder="' + esc(T(o.ph || "rc.ph")) + '"></textarea>' +
      '<div class="rc-bar"><span class="rc-tags">' + (o.tags || "") + rcModeHtml(o.mode || "read", o.read_tip) +
      '<span class="rc-tag rc-noweb" id="rc-noweb" tabindex="0" data-tip="' + esc(T("rc.noweb_tip")) + '">' + ic("offline", "sm") + esc(T("rc.noweb")) + "</span></span>" +
      '<span class="rc-meta" id="rc-meta"></span>' +
      '<button type="submit" class="btn primary sm" id="rc-send">' + ic("send", "sm") + esc(T("rc.send")) + "</button></div>" +
      '<p class="field-err" id="rc-err" role="alert"></p></form>';
  }
  // rcModeHtml: the session's permission level (只读 / 可修改 / 可执行命令) as a small
  // select in the message box; the tooltip says what the chosen level allows.
  function rcModeHtml(mode, readTip) {
    var m = mode === "edit" || mode === "exec" ? mode : "read";
    var tipOf = function (v) { return T(v === "read" ? readTip || "rc.read_tip" : "rn." + v + "_tip"); };
    return '<label class="rc-tag rc-mode" data-tip="' + esc(tipOf(m)) + '" data-read-tip="' + esc(readTip || "rc.read_tip") + '">' + ic({ read: "eye", edit: "edit", exec: "terminal" }[m], "sm") +
      '<select id="rc-mode" aria-label="' + esc(T("rn.mode")) + '">' + ["read", "edit", "exec"].map(function (v) { return '<option value="' + v + '"' + (v === m ? " selected" : "") + ">" + esc(T("rn." + v)) + "</option>"; }).join("") + "</select></label>";
  }
  // sandboxInfo: this machine's command sandbox status (from /api/settings), fetched once.
  function sandboxInfo() {
    if (state.sandbox) return Promise.resolve(state.sandbox);
    return api("/api/settings").then(function (st) { state.sandbox = st.sandbox || {}; return state.sandbox; }, function () { return {}; });
  }
  // "Open in 会话" beside the run page's divider: the same thread on the session page.
  function rcOpenHtml(id) {
    var okId = /^[\w-]{1,64}$/.test(id || "");
    return '<a class="rc-open" id="rc-open" href="' + (okId ? "#/chat/" + enc(id) : "#/chat") + '"' + (okId ? "" : " hidden") + ">" + ic("chat", "sm") + esc(T("rc.open_session")) + "</a>";
  }
  function rcTurnHtml(t) {
    var busy = rcBusy(t.status), ok = /^[\w-]{1,64}$/.test(t.run_id || "");
    var head = '<div class="rc-a-head"><span class="rc-av">' + ic("sparkle", "sm") + "</span>";
    if (busy) head += '<span class="rc-act"><span class="rc-dot"></span>' + esc(T(t.status === "queued" ? "rc.queued" : "rc.answering")) + ' <span class="rc-el num" data-since="' + esc(t.created_at || "") + '"></span></span>';
    else if (t.status !== "succeeded") head += t.status ? statusPill(t.status, 0, t.error) : '<span class="muted">' + esc(T("rc.run_gone")) + "</span>";
    if (t.answer_truncated) head += '<span class="pill warn sm" data-tip="' + esc(T("rd.ans_cut_tip")) + '">' + esc(T("rd.ans_cut")) + "</span>";
    head += '<span class="rc-sp"></span>';
    if (!busy && t.status) head += '<span class="rc-cost num" data-tip="' + esc(T("rc.cost_tip", { t: when(t.created_at) })) + '">' + esc(usd(t.cost_usd, 4)) + "</span>";
    if (busy && ok) head += '<button type="button" class="icon-btn sm" data-rc-stop="' + esc(t.run_id) + '" data-st="' + esc(t.status) + '" aria-label="' + esc(T("run.stop")) + '" data-tip="' + esc(T("run.stop_tip")) + '">' + ic("stop", "sm") + "</button>";
    if (!busy && t.answer) head += '<button type="button" class="icon-btn sm" data-rc-copy="' + t.index + '" aria-label="' + esc(T("rd.ans_copy")) + '" data-tip="' + esc(T("rd.ans_copy")) + '">' + ic("copy", "sm") + "</button>";
    if (ok) head += '<a class="icon-btn sm" href="#/runs/' + enc(t.run_id) + '" aria-label="' + esc(T("rc.open_run")) + '" data-tip="' + esc(T("rc.open_run")) + '">' + ic("terminal", "sm") + "</a>";
    head += "</div>";
    var body = busy ? '<div class="log rc-live" data-rc-live="' + esc(t.run_id || "") + '" tabindex="0" aria-label="' + esc(T("rd.log")) + '"></div>' :
      (t.answer ? '<div class="rc-a-body md" tabindex="0">' + mdHtml(t.answer, null) + "</div>" : "") +
      (t.error && t.status !== "succeeded" ? '<p class="rc-a-err">' + esc(runErr(t.error)) + "</p>" : "") +
      (!t.answer && !t.error && t.status ? '<p class="rc-a-err muted">' + esc(T("rw.answer_missing")) + "</p>" : "");
    return '<article class="rc-turn" data-turn="' + t.index + '"><div class="rc-q"><div class="rc-bubble" data-tip="' + esc(when(t.created_at)) + '">' + esc(t.message) + "</div></div>" +
      '<div class="rc-a' + (busy ? " busy" : "") + '">' + head + body + "</div></article>";
  }
  // Live lines of the turn being answered: the run's own event stream (same lines and
  // colours as the run log), only the newest few on screen, the full log on the turn's page.
  function rcLiveLine(text) {
    var l = classifyLog(text), body = l.text, tool = "";
    if (l.kind === "tool") {
      var m = /^assistant: tool (\S+)\s?(.*)$/.exec(body) || [null, "", body]; tool = m[1]; body = m[2];
      // the input's main field, like the run log's one-line preview
      try { var o = JSON.parse(body); if (o && typeof o === "object") body = String(o.command || o.file_path || o.path || o.pattern || o.url || o.query || o.description || body); } catch (e) {}
    }
    else if (l.kind === "text") body = body.replace(/^assistant: /, "");
    else if (l.kind === "result") body = "↳ " + body.replace(/^user: /, "");
    return '<div class="ln k-' + l.kind + '"><span class="ts">' + esc(l.ts || "") + '</span><span class="tx">' + (tool ? '<span class="tool-chip">' + esc(tool) + "</span> " : "") + esc(body) + "</span></div>";
  }
  function rcStream(box, runId, onEnd) {
    if (rcEsRun === runId && rcEs) return;
    stopChatStream();
    var es = new EventSource("/api/runs/" + enc(runId) + "/events");
    rcEs = es; rcEsRun = runId;
    es.onmessage = function (ev) {
      var cur = $('[data-rc-live="' + runId + '"]');
      if (!cur) { stopChatStream(); return; }
      var stick = cur.scrollTop + cur.clientHeight >= cur.scrollHeight - 24;
      cur.insertAdjacentHTML("beforeend", rcLiveLine(ev.data));
      while (cur.childElementCount > RC_LIVE_MAX) cur.removeChild(cur.firstChild);
      if (stick) cur.scrollTop = cur.scrollHeight;
    };
    var end = function () { if (rcEs === es) stopChatStream(); else es.close(); if (onEnd) onEnd(); };
    es.addEventListener("end", end);
    es.addEventListener("idle", function () { if (rcEs === es) stopChatStream(); else es.close(); });
    es.onerror = function () { if (es.readyState === 2 && rcEs === es) stopChatStream(); };
  }
  function rcElapsed() {
    var els = $$(".rc-el[data-since]");
    if (!els.length) { clearInterval(rcTick); rcTick = null; return; }
    els.forEach(function (el) {
      var t = Date.parse(el.getAttribute("data-since")), s = isNaN(t) ? 0 : Math.max(0, Date.now() - t);
      var v = s >= 1000 ? dur(s) : "";
      if (el.textContent !== v) el.textContent = v;
    });
  }
  function bindRunChat(r) {
    var sec = $("#rc");
    if (!sec) return;
    if (r.status === "failed" || r.status === "timeout") {
      var fix = document.createElement("button"); fix.type = "button"; fix.className = "btn sm ghost"; fix.textContent = T("cap.fix_session"); sec.insertBefore(fix, sec.firstChild);
      fix.addEventListener("click", function () { fix.disabled = true; api("/api/capabilities/recover", {method:"POST",body:{run_id:r.id}}).then(function (v) { draftSave("cs_"+v.session.id,v.draft_prompt); draftFlush(); location.hash="#/chat/"+enc(v.session.id); }).catch(function(e){fix.disabled=false;fail(e);}); });
    }
    bindChat(sec, {
      id: r.chat_session ? r.chat_session.id : "", key: rcDraftKey(r.id), pre: r._chat, turns: rcTurns,
      create: function (mode) { return api("/api/chat-sessions", { method: "POST", body: { run_id: r.id, permission_mode: mode } }); },
      onPaint: function (s) { var a = $("#rc-open", sec); if (a && s && /^[\w-]{1,64}$/.test(s.id || "")) { a.href = "#/chat/" + enc(s.id); a.hidden = false; } }
    });
  }
  // bindChat wires a thread + message box (#rc-thread, #rc-form… inside sec) to one session.
  // o.id: the session (or "" until o.create() makes one on the first send); o.key: draft key;
  // o.pre: the session already fetched; o.turns(s): the turns to show; o.onPaint(s), o.onGone(),
  // o.onSent(s): page hooks.
  function bindChat(sec, o) {
    var thread = $("#rc-thread", sec), form = $("#rc-form", sec), ta = $("#rc-in", sec), send = $("#rc-send", sec), meta = $("#rc-meta", sec), err = $("#rc-err", sec);
    var st = { id: o.id || "", ses: null, sending: false, painted: {} }, turnsOf = o.turns || rcTurns;
    var modeSel = $("#rc-mode", sec), modeLbl = modeSel && modeSel.parentNode;
    var paintMode = function (m) {
      if (!modeSel) return;
      if (m) modeSel.value = m;
      var v = modeSel.value, tipKey = v === "read" ? modeLbl.getAttribute("data-read-tip") : "rn." + v + "_tip";
      modeLbl.setAttribute("data-tip", T(tipKey));
      var icon = $(".i", modeLbl), tmp = document.createElement("span"); tmp.innerHTML = ic({ read: "eye", edit: "edit", exec: "terminal" }[v], "sm");
      if (icon && tmp.firstChild) modeLbl.replaceChild(tmp.firstChild, icon);
      modeLbl.classList.toggle("warn", v !== "read");
    };
    var key = o.key, d = draftLoad(key);
    if (d) ta.value = d.text;
    var fit = function () { ta.style.height = ""; ta.style.height = Math.min(Math.round(window.innerHeight * 0.4), ta.scrollHeight) + "px"; };
    fit();
    // a long session only gets a soft hint (more context per turn); sending never stops
    var long = function () { var s = st.ses; return !!(s && s.turn_hint && s.turns >= s.turn_hint); };
    var busy = function () { return st.sending || !!(st.ses && (st.ses.status === "running" || (st.ses.turn_list || []).some(function (t) { return t.index > 0 && rcBusy(t.status); }))); };
    var sync = function () {
      var b = busy(), empty = !ta.value.trim();
      send.disabled = b || empty;
      ta.placeholder = T(o.ph || "rc.ph");
      send.setAttribute("data-tip", T(b ? "rc.busy_tip" : o.send_tip || "rc.send_tip"));
      var s = st.ses, html = "";
      if (s) html = (long() ? '<span class="pill sm" tabindex="0" data-tip="' + esc(T("rc.long_tip")) + '">' + esc(T("rc.long")) + "</span>" : "") +
        '<span class="num" tabindex="0" data-tip="' + esc(T("rc.meta_tip", { n: num(s.turns), c: usd(s.cost_usd, 4) })) + '">' + esc(T("rc.meta", { n: num(s.turns) })) + "</span>";
      if (meta._h !== html) { meta.innerHTML = html; meta._h = html; }
      $("#rc-noweb", sec).hidden = !!(s && s.web_access) || !!(modeSel && modeSel.value === "exec"); // command sessions have network
      if (modeSel) modeSel.disabled = b;
    };
    // keyed by turn: only a turn whose content changed is redrawn (no flicker, the live
    // lines of the turn being answered are kept)
    var paint = function (s) {
      st.ses = s;
      if (s && s.permission_mode && document.activeElement !== modeSel) paintMode(s.permission_mode);
      var turns = turnsOf(s), seen = {};
      if (thread.firstElementChild && !thread.firstElementChild.classList.contains("rc-turn")) thread.innerHTML = "";
      turns.forEach(function (t) {
        var html = rcTurnHtml(t), el = $('.rc-turn[data-turn="' + t.index + '"]', thread);
        seen[t.index] = 1;
        if (el && st.painted[t.index] === html) return;
        var tmp = document.createElement("div"); tmp.innerHTML = html;
        var next = tmp.firstChild;
        if (el) thread.replaceChild(next, el); else thread.appendChild(next);
        st.painted[t.index] = html;
      });
      $$(".rc-turn", thread).forEach(function (el) { if (!seen[el.getAttribute("data-turn")]) { delete st.painted[el.getAttribute("data-turn")]; el.remove(); } });
      var live = turns.filter(function (t) { return rcBusy(t.status) && t.run_id; })[0];
      if (live && live.status === "running" && /^[\w-]{1,64}$/.test(live.run_id)) rcStream(thread, live.run_id, function () { load(); });
      else if (!live) stopChatStream();
      if ($(".rc-el[data-since]", thread) && !rcTick) { rcTick = setInterval(rcElapsed, 1000); }
      var sep = $("#rc-sep", sec);
      if (sep) sep.hidden = !turns.length;
      if (o.onPaint) o.onPaint(s);
      if (typeof capRefreshSession === "function") capRefreshSession(sec, s.id);
      rcElapsed();
      sync();
      if (busy()) livePoll(load, RC_POLL_MS); else if (st.polling) stopPoll();
      st.polling = busy();
    };
    // While a turn is answered this runs every 1.5 s. The answer's live lines come over the
    // run's event stream, so most polls find nothing new: with ?since=<sig> the server says
    // {unchanged} instead of sending every turn's answer again, and nothing is re-rendered
    // (each repaint used to turn every answer's markdown into HTML again to compare it).
    var load = function () {
      if (!st.id) return Promise.resolve();
      var id = st.id, since = st.ses && st.sig && st.sigId === id ? "?since=" + enc(st.sig) : "";
      return api("/api/chat-sessions/" + enc(id) + since, { fresh: true }).then(function (s) {
        if (!document.contains(sec) || id !== st.id) return;
        if (s && s.unchanged) {
          if (s.sig === st.sig) { if (typeof capRefreshSession === "function") capRefreshSession(sec, id); return; }
          st.sig = ""; return load();
        }
        st.sig = (s && s.sig) || ""; st.sigId = id;
        paint(s);
      }).catch(function (e) {
        if (!document.contains(sec)) return;
        if (e.status === 404) {
          st.sig = "";
          if (o.onGone) { o.onGone(); return; }
          st.id = ""; st.ses = null; thread.innerHTML = ""; st.painted = {}; if ($("#rc-sep", sec)) $("#rc-sep", sec).hidden = true; sync(); return;
        }
        if (!st.ses) thread.innerHTML = errorState(rcErrText(e));
      });
    };
    var showErr = function (m) { err.textContent = m || ""; };
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      var v = ta.value.trim();
      if (!v || busy()) return;
      if (new Blob([v]).size > 16384) { showErr(T("rc.err_too_long")); return; }
      showErr(""); st.sending = true; sync();
      var made = null;
      (st.id ? Promise.resolve({ id: st.id }) : o.create(modeSel ? modeSel.value : undefined).then(function (s) { made = s; return s; })).then(function (s) {
        st.id = s.id;
        return api("/api/chat-sessions/" + enc(s.id) + "/turns", { method: "POST", body: { message: v } });
      }).then(function () {
        ta.value = ""; draftClear(key); fit();
        if (o.onSent && o.onSent({ id: st.id })) return;
        return load().then(function () { var last = thread.lastElementChild; if (last && last.scrollIntoView) last.scrollIntoView({ block: "nearest" }); });
      }).catch(function (e) {
        if (made && o.dropEmpty) { api("/api/chat-sessions/" + enc(made.id), { method: "DELETE" }).catch(function () {}); st.id = ""; }
        if (e.auth || !document.contains(sec)) return;
        showErr(rcErrText(e));
        if (e.data && e.data.code === "busy") load();
      }).then(function () { st.sending = false; if (document.contains(sec)) sync(); });
    });
    ta.addEventListener("input", function () { fit(); showErr(""); draftSave(key, ta.value); sync(); });
    // changing the level: saved on the session (the next turn uses it); 可执行命令 on a
    // machine without a system sandbox explains itself once first
    if (modeSel) modeSel.addEventListener("change", function () {
      var v = modeSel.value, prev = (st.ses && st.ses.permission_mode) || modeSel._prev || "read";
      showErr("");
      (v === "exec" ? sandboxInfo().then(function (sb) { return execFirstUse(sb); }) : Promise.resolve(true)).then(function (ok) {
        if (!ok) { paintMode(prev); return; }
        paintMode(v); sync();
        if (!st.id) { modeSel._prev = v; return; }
        return api("/api/chat-sessions/" + enc(st.id) + "/mode", { method: "PUT", body: { mode: v } }).then(function (x) { st.ses = Object.assign(st.ses || {}, { permission_mode: x.permission_mode }); modeSel._prev = v; toast(T("rc.mode_saved", { m: T("rn." + v) })); })
          .catch(function (e) { paintMode(prev); sync(); showErr(rcErrText(e)); });
      });
    });
    paintMode();
    thread.addEventListener("click", function (e) {
      var c = e.target.closest("[data-rc-copy]"), s = e.target.closest("[data-rc-stop]");
      if (c && st.ses) { var t = (st.ses.turn_list || []).filter(function (x) { return String(x.index) === c.getAttribute("data-rc-copy"); })[0]; if (t) copyText(t.answer || "").catch(fail); }
      if (s) { s.disabled = true; stopRun(s.getAttribute("data-rc-stop"), s.getAttribute("data-st")).then(function () { load(); }).catch(function (x) { s.disabled = false; fail(x); }); }
    });
    sync();
    if (o.pre) { st.sig = o.pre.sig || ""; st.sigId = st.id; turnsOf(o.pre).forEach(function (t) { st.painted[t.index] = rcTurnHtml(t); }); paint(o.pre); } else load();
    return { load: load };
  }
