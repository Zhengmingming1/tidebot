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
    folder: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
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
  function btnIcon(b) { return P[b.icon] ? b.icon : "bolt"; }

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
        if (!res.ok) { var e2 = new Error((data && data.error) || res.statusText); e2.status = res.status; e2.data = data; throw e2; }
        return data;
      });
    });
  }
  // Toasts: success fades after 3s, errors stay until closed, undo toasts last 8s with a button.
  function toastBox() {
    var box = $("#toasts");
    if (!box) { box = document.createElement("div"); box.id = "toasts"; box.className = "toasts"; box.setAttribute("aria-live", "polite"); document.body.appendChild(box); }
    return box;
  }
  function toast(msg, bad, undo) {
    var el = document.createElement("div");
    el.className = "toast" + (bad ? " bad" : "") + (undo ? " undo" : "");
    el.setAttribute("role", bad ? "alert" : "status");
    el.innerHTML = '<span class="t-msg"></span>' + (undo ? '<button class="t-undo">' + esc(T("t.undo")) + "</button>" : "") + (bad || undo ? '<button class="t-x" aria-label="' + esc(T("btn.close")) + '">' + ic("x", "sm") + "</button>" : "");
    $(".t-msg", el).textContent = msg;
    var box = toastBox(), gone = false;
    var close = function () { if (gone) return; gone = true; el.classList.add("out"); setTimeout(function () { el.remove(); }, 200); };
    if (undo) $(".t-undo", el).addEventListener("click", function () { close(); undo(); });
    var x = $(".t-x", el); if (x) x.addEventListener("click", close);
    box.appendChild(el);
    while (box.children.length > 4) box.firstChild.remove();
    if (!bad) setTimeout(close, undo ? 10000 : 3000);
    return close;
  }
  function toastUndo(msg, undo) { return toast(msg, false, undo); }
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
    $("#btn-refresh").addEventListener("click", function () { refresh(); });
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
  // Modal: role=dialog, Esc closes, Tab stays inside, focus returns to the opener.
  function modal(html, onMount) {
    var root = $("#modal-root"), opener = document.activeElement;
    root.innerHTML = '<div class="modal-bg"><div class="card modal" role="dialog" aria-modal="true" tabindex="-1">' + html + "</div></div>";
    var box = $(".modal", root), h = $("h2", box);
    if (h) { h.id = "modal-title"; box.setAttribute("aria-labelledby", "modal-title"); }
    var close = function () {
      if (!root.contains(box)) return;
      root.innerHTML = ""; document.removeEventListener("keydown", onKey, true);
      if (opener && opener.focus && document.contains(opener)) opener.focus();
    };
    var onKey = function (e) {
      if (!root.contains(box)) { document.removeEventListener("keydown", onKey, true); return; }
      if (e.key === "Escape") { e.preventDefault(); close(); return; }
      if (e.key !== "Tab") return;
      var f = $$('a[href],button:not([disabled]),input:not([disabled]):not(.hidden),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])', box).filter(function (x) { return x.offsetParent !== null; });
      if (!f.length) return;
      if (e.shiftKey && document.activeElement === f[0]) { e.preventDefault(); f[f.length - 1].focus(); }
      else if (!e.shiftKey && document.activeElement === f[f.length - 1]) { e.preventDefault(); f[0].focus(); }
    };
    document.addEventListener("keydown", onKey, true);
    $(".modal-bg", root).addEventListener("mousedown", function (e) { if (e.target.classList.contains("modal-bg")) close(); });
    $$("[data-close]", root).forEach(function (b) { b.addEventListener("click", close); });
    if (onMount) onMount(root, close);
    var first = $("[autofocus]", box) || $("input:not([type=hidden]):not(.hidden),textarea,select", box);
    (first && first.offsetParent !== null ? first : box).focus();
  }
  // confirmDialog({title, body, ok, danger, typeName}) resolves true only when confirmed.
  function confirmDialog(o) {
    return new Promise(function (resolve) {
      var done = false;
      modal('<div class="card-head"><span class="ico' + (o.danger ? " danger" : "") + '">' + ic(o.danger ? "trash" : "info") + "</span><h2>" + esc(o.title) + "</h2></div>" +
        (o.body ? '<p class="confirm-body">' + esc(o.body) + "</p>" : "") +
        (o.typeName ? '<label class="field"><span>' + esc(T("cf.type_name", { n: o.typeName })) + '</span><input id="cf-name" autocomplete="off" spellcheck="false"></label>' : "") +
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
  function wireRows(root) { $$("[data-href]", root || page()).forEach(function (el) { el.addEventListener("click", function (e) { if (e.target.closest("a,button")) return; location.hash = el.getAttribute("data-href"); }); }); }

  function runsTable(runs, rerun) {
    if (!runs.length) return emptyState("runs", T("runs.none"), T("runs.none_sub"));
    return '<table class="table"><thead><tr><th>' + esc(T("col.run")) + "</th><th>" + esc(T("col.agent_task")) + "</th><th>" + esc(T("col.status")) + "</th><th>" + esc(T("col.started")) + "</th><th>" + esc(T("col.duration")) + '</th><th class="right">' + esc(T("col.cost")) + '</th><th></th></tr></thead><tbody>' +
      runs.map(function (r) {
        var running = r.status === "running";
        return '<tr class="click" data-href="#/runs/' + enc(r.id) + '"><td><div class="cell-main"><span class="ico">' + ic(runIcon(r), "sm") + "</span><div><strong>" + esc(runTitle(r)) + "</strong><small>" + esc(triggerText(r.trigger)) + "</small></div></div></td>" +
          "<td><strong>" + esc(r.task_name || T("run.adhoc")) + "</strong><small>" + esc(r.provider || "—") + (r.version_no ? " · v" + esc(r.version_no) : "") + "</small></td>" +
          "<td>" + statusPill(r.status) + '</td><td class="nowrap">' + esc(when(r.started_at || r.created_at)) + '</td><td class="nowrap">' + esc(running ? dur(Date.now() - new Date(r.started_at).getTime()) : dur(r.duration_ms)) +
          '</td><td class="right"><strong>' + esc(usd(r.cost_usd)) + '</strong></td><td class="right nowrap">' + (rerun && /failed|timeout|canceled/.test(r.status) ? '<button class="btn sm" data-rerun="' + esc(r.id) + '">' + ic("refresh", "sm") + esc(T("runs.rerun")) + "</button> " : "") + '<a class="icon-btn" href="#/runs/' + enc(r.id) + '" aria-label="' + esc(T("run.open")) + '">' + ic("more", "sm") + "</a></td></tr>";
      }).join("") + "</tbody></table>";
  }
  // ---------- HOME ----------
  function runButton(id, el) {
    if (el) el.classList.add("busy");
    return api("/api/buttons/" + enc(id) + "/run", { method: "POST", body: {} })
      .then(function (r) { toast(T("toast.started", { name: r.started })); setTimeout(refresh, 700); })
      .catch(fail)
      .then(function () { if (el) el.classList.remove("busy"); });
  }
  // ---------- live polling: pauses while the tab is hidden, catches up when it returns ----------
  var poller = { t: null, fn: null, ms: 0, hash: "" };
  function livePoll(fn, ms) {
    stopPoll();
    poller.fn = fn; poller.ms = ms; poller.hash = location.hash || "#/";
    var tick = function () {
      poller.t = null;
      if ((location.hash || "#/") !== poller.hash || !poller.fn) { stopPoll(); return; }
      if (document.hidden) return; // resumed by visibilitychange
      Promise.resolve(poller.fn()).catch(function () {}).then(function () { if (poller.fn && !poller.t && !document.hidden) poller.t = setTimeout(tick, poller.ms); });
    };
    poller.tick = tick;
    poller.t = setTimeout(tick, ms);
  }
  function stopPoll() { clearTimeout(poller.t); poller.t = null; poller.fn = null; }
  document.addEventListener("visibilitychange", function () {
    if (!document.hidden && poller.fn && !poller.t) poller.tick();
    if (!document.hidden && state.esPaused) { state.esPaused = false; if (state.esResume) state.esResume(); }
  });

  // ---------- HOME ----------
  function beatsBar(beats) {
    var cells = [];
    for (var i = 0; i < 20 - beats.length; i++) cells.push('<span class="hb hb-none"></span>');
    beats.forEach(function (b) {
      var cls = b.status === "succeeded" ? "ok" : (b.status === "running" || b.status === "queued") ? "run" : b.status === "skipped" ? "skip" : "bad";
      var tip = when(b.at) + " · " + T("st." + b.status) + " · " + dur(b.duration_ms) + " · " + usd(b.cost_usd, 3);
      cells.push('<a class="hb ' + cls + '" href="#/runs/' + enc(b.id) + '" data-tip="' + esc(tip) + '" aria-label="' + esc(tip) + '"></a>');
    });
    return '<div class="hbar">' + cells.join("") + "</div>";
  }
  function renderHome() {
    return Promise.all([api("/api/overview"), api("/api/dashboard")]).then(function (r) {
      var o = r[0], d = r[1];
      renderStatus(o);
      var qa = (o.buttons || []).slice(0, 5).map(function (b) {
        return '<button class="card qa' + (b.color ? " colored" : "") + '" data-color="' + esc(b.color || "") + '" data-btn="' + esc(b.id) + '"><span class="qa-ico">' + ic(btnIcon(b), "lg") + "</span><strong>" + esc(b.label) + "</strong><small>" + esc(b.description || "") + "</small></button>";
      });
      qa.push('<a class="card qa add" href="#/buttons" aria-label="' + esc(T("home.add_button")) + '"><span class="qa-ico">' + ic("plus", "lg") + "</span><small>" + esc(T("home.add_button")) + "</small></a>");
      qa.push('<button class="card qa tint" id="qa-new"><span class="qa-ico">' + ic("sparkle", "lg") + "</span><strong>" + esc(T("home.quick")) + "</strong><small>" + esc(T("home.quick_sub")) + " " + ic("arrow", "sm") + "</small></button>");
      var noTasks = !d.tasks.length;
      mount('<div class="grid g4 kpis" id="kpis"></div>' +
        '<div class="qa-row">' + qa.join("") + "</div>" +
        (noTasks ? '<section class="card">' + '<div class="empty">' + ic("tasks") + "<strong>" + esc(T("home.empty_title")) + "</strong>" + esc(T("home.empty_sub")) + '<div class="mt"><button class="btn primary" id="home-new">' + ic("plus", "sm") + esc(T("tf.new")) + "</button></div></div></section>" : "") +
        '<div class="grid g2"><section class="card fill"><div class="card-head"><span class="ico">' + ic("pulse") + "</span><h2>" + esc(T("home.running")) + '</h2><span class="link muted" id="run-n"></span></div><div id="running" class="grow"></div></section>' +
        '<section class="card fill"><div class="card-head"><span class="ico">' + ic("calendar") + "</span><h2>" + esc(T("home.next24")) + '</h2><a class="link" href="#/tasks">' + esc(T("home.view_tasks")) + " " + ic("arrow", "sm") + '</a></div><div id="next24" class="grow"></div><div class="card-foot">' + ic("info", "sm") + "<span>" + esc(T("home.times_in", { tz: tzLabel() })) + "</span></div></section></div>" +
        (noTasks ? "" : '<section class="card"><div class="card-head"><span class="ico">' + ic("runs") + "</span><h2>" + esc(T("home.health")) + '</h2><span class="link muted">' + esc(T("home.health_sub")) + '</span></div><div id="beats"></div></section>') +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("runs") + "</span><h2>" + esc(T("home.recent")) + '</h2><a class="link" href="#/runs">' + esc(T("home.view_runs")) + " " + ic("arrow", "sm") + '</a></div><div id="recent"></div></section>', "home", []);
      applyColors(page());
      var paint = function (o, d) {
        var k = d.kpi, rate = k.runs_today ? Math.round(k.succeeded_today / k.runs_today * 100) + "%" : "—", left = k.budget_usd ? Math.max(0, k.budget_usd - k.spent_today) : null;
        var bp = k.budget_usd ? Math.min(100, k.spent_today / k.budget_usd * 100) : 0;
        $("#kpis").innerHTML = [[T("home.k_runs"), num(k.runs_today), T("home.k_runs_sub", { n: k.succeeded_today })], [T("home.k_rate"), rate, T("time.today")], [T("home.k_spent"), usd(k.spent_today, 2), k.budget_usd ? T("home.pct_budget", { p: Math.round(bp) }) : T("home.no_budget")], [T("home.k_left"), left == null ? "∞" : usd(left, 2), T("sp.global_daily")]].map(function (x, i) {
          return '<div class="card stat"><small>' + esc(x[0]) + '</small><strong class="num">' + esc(x[1]) + "</strong><span>" + esc(x[2]) + "</span>" + (i === 3 && k.budget_usd ? '<div class="progress' + (bp >= 100 ? " bad" : bp >= 80 ? " warn" : "") + '"><span data-w="' + bp + '"></span></div>' : "") + "</div>";
        }).join("");
        $("#run-n").textContent = d.running.length ? T("home.n_running", { n: d.running.length }) : "";
        $("#running").innerHTML = d.running.length ? '<ul class="live-list">' + d.running.map(function (x) {
          return '<li data-href="#/runs/' + enc(x.id) + '"><span class="spin" aria-hidden="true"></span><div><strong>' + esc(x.task_name || T("run.adhoc")) + "</strong><small>" + esc(x.status === "queued" ? T("st.queued") : T("home.started_ago", { t: ago(x.started_at || x.created_at) })) + '</small></div><span class="num muted" data-since="' + esc(x.started_at || "") + '">' + esc(x.started_at ? dur(Date.now() - new Date(x.started_at).getTime()) : "—") + '</span><strong class="num">' + esc(usd(x.cost_usd, 3)) + "</strong></li>";
        }).join("") + "</ul>" : emptyState("checkc", T("home.idle"), T("home.idle_sub"));
        $("#next24").innerHTML = d.next.length ? '<ul class="timeline">' + d.next.map(function (s) {
          return '<li><span class="tl-dot"></span><span class="tl-time">' + esc(clock(s.at)) + '</span><div class="tl-name"><strong>' + esc(s.task_name) + "</strong><small>" + esc(relDay(s.at) + " · " + cronHuman(s.cron)) + '</small></div><a class="link" href="#/tasks/' + enc(s.task_id) + '">' + ic("chev", "sm") + "</a></li>";
        }).join("") + "</ul>" : emptyState("calendar", T("home.no_sched"), T("home.no_sched_sub"));
        if ($("#beats")) $("#beats").innerHTML = '<div class="beats">' + d.tasks.map(function (t) {
          return '<div class="beat-row"><a href="#/tasks/' + enc(t.id) + '" class="bn">' + esc(t.name) + (t.status === "paused" ? ' <span class="pill warn sm">' + esc(T("task.paused")) + "</span>" : "") + "</a>" + beatsBar(t.beats) + "</div>";
        }).join("") + "</div>";
        $("#recent").innerHTML = runsTable((o.runs || []).slice(0, 6));
        applySizes(page()); wireRows(page());
      };
      paint(o, d);
      $$("[data-btn]").forEach(function (b) { b.addEventListener("click", function () { runButton(b.getAttribute("data-btn"), b); }); });
      $("#qa-new").addEventListener("click", quickRunModal);
      if ($("#home-new")) $("#home-new").addEventListener("click", function () { taskFormModal(null); });
      var ticks = 0;
      livePoll(function () {
        ticks++;
        // live elapsed counters every tick; data every tick while something runs, else every 5s
        $$("[data-since]").forEach(function (el) { var t = el.getAttribute("data-since"); if (t) el.textContent = dur(Date.now() - new Date(t).getTime()); });
        if ($("#running .spin") || ticks % 5 === 0) {
          return Promise.all([api("/api/overview"), api("/api/dashboard")]).then(function (r2) { if ((location.hash || "#/") === "#/") paint(r2[0], r2[1]); });
        }
      }, 1000);
    });
  }
  function scheduleRefresh(hash, ms) {
    clearTimeout(scheduleRefresh._t);
    scheduleRefresh._t = setTimeout(function () { if ((location.hash || "#/") === hash && !$("#modal-root").innerHTML) refresh(); }, ms);
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
  var taskView = { q: "", status: "", skill: "", sort: "name", dir: 1, sel: {} };
  function renderTasks() {
    return api("/api/tasks").then(function (tasks) {
      var v = taskView, skills = {};
      tasks.forEach(function (t) { (t.skills || []).forEach(function (k) { skills[k] = 1; }); });
      v.sel = {};
      var sorters = {
        name: function (t) { return t.name.toLowerCase(); }, schedule: function (t) { return t.next_run_at || "~"; }, status: function (t) { return t.status; },
        version: function (t) { return t.version_no; }, last: function (t) { return t.last_status || ""; }, today: function (t) { return t.runs_today; }
      };
      var th = function (k, label) { return '<th><button class="th-sort' + (v.sort === k ? " on" : "") + '" data-sort="' + k + '" aria-sort="' + (v.sort === k ? (v.dir > 0 ? "ascending" : "descending") : "none") + '">' + esc(label) + (v.sort === k ? '<span class="arr">' + (v.dir > 0 ? "↑" : "↓") + "</span>" : "") + "</button></th>"; };
      mount('<div class="page-head"><div><h1>' + esc(T("nav.tasks")) + "</h1><p>" + esc(plural(tasks.length, "tasks.count_one", "tasks.count_many") + " · " + T("tasks.sub")) + '</p></div><div class="actions"><button class="btn primary" id="t-new">' + ic("plus", "sm") + esc(T("tf.new")) + "</button></div></div>" +
        '<section class="card flush"><div class="toolbar"><label class="search-in">' + ic("search", "sm") + '<input id="tq" type="search" maxlength="100" placeholder="' + esc(T("tl.search")) + '" value="' + esc(v.q) + '"></label>' +
        '<div class="filters" role="group">' + [["", T("runs.all_f")], ["active", T("task.active")], ["paused", T("task.paused")]].map(function (x) { return '<button data-st="' + x[0] + '" class="' + (x[0] === v.status ? "active" : "") + '">' + esc(x[1]) + "</button>"; }).join("") + "</div>" +
        '<select id="tsk" aria-label="' + esc(T("bt.skill")) + '"><option value="">' + esc(T("tl.any_skill")) + "</option>" + Object.keys(skills).sort().map(function (k) { return opt(k, k, k === v.skill); }).join("") + "</select>" +
        '<span class="grow"></span><span class="muted" id="tcount"></span></div>' +
        '<div class="batchbar hidden" id="tbatch"><strong id="tbn"></strong><button class="btn sm" data-batch="pause">' + ic("pause", "sm") + esc(T("td.pause")) + '</button><button class="btn sm" data-batch="resume">' + ic("play", "sm") + esc(T("td.resume")) + '</button><button class="btn sm danger" data-batch="delete">' + ic("trash", "sm") + esc(T("td.delete")) + '</button><button class="btn sm ghost" id="tclear">' + esc(T("tl.clear")) + "</button></div>" +
        '<table class="table"><thead><tr><th class="ck"><input type="checkbox" id="tall" aria-label="' + esc(T("tl.select_all")) + '"></th>' + th("name", T("col.task")) + th("schedule", T("col.schedule")) + th("status", T("col.status")) + th("version", T("col.version")) + th("last", T("col.last_run")) + th("today", T("col.runs_today")) + '<th></th></tr></thead><tbody id="tbody"></tbody></table></section>', "tasks", [[T("nav.tasks")]]);
      var body = $("#tbody");
      function rows() {
        var q = v.q.trim().toLowerCase(), key = sorters[v.sort];
        var list = tasks.filter(function (t) {
          return (!v.status || t.status === v.status) && (!v.skill || (t.skills || []).indexOf(v.skill) >= 0) && (!q || (t.name + " " + (t.goal || "") + " " + t.prompt).toLowerCase().indexOf(q) >= 0);
        }).sort(function (a, b) { var x = key(a), y = key(b); return (x < y ? -1 : x > y ? 1 : 0) * v.dir; });
        $("#tcount").textContent = T("tl.showing", { n: list.length, total: tasks.length });
        body.innerHTML = list.length ? list.map(function (t) {
          return '<tr class="click" data-href="#/tasks/' + enc(t.id) + '"><td class="ck"><input type="checkbox" data-pick="' + esc(t.id) + '"' + (v.sel[t.id] ? " checked" : "") + ' aria-label="' + esc(t.name) + '"></td><td><div class="cell-main"><span class="ico">' + ic(runIcon({ task_name: t.name }), "sm") + "</span><div><strong>" + esc(t.name) + "</strong><small>" + esc(t.goal || t.prompt) + "</small></div></div></td>" +
            '<td class="nowrap">' + esc(cronHuman(t.cron)) + (t.next_run_at && t.status === "active" ? "<small>" + esc(T("tl.next", { t: when(t.next_run_at) })) + "</small>" : "") + "</td><td>" + taskPill(t.status) + '</td><td><span class="tag">v' + esc(t.version_no) + "</span></td><td>" + (t.last_status ? statusPill(t.last_status) : '<span class="muted">' + esc(T("tasks.never")) + "</span>") + '</td><td class="num">' + esc(t.runs_today) + '</td><td class="right"><span class="muted">' + ic("chev", "sm") + "</span></td></tr>";
        }).join("") : '<tr><td colspan="8">' + (tasks.length ? emptyState("search", T("tl.no_match"), T("tl.no_match_sub")) : emptyState("tasks", T("tasks.none"), T("tasks.none_sub"))) + "</td></tr>";
        wireRows(body);
        $$("[data-pick]", body).forEach(function (c) {
          c.addEventListener("click", function (e) { e.stopPropagation(); });
          c.addEventListener("change", function () { if (c.checked) v.sel[c.getAttribute("data-pick")] = 1; else delete v.sel[c.getAttribute("data-pick")]; batch(); });
        });
        batch();
      }
      function batch() {
        var n = Object.keys(v.sel).length;
        $("#tbatch").classList.toggle("hidden", !n);
        $("#tbn").textContent = T("tl.selected", { n: n });
        var boxes = $$("[data-pick]", body);
        $("#tall").checked = boxes.length > 0 && boxes.every(function (c) { return c.checked; });
      }
      var qt;
      $("#tq").addEventListener("input", function (e) { clearTimeout(qt); qt = setTimeout(function () { v.q = e.target.value; rows(); }, 120); });
      $$("[data-st]").forEach(function (b) { b.addEventListener("click", function () { v.status = b.getAttribute("data-st"); $$("[data-st]").forEach(function (x) { x.classList.toggle("active", x === b); }); rows(); }); });
      $("#tsk").addEventListener("change", function (e) { v.skill = e.target.value; rows(); });
      $$("[data-sort]").forEach(function (b) { b.addEventListener("click", function () { var k = b.getAttribute("data-sort"); if (v.sort === k) v.dir = -v.dir; else { v.sort = k; v.dir = 1; } renderTasks(); }); });
      $("#tall").addEventListener("change", function (e) { $$("[data-pick]", body).forEach(function (c) { c.checked = e.target.checked; if (c.checked) v.sel[c.getAttribute("data-pick")] = 1; else delete v.sel[c.getAttribute("data-pick")]; }); batch(); });
      $("#tclear").addEventListener("click", function () { v.sel = {}; rows(); });
      $$("[data-batch]").forEach(function (b) { b.addEventListener("click", function () {
        var act = b.getAttribute("data-batch"), ids = Object.keys(v.sel);
        var go = function () {
          api("/api/tasks/batch", { method: "POST", body: { ids: ids, action: act } }).then(function (r) {
            if (r.failed && r.failed.length) toast(T("tl.batch_partial", { n: r.done, f: r.failed.length }), true); else toast(T("tl.batch_done_" + act, { n: r.done }));
            v.sel = {}; refresh();
          }).catch(fail);
        };
        if (act === "delete") confirmDialog({ title: T("tl.del_title", { n: ids.length }), body: T("tl.del_body"), ok: T("td.delete"), danger: true }).then(function (y) { if (y) go(); });
        else go();
      }); });
      $("#t-new").addEventListener("click", function () { taskFormModal(null); });
      rows();
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
  // impactLines: how the selected permanent items change the task's requirement list.
  function impactLines(tk, its) {
    var cons = tk.constraints || [], ids = tk.constraint_ids || [];
    var out = cons.map(function (c, k) { return { op: " ", t: c, id: ids[k] }; });
    its.forEach(function (i) {
      if (i.scope === "next_run") { out.push({ op: "~", t: i.content }); return; }
      var at = -1;
      if (i.target_spec_item) out.forEach(function (x, k) { if (at < 0 && x.op === " " && (x.id === i.target_spec_item || x.t === i.target_spec_item)) at = k; });
      if (i.op === "add") out.push({ op: "+", t: i.content });
      else if (i.op === "delete") { if (at >= 0) out[at].op = "-"; else out.push({ op: "-", t: i.target_spec_item || i.content }); }
      else if (at >= 0 && i.conflict_resolution === "coexist") out.push({ op: "+", t: i.content });
      else { if (at >= 0) { out[at].op = "-"; out.splice(at + 1, 0, { op: "+", t: i.content }); } else out.push({ op: "+", t: i.content }); }
    });
    return out;
  }
  function impactHtml(lines, all) {
    var shown = all ? lines : lines.filter(function (l) { return l.op !== " "; });
    if (!shown.length) return "";
    return '<ul class="idiff">' + shown.map(function (l) {
      return '<li class="' + (l.op === "+" ? "ins" : l.op === "-" ? "del" : l.op === "~" ? "once" : "") + '"><span class="sg">' + esc(l.op === "-" ? "−" : l.op === "~" ? "↷" : l.op === " " ? "" : "+") + "</span>" + esc(l.t) + (l.op === "~" ? ' <small class="muted">' + esc(T("req.next_short")) + "</small>" : "") + "</li>";
    }).join("") + "</ul>";
  }
  function threadHtml(items, tk) {
    if (!items.length) return emptyState("sparkle", T("thread.none"), T("thread.none_sub"));
    tk = tk || {};
    var cname = function (idv) { var k = (tk.constraint_ids || []).indexOf(idv); return k >= 0 ? tk.constraints[k] : idv; };
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
      var conflict = g.items.some(function (i) { return i.status === "needs_decision"; });
      var reply = T(conflict ? "thread.r_conflict"
        : pending.length ? (scope === "next" ? "thread.r_next" : "thread.r_confirm")
        : all(function (i) { return i.status === "rejected"; }) ? "thread.r_discarded"
        : scope === "next" ? "thread.r_scheduled" : "thread.r_done");
      var impact = pending.length ? impactHtml(impactLines(tk, pending)) : "";
      return '<div class="msg"><span class="avatar">' + esc(T("thread.me")) + '</span><div><div class="msg-meta"><strong>' + esc(T("thread.you")) + '</strong><small>' + esc(ago(g.at)) + '</small></div><div class="msg-body">' + esc(g.utter || T("thread.amendment")) + "</div></div></div>" +
        '<div class="msg"><span class="avatar bot">' + waveMark() + '</span><div><div class="msg-meta"><strong>tidebot</strong><small>' + esc(ago(g.at)) + '</small></div><div class="msg-body">' + esc(reply) + "</div>" +
        '<div class="req-card"><div class="req-card-head">' + esc(T("req.structured")) +
        (scope === "next" ? '<span class="tag next">' + esc(T("req.next_only")) + "</span>" : scope === "perm" ? '<span class="tag perm">' + esc(T("req.permanent")) + "</span>" : '<span class="tag">' + esc(T("req.mixed")) + "</span>") + "</div>" +
        g.items.map(function (i) {
          var expired = i.status === "consumed";
          var sl = expired ? ["", T("req.expired")] : REQ_ST[i.status] ? [REQ_ST[i.status][0], T(REQ_ST[i.status][1])] : ["", i.status];
          var ck = i.status === "rejected" ? ic("x", "sm") : (i.status === "pending" || i.status === "needs_decision") ? ic("circle", "sm") : ic("checkc", "sm");
          var sel = i.status === "pending" ? '<input type="checkbox" class="req-sel" value="' + esc(i.id) + '" data-scope="' + esc(i.scope) + '" checked aria-label="' + esc(T("req.include")) + '">' : '<span class="ck ' + (i.status === "rejected" ? "no" : "") + '">' + ck + "</span>";
          return '<div class="req-item' + (expired || i.status === "rejected" ? " expired" : "") + '">' + sel + '<span class="op">' + esc(T("op." + i.op)) + '</span><span class="txt">' +
            (i.op !== "add" && i.target_spec_item ? '<s class="muted">' + esc(cname(i.target_spec_item)) + "</s> " + (i.op === "delete" ? "" : "→ ") : "") + esc(i.content || i.target_spec_item) +
            ' <span class="tag ' + (i.scope === "next_run" ? "next" : "perm") + '">' + esc(T(i.scope === "next_run" ? "req.next_short" : "req.permanent")) + "</span>" +
            '</span><span class="st">' + (i.status === "pending" || i.status === "needs_decision" ? '<button class="btn sm ghost icon-btn" data-ed="' + esc(i.id) + '" title="' + esc(T("req.edit")) + '" aria-label="' + esc(T("req.edit")) + '">' + ic("doc", "sm") + "</button>" : "") + '<span class="pill ' + sl[0] + '">' + esc(sl[1]) + "</span></span></div>" +
            (i.status === "needs_decision" ? '<div class="req-actions"><button class="btn sm" data-resolve="' + esc(i.id) + '" data-res="replace">' + esc(T("req.replace")) + '</button><button class="btn sm" data-resolve="' + esc(i.id) + '" data-res="coexist">' + esc(T("req.coexist")) + "</button></div>" : "");
        }).join("") +
        (impact ? '<div class="impact"><small>' + esc(T("req.impact")) + "</small>" + impact + "</div>" : "") +
        (conflict ? '<p class="hint req-hint">' + ic("info", "sm") + esc(T("req.resolve_first")) + "</p>" : "") +
        "</div></div></div>";
    }).join("");
  }

  function taskTab(tab, tk, items, vers, extra) {
    if (tab === "thread") {
      return '<div class="thread scroll" id="thread">' + threadHtml(items, tk) + "</div>" +
        (items.some(function (i) { return i.status === "pending"; }) ? '<div class="req-bar" id="req-bar" role="region" aria-label="' + esc(T("req.bar")) + '"><span id="rb-n"></span><span class="grow"></span><button class="btn sm ghost" id="rb-prev">' + esc(T("req.preview_all")) + '</button><button class="btn sm danger" id="rb-rej">' + esc(T("req.reject_sel")) + '</button><button class="btn sm ok" id="rb-ok"></button></div>' : "") +
        '<form class="composer" id="composer"><input name="u" placeholder="' + esc(T("thread.composer")) + '" autocomplete="off" maxlength="2000" aria-label="' + esc(T("thread.amendment")) + '"><button class="btn primary sm" type="submit" aria-label="' + esc(T("thread.send")) + '">' + ic("send", "sm") + "</button></form>";
    }
    if (tab === "overview") {
      return '<div class="thread"><dl class="kv ov">' +
        "<dt>" + esc(T("f.prompt")) + '</dt><dd><div class="inline-edit" id="ie-prompt" tabindex="0" role="button" aria-label="' + esc(T("ie.edit_prompt")) + '" title="' + esc(T("ie.hint")) + '">' + esc(tk.prompt) + "</div></dd>" +
        (tk.goal && tk.goal !== tk.prompt ? "<dt>" + esc(T("td.goal")) + "</dt><dd>" + esc(tk.goal) + "</dd>" : "") +
        "<dt>" + esc(T("td.requirements")) + "</dt><dd>" + (tk.constraints.length ? '<ul class="list-plain">' + tk.constraints.map(function (c) { return "<li>" + esc(c) + "</li>"; }).join("") + "</ul>" : '<span class="muted">' + esc(T("td.none_yet")) + "</span>") + "</dd>" +
        "<dt>" + esc(T("col.schedule")) + "</dt><dd>" + esc(cronHuman(tk.cron)) + (tk.cron ? " <code>" + esc(tk.cron) + '</code><ul class="next" id="ov-next"></ul>' : "") + "</dd>" +
        "<dt>" + esc(T("f.provider")) + "</dt><dd>" + esc(tk.provider || T("td.default")) + "</dd>" +
        "<dt>" + esc(T("nav.skills")) + "</dt><dd>" + (tk.skills.length ? tk.skills.map(esc).join(", ") : '<span class="muted">' + esc(T("td.none")) + "</span>") + "</dd>" +
        "<dt>" + esc(T("td.workspace")) + "</dt><dd><code>" + esc(tk.workspace || "—") + "</code></dd>" +
        "<dt>" + esc(T("td.task_id")) + "</dt><dd><code>" + esc(tk.id) + "</code></dd></dl></div>";
    }
    if (tab === "runs") return '<div class="thread flush-t">' + (extra && extra.length ? runsTable(extra) : emptyState("runs", T("runs.none"), "")) + "</div>";
    return '<div class="thread"><table class="table"><thead><tr><th>' + esc(T("col.version")) + "</th><th>" + esc(T("col.source")) + "</th><th>" + esc(T("col.note")) + "</th><th>" + esc(T("col.created")) + '</th><th></th></tr></thead><tbody>' +
      vers.versions.slice().reverse().map(function (v) {
        var cur = v.id === vers.current;
        return "<tr><td><strong>v" + esc(v.no) + "</strong>" + (cur ? ' <span class="tag perm">' + esc(T("ver.current")) + "</span>" : "") + "</td><td>" + esc(T("src." + v.source) === "src." + v.source ? v.source : T("src." + v.source)) + "</td><td>" + esc(v.message || "—") + '</td><td class="nowrap">' + esc(when(v.created_at)) +
          '</td><td class="right">' + (cur ? "" : '<button class="btn sm" data-rollback="' + esc(v.id) + '" data-no="' + esc(v.no) + '">' + ic("rollback", "sm") + esc(T("ver.rollback")) + "</button>") + "</td></tr>";
      }).join("") + "</tbody></table></div>";
  }
  // inlineEdit: click/Enter turns el into an input; Enter (Ctrl+Enter for multi-line) saves, Esc cancels.
  function inlineEdit(el, multi, save) {
    var start = function () {
      if (el.classList.contains("editing")) return;
      var old = el.textContent;
      el.classList.add("editing");
      el.innerHTML = multi ? '<textarea rows="4" maxlength="32000"></textarea><small class="hint">' + esc(T("ie.keys_multi")) + "</small>" : '<input maxlength="80">';
      var inp = $(multi ? "textarea" : "input", el);
      inp.value = old; inp.focus(); inp.select();
      var done = false;
      var end = function (commit) {
        if (done) return; done = true;
        var nv = inp.value.trim();
        el.classList.remove("editing"); el.textContent = commit && nv ? nv : old;
        if (commit && nv && nv !== old) save(nv).catch(function (e) { el.textContent = old; fail(e); });
        el.focus();
      };
      inp.addEventListener("keydown", function (e) {
        if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); end(false); }
        else if (e.key === "Enter" && (!multi || e.ctrlKey || e.metaKey)) { e.preventDefault(); end(true); }
      });
      inp.addEventListener("blur", function () { end(true); });
    };
    el.addEventListener("click", start);
    el.addEventListener("keydown", function (e) { if (e.key === "Enter" && !el.classList.contains("editing")) { e.preventDefault(); start(); } });
  }
  function renderTaskDetail(id, tab) {
    tab = tab || "overview";
    var base = "/api/tasks/" + enc(id);
    return Promise.all([api(base), api(base + "/amendments"), api(base + "/versions"), api(base + "/runs?limit=" + (tab === "runs" ? 50 : 6))]).then(function (r) {
      var t = r[0], items = r[1] || [], vers = r[2], runs = r[3] || [];
      var mini = runs.length ? '<ul class="mini-runs">' + runs.map(function (x) {
        var d = x.status === "succeeded" ? "" : (x.status === "running" || x.status === "queued") ? "run" : "bad";
        return '<li data-href="#/runs/' + enc(x.id) + '"><span class="d ' + d + '"></span><div><strong>' + (x.version_no ? "v" + esc(x.version_no) : "#" + esc(shortId(x.id))) + "</strong><small>" + esc(when(x.created_at)) + '</small></div><span class="muted">' + esc(dur(x.duration_ms)) + "</span><strong>" + esc(usd(x.cost_usd, 3)) + "</strong></li>";
      }).join("") + "</ul>" : emptyState("runs", T("runs.none"), "");
      var tabs = [["overview", T("td.tab_overview")], ["thread", T("td.tab_req")], ["runs", T("nav.runs")], ["history", T("td.tab_versions")]];
      mount('<div class="grid g-detail">' +
        '<section class="card"><div class="task-head"><div class="grow-col"><h1 class="inline-edit" id="ie-name" tabindex="0" role="button" title="' + esc(T("ie.hint")) + '">' + esc(t.name) + "</h1><p>" + esc(t.goal || t.prompt) + "</p>" +
        '<div class="chips"><span class="chip">' + ic("user", "sm") + esc(T("td.owned")) + "</span><span class=\"chip\">" + ic("clock", "sm") + esc(cronHuman(t.cron)) + "</span>" +
        '<span class="chip">' + ic("calendar", "sm") + esc(T("td.created_updated", { c: fmtTime(t.created_at, { month: "short", day: "numeric", year: "numeric" }), u: ago(t.updated_at) })) + "</span>" + taskPill(t.status) + "</div></div>" +
        '<div class="row-actions">' + (t.status === "active" ? '<button class="btn" id="t-toggle" data-act="pause">' + ic("pause", "sm") + esc(T("td.pause")) + "</button>" : '<button class="btn" id="t-toggle" data-act="resume">' + ic("play", "sm") + esc(T("td.resume")) + "</button>") +
        '<button class="btn" id="t-edit">' + ic("doc", "sm") + esc(T("btn.edit")) + '</button><button class="btn danger" id="t-del">' + ic("trash", "sm") + esc(T("td.delete")) + '</button><button class="btn primary" id="t-run">' + ic("play", "sm") + esc(T("td.run_now")) + "</button></div></div>" +
        '<nav class="tabs">' + tabs.map(function (x) { return '<button data-tab="' + x[0] + '" class="' + (x[0] === tab ? "active" : "") + '">' + esc(x[1]) + (x[0] === "thread" && items.some(function (i) { return i.status === "pending"; }) ? ' <span class="pill info plain">' + items.filter(function (i) { return i.status === "pending"; }).length + "</span>" : "") + "</button>"; }).join("") + "</nav>" +
        taskTab(tab, t, items, vers, runs) + "</section>" +
        '<div class="stack"><section class="card"><div class="card-head"><span class="ico">' + ic("branch") + '</span><h3>' + esc(T("ver.tree")) + '</h3><span class="link muted">' + esc(T("ver.n_current", { n: t.version_no })) + "</span></div>" + versionTree(vers.versions, vers.current) + '<p class="hint mt">' + esc(T("ver.hint")) + "</p></section>" +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("runs") + '</span><h3>' + esc(T("home.recent")) + '</h3><a class="link" href="#/runs?task=' + enc(t.id) + '">' + esc(T("td.view_all")) + "</a></div>" + mini + "</section></div></div>",
        "tasks", [[T("nav.tasks"), "#/tasks"], [t.name]]);
      var reload = function () { renderTaskDetail(id, tab); };
      var patch = function (field) {
        return function (val) {
          return api(base + "/form").then(function (d) { var f = d.form; f[field] = val; return api(base, { method: "PUT", body: f }); }).then(function () { toast(T("ie.saved")); reload(); });
        };
      };
      inlineEdit($("#ie-name"), false, patch("name"));
      if ($("#ie-prompt")) inlineEdit($("#ie-prompt"), true, patch("prompt"));
      if ($("#ov-next")) api("/api/scheduler/preview?cron=" + enc(t.cron) + "&n=3").then(function (d) { $("#ov-next").innerHTML = (d.times || []).map(function (x) { return "<li>" + esc(when(x)) + "</li>"; }).join(""); }).catch(function () {});
      wireRows();
      $$("[data-tab]").forEach(function (b) { b.addEventListener("click", function () { var tb = b.getAttribute("data-tab"); history.replaceState(null, "", "#/tasks/" + enc(id) + "/" + tb); lastHash = location.hash; renderTaskDetail(id, tb); }); });
      $("#t-run").addEventListener("click", function () { api(base + "/run", { method: "POST", body: {} }).then(function () { toast(T("toast.run_started")); setTimeout(reload, 800); }).catch(fail); });
      $("#t-edit").addEventListener("click", function () { taskFormModal(id); });
      $("#t-del").addEventListener("click", function () { confirmDialog({ title: T("td.del_title"), body: T("td.confirm_del", { n: t.name }), ok: T("td.delete"), danger: true, typeName: t.name }).then(function (y) { if (y) api(base, { method: "DELETE" }).then(function () { toast(T("td.deleted")); location.hash = "#/tasks"; }).catch(fail); }); });
      var tg = $("#t-toggle");
      tg.addEventListener("click", function () { api(base + "/" + tg.getAttribute("data-act"), { method: "POST", body: {} }).then(reload).catch(fail); });
      var sel = function () { return $$(".req-sel").filter(function (c) { return c.checked; }); };
      var bar = function () {
        var on = sel(), perm = on.some(function (c) { return c.getAttribute("data-scope") === "permanent"; });
        var conflict = items.some(function (i) { return i.status === "needs_decision"; });
        if (!$("#req-bar")) return;
        $("#rb-n").textContent = T("req.n_selected", { n: on.length });
        var ok = $("#rb-ok");
        ok.textContent = on.length ? (perm ? T("req.confirm_to", { n: on.length, v: t.version_no + 1 }) : T("req.confirm_next", { n: on.length })) : T("req.confirm");
        ok.disabled = !on.length || conflict;
        $("#rb-rej").disabled = !on.length;
        $("#rb-prev").disabled = !on.length;
      };
      $$(".req-sel").forEach(function (c) { c.addEventListener("change", bar); });
      bar();
      var chosen = function () { var ids = sel().map(function (c) { return c.value; }); return items.filter(function (i) { return ids.indexOf(i.id) >= 0; }); };
      if ($("#rb-prev")) $("#rb-prev").addEventListener("click", function () {
        modal('<div class="card-head"><span class="ico">' + ic("branch") + "</span><h2>" + esc(T("req.preview_title", { v: t.version_no + 1 })) + '</h2></div><p class="hint">' + esc(T("req.preview_sub")) + "</p>" + impactHtml(impactLines(t, chosen()), true) + '<div class="actions"><button class="btn ghost" data-close>' + esc(T("btn.close")) + "</button></div>");
      });
      if ($("#rb-ok")) $("#rb-ok").addEventListener("click", function () {
        var b = $("#rb-ok"); b.disabled = true;
        api(base + "/amendments-confirm", { method: "POST", body: { ids: sel().map(function (c) { return c.value; }) } }).then(function (r) { toast(r.version_id ? T("req.made_version", { v: t.version_no + 1 }) : T("toast.confirmed")); reload(); }).catch(function (e) { b.disabled = false; fail(e); });
      });
      if ($("#rb-rej")) $("#rb-rej").addEventListener("click", function () {
        var ids = sel().map(function (c) { return c.value; });
        Promise.all(ids.map(function (x) { return api("/api/amendments/" + enc(x) + "/reject", { method: "POST", body: {} }); })).then(function () { toast(T("toast.rejected")); reload(); }).catch(fail);
      });
      $$("[data-ed]").forEach(function (b) { b.addEventListener("click", function () {
        var it = items.filter(function (i) { return i.id === b.getAttribute("data-ed"); })[0];
        modal('<div class="card-head"><span class="ico">' + ic("doc") + "</span><h2>" + esc(T("req.edit")) + '</h2></div><form id="ef">' + (it.target_spec_item && it.op !== "add" ? '<p class="hint">' + esc(T("req.replaces")) + " <s>" + esc((t.constraints || [])[(t.constraint_ids || []).indexOf(it.target_spec_item)] || it.target_spec_item) + "</s></p>" : "") +
          fieldHtml(T("req.text"), '<textarea name="content" rows="3" maxlength="2000" required>' + esc(it.content) + "</textarea>") +
          '<div class="seg" id="ef-scope">' + [["permanent", T("req.permanent")], ["next_run", T("req.next_only")]].map(function (x) { return '<button type="button" data-sc="' + x[0] + '" class="' + (it.scope === x[0] ? "active" : "") + '">' + esc(x[1]) + "</button>"; }).join("") + '</div><div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T("btn.save")) + "</button></div></form>", function (root, close) {
          var sc = it.scope;
          $$("[data-sc]", root).forEach(function (x) { x.addEventListener("click", function () { sc = x.getAttribute("data-sc"); $$("[data-sc]", root).forEach(function (y) { y.classList.toggle("active", y === x); }); }); });
          $("#ef", root).addEventListener("submit", function (e) { e.preventDefault(); api("/api/amendments/" + enc(it.id) + "/edit", { method: "POST", body: { content: e.target.content.value, scope: sc } }).then(function () { close(); toast(T("ie.saved")); reload(); }).catch(fail); });
        });
      }); });
      $$("[data-resolve]").forEach(function (b) { b.addEventListener("click", function () { api("/api/amendments/" + enc(b.getAttribute("data-resolve")) + "/resolve", { method: "POST", body: { resolution: b.getAttribute("data-res") } }).then(reload).catch(fail); }); });
      var rollback = function (vid, no) {
        if (vid === vers.current) return;
        var prevId = vers.current, prevNo = t.version_no;
        confirmDialog({ title: T("ver.rb_title", { n: no }), body: T("ver.rb_body"), ok: T("ver.rollback") }).then(function (y) {
          if (!y) return;
          api(base + "/rollback", { method: "POST", body: { version_id: vid } }).then(function () {
            reload();
            toastUndo(T("toast.rolled_back", { n: no }), function () { api(base + "/rollback", { method: "POST", body: { version_id: prevId, reason: "undo" } }).then(function () { toast(T("toast.rolled_back", { n: prevNo })); reload(); }).catch(fail); });
          }).catch(fail);
        });
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
          return '<tr><td><div class="cell-main"><span class="ico">' + ic("skills", "sm") + "</span><div><a class=\"strong-link\" href=\"#/skills/" + enc(s.id) + '">' + esc(s.name) + '</a><small class="mono">' + esc(s.id) + '</small></div></div></td><td><span class="tag perm">v' + esc(s.version_no) + "</span></td><td>" + esc(s.files) + "</td><td>" + esc(plural(s.used_by, "tasks.count_one", "tasks.count_many")) +
            '</td><td class="nowrap">' + esc(when(s.updated_at)) + '</td><td class="right"><button class="btn sm" data-versions="' + esc(s.id) + '" data-name="' + esc(s.name) + '">' + ic("branch", "sm") + esc(T("sk.versions")) + "</button></td></tr>";
        }).join("") + "</tbody></table>" : emptyState("skills", T("sk.none"), T("sk.none_sub"));
      mount('<div class="page-head"><div><h1>' + esc(T("nav.skills")) + "</h1><p>" + esc(T("sk.sub")) + '</p></div><div class="actions"><button class="btn primary" id="sk-import">' + ic("upload", "sm") + esc(T("sk.import")) + "</button></div></div>" +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("skills") + "</span><h2>" + esc(T("sk.library")) + "</h2></div>" + rows + "</section>", "skills", [[T("nav.skills")]]);
      $("#sk-import").addEventListener("click", function () { skillImportModal(renderSkills); });
      $$("[data-versions]").forEach(function (b) { b.addEventListener("click", function () { location.hash = "#/skills/" + enc(b.getAttribute("data-versions")); }); });
    });
  }

  // ---------- skill detail: version tree, diff, optimizer ----------
  function diffView(d) {
    var files = (d.files || []).filter(function (f) { return f.status !== "same"; });
    if (!files.length) return '<p class="muted pad">' + esc(T("diff.identical")) + "</p>";
    return '<p class="diff-sum"><span class="add">+' + esc(d.added) + '</span> <span class="del">−' + esc(d.removed) + "</span> · " + esc(T("diff.files_n", { n: files.length })) + "</p>" + files.map(function (f) {
      var body = f.lines && f.lines.length ? '<table class="diff"><tbody>' + f.lines.map(function (l) {
        if (l.op === "@") return '<tr class="gap"><td colspan="4">⋯</td></tr>';
        var c = l.op === "+" ? "ins" : l.op === "-" ? "del" : "";
        return '<tr class="' + c + '"><td class="ln-a">' + (l.a || "") + '</td><td class="ln-b">' + (l.b || "") + '</td><td class="sg">' + esc(l.op === " " ? "" : l.op === "-" ? "−" : "+") + '</td><td class="tx">' + esc(l.text) + "</td></tr>";
      }).join("") + "</tbody></table>" : '<p class="muted pad">' + esc(T("diff.st_" + f.status)) + "</p>";
      return '<details class="diff-file" open><summary><code>' + esc(f.path) + '</code><span class="pill sm ' + (f.status === "added" ? "ok" : f.status === "removed" ? "bad" : "info") + '">' + esc(T("diff.st_" + f.status)) + '</span><span class="grow"></span>' + (f.added ? '<span class="add">+' + f.added + "</span> " : "") + (f.removed ? '<span class="del">−' + f.removed + "</span>" : "") + "</summary>" + body + "</details>";
    }).join("");
  }
  function renderSkillDetail(id, params) {
    var base = "/api/skills/" + enc(id);
    return Promise.all([api("/api/skills"), api(base + "/versions"), rpc("skills.optimize_settings", { skill_id: id }).catch(function () { return null; }), rpc("skills.proposals", { skill_id: id }).catch(function () { return []; }), rpc("skills.compare", { skill_id: id }).catch(function () { return null; })]).then(function (r) {
      var sk = (r[0] || []).filter(function (x) { return x.id === id; })[0];
      if (!sk) throw new Error(T("sk.not_found"));
      var vers = r[1], oset = r[2] || { mode: "off" }, props = r[3] || [], cmp = r[4];
      var list = vers.versions.slice().reverse(), byId = {};
      list.forEach(function (v) { byId[v.id] = v; });
      var cur = byId[vers.current] || list[0];
      var b = params.get("b") && byId[params.get("b")] ? params.get("b") : cur.id;
      var a = params.get("a") && byId[params.get("a")] ? params.get("a") : ((byId[b] && byId[b].parent_id && byId[byId[b].parent_id]) ? byId[b].parent_id : (list[1] || cur).id);
      var vopt = function (sel) { return list.map(function (v) { return opt(v.id, "v" + v.no + (v.id === vers.current ? " · " + T("ver.current") : v.id === vers.canary ? " · " + T("ver.canary") : ""), v.id === sel); }).join(""); };
      var tree = '<ul class="vlist">' + list.map(function (v) {
        var isCur = v.id === vers.current, isCan = v.id === vers.canary;
        return '<li class="' + (isCur ? "cur" : "") + '"><span class="vdot' + (isCur ? " cur" : isCan ? " can" : "") + '"></span><div class="grow"><strong>v' + esc(v.no) + "</strong>" + (isCur ? ' <span class="tag perm">' + esc(T("ver.current")) + "</span>" : "") + (isCan ? ' <span class="tag next">' + esc(T("ver.canary")) + "</span>" : "") +
          "<small>" + esc((T("src." + v.source) === "src." + v.source ? v.source : T("src." + v.source)) + " · " + when(v.created_at)) + (v.message ? " · " + esc(v.message) : "") + '</small></div><div class="row-actions"><button class="btn sm ghost" data-cmp="' + esc(v.id) + '">' + esc(T("diff.compare")) + "</button>" +
          (isCur ? "" : '<button class="btn sm" data-srb="' + esc(v.id) + '" data-no="' + esc(v.no) + '">' + ic("rollback", "sm") + esc(T("ver.rollback")) + "</button>") + "</div></li>";
      }).join("") + "</ul>";
      var modes = [["off", T("opt.m_off"), T("opt.m_off_sub")], ["propose", T("opt.m_propose"), T("opt.m_propose_sub")], ["canary", T("opt.m_canary"), T("opt.m_canary_sub")]];
      var pct = function (x) { return Math.round((x || 0) * 100) + "%"; };
      var cmpTable = function (c) {
        if (!c) return "";
        var row = function (label, f) { return "<tr><th>" + esc(label) + "</th><td>" + esc(f(c.current)) + "</td><td>" + esc(f(c.candidate)) + "</td></tr>"; };
        return '<table class="table cmp"><thead><tr><th></th><th>' + esc(T("opt.cur_col")) + "</th><th>" + esc(T("opt.cand_col")) + "</th></tr></thead><tbody>" + row(T("opt.runs"), function (x) { return x.runs; }) + row(T("opt.success"), function (x) { return x.runs ? pct(x.success_rate) : "—"; }) + row(T("opt.avg_cost"), function (x) { return x.runs ? usd(x.avg_cost_usd, 4) : "—"; }) + row(T("opt.p50"), function (x) { return x.runs ? dur(x.p50_ms) : "—"; }) + "</tbody></table>" + (c.note === "low_sample" ? '<p class="hint">' + ic("info", "sm") + esc(T("opt.low_sample")) + "</p>" : "");
      };
      var propHtml = props.length ? props.slice().reverse().map(function (p) {
        var live = p.status === "pending" || p.status === "canary";
        var v = byId[p.version_id];
        return '<div class="prop' + (live ? "" : " done") + '"><div class="prop-head"><strong>' + esc(v ? "v" + v.no : shortId(p.version_id)) + '</strong><span class="pill sm ' + (p.status === "canary" ? "info" : p.status === "promoted" ? "ok" : live ? "warn" : "") + '">' + esc(T("opt.st_" + p.status)) + '</span><span class="tag">' + esc(T("opt.risk_" + (p.risk || "low"))) + '</span><small class="muted grow right">' + esc(ago(p.created_at)) + "</small></div>" +
          (p.rationale ? '<p class="prop-why">' + esc(p.rationale) + "</p>" : "") +
          (p.checks && p.checks.length ? '<ul class="checks">' + p.checks.map(function (c) { return '<li class="' + (c.ok ? "ok" : "bad") + '">' + ic(c.ok ? "check" : "x", "sm") + esc(c.name) + "</li>"; }).join("") + "</ul>" : "") +
          (p.status === "canary" && cmp && cmp.proposal_id === p.id ? cmpTable(cmp) : "") +
          '<div class="row-actions"><button class="btn sm ghost" data-cmp-pair="' + esc(p.version_id) + '">' + esc(T("opt.view_diff")) + "</button>" +
          (live ? (p.status === "pending" ? '<button class="btn sm" data-canary="' + esc(p.id) + '">' + esc(T("opt.start_canary")) + "</button>" : "") + '<button class="btn sm danger" data-discard="' + esc(p.id) + '">' + esc(T("opt.discard")) + '</button><button class="btn sm primary" data-promote="' + esc(p.id) + '" data-no="' + esc(v ? v.no : "") + '">' + esc(T("opt.promote")) + "</button>" : "") + "</div></div>";
      }).join("") : '<p class="muted">' + esc(T("opt.none")) + "</p>";
      mount('<div class="grid g-detail">' +
        '<section class="card"><div class="task-head"><div class="grow-col"><h1>' + esc(sk.name) + '</h1><p class="mono muted">' + esc(sk.id) + '</p><div class="chips"><span class="chip">' + ic("branch", "sm") + esc(T("ver.n_current", { n: sk.version_no })) + '</span><span class="chip">' + esc(plural(sk.used_by, "tasks.count_one", "tasks.count_many")) + "</span></div></div></div>" +
        '<div class="diff-bar"><label>' + esc(T("diff.from")) + ' <select id="d-a">' + vopt(a) + '</select></label><span aria-hidden="true">→</span><label>' + esc(T("diff.to")) + ' <select id="d-b">' + vopt(b) + '</select></label><button class="btn sm ghost" id="d-swap" title="' + esc(T("diff.swap")) + '">⇄</button><label class="check"><input type="checkbox" id="d-all"> ' + esc(T("diff.full")) + '</label></div><div id="diff" class="diff-host">' + skeleton("list") + "</div></section>" +
        '<div class="stack"><section class="card"><div class="card-head"><span class="ico">' + ic("branch") + "</span><h3>" + esc(T("ver.tree")) + "</h3></div>" + tree + '<p class="hint mt">' + ic("info", "sm") + esc(T("ver.rb_pointer")) + "</p></section>" +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("sparkle") + "</span><h3>" + esc(T("opt.title")) + '</h3></div><div class="seg" id="opt-mode" role="radiogroup">' + modes.map(function (m) { return '<button type="button" role="radio" aria-checked="' + (oset.mode === m[0]) + '" data-mode="' + m[0] + '" class="' + (oset.mode === m[0] ? "active" : "") + '">' + esc(m[1]) + "</button>"; }).join("") + '</div><p class="hint" id="opt-sub">' + esc((modes.filter(function (m) { return m[0] === oset.mode; })[0] || modes[0])[2]) + '</p><p class="notice">' + ic("shield", "sm") + esc(T("opt.never_auto")) + "</p>" +
        '<h4 class="mt">' + esc(T("opt.proposals")) + "</h4>" + propHtml + "</section></div></div>",
        "skills", [[T("nav.skills"), "#/skills"], [sk.name]]);
      var load = function () {
        var qa = $("#d-a").value, qb = $("#d-b").value;
        history.replaceState(null, "", "#/skills/" + enc(id) + "?a=" + enc(qa) + "&b=" + enc(qb)); lastHash = location.hash;
        if (qa === qb) { $("#diff").innerHTML = '<p class="muted pad">' + esc(T("diff.same_ver")) + "</p>"; return; }
        api(base + "/diff?a=" + enc(qa) + "&b=" + enc(qb) + ($("#d-all").checked ? "&ctx=all" : "")).then(function (d) { $("#diff").innerHTML = diffView(d); }).catch(function (e) { $("#diff").innerHTML = errorState(e.message); });
      };
      ["#d-a", "#d-b", "#d-all"].forEach(function (s) { $(s).addEventListener("change", load); });
      $("#d-swap").addEventListener("click", function () { var x = $("#d-a").value; $("#d-a").value = $("#d-b").value; $("#d-b").value = x; load(); });
      $$("[data-cmp]").forEach(function (bt) { bt.addEventListener("click", function () { $("#d-a").value = bt.getAttribute("data-cmp"); $("#d-b").value = vers.current; load(); $("#diff").scrollIntoView({ block: "nearest" }); }); });
      $$("[data-cmp-pair]").forEach(function (bt) { bt.addEventListener("click", function () { $("#d-a").value = vers.current; $("#d-b").value = bt.getAttribute("data-cmp-pair"); load(); $("#diff").scrollIntoView({ block: "nearest" }); }); });
      var reload = function () { renderSkillDetail(id, new URLSearchParams("a=" + $("#d-a").value + "&b=" + $("#d-b").value)); };
      $$("[data-srb]").forEach(function (bt) { bt.addEventListener("click", function () {
        var vid = bt.getAttribute("data-srb"), no = bt.getAttribute("data-no"), prev = vers.current, prevNo = (byId[prev] || {}).no;
        confirmDialog({ title: T("ver.rb_title", { n: no }), body: T("ver.rb_pointer"), ok: T("ver.rollback") }).then(function (y) {
          if (!y) return;
          api(base + "/rollback", { method: "POST", body: { version_id: vid } }).then(function () {
            reload();
            toastUndo(T("toast.rolled_back", { n: no }), function () { api(base + "/rollback", { method: "POST", body: { version_id: prev } }).then(function () { toast(T("toast.rolled_back", { n: prevNo })); reload(); }).catch(fail); });
          }).catch(fail);
        });
      }); });
      $$("#opt-mode [data-mode]").forEach(function (bt) { bt.addEventListener("click", function () {
        rpc("skills.optimize_settings", { skill_id: id, mode: bt.getAttribute("data-mode") }).then(function () { toast(T("opt.mode_saved")); reload(); }).catch(fail);
      }); });
      $$("[data-canary]").forEach(function (bt) { bt.addEventListener("click", function () { rpc("skills.canary_start", { proposal_id: bt.getAttribute("data-canary") }).then(function () { toast(T("opt.canary_started")); reload(); }).catch(fail); }); });
      $$("[data-discard]").forEach(function (bt) { bt.addEventListener("click", function () { confirmDialog({ title: T("opt.discard_title"), body: T("opt.discard_body"), ok: T("opt.discard"), danger: true }).then(function (y) { if (y) rpc("skills.discard", { proposal_id: bt.getAttribute("data-discard") }).then(function () { toast(T("opt.discarded")); reload(); }).catch(fail); }); }); });
      $$("[data-promote]").forEach(function (bt) { bt.addEventListener("click", function () { confirmDialog({ title: T("opt.promote_title", { n: bt.getAttribute("data-no") }), body: T("opt.promote_body"), ok: T("opt.promote") }).then(function (y) { if (y) rpc("skills.promote", { proposal_id: bt.getAttribute("data-promote") }).then(function () { toast(T("opt.promoted")); reload(); }).catch(fail); }); }); });
      load();
    });
  }
  // ---------- BUTTONS ----------
  function renderButtons() {
    return Promise.all([api("/api/buttons"), api("/api/tasks"), api("/api/skills")]).then(function (r) {
      var list = r[0], tasks = r[1], skills = r[2] || [], tname = {}, sname = {};
      tasks.forEach(function (t) { tname[t.id] = t.name; });
      skills.forEach(function (x) { sname[x.id] = x.name; });
      var cards = list.map(function (b, i) {
        var sub = b.description || (b.task_id ? T("bt.runs_task", { name: tname[b.task_id] || b.task_id }) : b.skill_id ? T("bt.runs_skill", { name: sname[b.skill_id] || b.skill_id }) : T("bt.prompt", { p: b.prompt }));
        return '<div class="card qa fill' + (b.color ? " colored" : "") + '" draggable="true" tabindex="0" data-idx="' + i + '" aria-roledescription="' + esc(T("bt.sortable")) + '" aria-label="' + esc(b.label) + '" data-color="' + esc(b.color || "") + '" data-run="' + esc(b.id) + '"><span class="grip qa-grip" aria-hidden="true" title="' + esc(T("bt.sort_hint")) + '">⋮⋮</span><span class="qa-ico">' + ic(btnIcon(b), "lg") + "</span><strong>" + esc(b.label) + "</strong><small>" + esc(sub) + '</small><div class="req-actions"><button class="btn sm" data-edit="' + i + '">' + esc(T("btn.edit")) + '</button><button class="btn sm danger" data-del="' + i + '">' + esc(T("btn.remove")) + '</button></div></div>';
      });
      cards.push('<button class="card qa tint" id="b-add"><span class="qa-ico">' + ic("plus", "lg") + "</span><strong>" + esc(T("bt.new")) + "</strong><small>" + esc(T("bt.new_sub")) + " " + ic("arrow", "sm") + "</small></button>");
      mount('<div class="page-head"><div><h1>' + esc(T("nav.buttons")) + "</h1><p>" + esc(T("bt.sub")) + '</p></div></div><p class="hint">' + esc(T("bt.sort_hint")) + '</p><div class="grid g4" id="bt-grid">' + cards.join("") + '</div><div class="sr-only" aria-live="polite" id="bt-live"></div>', "buttons", [[T("nav.buttons")]]);
      applyColors(page());
      var save = function (nl) { return api("/api/buttons", { method: "PUT", body: nl }).then(function () { toast(T("toast.buttons_saved")); renderButtons(); }); };
      $("#b-add").addEventListener("click", function () { buttonModal(list, null, tasks, skills, save); });
      $$("[data-edit]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); buttonModal(list, +b.getAttribute("data-edit"), tasks, skills, save); }); });
      $$("[data-del]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); var k = +b.getAttribute("data-del"); confirmDialog({ title: T("bt.rm_title", { n: list[k].label }), body: T("bt.confirm_rm"), ok: T("btn.remove"), danger: true }).then(function (y) { if (!y) return; var nl = list.slice(); nl.splice(k, 1); save(nl).catch(fail); }); }); });
      $$("[data-run]").forEach(function (c) { c.addEventListener("click", function (e) { if (e.target.closest("button")) return; runButton(c.getAttribute("data-run"), c); }); });
      // ordering: drag with the mouse, or focus a card and press Alt+←/→ (Alt+↑/↓)
      var grid = $("#bt-grid"), order = function () { return $$("[data-idx]", grid).map(function (c) { return list[+c.getAttribute("data-idx")]; }); };
      var persist = function (focusId) {
        var nl = order();
        if (nl.every(function (b, k) { return b === list[k]; })) return;
        api("/api/buttons", { method: "PUT", body: nl }).then(function () {
          $("#bt-live").textContent = T("bt.order_saved");
          toast(T("bt.order_saved"));
          renderButtons().then(function () { var c = $('[data-run="' + focusId + '"]'); if (c) c.focus(); });
        }).catch(fail);
      };
      $$("[data-idx]", grid).forEach(function (c) {
        c.addEventListener("dragstart", function (e) { c.classList.add("dragging"); e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", c.getAttribute("data-run")); });
        c.addEventListener("dragend", function () { c.classList.remove("dragging"); persist(c.getAttribute("data-run")); });
        c.addEventListener("keydown", function (e) {
          if (e.target !== c) return;
          if (e.key === "Enter" || e.key === " ") { e.preventDefault(); runButton(c.getAttribute("data-run"), c); return; }
          if (!e.altKey) return;
          var back = e.key === "ArrowLeft" || e.key === "ArrowUp", fwd = e.key === "ArrowRight" || e.key === "ArrowDown";
          if (!back && !fwd) return;
          e.preventDefault();
          var sib = back ? c.previousElementSibling : c.nextElementSibling;
          if (!sib || !sib.hasAttribute("data-idx")) return;
          grid.insertBefore(c, back ? sib : sib.nextSibling);
          persist(c.getAttribute("data-run"));
        });
      });
      grid.addEventListener("dragover", function (e) {
        var drag = $(".dragging", grid); if (!drag) return;
        e.preventDefault();
        var after = $$("[data-idx]:not(.dragging)", grid).filter(function (x) { var r = x.getBoundingClientRect(); return e.clientY < r.bottom && e.clientX < r.left + r.width / 2 || e.clientY < r.top; })[0];
        if (after) grid.insertBefore(drag, after); else grid.insertBefore(drag, $("#b-add"));
      });
    });
  }

  // ---------- v0.1.2 shared helpers ----------
  function upload(path, fd) {
    return fetch(path, { method: "POST", headers: { "Accept": "application/json", "X-CSRF-Token": state.csrf }, body: fd, credentials: "same-origin" }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (d) {
        if (res.status === 401) { showLogin(); throw new Error(T("err.sign_in")); }
        if (!res.ok) { var e = new Error(d.error || res.statusText); e.status = res.status; e.data = d; throw e; }
        return d;
      });
    });
  }
  // Colors go through CSSOM (CSP blocks inline style attributes).
  function applyColors(root) { $$("[data-color]", root).forEach(function (el) { var c = el.getAttribute("data-color"); if (/^#[0-9a-fA-F]{6}$/.test(c)) el.style.setProperty("--qa-color", c); }); }
  var SWATCHES = ["", "#2f6fed", "#0f9d8a", "#7b5cd6", "#d9822b", "#d64545", "#3b8b3b", "#5a6b7d"];
  function fieldHtml(label, inner, hint) { return '<label class="field"><span>' + esc(label) + "</span>" + inner + (hint ? '<small class="hint">' + esc(hint) + "</small>" : "") + "</label>"; }
  function opt(v, label, sel) { return '<option value="' + esc(v) + '"' + (sel ? " selected" : "") + ">" + esc(label) + "</option>"; }
  // Collect dropped files; folders are walked so the server sees relative paths.
  function droppedFiles(dt) {
    var items = dt.items ? Array.prototype.slice.call(dt.items) : [];
    var entries = items.map(function (it) { return it.webkitGetAsEntry ? it.webkitGetAsEntry() : null; }).filter(Boolean);
    if (!entries.length || (entries.length === 1 && entries[0].isFile)) return Promise.resolve({ single: dt.files[0] });
    var out = [];
    function walk(entry, prefix) {
      if (entry.isFile) return new Promise(function (ok, bad) { entry.file(function (f) { out.push({ file: f, path: prefix + f.name }); ok(); }, bad); });
      return new Promise(function (ok, bad) {
        var rd = entry.createReader(), all = [];
        (function more() { rd.readEntries(function (es) { if (!es.length) { Promise.all(all.map(function (e) { return walk(e, prefix + entry.name + "/"); })).then(ok, bad); } else { all = all.concat(Array.prototype.slice.call(es)); more(); } }, bad); })();
      });
    }
    return Promise.all(entries.map(function (e) { return walk(e, ""); })).then(function () { return { many: out }; });
  }
  function dropZone(id, accept, folder) {
    return '<div class="drop" id="' + id + '">' + ic("upload", "lg") + "<strong>" + esc(T("up.drop")) + "</strong><small>" + esc(T(folder ? "up.drop_sub_skill" : "up.drop_sub_plugin")) + '</small><div class="row-actions"><label class="btn sm">' + esc(T("up.pick_file")) + '<input type="file" class="hidden" data-pick="file" accept="' + accept + '"></label>' +
      (folder ? '<label class="btn sm">' + esc(T("up.pick_folder")) + '<input type="file" class="hidden" data-pick="dir" webkitdirectory multiple></label>' : "") + "</div></div>";
  }
  function wireDrop(root, id, onForm) {
    var z = $("#" + id, root);
    var many = function (list) { var fd = new FormData(); list.forEach(function (x) { fd.append("files", x.file, x.file.name); fd.append("paths", x.path); }); onForm(fd); };
    ["dragenter", "dragover"].forEach(function (ev) { z.addEventListener(ev, function (e) { e.preventDefault(); z.classList.add("over"); }); });
    z.addEventListener("dragleave", function () { z.classList.remove("over"); });
    z.addEventListener("drop", function (e) {
      e.preventDefault(); z.classList.remove("over");
      droppedFiles(e.dataTransfer).then(function (r) {
        if (r.single) { var fd = new FormData(); fd.append("file", r.single, r.single.name); onForm(fd); } else many(r.many);
      }).catch(fail);
    });
    $$("[data-pick]", z).forEach(function (inp) {
      inp.addEventListener("change", function () {
        if (!inp.files.length) return;
        if (inp.getAttribute("data-pick") === "dir") many(Array.prototype.slice.call(inp.files).map(function (f) { return { file: f, path: f.webkitRelativePath || f.name }; }));
        else { var fd = new FormData(); fd.append("file", inp.files[0], inp.files[0].name); onForm(fd); }
        inp.value = "";
      });
    });
  }

  function fmtBytes(n) { return n < 1024 ? n + " B" : n < 1048576 ? (n / 1024).toFixed(1) + " KB" : (n / 1048576).toFixed(1) + " MB"; }
  // fileTree renders "a/b/c.md" paths as an indented tree; folders first.
  function fileTree(files) {
    var root = { d: {}, f: [] };
    files.forEach(function (x) {
      var parts = x.path.split("/"), n = root;
      parts.slice(0, -1).forEach(function (p) { n = n.d[p] = n.d[p] || { d: {}, f: [] }; });
      n.f.push({ name: parts[parts.length - 1], size: x.size });
    });
    var walk = function (n) {
      return "<ul>" + Object.keys(n.d).sort().map(function (k) { return '<li class="dir">' + ic("folder", "sm") + esc(k) + "/" + walk(n.d[k]) + "</li>"; }).join("") +
        n.f.sort(function (a, b) { return a.name < b.name ? -1 : 1; }).map(function (f) { return '<li class="file' + (f.name === "SKILL.md" ? " key" : "") + '">' + ic("doc", "sm") + esc(f.name) + '<small class="muted">' + esc(fmtBytes(f.size)) + "</small></li>"; }).join("") + "</ul>";
    };
    return '<div class="tree">' + walk(root) + "</div>";
  }
  // ---------- skill import ----------
  function skillImportModal(done) {
    modal('<div class="card-head"><span class="ico">' + ic("upload") + "</span><h2>" + esc(T("sk.import")) + "</h2></div>" + dropZone("sk-drop", ".zip,.skill,.tar.gz,.tgz", true) + '<div id="sk-prev"></div><div class="actions" id="sk-foot"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button></div>", function (root, close) {
      var prev = $("#sk-prev", root), foot = $("#sk-foot", root);
      var doImport = function (p, name, mode, btn) {
        btn.disabled = true;
        api("/api/skills/import", { method: "POST", body: { stage_id: p.stage_id, name: name, mode: mode } }).then(function () {
          close(); toast(T(mode === "version" ? "toast.skill_version" : "toast.skill_imported")); done();
        }).catch(function (e) {
          btn.disabled = false;
          if (e.status === 409 && e.data && e.data.existing) { p.existing = e.data.existing; show(p, name); return; }
          fail(e);
        });
      };
      function show(p, name) {
        var ex = p.existing;
        prev.innerHTML = '<div class="preview"><form id="sk-form">' + fieldHtml(T("tf.name"), '<input name="name" required maxlength="64" value="' + esc(name || p.name) + '">', T("sk.name_hint")) +
          '<div class="field"><span>' + esc(T("sk.desc")) + '</span><p class="desc-box">' + esc(p.description || T("sk.no_desc")) + "</p></div>" +
          '<ul class="checks">' + (p.checks || []).map(function (c) { return '<li class="' + (c.ok ? "ok" : "warn") + '">' + ic(c.ok ? "check" : "info", "sm") + esc(T("sk.chk_" + c.key)) + "</li>"; }).join("") + "</ul>" +
          '<details class="ftree" open><summary>' + esc(T("sk.files_n", { n: p.files })) + " · " + esc(fmtBytes(p.size || 0)) + "</summary>" + fileTree(p.tree || []) + "</details>" +
          (ex ? '<p class="notice">' + ic("info", "sm") + esc(T("sk.exists", { n: ex.version_no, next: ex.next_no || ex.version_no + 1 })) + "</p>" : "") + "</form></div>";
        foot.innerHTML = '<button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button>" +
          (ex ? '<button type="button" class="btn primary" id="sk-ver">' + ic("branch", "sm") + esc(T("sk.as_version")) + "</button>" : '<button type="button" class="btn primary" id="sk-go">' + esc(T("sk.import_btn")) + "</button>");
        $$("[data-close]", foot).forEach(function (b) { b.addEventListener("click", close); });
        var f = $("#sk-form", prev);
        var nm = function () { return f.name.value.trim(); };
        f.name.addEventListener("input", function () { if (p.existing && nm() !== name) { p.existing = null; show(p, nm()); f = $("#sk-form", prev); f.name.focus(); f.name.setSelectionRange(f.name.value.length, f.name.value.length); } });
        f.addEventListener("submit", function (e) { e.preventDefault(); });
        var go = $("#sk-go", foot), ver = $("#sk-ver", foot);
        if (go) go.addEventListener("click", function () { if (f.reportValidity()) doImport(p, nm(), "", go); });
        if (ver) ver.addEventListener("click", function () { doImport(p, nm(), "version", ver); });
      }
      wireDrop(root, "sk-drop", function (fd) {
        prev.innerHTML = '<p class="muted">' + esc(T("up.reading")) + "</p>";
        upload("/api/skills/upload", fd).then(function (p) { show(p, p.name); }).catch(function (e) { prev.innerHTML = '<p class="notice bad">' + ic("x", "sm") + esc(e.message) + "</p>"; });
      });
    });
  }

  // ---------- button editor ----------
  function buttonModal(list, idx, tasks, skills, save) {
    var b = idx == null ? { id: "", label: "", description: "", icon: "bolt", color: "", task_id: "", skill_id: "", skill_version_id: "", prompt: "", params: [] } : JSON.parse(JSON.stringify(list[idx]));
    b.params = b.params || [];
    var mode = b.task_id ? "task" : b.skill_id ? "skill" : "prompt";
    var icons = Object.keys(P).filter(function (k) { return ["chev", "more", "x", "circle"].indexOf(k) < 0; });
    modal('<div class="card-head"><span class="ico">' + ic("buttons") + "</span><h2>" + esc(T(idx == null ? "bt.new" : "bt.edit")) + '</h2></div><div class="bt-edit"><form id="bf">' +
      '<div class="form-row">' + fieldHtml(T("bt.label"), '<input name="label" required maxlength="40" value="' + esc(b.label) + '">') + fieldHtml(T("bt.desc"), '<input name="description" maxlength="60" value="' + esc(b.description) + '">') + "</div>" +
      '<div class="field"><span>' + esc(T("bt.icon")) + '</span><input id="ic-q" type="search" placeholder="' + esc(T("bt.icon_search")) + '"><div class="icon-grid" id="ic-grid">' + icons.map(function (k) { return '<button type="button" class="ic-cell' + (k === b.icon ? " on" : "") + '" data-ic="' + k + '" title="' + k + '">' + ic(k) + "</button>"; }).join("") + "</div></div>" +
      '<div class="field"><span>' + esc(T("bt.color")) + '</span><div class="swatches">' + SWATCHES.map(function (c) { return '<button type="button" class="sw' + (c === (b.color || "") ? " on" : "") + (c ? "" : " none") + '" data-sw="' + c + '" data-color="' + c + '" aria-label="' + esc(c || T("bt.color_default")) + '"></button>'; }).join("") + "</div></div>" +
      '<div class="field"><span>' + esc(T("bt.action")) + '</span><div class="seg" id="bt-mode">' + [["prompt", T("bt.m_prompt")], ["skill", T("bt.m_skill")], ["task", T("bt.m_task")]].map(function (m) { return '<button type="button" data-m="' + m[0] + '" class="' + (m[0] === mode ? "active" : "") + '">' + esc(m[1]) + "</button>"; }).join("") + "</div></div>" +
      '<div data-for="task">' + fieldHtml(T("col.task"), '<select name="task_id">' + tasks.map(function (t) { return opt(t.id, t.name, t.id === b.task_id); }).join("") + "</select>") + "</div>" +
      '<div data-for="skill" class="form-row">' + fieldHtml(T("bt.skill"), '<select name="skill_id">' + skills.map(function (s) { return opt(s.id, s.name, s.id === b.skill_id); }).join("") + "</select>") + fieldHtml(T("bt.skill_ver"), '<select name="skill_version_id"><option value="">' + esc(T("bt.latest")) + "</option></select>") + "</div>" +
      '<div data-for="prompt skill">' + fieldHtml(T("bt.prompt_label"), '<textarea name="prompt" maxlength="8000">' + esc(b.prompt) + "</textarea>", T("bt.params_hint")) +
      '<div class="field"><span>' + esc(T("bt.params")) + '</span><div id="pl"></div><button type="button" class="btn sm" id="p-add">' + ic("plus", "sm") + esc(T("bt.param_add")) + "</button></div></div>" +
      '</form><div class="bt-prev"><small class="muted">' + esc(T("bt.preview")) + '</small><div id="bt-card"></div></div></div>' +
      '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary" id="bt-save">' + esc(T("btn.save")) + "</button></div>", function (root, close) {
      var f = $("#bf", root);
      var drawParams = function () {
        $("#pl", root).innerHTML = b.params.map(function (p, i) { return '<div class="param-row"><input data-pn="' + i + '" placeholder="' + esc(T("bt.param_name")) + '" maxlength="32" value="' + esc(p.name) + '"><input data-pd="' + i + '" placeholder="' + esc(T("bt.param_default")) + '" maxlength="200" value="' + esc(p.default) + '"><button type="button" class="btn sm ghost" data-px="' + i + '">' + ic("x", "sm") + "</button></div>"; }).join("");
        $$("[data-pn]", root).forEach(function (x) { x.addEventListener("input", function () { b.params[+x.getAttribute("data-pn")].name = x.value; }); });
        $$("[data-pd]", root).forEach(function (x) { x.addEventListener("input", function () { b.params[+x.getAttribute("data-pd")].default = x.value; }); });
        $$("[data-px]", root).forEach(function (x) { x.addEventListener("click", function () { b.params.splice(+x.getAttribute("data-px"), 1); drawParams(); }); });
      };
      var preview = function () {
        var sub = mode === "task" ? T("bt.runs_task", { name: (f.task_id.selectedOptions[0] || {}).text || "—" }) : mode === "skill" ? T("bt.runs_skill", { name: (f.skill_id.selectedOptions[0] || {}).text || "—" }) : T("bt.prompt", { p: f.prompt.value || "…" });
        $("#bt-card", root).innerHTML = '<div class="card qa fill' + (b.color ? " colored" : "") + '" data-color="' + esc(b.color) + '"><span class="qa-ico">' + ic(b.icon, "lg") + "</span><strong>" + esc(f.label.value || T("bt.label")) + "</strong><small>" + esc(f.description.value || sub) + "</small></div>";
        applyColors($("#bt-card", root));
      };
      var showMode = function () {
        $$("[data-m]", root).forEach(function (x) { x.classList.toggle("active", x.getAttribute("data-m") === mode); });
        $$("[data-for]", root).forEach(function (x) { x.classList.toggle("hidden", x.getAttribute("data-for").split(" ").indexOf(mode) < 0); });
        preview();
      };
      var loadVers = function () {
        var sel = f.skill_version_id;
        if (!f.skill_id.value) { sel.innerHTML = '<option value="">' + esc(T("bt.latest")) + "</option>"; return; }
        api("/api/skills/" + enc(f.skill_id.value) + "/versions").then(function (v) {
          sel.innerHTML = '<option value="">' + esc(T("bt.latest")) + "</option>" + v.versions.slice().reverse().map(function (x) { return opt(x.id, T("bt.pin_v", { n: x.no }), x.id === b.skill_version_id); }).join("");
        }).catch(fail);
      };
      applyColors(root);
      drawParams(); loadVers(); showMode();
      $("#ic-q", root).addEventListener("input", function (e) { var q = e.target.value.trim().toLowerCase(); $$("[data-ic]", root).forEach(function (x) { x.classList.toggle("hidden", q && x.getAttribute("data-ic").indexOf(q) < 0); }); });
      $$("[data-ic]", root).forEach(function (x) { x.addEventListener("click", function () { b.icon = x.getAttribute("data-ic"); $$("[data-ic]", root).forEach(function (y) { y.classList.toggle("on", y === x); }); preview(); }); });
      $$("[data-sw]", root).forEach(function (x) { x.addEventListener("click", function () { b.color = x.getAttribute("data-sw"); $$("[data-sw]", root).forEach(function (y) { y.classList.toggle("on", y === x); }); preview(); }); });
      $$("[data-m]", root).forEach(function (x) { x.addEventListener("click", function () { mode = x.getAttribute("data-m"); showMode(); }); });
      f.skill_id.addEventListener("change", function () { b.skill_version_id = ""; loadVers(); });
      f.addEventListener("input", preview);
      f.addEventListener("change", preview);
      $("#p-add", root).addEventListener("click", function () { if (b.params.length < 10) { b.params.push({ name: "", default: "" }); drawParams(); } });
      $("#bt-save", root).addEventListener("click", function () {
        if (!f.reportValidity()) return;
        if (mode === "task" && !f.task_id.value) { toast(T("bt.need_task"), true); return; }
        if (mode === "skill" && !f.skill_id.value) { toast(T("bt.need_skill"), true); return; }
        if (mode !== "task" && !f.prompt.value.trim()) { toast(T("bt.need_prompt"), true); return; }
        var nb = { id: b.id, label: f.label.value.trim(), description: f.description.value.trim(), icon: b.icon, color: b.color || "",
          task_id: mode === "task" ? f.task_id.value : "", skill_id: mode === "skill" ? f.skill_id.value : "", skill_version_id: mode === "skill" ? f.skill_version_id.value : "",
          prompt: mode === "task" ? "" : f.prompt.value, params: mode === "task" ? [] : b.params.filter(function (p) { return p.name.trim(); }) };
        var nl = list.slice();
        if (idx == null) nl.push(nb); else nl[idx] = nb;
        save(nl).then(close).catch(fail);
      });
    });
  }

  // ---------- providers: list on the left, details on the right ----------
  var provSel = null;
  function providerCards(box) {
    return api("/api/providers").then(function (d) {
      var ps = d.providers || [], presets = d.presets || [], byName = {}, presetById = {};
      ps.forEach(function (p) { byName[p.name] = p; });
      presets.forEach(function (p) { presetById[p.id] = p; });
      if (!provSel || (!byName[provSel] && provSel !== "+new")) provSel = (ps[0] || {}).name;
      box.innerHTML = '<div class="pv-split"><div class="pv-list"><ul id="pv-ul" aria-label="' + esc(T("set.providers")) + '">' + ps.map(function (p) {
        return '<li draggable="true" data-pv="' + esc(p.name) + '" tabindex="0" class="' + (p.name === provSel ? "on" : "") + '" aria-current="' + (p.name === provSel) + '"><span class="grip" aria-hidden="true">⋮⋮</span><span class="pv-n">' + esc(p.name) + "</span>" +
          (p.default ? '<span class="pill ok sm">' + esc(T("pv.default")) + "</span>" : "") + '<span class="dot ' + (p.key_set ? "ok" : "") + '" title="' + esc(T(p.key_set ? "set.configured" : "set.missing")) + '"></span></li>';
      }).join("") + '</ul><button class="btn sm wide" id="pv-add">' + ic("plus", "sm") + esc(T("pv.add")) + '</button><small class="hint">' + esc(T("pv.sort_hint")) + '</small></div><div class="pv-detail" id="pv-detail"></div></div>';
      var again = function (sel) { if (sel) provSel = sel; return providerCards(box); };
      var pick = function (n) { provSel = n; $$("#pv-ul li", box).forEach(function (li) { var on = li.getAttribute("data-pv") === n; li.classList.toggle("on", on); li.setAttribute("aria-current", on); }); providerDetail($("#pv-detail", box), byName[n], null, presetById, again); };
      $$("#pv-ul li", box).forEach(function (li) {
        li.addEventListener("click", function () { pick(li.getAttribute("data-pv")); });
        li.addEventListener("keydown", function (e) {
          if (e.key === "Enter" || e.key === " ") { e.preventDefault(); pick(li.getAttribute("data-pv")); }
          if (e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
            e.preventDefault();
            var sib = e.key === "ArrowUp" ? li.previousElementSibling : li.nextElementSibling;
            if (!sib) return;
            li.parentNode.insertBefore(li, e.key === "ArrowUp" ? sib : sib.nextSibling); li.focus(); saveOrder();
          }
        });
        li.addEventListener("dragstart", function (e) { li.classList.add("dragging"); e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", li.getAttribute("data-pv")); });
        li.addEventListener("dragend", function () { li.classList.remove("dragging"); saveOrder(); });
      });
      var ul = $("#pv-ul", box);
      ul.addEventListener("dragover", function (e) {
        e.preventDefault();
        var drag = $(".dragging", ul); if (!drag) return;
        var after = $$("li:not(.dragging)", ul).filter(function (x) { var r = x.getBoundingClientRect(); return e.clientY < r.top + r.height / 2; })[0];
        if (after) ul.insertBefore(drag, after); else ul.appendChild(drag);
      });
      function saveOrder() {
        var names = $$("#pv-ul li", box).map(function (x) { return x.getAttribute("data-pv"); });
        api("/api/providers/order", { method: "POST", body: { names: names } }).then(function () { toast(T("pv.order_saved")); }).catch(fail);
      }
      $("#pv-add", box).addEventListener("click", function () { presetPicker(presets, function (pr) { provSel = "+new"; $$("#pv-ul li", box).forEach(function (li) { li.classList.remove("on"); }); providerDetail($("#pv-detail", box), null, pr, presetById, again); }); });
      if (provSel && byName[provSel]) providerDetail($("#pv-detail", box), byName[provSel], null, presetById, again);
    });
  }
  function presetPicker(presets, pick) {
    modal('<div class="card-head"><span class="ico">' + ic("key") + "</span><h2>" + esc(T("pv.add")) + '</h2></div><p class="hint">' + esc(T("pv.pick")) + '</p><input type="search" id="pr-q" placeholder="' + esc(T("pv.pick_search")) + '"><div class="grid g3 presets mt">' + presets.map(function (p, i) {
      return '<button type="button" class="card preset" data-pr="' + i + '" data-q="' + esc((p.name + " " + p.id).toLowerCase()) + '"><strong>' + esc(p.name) + "</strong><small><code>" + esc(p.base_url || (p.id === "custom" ? T("pv.custom_sub") : "api.anthropic.com")) + "</code></small>" + (p.model ? "<small>" + esc(p.model) + "</small>" : "") + "</button>";
    }).join("") + '</div><div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button></div>", function (root, close) {
      $("#pr-q", root).addEventListener("input", function (e) { var q = e.target.value.trim().toLowerCase(); $$("[data-pr]", root).forEach(function (b) { b.classList.toggle("hidden", q && b.getAttribute("data-q").indexOf(q) < 0); }); });
      $$("[data-pr]", root).forEach(function (b) { b.addEventListener("click", function () { close(); pick(presets[+b.getAttribute("data-pr")]); }); });
    });
  }
  function providerDetail(host, p, preset, presetById, done) {
    var creating = !p || p.dup;
    var base = p ? (presetById[p.preset] || null) : preset;
    var v = p || { name: preset.id === "custom" ? "" : preset.id, preset: preset.id, base_url: preset.base_url || "", auth_env: preset.auth_env || "ANTHROPIC_AUTH_TOKEN", model: preset.model || "", small_fast_model: preset.small_fast_model || "", opus_model: preset.opus_model || "", sonnet_model: preset.sonnet_model || "", haiku_model: preset.haiku_model || "", extra_env: preset.extra_env || {} };
    if (p && p.dup) v = Object.assign({}, p, { name: p.name + "-copy", key_set: false, key_source: "" });
    var envText = Object.keys(v.extra_env || {}).map(function (k) { return k + "=" + v.extra_env[k]; }).join("\n");
    var inp = function (n, val, extra) { return '<input name="' + n + '" value="' + esc(val || "") + '"' + (extra || "") + ">"; };
    var models = [["model", T("pv.m_main")], ["small_fast_model", T("pv.m_fast")], ["opus_model", "Opus"], ["sonnet_model", "Sonnet"], ["haiku_model", "Haiku"]];
    var link = base && base.source_url ? '<a class="link" href="' + esc(base.source_url) + '" target="_blank" rel="noopener noreferrer">' + ic("globe", "sm") + esc(T("pv.get_key")) + "</a>" : "";
    host.innerHTML = '<div class="pv-head"><div><h3>' + esc(creating ? (p ? T("pv.dup_title") : T("pv.add_named", { n: preset.name })) : v.name) + "</h3>" +
      (!creating ? '<small class="muted">' + (v.default ? esc(T("pv.default")) + " · " : "") + (v.used_by ? esc(plural(v.used_by, "tasks.count_one", "tasks.count_many")) : esc(T("pv.unused"))) + "</small>" : "") + "</div>" +
      (!creating ? '<div class="row-actions">' + (v.default ? "" : '<button class="btn sm" id="pd-def">' + esc(T("pv.make_default")) + "</button>") + '<button class="btn sm" id="pd-dup">' + esc(T("pv.dup")) + "</button>" + (v.default ? "" : '<button class="btn sm danger" id="pd-del">' + ic("trash", "sm") + esc(T("btn.remove")) + "</button>") + "</div>" : "") + "</div>" +
      '<form id="pvf" autocomplete="off">' +
      '<div class="form-row">' + fieldHtml(T("set.name"), inp("name", v.name, ' required maxlength="40" pattern="[a-z0-9][a-z0-9._\\-]{0,39}"'), T("pv.name_hint")) + fieldHtml(T("pv.auth"), '<select name="auth_env">' + opt("ANTHROPIC_AUTH_TOKEN", "Bearer (ANTHROPIC_AUTH_TOKEN)", v.auth_env !== "ANTHROPIC_API_KEY") + opt("ANTHROPIC_API_KEY", "x-api-key (ANTHROPIC_API_KEY)", v.auth_env === "ANTHROPIC_API_KEY") + "</select>") + "</div>" +
      '<label class="field"><span>Base URL</span><div class="in-group">' + inp("base_url", v.base_url, ' type="url" placeholder="https://api.anthropic.com"') + (base && base.base_url !== undefined ? '<button type="button" class="btn sm ghost" id="pd-reset" title="' + esc(base.base_url || "https://api.anthropic.com") + '">' + ic("rollback", "sm") + esc(T("pv.reset_url")) + "</button>" : "") + '</div><small class="hint">' + esc(T("pv.url_hint")) + "</small></label>" +
      '<label class="field"><span>' + esc(T("pv.api_key")) + " " + link + '</span><div class="in-group"><input name="api_key" type="password" autocomplete="new-password" spellcheck="false" placeholder="' + esc(!creating && v.key_set && v.key_source === "store" ? T("pv.key_keep", { l: v.key_last4 || "" }) : !creating && v.key_source === "env" ? T("pv.key_env") : T("pv.key_new")) + '"><button type="button" class="btn sm ghost" id="pd-eye" aria-pressed="false" aria-label="' + esc(T("pv.show_key")) + '">' + esc(T("pv.show")) + '</button></div><small class="hint">' + esc(T("pv.key_hint")) + "</small></label>" +
      (!creating && v.key_source === "store" ? '<label class="check"><input type="checkbox" name="clear_key"> ' + esc(T("pv.clear_key")) + "</label>" : "") +
      '<div class="field"><span>' + esc(T("pv.models")) + '</span><div class="model-list">' + models.map(function (m) { return '<div class="model-row"><label>' + esc(m[1]) + "</label>" + inp(m[0], v[m[0]], ' maxlength="128"') + '<button type="button" class="btn sm" data-check="' + m[0] + '"' + (creating ? " disabled" : "") + ">" + esc(T("pv.check")) + '</button><span class="mres" data-res="' + m[0] + '"></span></div>'; }).join("") + "</div>" + (creating ? '<small class="hint">' + esc(T("pv.check_after_save")) + "</small>" : "") + "</div>" +
      fieldHtml(T("pv.extra_env"), '<textarea name="extra_env" rows="2" placeholder="API_TIMEOUT_MS=600000">' + esc(envText) + "</textarea>", T("pv.extra_hint")) +
      '<div class="pv-test" id="pd-test"></div>' +
      '<div class="actions">' + (creating ? "" : '<button type="button" class="btn" id="pd-test-btn">' + ic("pulse", "sm") + esc(T("pv.test")) + "</button>") + '<button type="submit" class="btn" data-mode="save">' + esc(T("btn.save")) + '</button><button type="submit" class="btn primary" data-mode="test">' + ic("check", "sm") + esc(T("pv.save_test")) + "</button></div></form>";
    var f = $("#pvf", host), out = $("#pd-test", host), mode = "save";
    var runTest = function (name, model, el) {
      el.innerHTML = '<span class="muted">' + esc(T("pv.testing")) + "</span>";
      return api("/api/providers/" + enc(name) + "/test", { method: "POST", body: { model: model || "" } }).then(function (r) {
        el.innerHTML = r.ok ? '<span class="pill ok">' + ic("check", "sm") + esc(T("pv.test_ok", { ms: r.latency_ms, m: r.model || "" })) + "</span>" : '<span class="pill bad">' + esc(T("pv.test_bad", { s: r.status || "—" })) + '</span> <small class="err">' + esc(r.error || "") + "</small>";
      }).catch(function (e) { el.innerHTML = '<span class="pill bad">' + esc(e.message) + "</span>"; });
    };
    $("#pd-eye", host).addEventListener("click", function (e) { var k = f.api_key, on = k.type === "password"; k.type = on ? "text" : "password"; e.currentTarget.setAttribute("aria-pressed", on); e.currentTarget.textContent = T(on ? "pv.hide" : "pv.show"); });
    var rs = $("#pd-reset", host); if (rs) rs.addEventListener("click", function () { f.base_url.value = base.base_url || ""; f.base_url.focus(); });
    $$("[data-check]", host).forEach(function (b) { b.addEventListener("click", function () { var k = b.getAttribute("data-check"); if (!f[k].value.trim()) { toast(T("pv.model_empty"), true); return; } runTest(v.name, f[k].value.trim(), $('[data-res="' + k + '"]', host)); }); });
    var tb = $("#pd-test-btn", host); if (tb) tb.addEventListener("click", function () { runTest(v.name, "", out); });
    $$("[data-mode]", f).forEach(function (b) { b.addEventListener("click", function () { mode = b.getAttribute("data-mode"); }); });
    var dd = $("#pd-def", host); if (dd) dd.addEventListener("click", function () { api("/api/providers/" + enc(v.name) + "/default", { method: "POST", body: {} }).then(function () { toast(T("pv.default_set")); done(v.name); }).catch(fail); });
    var dp = $("#pd-dup", host); if (dp) dp.addEventListener("click", function () { var c = JSON.parse(JSON.stringify(p)); c.dup = true; providerDetail(host, c, null, presetById, done); });
    var dl = $("#pd-del", host); if (dl) dl.addEventListener("click", function () { confirmDialog({ title: T("pv.del_title"), body: T("pv.confirm_del", { n: v.name }), ok: T("btn.remove"), danger: true }).then(function (y) { if (y) api("/api/providers/" + enc(v.name), { method: "DELETE" }).then(function () { toast(T("pv.deleted")); provSel = null; done(); }).catch(fail); }); });
    f.addEventListener("submit", function (e) {
      e.preventDefault();
      var env = {}, bad = "";
      f.extra_env.value.split("\n").forEach(function (l) { l = l.trim(); if (!l) return; var i = l.indexOf("="); if (i < 1) { bad = l; return; } env[l.slice(0, i).trim()] = l.slice(i + 1).trim(); });
      if (bad) { toast(T("pv.env_bad", { l: bad }), true); return; }
      var body = { original_name: creating ? "" : v.name, name: f.name.value.trim(), preset: v.preset || "", base_url: f.base_url.value.trim(), auth_env: f.auth_env.value, model: f.model.value.trim(), small_fast_model: f.small_fast_model.value.trim(),
        opus_model: f.opus_model.value.trim(), sonnet_model: f.sonnet_model.value.trim(), haiku_model: f.haiku_model.value.trim(), extra_env: env, clear_key: !!(f.clear_key && f.clear_key.checked) };
      if (f.api_key.value) body.api_key = f.api_key.value;
      var testAfter = mode === "test";
      api("/api/providers", { method: "POST", body: body }).then(function (saved) {
        f.api_key.value = "";
        toast(T("pv.saved"));
        return done(saved.name).then(function () { if (testAfter) { var el = $("#pd-test"); if (el) runTest(saved.name, "", el); } });
      }).catch(fail);
    });
  }

  // ---------- task form ----------
  // ---------- schedule editor (simple / cron / manual) ----------
  var DOW = [1, 2, 3, 4, 5, 6, 0];
  function parseSched(c) {
    var s = { mode: "simple", kind: "daily", time: "09:00", every: 15, minute: 0, dow: 1, dom: 1, cron: c || "" };
    if (!c) { s.mode = "manual"; return s; }
    var p = c.trim().split(/\s+/), num = function (x) { return /^\d+$/.test(x); };
    var hm = function () { return ("0" + p[1]).slice(-2) + ":" + ("0" + p[0]).slice(-2); };
    if (p.length !== 5) { s.mode = "cron"; return s; }
    var m;
    if ((m = /^\*\/(\d+)$/.exec(p[0])) && p[1] === "*" && p[2] === "*" && p[3] === "*" && p[4] === "*") { s.kind = "minutes"; s.every = +m[1]; return s; }
    if (num(p[0]) && p[1] === "*" && p[2] === "*" && p[3] === "*" && p[4] === "*") { s.kind = "hourly"; s.minute = +p[0]; return s; }
    if (num(p[0]) && num(p[1]) && p[3] === "*") {
      s.time = hm();
      if (p[2] === "*" && p[4] === "*") { s.kind = "daily"; return s; }
      if (p[2] === "*" && p[4] === "1-5") { s.kind = "weekdays"; return s; }
      if (p[2] === "*" && /^[0-6]$/.test(p[4])) { s.kind = "weekly"; s.dow = +p[4]; return s; }
      if (num(p[2]) && p[4] === "*") { s.kind = "monthly"; s.dom = +p[2]; return s; }
    }
    s.mode = "cron"; s.unconvertible = true; return s;
  }
  function simpleCron(s) {
    var t = (s.time || "09:00").split(":"), h = +t[0] || 0, m = +t[1] || 0;
    switch (s.kind) {
      case "minutes": return "*/" + Math.min(59, Math.max(1, +s.every || 15)) + " * * * *";
      case "hourly": return Math.min(59, Math.max(0, +s.minute || 0)) + " * * * *";
      case "weekdays": return m + " " + h + " * * 1-5";
      case "weekly": return m + " " + h + " * * " + s.dow;
      case "monthly": return m + " " + h + " " + Math.min(28, Math.max(1, +s.dom || 1)) + " * *";
      default: return m + " " + h + " * * *";
    }
  }
  // Quick local syntax check so obvious mistakes show up while typing; the server has the final say.
  function cronSyntaxError(c) {
    var p = c.trim().split(/\s+/);
    if (p.length !== 5) return T("se.need5");
    var lim = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 7]];
    for (var i = 0; i < 5; i++) {
      var ok = p[i].split(",").every(function (part) {
        var m = /^(\*|(\d+)(?:-(\d+))?)(?:\/(\d+))?$/.exec(part);
        if (!m) return /^[A-Za-z]{3}(-[A-Za-z]{3})?$/.test(part) && (i === 3 || i === 4);
        var a = m[2] == null ? null : +m[2], b = m[3] == null ? null : +m[3];
        return (a == null || (a >= lim[i][0] && a <= lim[i][1])) && (b == null || (b >= lim[i][0] && b <= lim[i][1] && b >= a)) && (m[4] == null || +m[4] > 0);
      });
      if (!ok) return T("se.bad_field", { f: T("se.f_" + i), v: p[i] });
    }
    return "";
  }
  var previewCache = {};
  function scheduleEditor(host, cron, serverTz) {
    var s = parseSched(cron), tz = serverTz || state.tz || "", timer = null, seq = 0;
    var dowName = function (d) { return new Intl.DateTimeFormat(state.lang, { weekday: "long", timeZone: "UTC" }).format(new Date(Date.UTC(2023, 0, 1 + d))); };
    var browserTz = (Intl.DateTimeFormat().resolvedOptions().timeZone) || "";
    var tzs = [serverTz, browserTz, "UTC", "Asia/Shanghai", "America/Los_Angeles", "America/New_York", "Europe/London", "Europe/Berlin", "Asia/Tokyo"].filter(function (x, i, a) { return x && a.indexOf(x) === i; });
    host.innerHTML = '<div class="sched-ed"><div class="seg" role="tablist">' + [["simple", T("se.simple")], ["cron", "Cron"], ["manual", T("se.manual")]].map(function (m) { return '<button type="button" role="tab" data-mode="' + m[0] + '">' + esc(m[1]) + "</button>"; }).join("") + "</div>" +
      '<div class="se-pane" data-pane="simple"><div class="form-row sched"><select data-k="kind" aria-label="' + esc(T("tf.schedule")) + '">' + ["minutes", "hourly", "daily", "weekdays", "weekly", "monthly"].map(function (k) { return opt(k, T("se.k_" + k), k === s.kind); }).join("") + "</select>" +
      '<label class="inl" data-show="minutes">' + esc(T("se.every")) + ' <input type="number" min="1" max="59" data-k="every" value="' + esc(s.every) + '"> ' + esc(T("se.minutes")) + "</label>" +
      '<label class="inl" data-show="hourly">' + esc(T("se.at_minute")) + ' <input type="number" min="0" max="59" data-k="minute" value="' + esc(s.minute) + '"></label>' +
      '<select data-k="dow" data-show="weekly" aria-label="' + esc(T("se.weekday")) + '">' + DOW.map(function (d) { return opt(d, dowName(d), d === s.dow); }).join("") + "</select>" +
      '<label class="inl" data-show="monthly">' + esc(T("se.day")) + ' <input type="number" min="1" max="28" data-k="dom" value="' + esc(s.dom) + '"></label>' +
      '<input type="time" data-k="time" value="' + esc(s.time) + '" data-show="daily weekdays weekly monthly" aria-label="' + esc(T("se.time")) + '"></div></div>' +
      '<div class="se-pane" data-pane="cron"><input class="mono" data-k="cron" maxlength="200" spellcheck="false" placeholder="*/30 9-18 * * 1-5" value="' + esc(s.cron) + '" aria-describedby="se-err"><small class="hint">' + esc(T("se.cron_hint")) + '</small></div>' +
      '<div class="se-pane" data-pane="manual"><p class="muted">' + esc(T("tf.manual_hint")) + "</p></div>" +
      '<p class="field-err" id="se-err" role="alert"></p><p class="se-note muted"></p>' +
      '<div class="sched-prev"><div class="sp-head"><span class="sp-human"></span><label class="inl">' + esc(T("se.show_in")) + ' <select data-k="tz">' + tzs.map(function (z) { return opt(z, z + (z === serverTz ? " · " + T("se.server") : ""), z === tz); }).join("") + '</select></label></div><ul class="next"></ul></div></div>';
    var $k = function (k) { return $('[data-k="' + k + '"]', host); };
    var err = $("#se-err", host), note = $(".se-note", host), list = $(".next", host), human = $(".sp-human", host);
    function value() {
      if (s.mode === "manual") return "";
      if (s.mode === "cron") return $k("cron").value.trim();
      return simpleCron({ kind: $k("kind").value, every: $k("every").value, minute: $k("minute").value, dow: $k("dow").value, dom: $k("dom").value, time: $k("time").value });
    }
    function show(times, zone) {
      list.innerHTML = times.map(function (t) { return "<li>" + esc(new Intl.DateTimeFormat(state.lang, { timeZone: zone, weekday: "short", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(new Date(t))) + "</li>"; }).join("");
    }
    function preview() {
      var c = value();
      err.textContent = ""; note.textContent = "";
      $$("[data-mode]", host).forEach(function (b) { var on = b.getAttribute("data-mode") === s.mode; b.classList.toggle("active", on); b.setAttribute("aria-selected", on); });
      $$("[data-pane]", host).forEach(function (p) { p.classList.toggle("hidden", p.getAttribute("data-pane") !== s.mode); });
      var kind = $k("kind").value;
      $$("[data-show]", host).forEach(function (x) { x.classList.toggle("hidden", x.getAttribute("data-show").split(" ").indexOf(kind) < 0); });
      $(".sched-prev", host).classList.toggle("hidden", !c);
      if (s.mode === "cron" && s.unconvertible) note.textContent = T("se.cron_only");
      if (!c) { host.dispatchEvent(new CustomEvent("sched", { detail: { cron: "", ok: true } })); return; }
      var bad = cronSyntaxError(c);
      if (bad) { err.textContent = bad; list.innerHTML = ""; human.textContent = ""; host.dispatchEvent(new CustomEvent("sched", { detail: { cron: c, ok: false } })); return; }
      human.innerHTML = "<strong>" + esc(cronHuman(c)) + '</strong> <code class="muted">' + esc(c) + "</code>";
      var zone = $k("tz").value, key = c + "|" + zone;
      host.dispatchEvent(new CustomEvent("sched", { detail: { cron: c, ok: true } }));
      if (previewCache[key]) { show(previewCache[key], zone); return; }
      clearTimeout(timer);
      var my = ++seq;
      timer = setTimeout(function () {
        api("/api/scheduler/preview?cron=" + enc(c) + "&tz=" + enc(zone)).then(function (d) {
          previewCache[key] = d.times || [];
          if (my === seq) show(previewCache[key], zone);
        }).catch(function (e) { if (my === seq) { err.textContent = e.message; list.innerHTML = ""; host.dispatchEvent(new CustomEvent("sched", { detail: { cron: c, ok: false } })); } });
      }, 300);
    }
    $$("[data-mode]", host).forEach(function (b) { b.addEventListener("click", function () {
      var to = b.getAttribute("data-mode");
      if (to === s.mode) return;
      if (to === "simple") {
        var cur = s.mode === "cron" ? parseSched($k("cron").value.trim()) : null;
        if (cur && cur.mode !== "simple") { s.mode = "cron"; preview(); note.textContent = T("se.cron_only"); return; }
        if (cur) { $k("kind").value = cur.kind; $k("every").value = cur.every; $k("minute").value = cur.minute; $k("dow").value = cur.dow; $k("dom").value = cur.dom; $k("time").value = cur.time; }
        s.unconvertible = false;
      }
      if (to === "cron" && s.mode === "simple") $k("cron").value = value();
      s.mode = to; preview();
    }); });
    ["kind", "every", "minute", "dow", "dom", "time", "tz"].forEach(function (k) { $k(k).addEventListener("input", preview); $k(k).addEventListener("change", preview); });
    $k("cron").addEventListener("input", function () { s.unconvertible = parseSched($k("cron").value).mode === "cron"; preview(); });
    preview();
    return { value: value, valid: function () { return !err.textContent; } };
  }
  function taskFormModal(taskId) {
    var reqs = [api("/api/skills"), api("/api/settings")];
    if (taskId) reqs.push(api("/api/tasks/" + enc(taskId) + "/form"));
    Promise.all(reqs).then(function (r) {
      var skills = r[0] || [], st = r[1], tf = taskId ? r[2].form : { name: "", prompt: "", skill_id: "", skill_version_id: "", provider: st.default_provider, cron: "0 9 * * *", budget_usd: 0, enabled: true, timeout_minutes: 30 };
      modal('<div class="card-head"><span class="ico">' + ic("tasks") + "</span><h2>" + esc(T(taskId ? "tf.edit" : "tf.new")) + '</h2></div><form id="tf" class="tf">' +
        fieldHtml(T("tf.name"), '<input name="name" required maxlength="80" value="' + esc(tf.name) + '" placeholder="' + esc(T("tf.name_ph")) + '">') +
        fieldHtml(T("tf.prompt"), '<textarea name="prompt" required maxlength="32000" rows="5" placeholder="' + esc(T("tf.prompt_ph")) + '">' + esc(tf.prompt) + "</textarea>") +
        '<div class="form-row">' + fieldHtml(T("bt.skill"), '<select name="skill_id"><option value="">' + esc(T("tf.no_skill")) + "</option>" + skills.map(function (s) { return opt(s.id, s.name, s.id === tf.skill_id); }).join("") + "</select>") +
        fieldHtml(T("bt.skill_ver"), '<select name="skill_version_id"><option value="">' + esc(T("bt.latest")) + "</option></select>") + "</div>" +
        fieldHtml(T("f.provider"), '<select name="provider">' + st.providers.map(function (p) { return opt(p.name, p.name + (p.model ? " · " + p.model : "") + (p.key_set ? "" : " " + T("qr.no_key")), p.name === tf.provider); }).join("") + "</select>") +
        '<div class="field"><span>' + esc(T("tf.schedule")) + '</span><div id="sched-host"></div></div>' +
        '<div class="form-row">' + fieldHtml(T("tf.budget"), '<input name="budget_usd" type="number" min="0" max="10000" step="0.01" value="' + esc(tf.budget_usd || "") + '" placeholder="' + esc(T("tf.no_cap")) + '">', T("tf.budget_hint")) +
        fieldHtml(T("tf.timeout"), '<input name="timeout_minutes" type="number" min="1" max="1440" value="' + esc(tf.timeout_minutes || 30) + '">') + "</div>" +
        '<label class="check"><input type="checkbox" name="enabled"' + (tf.enabled ? " checked" : "") + "> " + esc(T("tf.enabled")) + "</label>" +
        '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T(taskId ? "btn.save" : "tf.create")) + "</button></div></form>", function (root, close) {
        var f = $("#tf", root);
        var loadVers = function () {
          var sel = f.skill_version_id;
          if (!f.skill_id.value) { sel.innerHTML = '<option value="">' + esc(T("bt.latest")) + "</option>"; sel.disabled = true; return; }
          sel.disabled = false;
          api("/api/skills/" + enc(f.skill_id.value) + "/versions").then(function (v) { sel.innerHTML = '<option value="">' + esc(T("bt.latest")) + "</option>" + v.versions.slice().reverse().map(function (x) { return opt(x.id, T("bt.pin_v", { n: x.no }), x.id === tf.skill_version_id); }).join(""); }).catch(fail);
        };
        var se = scheduleEditor($("#sched-host", root), tf.cron, state.tz);
        loadVers();
        f.skill_id.addEventListener("change", function () { tf.skill_version_id = ""; loadVers(); });
        f.addEventListener("submit", function (e) {
          e.preventDefault();
          if (!se.valid()) { $("#se-err", root).scrollIntoView({ block: "center" }); return; }
          var body = { name: f.name.value.trim(), prompt: f.prompt.value, skill_id: f.skill_id.value, skill_version_id: f.skill_version_id.value, provider: f.provider.value, cron: se.value(),
            budget_usd: parseFloat(f.budget_usd.value) || 0, enabled: f.enabled.checked, timeout_minutes: parseInt(f.timeout_minutes.value, 10) || 30 };
          var btn = f.querySelector(".btn.primary"); btn.disabled = true;
          api(taskId ? "/api/tasks/" + enc(taskId) : "/api/tasks", { method: taskId ? "PUT" : "POST", body: body }).then(function (t) {
            close(); toast(T(taskId ? "tf.saved" : "tf.created"));
            if (taskId) refresh(); else location.hash = "#/tasks/" + enc(t.id);
          }).catch(function (err) { btn.disabled = false; fail(err); });
        });
      });
    }).catch(fail);
  }

  // ---------- plugin install & triggers ----------
  function pluginInstallModal() {
    modal('<div class="card-head"><span class="ico">' + ic("plugins") + "</span><h2>" + esc(T("pl.install")) + "</h2></div>" + dropZone("pl-drop", ".zip,.tar.gz,.tgz", false) + '<div id="pl-prev"></div><div class="actions" id="pl-foot"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button></div>", function (root, close) {
      var prev = $("#pl-prev", root), foot = $("#pl-foot", root);
      wireDrop(root, "pl-drop", function (fd) {
        prev.innerHTML = '<p class="muted">' + esc(T("up.reading")) + "</p>";
        upload("/api/plugins/upload", fd).then(function (r) {
          var v = r.result || r, ver = v.version || {}, m = ver.manifest || {}, fresh = v.new_permissions || [], req = ver.requested_permissions || [];
          var trig = ((m.contributes || {}).triggers || []);
          prev.innerHTML = '<div class="preview"><p><strong>' + esc(m.name || m.id) + '</strong> <span class="tag">' + esc(m.version || "") + '</span> <code class="muted">' + esc(m.id || "") + "</code></p>" +
            '<p class="muted">' + esc(T("pl.hash", { h: String(ver.content_hash || "").slice(0, 16) })) + "</p>" +
            "<h4>" + esc(T("pl.perms")) + "</h4>" + (req.length ? permChooser(req, fresh) : '<p class="muted">' + esc(T("pl.no_perms")) + "</p>") +
            (trig.length ? "<h4>" + esc(T("pl.contrib_triggers")) + "</h4><p>" + trig.map(function (t) { return '<span class="pill plain">' + esc(t.kind + ": " + (t.title || t.id)) + "</span>"; }).join(" ") + "</p>" : "") +
            '<p class="hint">' + esc(T("pl.grant_hint")) + "</p></div>";
          foot.innerHTML = '<button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn primary" id="pl-ok">' + ic("shield", "sm") + esc(T("pl.approve")) + "</button>";
          $$("[data-close]", foot).forEach(function (b) { b.addEventListener("click", close); });
          $("#pl-ok", foot).addEventListener("click", function (e) {
            e.target.disabled = true;
            var granted = $$("[data-perm]", prev).filter(function (c) { return c.checked; }).map(function (c) { return c.value; });
            rpc("plugins.approve", { version_id: ver.id, permissions: granted }).then(function () { close(); toast(T("pl.installed_ok", { n: m.name || m.id })); refresh(); }).catch(function (err) { e.target.disabled = false; fail(err); });
          });
        }).catch(function (e) { prev.innerHTML = '<p class="notice bad">' + ic("x", "sm") + esc(e.message) + "</p>"; });
      });
    });
  }
  function triggerModal(plugins) {
    api("/api/tasks").then(function (tasks) {
      var ptrig = [];
      plugins.forEach(function (p) { ((((p.current || {}).manifest || {}).contributes || {}).triggers || []).forEach(function (t) { if (t.kind === "email" || t.kind === "im") ptrig.push({ plugin: p.id, pname: p.name, t: t }); }); });
      modal('<div class="card-head"><span class="ico">' + ic("globe") + "</span><h2>" + esc(T("tr.new")) + '</h2></div><form id="trf">' +
        fieldHtml(T("tr.kind"), '<select name="kind">' + opt("webhook", T("tr.k_webhook"), true) + opt("file_change", T("tr.k_file")) + (ptrig.length ? opt("plugin", T("tr.k_plugin")) : "") + "</select>") +
        fieldHtml(T("col.task"), '<select name="task_id" required>' + tasks.map(function (t) { return opt(t.id, t.name); }).join("") + "</select>", tasks.length ? "" : T("tr.no_tasks")) +
        '<div data-k="file_change" class="hidden">' + fieldHtml(T("tr.path"), '<input name="path" class="mono" placeholder="/home/me/inbox">', T("tr.path_hint")) + "</div>" +
        '<div data-k="plugin" class="hidden">' + fieldHtml(T("tr.ptrig"), '<select name="ptrig">' + ptrig.map(function (x, i) { return opt(i, x.pname + " · " + (x.t.title || x.t.id) + " (" + x.t.kind + ")"); }).join("") + "</select>") + "</div>" +
        '<p class="hint" data-k="webhook">' + esc(T("tr.webhook_hint")) + "</p>" +
        '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T("tr.create")) + "</button></div></form>", function (root, close) {
        var f = $("#trf", root);
        var sync = function () { $$("[data-k]", f).forEach(function (x) { x.classList.toggle("hidden", x.getAttribute("data-k") !== f.kind.value); }); };
        f.kind.addEventListener("change", sync); sync();
        f.addEventListener("submit", function (e) {
          e.preventDefault();
          var k = f.kind.value, body = { kind: k, task_id: f.task_id.value, config: {} };
          if (k === "file_change") body.config.path = f.path.value.trim();
          if (k === "plugin") { var x = ptrig[+f.ptrig.value]; body.kind = x.t.kind; body.plugin_id = x.plugin; body.config.trigger_id = x.t.id; }
          rpc("triggers.create", body).then(function (r) {
            close();
            if (r && r.token) {
              var url = location.origin + r.url_path;
              modal('<div class="card-head"><span class="ico">' + ic("key") + "</span><h2>" + esc(T("tr.token_title")) + '</h2></div><p class="notice">' + ic("info", "sm") + esc(T("tr.token_once")) + "</p>" +
                fieldHtml("URL", '<input readonly class="mono" value="' + esc(url) + '">') + fieldHtml(T("tr.token"), '<input readonly class="mono" id="tok" value="' + esc(r.token) + '">') +
                '<p class="hint">' + esc(T("tr.curl_hint")) + '</p><pre class="code">' + esc("curl -X POST -H 'Authorization: Bearer <token>' " + url) + '</pre><div class="actions"><button class="btn" id="tok-copy">' + esc(T("tr.copy")) + '</button><button class="btn primary" data-close>' + esc(T("tr.saved_it")) + "</button></div>", function (r2) {
                $$("[data-close]", r2).forEach(function (b) { b.addEventListener("click", function () { refresh(); }); });
                $("#tok-copy", r2).addEventListener("click", function () { var t = $("#tok", r2); t.select(); (navigator.clipboard ? navigator.clipboard.writeText(t.value) : Promise.resolve(document.execCommand("copy"))).then(function () { toast(T("tr.copied")); }); });
              });
            } else { toast(T("tr.created")); refresh(); }
          }).catch(fail);
        });
      });
    }).catch(fail);
  }


  // ---------- plugin permissions, config forms, trigger cards (I5) ----------
  var PERM_GROUPS = [["fs", /^fs\./], ["net", /^network:/], ["exec", /^host_api:/], ["secret", /^secret:/]];
  function permRisk(p) {
    if (/^fs\.write:|^secret:|^host_api:runs\.start$/.test(p) || /^network:\*/.test(p) || /^fs\.read:(\/|~)$/.test(p)) return "high";
    if (/^network:|^fs\.read:|^host_api:notify\.send$/.test(p)) return "med";
    return "low";
  }
  function permLabel(p) { var i = p.indexOf(":"); return { kind: p.slice(0, i), val: p.slice(i + 1) }; }
  function groupPerms(list) {
    return PERM_GROUPS.map(function (g) { return { key: g[0], items: list.filter(function (p) { return g[1].test(p); }) }; }).filter(function (g) { return g.items.length; });
  }
  function permChooser(req, fresh) {
    return '<div class="perm-groups">' + groupPerms(req).map(function (g) {
      return '<fieldset class="perm-g"><legend>' + ic(g.key === "fs" ? "folder" : g.key === "net" ? "globe" : g.key === "exec" ? "bolt" : "key", "sm") + esc(T("perm.g_" + g.key)) + "</legend>" + g.items.map(function (p) {
        var r = permRisk(p), l = permLabel(p);
        return '<label class="perm-row"><input type="checkbox" data-perm value="' + esc(p) + '"' + (r === "high" ? "" : " checked") + '><span class="grow"><span class="muted">' + esc(T("perm.k_" + l.kind.replace(".", "_"))) + "</span> <code>" + esc(l.val) + "</code>" + (fresh && fresh.indexOf(p) >= 0 ? ' <span class="pill warn sm">' + esc(T("pl.new_perm")) + "</span>" : "") + '</span><span class="risk ' + r + '">' + esc(T("perm.r_" + r)) + "</span></label>";
      }).join("") + "</fieldset>";
    }).join("") + "</div>";
  }
  function permCard(p) {
    var cur = p.current || {}, req = cur.requested_permissions || [], ok = cur.approved_permissions || [];
    if (!req.length) return '<p class="muted">' + esc(T("pl.no_perms")) + "</p>";
    return groupPerms(req).map(function (g) {
      return '<div class="perm-line"><small class="muted">' + esc(T("perm.g_" + g.key)) + "</small>" + g.items.map(function (x) {
        var on = ok.indexOf(x) >= 0, l = permLabel(x);
        return '<span class="perm-chip ' + (on ? "on " : "off ") + permRisk(x) + '"><code>' + esc(l.val) + "</code>" + '<button class="linkish" data-perm-t="' + esc(p.id) + '" data-perm-v="' + esc(x) + '" data-on="' + (on ? "1" : "") + '">' + esc(T(on ? "perm.revoke" : "perm.grant")) + "</button></span>";
      }).join("") + "</div>";
    }).join("");
  }
  function schemaForm(fields, values, secretsSet) {
    return fields.map(function (f) {
      var v = values[f.key] !== undefined ? values[f.key] : f.default, nm = ' name="' + esc(f.key) + '" data-f="' + esc(f.key) + '" data-t="' + esc(f.type) + '"';
      var lab = (f.title || f.key) + (f.required ? " *" : ""), inner;
      if (f.type === "boolean") return '<label class="check field-check"><input type="checkbox"' + nm + (v ? " checked" : "") + "> " + esc(lab) + (f.description ? '<small class="hint">' + esc(f.description) + "</small>" : "") + "</label>";
      if (f.type === "array") inner = '<div class="multi">' + f.items_enum.map(function (x) { return '<label class="check"><input type="checkbox" data-arr="' + esc(f.key) + '" value="' + esc(x) + '"' + ((v || []).map(String).indexOf(String(x)) >= 0 ? " checked" : "") + "> " + esc(x) + "</label>"; }).join("") + "</div>";
      else if (f.enum && f.enum.length) inner = "<select" + nm + ">" + (f.required ? "" : opt("", "—", v == null)) + f.enum.map(function (x) { return opt(x, x, String(x) === String(v)); }).join("") + "</select>";
      else if (f.write_only) inner = '<input type="password" autocomplete="new-password"' + nm + ' placeholder="' + esc(T(secretsSet[f.key] ? "cfg.secret_keep" : "cfg.secret_new")) + '"' + (f.max_length ? ' maxlength="' + f.max_length + '"' : "") + ">";
      else if (f.type === "number" || f.type === "integer") inner = '<input type="number"' + nm + (f.type === "integer" ? ' step="1"' : ' step="any"') + (f.minimum != null ? ' min="' + f.minimum + '"' : "") + (f.maximum != null ? ' max="' + f.maximum + '"' : "") + ' value="' + esc(v == null ? "" : v) + '"' + (f.required ? " required" : "") + ">";
      else inner = "<input" + nm + ' value="' + esc(v == null ? "" : v) + '"' + (f.max_length ? ' maxlength="' + f.max_length + '"' : ' maxlength="4000"') + (f.required ? " required" : "") + ">";
      return fieldHtml(lab, inner, f.description || (f.write_only ? T("cfg.secret_hint") : ""));
    }).join("");
  }
  function readSchemaForm(root, fields) {
    var out = {};
    fields.forEach(function (f) {
      if (f.type === "array") { out[f.key] = $$('[data-arr="' + f.key + '"]', root).filter(function (c) { return c.checked; }).map(function (c) { var n = Number(c.value); return f.items_enum.some(function (x) { return typeof x === "number"; }) && !isNaN(n) ? n : c.value; }); return; }
      var el = $('[data-f="' + f.key + '"]', root); if (!el) return;
      if (f.type === "boolean") { out[f.key] = el.checked; return; }
      if (el.value === "") return;
      out[f.key] = f.type === "number" || f.type === "integer" ? Number(el.value) : el.value;
    });
    return out;
  }
  function pluginConfigModal(p) {
    rpc("plugins.config_get", { id: p.id }).then(function (cv) {
      modal('<div class="card-head"><span class="ico">' + ic("settings") + "</span><h2>" + esc(T("cfg.title", { n: p.name })) + '</h2></div><form id="pcf" novalidate>' + schemaForm(cv.fields || [], cv.values || {}, cv.secrets_set || {}) + '<p class="notice bad hidden" id="pcf-err"></p><div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T("btn.save")) + "</button></div></form>", function (root, close) {
        var f = $("#pcf", root);
        f.addEventListener("submit", function (e) {
          e.preventDefault();
          if (!f.reportValidity()) return;
          rpc("plugins.config_set", { id: p.id, values: readSchemaForm(f, cv.fields || []) }).then(function () { close(); toast(T("cfg.saved")); }).catch(function (err) { var x = $("#pcf-err", root); x.textContent = err.message; x.classList.remove("hidden"); });
        });
      });
    }).catch(fail);
  }
  function copyText(t) { return (navigator.clipboard && window.isSecureContext ? navigator.clipboard.writeText(t) : new Promise(function (ok) { var ta = document.createElement("textarea"); ta.value = t; document.body.appendChild(ta); ta.select(); document.execCommand("copy"); ta.remove(); ok(); })).then(function () { toast(T("tr.copied")); }); }
  function triggerCard(t, tname) {
    var url = location.origin + "/hooks/" + t.id;
    var curl = "curl -X POST " + url + " \\\n  -H \"Authorization: Bearer $TIDEBOT_HOOK_TOKEN\" \\\n  -H \"Content-Type: application/json\" \\\n  -d '{\"hello\":\"world\"}'";
    var res = t.last_result || "", good = res === "accepted";
    return '<div class="card trig' + (t.enabled ? "" : " off") + '"><div class="trig-head"><span class="ico">' + ic(t.kind === "webhook" ? "globe" : t.kind === "file_change" ? "folder" : "bolt") + '</span><div class="grow"><strong>' + esc(T("tr.k_" + (t.kind === "file_change" ? "file" : t.kind === "webhook" ? "webhook" : "plugin"))) + '</strong><small><a href="#/tasks/' + enc(t.task_id) + '">' + esc(tname[t.task_id] || t.task_id) + "</a>" + (t.plugin_id ? " · " + esc(t.plugin_id) : "") + "</small></div>" +
      '<button class="switch' + (t.enabled ? " on" : "") + '" data-trg-en="' + esc(t.id) + '" role="switch" aria-checked="' + !!t.enabled + '" aria-label="' + esc(T("tr.enabled")) + '"></button></div>' +
      '<p class="trig-last">' + (t.last_fired_at ? esc(T("tr.last", { t: ago(t.last_fired_at), n: t.fire_count })) + ' <span class="pill sm ' + (good ? "ok" : "bad") + '">' + esc(good ? T("tr.r_ok") : res) + "</span>" : '<span class="muted">' + esc(T("tr.never")) + "</span>") + "</p>" +
      (t.kind === "webhook" ? '<div class="copy-row"><code class="grow">' + esc(url) + '</code><button class="btn sm" data-copy="' + esc(url) + '">' + esc(T("tr.copy_url")) + '</button></div><details class="curl"><summary>' + esc(T("tr.curl")) + '</summary><pre class="code">' + esc(curl) + '</pre><button class="btn sm" data-copy="' + esc(curl) + '">' + esc(T("tr.copy_curl")) + '</button><small class="hint">' + esc(T("tr.curl_token")) + "</small></details>" : t.kind === "file_change" ? '<p class="mono muted">' + esc((t.config || {}).path || "") + "</p>" : "") +
      '<div class="row-actions">' + (t.kind === "webhook" || t.kind === "file_change" ? '<button class="btn sm" data-trg-test="' + esc(t.id) + '"' + (t.enabled ? "" : " disabled") + ">" + ic("play", "sm") + esc(T("tr.test")) + "</button>" : "") + '<button class="btn sm ghost danger" data-trg-del="' + esc(t.id) + '">' + esc(T("pl.remove")) + "</button></div></div>";
  }

  // ---------- PLUGINS ----------
  function rpc(method, params) {
    return api("/api/rpc/" + method, { method: "POST", body: params || {} }).then(function (r) { return r.result; });
  }
  function renderPlugins() {
    return Promise.all([rpc("dashboard"), rpc("plugins.list"), rpc("triggers.list"), rpc("backup.list"), api("/api/tasks").catch(function () { return []; })]).then(function (res) {
      var d = res[0] || {}, plugins = res[1] || [], trig = res[2] || [], baks = res[3] || [], tname = {};
      (res[4] || []).forEach(function (t) { tname[t.id] = t.name; });
      var stat = function (label, v) { return '<div class="stat"><span class="muted">' + esc(label) + "</span><strong>" + esc(v) + "</strong></div>"; };
      var r24 = d.runs_24h || {}, n24 = 0; Object.keys(r24).forEach(function (k) { n24 += r24[k]; });
      var health = '<section class="card"><div class="card-head"><span class="ico">' + ic("pulse") + "</span><h2>" + esc(T("pl.health")) + '</h2></div><div class="grid g4">' +
        stat(T("pl.runs24"), n24) + stat(T("pl.success7"), Math.round((d.success_rate_7d || 0) * 100) + "%") +
        stat(T("pl.p95"), ((d.duration_p95_ms || 0) / 1000).toFixed(1) + " s") + stat(T("pl.cost7"), usd(d.cost_7d_usd || 0, 2)) +
        stat(T("pl.confirms"), d.pending_confirmations || 0) + stat(T("pl.claude"), d.claude_version || "?") +
        stat(T("pl.disk"), ((d.disk_bytes || 0) / 1048576).toFixed(1) + " MB") + stat(T("pl.backup"), d.last_backup ? fmtTime(d.last_backup) : T("td.none")) + "</div></section>";
      var prow = plugins.length ? '<div class="grid g2 pl-cards">' + plugins.map(function (p) {
        var cur = p.current || {}, m = cur.manifest || {}, hasCfg = m.config_schema && m.config_schema.properties && Object.keys(m.config_schema.properties).length;
        return '<div class="card plug' + (p.enabled ? "" : " off") + '"><div class="trig-head"><span class="ico">' + ic("plugins") + '</span><div class="grow"><strong>' + esc(p.name) + ' <span class="tag">' + esc(cur.version || "-") + '</span></strong><small class="mono">' + esc(p.id) + '</small></div><button class="switch' + (p.enabled ? " on" : "") + '" data-pl-en="' + esc(p.id) + '" role="switch" aria-checked="' + !!p.enabled + '" aria-label="' + esc(T("pl.enabled")) + '"></button></div>' +
          '<div class="perm-box"><small class="muted">' + esc(T("perm.granted_title")) + "</small>" + permCard(p) + "</div>" +
          '<div class="row-actions">' + (hasCfg ? '<button class="btn sm" data-pl-cfg="' + esc(p.id) + '">' + ic("settings", "sm") + esc(T("cfg.open")) + "</button>" : '<span class="muted sm-t">' + esc(T("cfg.none")) + "</span>") + '<span class="grow"></span><button class="btn sm ghost danger" data-pl-uninstall="' + esc(p.id) + '">' + esc(T("pl.uninstall")) + "</button></div></div>";
      }).join("") + "</div>" : emptyState("plugins", T("pl.empty"), T("pl.empty_sub"));
      var trow = trig.length ? '<div class="grid g2 trig-cards">' + trig.map(function (t) { return triggerCard(t, tname); }).join("") + "</div>" : '<p class="muted">' + esc(T("pl.no_triggers")) + "</p>";
      var brow = '<p class="muted">' + esc(baks.length ? T("pl.backups_n", { n: baks.length }) : T("pl.no_backups")) + "</p>";
      mount('<div class="page-head"><div><h1>' + esc(T("nav.plugins")) + "</h1><p>" + esc(T("pl.sub")) + '</p></div><div class="actions"><button class="btn" id="bk-now">' + esc(T("pl.backup_now")) + '</button><button class="btn primary" id="pl-add">' + ic("upload", "sm") + esc(T("pl.install")) + "</button></div></div>" + health +
        '<h2 class="sec-h">' + ic("plugins") + esc(T("pl.installed")) + "</h2>" + prow +
        '<h2 class="sec-h">' + ic("globe") + esc(T("pl.triggers")) + '<span class="grow"></span><button class="btn sm" id="tr-add">' + ic("plus", "sm") + esc(T("tr.new")) + "</button></h2>" + trow +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("receipt") + "</span><h2>" + esc(T("pl.backups")) + "</h2></div>" + brow + "</section>", "plugins", [[T("nav.plugins")]]);
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
      var byId = {}; plugins.forEach(function (p) { byId[p.id] = p; });
      $$("[data-pl-cfg]").forEach(function (b) { b.addEventListener("click", function () { pluginConfigModal(byId[b.getAttribute("data-pl-cfg")]); }); });
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

  // ---------- RUNS ----------
  var runsView = { q: "", sort: "new", seq: 0 };
  function renderRuns(params) {
    var st = params.get("status") || "", task = params.get("task") || "", v = runsView;
    var url = function () { return "/api/runs?limit=200&sort=" + enc(v.sort) + (st ? "&status=" + enc(st) : "") + (task ? "&task_id=" + enc(task) : "") + (v.q ? "&q=" + enc(v.q) : ""); };
    return api(url()).then(function (runs) {
      var f = [["", T("runs.all_f")], ["running", T("st.running")], ["succeeded", T("st.succeeded")], ["failed", T("st.failed")]];
      mount('<div class="page-head"><div><h1>' + esc(T("nav.runs")) + "</h1><p>" + esc(T("runs.sub")) + "</p></div></div>" +
        '<section class="card flush"><div class="toolbar"><label class="search-in">' + ic("search", "sm") + '<input id="rq" type="search" maxlength="100" placeholder="' + esc(T("runs.search")) + '" value="' + esc(v.q) + '"></label>' +
        '<div class="filters">' + f.map(function (x) { return '<button data-f="' + x[0] + '" class="' + (x[0] === st ? "active" : "") + '">' + esc(x[1]) + "</button>"; }).join("") + "</div>" +
        '<label class="inl">' + esc(T("runs.sort")) + ' <select id="rsort">' + [["new", T("runs.s_new")], ["old", T("runs.s_old")], ["cost", T("runs.s_cost")], ["tokens", T("runs.s_tokens")], ["duration", T("runs.s_duration")]].map(function (x) { return opt(x[0], x[1], x[0] === v.sort); }).join("") + "</select></label>" +
        '<span class="grow"></span><span class="muted">' + esc(task && runs[0] ? T("runs.for", { name: runs[0].task_name }) + " · " : "") + esc(T("runs.times_in", { tz: tzLabel() })) + '</span></div><div id="rtable"></div></section>', "runs", [[T("nav.runs")]]);
      var paint = function (list) {
        $("#rtable").innerHTML = list.length || !v.q ? runsTable(list, true) : emptyState("search", T("runs.no_match"), "");
        wireRows($("#rtable"));
        $$("[data-rerun]").forEach(function (b) { b.addEventListener("click", function (e) {
          e.stopPropagation(); b.disabled = true;
          api("/api/runs/" + enc(b.getAttribute("data-rerun")) + "/rerun", { method: "POST", body: {} }).then(function () { toast(T("runs.rerun_started")); setTimeout(reload, 600); }).catch(function (err) { b.disabled = false; fail(err); });
        }); });
      };
      // a newer request always wins; slow responses for old search text are dropped
      var reload = function () { var my = ++v.seq; return api(url()).then(function (list) { if (my === v.seq) { paint(list); live(list); } }); };
      var live = function (list) { if (list.some(function (r) { return r.status === "running" || r.status === "queued"; })) livePoll(reload, 2000); else stopPoll(); };
      paint(runs); live(runs);
      var qt;
      $("#rq").addEventListener("input", function (e) { clearTimeout(qt); qt = setTimeout(function () { v.q = e.target.value.trim(); reload().catch(fail); }, 250); });
      $("#rsort").addEventListener("change", function (e) { v.sort = e.target.value; reload().catch(fail); });
      $$("[data-f]").forEach(function (b) { b.addEventListener("click", function () { location.hash = "#/runs" + (b.getAttribute("data-f") ? "?status=" + b.getAttribute("data-f") : ""); }); });
    });
  }

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
      '<button class="icon-btn" id="lv-prev" aria-label="' + esc(T("lv.prev")) + '" title="' + esc(T("lv.prev")) + ' (N)">↑</button><button class="icon-btn" id="lv-next" aria-label="' + esc(T("lv.next")) + '" title="' + esc(T("lv.next")) + ' (n)">↓</button>' +
      '<button class="btn sm" id="lv-copy">' + esc(T("lv.copy")) + '</button><button class="btn sm" id="lv-dl">' + esc(T("lv.download")) + '</button><button class="icon-btn" id="lv-full" aria-label="' + esc(T("lv.full")) + '" title="' + esc(T("lv.full")) + '">⤢</button></div>' +
      '<div class="lv-wrap"><div class="log" id="log" aria-live="off" tabindex="0"><button class="lv-more hidden" id="lv-more"></button><div id="lv-rows"></div></div><button class="lv-new hidden" id="lv-new"></button></div>';
    var log = $("#log", host), rows = $("#lv-rows", host), more = $("#lv-more", host), newBtn = $("#lv-new", host);
    var atBottom = function () { return log.scrollTop + log.clientHeight >= log.scrollHeight - 24; };
    var rowHtml = function (l, idx) {
      var body = l.text, extra = "";
      if (l.kind === "tool") {
        var mm = /^assistant: tool (\S+)\s?(.*)$/.exec(body) || [null, "", body];
        body = "tool " + mm[1]; extra = mm[2];
      }
      return '<div class="ln k-' + l.kind + (idx === matches[cur] ? " cur" : "") + (extra ? " fold" : "") + '" data-i="' + idx + '">' + (l.ts ? '<span class="ts">' + esc(l.ts) + "</span>" : "") +
        '<span class="tx">' + hlText(body, q) + (extra ? '<span class="args">' + hlText(extra, q) + "</span>" : "") + "</span></div>";
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
    var qt;
    $("#lv-q", host).addEventListener("input", function (e) { clearTimeout(qt); qt = setTimeout(function () { q = e.target.value.trim().toLowerCase(); cur = -1; paint(true); if (q) jump(1); }, 150); });
    $("#lv-q", host).addEventListener("keydown", function (e) { if (e.key === "Enter") { e.preventDefault(); jump(e.shiftKey ? -1 : 1); } });
    $("#lv-next", host).addEventListener("click", function () { jump(1); });
    $("#lv-prev", host).addEventListener("click", function () { jump(-1); });
    host.addEventListener("keydown", function (e) {
      if (e.target.tagName === "INPUT") return;
      if (e.key === "n") { e.preventDefault(); jump(1); } else if (e.key === "N") { e.preventDefault(); jump(-1); }
      else if (e.key === "/") { e.preventDefault(); $("#lv-q", host).focus(); }
    });
    var jsonl = function () { return lines.map(function (l) { return JSON.stringify({ time: l.ts, kind: l.kind, text: l.text }); }).join("\n") + "\n"; };
    $("#lv-copy", host).addEventListener("click", function () { navigator.clipboard.writeText(jsonl()).then(function () { toast(T("lv.copied", { n: lines.length })); }, fail); });
    $("#lv-dl", host).addEventListener("click", function () {
      var a = document.createElement("a"); a.href = URL.createObjectURL(new Blob([jsonl()], { type: "application/x-ndjson" })); a.download = runId + ".jsonl";
      document.body.appendChild(a); a.click(); setTimeout(function () { URL.revokeObjectURL(a.href); a.remove(); }, 0);
    });
    var sec = host.closest("section");
    $("#lv-full", host).addEventListener("click", function () { sec.classList.toggle("log-full"); log.focus(); });
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
        if (es.readyState === 2 && document.contains(host)) { retry++; setTimeout(function () { if (!ended && document.contains(host)) connect(); }, Math.min(10000, 1000 * retry)); }
      };
    }
    state.esResume = function () { if (!ended && document.contains(host)) connect(); };
    connect();
    return { push: push, lines: lines };
  }
  function renderRunDetail(id) {
    return api("/api/runs/" + enc(id)).then(function (r) {
      var live = r.status === "running" || r.status === "queued";
      mount('<div class="grid g4">' +
        '<div class="card stat"><small>' + esc(T("col.status")) + "</small><strong>" + statusPill(r.status) + "</strong><span>" + esc(triggerText(r.trigger)) + "</span></div>" +
        '<div class="card stat"><small>' + esc(T("col.duration")) + "</small><strong>" + esc(live ? T("rd.running") : dur(r.duration_ms)) + "</strong><span>" + esc(T("rd.started", { t: when(r.started_at || r.created_at) })) + "</span></div>" +
        '<div class="card stat"><small>' + esc(T("col.cost")) + "</small><strong>" + esc(usd(r.cost_usd, 4)) + "</strong><span>" + esc(T("rd.bucket", { b: r.bucket })) + "</span></div>" +
        '<div class="card stat"><small>Tokens</small><strong>' + esc(num(r.input_tokens + r.output_tokens)) + "</strong><span>" + esc(T("rd.in_out", { i: num(r.input_tokens), o: num(r.output_tokens) })) + "</span></div></div>" +
        '<div class="grid g-side"><section class="card"><div class="card-head"><span class="ico">' + ic("terminal") + '</span><h2>' + esc(T("rd.log")) + '</h2><span class="link" id="live-ind">' + (live ? '<span class="live">' + esc(T("rd.live")) + "</span>" : '<span class="muted">' + esc(T("rd.finished")) + "</span>") + '</span></div><div id="lv"></div></section>' +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("info") + '</span><h3>' + esc(T("rd.details")) + '</h3></div><dl class="kv"><dt>' + esc(T("col.run")) + "</dt><dd><code>" + esc(r.id) + "</code></dd><dt>" + esc(T("col.task")) + "</dt><dd>" + (r.task_id ? '<a href="#/tasks/' + enc(r.task_id) + '">' + esc(r.task_name) + "</a>" + (r.version_no ? " · v" + esc(r.version_no) : "") : esc(T("run.adhoc"))) +
        "</dd><dt>" + esc(T("f.provider")) + "</dt><dd>" + esc(r.provider || "—") + "</dd><dt>" + esc(T("f.model")) + "</dt><dd>" + esc(r.model || "—") + "</dd><dt>" + esc(T("f.prompt")) + "</dt><dd>" + esc(r.prompt) + "</dd>" + (r.error ? "<dt>" + esc(T("rd.error")) + '</dt><dd class="err">' + esc(r.error) + "</dd>" : "") + "</dl></section></div>",
        "runs", [[T("nav.runs"), "#/runs"], [runTitle(r)]]);
      bumpIdle();
      logView($("#lv"), id, live, function (why) {
        if (why === "idle") { $("#live-ind").innerHTML = '<span class="muted">' + esc(T("rd.idle")) + "</span>"; return; }
        if (live) setTimeout(function () { if (location.hash === "#/runs/" + id) refresh(); }, 300);
      });
    });
  }
  // ---------- SPEND ----------
  var taskNames = {};
  function bucketName(b) { return b.scope === "global" ? T("sp.global") : b.scope === "bucket" ? (T("bucket." + b.scope_id) === "bucket." + b.scope_id ? cap(b.scope_id) : T("bucket." + b.scope_id)) : (taskNames[b.scope_id] || T("col.task") + " " + String(b.scope_id).slice(0, 12)); }
  var spendView = { range: "7d", by: "task" }, spendCache = {};
  function renderSpend() {
    spendCache = {};
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
        '<section class="card"><div class="card-head"><span class="ico">' + ic("receipt") + '</span><h2>' + esc(T("sp.breakdown")) + '</h2><div class="seg sm" id="sp-range" role="radiogroup">' + [["7d", T("sp.r7")], ["30d", T("sp.r30")], ["all", T("sp.rall")]].map(function (x) { return '<button type="button" role="radio" data-r="' + x[0] + '" class="' + (x[0] === spendView.range ? "active" : "") + '" aria-checked="' + (x[0] === spendView.range) + '">' + esc(x[1]) + "</button>"; }).join("") + "</div></div>" +
        '<nav class="tabs" role="tablist">' + [["task", T("sp.by_task")], ["model", T("sp.by_model")], ["provider", T("sp.by_provider")]].map(function (x) { return '<button role="tab" data-by="' + x[0] + '" class="' + (x[0] === spendView.by ? "active" : "") + '" aria-selected="' + (x[0] === spendView.by) + '">' + esc(x[1]) + "</button>"; }).join("") + '</nav><div id="sp-body" class="sp-body"></div></section>' +
        '<div class="grid g2"><section class="card fill"><div class="card-head"><span class="ico">' + ic("wallet") + '</span><h2>' + esc(T("sp.budgets")) + '</h2></div><div class="grow">' + budgets.map(function (b, k) {
          var p = pct(b.used_usd, b.max_usd);
          return '<div class="budget-row"><div><strong>' + esc(bucketName(b)) + '</strong> <span class="muted">· ' + esc(T("sp.per_" + b.period)) + '</span></div><div class="right">' + esc(usd(b.used_usd, 4)) + ' <span class="muted">/ ' + (b.max_usd ? esc(usd(b.max_usd)) : "∞") + '</span></div><div class="progress' + cls(p) + '" role="progressbar" aria-valuenow="' + Math.round(p) + '" aria-valuemin="0" aria-valuemax="100"><span data-w="' + p + '"></span></div>' +
            '<div class="budget-act"><label>' + esc(T("sp.on_breach")) + ' <select data-bact="' + k + '">' + opt("pause_notify", T("sp.a_pause"), b.action !== "block_notify") + opt("block_notify", T("sp.a_block"), b.action === "block_notify") + '</select></label><label>' + esc(T("sp.warn_at")) + ' <select data-bwarn="' + k + '">' + [50, 70, 80, 90, 100].map(function (n) { return opt(n, n + "%", (b.warn_at_pct || 80) === n); }).join("") + "</select></label></div></div>";
        }).join("") + '<p class="hint">' + esc(T("sp.breach_hint")) + "</p></div></section>" +
        '<section class="card flush fill"><div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>" + esc(T("sp.events")) + "</h2></div>" + (d.events.length ? '<table class="table"><thead><tr><th>' + esc(T("sp.when")) + "</th><th>" + esc(T("sp.scope")) + "</th><th>" + esc(T("sp.detail")) + "</th></tr></thead><tbody>" + d.events.map(function (e) { return '<tr><td class="nowrap">' + esc(when(e.at)) + '</td><td><span class="pill bad">' + esc(T("sp.scope_" + e.scope)) + "</span></td><td>" + esc(e.code ? T("be." + e.code, { name: e.name, used: e.used, cap: e.cap }) : e.detail) + "<small>" + esc(T("sp.action", { a: T("act." + e.action) === "act." + e.action ? e.action : T("act." + e.action) })) + "</small></td></tr>"; }).join("") + "</tbody></table>" : emptyState("shield", T("sp.no_breach"), T("sp.no_breach_sub"))) + "</section></div>",
        "spend", [[T("nav.spend")]]);
      applySizes(page());
      var paintBody = function (r) {
        var rows = r.rows || [], mxd = Math.max.apply(null, (r.days || []).map(function (x) { return x.usd; }).concat([0.0001]));
        var nm = function (x) { return spendView.by === "task" ? (x.key ? (x.name || taskNames[x.key] || x.key) : T("run.adhoc_paren")) : (x.key || T("sp.unknown")); };
        $("#sp-body").innerHTML = '<div class="sp-total"><strong>' + esc(usd(r.total_usd || 0, 4)) + '</strong> <span class="muted">' + esc(T("sp.since", { d: spendView.range === "all" ? T("sp.rall") : r.since })) + "</span></div>" +
          ((r.days || []).length ? '<div class="bars sp-bars">' + r.days.map(function (x) { return '<div class="bar" title="' + esc(x.day + " · " + usd(x.usd, 4)) + '"><span data-h="' + (x.usd / mxd * 100) + '"></span><small>' + esc(x.day.slice(5)) + "</small></div>"; }).join("") + "</div>" : "") +
          (rows.length ? '<table class="table"><thead><tr><th>' + esc(T("sp.col_" + spendView.by)) + "</th><th>" + esc(T("nav.runs")) + "</th><th>Tokens</th><th>" + esc(T("sp.share")) + '</th><th class="right">' + esc(T("col.cost")) + "</th></tr></thead><tbody>" + rows.map(function (x) {
            var sh = r.total_usd ? x.usd / r.total_usd * 100 : 0;
            return "<tr><td><strong>" + esc(nm(x)) + "</strong></td><td>" + esc(x.runs) + "</td><td>" + esc(num(x.tokens)) + '</td><td><div class="progress thin"><span data-w="' + sh + '"></span></div></td><td class="right"><strong>' + esc(usd(x.usd, 4)) + "</strong></td></tr>";
          }).join("") + "</tbody></table>" : emptyState("receipt", T("sp.none_range"), ""));
        applySizes($("#sp-body"));
      };
      // cached per range+tab: switching back is instant and never flashes a loader
      var load = function () {
        var key = spendView.range + "|" + spendView.by;
        if (spendCache[key]) paintBody(spendCache[key]); else if (!$("#sp-body").innerHTML) $("#sp-body").innerHTML = skeleton("list");
        return api("/api/spend/breakdown?range=" + spendView.range + "&by=" + spendView.by).then(function (r) { spendCache[key] = r; if (key === spendView.range + "|" + spendView.by) paintBody(r); }).catch(function (e) { $("#sp-body").innerHTML = errorState(e.message); });
      };
      $$("[data-r]").forEach(function (b) { b.addEventListener("click", function () { spendView.range = b.getAttribute("data-r"); $$("[data-r]").forEach(function (x) { x.classList.toggle("active", x === b); x.setAttribute("aria-checked", x === b); }); load(); }); });
      $$("[data-by]").forEach(function (b) { b.addEventListener("click", function () { spendView.by = b.getAttribute("data-by"); $$("[data-by]").forEach(function (x) { x.classList.toggle("active", x === b); x.setAttribute("aria-selected", x === b); }); load(); }); });
      var saveBudget = function (k) {
        var b = budgets[k];
        api("/api/budgets", { method: "POST", body: { scope: b.scope, scope_id: b.scope_id, period: b.period, bucket: b.bucket, max_usd: b.max_usd, max_tokens: b.max_tokens, action: $('[data-bact="' + k + '"]').value, warn_at_pct: +$('[data-bwarn="' + k + '"]').value } }).then(function () { toast(T("sp.budget_saved")); }).catch(fail);
      };
      $$("[data-bact],[data-bwarn]").forEach(function (sel) { sel.addEventListener("change", function () { saveBudget(+(sel.getAttribute("data-bact") || sel.getAttribute("data-bwarn"))); }); });
      load();
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
        '<section class="card"><div class="card-head"><span class="ico">' + ic("key") + '</span><h2>' + esc(T("set.providers")) + '</h2><span class="link muted">' + esc(T("pv.sub")) + '</span></div><div id="pv-box"><p class="muted">…</p></div></section>',
        "settings", [[T("nav.settings")]]);
      $("#set-lang").addEventListener("change", function (e) { setLanguage(e.target.value); });
      providerCards($("#pv-box")).catch(fail);
      $("#btn-logout").addEventListener("click", function () {
        api("/api/logout", { method: "POST", body: {} }).catch(function () {}).then(function () { state.csrf = ""; showLogin(); });
      });
      $("#sw-webui").addEventListener("click", function () {
        confirmDialog({ title: T("set.off_title"), body: T("set.confirm_off"), ok: T("set.turn_off"), danger: true }).then(function (y) { if (y) api("/api/webui", { method: "POST", body: { enabled: false } }).then(function () { toast(T("toast.webui_off")); }).catch(fail); });
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
  var lastHash = null;
  // refresh(): re-render the current page in place (keeps scroll and open modals closed state).
  function refresh() { return route(true); }
  function route(soft) {
    soft = soft === true;
    stopStream(); stopPoll();
    clearTimeout(scheduleRefresh._t);
    if (!soft) $("#modal-root").innerHTML = "";
    var y = window.scrollY, pg = page(), sy = pg ? pg.scrollTop : 0;
    if (!soft && location.hash !== lastHash && pg) pg.innerHTML = skeleton(/^#\/(buttons)?$/.test(location.hash || "#/") ? "cards" : "list");
    lastHash = location.hash;
    var nav0 = (location.hash.replace(/^#\/?/, "").split(/[/?]/)[0]) || "home";
    if (!soft) setNav(nav0);
    var h = location.hash.replace(/^#\/?/, "");
    var qi = h.indexOf("?"), params = new URLSearchParams(qi >= 0 ? h.slice(qi + 1) : "");
    var parts = (qi >= 0 ? h.slice(0, qi) : h).split("/").filter(Boolean).map(decodeURIComponent);
    var p;
    switch (parts[0]) {
      case undefined: p = renderHome(); break;
      case "tasks": p = parts[1] ? renderTaskDetail(parts[1], parts[2]) : renderTasks(); break;
      case "skills": p = parts[1] ? renderSkillDetail(parts[1], params) : renderSkills(); break;
      case "buttons": p = renderButtons(); break;
      case "plugins": p = renderPlugins(); break;
      case "runs": p = parts[1] ? renderRunDetail(parts[1]) : renderRuns(params); break;
      case "spend": p = renderSpend(); break;
      case "settings": p = renderSettings(); break;
      default: p = renderHome();
    }
    p = Promise.resolve(p).then(function () { if (soft) { window.scrollTo(0, y); if (page()) page().scrollTop = sy; } }, function (e) {
      if (!e.auth) { renderTopbar([]); page().innerHTML = errorState(e.message); $("#err-retry").addEventListener("click", function () { lastHash = null; route(); }); }
    });
    if (parts[0]) api("/api/overview").then(renderStatus).catch(function () {});
    return p;
  }
  window.addEventListener("hashchange", function () { route(); });

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
