  // ---------- status helpers ----------
  // A queued run shows its place in line ("排队中 · 第 2 位"); what it waits for is in the tooltip.
  // A run that ended without starting (skipped at its slot, stopped while queued)
  // says why in the pill's tooltip, so the reason is visible without scrolling.
  function statusPill(st, pos, err) {
    if (st === "queued" && pos > 0) return '<span class="pill info" data-tip="' + esc(T("st.queued_tip")) + '">' + esc(T("st.queued_n", { n: pos })) + "</span>";
    var tip = (st === "skipped" || st === "canceled") && err ? ' data-tip="' + esc(runErr(err)) + '"' : "";
    var map = { succeeded: ["ok", T("st.succeeded")], running: ["run", T("st.running")], queued: ["info", T("st.queued")], failed: ["bad", T("st.failed")], timeout: ["bad", T("st.timeout")], canceled: ["warn", T("st.canceled")], skipped: ["", T("st.skipped")] };
    var m = map[st] || ["", st || "—"];
    return '<span class="pill ' + m[0] + '"' + tip + ">" + esc(m[1]) + "</span>";
  }
  // Stable error codes from the core, shown in the viewer's language; anything else as-is.
  function runErr(msg) {
    msg = msg || "";
    if (/^interrupted: quit/.test(msg)) return T("rd.err_quit");
    if (/^stopped: by user/.test(msg)) return T("rd.err_stopped");
    // A queued run turned away when it got its slot: why is in the viewer's language,
    // the exact cap figures follow as-is.
    if (/^skipped: task paused/.test(msg)) return T("rd.err_skip_paused");
    var pw = /^skipped: power saving: (battery|low|saver) (-?\d+)/.exec(msg);
    if (pw) return T("rd.err_skip_power_" + pw[1], { p: Number(pw[2]) >= 0 ? pw[2] + "%" : "—" });
    var dk = /^skipped: disk full: (\d+)\/(\d+)/.exec(msg);
    if (dk) return T("rd.err_skip_disk", { f: fmtBytes(Number(dk[1])), m: mbLabel(Math.round(Number(dk[2]) / 1048576)) });
    var b = /^skipped: budget blocked: (.*)$/.exec(msg);
    if (b) return T(/ cap held by \d+ running /.test(b[1]) ? "rd.err_skip_held" : "rd.err_skip_budget", { why: budgetWhy(b[1]) });
    return /^interrupted:/.test(msg) ? T("rd.err_interrupted") : msg;
  }
  // A core budget reason ("global/ daily USD cap reached (5.1/5.0)") in the viewer's language.
  function budgetWhy(d) {
    var sc = /^(global|task|bucket)\//.exec(d), name = sc ? T("sp.scope_" + sc[1]) : "";
    var m = /daily (USD|CNY) cap reached \(([\d.]+)\/([\d.]+)\)/.exec(d);
    if (m) { var sym = m[1] === "CNY" ? "¥" : "$"; return T("be.usd_cap", { name: name, used: sym + m[2], cap: sym + m[3] }); }
    m = /daily (USD|CNY) cap held by (\d+) running \(([\d.]+)\+([\d.]+)\/([\d.]+)\)/.exec(d);
    if (m) { var hs = m[1] === "CNY" ? "¥" : "$"; return T("be.held", { name: name, n: m[2], used: hs + m[3], held: hs + m[4], cap: hs + m[5] }); }
    m = /daily token cap reached \((\d+)\/(\d+)\)/.exec(d);
    if (m) return T("be.token_cap", { name: name, used: m[1], cap: m[2] });
    if (/remaining daily budget is zero/.test(d)) return T("be.zero", { name: name || T("sp.scope_global") });
    return d;
  }
  // Why a paused task is paused: the pill says it in a word, the tooltip in a sentence.
  var PAUSE_KINDS = ["user", "snooze", "breaker", "budget", "pack", "plugin"];
  function pauseKind(t) { return t && t.status === "paused" && PAUSE_KINDS.indexOf(t.pause_kind) >= 0 ? t.pause_kind : ""; }
  function pauseClass(c) { return state.dict["pz.cls." + c] ? T("pz.cls." + c) : T("pz.cls.unknown"); }
  function pauseWhy(t) {
    var k = pauseKind(t), at = t.paused_at ? when(t.paused_at) : "";
    if (!k) return "";
    if (k === "breaker") return T("pz.why_breaker", { n: t.breaker_at || 3, t: at, c: pauseClass(t.pause_detail) });
    if (k === "budget") return T("pz.why_budget", { t: at, r: budgetWhy(t.pause_detail || "") });
    if (k === "plugin") return T("pz.why_plugin", { t: at, p: t.pause_detail || "" });
    if (k === "pack") return T("pz.why_pack");
    if (k === "snooze") return T("pz.why_snooze", { u: when(t.pause_detail), t: at });
    return at ? T("pz.why_user", { t: at }) : "";
  }
  function taskPill(st, t, small) {
    var m = { active: ["ok", T("task.active")], paused: ["warn", T("task.paused")], archived: ["", T("task.archived")] }[st] || ["", st];
    var k = pauseKind(t), why = k ? pauseWhy(t) : "";
    if (k && k !== "user") m = [k === "pack" ? "info" : "warn", T("pz.pill_" + k)];
    return '<span class="pill ' + m[0] + (small ? " sm" : "") + (why ? ' pz-pill"' + (small ? "" : ' tabindex="0"') + ' data-tip="' + esc(why) + '" aria-label="' + esc(m[1] + " · " + why) : "") + '">' + esc(m[1]) + "</span>";
  }
  // Task page: an automatic pause (failures, budget, plugin) gets one line saying what
  // happened and what resuming does; a pause by hand needs no explanation.
  function pauseNotice(t) {
    var k = pauseKind(t);
    if (!k || k === "user" || k === "snooze") return "";
    var body = k === "breaker" ? T("pz.note_breaker", { n: t.breaker_at || 3, c: pauseClass(t.pause_detail) }) : k === "budget" ? T("pz.note_budget") : k === "plugin" ? T("pz.note_plugin", { p: t.pause_detail || "" }) : T("pz.note_pack");
    var link = k === "breaker" ? '<a class="link" href="#/runs?task=' + enc(t.id) + '&status=failed">' + esc(T("pz.see_failed")) + "</a>" : k === "budget" ? '<a class="link" href="#/spend">' + esc(T("pz.see_spend")) + "</a>" : "";
    return '<p class="notice ' + (k === "pack" ? "" : "warn ") + 'pz-note" id="pz-note"' + (t.paused_at ? ' data-tip="' + esc(pauseWhy(t)) + '"' : "") + ">" + ic(k === "pack" ? "info" : "pause", "sm") + "<span>" + esc(body) + "</span>" + link + "</p>";
  }
  function triggerText(tr) {
    return { cron: T("trig.cron"), manual: T("trig.manual"), quick: T("trig.quick"), button: T("trig.button"), session: T("trig.session") }[tr] || T("trig.other", { t: tr });
  }
  function runIcon(r) {
    var n = (r.task_name || r.prompt || "").toLowerCase();
    if (/report|doc/.test(n)) return "doc";
    if (/sync|issue/.test(n)) return "sync";
    if (/digest|week|summar/.test(n)) return "pie";
    if (/clean|log|trash/.test(n)) return "trash";
    if (/backup/.test(n)) return "shield";
    if (r.trigger === "quick") return r.skill_id ? "skills" : "sparkle";
    return "terminal";
  }
  // a run of a skill (no task) is titled by the skill, so the list and the breadcrumb say what ran
  function runTitle(r) { return (r.task_name || r.skill_name || T(r.trigger === "session" ? "trig.session" : "trig.quick")) + " #" + shortId(r.id); }
  // runCtxText: "Quick run · tidebot · Read-only" for runs started in a folder from the Run dialog
  // (short: just "tidebot · Read-only", for the narrow status card)
  function runCtxText(r, short) {
    var t = triggerText(r.trigger);
    if (!r.workspace || !r.run_mode) return t;
    var nm = r.workspace.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || r.workspace;
    return (short ? "" : t + " · ") + nm + " · " + T(r.run_mode === "edit" ? "rn.edit" : "rn.read");
  }
  function cronHuman(c) {
    if (!c) return T("cron.manual");
    var p = c.split(/\s+/);
    var mm;
    if (p.length === 5 && (mm = /^\*\/(\d+)$/.exec(p[0])) && p.slice(1).join(" ") === "* * * *") return T("cron.every_min", { n: mm[1] });
    if (p.length === 5 && /^\d+$/.test(p[0]) && p.slice(1).join(" ") === "* * * *") return T("cron.hourly", { m: p[0] });
    if (p.length === 5 && /^\d+$/.test(p[0]) && /^\d+$/.test(p[1]) && /^\d+$/.test(p[2]) && p[3] === "*" && p[4] === "*") return T("cron.monthly", { d: p[2], t: ("0" + p[1]).slice(-2) + ":" + ("0" + p[0]).slice(-2) });
    if (p.length === 5 && /^\d+$/.test(p[0]) && /^\d+$/.test(p[1])) {
      var tm = new Intl.DateTimeFormat(state.lang, { hour: "numeric", minute: "2-digit", timeZone: "UTC" }).format(new Date(Date.UTC(2000, 0, 1, +p[1], +p[0])));
      if (p[2] === "*" && p[3] === "*" && p[4] === "*") return T("cron.daily", { t: tm });
      if (p[2] === "*" && p[3] === "*" && p[4] === "1-5") return T("cron.weekdays", { t: tm });
      if (p[2] === "*" && p[3] === "*" && /^\d$/.test(p[4])) return T("cron.weekly", { d: new Intl.DateTimeFormat(state.lang, { weekday: "long", timeZone: "UTC" }).format(new Date(Date.UTC(2023, 0, 1 + (+p[4])))), t: tm });
    }
    return c;
  }
  function emptyState(icon, title, sub) { return '<div class="empty">' + ic(icon) + "<strong>" + esc(title) + "</strong>" + esc(sub || "") + "</div>"; }
  // ---------- shell ----------
  var NAV = [["home", "#/"], ["tasks", "#/tasks"], ["skills", "#/skills"], ["workflows", "#/workflows"], ["buttons", "#/buttons"], ["plugins", "#/plugins"], ["runs", "#/runs"], ["spend", "#/spend"], ["settings", "#/settings"]];
  var shellNavChoice = null, shellNavOff = null;
  function syncShellNav() {
    var shell = $("#shell"), toggle = $("#nav-toggle");
    if (!shell) return;
    var compact = shellNavChoice == null ? window.innerWidth <= 960 : shellNavChoice;
    shell.setAttribute("data-nav-size", compact ? "compact" : "expanded");
    if (toggle) { toggle.setAttribute("aria-expanded", String(!compact)); toggle.setAttribute("aria-label", T(compact ? "layout.expand_nav" : "layout.collapse_nav")); }
  }
  function wireShellNav() {
    if (shellNavOff) shellNavOff();
    var side = $("#shell .sidebar"), media = window.matchMedia("(max-width: 960px)");
    side.id = "app-sidebar";
    var resize = function () { shellNavChoice = null; syncShellNav(); };
    var choose = function (e) { if (window.innerWidth <= 640 && e.target.closest("a")) { shellNavChoice = true; syncShellNav(); } };
    var key = function (e) { if (e.key === "Escape") { shellNavChoice = true; syncShellNav(); var b = $("#nav-toggle"); if (b) b.focus(); } };
    media.addEventListener("change", resize); side.addEventListener("click", choose); side.addEventListener("keydown", key);
    shellNavOff = function () { media.removeEventListener("change", resize); side.removeEventListener("click", choose); side.removeEventListener("keydown", key); };
    syncShellNav();
  }

  function renderShell() {
    $("#brand-mark").innerHTML = tideMark();
    $("#nav").innerHTML = NAV.map(function (n) { return '<a href="' + n[1] + '" data-nav="' + n[0] + '" aria-label="' + esc(T("nav." + n[0])) + '">' + ic(n[0]) + "<span>" + esc(T("nav." + n[0])) + "</span></a>"; }).join("");
    wireShellNav();
    paintChannelDot();
    var tag = document.querySelector("#shell .brand-text small"); if (tag) tag.textContent = T("brand.tagline");
  }
  function setNav(name) { $$("#nav a").forEach(function (a) { a.classList.toggle("active", a.getAttribute("data-nav") === name); }); }

  function renderTopbar(crumbs) {
    // A single crumb repeats the page title right below it: only detail pages get a trail.
    // The page title lives here (the narrow top bar), with the trail for detail pages.
    if ((!crumbs || !crumbs.length) && renderTopbar.nav) crumbs = [[T("nav." + renderTopbar.nav)]];
    var c = crumbs && crumbs.length ? '<div class="crumbs">' + crumbs.map(function (x, i) {
      return (i ? '<span class="sep">' + ic("chev", "sm") + "</span>" : "") + (x[1] ? '<a href="' + esc(x[1]) + '">' + esc(x[0]) + "</a>" : "<span>" + esc(x[0]) + "</span>");
    }).join("") + "</div>" : "";
    $("#topbar").innerHTML = '<button type="button" class="icon-btn" id="nav-toggle" aria-controls="app-sidebar" aria-expanded="false" aria-label="' + esc(T("layout.expand_nav")) + '">' + ic("sidebar") + '</button>' + c + '<div class="spacer"></div>' +
      '<button class="search pal-open" id="q-open" aria-label="' + esc(T("top.search_label")) + '">' + ic("search") + '<span class="ph">' + esc(T("pal.placeholder")) + "</span>" + kbdHtml("Mod+K") + "</button>" +
      '<button class="lang-btn" id="btn-lang" title="' + esc(T("top.language")) + '" aria-label="' + esc(T("top.language")) + '">' + (state.lang === "zh-CN" ? "中" : "EN") + "</button>" +
      '<button class="icon-btn" id="btn-refresh" title="' + esc(T("top.refresh")) + '" aria-label="' + esc(T("top.refresh")) + '">' + ic("refresh") + "</button>" +
      '<div class="rel"><button class="icon-btn" id="btn-bell" title="' + esc(T("top.notifications")) + '" aria-label="' + esc(T("top.notifications")) + '" aria-haspopup="true">' + ic("bell") + '<span class="badge-dot hidden" id="bell-dot"></span></button><div id="notif" class="popover notif-center hidden"></div></div>';
    wireTopbar();
  }

  // Structured notifications (key + params) render in the viewer's language;
  // legacy rows only have server-rendered text and fall back to it.
  function notifText(n) {
    if (!n.key) return { t: notifTitle(n), b: /^budget_/.test(n.event || "") ? budgetWhy(n.body || "") : n.body || "" };
    var p = {}, src = n.params || {};
    Object.keys(src).forEach(function (k) { p[k] = src[k]; });
    if (!p.name) p.name = T("ntf.quick_task");
    // An update notice carries the start of the release notes (Markdown): the bell shows a
    // one-line plain summary, the whole text on hover, the full notes on the update card.
    if (/^update_/.test(n.key)) { var full = notesPlain(p.detail || ""); p.detail = notesSummary(p.detail || ""); }
    var bk = "ntf." + n.key + ".b", b = T(bk, p);
    return { t: T("ntf." + n.key + ".t", p), b: b === bk ? "" : b.trim(), full: full || "" };
  }
  // Release notes → plain lines: headings, list marks, emphasis, code ticks and link syntax
  // go, link text stays; blank lines dropped. CRLF and LF alike.
  function notesPlain(md) {
    return String(md).split(/\r?\n/).map(function (l) {
      return l.replace(/^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+|\d+[.)]\s+)/, "").replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1").replace(/<(https?:[^>]+)>/g, "$1")
        .replace(/(\*\*|__)(.+?)\1/g, "$2").replace(/`([^`]*)`/g, "$1").replace(/\s+/g, " ").trim();
    }).filter(Boolean).join("\n");
  }
  // Summary: the list items' lead (bold lead-in if any, else up to the first comma or stop),
  // at most three, " · " between; notes without a list fall back to their first plain line.
  function notesSummary(md) {
    var items = [];
    String(md).split(/\r?\n/).forEach(function (l) {
      var m = /^\s{0,3}(?:[-*+]|\d+[.)])\s+(.*)$/.exec(l);
      if (!m) return;
      var lead = /^(?:\*\*|__)(.+?)(?:\*\*|__)/.exec(m[1]), t = notesPlain(lead ? lead[1] : m[1]);
      if (!lead) t = t.split(/[。；;：:，]|[.,]\s/)[0];
      t = t.replace(/[：:，,。.\s]+$/, "");
      if (t) items.push(t.length > 40 ? t.slice(0, 39) + "…" : t);
    });
    if (!items.length) return (notesPlain(md).split("\n")[0] || "").slice(0, 80);
    return items.slice(0, 3).join(" · ") + (items.length > 3 ? " " + T("nc.more_items", { n: items.length - 3 }) : "");
  }
  function notifTitle(n) { var k = "notif." + n.event, v = T(k); return v === k ? cap(n.event.replace(/_/g, " ")) : v; }
  function markAllRead(list) {
    if (list && list[0] && list[0].created_at > state.notifSeen) { seenLocal(list[0].created_at); seenPush(); }
    var dot = $("#bell-dot"); if (dot) dot.classList.add("hidden");
    fav.failed = 0; paintFavicon();
  }
  // The "read up to" mark lives in tidebot (Novu keeps isRead on the server), so read in
  // the desktop window is read in every browser. localStorage only paints the first frame.
  function seenLocal(v) {
    state.notifSeen = v;
    try { localStorage.setItem("tb_notif_seen", v); } catch (e) {}
  }
  // Send this browser's mark when it's ahead of tidebot's (marked here, or read before the
  // mark was shared); once per value, a failed send retries on the next bell refresh.
  function seenPush() {
    var v = state.notifSeen;
    if (!v || state.seenSent === v) return;
    state.seenSent = v;
    api("/api/notifications/read", { method: "POST", body: { seen_at: v }, read: true }).then(function (r) {
      if (r && r.seen_at && r.seen_at > state.notifSeen) seenLocal(r.seen_at);
    }).catch(function () { if (state.seenSent === v) state.seenSent = ""; });
  }
  // Another window marked read: catch up when this one comes back (at most every 10s),
  // at once for tabs of this browser.
  function seenSync() {
    if (!state.csrf || ($("#shell") && $("#shell").classList.contains("hidden"))) return;
    if (Date.now() - (state.seenSyncAt || 0) < 10000) return;
    state.seenSyncAt = Date.now();
    api("/api/notifications/center", { fresh: true }).then(noteNotifs).catch(function () {});
  }
  window.addEventListener("focus", seenSync);
  document.addEventListener("visibilitychange", function () { if (!document.hidden) seenSync(); });
  window.addEventListener("storage", function (e) {
    if (e.key !== "tb_notif_seen" || !e.newValue || !(e.newValue > state.notifSeen)) return;
    state.notifSeen = e.newValue;
    if (state.lastNotifs) noteNotifs(state.lastNotifs);
  });
  function renderNotifCenter(pop, data) {
    var items = data.items || [], pend = data.pending || [];
    var groups = { failed: [], confirm: [], other: [] };
    items.forEach(function (n) { (groups[n.group] || groups.other).push(n); });
    var unread = function (n) { return n.created_at > state.notifSeen; };
    var nread = items.filter(function (n) { return !unread(n); }).length;
    var it = function (n) {
      var nt = notifText(n), pr = n.params || {};
      // A success notice may carry the start of the answer: three lines here, all of it on hover.
      var ex = pr.excerpt ? '<p class="nc-ex" title="' + esc(pr.excerpt) + '">' + esc(pr.excerpt) + "</p>" : "";
      var inner = "<strong>" + esc(nt.t) + "</strong><div" + (nt.full ? ' class="nc-sum" title="' + esc(nt.full) + '"' : "") + ">" + esc(nt.b) + "</div>" + ex + '<small class="muted">' + esc(when(n.created_at)) + "</small>";
      // low disk space opens the storage card; a channel that stopped (or started) delivering
      // opens the channel list; a run notice opens its run; an update opens the update card.
      // Same stacked layout as the plain items, the whole row clickable.
      var to = n.key === "disk_low" ? "#/settings/data/storage" : n.key === "channel_down" || n.key === "channel_up" ? "#/settings/notifications" :
        /^update_/.test(n.key || "") ? "#/settings/about/update" :
        /^run_/.test(n.key || "") && /^[\w-]{1,64}$/.test(pr.run || "") ? "#/runs/" + enc(pr.run) : "";
      var row = to ? '<a class="item link-item note' + (unread(n) ? " unread" : "") + '" href="' + to + '">' + inner + "</a>" : '<div class="item' + (unread(n) ? " unread" : "") + '">' + inner + "</div>";
      // × beside the row (not inside the link): shows on hover / keyboard focus, always on touch
      return '<div class="nc-row">' + row + '<button type="button" class="nc-x" data-clear="' + esc(n.id) + '" title="' + esc(T("nc.clear_one")) + '" aria-label="' + esc(T("nc.clear_one") + " · " + nt.t) + '">' + ic("x", "sm") + "</button></div>";
    };
    var sec = function (key, inner, count) {
      return '<div class="ngroup"><h5>' + esc(T("nc." + key)) + (count ? ' <span class="pill sm">' + esc(count) + "</span>" : "") + "</h5>" + (inner || '<div class="item muted">' + esc(T("nc.empty")) + "</div>") + "</div>";
    };
    var confirmInner = pend.map(function (p) {
      return '<a class="item link-item" href="#/tasks/' + enc(p.task_id) + '/thread">' + ic("sparkle", "sm") + "<span>" + esc(T("nc.pending_amend", { name: p.task_name || shortId(p.task_id), n: p.count })) + "</span></a>";
    }).join("") + groups.confirm.slice(0, 5).map(it).join("");
    var nconf = pend.reduce(function (a, p) { return a + p.count; }, 0) + groups.confirm.length;
    pop.innerHTML = '<div class="nc-head"><h4>' + esc(T("top.notifications")) + '</h4><span class="nc-acts"><button class="btn sm ghost" id="nc-read" title="' + esc(T("nc.mark_all_tip")) + '"' + (items.length > nread ? "" : " disabled") + ">" + esc(T("nc.mark_all")) + "</button>" +
      '<button class="btn sm ghost" id="nc-clear"' + (nread ? ' title="' + esc(T("nc.clear_read_tip", { n: nread })) + '"' : " disabled") + ">" + esc(T("nc.clear_read")) + "</button></span></div>" +
      sec("failed", groups.failed.slice(0, 5).map(it).join(""), groups.failed.length) +
      sec("confirm", confirmInner, nconf) +
      sec("other", groups.other.slice(0, 5).map(it).join(""), 0);
    $("#nc-read", pop).addEventListener("click", function (e) { e.stopPropagation(); markAllRead(items); renderNotifCenter(pop, data); toast(T("nc.marked")); });
    $("#nc-clear", pop).addEventListener("click", function (e) { e.stopPropagation(); clearNotifs(pop, { read_before: state.notifSeen }); });
    $$(".nc-x", pop).forEach(function (b) {
      b.addEventListener("click", function (e) {
        e.stopPropagation();
        // keep the keyboard where it was: focus moves to the next row's ×, else the header
        var all = $$(".nc-x", pop), i = all.indexOf(b);
        clearNotifs(pop, { ids: [b.getAttribute("data-clear")] }, i);
      });
    });
    $$("a.link-item", pop).forEach(function (a) { a.addEventListener("click", function () { pop.classList.add("hidden"); }); });
  }
  // Clear from the bell (one row, or everything this browser has seen): the rows leave the
  // center at once, the popover stays open and redraws in place, and the toast can undo it.
  function clearNotifs(pop, body, focusAt) {
    var redraw = function () {
      return api("/api/notifications/center", { fresh: true }).then(function (d) {
        noteNotifs(d);
        if (pop.classList.contains("hidden")) return;
        renderNotifCenter(pop, d);
        var xs = $$(".nc-x", pop), f = focusAt == null ? null : xs[Math.min(focusAt, xs.length - 1)] || $("#nc-read", pop);
        if (f) f.focus();
      });
    };
    api("/api/notifications/clear", { method: "POST", body: body }).then(function (r) {
      var n = r.cleared || 0;
      return redraw().then(function () {
        if (!n) return;
        toastUndo(T(body.ids ? "nc.cleared_one" : "nc.cleared", { n: n }), function () {
          api("/api/notifications/clear", { method: "POST", body: { undo: r.batch } }).then(redraw).then(function () { toast(T("nc.restored")); }).catch(fail);
        });
      });
    }).catch(fail);
  }
  function toggleNotif() {
    var pop = $("#notif");
    if (!pop) return;
    if (!pop.classList.contains("hidden")) { pop.classList.add("hidden"); return; }
    api("/api/notifications/center").then(function (data) {
      renderNotifCenter(pop, data);
      pop.classList.remove("hidden");
    }).catch(fail);
  }

  function wireTopbar() {
    $("#nav-toggle").addEventListener("click", function () { shellNavChoice = $("#shell").getAttribute("data-nav-size") !== "compact"; syncShellNav(); });
    syncShellNav();
    $("#q-open").addEventListener("click", function () { openPalette(); });
    $("#btn-refresh").addEventListener("click", function () { refresh(); });
    $("#btn-lang").addEventListener("click", function () {
      var next = state.lang === "zh-CN" ? "en" : "zh-CN";
      setLanguage(next);
    });
    $("#btn-bell").addEventListener("click", function (e) { e.stopPropagation(); toggleNotif(); });
    api("/api/notifications/center").then(noteNotifs).catch(function () {});
  }
  document.addEventListener("click", function (e) { var p = $("#notif"); if (p && !p.contains(e.target)) p.classList.add("hidden"); });

