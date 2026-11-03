  // ---------- intent prefetch ----------
  // Resting the pointer on a link for a moment, pressing it, tabbing to it or picking its row
  // with j / k loads that page's data into the page cache, so the click renders at once
  // (no skeleton on a slow link). Only reads, at most 6 at a time, nothing on Save-Data, a
  // hidden tab or the page already shown; a 401 here never opens the sign-in screen.
  var PF_REST_MS = 60, PF_MAX = 6, pf = { busy: 0, el: null, t: null };
  function pfGet(path) { return { key: path, run: function () { return apiRaw(path, { quiet: true }); } }; }
  function pfRpc(method, params) { return { key: rpcKey(method, params), run: function () { return apiRaw("/api/rpc/" + method, { method: "POST", body: params || {}, quiet: true }); } }; }
  function pfDecode(x) { try { return decodeURIComponent(x); } catch (e) { return ""; } }
  // Same requests the page's render starts with; routes with filters in the address are left out.
  function prefetchReqs(hash) {
    var h = String(hash || "").replace(/^#\/?/, ""), qi = h.indexOf("?");
    if (qi >= 0) return [];
    var parts = h.split("/").filter(Boolean).map(pfDecode);
    if (parts.some(function (x) { return !x; })) return [];
    var id = parts[1];
    switch (parts[0]) {
      case undefined: return [pfGet("/api/overview"), pfGet("/api/dashboard")];
      case "tasks": return id ? taskDetailPaths(id, parts[2] || "overview").map(pfGet) : [pfGet("/api/tasks")];
      case "runs": return id ? [pfGet("/api/runs/" + enc(id))] : [];
      case "skills": return id ? [pfGet("/api/skills"), pfGet("/api/skills/" + enc(id) + "/versions"), pfRpc("skills.optimize_settings", { skill_id: id }), pfRpc("skills.proposals", { skill_id: id }), pfRpc("skills.compare", { skill_id: id })] : [pfGet("/api/skills")];
    }
    return [];
  }
  function prefetchRoute(hash) {
    var conn = navigator.connection;
    if (!state.csrf || document.hidden || !hash || hash === (location.hash || "#/") || (conn && conn.saveData)) return 0;
    var n = 0, gen = swr.gen;
    prefetchReqs(hash).forEach(function (q) {
      var c = swr.cache[q.key];
      // Anything the page cache would serve already opens at once (and revalidates as before).
      if (swr.pending[q.key] || (c && Date.now() - c.t < SWR_MAX_MS) || pf.busy >= PF_MAX) return;
      n++; pf.busy++;
      var done = function () { pf.busy--; delete swr.pending[q.key]; };
      // A write while this was in flight (gen moved) means the answer may predate it: don't keep it.
      var pr = swr.pending[q.key] = q.run().then(function (d) { done(); if (gen === swr.gen) swrPut(q.key, d, true); return d; }, function (e) {
        done();
        // Keep a server refusal (4xx) so the page doesn't ask again; network errors and 5xx are just dropped.
        if (gen === swr.gen && e.status >= 400 && e.status < 500 && e.status !== 401 && !swr.cache[q.key]) { swr.order.push(q.key); swr.cache[q.key] = { err: { m: e.message, status: e.status, data: e.data }, t: Date.now(), pf: true }; }
        throw e;
      });
      pr.catch(function () {});
    });
    return n;
  }
  function pfTarget(el) {
    var a = el && el.closest && el.closest('a[href^="#/"], [data-href^="#/"]');
    if (!a || a.closest("#modal-root")) return "";
    return a.getAttribute("data-href") || a.getAttribute("href") || "";
  }
  (function () {
    var clear = function () { clearTimeout(pf.t); pf.t = null; pf.el = null; };
    // Wait for the pointer to rest (mousemove restarts the clock), so sweeping across a list loads nothing.
    document.addEventListener("mouseover", function (e) {
      var h = pfTarget(e.target);
      if (!h) { clear(); return; }
      if (h === pf.el) return;
      clear(); pf.el = h;
      pf.t = setTimeout(function () { pf.t = null; prefetchRoute(h); }, PF_REST_MS);
    });
    document.addEventListener("mousemove", function (e) {
      if (!pf.el || pfTarget(e.target) !== pf.el) return;
      var h = pf.el;
      clearTimeout(pf.t); pf.t = setTimeout(function () { pf.t = null; prefetchRoute(h); }, PF_REST_MS);
    }, { passive: true });
    var now = function (e) { var h = pfTarget(e.target); if (h) prefetchRoute(h); };
    document.addEventListener("pointerdown", now, true);
    document.addEventListener("touchstart", now, { passive: true, capture: true });
    document.addEventListener("focusin", now);
  })();

  // ---------- router ----------
  var lastHash = null;
  // refresh(): re-render the current page in place (keeps scroll and open modals closed state).
  function refresh() { return route(true); }
  // Each route() call gets a number; only the newest one may paint its failure.
  var routeSeq = 0;
  function route(soft) {
    soft = soft === true;
    if (soft && repositoryScan) return Promise.resolve();
    if (soft && workflowDraftGuard && workflowDraftGuard.dirty()) return Promise.resolve();
    if (!soft) workflowDraftGuard = null;
    stopStream(); stopChatStream(); stopPoll();
    if (!soft) stopRepositoryScan();
    clearTimeout(scheduleRefresh._t);
    if (!soft) { draftFlush(); $("#modal-root").innerHTML = ""; closeDrawer(); }
    var y = window.scrollY, pg = page(), sy = pg ? pg.scrollTop : 0;
    if (!soft && location.hash !== lastHash && pg) {
      // Keep the old page on screen; only show a skeleton if loading is slow.
      if (lastHash != null) scrollPos[lastHash] = Math.max(y, sy);
      pendingRestore = scrollPos[location.hash] || 0;
      var seq = mountSeq, kind = /^#\/(buttons)?$/.test(location.hash || "#/") ? "cards" : "list";
      setTimeout(function () { if (mountSeq === seq && page()) { page().innerHTML = skeleton(kind); page().setAttribute("aria-busy", "true"); } }, 200);
    }
    lastHash = location.hash;
    var rs = ++routeSeq;
    var nav0 = (location.hash.replace(/^#\/?/, "").split(/[/?]/)[0]) || "home";
    if (!soft) setNav(nav0);
    var h = location.hash.replace(/^#\/?/, "");
    var qi = h.indexOf("?"), params = new URLSearchParams(qi >= 0 ? h.slice(qi + 1) : "");
    var parts = (qi >= 0 ? h.slice(0, qi) : h).split("/").filter(Boolean).map(decodeURIComponent);
    var p;
    swr.on = true; swr.soft = soft;
    try {
    switch (parts[0]) {
      case undefined: p = renderHome(); break;
      case "tasks": p = parts[1] ? renderTaskDetail(parts[1], parts[2]) : renderTasks(params); break;
      case "skill-sets": p = renderSkillSets(); break;
      case "skill-repositories": p = renderSkillRepositories(parts[1]); break;
      case "skills": p = parts[1] ? renderSkillDetail(parts[1], params) : renderSkills(); break;
      case "workflows": p = parts[1] ? renderWorkflowEditor(parts[1]) : renderWorkflows(); break;
      case "workflow-runs": p = renderWorkflowRun(parts[1]); break;
      case "buttons": p = renderButtons(); break;
      case "plugins": p = renderPlugins(); break;
      case "runs": p = parts[1] ? renderRunDetail(parts[1]) : renderRuns(params); break;
      case "spend": p = renderSpend(params); break;
      case "settings": p = renderSettings(parts[1], parts[2]); break;
      default: p = renderHome();
    }
    } finally { swr.on = false; }
    p = Promise.resolve(p).then(function () { if (soft) { window.scrollTo(0, y); if (page()) page().scrollTop = sy; } }, function (e) {
      routeFailed(e, rs);
    });
    loadCC(false);
    return p;
  }
  // A page that could not load shows the error with a retry, not a skeleton forever: a fast
  // refusal (404 for a deleted run, task or workflow) used to land before the 200 ms skeleton
  // timer, which then covered it with aria-busy. Bumping mountSeq cancels that timer, and a
  // failure from a route the user already left never paints over the page they went to.
  function routeFailed(e, rs) {
    var pg = page();
    if (e.auth || rs !== routeSeq || !pg) return;
    mountSeq++;
    // The top bar names the section the sidebar shows (route() already set it), not the last page.
    var nav = $("#nav a.active");
    renderTopbar.nav = nav ? nav.getAttribute("data-nav") : "";
    renderTopbar([]);
    pg.removeAttribute("aria-busy");
    pg.innerHTML = errorState(e.status === 404 ? T("err.not_found") : e.message);
    $("#err-retry").addEventListener("click", function () { lastHash = null; route(); });
  }
  // Back/forward while a dialog holds edits: stay put and ask; "Discard" then goes on.
  window.addEventListener("hashchange", function () {
    if (modalGuard && location.hash !== lastHash && modalGuard.dirty()) {
      var want = location.hash;
      history.replaceState(null, "", lastHash || "#/");
      modalGuard.ask(function () { location.hash = want; });
      return;
    }
    if (workflowDraftGuard && location.hash !== lastHash && workflowDraftGuard.dirty()) {
      var target = location.hash;
      history.replaceState(null, "", lastHash || "#/");
      confirmDialog({title:T("wf.leave_title"),body:T("wf.leave_hint"),ok:T("btn.discard"),danger:true}).then(function(yes){if(yes){workflowDraftGuard=null;location.hash=target;}});
      return;
    }
    route();
  });
  // Closing/reloading the tab with edits in a dialog: the browser's own "leave site?" prompt.
  // Not in the desktop window: WKWebView never shows it while WebView2 does, so it would differ by OS.
  window.addEventListener("beforeunload", function (e) {
    if (!state.native && modalGuard && modalGuard.dirty()) { e.preventDefault(); e.returnValue = ""; return ""; }
    if (!state.native && workflowDraftGuard && workflowDraftGuard.dirty()) { e.preventDefault(); e.returnValue = ""; return ""; }
  });

  // ---------- appearance: theme × colour mode ----------
  // The server puts the saved look on <html data-theme data-mode> in the page itself, so the
  // first paint is already right; switching here only flips the attributes (styles.css has
  // every palette) and saves. Transitions are held for a frame so nothing fades between themes.
  var THEMES = ["graphite", "tide", "midnight"], MODES = ["system", "light", "dark"];
  function curLook() {
    var h = document.documentElement, t = h.getAttribute("data-theme"), m = h.getAttribute("data-mode");
    return { theme: THEMES.indexOf(t) >= 0 ? t : "graphite", mode: MODES.indexOf(m) >= 0 ? m : "system" };
  }
  // applyLook only repaints (palette preview); setLook also saves.
  function applyLook(look) {
    var h = document.documentElement;
    h.classList.add("tb-theming");
    h.setAttribute("data-theme", look.theme); h.setAttribute("data-mode", look.mode);
    requestAnimationFrame(function () { requestAnimationFrame(function () { h.classList.remove("tb-theming"); }); });
  }
  function setLook(look) {
    applyLook(look);
    return api("/api/settings/appearance", { method: "PUT", body: look });
  }
  // Settings › General shows the saved look on its swatches and mode segment.
  function lookMark(look) {
    $$("[data-look-theme]").forEach(function (b) { var on = b.getAttribute("data-look-theme") === look.theme; b.classList.toggle("on", on); b.setAttribute("aria-checked", String(on)); });
    $$("[data-look-mode]").forEach(function (b) { var on = b.getAttribute("data-look-mode") === look.mode; b.classList.toggle("active", on); b.setAttribute("aria-pressed", String(on)); });
  }
  function lookCardHtml() {
    var cur = curLook();
    var sw = THEMES.map(function (t) {
      return '<button type="button" class="tsw' + (t === cur.theme ? " on" : "") + '" data-pv="' + t + '" data-look-theme="' + t + '" role="radio" aria-checked="' + (t === cur.theme) + '" data-tip="' + esc(T("theme." + t + "_sub")) + '">' +
        '<span class="tsw-pv" aria-hidden="true"><i class="pv-side"></i><i class="pv-main"><b class="pv-bar"></b><b class="pv-card"></b><b class="pv-card"></b><b class="pv-btn"></b></i></span>' +
        '<span class="tsw-name">' + ic("checkc", "sm") + esc(T("theme." + t)) + "</span></button>";
    }).join("");
    var modes = MODES.map(function (m) {
      return '<button type="button" data-look-mode="' + m + '" class="' + (m === cur.mode ? "active" : "") + '" aria-pressed="' + (m === cur.mode) + '">' + ic(m === "system" ? "monitor" : m === "light" ? "sun" : "moon", "sm") + esc(T("mode." + m)) + "</button>";
    }).join("");
    return '<section class="card" id="set-appearance"><div class="card-head"><span class="ico">' + ic("palette") + "</span><h2>" + esc(T("set.appearance")) + "</h2></div>" +
      '<div class="set-row" id="set-theme"><div class="lbl"><strong>' + esc(T("set.theme")) + "</strong><small>" + esc(T("set.theme_sub")) + "</small></div></div>" +
      '<div class="tsw-row" role="radiogroup" aria-label="' + esc(T("set.theme")) + '">' + sw + "</div>" +
      '<div class="set-row" id="set-mode"><div class="lbl"><strong>' + esc(T("set.mode")) + "</strong><small>" + esc(T("set.mode_sub")) + '</small></div><div class="seg" role="group" aria-label="' + esc(T("set.mode")) + '">' + modes + "</div></div></section>";
  }
  function lookWire() {
    var save = function (look, el) {
      setLook(look).then(function () { savedMark(el); }).catch(fail);
      lookMark(look);
    };
    $$("[data-look-theme]").forEach(function (b) { b.addEventListener("click", function () { var l = curLook(); l.theme = b.getAttribute("data-look-theme"); save(l, $("#set-theme .lbl")); }); });
    $$("[data-look-mode]").forEach(function (b) { b.addEventListener("click", function () { var l = curLook(); l.mode = b.getAttribute("data-look-mode"); save(l, $("#set-mode .seg")); }); });
  }

  function langSelect(id) {
    var opts = [["auto", T("lang.auto")], ["zh-CN", "简体中文"], ["en", "English"]];
    return '<select class="lang-sel" id="' + id + '" aria-label="' + esc(T("top.language")) + '">' + opts.map(function (o) { return '<option value="' + o[0] + '"' + (o[0] === state.langPref ? " selected" : "") + ">" + esc(o[1]) + "</option>"; }).join("") + "</select>";
  }
  // Persist the preference server-side (setting "language") and re-render.
  function setLanguage(pref) {
    return loadLang(pref).then(function () {
      renderShell(); route();
      if (state.csrf) api("/api/settings/language", { method: "PUT", body: { language: pref } }).catch(fail);
    });
  }
  function boot() {
    api("/api/me").then(function (me) {
      state.csrf = me.csrf_token;
      state.osLang = me.os_language || "";
      return loadLang(me.language || "auto").then(function () { return me; });
    }).then(function (me) {
      state.tz = me.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone;
      state.version = me.version || "";
      state.sourceURL = me.source_url || SOURCE_REPO;
      state.license = me.license || "AGPL-3.0-only";
      state.currency = me.currency || "USD";
      $("#login").classList.add("hidden");
      $("#shell").classList.remove("hidden");
      nativeSave().then(function (n) { state.native = n; });
      dropBoot();
      renderShell();
      route();
      evsStart();
    }).catch(function (e) {
      // Unreachable at start: keep the splash, the connection bar retries and boots again.
      if (e && e.offline) return;
      showLogin();
    });
  }
  var storedLang = "auto";
  try { storedLang = localStorage.getItem("tb_lang") || "auto"; } catch (e) {}
  loadLang(storedLang).then(boot);
})();
