/* tidebot WebUI — vanilla JS, no framework. All dynamic text goes through esc(). */
(function () {
  "use strict";
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };
  var state = { lang: "en", langPref: "auto", dict: {}, csrf: "", tz: "UTC", version: "", es: null, idleTimer: null, notifSeen: localStorage.getItem("tb_notif_seen") || "" };
  var IDLE_MS = 30 * 60 * 1000;

  function esc(v) {
    return String(v == null ? "" : v).replace(/[&<>"'`]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;", "`": "&#96;" }[c];
    });
  }
  function enc(v) { return encodeURIComponent(String(v == null ? "" : v)); }

  // ---------- i18n ----------
  // Key-based dictionaries (embedded JSON). T() returns plain text; callers still esc() it.
  var LANGS = ["en", "zh-CN"];
  function pickLang(pref) {
    if (pref && pref !== "auto" && LANGS.indexOf(pref) >= 0) return pref;
    // Desktop shell: the OS locale comes from the core (webviews often report en-US).
    if (state.osLang) return /^zh/i.test(state.osLang) ? "zh-CN" : "en";
    var nav = (navigator.languages && navigator.languages[0]) || navigator.language || "en";
    return /^zh/i.test(nav) ? "zh-CN" : "en";
  }
  function T(key, vars) {
    var s = state.dict[key];
    if (s == null) s = key;
    if (vars) s = s.replace(/\{(\w+)\}/g, function (m, n) { return vars[n] == null ? m : String(vars[n]); });
    return s;
  }
  function loadLang(pref) {
    state.langPref = pref || "auto";
    var lang = pickLang(state.langPref);
    return fetch("/static/i18n/" + lang + ".json", { credentials: "same-origin" }).then(function (r) { return r.json(); }).then(function (d) {
      state.dict = d; state.lang = lang;
      document.documentElement.lang = lang;
      try { localStorage.setItem("tb_lang", state.langPref); } catch (e) {}
    }).catch(function () {
      if (lang !== "en") return fetch("/static/i18n/en.json").then(function (r) { return r.json(); }).then(function (d) { state.dict = d; state.lang = "en"; }).catch(function () {});
    });
  }
  function plural(n, one, many) { return T(n === 1 ? one : many, { n: n }); }

  var P = {
    home: '<path d="M3 10.5 12 3l9 7.5"/><path d="M5 9.5V20h5v-6h4v6h5V9.5"/>',
    tasks: '<rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 8h8M8 12h8M8 16h5"/>',
    skills: '<path d="M12 3v4M12 17v4M3 12h4M17 12h4M5.6 5.6l2.8 2.8M15.6 15.6l2.8 2.8M5.6 18.4l2.8-2.8M15.6 8.4l2.8-2.8"/>',
    buttons: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M8 14h3"/>',
    plugins: '<path d="M12 2 3 7v10l9 5 9-5V7z"/><path d="M3 7l9 5 9-5M12 12v10"/>',
    runs: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    spend: '<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>',
    wallet: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 10h18M16 15h2"/>',
    settings: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M4.2 4.2l2.1 2.1M17.7 17.7l2.1 2.1M2 12h3M19 12h3M4.2 19.8l2.1-2.1M17.7 6.3l2.1-2.1"/>',
    search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
    refresh: '<path d="M20 11a8 8 0 0 0-14.3-4.9L4 8"/><path d="M4 3v5h5"/><path d="M4 13a8 8 0 0 0 14.3 4.9L20 16"/><path d="M20 21v-5h-5"/>',
    bell: '<path d="M6 8a6 6 0 1 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.9 1.9 0 0 0 3.4 0"/>',
    calendar: '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M3 10h18M8 3v4M16 3v4"/>',
    receipt: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9h6M7 13h10"/>',
    pulse: '<path d="M3 12h4l3-8 4 16 3-8h4"/>',
    arrow: '<path d="M5 12h14M13 6l6 6-6 6"/>',
    chev: '<path d="m9 6 6 6-6 6"/>',
    info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
    doc: '<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6M8 13h8M8 17h5"/>',
    sync: '<path d="M20 11a8 8 0 0 0-14.3-4.9L4 8"/><path d="M4 3v5h5"/><path d="M4 13a8 8 0 0 0 14.3 4.9L20 16"/><path d="M20 21v-5h-5"/>',
    pie: '<path d="M21 12A9 9 0 1 1 12 3v9z"/><path d="M15 3.5A9 9 0 0 1 20.5 9H15z"/>',
    trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v6M14 11v6"/>',
    sparkle: '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z"/><path d="M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z"/>',
    play: '<path d="M7 4v16l13-8z"/>',
    pause: '<path d="M8 5v14M16 5v14"/>',
    check: '<path d="M20 6 9 17l-5-5"/>',
    checkc: '<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
    circle: '<circle cx="12" cy="12" r="9"/>',
    x: '<path d="M18 6 6 18M6 6l12 12"/>',
    send: '<path d="M22 2 11 13"/><path d="M22 2 15 22l-4-9-9-4z"/>',
    more: '<circle cx="12" cy="5" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="19" r="1"/>',
    branch: '<circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="8" r="2"/><path d="M6 7v10M18 10c0 4-6 3-12 7"/>',
    upload: '<path d="M12 16V4M7 9l5-5 5 5"/><path d="M4 16v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    shield: '<path d="M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6z"/>',
    globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>',
    key: '<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9M17 6l3 3"/>',
    logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>',
    clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
    user: '<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>',
    terminal: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3M13 15h4"/>',
    bolt: '<path d="M13 2 4 14h7l-1 8 9-12h-7z"/>',
    rollback: '<path d="M3 7v6h6"/><path d="M3 13a9 9 0 1 0 3-7.7L3 8"/>'
  };
  function ic(name, cls) { return '<svg class="i ' + (cls || "") + '" viewBox="0 0 24 24" aria-hidden="true">' + (P[name] || P.circle) + "</svg>"; }
  var BUTTON_ICONS = ["doc", "sync", "pie", "trash", "bolt", "terminal", "calendar", "pulse", "shield", "globe"];
  function btnIcon(b) { return BUTTON_ICONS.indexOf(b.icon) >= 0 ? b.icon : "bolt"; }

  var markN = 0;
  function waveMark() {
    var gid = "tbg" + (markN++);
    return '<svg viewBox="0 0 40 40" aria-hidden="true"><defs><linearGradient id="' + gid + '" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#4f8cf0"/><stop offset="1" stop-color="#1546b0"/></linearGradient></defs>' +
      '<circle cx="20" cy="20" r="19" fill="url(#' + gid + ')"/>' +
      '<path d="M7 18.5c3.3-2.8 6.6-2.8 9.9 0s6.6 2.8 9.9 0 4.6-2.3 6.2-1.4" stroke="#fff" stroke-width="2.6" fill="none" stroke-linecap="round"/>' +
      '<path d="M7 25c3.3-2.8 6.6-2.8 9.9 0s6.6 2.8 9.9 0 4.6-2.3 6.2-1.4" stroke="#fff" stroke-opacity=".7" stroke-width="2.6" fill="none" stroke-linecap="round"/>' +
      '<path d="M10 12.5c2.4-1.9 5.3-1.9 7.7 0s5.3 1.9 7.7 0" stroke="#fff" stroke-opacity=".45" stroke-width="2.2" fill="none" stroke-linecap="round"/></svg>';
  }
  function waveArt() {
    return '<svg class="wave" viewBox="0 0 300 170" aria-hidden="true">' +
      '<path d="M0 170V120c40-10 70-40 110-38 30 2 40 26 66 22 22-3 26-36 4-46-16-7-34 4-30 18-14-26 10-56 46-50 44 7 66 54 104 60v84z" fill="#e6eefc"/>' +
      '<path d="M0 170v-28c50-6 90-34 140-30 40 3 60 30 100 26 24-2 44-14 60-24v56z" fill="#d3e1f8"/>' +
      '<path d="M150 98c-6-18 14-38 40-32" stroke="#b5cdf3" stroke-width="2" fill="none"/>' +
      '<path d="M120 160c40-10 80-14 180-6" stroke="#fff" stroke-width="2" fill="none" opacity=".8"/></svg>';
  }
  // ---------- time ----------
  function fmtTime(iso, opts) {
    if (!iso) return "—";
    var d = new Date(iso);
    if (isNaN(d)) return "—";
    return new Intl.DateTimeFormat(state.lang, Object.assign({ timeZone: state.tz }, opts)).format(d);
  }
  function dayKey(d) { return new Intl.DateTimeFormat("en-CA", { timeZone: state.tz, year: "numeric", month: "2-digit", day: "2-digit" }).format(d); }
  function relDay(iso) {
    var d = new Date(iso), now = Date.now();
    var k = dayKey(d);
    if (k === dayKey(new Date(now))) return T("time.today");
    if (k === dayKey(new Date(now - 864e5))) return T("time.yesterday");
    if (k === dayKey(new Date(now + 864e5))) return T("time.tomorrow");
    return fmtTime(iso, { month: "short", day: "numeric" });
  }
  function clock(iso) { return fmtTime(iso, { hour: "numeric", minute: "2-digit" }); }
  function when(iso) { return iso ? T("time.when", { day: relDay(iso), time: clock(iso) }) : "—"; }
  function ago(iso) {
    if (!iso) return "—";
    var s = (Date.now() - new Date(iso).getTime()) / 1000;
    if (s < 60) return T("time.just_now");
    if (s < 3600) return T("time.min_ago", { n: Math.floor(s / 60) });
    if (s < 86400) return T("time.hour_ago", { n: Math.floor(s / 3600) });
    return T("time.day_ago", { n: Math.floor(s / 86400) });
  }
  function tzLabel() {
    try {
      var parts = new Intl.DateTimeFormat("en-US", { timeZone: state.tz, timeZoneName: "short" }).formatToParts(new Date());
      var n = parts.filter(function (p) { return p.type === "timeZoneName"; })[0];
      return state.tz + (n ? " (" + n.value + ")" : "");
    } catch (e) { return state.tz; }
  }
  function dur(ms) {
    if (!ms || ms < 0) return "—";
    var s = Math.round(ms / 1000);
    if (s < 60) return T("time.sec", { n: s });
    return T("time.min_sec", { m: Math.floor(s / 60), s: String(s % 60).padStart(2, "0") });
  }
  function usd(v, d) {
    d = d == null ? 2 : d;
    try { return new Intl.NumberFormat(state.lang, { style: "currency", currency: "USD", currencyDisplay: "narrowSymbol", minimumFractionDigits: d, maximumFractionDigits: d }).format(Number(v || 0)); }
    catch (e) { return "$" + Number(v || 0).toFixed(d); }
  }
  function num(v) { return new Intl.NumberFormat(state.lang).format(Number(v || 0)); }
  function shortId(id) { var s = String(id || ""); return s.slice(-4).toUpperCase(); }
  function cap(s) { s = String(s || ""); return s.charAt(0).toUpperCase() + s.slice(1); }

  // ---------- api ----------
  function api(path, opts) {
    opts = opts || {};
    var headers = { "Accept": "application/json" };
    var method = opts.method || "GET";
    if (method !== "GET") headers["X-CSRF-Token"] = state.csrf;
    var body = opts.body;
    if (body !== undefined) { headers["Content-Type"] = "application/json"; body = JSON.stringify(body); }
    return fetch(path, { method: method, headers: headers, body: body, credentials: "same-origin" }).then(function (res) {
      var ct = res.headers.get("content-type") || "";
      var p = ct.indexOf("json") >= 0 ? res.json() : res.text();
      return p.then(function (data) {
        if (res.status === 401 && path !== "/api/login") { showLogin(); var e1 = new Error(T("err.sign_in")); e1.auth = true; throw e1; }
        if (!res.ok) { var e2 = new Error((data && data.error) || res.statusText); e2.status = res.status; throw e2; }
        return data;
      });
    });
  }
  function toast(msg, bad) {
    var t = $("#toast");
    t.textContent = msg;
    t.className = "toast" + (bad ? " bad" : "");
    clearTimeout(toast._t);
    toast._t = setTimeout(function () { t.className = "toast hidden"; }, 3200);
  }
  function fail(e) { toast(e.message, true); }
  // CSP forbids inline style attributes; sizes are applied through CSSOM.
  function applySizes(root) {
    $$("[data-w]", root).forEach(function (el) { el.style.width = Math.max(0, Math.min(100, Number(el.getAttribute("data-w")))) + "%"; });
    $$("[data-h]", root).forEach(function (el) { el.style.height = Math.max(1, Math.min(100, Number(el.getAttribute("data-h")))) + "%"; });
  }

  // ---------- status helpers ----------
  function statusPill(st) {
    var map = { succeeded: ["ok", T("st.succeeded")], running: ["run", T("st.running")], queued: ["info", T("st.queued")], failed: ["bad", T("st.failed")], timeout: ["bad", T("st.timeout")], canceled: ["warn", T("st.canceled")], skipped: ["", T("st.skipped")] };
    var m = map[st] || ["", st || "—"];
    return '<span class="pill ' + m[0] + '">' + esc(m[1]) + "</span>";
  }
  function taskPill(st) {
    var m = { active: ["ok", T("task.active")], paused: ["warn", T("task.paused")], archived: ["", T("task.archived")] }[st] || ["", st];
    return '<span class="pill ' + m[0] + '">' + esc(m[1]) + "</span>";
  }
  function triggerText(tr) {
    return { cron: T("trig.cron"), manual: T("trig.manual"), quick: T("trig.quick"), button: T("trig.button") }[tr] || T("trig.other", { t: tr });
  }
  function runIcon(r) {
    var n = (r.task_name || r.prompt || "").toLowerCase();
    if (/report|doc/.test(n)) return "doc";
    if (/sync|issue/.test(n)) return "sync";
    if (/digest|week|summar/.test(n)) return "pie";
    if (/clean|log|trash/.test(n)) return "trash";
    if (/backup/.test(n)) return "shield";
    if (r.trigger === "quick") return "sparkle";
    return "terminal";
  }
  function runTitle(r) { return (r.task_name || T("trig.quick")) + " #" + shortId(r.id); }
  function cronHuman(c) {
    if (!c) return T("cron.manual");
    var p = c.split(/\s+/);
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
  var NAV = [["home", "#/"], ["tasks", "#/tasks"], ["skills", "#/skills"], ["buttons", "#/buttons"], ["plugins", "#/plugins"], ["runs", "#/runs"], ["spend", "#/spend"], ["settings", "#/settings"]];

  function renderShell() {
    $("#brand-mark").innerHTML = waveMark();
    $("#nav").innerHTML = NAV.map(function (n) { return '<a href="' + n[1] + '" data-nav="' + n[0] + '">' + ic(n[0]) + "<span>" + esc(T("nav." + n[0])) + "</span></a>"; }).join("");
    var tag = document.querySelector("#shell .brand-text small"); if (tag) tag.textContent = T("brand.tagline");
  }
  function setNav(name) { $$("#nav a").forEach(function (a) { a.classList.toggle("active", a.getAttribute("data-nav") === name); }); }

  function renderTopbar(crumbs) {
    var c = crumbs && crumbs.length ? '<div class="crumbs">' + crumbs.map(function (x, i) {
      return (i ? '<span class="sep">' + ic("chev", "sm") + "</span>" : "") + (x[1] ? '<a href="' + esc(x[1]) + '">' + esc(x[0]) + "</a>" : "<span>" + esc(x[0]) + "</span>");
    }).join("") + "</div>" : "";
    $("#topbar").innerHTML = c + '<div class="spacer"></div>' +
      '<div class="search">' + ic("search") + '<input id="q" type="search" placeholder="' + esc(T("top.search")) + '" autocomplete="off" aria-label="' + esc(T("top.search_label")) + '"><kbd>K</kbd><div id="qres" class="search-results hidden"></div></div>' +
      '<button class="lang-btn" id="btn-lang" title="' + esc(T("top.language")) + '" aria-label="' + esc(T("top.language")) + '">' + (state.lang === "zh-CN" ? "中" : "EN") + "</button>" +
      '<button class="icon-btn" id="btn-refresh" title="' + esc(T("top.refresh")) + '" aria-label="' + esc(T("top.refresh")) + '">' + ic("refresh") + "</button>" +
      '<div class="rel"><button class="icon-btn" id="btn-bell" title="' + esc(T("top.notifications")) + '" aria-label="' + esc(T("top.notifications")) + '">' + ic("bell") + '<span class="badge-dot hidden" id="bell-dot"></span></button><div id="notif" class="popover hidden"></div></div>';
    wireTopbar();
  }

  function wireTopbar() {
    var q = $("#q"), res = $("#qres"), timer;
    q.addEventListener("input", function () {
      clearTimeout(timer);
      var v = q.value.trim();
      if (!v) { res.classList.add("hidden"); return; }
      timer = setTimeout(function () {
        api("/api/search?q=" + enc(v)).then(function (hits) {
          res.innerHTML = hits.length ? hits.map(function (h) {
            var href = h.kind === "task" ? "#/tasks/" + enc(h.id) : h.kind === "run" ? "#/runs/" + enc(h.id) : "#/skills";
            return '<a href="' + href + '"><span class="kind">' + esc(T("search.kind." + h.kind)) + "</span><span>" + esc(h.kind === "run" ? T("search.run", { id: shortId(h.id) }) : h.name) + '</span><span class="sub">' + esc(h.sub) + "</span></a>";
          }).join("") : '<div class="none">' + esc(T("search.none")) + "</div>";
          res.classList.remove("hidden");
        }).catch(function () {});
      }, 180);
    });
    q.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { var a = $("a", res); if (a) { location.hash = a.getAttribute("href"); res.classList.add("hidden"); q.blur(); } }
      if (e.key === "Escape") { res.classList.add("hidden"); q.blur(); }
    });
    q.addEventListener("blur", function () { setTimeout(function () { res.classList.add("hidden"); }, 150); });
    $("#btn-refresh").addEventListener("click", function () { route(); });
    $("#btn-lang").addEventListener("click", function () {
      var next = state.lang === "zh-CN" ? "en" : "zh-CN";
      setLanguage(next);
    });
    $("#btn-bell").addEventListener("click", function (e) {
      e.stopPropagation();
      var pop = $("#notif");
      if (!pop.classList.contains("hidden")) { pop.classList.add("hidden"); return; }
      api("/api/notifications").then(function (list) {
        pop.innerHTML = "<h4>" + esc(T("top.notifications")) + "</h4>" + (list.length ? list.slice(0, 8).map(function (n) {
          return '<div class="item"><strong>' + esc(T("notif." + n.event) === "notif." + n.event ? cap(n.event.replace(/_/g, " ")) : T("notif." + n.event)) + "</strong><div>" + esc(n.body) + '</div><small class="muted">' + esc(when(n.created_at)) + "</small></div>";
        }).join("") : '<div class="item muted">' + esc(T("notif.none")) + "</div>");
        pop.classList.remove("hidden");
        if (list[0]) { state.notifSeen = list[0].created_at; localStorage.setItem("tb_notif_seen", state.notifSeen); }
        $("#bell-dot").classList.add("hidden");
      }).catch(fail);
    });
    api("/api/notifications").then(function (list) {
      var dot = $("#bell-dot");
      if (dot) dot.classList.toggle("hidden", !(list[0] && list[0].created_at > state.notifSeen));
    }).catch(function () {});
  }
  document.addEventListener("click", function (e) { var p = $("#notif"); if (p && !p.contains(e.target)) p.classList.add("hidden"); });
  document.addEventListener("keydown", function (e) {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); var q = $("#q"); if (q) q.focus(); }
  });

  function renderStatus(o) {
    var warn = o && o.spend && o.spend.max_usd && o.spend.used_usd >= o.spend.max_usd;
    $("#sys-status").innerHTML = '<span class="dot' + (warn ? " warn" : "") + '"></span><div><strong class="' + (warn ? "warn" : "") + '">' + esc(warn ? T("sys.budget_reached") : T("sys.ok")) + "</strong><small>" +
      esc(o && o.active_runs ? plural(o.active_runs, "sys.runs_one", "sys.runs_many") : T("sys.smooth")) + "</small></div>";
  }

  // ---------- idle / SSE ----------
  function stopStream() { if (state.es) { state.es.close(); state.es = null; } }
  function bumpIdle() { clearTimeout(state.idleTimer); state.idleTimer = setTimeout(stopStream, IDLE_MS); }
  ["mousemove", "click", "scroll", "keydown"].forEach(function (ev) { document.addEventListener(ev, bumpIdle, { passive: true }); });

  // ---------- login ----------
  function showLogin(totp) {
    stopStream();
    $("#shell").classList.add("hidden");
    var l = $("#login");
    l.classList.remove("hidden");
    l.innerHTML = '<div class="card login-card"><div class="brand"><span class="brand-mark">' + waveMark() + '</span><span class="brand-text"><strong>tidebot</strong><small>' + esc(T("brand.tagline")) + '</small></span></div>' +
      '<form id="login-form" autocomplete="on"><label class="field"><span>' + esc(T("login.password")) + '</span><input type="password" id="lp" required minlength="8" autocomplete="current-password" autofocus></label>' +
      '<label class="field' + (totp ? "" : " hidden") + '" id="totp-field"><span>' + esc(T("login.totp")) + '</span><input id="lt" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" autocomplete="one-time-code" placeholder="123456"></label>' +
      '<p class="err" id="lerr"></p><button class="btn primary" type="submit">' + esc(T("login.submit")) + '</button></form>' +
      '<p class="login-foot">' + esc(T("login.foot")) + ' <code>agentctl auth set-password</code></p>' +
      '<div class="login-lang">' + langSelect("login-lang-sel") + "</div></div>";
    $("#login-lang-sel").addEventListener("change", function (e) { loadLang(e.target.value).then(function () { showLogin(totp); }); });
    $("#login-form").addEventListener("submit", function (e) {
      e.preventDefault();
      $("#lerr").textContent = "";
      api("/api/login", { method: "POST", body: { password: $("#lp").value, totp: $("#lt").value } }).then(function () { boot(); })
        .catch(function (err) {
          if (/credentials/.test(err.message) || err.status === 401) $("#totp-field").classList.remove("hidden");
          $("#lerr").textContent = err.message;
        });
    });
  }

  // ---------- page plumbing ----------
  function page() { return $("#page"); }
  function mount(html, nav, crumbs) {
    setNav(nav);
    renderTopbar(crumbs);
    page().innerHTML = html;
    applySizes(page());
  }
  function modal(html, onMount) {
    var root = $("#modal-root");
    root.innerHTML = '<div class="modal-bg"><div class="card modal">' + html + "</div></div>";
    var close = function () { root.innerHTML = ""; };
    $(".modal-bg", root).addEventListener("click", function (e) { if (e.target.classList.contains("modal-bg")) close(); });
    $$("[data-close]", root).forEach(function (b) { b.addEventListener("click", close); });
    if (onMount) onMount(root, close);
  }
  function wireRows(root) { $$("[data-href]", root || page()).forEach(function (el) { el.addEventListener("click", function (e) { if (e.target.closest("a,button")) return; location.hash = el.getAttribute("data-href"); }); }); }

  function runsTable(runs) {
    if (!runs.length) return emptyState("runs", T("runs.none"), T("runs.none_sub"));
    return '<table class="table"><thead><tr><th>' + esc(T("col.run")) + "</th><th>" + esc(T("col.agent_task")) + "</th><th>" + esc(T("col.status")) + "</th><th>" + esc(T("col.started")) + "</th><th>" + esc(T("col.duration")) + '</th><th class="right">' + esc(T("col.cost")) + '</th><th></th></tr></thead><tbody>' +
      runs.map(function (r) {
        var running = r.status === "running";
        return '<tr class="click" data-href="#/runs/' + enc(r.id) + '"><td><div class="cell-main"><span class="ico">' + ic(runIcon(r), "sm") + "</span><div><strong>" + esc(runTitle(r)) + "</strong><small>" + esc(triggerText(r.trigger)) + "</small></div></div></td>" +
          "<td><strong>" + esc(r.task_name || T("run.adhoc")) + "</strong><small>" + esc(r.provider || "—") + (r.version_no ? " · v" + esc(r.version_no) : "") + "</small></td>" +
          "<td>" + statusPill(r.status) + '</td><td class="nowrap">' + esc(when(r.started_at || r.created_at)) + '</td><td class="nowrap">' + esc(running ? dur(Date.now() - new Date(r.started_at).getTime()) : dur(r.duration_ms)) +
          '</td><td class="right"><strong>' + esc(usd(r.cost_usd)) + '</strong></td><td class="right"><a class="icon-btn" href="#/runs/' + enc(r.id) + '" aria-label="' + esc(T("run.open")) + '">' + ic("more", "sm") + "</a></td></tr>";
      }).join("") + "</tbody></table>";
  }
  // ---------- HOME ----------
  function runButton(id, el) {
    if (el) el.classList.add("busy");
    return api("/api/buttons/" + enc(id) + "/run", { method: "POST", body: {} })
      .then(function (r) { toast(T("toast.started", { name: r.started })); setTimeout(route, 700); })
      .catch(fail)
      .then(function () { if (el) el.classList.remove("busy"); });
  }
  function renderHome() {
    return api("/api/overview").then(function (o) {
      renderStatus(o);
      var qa = (o.buttons || []).slice(0, 4).map(function (b) {
        return '<button class="card qa" data-btn="' + esc(b.id) + '"><span class="qa-ico">' + ic(btnIcon(b), "lg") + "</span><strong>" + esc(b.label) + "</strong><small>" + esc(b.description || "") + "</small></button>";
      });
      while (qa.length < 4) qa.push('<a class="card qa" href="#/buttons"><span class="qa-ico">' + ic("plus", "lg") + "</span><strong>" + esc(T("home.add_button")) + "</strong><small>" + esc(T("home.add_button_sub")) + "</small></a>");
      qa.push('<button class="card qa tint" id="qa-new"><span class="qa-ico">' + ic("sparkle", "lg") + "</span><strong>" + esc(T("home.quick")) + "</strong><small>" + esc(T("home.quick_sub")) + " " + ic("arrow", "sm") + "</small></button>");

      var sched = o.schedule || [];
      var schedHtml = sched.length ? '<ul class="timeline">' + sched.map(function (s) {
        return '<li><span class="tl-dot"></span><span class="tl-time">' + esc(clock(s.at)) + '</span><div class="tl-name"><strong>' + esc(s.task_name) + "</strong><small>" + esc(cronHuman(s.cron)) + '</small></div><span class="tag">' + esc(relDay(s.at)) + "</span></li>";
      }).join("") + "</ul>" : emptyState("calendar", T("home.no_sched"), T("home.no_sched_sub"));

      var sp = o.spend || {}, max = sp.max_usd || 0, pct = max ? Math.min(100, sp.used_usd / max * 100) : 0;
      var resetMs = new Date(sp.resets_at).getTime() - Date.now();
      var resetTxt = resetMs > 0 ? T("time.hm", { h: Math.floor(resetMs / 3600e3), m: Math.floor(resetMs % 3600e3 / 60e3) }) : T("time.soon");

      mount(
        '<div class="grid g5">' + qa.join("") + "</div>" +
        '<div class="grid g2">' +
        '<section class="card fill"><div class="card-head"><span class="ico">' + ic("calendar") + "</span><h2>" + esc(T("home.schedule")) + '</h2><a class="link" href="#/tasks">' + esc(T("home.view_tasks")) + " " + ic("arrow", "sm") + "</a></div>" +
        '<div class="grow">' + schedHtml + "</div>" +
        '<div class="card-foot">' + ic("info", "sm") + "<span>" + esc(T("home.times_in", { tz: tzLabel() })) + '</span><a class="link" href="#/tasks">' + esc(T("home.manage")) + " " + ic("arrow", "sm") + "</a></div></section>" +
        '<section class="card spend-card">' + waveArt() + '<div class="card-head"><span class="ico">' + ic("receipt") + "</span><h2>" + esc(T("home.spend")) + '</h2><a class="link" href="#/spend">' + esc(T("home.view_spend")) + " " + ic("arrow", "sm") + "</a></div>" +
        '<div class="spend-big">' + esc(usd(sp.used_usd)) + " <span>/ " + esc(max ? usd(max) : T("spend.no_cap")) + "</span></div>" +
        '<div class="meta">' + esc((max ? T("home.pct_budget", { p: Math.round(pct) }) : T("home.no_budget")) + " · " + T("home.tokens", { n: num(sp.tokens) })) + "</div>" +
        '<div class="progress' + (pct >= 100 ? " bad" : pct >= 80 ? " warn" : "") + '"><span data-w="' + pct + '"></span></div>' +
        '<div class="meta">' + esc(T("home.resets", { t: resetTxt, tz: sp.timezone || state.tz })) + "</div></section>" +
        "</div>" +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("pulse") + "</span><h2>" + esc(T("home.recent")) + '</h2><a class="link" href="#/runs">' + esc(T("home.view_runs")) + " " + ic("arrow", "sm") + "</a></div>" + runsTable(o.runs || []) + "</section>",
        "home", []);
      wireRows();
      $$("[data-btn]").forEach(function (b) { b.addEventListener("click", function () { runButton(b.getAttribute("data-btn"), b); }); });
      $("#qa-new").addEventListener("click", quickRunModal);
      if ((o.runs || []).some(function (r) { return r.status === "running" || r.status === "queued"; })) scheduleRefresh("#/", 2500);
    });
  }
  function scheduleRefresh(hash, ms) {
    clearTimeout(scheduleRefresh._t);
    scheduleRefresh._t = setTimeout(function () { if ((location.hash || "#/") === hash && !$("#modal-root").innerHTML) route(); }, ms);
  }

  function quickRunModal() {
    api("/api/settings").then(function (st) {
      modal('<div class="card-head"><span class="ico">' + ic("sparkle") + "</span><h2>" + esc(T("home.quick")) + "</h2></div>" +
        '<form id="qr"><label class="field"><span>' + esc(T("f.prompt")) + '</span><textarea name="prompt" required maxlength="8000" placeholder="' + esc(T("qr.placeholder")) + '"></textarea></label>' +
        '<label class="field"><span>' + esc(T("f.provider")) + '</span><select name="provider">' + st.providers.map(function (p) { return '<option value="' + esc(p.name) + '"' + (p.name === st.default_provider ? " selected" : "") + ">" + esc(p.name) + (p.key_set ? "" : " " + T("qr.no_key")) + "</option>"; }).join("") + "</select></label>" +
        '<p class="hint">' + esc(T("qr.hint")) + "</p>" +
        '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary" type="submit">' + ic("play", "sm") + esc(T("btn.run")) + "</button></div></form>",
        function (root, close) {
          $("#qr", root).addEventListener("submit", function (e) {
            e.preventDefault();
            var f = e.target;
            api("/api/runs/quick", { method: "POST", body: { prompt: f.prompt.value, provider: f.provider.value } }).then(function () {
              close(); toast(T("toast.quick_started"));
              setTimeout(function () { api("/api/runs?limit=1").then(function (r) { location.hash = r[0] ? "#/runs/" + r[0].id : "#/runs"; }); }, 500);
            }).catch(fail);
          });
        });
    }).catch(fail);
  }

  // ---------- TASKS ----------
  function renderTasks() {
    return api("/api/tasks").then(function (tasks) {
      var rows = tasks.length ? '<table class="table"><thead><tr><th>' + esc(T("col.task")) + "</th><th>" + esc(T("col.schedule")) + "</th><th>" + esc(T("col.status")) + "</th><th>" + esc(T("col.version")) + "</th><th>" + esc(T("col.last_run")) + "</th><th>" + esc(T("col.runs_today")) + '</th><th></th></tr></thead><tbody>' +
        tasks.map(function (t) {
          return '<tr class="click" data-href="#/tasks/' + enc(t.id) + '"><td><div class="cell-main"><span class="ico">' + ic(runIcon({ task_name: t.name }), "sm") + "</span><div><strong>" + esc(t.name) + "</strong><small>" + esc(t.goal || t.prompt) + "</small></div></div></td>" +
            '<td class="nowrap">' + esc(cronHuman(t.cron)) + "</td><td>" + taskPill(t.status) + '</td><td><span class="tag">v' + esc(t.version_no) + "</span></td><td>" + (t.last_status ? statusPill(t.last_status) : '<span class="muted">' + esc(T("tasks.never")) + "</span>") + "</td><td>" + esc(t.runs_today) + '</td><td class="right"><span class="muted">' + ic("chev", "sm") + "</span></td></tr>";
        }).join("") + "</tbody></table>" : emptyState("tasks", T("tasks.none"), T("tasks.none_sub"));
      mount('<div class="page-head"><div><h1>' + esc(T("nav.tasks")) + "</h1><p>" + esc(plural(tasks.length, "tasks.count_one", "tasks.count_many") + " · " + T("tasks.sub")) + "</p></div></div>" +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("tasks") + "</span><h2>" + esc(T("tasks.all")) + "</h2></div>" + rows + "</section>", "tasks", [[T("nav.tasks")]]);
      wireRows();
    });
  }
  // ---------- TASK DETAIL ----------
  function versionTree(vers, current) {
    if (!vers.length) return emptyState("branch", T("ver.none"), "");
    var byId = {}, kids = {}, roots = [], lane = {}, depth = {}, lanes = 0, maxD = 0;
    vers.forEach(function (v) { byId[v.id] = v; kids[v.id] = []; });
    vers.forEach(function (v) { if (v.parent_id && byId[v.parent_id]) kids[v.parent_id].push(v); else roots.push(v); });
    // Each branch gets its own lane; the first child continues its parent's lane.
    (function walk(list, d, l) {
      list.forEach(function (v, i) {
        var ln = i === 0 && l != null ? l : lanes++;
        lane[v.id] = ln; depth[v.id] = d; maxD = Math.max(maxD, d);
        walk(kids[v.id], d + 1, ln);
      });
    })(roots, 0, null);
    var W = 312, NW = 84, NH = 44, VG = 20, pos = {};
    var laneX = function (l) { return Math.round(lanes === 1 ? W / 2 : 56 + l * Math.min(124, (W - 112) / (lanes - 1))); };
    vers.forEach(function (v) { pos[v.id] = { x: laneX(lane[v.id]), y: 4 + depth[v.id] * (NH + VG) }; });
    var H = 8 + (maxD + 1) * NH + maxD * VG;
    var s = '<svg class="vtree" viewBox="0 0 ' + W + " " + H + '" role="img" aria-label="' + esc(T("ver.tree")) + '">';
    vers.forEach(function (v) {
      if (v.parent_id && pos[v.parent_id]) {
        var a = pos[v.parent_id], b = pos[v.id];
        s += '<path class="edge" d="M' + a.x + " " + (a.y + NH) + " C" + a.x + " " + (a.y + NH + VG / 2) + " " + b.x + " " + (b.y - VG / 2) + " " + b.x + " " + b.y + '"/>';
      }
    });
    vers.forEach(function (v) {
      var p = pos[v.id], cur = v.id === current;
      s += '<g class="node' + (cur ? " cur" : "") + '" data-ver="' + esc(v.id) + '" data-no="' + esc(v.no) + '"><title>' + esc((v.message || v.source) + (cur ? " " + T("ver.cur_paren") : " — " + T("ver.click_rb"))) + "</title>" +
        '<rect x="' + (p.x - NW / 2) + '" y="' + p.y + '" width="' + NW + '" height="' + NH + '" rx="8"/>' +
        '<text x="' + p.x + '" y="' + (p.y + 19) + '" text-anchor="middle" font-size="13" font-weight="600">v' + esc(v.no) + "</text>" +
        '<text class="sub" x="' + p.x + '" y="' + (p.y + 34) + '" text-anchor="middle">' + esc(ago(v.created_at)) + "</text></g>";
      if (cur) {
        var right = p.x + NW / 2 + 56 <= W, tx = right ? p.x + NW / 2 + 8 : p.x - NW / 2 - 8;
        s += '<text class="ptr" x="' + tx + '" y="' + (p.y + 26) + '" text-anchor="' + (right ? "start" : "end") + '">' + esc(T("ver.current")) + "</text>";
      }
    });
    return s + "</svg>";
  }

  var REQ_ST = { pending: ["info", "req.pending"], applied: ["ok", "req.applied"], scheduled: ["warn", "req.scheduled"], consumed: ["ok", "req.consumed"], rejected: ["", "req.rejected"], needs_decision: ["bad", "req.conflict"] };
  function threadHtml(items) {
    if (!items.length) return emptyState("sparkle", T("thread.none"), T("thread.none_sub"));
    var groups = [], idx = {};
    items.forEach(function (it) {
      var k = it.message_id || it.id;
      if (!(k in idx)) { idx[k] = groups.length; groups.push({ key: k, utter: it.utterance, at: it.created_at, items: [] }); }
      groups[idx[k]].items.push(it);
    });
    return groups.map(function (g) {
      var all = function (f) { return g.items.every(f); };
      var scope = all(function (i) { return i.scope === "next_run"; }) ? "next" : all(function (i) { return i.scope === "permanent"; }) ? "perm" : "mixed";
      var pending = g.items.filter(function (i) { return i.status === "pending"; });
      var reply = T(g.items.some(function (i) { return i.status === "needs_decision"; }) ? "thread.r_conflict"
        : pending.length ? (scope === "next" ? "thread.r_next" : "thread.r_confirm")
        : all(function (i) { return i.status === "rejected"; }) ? "thread.r_discarded"
        : scope === "next" ? "thread.r_scheduled" : "thread.r_done");
      var conflict = g.items.some(function (i) { return i.status === "needs_decision"; });
      var multi = pending.length + g.items.filter(function (i) { return i.status === "needs_decision"; }).length > 1;
      var bid = esc(g.key);
      return '<div class="msg"><span class="avatar">' + esc(T("thread.me")) + '</span><div><div class="msg-meta"><strong>' + esc(T("thread.you")) + '</strong><small>' + esc(ago(g.at)) + '</small></div><div class="msg-body">' + esc(g.utter || T("thread.amendment")) + "</div></div></div>" +
        '<div class="msg"><span class="avatar bot">' + waveMark() + '</span><div><div class="msg-meta"><strong>tidebot</strong><small>' + esc(ago(g.at)) + '</small></div><div class="msg-body">' + esc(reply) + "</div>" +
        '<div class="req-card"><div class="req-card-head">' + esc(T(scope === "next" ? "req.additional" : "req.updated")) +
        (scope === "next" ? '<span class="tag next">' + esc(T("req.next_only")) + "</span>" : scope === "perm" ? '<span class="tag perm">' + esc(T("req.permanent")) + "</span>" : '<span class="tag">' + esc(T("req.mixed")) + "</span>") + "</div>" +
        g.items.map(function (i) {
          var sl = REQ_ST[i.status] ? [REQ_ST[i.status][0], T(REQ_ST[i.status][1])] : ["", i.status];
          var ck = i.status === "rejected" ? ic("x", "sm") : (i.status === "pending" || i.status === "needs_decision") ? ic("circle", "sm") : ic("checkc", "sm");
          var sel = multi && i.status === "pending" ? '<input type="checkbox" class="req-sel" data-sel="' + bid + '" value="' + esc(i.id) + '" checked aria-label="' + esc(T("req.include")) + '">' : "";
          return '<div class="req-item">' + sel + '<span class="ck ' + (i.status === "rejected" ? "no" : i.status === "pending" ? "pending" : "") + '">' + ck + '</span><span class="op">' + esc(T("op." + i.op)) + '</span><span class="txt">' + esc(i.content || i.target_spec_item) +
            (scope === "mixed" ? ' <span class="tag ' + (i.scope === "next_run" ? "next" : "perm") + '">' + esc(T(i.scope === "next_run" ? "req.next_short" : "req.permanent")) + "</span>" : "") +
            '</span><span class="st"><span class="pill ' + sl[0] + '">' + esc(sl[1]) + "</span></span></div>" +
            (i.status === "needs_decision" ? '<div class="req-actions"><button class="btn sm" data-resolve="' + esc(i.id) + '" data-res="replace">' + esc(T("req.replace")) + '</button><button class="btn sm" data-resolve="' + esc(i.id) + '" data-res="coexist">' + esc(T("req.coexist")) + "</button></div>" : "");
        }).join("") +
        (pending.length || conflict ? (conflict ? '<p class="hint req-hint">' + ic("info", "sm") + esc(T("req.resolve_first")) + "</p>" : "") +
          '<div class="req-actions"><button class="btn ok" data-confirm="' + bid + '"' + (conflict ? " disabled" : "") + ">" + ic("check", "sm") + esc(T(multi ? "req.confirm_all" : "req.confirm")) + '</button><button class="btn danger" data-reject="' + bid + '">' + ic("x", "sm") + esc(T(multi ? "req.reject_all" : "req.reject")) + "</button></div>" : "") +
        "</div></div></div>";
    }).join("");
  }

  function taskTab(tab, tk, items, vers) {
    if (tab === "thread") {
      return '<div class="thread scroll" id="thread">' + threadHtml(items) + "</div>" +
        '<form class="composer" id="composer"><input name="u" placeholder="' + esc(T("thread.composer")) + '" autocomplete="off" maxlength="2000" aria-label="' + esc(T("thread.amendment")) + '"><button class="btn primary sm" type="submit" aria-label="' + esc(T("thread.send")) + '">' + ic("send", "sm") + "</button></form>";
    }
    if (tab === "requirements") {
      return '<div class="thread"><dl class="kv"><dt>' + esc(T("td.goal")) + "</dt><dd>" + esc(tk.goal || "—") + "</dd><dt>" + esc(T("f.prompt")) + "</dt><dd>" + esc(tk.prompt) + "</dd><dt>" + esc(T("td.requirements")) + "</dt><dd>" +
        (tk.constraints.length ? '<ul class="list-plain">' + tk.constraints.map(function (c) { return "<li>" + esc(c) + "</li>"; }).join("") + "</ul>" : '<span class="muted">' + esc(T("td.none_yet")) + "</span>") +
        "</dd><dt>" + esc(T("nav.skills")) + "</dt><dd>" + (tk.skills.length ? tk.skills.map(esc).join(", ") : '<span class="muted">' + esc(T("td.none")) + "</span>") + "</dd></dl></div>";
    }
    if (tab === "settings") {
      return '<div class="thread"><dl class="kv"><dt>' + esc(T("col.schedule")) + "</dt><dd>" + esc(cronHuman(tk.cron)) + (tk.cron ? " <code>" + esc(tk.cron) + "</code>" : "") + "</dd><dt>" + esc(T("f.provider")) + "</dt><dd>" + esc(tk.provider || T("td.default")) +
        "</dd><dt>" + esc(T("td.workspace")) + "</dt><dd><code>" + esc(tk.workspace || "—") + "</code></dd><dt>" + esc(T("col.status")) + "</dt><dd>" + taskPill(tk.status) + "</dd><dt>" + esc(T("td.task_id")) + "</dt><dd><code>" + esc(tk.id) + "</code></dd></dl></div>";
    }
    return '<div class="thread"><table class="table"><thead><tr><th>' + esc(T("col.version")) + "</th><th>" + esc(T("col.source")) + "</th><th>" + esc(T("col.note")) + "</th><th>" + esc(T("col.created")) + '</th><th></th></tr></thead><tbody>' +
      vers.versions.slice().reverse().map(function (v) {
        var cur = v.id === vers.current;
        return "<tr><td><strong>v" + esc(v.no) + "</strong>" + (cur ? ' <span class="tag perm">' + esc(T("ver.current")) + "</span>" : "") + "</td><td>" + esc(T("src." + v.source) === "src." + v.source ? v.source : T("src." + v.source)) + "</td><td>" + esc(v.message || "—") + '</td><td class="nowrap">' + esc(when(v.created_at)) +
          '</td><td class="right">' + (cur ? "" : '<button class="btn sm" data-rollback="' + esc(v.id) + '" data-no="' + esc(v.no) + '">' + ic("rollback", "sm") + esc(T("ver.rollback")) + "</button>") + "</td></tr>";
      }).join("") + "</tbody></table></div>";
  }
  function renderTaskDetail(id, tab) {
    tab = tab || "thread";
    var base = "/api/tasks/" + enc(id);
    return Promise.all([api(base), api(base + "/amendments"), api(base + "/versions"), api(base + "/runs?limit=6")]).then(function (r) {
      var t = r[0], items = r[1] || [], vers = r[2], runs = r[3] || [];
      var mini = runs.length ? '<ul class="mini-runs">' + runs.map(function (x) {
        var d = x.status === "succeeded" ? "" : (x.status === "running" || x.status === "queued") ? "run" : "bad";
        return '<li data-href="#/runs/' + enc(x.id) + '"><span class="d ' + d + '"></span><div><strong>' + (x.version_no ? "v" + esc(x.version_no) : "#" + esc(shortId(x.id))) + "</strong><small>" + esc(when(x.created_at)) + '</small></div><span class="muted">' + esc(dur(x.duration_ms)) + "</span><strong>" + esc(usd(x.cost_usd, 3)) + "</strong></li>";
      }).join("") + "</ul>" : emptyState("runs", T("runs.none"), "");
      var tabs = [["thread", T("td.tab_thread")], ["requirements", T("td.requirements")], ["settings", T("nav.settings")], ["history", T("td.tab_history")]];
      mount('<div class="grid g-detail">' +
        '<section class="card"><div class="task-head"><div class="grow-col"><h1>' + esc(t.name) + "</h1><p>" + esc(t.goal || t.prompt) + "</p>" +
        '<div class="chips"><span class="chip">' + ic("user", "sm") + esc(T("td.owned")) + "</span><span class=\"chip\">" + ic("clock", "sm") + esc(cronHuman(t.cron)) + "</span>" +
        '<span class="chip">' + ic("calendar", "sm") + esc(T("td.created_updated", { c: fmtTime(t.created_at, { month: "short", day: "numeric", year: "numeric" }), u: ago(t.updated_at) })) + "</span>" + taskPill(t.status) + "</div></div>" +
        '<div class="row-actions">' + (t.status === "active" ? '<button class="btn" id="t-toggle" data-act="pause">' + ic("pause", "sm") + esc(T("td.pause")) + "</button>" : '<button class="btn" id="t-toggle" data-act="resume">' + ic("play", "sm") + esc(T("td.resume")) + "</button>") +
        '<button class="btn primary" id="t-run">' + ic("play", "sm") + esc(T("td.run_now")) + "</button></div></div>" +
        '<nav class="tabs">' + tabs.map(function (x) { return '<button data-tab="' + x[0] + '" class="' + (x[0] === tab ? "active" : "") + '">' + esc(x[1]) + (x[0] === "thread" && items.some(function (i) { return i.status === "pending"; }) ? ' <span class="pill info plain">' + items.filter(function (i) { return i.status === "pending"; }).length + "</span>" : "") + "</button>"; }).join("") + "</nav>" +
        taskTab(tab, t, items, vers) + "</section>" +
        '<div class="stack"><section class="card"><div class="card-head"><span class="ico">' + ic("branch") + '</span><h3>' + esc(T("ver.tree")) + '</h3><span class="link muted">' + esc(T("ver.n_current", { n: t.version_no })) + "</span></div>" + versionTree(vers.versions, vers.current) + '<p class="hint mt">' + esc(T("ver.hint")) + "</p></section>" +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("runs") + '</span><h3>' + esc(T("home.recent")) + '</h3><a class="link" href="#/runs?task=' + enc(t.id) + '">' + esc(T("td.view_all")) + "</a></div>" + mini + "</section></div></div>",
        "tasks", [[T("nav.tasks"), "#/tasks"], [t.name]]);
      var reload = function () { renderTaskDetail(id, tab); };
      wireRows();
      $$("[data-tab]").forEach(function (b) { b.addEventListener("click", function () { renderTaskDetail(id, b.getAttribute("data-tab")); }); });
      $("#t-run").addEventListener("click", function () { api(base + "/run", { method: "POST", body: {} }).then(function () { toast(T("toast.run_started")); setTimeout(reload, 800); }).catch(fail); });
      var tg = $("#t-toggle");
      tg.addEventListener("click", function () { api(base + "/" + tg.getAttribute("data-act"), { method: "POST", body: {} }).then(reload).catch(fail); });
      var picked = function (bid) {
        var boxes = $$('[data-sel="' + bid + '"]');
        return boxes.length ? boxes.filter(function (c) { return c.checked; }).map(function (c) { return c.value; }) : null;
      };
      $$(".req-sel").forEach(function (c) { c.addEventListener("change", function () {
        var bid = c.getAttribute("data-sel"), n = picked(bid).length, btn = $('[data-confirm="' + bid + '"]');
        if (btn && !btn.hasAttribute("data-blocked")) btn.disabled = n === 0;
      }); });
      $$("[data-confirm][disabled]").forEach(function (b) { b.setAttribute("data-blocked", "1"); });
      var batch = function (b, attr, act, msg) {
        b.addEventListener("click", function () {
          var bid = b.getAttribute(attr), only = picked(bid);
          if (only && !only.length) return;
          b.disabled = true;
          api("/api/amendments/" + enc(bid) + "/" + act, { method: "POST", body: { only: only || [] } }).then(function () { toast(msg); reload(); }).catch(function (e) { b.disabled = false; fail(e); });
        });
      };
      $$("[data-confirm]").forEach(function (b) { batch(b, "data-confirm", "confirm-batch", T("toast.confirmed")); });
      $$("[data-reject]").forEach(function (b) { batch(b, "data-reject", "reject-batch", T("toast.rejected")); });
      $$("[data-resolve]").forEach(function (b) { b.addEventListener("click", function () { api("/api/amendments/" + enc(b.getAttribute("data-resolve")) + "/resolve", { method: "POST", body: { resolution: b.getAttribute("data-res") } }).then(reload).catch(fail); }); });
      var rollback = function (vid, no) {
        if (vid === vers.current) return;
        if (!confirm(T("ver.confirm_rb", { n: no }))) return;
        api(base + "/rollback", { method: "POST", body: { version_id: vid } }).then(function () { toast(T("toast.rolled_back", { n: no })); reload(); }).catch(fail);
      };
      $$("[data-rollback]").forEach(function (b) { b.addEventListener("click", function () { rollback(b.getAttribute("data-rollback"), b.getAttribute("data-no")); }); });
      $$(".vtree .node").forEach(function (g) { g.addEventListener("click", function () { rollback(g.getAttribute("data-ver"), g.getAttribute("data-no")); }); });
      var th = $("#thread");
      if (th) th.scrollTop = th.scrollHeight;
      var comp = $("#composer");
      if (comp) comp.addEventListener("submit", function (e) {
        e.preventDefault();
        var v = comp.u.value.trim();
        if (!v) return;
        comp.querySelector("button").disabled = true;
        api(base + "/amendments", { method: "POST", body: { utterance: v } }).then(reload).catch(function (err) { fail(err); comp.querySelector("button").disabled = false; });
      });
    });
  }

  // ---------- SKILLS ----------
  function renderSkills() {
    return api("/api/skills").then(function (skills) {
      var rows = skills.length ? '<table class="table"><thead><tr><th>' + esc(T("col.skill")) + "</th><th>" + esc(T("col.current")) + "</th><th>" + esc(T("col.files")) + "</th><th>" + esc(T("col.used_by")) + "</th><th>" + esc(T("col.updated")) + '</th><th></th></tr></thead><tbody>' +
        skills.map(function (s) {
          return '<tr><td><div class="cell-main"><span class="ico">' + ic("skills", "sm") + "</span><div><strong>" + esc(s.name) + '</strong><small class="mono">' + esc(s.id) + '</small></div></div></td><td><span class="tag perm">v' + esc(s.version_no) + "</span></td><td>" + esc(s.files) + "</td><td>" + esc(plural(s.used_by, "tasks.count_one", "tasks.count_many")) +
            '</td><td class="nowrap">' + esc(when(s.updated_at)) + '</td><td class="right"><button class="btn sm" data-versions="' + esc(s.id) + '" data-name="' + esc(s.name) + '">' + ic("branch", "sm") + esc(T("sk.versions")) + "</button></td></tr>";
        }).join("") + "</tbody></table>" : emptyState("skills", T("sk.none"), T("sk.none_sub"));
      mount('<div class="page-head"><div><h1>' + esc(T("nav.skills")) + "</h1><p>" + esc(T("sk.sub")) + '</p></div><div class="actions"><button class="btn primary" id="sk-import">' + ic("upload", "sm") + esc(T("sk.import")) + "</button></div></div>" +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("skills") + "</span><h2>" + esc(T("sk.library")) + "</h2></div>" + rows + "</section>", "skills", [[T("nav.skills")]]);
      $("#sk-import").addEventListener("click", function () {
        modal('<div class="card-head"><span class="ico">' + ic("upload") + '</span><h2>' + esc(T("sk.import")) + '</h2></div><form id="ski"><label class="field"><span>' + esc(T("sk.dir")) + '</span><input name="dir" required placeholder="' + esc(T("sk.dir_ph")) + '"></label><label class="field"><span>' + esc(T("sk.name")) + '</span><input name="name" maxlength="64"></label><p class="hint">' + esc(T("sk.reimport")) + '</p><div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T("sk.import_btn")) + "</button></div></form>", function (root, close) {
          $("#ski", root).addEventListener("submit", function (e) {
            e.preventDefault();
            api("/api/skills/import", { method: "POST", body: { dir: e.target.dir.value, name: e.target.name.value } }).then(function () { close(); toast(T("toast.skill_imported")); renderSkills(); }).catch(fail);
          });
        });
      });
      $$("[data-versions]").forEach(function (b) {
        b.addEventListener("click", function () {
          var sid = b.getAttribute("data-versions");
          api("/api/skills/" + enc(sid) + "/versions").then(function (v) {
            modal('<div class="card-head"><span class="ico">' + ic("branch") + "</span><h2>" + esc(b.getAttribute("data-name")) + '</h2></div><table class="table"><tbody>' + v.versions.slice().reverse().map(function (x) {
              return "<tr><td><strong>v" + esc(x.no) + "</strong>" + (x.id === v.current ? ' <span class="tag perm">' + esc(T("ver.current")) + "</span>" : "") + '</td><td class="nowrap">' + esc(when(x.created_at)) + '</td><td class="right">' + (x.id === v.current ? "" : '<button class="btn sm" data-srb="' + esc(x.id) + '">' + esc(T("ver.rollback")) + "</button>") + "</td></tr>";
            }).join("") + '</tbody></table><div class="actions"><button class="btn ghost" data-close>' + esc(T("btn.close")) + "</button></div>", function (root, close) {
              $$("[data-srb]", root).forEach(function (x) { x.addEventListener("click", function () { api("/api/skills/" + enc(sid) + "/rollback", { method: "POST", body: { version_id: x.getAttribute("data-srb") } }).then(function () { close(); toast(T("toast.skill_rb")); renderSkills(); }).catch(fail); }); });
            });
          }).catch(fail);
        });
      });
    });
  }
  // ---------- BUTTONS ----------
  function renderButtons() {
    return Promise.all([api("/api/buttons"), api("/api/tasks")]).then(function (r) {
      var list = r[0], tasks = r[1], tname = {};
      tasks.forEach(function (t) { tname[t.id] = t.name; });
      var cards = list.map(function (b, i) {
        return '<div class="card qa fill" data-run="' + esc(b.id) + '"><span class="qa-ico">' + ic(btnIcon(b), "lg") + "</span><strong>" + esc(b.label) + "</strong><small>" + esc(b.task_id ? T("bt.runs_task", { name: tname[b.task_id] || b.task_id }) : T("bt.prompt", { p: b.prompt })) + '</small><div class="req-actions"><button class="btn sm" data-edit="' + i + '">' + esc(T("btn.edit")) + '</button><button class="btn sm danger" data-del="' + i + '">' + esc(T("btn.remove")) + '</button></div></div>';
      });
      cards.push('<button class="card qa tint" id="b-add"><span class="qa-ico">' + ic("plus", "lg") + "</span><strong>" + esc(T("bt.new")) + "</strong><small>" + esc(T("bt.new_sub")) + " " + ic("arrow", "sm") + "</small></button>");
      mount('<div class="page-head"><div><h1>' + esc(T("nav.buttons")) + "</h1><p>" + esc(T("bt.sub")) + '</p></div></div><div class="grid g4">' + cards.join("") + "</div>", "buttons", [[T("nav.buttons")]]);
      var save = function (nl) { return api("/api/buttons", { method: "PUT", body: nl }).then(function () { toast(T("toast.buttons_saved")); renderButtons(); }); };
      var edit = function (idx) {
        var b = idx == null ? { id: "", label: "", description: "", icon: "bolt", task_id: "", prompt: "" } : list[idx];
        modal('<div class="card-head"><span class="ico">' + ic("buttons") + "</span><h2>" + esc(T(idx == null ? "bt.new" : "bt.edit")) + '</h2></div><form id="bf">' +
          '<div class="form-row"><label class="field"><span>' + esc(T("bt.label")) + '</span><input name="label" required maxlength="40" value="' + esc(b.label) + '"></label><label class="field"><span>' + esc(T("bt.id")) + '</span><input name="bid" required pattern="[a-z0-9_\\-]{1,40}" value="' + esc(b.id) + '"' + (idx == null ? "" : " readonly") + "></label></div>" +
          '<label class="field"><span>' + esc(T("bt.desc")) + '</span><input name="description" maxlength="60" value="' + esc(b.description) + '"></label>' +
          '<div class="form-row"><label class="field"><span>' + esc(T("bt.icon")) + '</span><select name="icon">' + BUTTON_ICONS.map(function (x) { return '<option value="' + x + '"' + (x === b.icon ? " selected" : "") + ">" + x + "</option>"; }).join("") + '</select></label><label class="field"><span>' + esc(T("col.task")) + '</span><select name="task_id"><option value="">' + esc(T("bt.use_prompt")) + '</option>' +
          tasks.map(function (t) { return '<option value="' + esc(t.id) + '"' + (t.id === b.task_id ? " selected" : "") + ">" + esc(t.name) + "</option>"; }).join("") + "</select></label></div>" +
          '<label class="field"><span>' + esc(T("bt.prompt_label")) + '</span><textarea name="prompt" maxlength="8000">' + esc(b.prompt) + '</textarea></label><div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T("btn.save")) + "</button></div></form>", function (root, close) {
          $("#bf", root).addEventListener("submit", function (e) {
            e.preventDefault();
            var f = e.target, nb = { id: f.bid.value, label: f.label.value, description: f.description.value, icon: f.icon.value, task_id: f.task_id.value, prompt: f.task_id.value ? "" : f.prompt.value };
            var nl = list.slice();
            if (idx == null) nl.push(nb); else nl[idx] = nb;
            save(nl).then(close).catch(fail);
          });
        });
      };
      $("#b-add").addEventListener("click", function () { edit(null); });
      $$("[data-edit]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); edit(+b.getAttribute("data-edit")); }); });
      $$("[data-del]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); if (!confirm(T("bt.confirm_rm"))) return; var nl = list.slice(); nl.splice(+b.getAttribute("data-del"), 1); save(nl).catch(fail); }); });
      $$("[data-run]").forEach(function (c) { c.addEventListener("click", function (e) { if (e.target.closest("button")) return; runButton(c.getAttribute("data-run"), c); }); });
    });
  }

  // ---------- PLUGINS ----------
  function renderPlugins() {
    var kinds = [["terminal", T("pl.mcp"), T("pl.mcp_sub"), T("pl.none_inst")], ["bell", T("pl.notifiers"), T("pl.notifiers_sub"), T("pl.builtin")], ["globe", T("pl.triggers"), T("pl.triggers_sub"), T("pl.none_inst")]];
    mount('<div class="page-head"><div><h1>' + esc(T("nav.plugins")) + "</h1><p>" + esc(T("pl.sub")) + "</p></div></div>" +
      '<div class="grid g3">' + kinds.map(function (x) {
        return '<section class="card fill"><div class="card-head"><span class="ico">' + ic(x[0]) + "</span><h3>" + esc(x[1]) + '</h3></div><p class="grow muted">' + esc(x[2]) + '</p><div class="card-foot"><span class="pill plain">' + esc(x[3]) + "</span></div></section>";
      }).join("") + '</div><section class="card">' + emptyState("plugins", T("pl.empty"), T("pl.empty_sub")) + "</section>", "plugins", [[T("nav.plugins")]]);
    return Promise.resolve();
  }

  // ---------- RUNS ----------
  function renderRuns(params) {
    var st = params.get("status") || "", task = params.get("task") || "";
    var qs = "?limit=100" + (st ? "&status=" + enc(st) : "") + (task ? "&task_id=" + enc(task) : "");
    return api("/api/runs" + qs).then(function (runs) {
      var f = [["", T("runs.all_f")], ["running", T("st.running")], ["succeeded", T("st.succeeded")], ["failed", T("st.failed")]];
      mount('<div class="page-head"><div><h1>' + esc(T("nav.runs")) + "</h1><p>" + esc(T("runs.sub")) + '</p></div><div class="actions filters">' + f.map(function (x) { return '<button data-f="' + x[0] + '" class="' + (x[0] === st ? "active" : "") + '">' + esc(x[1]) + "</button>"; }).join("") + "</div></div>" +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("pulse") + "</span><h2>" + esc(task && runs[0] ? T("runs.for", { name: runs[0].task_name }) : T("runs.all")) + '</h2><span class="link muted">' + esc(T("runs.times_in", { tz: tzLabel() })) + "</span></div>" + runsTable(runs) + "</section>", "runs", [[T("nav.runs")]]);
      wireRows();
      $$("[data-f]").forEach(function (b) { b.addEventListener("click", function () { location.hash = "#/runs" + (b.getAttribute("data-f") ? "?status=" + b.getAttribute("data-f") : ""); }); });
      if (runs.some(function (r) { return r.status === "running" || r.status === "queued"; })) scheduleRefresh(location.hash, 3000);
    });
  }

  function logLine(text) {
    var m = /^\[(\d\d:\d\d:\d\d)\]\s(.*)$/.exec(text), ts = "", body = text;
    if (m) { ts = m[1]; body = m[2]; }
    var cls = /^result error|run finished: (failed|timeout|canceled)/.test(body) ? "ln-err" : /^result:|run finished: succeeded/.test(body) ? "ln-res" : /tool /.test(body) ? "ln-tool" : /^(init|run )/.test(body) ? "ln-sys" : "";
    return (ts ? '<span class="ts">' + esc(ts) + "</span>  " : "") + '<span class="' + cls + '">' + esc(body) + "</span>\n";
  }
  function renderRunDetail(id) {
    return api("/api/runs/" + enc(id)).then(function (r) {
      var live = r.status === "running" || r.status === "queued";
      mount('<div class="grid g4">' +
        '<div class="card stat"><small>' + esc(T("col.status")) + "</small><strong>" + statusPill(r.status) + "</strong><span>" + esc(triggerText(r.trigger)) + "</span></div>" +
        '<div class="card stat"><small>' + esc(T("col.duration")) + "</small><strong>" + esc(live ? T("rd.running") : dur(r.duration_ms)) + "</strong><span>" + esc(T("rd.started", { t: when(r.started_at || r.created_at) })) + "</span></div>" +
        '<div class="card stat"><small>' + esc(T("col.cost")) + "</small><strong>" + esc(usd(r.cost_usd, 4)) + "</strong><span>" + esc(T("rd.bucket", { b: r.bucket })) + "</span></div>" +
        '<div class="card stat"><small>Tokens</small><strong>' + esc(num(r.input_tokens + r.output_tokens)) + "</strong><span>" + esc(T("rd.in_out", { i: num(r.input_tokens), o: num(r.output_tokens) })) + "</span></div></div>" +
        '<div class="grid g-side"><section class="card"><div class="card-head"><span class="ico">' + ic("terminal") + '</span><h2>' + esc(T("rd.log")) + '</h2><span class="link" id="live-ind">' + (live ? '<span class="live">' + esc(T("rd.live")) + "</span>" : '<span class="muted">' + esc(T("rd.finished")) + "</span>") + '</span></div><div class="log" id="log" aria-live="polite"></div></section>' +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("info") + '</span><h3>' + esc(T("rd.details")) + '</h3></div><dl class="kv"><dt>' + esc(T("col.run")) + "</dt><dd><code>" + esc(r.id) + "</code></dd><dt>" + esc(T("col.task")) + "</dt><dd>" + (r.task_id ? '<a href="#/tasks/' + enc(r.task_id) + '">' + esc(r.task_name) + "</a>" + (r.version_no ? " · v" + esc(r.version_no) : "") : esc(T("run.adhoc"))) +
        "</dd><dt>" + esc(T("f.provider")) + "</dt><dd>" + esc(r.provider || "—") + "</dd><dt>" + esc(T("f.model")) + "</dt><dd>" + esc(r.model || "—") + "</dd><dt>" + esc(T("f.prompt")) + "</dt><dd>" + esc(r.prompt) + "</dd>" + (r.error ? "<dt>" + esc(T("rd.error")) + '</dt><dd class="err">' + esc(r.error) + "</dd>" : "") + "</dl></section></div>",
        "runs", [[T("nav.runs"), "#/runs"], [runTitle(r)]]);
      var log = $("#log");
      stopStream();
      bumpIdle();
      var es = new EventSource("/api/runs/" + enc(id) + "/events");
      state.es = es;
      es.onmessage = function (ev) {
        var atBottom = log.scrollTop + log.clientHeight >= log.scrollHeight - 24;
        log.insertAdjacentHTML("beforeend", logLine(ev.data));
        if (atBottom) log.scrollTop = log.scrollHeight;
      };
      es.addEventListener("end", function () { es.close(); state.es = null; if (live) setTimeout(function () { if (location.hash === "#/runs/" + id) renderRunDetail(id); }, 300); });
      es.addEventListener("idle", function () { es.close(); $("#live-ind").innerHTML = '<span class="muted">' + esc(T("rd.idle")) + "</span>"; });
      es.onerror = function () { es.close(); };
    });
  }
  // ---------- SPEND ----------
  var taskNames = {};
  function bucketName(b) { return b.scope === "global" ? T("sp.global") : b.scope === "bucket" ? (T("bucket." + b.scope_id) === "bucket." + b.scope_id ? cap(b.scope_id) : T("bucket." + b.scope_id)) : (taskNames[b.scope_id] || T("col.task") + " " + String(b.scope_id).slice(0, 12)); }
  function renderSpend() {
    return api("/api/tasks").then(function (ts) { (ts.tasks || ts || []).forEach(function (t) { taskNames[t.id] = t.name; }); }, function () {}).then(function () { return api("/api/spend"); }).then(function (d) {
      var s = d.summary, budgets = s.budgets || [];
      var global = budgets.filter(function (b) { return b.scope === "global" && b.period === "day"; })[0] || { max_usd: 0, max_tokens: 0 };
      var runsToday = d.top_tasks.reduce(function (a, t) { return a + t.runs; }, 0);
      var byDay = {}, days = [];
      d.days.forEach(function (x) { byDay[x.day] = x; });
      for (var i = 13; i >= 0; i--) { var k = dayKey(new Date(Date.now() - i * 864e5)); days.push({ day: k, usd: (byDay[k] || {}).usd || 0 }); }
      var mx = Math.max.apply(null, days.map(function (x) { return x.usd; }).concat([0.0001]));
      var pct = function (u, m) { return m ? Math.min(100, u / m * 100) : 0; };
      var cls = function (p) { return p >= 100 ? " bad" : p >= 80 ? " warn" : ""; };
      var gp = pct(s.total_usd, global.max_usd);
      mount('<div class="page-head"><div><h1>' + esc(T("nav.spend")) + "</h1><p>" + esc(T("sp.sub", { tz: s.timezone || state.tz })) + "</p></div></div>" +
        '<div class="grid g4">' +
        '<div class="card stat"><small>' + esc(T("time.today")) + "</small><strong>" + esc(usd(s.total_usd, 4)) + "</strong><span>" + esc(global.max_usd ? T("sp.pct_of", { p: Math.round(gp), m: usd(global.max_usd) }) : T("sp.no_cap")) + "</span></div>" +
        '<div class="card stat"><small>' + esc(T("sp.remaining")) + "</small><strong>" + esc(global.max_usd ? usd(Math.max(0, global.max_usd - s.total_usd)) : "∞") + "</strong><span>" + esc(T("sp.global_daily")) + "</span></div>" +
        '<div class="card stat"><small>Tokens</small><strong>' + esc(num(s.total_tokens)) + "</strong><span>" + esc(global.max_tokens ? T("sp.tok_cap", { n: num(global.max_tokens) }) : T("sp.no_tok_cap")) + "</span></div>" +
        '<div class="card stat"><small>' + esc(T("col.runs_today")) + "</small><strong>" + esc(runsToday) + "</strong><span>" + esc(plural(d.top_tasks.length, "sp.sources_one", "sp.sources_many")) + "</span></div></div>" +
        '<div class="grid g2"><section class="card fill"><div class="card-head"><span class="ico">' + ic("receipt") + '</span><h2>' + esc(T("sp.last14")) + '</h2><span class="link muted">' + esc(T("sp.peak", { v: usd(mx, 4) })) + '</span></div><div class="bars grow">' + days.map(function (x, j) {
          return '<div class="bar' + (j === 13 ? " today" : "") + '" title="' + esc(x.day + " · " + usd(x.usd, 4)) + '"><span data-h="' + (x.usd / mx * 100) + '"></span><small>' + esc(x.day.slice(8)) + "</small></div>";
        }).join("") + "</div></section>" +
        '<section class="card fill"><div class="card-head"><span class="ico">' + ic("wallet") + '</span><h2>' + esc(T("sp.budgets")) + '</h2><a class="link" href="#/settings">' + esc(T("btn.edit")) + " " + ic("arrow", "sm") + '</a></div><div class="grow">' + budgets.map(function (b) {
          var p = pct(b.used_usd, b.max_usd);
          return '<div class="budget-row"><div><strong>' + esc(bucketName(b)) + '</strong> <span class="muted">· ' + esc(T("sp.per_" + b.period)) + '</span></div><div class="right">' + esc(usd(b.used_usd, 4)) + ' <span class="muted">/ ' + esc(usd(b.max_usd)) + '</span></div><div class="progress' + cls(p) + '"><span data-w="' + p + '"></span></div></div>';
        }).join("") + "</div></section></div>" +
        '<div class="grid g2"><section class="card flush fill"><div class="card-head"><span class="ico">' + ic("tasks") + "</span><h2>" + esc(T("sp.top")) + "</h2></div>" + (d.top_tasks.length ? '<table class="table"><thead><tr><th>' + esc(T("col.source")) + "</th><th>" + esc(T("nav.runs")) + '</th><th class="right">' + esc(T("col.cost")) + "</th></tr></thead><tbody>" + d.top_tasks.map(function (t) { return "<tr><td><strong>" + esc(t.task_id ? t.name : T("run.adhoc_paren")) + "</strong></td><td>" + esc(t.runs) + '</td><td class="right"><strong>' + esc(usd(t.usd, 4)) + "</strong></td></tr>"; }).join("") + "</tbody></table>" : emptyState("receipt", T("sp.none_today"), "")) + "</section>" +
        '<section class="card flush fill"><div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>" + esc(T("sp.events")) + "</h2></div>" + (d.events.length ? '<table class="table"><thead><tr><th>' + esc(T("sp.when")) + "</th><th>" + esc(T("sp.scope")) + "</th><th>" + esc(T("sp.detail")) + "</th></tr></thead><tbody>" + d.events.map(function (e) { return '<tr><td class="nowrap">' + esc(when(e.at)) + '</td><td><span class="pill bad">' + esc(T("sp.scope_" + e.scope)) + "</span></td><td>" + esc(e.detail) + "<small>" + esc(T("sp.action", { a: T("act." + e.action) === "act." + e.action ? e.action : T("act." + e.action) })) + "</small></td></tr>"; }).join("") + "</tbody></table>" : emptyState("shield", T("sp.no_breach"), T("sp.no_breach_sub"))) + "</section></div>",
        "spend", [[T("nav.spend")]]);
    });
  }

  // ---------- SETTINGS ----------
  function renderSettings() {
    return api("/api/settings").then(function (st) {
      var g = (st.budgets || []).filter(function (b) { return b.scope === "global" && b.period === "day"; })[0] || {};
      var buckets = (st.budgets || []).filter(function (b) { return b.scope === "bucket"; });
      // title/sub are trusted dictionary strings; "{cmd}" is replaced with a <code> chip after escaping.
      var row = function (title, sub, right, cmd) { return '<div class="set-row"><div class="lbl"><strong>' + esc(title) + "</strong><small>" + esc(sub).replace("{cmd}", cmd ? "<code>" + esc(cmd) + "</code>" : "") + "</small></div>" + right + "</div>"; };
      mount('<div class="page-head"><div><h1>' + esc(T("nav.settings")) + "</h1><p>" + esc(T("set.sub")) + "</p></div></div>" +
        '<div class="grid g2"><section class="card fill"><div class="card-head"><span class="ico">' + ic("globe") + "</span><h2>WebUI</h2></div>" +
        row(T("set.webui_on"), T("set.webui_on_sub"), '<button class="switch' + (st.webui_enabled ? " on" : "") + '" id="sw-webui" aria-label="' + esc(T("set.webui_on")) + '"></button>', "agentctl webui on") +
        row(T("set.addr"), T(st.allow_lan ? "set.lan_on" : "set.lan_off"), "<code>" + esc(st.addr) + "</code>") +
        row("HTTPS", T("set.https_sub"), '<span class="pill ' + (st.tls ? "ok" : "plain") + '">' + esc(T(st.tls ? "set.enabled" : "set.off")) + "</span>") +
        row(T("set.idle"), T("set.idle_sub"), "<span>" + esc(T("set.min", { n: st.idle_minutes })) + "</span>") + "</section>" +
        '<section class="card fill"><div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>" + esc(T("set.security")) + "</h2></div>" +
        row(T("login.password"), T("set.pw_sub"), '<span class="pill ok">' + esc(T("set.set")) + "</span>", "agentctl auth set-password") +
        row(T("set.totp"), T("set.totp_sub"), '<span class="pill ' + (st.totp_enabled ? "ok" : "plain") + '">' + esc(T(st.totp_enabled ? "set.on" : "set.off")) + "</span>", "agentctl auth totp-setup") +
        row(T("set.cookies"), T("set.cookies_sub"), "") +
        row(T("set.tz"), T("set.tz_sub"), "<span>" + esc(tzLabel()) + "</span>") +
        row(T("set.language"), T("set.language_sub"), langSelect("set-lang")) +
        row(T("set.session"), T("set.session_sub"), '<button class="btn sm" id="btn-logout">' + ic("logout", "sm") + esc(T("set.signout")) + "</button>") + "</section></div>" +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("wallet") + '</span><h2>' + esc(T("sp.budgets")) + '</h2><span class="link muted">' + esc(T("set.bud_note")) + '</span></div><form id="bud" class="grid g4">' +
        '<label class="field"><span>' + esc(T("set.g_usd")) + "</span>" + '<input name="g_usd" type="number" step="0.01" min="0" value="' + esc(g.max_usd || "") + '"></label>' +
        '<label class="field"><span>' + esc(T("set.g_tok")) + "</span>" + '<input name="g_tok" type="number" step="1000" min="0" value="' + esc(g.max_tokens || "") + '"></label>' +
        buckets.map(function (b) { return '<label class="field"><span>' + esc(T("set.b_usd", { b: bucketName(b) })) + '</span><input name="b_' + esc(b.scope_id) + '" type="number" step="0.01" min="0" value="' + esc(b.max_usd || "") + '"></label>'; }).join("") +
        '<div class="field"><span>&nbsp;</span><button class="btn primary">' + esc(T("set.save_bud")) + '</button></div></form></section>' +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("key") + '</span><h2>' + esc(T("set.providers")) + '</h2><span class="link muted">' + esc(T("set.default_p", { p: st.default_provider })) + '</span></div><table class="table"><thead><tr><th>' + esc(T("set.name")) + "</th><th>Base URL</th><th>" + esc(T("f.model")) + "</th><th>" + esc(T("set.cred")) + "</th></tr></thead><tbody>" +
        st.providers.map(function (p) { return "<tr><td><strong>" + esc(p.name) + "</strong></td><td><code>" + esc(p.base_url || T("td.default")) + "</code></td><td>" + esc(p.model || T("td.default")) + '</td><td><span class="pill ' + (p.key_set ? "ok" : "plain") + '">' + esc(T(p.key_set ? "set.configured" : "set.missing")) + "</span><small>" + esc(T("set.env_hidden", { e: p.auth_env || "—" })) + "</small></td></tr>"; }).join("") + "</tbody></table></section>",
        "settings", [[T("nav.settings")]]);
      $("#set-lang").addEventListener("change", function (e) { setLanguage(e.target.value); });
      $("#btn-logout").addEventListener("click", function () {
        api("/api/logout", { method: "POST", body: {} }).catch(function () {}).then(function () { state.csrf = ""; showLogin(); });
      });
      $("#sw-webui").addEventListener("click", function () {
        if (!confirm(T("set.confirm_off"))) return;
        api("/api/webui", { method: "POST", body: { enabled: false } }).then(function () { toast(T("toast.webui_off")); }).catch(fail);
      });
      $("#bud").addEventListener("submit", function (e) {
        e.preventDefault();
        var f = e.target, reqs = [api("/api/budgets", { method: "POST", body: { scope: "global", period: "day", max_usd: parseFloat(f.g_usd.value) || 0, max_tokens: parseInt(f.g_tok.value, 10) || 0 } })];
        buckets.forEach(function (b) { reqs.push(api("/api/budgets", { method: "POST", body: { scope: "bucket", scope_id: b.scope_id, bucket: b.scope_id, period: "day", max_usd: parseFloat(f["b_" + b.scope_id].value) || 0 } })); });
        Promise.all(reqs).then(function () { toast(T("toast.bud_saved")); }).catch(fail);
      });
    });
  }

  // ---------- router ----------
  function route() {
    stopStream();
    clearTimeout(scheduleRefresh._t);
    $("#modal-root").innerHTML = "";
    var h = location.hash.replace(/^#\/?/, "");
    var qi = h.indexOf("?"), params = new URLSearchParams(qi >= 0 ? h.slice(qi + 1) : "");
    var parts = (qi >= 0 ? h.slice(0, qi) : h).split("/").filter(Boolean).map(decodeURIComponent);
    var p;
    switch (parts[0]) {
      case undefined: p = renderHome(); break;
      case "tasks": p = parts[1] ? renderTaskDetail(parts[1]) : renderTasks(); break;
      case "skills": p = renderSkills(); break;
      case "buttons": p = renderButtons(); break;
      case "plugins": p = renderPlugins(); break;
      case "runs": p = parts[1] ? renderRunDetail(parts[1]) : renderRuns(params); break;
      case "spend": p = renderSpend(); break;
      case "settings": p = renderSettings(); break;
      default: p = renderHome();
    }
    p.catch(function (e) { if (!e.auth) { renderTopbar([]); page().innerHTML = '<section class="card">' + emptyState("info", T("err.generic"), e.message) + "</section>"; } });
    if (parts[0]) api("/api/overview").then(renderStatus).catch(function () {});
  }
  window.addEventListener("hashchange", route);

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
      $("#login").classList.add("hidden");
      $("#shell").classList.remove("hidden");
      renderShell();
      route();
    }).catch(function () { showLogin(); });
  }
  var storedLang = "auto";
  try { storedLang = localStorage.getItem("tb_lang") || "auto"; } catch (e) {}
  loadLang(storedLang).then(boot);
})();
