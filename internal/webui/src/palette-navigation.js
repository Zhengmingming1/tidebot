  // ---------- command palette ----------
  // Ctrl/⌘+K opens it. Arrow keys move, Enter runs. Search responses that arrive
  // after a newer query was typed are dropped (seq check), so slow requests never
  // overwrite fresh results.
  // "Change theme…" opens a page of its own (pacocoursey/cmdk "pages"): moving through it
  // previews the theme or mode on the whole window (VS Code's theme picker), Enter keeps it,
  // Esc / Backspace on an empty box go back and closing without Enter puts the old look back.
  var pal = { seq: 0, items: [], sel: 0, buttons: null, timer: null, page: "", look0: null, pv: 0, kept: false };
  function palCommands() {
    var go = function (h) { return function () { location.hash = h; }; };
    return [
      { kind: "cmd", icon: "plus", label: T("pal.new_task"), keys: "n t", run: function () { taskFormModal(null); } },
      { kind: "cmd", icon: "sparkle", label: T("home.quick"), keys: "n q", run: function () { quickRunModal(); } },
      { kind: "cmd", icon: "upload", label: T("sk.import"), keys: "n s", run: function () { skillImportModal(function () { location.hash = "#/skills"; refresh(); }); } },
      { kind: "cmd", icon: "key", label: T("pv.add"), keys: "n p", run: function () { goClick("#/settings/providers", "#pv-add"); } },
      { kind: "cmd", icon: "bolt", label: T("bt.new"), keys: "n b", run: function () { goClick("#/buttons", "#b-add"); } },
      { kind: "cmd", icon: "globe", label: T("pal.lang_to", { l: state.lang === "zh-CN" ? "English" : "简体中文" }), run: function () { setLanguage(state.lang === "zh-CN" ? "en" : "zh-CN"); } },
      { kind: "cmd", icon: "bell", label: T("pal.notifications"), run: function () { setTimeout(toggleNotif, 0); } },
      { kind: "cmd", icon: "palette", label: T("pal.look"), kw: T("pal.look_kw"), stay: true, run: function () { palPage("look"); } },
    ].concat(spState && spState.paused ? [
      { kind: "cmd", icon: "play", label: T("spz.pal_resume"), run: function () { setSchedPause(false); } }
    ] : [
      { kind: "cmd", icon: "pause", label: T("spz.pal_pause"), run: function () { setSchedPause(true, "1h"); } },
      { kind: "cmd", icon: "pause", label: T("spz.pal_pause_manual"), run: function () { setSchedPause(true, "manual"); } }
    ]).concat([
      { kind: "cmd", icon: "more", label: T("pal.shortcuts"), run: function () { shortcutsHelp(); } },
      { kind: "nav", icon: "home", label: T("nav.home"), run: go("#/") },
      { kind: "nav", icon: "tasks", label: T("nav.tasks"), run: go("#/tasks") },
      { kind: "nav", icon: "tasks", label: T("pal.failing_tasks"), run: go("#/tasks?status=failing") },
      { kind: "nav", icon: "skills", label: T("nav.skills"), run: go("#/skills") },
      { kind: "nav", icon: "runs", label: T("nav.runs"), run: go("#/runs") },
      { kind: "nav", icon: "calendar", label: T("pal.runs_today"), run: go("#/runs?range=today") },
      { kind: "nav", icon: "spend", label: T("nav.spend"), run: go("#/spend") },
      { kind: "nav", icon: "settings", label: T("nav.settings"), run: go("#/settings") },
      { kind: "nav", icon: "database", label: T("nav.settings") + " › " + T("st.title"), run: go("#/settings/data/storage") }
    ]).concat(SET_SECTIONS.map(function (k) { return { kind: "nav", icon: "settings", label: T("nav.settings") + " › " + T("setnav." + k), run: go("#/settings/" + k) }; }));
  }
  function palMatch(label, q) { return !q || label.toLowerCase().indexOf(q.toLowerCase()) >= 0; }
  // The look page: three themes, then three colour modes. Each previews its own change on top
  // of the look the page was opened with, so a mode never drags along a theme only hovered.
  function palLookItems(root) {
    var base = pal.look0 || curLook(), cur = curLook();
    if (root) base = cur;
    var keep = function (look) { return function () { palKeepLook(look); }; };
    var crumb = root ? T("set.appearance") + " › " : "";
    return THEMES.map(function (t) {
      var look = { theme: t, mode: base.mode };
      return { kind: "theme", swatch: t, label: crumb + T("theme." + t), kw: t, sub: t === base.theme ? T("pal.current") : "", look: look, run: keep(look) };
    }).concat(MODES.map(function (m) {
      var look = { theme: base.theme, mode: m };
      return { kind: "mode", icon: m === "system" ? "monitor" : m === "light" ? "sun" : "moon", label: crumb + T("mode." + m), kw: m, sub: m === base.mode ? T("pal.current") : "", look: look, run: keep(look) };
    }));
  }
  function palPage(page) {
    var input = $("#pal-q");
    if (!input) return;
    if (page === "look") { pal.look0 = curLook(); pal.kept = false; } else palRevertLook();
    pal.page = page;
    var crumb = $("#pal-crumb");
    crumb.textContent = page === "look" ? T("set.appearance") : "";
    crumb.classList.toggle("hidden", !page);
    input.value = "";
    input.setAttribute("placeholder", T(page === "look" ? "pal.look_ph" : "pal.placeholder"));
    palFoot();
    palUpdate("");
    input.focus();
  }
  function palFoot() {
    var f = $(".pal-foot");
    if (!f) return;
    var k = function (keys, what) { return "<span>" + keys.map(function (x) { return "<kbd>" + esc(x) + "</kbd>"; }).join("") + " " + esc(T(what)) + "</span>"; };
    f.innerHTML = pal.page === "look" ? k(["↑", "↓"], "pal.k_preview") + k(["Enter"], "pal.k_apply") + k(["Esc"], "pal.k_back")
      : k(["↑", "↓"], "pal.k_move") + k(["Enter"], "pal.k_run") + k(["Esc"], "pal.k_close") + k(["?"], "pal.shortcuts");
  }
  // Preview waits a beat (like VS Code's 200 ms) so holding ↓ does not repaint every theme.
  function palPreview() {
    clearTimeout(pal.pv);
    if (pal.page !== "look") return;
    var it = pal.items[pal.sel];
    var look = it && it.look ? it.look : pal.look0;
    pal.pv = setTimeout(function () { if (pal.page === "look" && !pal.kept) applyLook(look); }, 90);
  }
  function palRevertLook() {
    clearTimeout(pal.pv);
    if (pal.page === "look" && !pal.kept && pal.look0) {
      var c = curLook();
      if (c.theme !== pal.look0.theme || c.mode !== pal.look0.mode) applyLook(pal.look0);
    }
    pal.page = ""; pal.look0 = null;
  }
  function palKeepLook(look) {
    pal.kept = true;
    clearTimeout(pal.pv);
    setLook(look).then(function () { toast(T("pal.look_set", { t: T("theme." + look.theme), m: T("mode." + look.mode) })); }).catch(fail);
    lookMark(look);
  }
  function openPalette() {
    var root = $("#pal-root");
    if (!root) { root = document.createElement("div"); root.id = "pal-root"; document.body.appendChild(root); }
    if (root.innerHTML) { var qi = $("#pal-q"); if (qi) qi.focus(); return; }
    var opener = document.activeElement;
    root.innerHTML = '<div class="modal-bg pal-bg"><div class="card palette" role="dialog" aria-modal="true" aria-label="' + esc(T("pal.title")) + '">' +
      '<div class="pal-in">' + ic("search") + '<button type="button" id="pal-crumb" class="pal-crumb hidden" data-tip="' + esc(T("pal.back")) + '"></button><input id="pal-q" type="text" autocomplete="off" maxlength="200" role="combobox" aria-expanded="true" aria-controls="pal-list" aria-autocomplete="list" placeholder="' + esc(T("pal.placeholder")) + '"></div>' +
      '<ul id="pal-list" role="listbox"></ul><div class="pal-foot muted"></div></div></div>';
    var input = $("#pal-q");
    pal.page = ""; pal.look0 = null;
    var close = function () { palRevertLook(); root.innerHTML = ""; pal.seq++; document.removeEventListener("keydown", onKey, true); if (opener && opener.focus && document.contains(opener)) opener.focus(); };
    pal.close = close;
    var onKey = function (e) {
      if (!root.innerHTML) return;
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); if (pal.page) palPage(""); else close(); }
      else if (e.key === "Backspace" && pal.page && !input.value && e.target === input) { e.preventDefault(); palPage(""); }
      else if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); palSelect(pal.sel + (e.key === "ArrowDown" ? 1 : -1)); }
      else if (e.key === "Enter") { e.preventDefault(); palRun(pal.sel); }
      else if (e.key === "Tab") { e.preventDefault(); input.focus(); }
    };
    document.addEventListener("keydown", onKey, true);
    $(".pal-bg", root).addEventListener("mousedown", function (e) { if (e.target.classList.contains("pal-bg")) close(); });
    imeInput(input, 0, function (v) { palUpdate(v.trim()); });
    $("#pal-crumb").addEventListener("click", function () { palPage(""); });
    palFoot();
    if (!pal.buttons) api("/api/buttons").then(function (l) { pal.buttons = l || []; if (root.innerHTML) palUpdate(input.value.trim()); }).catch(function () { pal.buttons = []; });
    palUpdate("");
    input.focus();
  }
  function palUpdate(q) {
    var m = function (c) { return palMatch(c.label + (c.kw ? " " + c.kw : ""), q); };
    if (pal.page === "look") {
      clearTimeout(pal.timer); pal.seq++;
      pal.items = palLookItems().filter(m);
      var b = pal.look0, at = -1;
      pal.items.forEach(function (it, i) { if (at < 0 && it.kind === "theme" && it.look.theme === b.theme) at = i; });
      pal.sel = q || at < 0 ? 0 : at;
      palDraw();
      palPreview();
      return;
    }
    // Typing "深色" / "midnight" on the first page lists the looks directly (applied on Enter, no preview).
    // Only names count here ("深色", "Midnight"), so "主题" still puts "Change theme…" first.
    var looks = q ? palLookItems(true).filter(function (c) { return palMatch(c.label.split(" › ").pop() + " " + c.kw, q); }) : [];
    var local = looks.concat(palCommands().filter(m));
    var btns = (pal.buttons || []).filter(function (b) { return palMatch(b.label, q); }).slice(0, 5).map(function (b) {
      return { kind: "button", icon: "play", label: T("pal.run_button", { name: b.label }), run: function () { runButton(b.id); } };
    });
    pal.items = local.concat(btns);
    pal.sel = 0;
    palDraw();
    clearTimeout(pal.timer);
    if (!q) return;
    var my = ++pal.seq;
    pal.timer = setTimeout(function () {
      api("/api/search?q=" + enc(q)).then(function (hits) {
        if (my !== pal.seq || !$("#pal-list")) return; // stale response: drop it
        var extra = [];
        // sub is the raw status from the DB ("active", "succeeded"); show it in the UI language.
        var subT = function (pre, v) { var k = pre + v; return v && T(k) !== k ? T(k) : v; };
        hits.forEach(function (h) {
          h.sub = h.kind === "task" ? subT("task.", h.sub) : h.kind === "run" ? subT("st.", h.sub) : h.sub;
          if (h.kind === "task") {
            extra.push({ kind: "task", icon: "tasks", label: h.name, sub: h.sub, run: function () { location.hash = "#/tasks/" + enc(h.id); } });
            extra.push({ kind: "run_task", icon: "play", label: T("pal.run_task", { name: h.name }), run: function () {
              api("/api/tasks/" + enc(h.id) + "/run", { method: "POST", body: {} }).then(function () { toast(T("toast.run_started")); }).catch(fail);
            } });
          } else if (h.kind === "skill") {
            extra.push({ kind: "skill", icon: "skills", label: h.name, sub: h.sub, run: function () { location.hash = "#/skills/" + enc(h.id); } });
            extra.push({ kind: "run_skill", icon: "play", label: T("pal.run_skill", { name: h.name }), run: function () { quickRunModal({ skill_id: h.id }); } });
          }
          else extra.push({ kind: "run", icon: "runs", label: T("search.run", { id: shortId(h.id) }), sub: h.sub, run: function () { location.hash = "#/runs/" + enc(h.id); } });
        });
        pal.items = extra.concat(pal.items);
        pal.sel = 0;
        palDraw();
      }).catch(function () {});
    }, 150);
  }
  function palDraw() {
    var ul = $("#pal-list");
    if (!ul) return;
    ul.innerHTML = pal.items.length ? pal.items.map(function (it, i) {
      return '<li role="option" id="pal-o' + i + '" data-i="' + i + '" data-ic="' + esc(it.icon || "") + '" aria-selected="' + (i === pal.sel) + '" class="' + (i === pal.sel ? "sel" : "") + '">' + (it.swatch ? '<i class="pv-dot" data-pv="' + esc(it.swatch) + '" aria-hidden="true"></i>' : ic(it.icon, "sm")) + '<span class="kind">' + esc(T("pal.kind." + it.kind)) + "</span><span class=\"lbl\">" + esc(it.label) + "</span>" + (it.sub ? '<span class="sub">' + esc(it.sub) + "</span>" : "") + (it.keys ? '<span class="pal-keys">' + it.keys.split(" ").map(function (k) { return "<kbd>" + esc(k) + "</kbd>"; }).join("") + "</span>" : "") + "</li>";
    }).join("") : '<li class="none muted">' + esc(T("search.none")) + "</li>";
    $$("li[data-i]", ul).forEach(function (li) {
      li.addEventListener("mousemove", function () { if (pal.sel !== +li.getAttribute("data-i")) palSelect(+li.getAttribute("data-i")); });
      li.addEventListener("click", function () { palRun(+li.getAttribute("data-i")); });
    });
    var q = $("#pal-q"); if (q) q.setAttribute("aria-activedescendant", pal.items.length ? "pal-o" + pal.sel : "");
  }
  function palSelect(i) {
    if (!pal.items.length) return;
    pal.sel = (i + pal.items.length) % pal.items.length;
    palDraw();
    palPreview();
    var li = $("#pal-o" + pal.sel); if (li && li.scrollIntoView) li.scrollIntoView({ block: "nearest" });
  }
  function palRun(i) {
    var it = pal.items[i];
    if (!it) return;
    if (it.stay) { it.run(); return; }
    if (it.look) { it.run(); if (pal.close) pal.close(); return; } // keep first, so closing does not revert
    if (pal.close) pal.close();
    it.run();
  }
  function shortcutsHelp() {
    var rows = [["Mod+K", T("sc.palette")], ["?", T("sc.help")], ["↑ ↓", T("sc.move")], ["Enter", T("sc.run")], ["j / k", T("sc.rows")], ["Enter / o", T("sc.open_row")], ["x / r", T("sc.row_acts")], ["/", T("sc.search")], ["Mod+Enter", T("sc.submit")], ["Esc", T("sc.close")], ["Alt+← / Alt+→", T("sc.reorder")], ["g h / g t / g r / g c", T("sc.goto")], ["n t / n s / n p / n b / n q", T("sc.create")], ["r / e / o", T("sc.task_keys")]];
    modal('<div class="card-head"><span class="ico">' + ic("more") + "</span><h2>" + esc(T("sc.title")) + '</h2></div><table class="tbl sc-table"><tbody>' +
      rows.map(function (r) { return "<tr><td>" + r[0].split(" / ").map(kbdHtml).join(" / ") + "</td><td>" + esc(r[1]) + "</td></tr>"; }).join("") +
      '</tbody></table><div class="actions"><button class="btn" id="sc-close">' + esc(T("btn.close")) + "</button></div>", function (r, close) { $("#sc-close").addEventListener("click", close); });
  }
  // Navigate, then press a button on the new page once it has rendered.
  function goClick(hash, sel) {
    if (location.hash !== hash) location.hash = hash;
    var n = 0, t = setInterval(function () { var b = $(sel); if (b || ++n > 40) { clearInterval(t); if (b) b.click(); } }, 50);
  }
  var gPending = 0;
  document.addEventListener("keydown", function (e) {
    if ($("#shell") && $("#shell").classList.contains("hidden")) return;
    var key = kbLetter(e);
    if (modKey(e) && key.toLowerCase() === "k") { e.preventDefault(); openPalette(); return; }
    var t = e.target, typing = t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable);
    if (typing || e.metaKey || e.ctrlKey || e.altKey || $("#modal-root").innerHTML || ($("#pal-root") && $("#pal-root").innerHTML)) return;
    if (e.key === "?") { e.preventDefault(); shortcutsHelp(); return; }
    if (gPending && Date.now() - gPending < 1000) {
      var lead = gPending.k;
      gPending = 0;
      if (lead === "g") {
        var dest = { h: "#/", t: "#/tasks", r: "#/runs", s: "#/skills", b: "#/buttons", p: "#/plugins", c: "#/spend", ",": "#/settings" }[key];
        if (dest) { e.preventDefault(); location.hash = dest; }
      } else {
        // "n x" creates (same scheme as Plane's Power K: n i / n p …).
        var mk = { t: function () { taskFormModal(null); }, s: function () { skillImportModal(function () { location.hash = "#/skills"; refresh(); }); }, p: function () { goClick("#/settings/providers", "#pv-add"); }, b: function () { goClick("#/buttons", "#b-add"); }, q: function () { quickRunModal(); } }[key];
        if (mk) { e.preventDefault(); mk(); }
      }
      return;
    }
    if (key === "g" || key === "n") { gPending = { k: key, valueOf: function () { return this.t; }, t: Date.now() }; return; }
    if (kbKey(e)) return;
    // page-level single keys
    // r / e: run & edit on a task page, rerun & edit-and-rerun on a run page.
    // o: open the task's folder (desktop app, overview tab).
    var act = { r: ["#t-run", "#rd-rerun"], e: ["#t-edit", "#rd-edit"], o: ["#ws-open"] }[key], hit = act && (($(act[0]) || $(act[1])));
    if (hit && hit.classList.contains("hidden")) hit = null; // e.g. "o" in the browser, where there is no file manager
    if (hit && !hit.disabled) { e.preventDefault(); hit.click(); }
  });

  // ---------- list cursor: j / k through the rows of a list page ----------
  // Idea: Miniflux internal/ui/static/js/app.js goToListItem() + .current-item (keyboard_handler
  // j / k / o / Enter). j / k move a highlighted row through the page's clickable rows
  // ([data-href]: tasks, runs, skills, home and task-detail lists), Enter or o opens it, x ticks
  // its checkbox (task list batch bar), r runs it (task list), Esc drops it, / jumps to the
  // page's search box. The row is remembered per page (by its link, not its node), so a live
  // repaint keeps it and coming back from a row shows where you were. Same keys on every OS.
  // Enter / o / x / r only act on a row picked with j / k on this visit (kbLive); a row
  // remembered from a click is only highlighted, so o on a task page still opens its folder.
  var kbCur = {}, kbPend = 0, kbLive = "";
  // "/" badge at the right of a list's search box; hidden while it has focus or text.
  var KB_SLASH = '<kbd class="kb-hint" aria-hidden="true">/</kbd>';
  function kbPage() { return (location.hash || "#/").split("?")[0]; }
  function kbStep(n, i, d) { if (!n) return -1; if (i < 0) return d > 0 ? 0 : n - 1; return Math.max(0, Math.min(n - 1, i + d)); }
  function kbRows() { var pg = page(); return pg ? $$("[data-href]", pg).filter(function (el) { return el.offsetParent !== null; }) : []; }
  function kbRow(rows) { var h = kbCur[kbPage()]; if (!h) return null; return (rows || kbRows()).filter(function (el) { return el.getAttribute("data-href") === h; })[0] || null; }
  function kbPaint(rows) {
    var pg = page(), cur = kbRow(rows);
    if (!pg) return cur;
    $$(".kb-cur", pg).forEach(function (el) { if (el !== cur) { el.classList.remove("kb-cur"); el.removeAttribute("aria-current"); if (el._kbTab) { el.removeAttribute("tabindex"); el._kbTab = false; } } });
    if (cur && !cur.classList.contains("kb-cur")) {
      cur.classList.add("kb-cur"); cur.setAttribute("aria-current", "true");
      if (!cur.hasAttribute("tabindex")) { cur.setAttribute("tabindex", "-1"); cur._kbTab = true; }
    }
    return cur;
  }
  function kbMove(d) {
    var rows = kbRows(), j = kbStep(rows.length, rows.indexOf(kbRow(rows)), d);
    if (j < 0) return false;
    kbCur[kbPage()] = rows[j].getAttribute("data-href"); kbLive = kbPage();
    prefetchRoute(kbCur[kbPage()]);
    var el = kbPaint(rows);
    // Focus moves to the row (screen readers follow, a chip clicked before keeps no Enter).
    if (el) { el.focus({ preventScroll: true }); el.scrollIntoView({ block: "nearest" }); }
    return true;
  }
  // A focused control keeps its own Enter (buttons, links, chips); j / k / o / x work anywhere.
  function kbFocusFree(cur) { var a = document.activeElement; return !a || a === cur || a === document.body || a === page() || !(a.closest && a.closest("a,button,input,select,textarea,summary,[tabindex],[contenteditable]")); }
  function kbKey(e) {
    var k = kbLetter(e);
    if (k === "j" || k === "k") { if (kbMove(k === "j" ? 1 : -1)) { e.preventDefault(); return true; } return false; }
    if (k === "/") {
      var s = $$("input[type=search]", page()).filter(function (x) { return x.offsetParent !== null && !x.disabled; })[0];
      if (s) { e.preventDefault(); s.focus(); s.select(); return true; }
      return false;
    }
    var cur = kbCur[kbPage()] && kbRow();
    if (!cur) return false;
    if (k === "Escape") { delete kbCur[kbPage()]; kbLive = ""; kbPaint(); return true; }
    if (kbLive !== kbPage()) return false;
    if (k === "o" || (k === "Enter" && kbFocusFree(cur))) { e.preventDefault(); location.hash = cur.getAttribute("data-href"); return true; }
    if (k === "x") { var c = $("input[type=checkbox][data-pick]", cur); if (c && !c.disabled) { e.preventDefault(); c.click(); return true; } return false; }
    if (k === "r") { var b = $("[data-runnow]", cur); if (b && !b.disabled) { e.preventDefault(); b.click(); return true; } return false; }
    return false;
  }
  window.addEventListener("hashchange", function () { kbLive = ""; });
  // Clicking a row (or its link) remembers it too, so Back lands on it.
  document.addEventListener("click", function (e) {
    var el = e.target && e.target.closest && e.target.closest("#page [data-href]");
    if (el) kbCur[kbPage()] = el.getAttribute("data-href");
  }, true);
  // Lists repaint on their own (search, sort, live polls): put the highlight back once per frame.
  (function () {
    var pg = page();
    if (!pg || typeof MutationObserver === "undefined") return;
    new MutationObserver(function () {
      if (kbPend || !kbCur[kbPage()]) return;
      kbPend = requestAnimationFrame(function () { kbPend = 0; kbPaint(); });
    }).observe(pg, { childList: true, subtree: true });
  })();

  // ---------- sidebar: Claude Code status ----------
  // GET /api/claude/status is cached server-side (30s) and never calls a model API.
  // We refresh it on navigation (at most every 15s) and every 30s; "Recheck" forces it.
  var cc = { st: null, at: 0, err: false, prevState: "", timer: null };
  function renderStatus() { loadCC(false); }
  function fmtMem(b) { return b ? (b >= 1073741824 ? (b / 1073741824).toFixed(1) + " GB" : Math.round(b / 1048576) + " MB") : "—"; }
  function loadCC(force) {
    if (!force && cc.st && Date.now() - cc.at < 15000) { paintCC(); return Promise.resolve(cc.st); }
    var p = force ? api("/api/claude/status", { method: "POST", body: {} }) : api("/api/claude/status"), asked = Date.now();
    return p.then(function (st) { cc.st = st; cc.err = false; cc.at = Date.now(); spAdopt(st.sched, asked); paintCC(); paintHeldNext(); return st; })
      .catch(function (e) { if (e.auth) return; cc.err = true; cc.at = Date.now(); paintCC(); });
  }
  function ccView() {
    var st = cc.st;
    if (cc.err || !st) return { state: cc.err ? "down" : "ready", head: T(cc.err ? "cc.h_down" : "cc.h_checking"), sub: cc.err ? T("cc.s_down") : "" };
    var ver = st.version ? "Claude Code " + st.version : "Claude Code";
    var prov = st.provider && st.provider.name ? st.provider.name + (st.provider.model ? " · " + st.provider.model : "") : T("cc.no_provider");
    switch (st.state) {
      case "missing": return { state: "missing", head: T("cc.h_missing"), sub: T("cc.s_missing") };
      case "running": return { state: "running", head: T("cc.h_running", { n: st.sessions.length }), sub: prov };
      case "warning": return { state: "warning", head: st.reasons.indexOf("provider_failing") >= 0 ? T("cc.h_provider") : T("cc.h_old", { v: st.version }), sub: prov };
      default: return { state: "ready", head: T("cc.h_ready", { v: ver }), sub: prov };
    }
  }
  function paintCC() {
    var box = $("#sys-status");
    if (!box) return;
    var v = ccView();
    document.documentElement.classList.toggle("tb-busy", v.state === "running");
    setDocTitle({ running: v.state === "running" && cc.st ? cc.st.sessions.length : 0, alert: v.state === "warning" || v.state === "missing" || v.state === "down" });
    box.innerHTML = spPillHtml() + '<button class="cc-card cc-' + v.state + '" id="cc-btn" aria-haspopup="dialog" aria-expanded="false" title="' + esc(v.head + (v.sub ? " · " + v.sub : "")) + '">' +
      '<span class="dot" aria-hidden="true"></span><span class="cc-txt"><strong>' + esc(v.head) + "</strong>" + (v.sub ? "<small>" + esc(v.sub) + "</small>" : "") + "</span></button>" +
      '<span class="sr-only" aria-live="polite" id="cc-live"></span>';
    if (cc.prevState && cc.prevState !== v.state) $("#cc-live").textContent = v.head;
    cc.prevState = v.state;
    $("#cc-btn").addEventListener("click", function (e) { e.stopPropagation(); toggleCCPop(); });
    var pill = $("#spz-pill"); if (pill) pill.addEventListener("click", function (e) { e.stopPropagation(); spMenu(pill, "up"); });
    if ($("#cc-pop")) fillCCPop();
  }

