  // ---------- page plumbing ----------
  function page() { return $("#page"); }
  var mountSeq = 0, scrollPos = {}, pendingRestore = null;
  function scroller() { var pg = page(); return pg && pg.scrollHeight > pg.clientHeight + 1 ? pg : null; }
  function mount(html, nav, crumbs) {
    mountSeq++;
    setNav(nav);
    renderTopbar.nav = nav;
    renderTopbar(crumbs);
    var leaf = crumbs && crumbs.length ? crumbs[crumbs.length - 1][0] : "";
    setDocTitle({ page: leaf || (nav ? T("nav." + nav) : "") });
    var pg = page();
    pg.removeAttribute("aria-busy");
    pg.innerHTML = html;
    applySizes(pg);
    if (pendingRestore !== null) { var y = pendingRestore; pendingRestore = null; window.scrollTo(0, y); pg.scrollTop = y; }
  }
  // Unsaved-changes guard (idea: n8n Modal.vue beforeClose + CredentialEdit hasUnsavedChanges).
  // A field counts only once the user typed/clicked in it and its value moved away from what it
  // held when the user first touched the dialog, so prefills and auto-fill never trip it, and
  // undoing an edit makes the dialog clean again. Search boxes and [data-noguard] are ignored.
  var modalGuard = null;
  function fieldVal(el) { return el.type === "checkbox" || el.type === "radio" ? String(el.checked) : String(el.value).replace(/\r\n?/g, "\n"); }
  function guardable(el) { return !!el && /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) && !/^(hidden|search|button|submit|reset|file|image)$/.test(el.type) && !(el.closest && el.closest("[data-noguard]")); }
  function guardBase(box) { $$("input,textarea,select", box).forEach(function (el) { if (guardable(el) && el._tbBase === undefined) el._tbBase = fieldVal(el); }); }
  function guardDirty(box) {
    return $$("input,textarea,select", box).some(function (el) { return el._tbTouched && guardable(el) && el._tbBase !== undefined && fieldVal(el) !== el._tbBase; });
  }
  // ⌘/Ctrl+Enter in a dialog or drawer does what its main button does, from any field
  // (idea: Dify create-app-modal useHotkey("Mod+Enter") scoped to the dialog, skipped while
  // composing, disabled with the button). The button is the form's primary submit when the
  // focus is in a form, else the last enabled primary in the footer; a red confirm counts
  // only when there is no primary. Nothing happens while the discard bar asks, with an input
  // method open, when a field handled the key itself, or on auto-repeat.
  var DLG_FOOT = ".actions:not(.discard-bar), .drawer-foot";
  function dlgUsable(b) { return !!b && !b.disabled && b.offsetParent !== null && !(b.closest && b.closest(".hidden, .discard-bar, [aria-busy=true]")); }
  function dlgLast(list, ok) { for (var i = list.length - 1; i >= 0; i--) if (ok(list[i])) return list[i]; return null; }
  function dialogPrimary(box, from) {
    if (!box || $(".discard-bar", box)) return null;
    var f = from && from.closest ? from.closest("form") : null;
    if (f && box.contains(f)) {
      var subs = $$("button", f).filter(function (b) { return b.type === "submit" && dlgUsable(b); });
      var sb = dlgLast(subs, function (b) { return b.classList.contains("primary"); }) || dlgLast(subs, function () { return true; });
      if (sb) return sb;
    }
    var all = $$("button.btn", box).filter(function (b) { return b.closest && b.closest(DLG_FOOT) && dlgUsable(b); });
    return dlgLast(all, function (b) { return b.classList.contains("primary"); }) || dlgLast(all, function (b) { return b.classList.contains("danger-solid"); });
  }
  function dialogKeySubmit(e, box) {
    if (!submitKey(e) || e.defaultPrevented || e.isComposing || e.keyCode === 229) return false;
    var b = dialogPrimary(box, e.target);
    if (!b) return false;
    e.preventDefault(); e.stopPropagation();
    if (e.repeat) return true;
    if (b.type === "submit" && b.form && b.form.requestSubmit) b.form.requestSubmit(b); else b.click();
    return true;
  }
  // The key goes into the main button's tooltip ("保存 · ⌘Enter"), added when the pointer or
  // focus reaches it, so buttons drawn later get it too; a tooltip that already names it stays.
  function dialogKeyHint(b) {
    if (!b || b._kbHint) return;
    b._kbHint = true;
    var k = keyLabel("Mod+Enter"), t = b.getAttribute("data-tip") || "";
    if (t.indexOf(k) < 0) b.setAttribute("data-tip", t ? t + " · " + k : T("dlg.key", { k: k }));
    var ks = b.getAttribute("aria-keyshortcuts");
    if (!ks) b.setAttribute("aria-keyshortcuts", KB_MAC ? "Meta+Enter" : "Control+Enter");
  }
  function dialogKeys(box) {
    box.addEventListener("keydown", function (e) { dialogKeySubmit(e, box); });
    var hint = function (e) { var b = e.target && e.target.closest && e.target.closest("button"); if (b && b === dialogPrimary(box, b)) dialogKeyHint(b); };
    box.addEventListener("mouseover", hint);
    box.addEventListener("focusin", hint);
  }
  // Modal: role=dialog, Esc closes, Tab stays inside, focus returns to the opener.
  // Esc, the backdrop, × / Cancel and the browser Back button ask before dropping edits;
  // the close() handed to onMount (used after a successful save) never asks.
  function modal(html, onMount, onClose) {
    var root = $("#modal-root"), opener = document.activeElement;
    root.innerHTML = '<div class="modal-bg"><div class="card modal" role="dialog" aria-modal="true" tabindex="-1">' + html + "</div></div>";
    var box = $(".modal", root), h = $("h2", box);
    if (h) { h.id = "modal-title"; box.setAttribute("aria-labelledby", "modal-title"); }
    var close = function () {
      if (!root.contains(box)) return;
      draftFlush();
      root.innerHTML = ""; document.removeEventListener("keydown", onKey, true);
      if (modalGuard && modalGuard.box === box) modalGuard = null;
      if (onClose) onClose();
      if (opener && opener.focus && document.contains(opener)) opener.focus();
    };
    var bar = null, footer = null, back = null;
    var keep = function () {
      if (!bar) return;
      bar.remove(); bar = null; box.classList.remove("discarding");
      if (footer) footer.classList.remove("hidden");
      if (back && document.contains(back)) back.focus(); else box.focus();
    };
    var ask = function (then) {
      if (bar) { $("#dc-keep", bar).focus(); return; }
      back = document.activeElement;
      var all = $$(".actions", box); footer = all.length ? all[all.length - 1] : null;
      bar = document.createElement("div");
      bar.className = "actions discard-bar"; bar.setAttribute("role", "alert");
      bar.innerHTML = '<span class="dc-msg">' + ic("info", "sm") + esc(T("cf.discard_q")) + '</span><button type="button" class="btn ghost" id="dc-keep">' + esc(T("cf.keep_editing")) + '</button><button type="button" class="btn danger-solid" id="dc-go">' + esc(T("cf.discard")) + "</button>";
      if (footer) { footer.classList.add("hidden"); footer.parentNode.insertBefore(bar, footer.nextSibling); } else box.appendChild(bar);
      box.classList.add("discarding");
      $("#dc-keep", bar).addEventListener("click", keep);
      $("#dc-go", bar).addEventListener("click", function () { close(); if (then) then(); });
      $("#dc-keep", bar).focus();
    };
    var tryClose = function () { if (bar) { keep(); return; } if (guardDirty(box)) ask(); else close(); };
    modalGuard = { box: box, dirty: function () { return root.contains(box) && guardDirty(box); }, ask: ask };
    ["pointerdown", "keydown", "focusin"].forEach(function (ev) { box.addEventListener(ev, function () { guardBase(box); }, true); });
    ["input", "change"].forEach(function (ev) { box.addEventListener(ev, function (e) { if (e.isTrusted && guardable(e.target)) e.target._tbTouched = true; }, true); });
    var onKey = function (e) {
      if (!root.contains(box)) { document.removeEventListener("keydown", onKey, true); return; }
      if (e.key === "Escape") { e.preventDefault(); tryClose(); return; }
      if (e.key !== "Tab") return;
      var f = $$('a[href],button:not([disabled]),input:not([disabled]):not(.hidden),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])', box).filter(function (x) { return x.offsetParent !== null; });
      if (!f.length) return;
      if (e.shiftKey && document.activeElement === f[0]) { e.preventDefault(); f[f.length - 1].focus(); }
      else if (!e.shiftKey && document.activeElement === f[f.length - 1]) { e.preventDefault(); f[0].focus(); }
    };
    document.addEventListener("keydown", onKey, true);
    $(".modal-bg", root).addEventListener("mousedown", function (e) { if (e.target.classList.contains("modal-bg")) tryClose(); });
    // Standard dialogs: a close button in the header, and header/footer shadows only while
    // there is content scrolled under them (the header and actions are sticky in CSS).
    var head = box.firstElementChild;
    if (head && head.classList.contains("card-head") && !$("[data-close]", head)) head.insertAdjacentHTML("beforeend", '<button type="button" class="icon-btn sm modal-x" data-close aria-label="' + esc(T("btn.close")) + '" data-tip="' + esc(T("btn.close")) + ' · Esc">' + ic("x", "sm") + "</button>");
    var edges = function () { box.classList.toggle("sc-top", box.scrollTop > 0); box.classList.toggle("sc-bot", box.scrollTop + box.clientHeight < box.scrollHeight - 1); };
    box.addEventListener("scroll", edges, { passive: true });
    $$("[data-close]", root).forEach(function (b) { b.addEventListener("click", tryClose); });
    dialogKeys(box);
    if (onMount) onMount(root, close);
    requestAnimationFrame(edges);
    var first = $("[autofocus]", box) || $("input:not([type=hidden]):not(.hidden),textarea,select", box);
    (first && first.offsetParent !== null ? first : box).focus();
  }
  // confirmDialog({title, body, ok, danger, typeName}) resolves true only when confirmed.
  function confirmDialog(o) {
    return new Promise(function (resolve) {
      var done = false;
      modal('<div class="card-head"><span class="ico' + (o.danger ? " danger" : "") + '">' + ic(o.icon || (o.danger ? "trash" : "info")) + "</span><h2>" + esc(o.title) + "</h2></div>" +
        (o.body ? '<p class="confirm-body">' + esc(o.body) + "</p>" : "") +
        (o.typeName ? '<label class="field"><span>' + esc(T("cf.type_name", { n: o.typeName })) + '</span><input id="cf-name" autocomplete="off" spellcheck="false" data-noguard></label>' : "") +
        '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn ' + (o.danger ? "danger-solid" : "primary") + '" id="cf-ok"' + (o.typeName ? " disabled" : " autofocus") + ">" + esc(o.ok || T("cf.ok")) + "</button></div>", function (root, close) {
        var ok = $("#cf-ok", root), nm = $("#cf-name", root);
        if (nm) nm.addEventListener("input", function () { ok.disabled = nm.value.trim() !== o.typeName; });
        ok.addEventListener("click", function () { done = true; close(); resolve(true); });
        var obs = new MutationObserver(function () { if (!root.contains(ok)) { obs.disconnect(); if (!done) resolve(false); } });
        obs.observe(root, { childList: true });
      });
    });
  }
  function skeleton(kind) {
    var row = '<div class="sk-row"><span class="sk sk-ico"></span><span class="sk sk-line w60"></span><span class="sk sk-line w20"></span></div>';
    return '<div class="skel" aria-busy="true" aria-label="' + esc(T("st.loading")) + '"><div class="sk sk-title"></div><div class="sk sk-line w40"></div>' +
      (kind === "cards" ? '<div class="grid g4 mt">' + '<div class="card sk-card"></div>'.repeat(4) + "</div>" : '<section class="card mt">' + row.repeat(6) + "</section>") + "</div>";
  }
  function errorState(msg, retry) {
    return '<section class="card"><div class="empty">' + ic("info") + "<strong>" + esc(T("err.generic")) + "</strong>" + esc(msg || "") + '<div class="mt"><button class="btn primary" id="err-retry">' + ic("refresh", "sm") + esc(T("err.retry")) + "</button></div></div></section>";
  }
  // One-click "run now" from any list; the toast links to the live run.
  function runTaskNow(id, btn) {
    if (btn) btn.disabled = true;
    return api("/api/tasks/" + enc(id) + "/run", { method: "POST", body: {} }).then(function (res) { toast(startedText(res, "toast.run_started")); if (/^#\/tasks$/.test(location.hash)) refresh(); }).catch(fail).then(function () { if (btn) btn.disabled = false; });
  }
  function wireRows(root) { $$("[data-href]", root || page()).forEach(function (el) { el.addEventListener("click", function (e) { if (e.target.closest("a,button")) return; location.hash = el.getAttribute("data-href"); }); }); }

  // runHitHtml: the excerpt behind a search hit (server-cut, parts alternate plain / matched),
  // shown in place of the trigger line: the field as a muted prefix, matches marked, how many
  // hits in the tooltip. A hit in the task name has no excerpt; the title is marked instead.
  function runHitHtml(m) {
    if (!m || !m.parts || !m.parts.length) return "";
    return '<small class="ell run-hit" data-tip="' + esc(T("runs.hit_tip", { f: T("runs.hit_" + m.field), n: num(m.count || 1) })) + '"><span class="hit-f">' + esc(T("runs.hit_" + m.field)) + "</span>" +
      m.parts.map(function (p, i) { return i % 2 ? "<mark>" + esc(p) + "</mark>" : esc(p); }).join("") + "</small>";
  }
  // "▢ 3 +12 −3" beside an edit run's title: how much it changed in its folder, without opening
  // it. A side with no lines is left out (+40 is a new file, −40 a deletion); a run that changed
  // nothing shows no badge. Clicking it opens the run at its "Changed files" card. Idea:
  // siteboon/claudecodeui src/modules/chat/tools/DiffStatsBadge.tsx (+N −N in green/red on a
  // collapsed edit so its size reads without expanding it).
  function runChgBadge(r) {
    var c = r.changes;
    if (!c || !(c.files > 0)) return "";
    var tip = T("runs.chg_tip", { f: plural(c.files, "runs.chg_files_one", "runs.chg_files_many"), a: num(c.add || 0), d: num(c.del || 0) });
    if (c.undone) tip = T("runs.chg_undone_tip") + " · " + tip;
    else if (c.undone_n > 0) tip = T("runs.chg_partly_tip", { n: num(c.undone_n) }) + " · " + tip;
    return '<a class="chg-badge' + (c.undone ? " undone" : c.undone_n > 0 ? " partly" : "") + '" href="#/runs/' + enc(r.id) + '" data-chg-jump data-tip="' + esc(tip) + '" aria-label="' + esc(tip) + '">' + ic("git", "sm") +
      '<span class="n">' + esc(num(c.files)) + "</span>" + (c.add ? '<span class="add">+' + esc(num(c.add)) + "</span>" : "") + (c.del ? '<span class="del">−' + esc(num(c.del)) + "</span>" : "") + "</a>";
  }
  function runsTable(runs, rerun, q) {
    var lq = (q || "").trim().toLowerCase();
    if (!runs.length) return emptyState("runs", T("runs.none"), T("runs.none_sub"));
    return '<table class="table"><thead><tr><th>' + esc(T("col.run")) + "</th><th>" + esc(T("col.provider")) + "</th><th>" + esc(T("col.status")) + "</th><th>" + esc(T("col.started")) + '</th><th class="c-opt">' + esc(T("col.duration")) + '</th><th class="right">' + esc(T("col.cost")) + '</th><th class="c-act"></th></tr></thead><tbody>' +
      runs.map(function (r) {
        var running = r.status === "running", active = running || r.status === "queued";
        return '<tr class="click" data-href="#/runs/' + enc(r.id) + '"><td><div class="cell-main"><span class="ico" data-hue="' + iconHue(runIcon(r)) + '">' + ic(runIcon(r), "sm") + "</span><div>" + '<div class="run-line"><a class="row-link" href="#/runs/' + enc(r.id) + '"><strong class="ell">' + hlText(runTitle(r), lq) + "</strong></a>" + runChgBadge(r) + "</div>" + (runHitHtml(r.match) || '<small class="ell">' + esc(runCtxText(r)) + (r.version_no ? " · v" + esc(r.version_no) : "") + "</small>") + "</div></div></td>" +
          '<td class="run-prov"><span class="ell">' + esc(r.provider || "—") + "</span>" + (r.model ? '<small class="ell">' + esc(r.model) + "</small>" : "") + "</td>" +
          "<td>" + statusPill(r.status, r.queue_pos, r.error) + '</td><td class="nowrap">' + esc(when(r.started_at || r.created_at)) + '</td><td class="nowrap c-opt">' + esc(running ? dur(Date.now() - new Date(r.started_at).getTime()) : dur(r.duration_ms)) +
          '</td><td class="right"><strong>' + esc(usd(r.cost_usd)) + '</strong></td><td class="right nowrap">' + (rerun && active ? '<button class="icon-btn sm row-act" data-stop="' + esc(r.id) + '" data-st="' + esc(r.status) + '" data-tip="' + esc(T(r.status === "queued" ? "run.dequeue" : "run.stop")) + '" aria-label="' + esc(T(r.status === "queued" ? "run.dequeue" : "run.stop")) + '">' + ic("stop", "sm") + "</button>" : "") + (rerun && /failed|timeout|canceled/.test(r.status) ? '<button class="icon-btn sm row-act" data-rerun="' + esc(r.id) + '" data-tip="' + esc(T("runs.rerun")) + '" aria-label="' + esc(T("runs.rerun")) + '">' + ic("refresh", "sm") + "</button>" : "") + "</td></tr>";
      }).join("") + "</tbody></table>";
  }
  // A start request answers once the new run exists, with its id: open exactly that run so
  // its log streams right away. No id means it is queued behind the concurrency limit.
  function openStarted(res, msg) {
    if (res && res.run_id) { toast(startedText(res, msg)); location.hash = "#/runs/" + enc(res.run_id); return; }
    toast(T("toast.run_queued")); location.hash = "#/runs";
  }
  // Toast for a start answer: a run waiting for a free slot says where it is in line.
  function startedText(res, msg) {
    return res && res.queue_pos ? T("toast.queued_n", { n: res.queue_pos }) : T(res && res.run_id ? msg : "toast.run_queued");
  }
  // Stop a run: a queued one just leaves the line (nothing to lose, no question); a
  // running one is killed after a confirm, keeping what it has spent. Resolves true
  // when it was stopped; a run that ended meanwhile says so instead of an error.
  function stopRun(id, st) {
    var go = function () {
      return api("/api/runs/" + enc(id) + "/stop", { method: "POST", body: {} }).then(function (res) {
        toast(T(res && res.was === "queued" ? "run.dequeued" : "run.stopped")); return true;
      }, function (e) { if (e.status === 409) { toast(T("run.stop_gone")); return true; } throw e; });
    };
    if (st === "queued") return go();
    return confirmDialog({ title: T("run.stop_title"), body: T("run.stop_body"), ok: T("run.stop"), danger: true, icon: "stop" }).then(function (y) { return y ? go() : false; });
  }
  // ---------- HOME ----------
  function runButton(id, el) {
    if (el) el.classList.add("busy");
    return api("/api/buttons/" + enc(id) + "/run", { method: "POST", body: {} })
      .then(function (r) { toast(T("toast.started", { name: r.started })); refresh(); })
      .catch(fail)
      .then(function () { if (el) el.classList.remove("busy"); });
  }
  // ---------- live polling: pauses while the tab is hidden, catches up when it returns ----------
  var poller = { t: null, fn: null, ms: 0, hash: "" };
  // same(hash): optional test for "still this page" — the task list rewrites its address when a
  // filter changes (replaceState), which must not end its poll; default is the exact address.
  function livePoll(fn, ms, same) {
    stopPoll();
    poller.fn = fn; poller.ms = ms; poller.hash = location.hash || "#/";
    var here = same || function (h) { return h === poller.hash; };
    var tick = function () {
      poller.t = null;
      if (!here(location.hash || "#/") || !poller.fn) { stopPoll(); return; }
      if (document.hidden) return; // resumed by visibilitychange
      Promise.resolve(poller.fn()).catch(function () {}).then(function () { if (poller.fn && !poller.t && !document.hidden) poller.t = setTimeout(tick, poller.ms); });
    };
    poller.tick = tick;
    poller.t = setTimeout(tick, ms);
  }
  function stopPoll() { clearTimeout(poller.t); poller.t = null; poller.fn = null; }
  // Change gate for live pages: ask /api/rev (one in-memory SQLite counter, no table scans)
  // and run the page's heavy reload only when the token moved since the last good reload.
  // The token also turns over each minute, so time-based views (next 24 h) stay fresh.
  function revGate() {
    var seen = null;
    return function (load) {
      if (net.down) return Promise.resolve(); // the reconnect probe is already asking
      // the change stream already knows the token: no request at all
      return (evsLive() ? Promise.resolve({ rev: evs.st.rev }) : apiRaw("/api/rev")).then(function (r) {
        var rev = r && r.rev;
        if (rev && rev === seen) return;
        return Promise.resolve(load()).then(function () { seen = rev; });
      });
    };
  }
  // Seconds between change checks: 2 while something runs, 5 while you're using the page,
  // 15 once there's been no input for 2 minutes (the desktop window often sits open all day).
  // With the change stream up a check costs nothing, so it is 2 s throughout: the page
  // follows changes within 2 s and still reloads only when something changed.
  function liveEvery(busy) { return busy || evsLive() ? 2 : Date.now() - (state.lastInput || 0) < 120000 ? 5 : 15; }
  // ---------- quiet follow: pages that aren't built for live updates follow changes too ----------
  // The task list and a task's page used to show what they had when opened: a run that finished,
  // a task paused by its failure streak or edited in another window only appeared after a
  // reload. When rev moves they refetch their data and repaint only if it differs from what is
  // on screen — and only while you aren't in the middle of something there (typing, an inline
  // edit, a ticked amendment, an open dialog, drawer or menu, selected text). Otherwise the new
  // data is held and painted at the first quiet check, with no second request.
  // (idea: uptime-kuma src/mixins/socket.js "updateMonitorIntoList" patches the list in place
  // while src/components/MonitorList.vue keeps its search text and selection.)
  function liveQuiet(box) {
    if ($("#modal-root").innerHTML || $("#drawer") || $(".popover[role=menu]")) return false;
    if (!box || !document.contains(box)) return false;
    var a = document.activeElement;
    if (a && box.contains(a) && (/^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName) || a.isContentEditable)) return false;
    if ($(".editing", box)) return false;
    var s = window.getSelection ? window.getSelection() : null;
    if (s && !s.isCollapsed && s.anchorNode && box.contains(s.anchorNode)) return false;
    return true;
  }
  // keepFocus(box, paint): a repaint of box puts the focus back on the same row or row control
  // (matched by its data-href / data-pick / data-runnow), so j / k, x and Tab carry on.
  function keepFocus(box, paint) {
    var a = document.activeElement, key = null;
    if (a && box.contains(a)) ["data-pick", "data-runnow", "data-href"].some(function (n) { var v = a.getAttribute(n); if (v != null) key = [n, v]; return key; });
    paint();
    if (!key) return;
    var el = $$("[" + key[0] + "]", box).filter(function (x) { return x.getAttribute(key[0]) === key[1]; })[0];
    if (el && el.focus) el.focus({ preventScroll: true });
  }
  // liveFollow(load, shown, apply, quiet) → a check for livePoll. shown = the data on screen.
  function liveFollow(load, shown, apply, quiet) {
    var gate = revGate(), sig = JSON.stringify(shown), held = null;
    return function () {
      if (held) { if (quiet()) { var d = held; held = null; apply(d); } return; }
      return gate(function () {
        return load().then(function (d) {
          var s = JSON.stringify(d);
          if (s === sig) return;
          sig = s;
          if (quiet()) apply(d); else held = d;
        });
      });
    };
  }
  document.addEventListener("visibilitychange", function () {
    if (!document.hidden) state.lastInput = Date.now(); // coming back counts as activity
    if (!document.hidden && poller.fn && !poller.t) poller.tick();
    if (!document.hidden && state.esPaused) { state.esPaused = false; if (state.esResume) state.esResume(); }
  });

