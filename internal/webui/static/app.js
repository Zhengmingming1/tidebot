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

  // Icons: Lucide (ISC license, lucide.dev), 24px grid, drawn at 1.5px stroke.
  var P = {
    rss: '<path d="M4 11a9 9 0 0 1 9 9"/><path d="M4 4a16 16 0 0 1 16 16"/><circle cx="5" cy="19" r="1"/>',
    home: '<path d="M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8"/><path d="M3 10a2 2 0 0 1 .709-1.528l7-6a2 2 0 0 1 2.582 0l7 6A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
    tasks: '<path d="M13 5h8"/><path d="M13 12h8"/><path d="M13 19h8"/><path d="m3 17 2 2 4-4"/><path d="m3 7 2 2 4-4"/>',
    skills: '<path d="M11.017 2.814a1 1 0 0 1 1.966 0l1.051 5.558a2 2 0 0 0 1.594 1.594l5.558 1.051a1 1 0 0 1 0 1.966l-5.558 1.051a2 2 0 0 0-1.594 1.594l-1.051 5.558a1 1 0 0 1-1.966 0l-1.051-5.558a2 2 0 0 0-1.594-1.594l-5.558-1.051a1 1 0 0 1 0-1.966l5.558-1.051a2 2 0 0 0 1.594-1.594z"/><path d="M20 2v4"/><path d="M22 4h-4"/><circle cx="4" cy="20" r="2"/>',
    buttons: '<path d="M12.034 12.681a.498.498 0 0 1 .647-.647l9 3.5a.5.5 0 0 1-.033.943l-3.444 1.068a1 1 0 0 0-.66.66l-1.067 3.443a.5.5 0 0 1-.943.033z"/><path d="M21 11V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h6"/>',
    plugins: '<path d="M15.39 4.39a1 1 0 0 0 1.68-.474 2.5 2.5 0 1 1 3.014 3.015 1 1 0 0 0-.474 1.68l1.683 1.682a2.414 2.414 0 0 1 0 3.414L19.61 15.39a1 1 0 0 1-1.68-.474 2.5 2.5 0 1 0-3.014 3.015 1 1 0 0 1 .474 1.68l-1.683 1.682a2.414 2.414 0 0 1-3.414 0L8.61 19.61a1 1 0 0 0-1.68.474 2.5 2.5 0 1 1-3.014-3.015 1 1 0 0 0 .474-1.68l-1.683-1.682a2.414 2.414 0 0 1 0-3.414L4.39 8.61a1 1 0 0 1 1.68.474 2.5 2.5 0 1 0 3.014-3.015 1 1 0 0 1-.474-1.68l1.683-1.682a2.414 2.414 0 0 1 3.414 0z"/>',
    runs: '<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l4 2"/>',
    spend: '<path d="M3 3v16a2 2 0 0 0 2 2h16"/><path d="M18 17V9"/><path d="M13 17V5"/><path d="M8 17v-3"/>',
    wallet: '<path d="M19 7V4a1 1 0 0 0-1-1H5a2 2 0 0 0 0 4h15a1 1 0 0 1 1 1v4h-3a2 2 0 0 0 0 4h3a1 1 0 0 0 1-1v-2a1 1 0 0 0-1-1"/><path d="M3 5v14a2 2 0 0 0 2 2h15a1 1 0 0 0 1-1v-4"/>',
    settings: '<path d="M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915"/><circle cx="12" cy="12" r="3"/>',
    search: '<path d="m21 21-4.34-4.34"/><circle cx="11" cy="11" r="8"/>',
    refresh: '<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>',
    bell: '<path d="M10.268 21a2 2 0 0 0 3.464 0"/><path d="M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326"/>',
    calendar: '<path d="M8 2v3"/><path d="M16 2v3"/><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18"/>',
    receipt: '<path d="M12 17V7"/><path d="M16 8h-6a2 2 0 0 0 0 4h4a2 2 0 0 1 0 4H8"/><path d="M4 3a1 1 0 0 1 1-1 1.3 1.3 0 0 1 .7.2l.933.6a1.3 1.3 0 0 0 1.4 0l.934-.6a1.3 1.3 0 0 1 1.4 0l.933.6a1.3 1.3 0 0 0 1.4 0l.933-.6a1.3 1.3 0 0 1 1.4 0l.934.6a1.3 1.3 0 0 0 1.4 0l.933-.6A1.3 1.3 0 0 1 19 2a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1 1.3 1.3 0 0 1-.7-.2l-.933-.6a1.3 1.3 0 0 0-1.4 0l-.934.6a1.3 1.3 0 0 1-1.4 0l-.933-.6a1.3 1.3 0 0 0-1.4 0l-.933.6a1.3 1.3 0 0 1-1.4 0l-.934-.6a1.3 1.3 0 0 0-1.4 0l-.933.6a1.3 1.3 0 0 1-.7.2 1 1 0 0 1-1-1z"/>',
    pulse: '<path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2"/>',
    arrow: '<path d="M5 12h14"/><path d="m12 5 7 7-7 7"/>',
    chev: '<path d="m9 18 6-6-6-6"/>',
    info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>',
    folder: '<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>',
    doc: '<path d="M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 20 8v12a2 2 0 0 1-2 2z"/><path d="M14 2v5a1 1 0 0 0 1 1h5"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>',
    sync: '<path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16"/><path d="M16 16h5v5"/>',
    pie: '<path d="M21 12c.552 0 1.005-.449.95-.998a10 10 0 0 0-8.953-8.951c-.55-.055-.998.398-.998.95v8a1 1 0 0 0 1 1z"/><path d="M21.21 15.89A10 10 0 1 1 8 2.83"/>',
    trash: '<path d="M10 11v6"/><path d="M14 11v6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M3 6h18"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>',
    sparkle: '<path d="M11.017 2.814a1 1 0 0 1 1.966 0l1.051 5.558a2 2 0 0 0 1.594 1.594l5.558 1.051a1 1 0 0 1 0 1.966l-5.558 1.051a2 2 0 0 0-1.594 1.594l-1.051 5.558a1 1 0 0 1-1.966 0l-1.051-5.558a2 2 0 0 0-1.594-1.594l-5.558-1.051a1 1 0 0 1 0-1.966l5.558-1.051a2 2 0 0 0 1.594-1.594z"/><path d="M20 2v4"/><path d="M22 4h-4"/><circle cx="4" cy="20" r="2"/>',
    play: '<path d="M5 5a2 2 0 0 1 3.008-1.728l11.997 6.998a2 2 0 0 1 .003 3.458l-12 7A2 2 0 0 1 5 19z"/>',
    pause: '<rect x="14" y="3" width="5" height="18" rx="1"/><rect x="5" y="3" width="5" height="18" rx="1"/>',
    check: '<path d="M20 6 9 17l-5-5"/>',
    checkc: '<circle cx="12" cy="12" r="10"/><path d="m16 9-5.5 5.5L8 12"/>',
    circle: '<circle cx="12" cy="12" r="10"/>',
    x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
    send: '<path d="M14.536 21.686a.5.5 0 0 0 .937-.024l6.5-19a.496.496 0 0 0-.635-.635l-19 6.5a.5.5 0 0 0-.024.937l7.93 3.18a2 2 0 0 1 1.112 1.11z"/><path d="m21.854 2.147-10.94 10.939"/>',
    more: '<circle cx="12" cy="12" r="1"/><circle cx="12" cy="5" r="1"/><circle cx="12" cy="19" r="1"/>',
    branch: '<path d="M15 6a9 9 0 0 0-9 9V3"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/>',
    upload: '<path d="M12 3v12"/><path d="m17 8-5-5-5 5"/><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>',
    plus: '<path d="M5 12h14"/><path d="M12 5v14"/>',
    shield: '<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/>',
    globe: '<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>',
    key: '<path d="M2.586 17.414A2 2 0 0 0 2 18.828V21a1 1 0 0 0 1 1h3a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h1a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h.172a2 2 0 0 0 1.414-.586l.814-.814a6.5 6.5 0 1 0-4-4z"/><circle cx="16.5" cy="7.5" r=".5" fill="currentColor"/>',
    logout: '<path d="m16 17 5-5-5-5"/><path d="M21 12H9"/><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>',
    clock: '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>',
    user: '<path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',
    terminal: '<path d="m7 11 2-2-2-2"/><path d="M11 13h4"/><rect width="18" height="18" x="3" y="3" rx="2" ry="2"/>',
    bolt: '<path d="M15.914 4a1.5 1.5 0 00-2.474-1.561l-9 9A1.5 1.5 0 005.5 14h4.002a.5.5 0 01.471.666L8.086 20a1.5 1.5 0 002.475 1.56l9-9A1.5 1.5 0 0018.5 10h-3.997a.5.5 0 01-.472-.667z"/>',
    rollback: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 5.5 5.5a5.5 5.5 0 0 1-5.5 5.5H11"/>',
    mail: '<path d="m22 7-8.991 5.727a2 2 0 0 1-2.009 0L2 7"/><rect x="2" y="4" width="20" height="16" rx="2"/>',
    database: '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5V19A9 3 0 0 0 21 19V5"/><path d="M3 12A9 3 0 0 0 21 12"/>',
    code: '<path d="m16 18 6-6-6-6"/><path d="m8 6-6 6 6 6"/>',
    rocket: '<path d="M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5"/><path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09"/><path d="M9 12a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.4 22.4 0 0 1-4 2z"/><path d="M9 12H4s.55-3.03 2-4c1.62-1.08 5 .05 5 .05"/>',
    bug: '<path d="M12 20v-9"/><path d="M14 7a4 4 0 0 1 4 4v3a6 6 0 0 1-12 0v-3a4 4 0 0 1 4-4z"/><path d="M14.12 3.88 16 2"/><path d="M21 21a4 4 0 0 0-3.81-4"/><path d="M21 5a4 4 0 0 1-3.55 3.97"/><path d="M22 13h-4"/><path d="M3 21a4 4 0 0 1 3.81-4"/><path d="M3 5a4 4 0 0 0 3.55 3.97"/><path d="M6 13H2"/><path d="m8 2 1.88 1.88"/><path d="M9 7.13V6a3 3 0 1 1 6 0v1.13"/>',
    cloud: '<path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z"/>',
    camera: '<path d="M13.997 4a2 2 0 0 1 1.76 1.05l.486.9A2 2 0 0 0 18.003 7H20a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V9a2 2 0 0 1 2-2h1.997a2 2 0 0 0 1.759-1.048l.489-.904A2 2 0 0 1 10.004 4z"/><circle cx="12" cy="13" r="3"/>',
    book: '<path d="M12 5v16"/><path d="M20.001 19A2 2 0 0022 17V5a2 2 0 00-1.999-2L16 3.002A5 5 0 0012 5a5 5 0 00-4-2H4a2 2 0 00-2 2v12a2 2 0 001.999 2H8a5 5 0 014 2 5 5 0 014-2z"/>',
    box: '<path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z"/><path d="M12 22V12"/><polyline points="3.29 7 12 12 20.71 7"/><path d="m7.5 4.27 9 5.15"/>',
    git: '<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M13 6h3a2 2 0 0 1 2 2v7"/><line x1="6" x2="6" y1="9" y2="21"/>',
    chat: '<path d="M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z"/>',
    image: '<rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/>',
    link: '<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>',
    download: '<path d="M12 15V3"/><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/>',
    copy: '<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>',
    star: '<path d="M11.525 2.295a.53.53 0 0 1 .95 0l2.31 4.679a2.123 2.123 0 0 0 1.595 1.16l5.166.756a.53.53 0 0 1 .294.904l-3.736 3.638a2.123 2.123 0 0 0-.611 1.878l.882 5.14a.53.53 0 0 1-.771.56l-4.618-2.428a2.122 2.122 0 0 0-1.973 0L6.396 21.01a.53.53 0 0 1-.77-.56l.881-5.139a2.122 2.122 0 0 0-.611-1.879L2.16 9.795a.53.53 0 0 1 .294-.906l5.165-.755a2.122 2.122 0 0 0 1.597-1.16z"/>',
    flag: '<path d="M4 22V4a1 1 0 0 1 .4-.8A6 6 0 0 1 8 2c3 0 5 2 7.333 2q2 0 3.067-.8A1 1 0 0 1 20 4v10a1 1 0 0 1-.4.8A6 6 0 0 1 16 16c-3 0-5-2-8-2a6 6 0 0 0-4 1.528"/>',
    cpu: '<path d="M12 20v2"/><path d="M12 2v2"/><path d="M17 20v2"/><path d="M17 2v2"/><path d="M2 12h2"/><path d="M2 17h2"/><path d="M2 7h2"/><path d="M20 12h2"/><path d="M20 17h2"/><path d="M20 7h2"/><path d="M7 20v2"/><path d="M7 2v2"/><rect x="4" y="4" width="16" height="16" rx="2"/><rect x="8" y="8" width="8" height="8" rx="1"/>',
    server: '<rect width="20" height="8" x="2" y="2" rx="2" ry="2"/><rect width="20" height="8" x="2" y="14" rx="2" ry="2"/><line x1="6" x2="6.01" y1="6" y2="6"/><line x1="6" x2="6.01" y1="18" y2="18"/>',
    list: '<path d="M3 5h.01"/><path d="M3 12h.01"/><path d="M3 19h.01"/><path d="M8 5h13"/><path d="M8 12h13"/><path d="M8 19h13"/>',
    edit: '<path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/><path d="m15 5 4 4"/>',
    eye: '<path d="M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0"/><circle cx="12" cy="12" r="3"/>',
    lock: '<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>'
  };
  function ic(name, cls) { return '<svg class="i ' + (cls || "") + '" viewBox="0 0 24 24" aria-hidden="true">' + (P[name] || P.circle) + "</svg>"; }
  var BUTTON_ICONS = ["doc", "sync", "pie", "trash", "bolt", "terminal", "calendar", "pulse", "shield", "globe", "mail", "database", "code", "rocket", "bug", "cloud", "camera", "book", "box", "git", "chat", "image", "link", "download", "star", "cpu", "server", "edit"];
  function btnIcon(b) { return P[b.icon] ? b.icon : "bolt"; }

  var markN = 0;
  // Animated "flowing tide" brand mark (sidebar, login, boot splash). Pure SVG + CSS:
  // the two wave strokes are drawn wider than the tile with a 24-unit period and slide
  // left by exactly one period, so the loop is seamless. No JS timers; styles.css pauses
  // it when the window is hidden (html.tb-hidden) and drops it for prefers-reduced-motion.
  // The favicon / app icon (branding/mark.svg) stays static.
  function wavePath(y) { var d = "M-24 " + y; for (var i = 0; i < 5; i++) d += (i ? "s" : "c4-3 8-3 12 0s") + "8 3 12 0" + (i < 4 ? " 8-3 12 0" : ""); return d; }
  function waveMark() {
    var g = "tbg" + (markN++), c = "tbc" + markN;
    return '<svg class="tide" viewBox="0 0 64 64" aria-hidden="true"><defs><linearGradient id="' + g + '" x1="0" y1="0" x2=".35" y2="1"><stop offset="0" stop-color="#4c8df2"/><stop offset="1" stop-color="#1640a3"/></linearGradient><clipPath id="' + c + '"><rect width="64" height="64" rx="15"/></clipPath></defs>' +
      '<g clip-path="url(#' + c + ')"><rect width="64" height="64" fill="url(#' + g + ')"/><circle cx="45" cy="18" r="6" fill="#ffd98a"/>' +
      '<g class="tw-swell s1"><path class="tw tw1" d="' + wavePath(29) + '" fill="none" stroke="#fff" stroke-opacity=".6" stroke-width="4" stroke-linecap="round"/></g>' +
      '<g class="tw-swell s2"><path class="tw tw2" d="' + wavePath(38) + '" fill="none" stroke="#fff" stroke-width="4.4" stroke-linecap="round"/></g>' +
      '<path class="tw-sand" d="M0 48c8-3.6 16-4 24-2 8 2 16 2.4 40-1.6V64H0z" fill="#efe4cf"/></g></svg>';
  }
  document.addEventListener("visibilitychange", function () { document.documentElement.classList.toggle("tb-hidden", document.hidden); });
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
  // Relative times that stay true and explain themselves: the text reads "5 min ago",
  // the tooltip carries the full local date and zone (Open WebUI wraps dayjs fromNow()
  // in a Tooltip showing LLLL the same way), and one shared ticker rewrites every
  // [data-ago] once a minute while the window is visible, so a page left open never
  // says "just now" an hour later.
  function fullTime(iso) {
    var d = new Date(iso);
    if (!iso || isNaN(d)) return "";
    try { return new Intl.DateTimeFormat(state.lang, { timeZone: state.tz, dateStyle: "full", timeStyle: "short" }).format(d) + " · " + state.tz; }
    catch (e) { return fmtTime(iso) + " · " + state.tz; }
  }
  function agoHtml(iso) {
    var full = fullTime(iso);
    if (!full) return esc(ago(iso));
    return '<time class="ago" datetime="' + esc(new Date(iso).toISOString()) + '" data-ago="' + esc(iso) + '" data-tip="' + esc(full) + '">' + esc(ago(iso)) + "</time>";
  }
  // T() with one placeholder rendered as agoHtml(); everything else is escaped as usual.
  function tAgo(key, vars, name, iso) {
    var mark = "\u0001ago\u0001", v = Object.assign({}, vars || {});
    v[name] = mark;
    return esc(T(key, v)).split(mark).join(agoHtml(iso));
  }
  function tickAgo(root) {
    $$("[data-ago]", root).forEach(function (el) { var t = ago(el.getAttribute("data-ago")); if (el.textContent !== t) el.textContent = t; });
  }
  setInterval(function () { if (!document.hidden) tickAgo(); }, 30000);
  document.addEventListener("visibilitychange", function () { if (!document.hidden) tickAgo(); });
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
  // usd() formats an amount in the display currency (field names say "usd" for
  // API compatibility; the server converts at the user's fixed rate).
  function usd(v, d) { return money(v, state.currency || "USD", d); }
  function money(v, cur, d) {
    d = d == null ? 2 : d; cur = cur || "USD";
    try { return new Intl.NumberFormat(state.lang, { style: "currency", currency: cur, currencyDisplay: "narrowSymbol", minimumFractionDigits: d, maximumFractionDigits: d }).format(Number(v || 0)); }
    catch (e) { return (cur === "CNY" ? "¥" : "$") + Number(v || 0).toFixed(d); }
  }
  function curSym(cur) { return cur === "CNY" ? "¥" : "$"; }
  // cost source badge: priced runs need none; estimates / legacy / subscription get one
  function costBadge(src) {
    if (src === "estimate" || src === "claude" || src === "legacy") return ' <span class="pill warn sm" title="' + esc(T("cost.src_" + src + "_tip")) + '">' + esc(T("cost.est")) + "</span>";
    if (src === "subscription") return ' <span class="pill info sm">' + esc(T("cost.src_subscription")) + "</span>";
    if (src === "free") return ' <span class="pill sm">' + esc(T("cost.src_free")) + "</span>";
    return "";
  }
  function num(v) { return new Intl.NumberFormat(state.lang).format(Number(v || 0)); }
  function shortId(id) { var s = String(id || ""); return s.slice(-4).toUpperCase(); }
  function cap(s) { s = String(s || ""); return s.charAt(0).toUpperCase() + s.slice(1); }

  // ---------- api ----------
  // Stale-while-revalidate for page loads: GETs issued while a route renders
  // resolve at once from cache (no blank frame between tabs), then refetch in
  // the background; if the data changed the page re-renders softly (scroll and
  // focus kept). Any write clears the cache.
  var swr = { on: false, cache: {}, order: [], pending: {}, timer: null };
  function swrPut(path, data) {
    if (!swr.cache[path]) { swr.order.push(path); if (swr.order.length > 80) delete swr.cache[swr.order.shift()]; }
    var str = JSON.stringify(data), changed = swr.cache[path] && swr.cache[path].s !== str;
    swr.cache[path] = { s: str, t: Date.now() };
    return changed;
  }
  function swrRerender() {
    clearTimeout(swr.timer);
    swr.timer = setTimeout(function () {
      var a = document.activeElement, pg = page();
      if ($("#modal-root").innerHTML || (a && pg && pg.contains(a) && /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName))) return;
      route(true);
    }, 120);
  }
  function api(path, opts) {
    opts = opts || {};
    if ((opts.method || "GET") === "GET" && swr.on && !opts.fresh) {
      var c = swr.cache[path];
      if (c && swr.soft && Date.now() - c.t < 3000) return Promise.resolve(JSON.parse(c.s)); // just refreshed: no loop
      var net = swr.pending[path] || (swr.pending[path] = apiRaw(path, opts).then(function (d) { delete swr.pending[path]; if (swrPut(path, d)) swrRerender(); return d; }, function (e) { delete swr.pending[path]; throw e; }));
      if (c && Date.now() - c.t < 5 * 60 * 1000) { net.catch(function () {}); return Promise.resolve(JSON.parse(c.s)); }
      return net.then(function (d) { return JSON.parse(JSON.stringify(d)); });
    }
    return apiRaw(path, opts).then(function (d) {
      if ((opts.method || "GET") !== "GET") { swr.cache = {}; swr.order = []; }
      return d;
    });
  }
  function apiRaw(path, opts) {
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
  // Stable error codes from the core, shown in the viewer's language; anything else as-is.
  function runErr(msg) {
    msg = msg || "";
    if (/^interrupted: quit/.test(msg)) return T("rd.err_quit");
    return /^interrupted:/.test(msg) ? T("rd.err_interrupted") : msg;
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
    // A single crumb repeats the page title right below it: only detail pages get a trail.
    var c = crumbs && crumbs.length > 1 ? '<div class="crumbs">' + crumbs.map(function (x, i) {
      return (i ? '<span class="sep">' + ic("chev", "sm") + "</span>" : "") + (x[1] ? '<a href="' + esc(x[1]) + '">' + esc(x[0]) + "</a>" : "<span>" + esc(x[0]) + "</span>");
    }).join("") + "</div>" : "";
    var mac = /Mac|iPhone|iPad/.test(navigator.platform || "");
    $("#topbar").innerHTML = c + '<div class="spacer"></div>' +
      '<button class="search pal-open" id="q-open" aria-label="' + esc(T("top.search_label")) + '">' + ic("search") + '<span class="ph">' + esc(T("pal.placeholder")) + "</span><kbd>" + (mac ? "⌘" : "Ctrl") + " K</kbd></button>" +
      '<button class="lang-btn" id="btn-lang" title="' + esc(T("top.language")) + '" aria-label="' + esc(T("top.language")) + '">' + (state.lang === "zh-CN" ? "中" : "EN") + "</button>" +
      '<button class="icon-btn" id="btn-refresh" title="' + esc(T("top.refresh")) + '" aria-label="' + esc(T("top.refresh")) + '">' + ic("refresh") + "</button>" +
      '<div class="rel"><button class="icon-btn" id="btn-bell" title="' + esc(T("top.notifications")) + '" aria-label="' + esc(T("top.notifications")) + '" aria-haspopup="true">' + ic("bell") + '<span class="badge-dot hidden" id="bell-dot"></span></button><div id="notif" class="popover notif-center hidden"></div></div>';
    wireTopbar();
  }

  // Structured notifications (key + params) render in the viewer's language;
  // legacy rows only have server-rendered text and fall back to it.
  function notifText(n) {
    if (!n.key) return { t: notifTitle(n), b: n.body || "" };
    var p = {}, src = n.params || {};
    Object.keys(src).forEach(function (k) { p[k] = src[k]; });
    if (!p.name) p.name = T("ntf.quick_task");
    var bk = "ntf." + n.key + ".b", b = T(bk, p);
    return { t: T("ntf." + n.key + ".t", p), b: b === bk ? "" : b.trim() };
  }
  function notifTitle(n) { var k = "notif." + n.event, v = T(k); return v === k ? cap(n.event.replace(/_/g, " ")) : v; }
  function markAllRead(list) {
    if (list && list[0]) { state.notifSeen = list[0].created_at; try { localStorage.setItem("tb_notif_seen", state.notifSeen); } catch (e) {} }
    var dot = $("#bell-dot"); if (dot) dot.classList.add("hidden");
  }
  function renderNotifCenter(pop, data) {
    var items = data.items || [], pend = data.pending || [];
    var groups = { failed: [], confirm: [], other: [] };
    items.forEach(function (n) { (groups[n.group] || groups.other).push(n); });
    var unread = function (n) { return n.created_at > state.notifSeen; };
    var it = function (n) {
      return '<div class="item' + (unread(n) ? " unread" : "") + '"><strong>' + esc(notifText(n).t) + "</strong><div>" + esc(notifText(n).b) + '</div><small class="muted">' + esc(when(n.created_at)) + "</small></div>";
    };
    var sec = function (key, inner, count) {
      return '<div class="ngroup"><h5>' + esc(T("nc." + key)) + (count ? ' <span class="pill sm">' + esc(count) + "</span>" : "") + "</h5>" + (inner || '<div class="item muted">' + esc(T("nc.empty")) + "</div>") + "</div>";
    };
    var confirmInner = pend.map(function (p) {
      return '<a class="item link-item" href="#/tasks/' + enc(p.task_id) + '/thread">' + ic("sparkle", "sm") + "<span>" + esc(T("nc.pending_amend", { name: p.task_name || shortId(p.task_id), n: p.count })) + "</span></a>";
    }).join("") + groups.confirm.slice(0, 5).map(it).join("");
    var nconf = pend.reduce(function (a, p) { return a + p.count; }, 0) + groups.confirm.length;
    pop.innerHTML = '<div class="nc-head"><h4>' + esc(T("top.notifications")) + '</h4><button class="btn sm ghost" id="nc-read">' + esc(T("nc.mark_all")) + "</button></div>" +
      sec("failed", groups.failed.slice(0, 5).map(it).join(""), groups.failed.length) +
      sec("confirm", confirmInner, nconf) +
      sec("other", groups.other.slice(0, 5).map(it).join(""), 0);
    $("#nc-read", pop).addEventListener("click", function (e) { e.stopPropagation(); markAllRead(items); $$(".item.unread", pop).forEach(function (x) { x.classList.remove("unread"); }); toast(T("nc.marked")); });
    $$("a.link-item", pop).forEach(function (a) { a.addEventListener("click", function () { pop.classList.add("hidden"); }); });
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
    $("#q-open").addEventListener("click", function () { openPalette(); });
    $("#btn-refresh").addEventListener("click", function () { refresh(); });
    $("#btn-lang").addEventListener("click", function () {
      var next = state.lang === "zh-CN" ? "en" : "zh-CN";
      setLanguage(next);
    });
    $("#btn-bell").addEventListener("click", function (e) { e.stopPropagation(); toggleNotif(); });
    api("/api/notifications/center").then(function (d) {
      var dot = $("#bell-dot"), list = d.items || [];
      if (dot) dot.classList.toggle("hidden", !((list[0] && list[0].created_at > state.notifSeen) || (d.pending || []).length));
    }).catch(function () {});
  }
  document.addEventListener("click", function (e) { var p = $("#notif"); if (p && !p.contains(e.target)) p.classList.add("hidden"); });

  // ---------- command palette ----------
  // Ctrl/⌘+K opens it. Arrow keys move, Enter runs. Search responses that arrive
  // after a newer query was typed are dropped (seq check), so slow requests never
  // overwrite fresh results.
  var pal = { seq: 0, items: [], sel: 0, buttons: null, timer: null };
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
      { kind: "cmd", icon: "more", label: T("pal.shortcuts"), run: function () { shortcutsHelp(); } },
      { kind: "nav", icon: "home", label: T("nav.home"), run: go("#/") },
      { kind: "nav", icon: "tasks", label: T("nav.tasks"), run: go("#/tasks") },
      { kind: "nav", icon: "skills", label: T("nav.skills"), run: go("#/skills") },
      { kind: "nav", icon: "runs", label: T("nav.runs"), run: go("#/runs") },
      { kind: "nav", icon: "spend", label: T("nav.spend"), run: go("#/spend") },
      { kind: "nav", icon: "settings", label: T("nav.settings"), run: go("#/settings") }
    ].concat(SET_SECTIONS.map(function (k) { return { kind: "nav", icon: "settings", label: T("nav.settings") + " › " + T("setnav." + k), run: go("#/settings/" + k) }; }));
  }
  function palMatch(label, q) { return !q || label.toLowerCase().indexOf(q.toLowerCase()) >= 0; }
  function openPalette() {
    var root = $("#pal-root");
    if (!root) { root = document.createElement("div"); root.id = "pal-root"; document.body.appendChild(root); }
    if (root.innerHTML) { var qi = $("#pal-q"); if (qi) qi.focus(); return; }
    var opener = document.activeElement;
    root.innerHTML = '<div class="modal-bg pal-bg"><div class="card palette" role="dialog" aria-modal="true" aria-label="' + esc(T("pal.title")) + '">' +
      '<div class="pal-in">' + ic("search") + '<input id="pal-q" type="text" autocomplete="off" maxlength="200" role="combobox" aria-expanded="true" aria-controls="pal-list" aria-autocomplete="list" placeholder="' + esc(T("pal.placeholder")) + '"></div>' +
      '<ul id="pal-list" role="listbox"></ul><div class="pal-foot muted"><span><kbd>↑</kbd><kbd>↓</kbd> ' + esc(T("pal.k_move")) + "</span><span><kbd>Enter</kbd> " + esc(T("pal.k_run")) + "</span><span><kbd>Esc</kbd> " + esc(T("pal.k_close")) + "</span><span><kbd>?</kbd> " + esc(T("pal.shortcuts")) + "</span></div></div></div>";
    var input = $("#pal-q");
    var close = function () { root.innerHTML = ""; pal.seq++; document.removeEventListener("keydown", onKey, true); if (opener && opener.focus && document.contains(opener)) opener.focus(); };
    pal.close = close;
    var onKey = function (e) {
      if (!root.innerHTML) return;
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(); }
      else if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); palSelect(pal.sel + (e.key === "ArrowDown" ? 1 : -1)); }
      else if (e.key === "Enter") { e.preventDefault(); palRun(pal.sel); }
      else if (e.key === "Tab") { e.preventDefault(); input.focus(); }
    };
    document.addEventListener("keydown", onKey, true);
    $(".pal-bg", root).addEventListener("mousedown", function (e) { if (e.target.classList.contains("pal-bg")) close(); });
    input.addEventListener("input", function () { palUpdate(input.value.trim()); });
    if (!pal.buttons) api("/api/buttons").then(function (l) { pal.buttons = l || []; if (root.innerHTML) palUpdate(input.value.trim()); }).catch(function () { pal.buttons = []; });
    palUpdate("");
    input.focus();
  }
  function palUpdate(q) {
    var local = palCommands().filter(function (c) { return palMatch(c.label, q); });
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
        hits.forEach(function (h) {
          if (h.kind === "task") {
            extra.push({ kind: "task", icon: "tasks", label: h.name, sub: h.sub, run: function () { location.hash = "#/tasks/" + enc(h.id); } });
            extra.push({ kind: "run_task", icon: "play", label: T("pal.run_task", { name: h.name }), run: function () {
              api("/api/tasks/" + enc(h.id) + "/run", { method: "POST", body: {} }).then(function () { toast(T("toast.run_started")); }).catch(fail);
            } });
          } else if (h.kind === "skill") extra.push({ kind: "skill", icon: "skills", label: h.name, sub: h.sub, run: function () { location.hash = "#/skills/" + enc(h.id); } });
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
      return '<li role="option" id="pal-o' + i + '" data-i="' + i + '" aria-selected="' + (i === pal.sel) + '" class="' + (i === pal.sel ? "sel" : "") + '">' + ic(it.icon, "sm") + '<span class="kind">' + esc(T("pal.kind." + it.kind)) + "</span><span class=\"lbl\">" + esc(it.label) + "</span>" + (it.sub ? '<span class="sub">' + esc(it.sub) + "</span>" : "") + (it.keys ? '<span class="pal-keys">' + it.keys.split(" ").map(function (k) { return "<kbd>" + esc(k) + "</kbd>"; }).join("") + "</span>" : "") + "</li>";
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
    var li = $("#pal-o" + pal.sel); if (li && li.scrollIntoView) li.scrollIntoView({ block: "nearest" });
  }
  function palRun(i) {
    var it = pal.items[i];
    if (!it) return;
    if (pal.close) pal.close();
    it.run();
  }
  function shortcutsHelp() {
    var mod = /Mac|iPhone|iPad/.test(navigator.platform || "") ? "⌘" : "Ctrl";
    var rows = [[mod + " K", T("sc.palette")], ["?", T("sc.help")], ["↑ ↓", T("sc.move")], ["Enter", T("sc.run")], ["Esc", T("sc.close")], ["Alt ← →", T("sc.reorder")], ["g h / g t / g r / g c", T("sc.goto")], ["n t / n s / n p / n b / n q", T("sc.create")], ["r / e", T("sc.task_keys")]];
    modal('<div class="card-head"><span class="ico">' + ic("more") + "</span><h2>" + esc(T("sc.title")) + '</h2></div><table class="tbl sc-table"><tbody>' +
      rows.map(function (r) { return "<tr><td>" + r[0].split(" / ").map(function (k) { return "<kbd>" + esc(k) + "</kbd>"; }).join(" / ") + "</td><td>" + esc(r[1]) + "</td></tr>"; }).join("") +
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
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); openPalette(); return; }
    var t = e.target, typing = t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable);
    if (typing || e.metaKey || e.ctrlKey || e.altKey || $("#modal-root").innerHTML || ($("#pal-root") && $("#pal-root").innerHTML)) return;
    if (e.key === "?") { e.preventDefault(); shortcutsHelp(); return; }
    if (gPending && Date.now() - gPending < 1000) {
      var lead = gPending.k;
      gPending = 0;
      if (lead === "g") {
        var dest = { h: "#/", t: "#/tasks", r: "#/runs", s: "#/skills", b: "#/buttons", p: "#/plugins", c: "#/spend", ",": "#/settings" }[e.key];
        if (dest) { e.preventDefault(); location.hash = dest; }
      } else {
        // "n x" creates (same scheme as Plane's Power K: n i / n p …).
        var mk = { t: function () { taskFormModal(null); }, s: function () { skillImportModal(function () { location.hash = "#/skills"; refresh(); }); }, p: function () { goClick("#/settings/providers", "#pv-add"); }, b: function () { goClick("#/buttons", "#b-add"); }, q: function () { quickRunModal(); } }[e.key];
        if (mk) { e.preventDefault(); mk(); }
      }
      return;
    }
    if (e.key === "g" || e.key === "n") { gPending = { k: e.key, valueOf: function () { return this.t; }, t: Date.now() }; return; }
    // page-level single keys
    var act = { r: "#t-run", e: "#t-edit" }[e.key];
    if (act && $(act)) { e.preventDefault(); $(act).click(); }
  });

  // ---------- sidebar: Claude Code status ----------
  // GET /api/claude/status is cached server-side (30s) and never calls a model API.
  // We refresh it on navigation (at most every 15s) and every 30s; "Recheck" forces it.
  var cc = { st: null, at: 0, err: false, prevState: "", timer: null };
  function renderStatus() { loadCC(false); }
  function fmtMem(b) { return b ? (b >= 1073741824 ? (b / 1073741824).toFixed(1) + " GB" : Math.round(b / 1048576) + " MB") : "—"; }
  function loadCC(force) {
    if (!force && cc.st && Date.now() - cc.at < 15000) { paintCC(); return Promise.resolve(cc.st); }
    var p = force ? api("/api/claude/status", { method: "POST", body: {} }) : api("/api/claude/status");
    return p.then(function (st) { cc.st = st; cc.err = false; cc.at = Date.now(); paintCC(); return st; })
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
    box.innerHTML = '<button class="cc-card cc-' + v.state + '" id="cc-btn" aria-haspopup="dialog" aria-expanded="false" title="' + esc(v.head + (v.sub ? " · " + v.sub : "")) + '">' +
      '<span class="dot" aria-hidden="true"></span><span class="cc-txt"><strong>' + esc(v.head) + "</strong>" + (v.sub ? "<small>" + esc(v.sub) + "</small>" : "") + "</span></button>" +
      '<span class="sr-only" aria-live="polite" id="cc-live"></span>';
    if (cc.prevState && cc.prevState !== v.state) $("#cc-live").textContent = v.head;
    cc.prevState = v.state;
    $("#cc-btn").addEventListener("click", function (e) { e.stopPropagation(); toggleCCPop(); });
    if ($("#cc-pop")) fillCCPop();
  }
  // ---------- tab title ----------
  // "<status> <page> – tidebot", the shape of n8n's useDocumentTitle (status prefix +
  // name + product): a background tab or taskbar entry shows that runs are going
  // ("▶ 2 running") or that Claude Code / the provider needs a look ("⚠").
  // Fed by the Claude Code status poll and, on Home, by the live run list.
  var docTitle = { page: "", running: 0, alert: false };
  function setDocTitle(o) {
    o = o || {};
    if (o.page != null) docTitle.page = o.page;
    if (o.running != null) docTitle.running = o.running;
    if (o.alert != null) docTitle.alert = o.alert;
    var pre = docTitle.running > 0 ? "▶ " + T("home.n_running", { n: docTitle.running }) + " · " : docTitle.alert ? "⚠ " : "";
    var t = pre + (docTitle.page ? docTitle.page + " – " : "") + "tidebot";
    if (document.title !== t) document.title = t;
  }
  function toggleCCPop() {
    var old = $("#cc-pop");
    if (old) { old.remove(); $("#cc-btn").setAttribute("aria-expanded", "false"); return; }
    var pop = document.createElement("div");
    pop.id = "cc-pop"; pop.className = "popover cc-pop"; pop.setAttribute("role", "dialog"); pop.setAttribute("aria-label", T("cc.title"));
    document.body.appendChild(pop);
    $("#cc-btn").setAttribute("aria-expanded", "true");
    fillCCPop();
    var off = function (e) { if (!pop.contains(e.target) && !(e.target.closest && e.target.closest("#cc-btn"))) { pop.remove(); document.removeEventListener("click", off); document.removeEventListener("keydown", esc1); } };
    var esc1 = function (e) { if (e.key === "Escape") { pop.remove(); document.removeEventListener("click", off); document.removeEventListener("keydown", esc1); var b = $("#cc-btn"); if (b) b.focus(); } };
    setTimeout(function () { document.addEventListener("click", off); document.addEventListener("keydown", esc1); }, 0);
    loadCC(false);
  }
  function fillCCPop() {
    var pop = $("#cc-pop"), st = cc.st, v = ccView();
    if (!pop) return;
    var btn = $("#cc-btn"); if (btn) { var r = btn.getBoundingClientRect(); pop.style.left = Math.round(r.left) + "px"; pop.style.bottom = Math.round(window.innerHeight - r.top + 8) + "px"; }
    var kv = function (k, val, cls) { return "<dt>" + esc(k) + '</dt><dd class="' + (cls || "") + '">' + val + "</dd>"; };
    var h = '<div class="cc-pop-head"><span class="dot cc-' + v.state + '"></span><strong>' + esc(v.head) + "</strong></div>";
    if (!st) { h += '<p class="muted">' + esc(T(cc.err ? "cc.s_down" : "cc.h_checking")) + "</p>"; }
    else {
      if (!st.found) {
        var cmd = "npm i -g @anthropic-ai/claude-code";
        h += '<p class="cc-hint">' + esc(T("cc.install_hint")) + '</p><div class="cc-cmd"><code>' + esc(cmd) + '</code><button class="btn sm" id="cc-copy">' + esc(T("tf.copy")) + "</button></div>" +
          '<p class="muted small">' + esc(T("cc.install_after")) + "</p>";
        if (st.note) h += '<p class="cc-err small">' + esc(st.note) + "</p>";
        if (st.searched && st.searched.length) h += '<details class="cc-searched"><summary>' + esc(T("cc.searched", { n: st.searched.length })) + "</summary><ul>" +
          st.searched.map(function (x) { return "<li><code>" + esc(x) + "</code></li>"; }).join("") + "</ul></details>";
      }
      h += '<div class="cc-pathset"><label for="cc-path-in">' + esc(T("cc.set_path")) + '</label><div class="cc-path-row"><input id="cc-path-in" spellcheck="false" placeholder="/opt/homebrew/bin/claude" value="' + esc(st.custom_path ? st.path || "" : "") + '">' +
        '<button class="btn sm" id="cc-pick" type="button">' + esc(T("cc.pick")) + '</button><button class="btn sm primary" id="cc-path-save" type="button">' + esc(T("btn.save")) + "</button></div>" +
        (st.custom_path ? '<button class="link-btn small" id="cc-path-auto" type="button">' + esc(T("cc.path_auto")) + "</button>" : "") + '<p class="err small" id="cc-path-err" role="alert"></p></div>';
      var pv = st.provider || {};
      var health = pv.health === "ok" ? '<span class="pill ok sm">' + esc(T("cc.p_ok")) + "</span>" : pv.health === "failing" ? '<span class="pill bad sm">' + esc(T("cc.p_failing")) + "</span>" : '<span class="pill plain sm">' + esc(T("cc.p_unknown")) + "</span>";
      var checked = pv.checked_at ? esc(T(pv.source === "test" ? "cc.by_test" : "cc.by_run", { t: when(pv.checked_at) })) + (pv.latency_ms ? " · " + esc(pv.latency_ms) + " ms" : "") : esc(T("cc.never_checked"));
      h += '<dl class="cc-kv">' +
        (st.found ? kv(T("cc.path"), "<code>" + esc(st.path) + "</code>") : "") +
        (st.found && st.method ? kv(T("cc.found_by"), esc(T("cc.m_" + st.method))) : "") +
        (st.found ? kv(T("cc.version"), esc(st.version || T("cc.unknown")) + (st.compatible ? "" : ' <span class="pill warn sm">' + esc(T("cc.too_old")) + "</span>")) : "") +
        kv(T("cc.min"), esc(st.min_version)) +
        kv(T("cc.provider"), esc(pv.name || "—") + (pv.model ? ' <span class="muted">· ' + esc(pv.model) + "</span>" : "") + " " + health) +
        kv(T("cc.last_check"), checked) +
        (!pv.key_set && pv.health !== "ok" ? '<dd class="cc-hint2 small">' + esc(T("cc.no_key_hint")) + "</dd>" : "") +
        (pv.health === "failing" && pv.detail ? kv(T("cc.error"), esc(pv.detail), "cc-err") : "") +
        kv(T("cc.core"), esc((st.core.version || "dev") + " · " + T("cc.uptime", { t: dur(Math.max(1, st.core.uptime_sec) * 1000) }))) + "</dl>";
      h += '<h5 class="cc-sub">' + esc(T("cc.sessions", { n: st.sessions.length })) + (st.total_rss_bytes ? ' <span class="muted">· ' + esc(fmtMem(st.total_rss_bytes)) + "</span>" : "") + "</h5>";
      h += st.sessions.length ? '<ul class="cc-sess">' + st.sessions.map(function (x) {
        return '<li><a href="#/runs/' + enc(x.run_id) + '"><span class="spin" aria-hidden="true"></span><span class="nm">' + esc(x.task_name || T("run.adhoc")) + '</span><span class="num muted">' + esc(dur(x.duration_ms)) + '</span><span class="num muted">' + esc(fmtMem(x.rss_bytes)) + "</span></a></li>";
      }).join("") + "</ul>" : '<p class="muted small">' + esc(T("cc.no_sessions")) + "</p>";
    }
    h += '<div class="cc-actions"><button class="btn sm" id="cc-recheck">' + ic("refresh", "sm") + esc(T("cc.recheck")) + '</button><a class="btn sm" href="#/settings/providers" id="cc-models">' + ic("key", "sm") + esc(T("cc.open_models")) + "</a></div>";
    pop.innerHTML = h;
    if ($("#cc-copy")) $("#cc-copy").addEventListener("click", function () { copyText("npm i -g @anthropic-ai/claude-code"); });
    $("#cc-recheck").addEventListener("click", function (e) {
      var b = e.currentTarget; b.disabled = true;
      loadCC(true).then(function () { var b2 = $("#cc-recheck"); if (b2) b2.disabled = false; });
    });
    $("#cc-models").addEventListener("click", function () { pop.remove(); });
    var savePath = function (p) {
      $("#cc-path-err").textContent = "";
      return rpc("claude.set_path", { path: p }).then(function (st2) { cc.st = st2; paintCC(); toast(T("cc.path_saved")); })
        .catch(function (err) { var e = $("#cc-path-err"); if (e) e.textContent = (err && err.message) || String(err); });
    };
    if ($("#cc-path-save")) $("#cc-path-save").addEventListener("click", function () { savePath($("#cc-path-in").value.trim()); });
    if ($("#cc-path-auto")) $("#cc-path-auto").addEventListener("click", function () { savePath(""); });
    if ($("#cc-pick")) $("#cc-pick").addEventListener("click", function () {
      api("/api/desktop/pick-file", { method: "POST", body: {} }).then(function (r) { if (r && r.path) { $("#cc-path-in").value = r.path; savePath(r.path); } })
        .catch(function () { $("#cc-path-in").focus(); toast(T("cc.pick_browser")); });
    });
    $$("a", pop).forEach(function (a) { if (a.getAttribute("href").indexOf("#/runs/") === 0) a.addEventListener("click", function () { pop.remove(); }); });
  }
  setInterval(function () { if (state.csrf && !document.hidden) loadCC(false); }, 30000);

  // ---------- idle / SSE ----------
  function stopStream() { if (state.es) { state.es.close(); state.es = null; } }
  function bumpIdle() { clearTimeout(state.idleTimer); state.idleTimer = setTimeout(stopStream, IDLE_MS); }
  ["mousemove", "click", "scroll", "keydown"].forEach(function (ev) { document.addEventListener(ev, bumpIdle, { passive: true }); });

  // ---------- login ----------
  function dropBoot() { var b = document.getElementById("boot"); if (b) b.remove(); }
  function showLogin(totp) {
    dropBoot();
    stopStream();
    setDocTitle({ page: T("login.submit"), running: 0, alert: false });
    $("#shell").classList.add("hidden");
    var l = $("#login");
    l.classList.remove("hidden");
    l.innerHTML = '<div class="card login-card"><div class="brand"><span class="brand-mark">' + waveMark() + '</span><span class="brand-text"><strong>tidebot</strong><small>' + esc(T("brand.tagline")) + '</small></span></div>' +
      '<form id="login-form" autocomplete="on"><label class="field"><span>' + esc(T("login.password")) + '</span><input type="password" id="lp" required minlength="8" autocomplete="current-password" autofocus></label>' +
      '<div class="' + (totp ? "" : "hidden") + '" id="totp-field"><label class="field"><span id="lt-lbl">' + esc(T("login.totp")) + '</span><input id="lt" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" autocomplete="one-time-code" placeholder="123456"></label>' +
      '<button type="button" class="link-btn" id="lt-rec">' + esc(T("login.use_recovery")) + "</button></div>" +
      '<p class="err" id="lerr" role="alert"></p><button class="btn primary" type="submit" id="lsub">' + esc(T("login.submit")) + '</button></form>' +
      '<p class="login-foot">' + esc(T("login.foot")) + ' <code>agentctl auth set-password</code></p>' +
      '<div class="login-lang">' + langSelect("login-lang-sel") + "</div></div>";
    $("#login-lang-sel").addEventListener("change", function (e) { loadLang(e.target.value).then(function () { showLogin(totp); }); });
    var lt = $("#lt"), recMode = false, busy = false, waitTimer = null;
    $("#lt-rec").addEventListener("click", function () {
      recMode = !recMode;
      lt.value = "";
      lt.setAttribute("inputmode", recMode ? "text" : "numeric");
      lt.setAttribute("maxlength", recMode ? "11" : "6");
      lt.setAttribute("autocomplete", recMode ? "off" : "one-time-code");
      if (recMode) lt.removeAttribute("pattern"); else lt.setAttribute("pattern", "[0-9]{6}");
      lt.placeholder = recMode ? "abcde-12345" : "123456";
      $("#lt-lbl").textContent = T(recMode ? "login.recovery" : "login.totp");
      $("#lt-rec").textContent = T(recMode ? "login.use_totp" : "login.use_recovery");
      lt.focus();
    });
    var countdown = function (secs) {
      var sub = $("#lsub"), end = Date.now() + secs * 1000;
      clearInterval(waitTimer);
      var tick = function () {
        var left = Math.max(0, Math.ceil((end - Date.now()) / 1000));
        if (!document.contains(sub)) { clearInterval(waitTimer); return; }
        if (!left) { clearInterval(waitTimer); sub.disabled = false; $("#lerr").textContent = ""; return; }
        sub.disabled = true;
        $("#lerr").textContent = T("login.wait", { t: T("login.wait_ms", { m: Math.floor(left / 60), s: left % 60 }) });
      };
      tick(); waitTimer = setInterval(tick, 1000);
    };
    var submit = function () {
      if (busy || $("#lsub").disabled) return;
      busy = true;
      $("#lerr").textContent = "";
      api("/api/login", { method: "POST", body: { password: $("#lp").value, totp: lt.value } }).then(function () { clearInterval(waitTimer); boot(); })
        .catch(function (err) {
          if (err.status === 429) { countdown((err.data && err.data.retry_after) || 60); return; }
          if (err.status === 401) {
            var wasHidden = $("#totp-field").classList.contains("hidden");
            $("#totp-field").classList.remove("hidden");
            $("#lerr").textContent = wasHidden ? T("login.bad_or_totp") : T("login.bad");
            if (!wasHidden) { lt.value = ""; } (wasHidden ? lt : $("#lp")).focus();
            return;
          }
          $("#lerr").textContent = err.message;
        }).then(function () { busy = false; });
    };
    // Six digits typed (or pasted/autofilled) = submit; no extra click.
    lt.addEventListener("input", function () {
      if (recMode) return;
      lt.value = lt.value.replace(/\D/g, "").slice(0, 6);
      if (lt.value.length === 6 && $("#lp").value) submit();
    });
    $("#login-form").addEventListener("submit", function (e) { e.preventDefault(); submit(); });
  }

  // ---------- page plumbing ----------
  function page() { return $("#page"); }
  var mountSeq = 0, scrollPos = {}, pendingRestore = null;
  function scroller() { var pg = page(); return pg && pg.scrollHeight > pg.clientHeight + 1 ? pg : null; }
  function mount(html, nav, crumbs) {
    mountSeq++;
    setNav(nav);
    renderTopbar(crumbs);
    var leaf = crumbs && crumbs.length ? crumbs[crumbs.length - 1][0] : "";
    setDocTitle({ page: leaf || (nav ? T("nav." + nav) : "") });
    var pg = page();
    pg.removeAttribute("aria-busy");
    pg.innerHTML = html;
    applySizes(pg);
    if (pendingRestore !== null) { var y = pendingRestore; pendingRestore = null; window.scrollTo(0, y); pg.scrollTop = y; }
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
  // One-click "run now" from any list; the toast links to the live run.
  function runTaskNow(id, btn) {
    if (btn) btn.disabled = true;
    return api("/api/tasks/" + enc(id) + "/run", { method: "POST", body: {} }).then(function () { toast(T("toast.run_started")); setTimeout(function () { if (/^#\/tasks$/.test(location.hash)) refresh(); }, 800); }).catch(fail).then(function () { if (btn) btn.disabled = false; });
  }
  function wireRows(root) { $$("[data-href]", root || page()).forEach(function (el) { el.addEventListener("click", function (e) { if (e.target.closest("a,button")) return; location.hash = el.getAttribute("data-href"); }); }); }

  function runsTable(runs, rerun) {
    if (!runs.length) return emptyState("runs", T("runs.none"), T("runs.none_sub"));
    return '<table class="table"><thead><tr><th>' + esc(T("col.run")) + "</th><th>" + esc(T("col.agent_task")) + "</th><th>" + esc(T("col.status")) + "</th><th>" + esc(T("col.started")) + '</th><th class="c-opt">' + esc(T("col.duration")) + '</th><th class="right">' + esc(T("col.cost")) + '</th><th class="c-act"></th></tr></thead><tbody>' +
      runs.map(function (r) {
        var running = r.status === "running";
        return '<tr class="click" data-href="#/runs/' + enc(r.id) + '"><td><div class="cell-main"><span class="ico">' + ic(runIcon(r), "sm") + "</span><div>" + '<a class="row-link" href="#/runs/' + enc(r.id) + '"><strong>' + esc(runTitle(r)) + "</strong></a><small>" + esc(triggerText(r.trigger)) + "</small></div></div></td>" +
          "<td><strong>" + esc(r.task_name || T("run.adhoc")) + "</strong><small>" + esc(r.provider || "—") + (r.version_no ? " · v" + esc(r.version_no) : "") + "</small></td>" +
          "<td>" + statusPill(r.status) + '</td><td class="nowrap">' + esc(when(r.started_at || r.created_at)) + '</td><td class="nowrap c-opt">' + esc(running ? dur(Date.now() - new Date(r.started_at).getTime()) : dur(r.duration_ms)) +
          '</td><td class="right"><strong>' + esc(usd(r.cost_usd)) + '</strong></td><td class="right nowrap">' + (rerun && /failed|timeout|canceled/.test(r.status) ? '<button class="icon-btn sm row-act" data-rerun="' + esc(r.id) + '" data-tip="' + esc(T("runs.rerun")) + '" aria-label="' + esc(T("runs.rerun")) + '">' + ic("refresh", "sm") + "</button>" : "") + "</td></tr>";
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
  var HOME_BEATS = 10;
  function beatsBar(beats) {
    var cells = [];
    beats = beats.slice(-HOME_BEATS);
    for (var i = 0; i < HOME_BEATS - beats.length; i++) cells.push('<span class="hb hb-none"></span>');
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
        return '<button class="card qa' + (b.color ? " colored" : "") + '" data-color="' + esc(b.color || "") + '" data-btn="' + esc(b.id) + '" title="' + esc(b.description || b.label) + '"><span class="qa-ico">' + ic(btnIcon(b)) + '</span><span class="qa-txt"><strong>' + esc(b.label) + "</strong><small>" + esc(b.description || "") + "</small></span></button>";
      });
      qa.push('<button class="card qa tint" id="qa-new"><span class="qa-ico">' + ic("sparkle") + '</span><span class="qa-txt"><strong>' + esc(T("home.quick")) + "</strong><small>" + esc(T("home.quick_sub")) + "</small></span></button>");
      qa.push('<a class="card qa add" href="#/buttons" title="' + esc(T("home.add_button")) + '" aria-label="' + esc(T("home.add_button")) + '">' + ic("plus") + "</a>");
      var noTasks = !d.tasks.length;
      mount('<div class="home"><div class="grid g4 kpis" id="kpis"></div>' +
        '<div class="qa-row">' + qa.join("") + "</div>" +
        (noTasks ? '<section class="card">' + '<div class="empty">' + ic("tasks") + "<strong>" + esc(T("home.empty_title")) + "</strong>" + esc(T("home.empty_sub")) + '<div class="mt"><button class="btn primary" id="home-new">' + ic("plus", "sm") + esc(T("tf.new")) + "</button></div></div></section>" : "") +
        '<div class="home-mid' + (noTasks ? " two" : "") + '"><section class="card hc"><div class="card-head"><span class="ico">' + ic("pulse") + "</span><h2>" + esc(T("home.running")) + '</h2><span class="link muted" id="run-n"></span></div><div id="running"></div></section>' +
        '<section class="card hc"><div class="card-head"><span class="ico">' + ic("calendar") + "</span><h2>" + esc(T("home.next24")) + '</h2><a class="link" href="#/tasks">' + esc(T("home.view_tasks")) + " " + ic("arrow", "sm") + '</a></div><div id="next24"></div><div class="hc-foot">' + ic("info", "sm") + "<span>" + esc(T("home.times_in", { tz: tzLabel() })) + "</span></div></section>" +
        (noTasks ? "" : '<section class="card hc"><div class="card-head"><span class="ico">' + ic("runs") + "</span><h2>" + esc(T("home.health")) + '</h2><span class="link muted">' + esc(T("home.health_sub")) + '</span></div><div id="beats"></div></section>') + "</div>" +
        '<section class="card flush home-recent"><div class="card-head"><span class="ico">' + ic("runs") + "</span><h2>" + esc(T("home.recent")) + '</h2><a class="link" href="#/runs">' + esc(T("home.view_runs")) + " " + ic("arrow", "sm") + '</a></div><div id="recent"></div></section></div>', "home", []);
      applyColors(page());
      var paint = function (o, d) {
        var k = d.kpi, rate = k.runs_today ? Math.round(k.succeeded_today / k.runs_today * 100) + "%" : "—", left = k.budget_usd ? Math.max(0, k.budget_usd - k.spent_today) : null;
        var bp = k.budget_usd ? Math.min(100, k.spent_today / k.budget_usd * 100) : 0;
        $("#kpis").innerHTML = [[T("home.k_runs"), num(k.runs_today), T("home.k_runs_sub", { n: k.succeeded_today })], [T("home.k_rate"), rate, T("time.today")], [T("home.k_spent"), usd(k.spent_today, 2), k.budget_usd ? T("home.pct_budget", { p: Math.round(bp) }) : T("home.no_budget")], [T("home.k_left"), left == null ? "∞" : usd(left, 2), T("sp.global_daily")]].map(function (x, i) {
          return '<div class="card stat"><small>' + esc(x[0]) + '</small><div class="stat-v"><strong class="num">' + esc(x[1]) + "</strong><span>" + esc(x[2]) + "</span></div>" + (i === 3 && k.budget_usd ? '<div class="progress' + (bp >= 100 ? " bad" : bp >= 80 ? " warn" : "") + '"><span data-w="' + bp + '"></span></div>' : "") + "</div>";
        }).join("");
        $("#run-n").textContent = d.running.length ? T("home.n_running", { n: d.running.length }) : "";
        setDocTitle({ running: d.running.filter(function (x) { return x.status === "running"; }).length });
        $("#running").innerHTML = d.running.length ? '<ul class="live-list">' + d.running.map(function (x) {
          return '<li data-href="#/runs/' + enc(x.id) + '"><span class="spin" aria-hidden="true"></span><div><strong>' + esc(x.task_name || T("run.adhoc")) + "</strong><small>" + (x.status === "queued" ? esc(T("st.queued")) : tAgo("home.started_ago", null, "t", x.started_at || x.created_at)) + '</small></div><span class="num muted" data-since="' + esc(x.started_at || "") + '">' + esc(x.started_at ? dur(Date.now() - new Date(x.started_at).getTime()) : "—") + '</span><strong class="num">' + esc(usd(x.cost_usd, 3)) + "</strong></li>";
        }).join("") + "</ul>" : '<p class="hc-empty">' + ic("checkc", "sm") + "<span><strong>" + esc(T("home.idle")) + "</strong> · " + esc(T("home.idle_sub")) + "</span></p>";
        $("#next24").innerHTML = d.next.length ? '<ul class="timeline">' + d.next.slice(0, 4).map(function (s) {
          return '<li><span class="tl-dot"></span><span class="tl-time">' + esc(clock(s.at)) + '</span><div class="tl-name"><strong>' + esc(s.task_name) + "</strong><small>" + esc(relDay(s.at) + " · " + cronHuman(s.cron)) + '</small></div><a class="link" href="#/tasks/' + enc(s.task_id) + '">' + ic("chev", "sm") + "</a></li>";
        }).join("") + "</ul>" : '<p class="hc-empty">' + ic("calendar", "sm") + "<span><strong>" + esc(T("home.no_sched")) + "</strong> · " + esc(T("home.no_sched_sub")) + "</span></p>";
        if ($("#beats")) $("#beats").innerHTML = '<div class="beats">' + d.tasks.slice(0, 5).map(function (t) {
          return '<div class="beat-row"><a href="#/tasks/' + enc(t.id) + '" class="bn">' + esc(t.name) + (t.status === "paused" ? ' <span class="pill warn sm">' + esc(T("task.paused")) + "</span>" : "") + "</a>" + beatsBar(t.beats) + "</div>";
        }).join("") + "</div>";
        $("#recent").innerHTML = runsTable((o.runs || []).slice(0, 5));
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
      mount('<div class="page-head"><div><h1>' + esc(T("nav.tasks")) + "</h1><p>" + esc(plural(tasks.length, "tasks.count_one", "tasks.count_many")) + '</p></div><div class="actions"><button class="btn primary" id="t-new">' + ic("plus", "sm") + esc(T("tf.new")) + "</button></div></div>" +
        '<section class="card flush"><div class="toolbar"><label class="search-in">' + ic("search", "sm") + '<input id="tq" type="search" maxlength="100" placeholder="' + esc(T("tl.search")) + '" value="' + esc(v.q) + '"></label>' +
        '<div class="filters" role="group">' + [["", T("runs.all_f")], ["active", T("task.active")], ["paused", T("task.paused")]].map(function (x) { return '<button data-st="' + x[0] + '" class="' + (x[0] === v.status ? "active" : "") + '">' + esc(x[1]) + "</button>"; }).join("") + "</div>" +
        '<select id="tsk" aria-label="' + esc(T("bt.skill")) + '"><option value="">' + esc(T("tl.any_skill")) + "</option>" + Object.keys(skills).sort().map(function (k) { return opt(k, k, k === v.skill); }).join("") + "</select>" +
        '<span class="grow"></span><span class="muted" id="tcount"></span></div>' +
        '<div class="batchbar hidden" id="tbatch"><strong id="tbn"></strong><button class="btn sm" data-batch="pause">' + ic("pause", "sm") + esc(T("td.pause")) + '</button><button class="btn sm" data-batch="resume">' + ic("play", "sm") + esc(T("td.resume")) + '</button><button class="btn sm danger" data-batch="delete">' + ic("trash", "sm") + esc(T("td.delete")) + '</button><button class="btn sm ghost" id="tclear">' + esc(T("tl.clear")) + "</button></div>" +
        '<table class="table"><thead><tr><th class="ck"><input type="checkbox" id="tall" aria-label="' + esc(T("tl.select_all")) + '"></th>' + th("name", T("col.task")) + th("schedule", T("col.schedule")) + th("status", T("col.status")) + th("version", T("col.version")).replace("<th>", '<th class="c-opt">') + th("last", T("col.last_run")) + th("today", T("col.runs_today")).replace("<th>", '<th class="c-opt">') + '<th class="c-act"></th></tr></thead><tbody id="tbody"></tbody></table></section>', "tasks", [[T("nav.tasks")]]);
      var body = $("#tbody");
      function rows() {
        var q = v.q.trim().toLowerCase(), key = sorters[v.sort];
        var list = tasks.filter(function (t) {
          return (!v.status || t.status === v.status) && (!v.skill || (t.skills || []).indexOf(v.skill) >= 0) && (!q || (t.name + " " + (t.goal || "") + " " + t.prompt).toLowerCase().indexOf(q) >= 0);
        }).sort(function (a, b) { var x = key(a), y = key(b); return (x < y ? -1 : x > y ? 1 : 0) * v.dir; });
        $("#tcount").textContent = T("tl.showing", { n: list.length, total: tasks.length });
        body.innerHTML = list.length ? list.map(function (t) {
          return '<tr class="click" data-href="#/tasks/' + enc(t.id) + '"><td class="ck"><input type="checkbox" data-pick="' + esc(t.id) + '"' + (v.sel[t.id] ? " checked" : "") + ' aria-label="' + esc(t.name) + '"></td><td><div class="cell-main"><span class="ico">' + ic(runIcon({ task_name: t.name }), "sm") + '</span><div><a class="row-link" href="#/tasks/' + enc(t.id) + '"><strong>' + esc(t.name) + "</strong></a><small class=\"clamp1\">" + esc(t.goal || t.prompt) + "</small></div></div></td>" +
            '<td class="nowrap">' + esc(cronHuman(t.cron)) + (t.next_run_at && t.status === "active" ? "<small>" + esc(T("tl.next", { t: when(t.next_run_at) })) + "</small>" : "") + "</td><td>" + taskPill(t.status) + '</td><td class="c-opt"><span class="tag">v' + esc(t.version_no) + "</span></td><td>" + (t.last_status ? statusPill(t.last_status) : '<span class="muted">' + esc(T("tasks.never")) + "</span>") + '</td><td class="num c-opt">' + esc(t.runs_today) + '</td><td class="right c-act"><button type="button" class="icon-btn sm row-act" data-runnow="' + esc(t.id) + '" title="' + esc(T("td.run_now")) + '" aria-label="' + esc(T("td.run_now") + " · " + t.name) + '">' + ic("play", "sm") + "</button></td></tr>";
        }).join("") : '<tr><td colspan="8">' + (tasks.length ? emptyState("search", T("tl.no_match"), T("tl.no_match_sub")) : emptyState("tasks", T("tasks.none"), T("tasks.none_sub"))) + "</td></tr>";
        wireRows(body);
        $$("[data-runnow]", body).forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); runTaskNow(b.getAttribute("data-runnow"), b); }); });
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
            var inv = { pause: "resume", resume: "pause" }[act];
            if (r.failed && r.failed.length) toast(T("tl.batch_partial", { n: r.done, f: r.failed.length }), true);
            else if (inv) toastUndo(T("tl.batch_done_" + act, { n: r.done }), function () { api("/api/tasks/batch", { method: "POST", body: { ids: ids, action: inv } }).then(function () { refresh(); }).catch(fail); });
            else toast(T("tl.batch_done_" + act, { n: r.done }));
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
      s += '<g class="node' + (cur ? " cur" : "") + '" data-ver="' + esc(v.id) + '" data-no="' + esc(v.no) + '"><title>' + esc((v.message || v.source) + (cur ? " " + T("ver.cur_paren") : " — " + T("ver.click_rb")) + (v.created_at ? "\n" + fullTime(v.created_at) : "")) + "</title>" +
        '<rect x="' + (p.x - NW / 2) + '" y="' + p.y + '" width="' + NW + '" height="' + NH + '" rx="8"/>' +
        '<text x="' + p.x + '" y="' + (p.y + 19) + '" text-anchor="middle" font-size="13" font-weight="600">v' + esc(v.no) + "</text>" +
        '<text class="sub" x="' + p.x + '" y="' + (p.y + 34) + '" text-anchor="middle" data-ago="' + esc(v.created_at || "") + '">' + esc(ago(v.created_at)) + "</text></g>";
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
      return '<div class="msg"><span class="avatar">' + esc(T("thread.me")) + '</span><div><div class="msg-meta"><strong>' + esc(T("thread.you")) + '</strong><small>' + agoHtml(g.at) + '</small></div><div class="msg-body">' + esc(g.utter || T("thread.amendment")) + "</div></div></div>" +
        '<div class="msg"><span class="avatar bot">' + waveMark() + '</span><div><div class="msg-meta"><strong>tidebot</strong><small>' + agoHtml(g.at) + '</small></div><div class="msg-body">' + esc(reply) + "</div>" +
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
        '<section class="card"><div class="task-head"><div class="grow-col"><div class="td-title"><h1 class="inline-edit" id="ie-name" tabindex="0" role="button" data-tip="' + esc(T("ie.hint")) + '">' + esc(t.name) + "</h1>" + taskPill(t.status) + "</div><p>" + esc(t.goal || t.prompt) + "</p>" +
        '<div class="td-meta">' + ic("clock", "sm") + "<span>" + esc(cronHuman(t.cron)) + "</span><span>·</span><span>" + tAgo("td.created_updated", { c: fmtTime(t.created_at, { month: "short", day: "numeric", year: "numeric" }) }, "u", t.updated_at) + "</span></div></div>" +
        '<div class="row-actions">' + (t.status === "active" ? '<button class="btn" id="t-toggle" data-act="pause">' + ic("pause", "sm") + esc(T("td.pause")) + "</button>" : '<button class="btn" id="t-toggle" data-act="resume">' + ic("play", "sm") + esc(T("td.resume")) + "</button>") +
        '<button class="btn" id="t-edit">' + ic("doc", "sm") + esc(T("btn.edit")) + '</button><button class="icon-btn" id="t-dup" data-tip="' + esc(T("td.dup_tip")) + '" aria-label="' + esc(T("td.dup")) + '">' + ic("copy", "sm") + '</button><button class="icon-btn danger-text" id="t-del" data-tip="' + esc(T("td.delete")) + '" aria-label="' + esc(T("td.delete")) + '">' + ic("trash", "sm") + '</button><button class="btn primary" id="t-run">' + ic("play", "sm") + esc(T("td.run_now")) + "</button></div></div>" +
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
      $("#t-dup").addEventListener("click", function () {
        var b = $("#t-dup"); b.disabled = true;
        api(base + "/duplicate", { method: "POST", body: { name: T("td.dup_name", { n: t.name }) } }).then(function (c) {
          location.hash = "#/tasks/" + enc(c.id);
          toastUndo(T("td.dup_done", { n: c.name }), function () { api("/api/tasks/" + enc(c.id), { method: "DELETE" }).then(function () { location.hash = "#/tasks/" + enc(id); }).catch(fail); });
        }).catch(function (e) { b.disabled = false; fail(e); });
      });
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
        return '<div class="prop' + (live ? "" : " done") + '"><div class="prop-head"><strong>' + esc(v ? "v" + v.no : shortId(p.version_id)) + '</strong><span class="pill sm ' + (p.status === "canary" ? "info" : p.status === "promoted" ? "ok" : live ? "warn" : "") + '">' + esc(T("opt.st_" + p.status)) + '</span><span class="tag">' + esc(T("opt.risk_" + (p.risk || "low"))) + '</span><small class="muted grow right">' + agoHtml(p.created_at) + "</small></div>" +
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
      if (!list.length) cards.push('<div class="bt-empty">' + emptyState("bolt", T("bt.none"), T("bt.new_sub")) + "</div>");
      mount('<div class="page-head"><div><h1>' + esc(T("nav.buttons")) + "</h1><p>" + esc(T("bt.sub")) + tip(T("bt.sort_hint")) + '</p></div><div class="actions"><button class="btn primary" id="b-add">' + ic("plus", "sm") + esc(T("bt.new")) + '</button></div></div><div class="grid g4" id="bt-grid">' + cards.join("") + '</div><div class="sr-only" aria-live="polite" id="bt-live"></div>', "buttons", [[T("nav.buttons")]]);
      applyColors(page());
      var save = function (nl, quiet) { return api("/api/buttons", { method: "PUT", body: nl }).then(function () { if (!quiet) toast(T("toast.buttons_saved")); renderButtons(); }); };
      $("#b-add").addEventListener("click", function () { buttonModal(list, null, tasks, skills, save); });
      $$("[data-edit]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); buttonModal(list, +b.getAttribute("data-edit"), tasks, skills, save); }); });
      $$("[data-del]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); var k = +b.getAttribute("data-del"); var before = list.slice(), nl = list.slice(), gone = nl.splice(k, 1)[0]; save(nl, true).then(function () { toastUndo(T("bt.removed", { n: gone.label }), function () { save(before, true).catch(fail); }); }).catch(fail); }); });
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
        if (after) grid.insertBefore(drag, after); else grid.appendChild(drag);
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
  // Secondary help lives in a tooltip next to the label (hover or keyboard focus), not under every field.
  function tip(text) { return text ? '<span class="info-tip" tabindex="0" role="img" aria-label="' + esc(text) + '" data-tip="' + esc(text) + '">' + ic("info", "sm") + "</span>" : ""; }
  function fieldHtml(label, inner, hint) { return '<label class="field"><span>' + esc(label) + tip(hint) + "</span>" + inner + "</label>"; }
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
  var PV_COLORS = ["#2563eb", "#0d9488", "#7c3aed", "#db2777", "#ea580c", "#16a34a", "#0891b2", "#4f46e5"];
  function pvDisplay(p, presetById) { var pr = presetById[p.preset] || presetById[p.name]; return pr && pr.name && pr.id !== "custom" ? pr.name : p.name; }
  function pvAvatar(p, presetById) {
    var n = pvDisplay(p || {}, presetById || {}) || "?", h = 0;
    for (var i = 0; i < n.length; i++) h = (h * 31 + n.charCodeAt(i)) >>> 0;
    return '<span class="pv-av av' + (h % PV_COLORS.length) + '" aria-hidden="true">' + esc(n.replace(/[^A-Za-z0-9\u4e00-\u9fff]/g, "").slice(0, 1).toUpperCase() || "?") + "</span>";
  }
  // Right-side drawer for editing one provider. Esc / backdrop closes.
  function openDrawer(fill) {
    closeDrawer();
    var bg = document.createElement("div"), opener = document.activeElement;
    bg.className = "drawer-bg"; bg.id = "drawer";
    bg.innerHTML = '<aside class="drawer" role="dialog" aria-modal="true" tabindex="-1"></aside>';
    document.body.appendChild(bg);
    var d = $(".drawer", bg);
    var onKey = function (e) { if (e.key === "Escape" && !$("#modal-root").innerHTML) { e.preventDefault(); closeDrawer(); } };
    bg._off = function () { document.removeEventListener("keydown", onKey, true); if (opener && opener.focus && document.contains(opener)) opener.focus(); };
    document.addEventListener("keydown", onKey, true);
    bg.addEventListener("mousedown", function (e) { if (e.target === bg) closeDrawer(); });
    fill(d);
        requestAnimationFrame(function () { bg.classList.add("open"); });
    var first = $("input:not([type=hidden]):not([type=radio])", d); (first || d).focus();
    return d;
  }
  function closeDrawer() { var bg = $("#drawer"); if (bg) { if (bg._off) bg._off(); bg.remove(); } }
  function providerCards(box) {
    return api("/api/providers").then(function (d) {
      var ps = d.providers || [], presets = d.presets || [], byName = {}, presetById = {};
      ps.forEach(function (p) { byName[p.name] = p; });
      presets.forEach(function (p) { presetById[p.id] = p; });
      var configured = function (p) { return p.default || p.key_set || p.used_by > 0; };
      var shown = ps.filter(configured), hidden = ps.filter(function (p) { return !configured(p); });
      var card = function (p) {
        return '<li class="pv-card" draggable="true" data-pv="' + esc(p.name) + '" tabindex="0" role="button" aria-label="' + esc(pvDisplay(p, presetById)) + '">' + pvAvatar(p, presetById) +
          '<div class="pv-main"><div class="pv-title"><strong>' + esc(pvDisplay(p, presetById)) + "</strong>" + (p.default ? '<span class="pill ok sm">' + esc(T("pv.default")) + "</span>" : "") + "</div>" +
          '<small class="pv-meta"><code>' + esc((p.base_url || "https://api.anthropic.com").replace(/^https?:\/\//, "")) + "</code>" + (p.model ? " · " + esc(p.model) : "") + "</small></div>" +
          '<span class="pv-key ' + (p.key_set ? "ok" : "") + '"><span class="dot ' + (p.key_set ? "ok" : "") + '"></span>' + esc(T(p.key_set ? "pv.key_ok" : "pv.key_none")) + "</span>" + ic("chev", "sm") + "</li>";
      };
      box.innerHTML = '<div class="pv-toolbar"><p class="muted">' + tip(T("pv.sub")) + '</p><button class="btn primary" id="pv-add">' + ic("plus", "sm") + esc(T("pv.add")) + "</button></div>" +
        (shown.length ? '<ul class="pv-cards" id="pv-ul" aria-label="' + esc(T("set.providers")) + '">' + shown.map(card).join("") + "</ul>" : '<p class="muted pv-empty">' + esc(T("pv.none_configured")) + "</p>") +
        (hidden.length ? '<details class="pv-more"><summary>' + esc(T("pv.more", { n: hidden.length })) + '</summary><ul class="pv-cards">' + hidden.map(card).join("") + "</ul></details>" : "") +
        '<small class="hint">' + esc(T("pv.sort_hint")) + "</small>";
      var again = function (sel) { return providerCards(box).then(function () { if (sel && $("#drawer")) open(sel); }); };
      var open = function (n) { provSel = n; openDrawer(function (dw) { providerDetail(dw, byName[n] || null, null, presetById, again); }); };
      $$(".pv-card", box).forEach(function (li) {
        li.addEventListener("click", function () { open(li.getAttribute("data-pv")); });
        li.addEventListener("keydown", function (e) {
          if (e.key === "Enter" || e.key === " ") { e.preventDefault(); open(li.getAttribute("data-pv")); }
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
      $$(".pv-cards", box).forEach(function (ul) {
        ul.addEventListener("dragover", function (e) {
          e.preventDefault();
          var drag = $(".dragging", ul); if (!drag) return;
          var after = $$("li:not(.dragging)", ul).filter(function (x) { var r = x.getBoundingClientRect(); return e.clientY < r.top + r.height / 2; })[0];
          if (after) ul.insertBefore(drag, after); else ul.appendChild(drag);
        });
      });
      function saveOrder() {
        var names = $$(".pv-card", box).map(function (x) { return x.getAttribute("data-pv"); });
        api("/api/providers/order", { method: "POST", body: { names: names } }).then(function () { toast(T("pv.order_saved")); }).catch(fail);
      }
      $("#pv-add", box).addEventListener("click", function () { presetPicker(presets, function (pr) { provSel = "+new"; openDrawer(function (dw) { providerDetail(dw, null, pr, presetById, again); }); }); });
    });
  }
  // Brand metadata for the "add provider" card wall. cat: official|cn|coding|local; tag: i18n key; b: brand color class.
  var PV_BRAND = {
    "anthropic": { zh: "Anthropic", en: "Anthropic", cat: "official", tag: "ap.t_official", b: "br-anthropic", m: "A" },
    "kimi-cn": { zh: "Kimi", en: "Kimi (China)", cat: "cn", tag: "ap.t_cn", b: "br-kimi", m: "K" },
    "kimi": { zh: "Kimi 国际版", en: "Kimi (global)", cat: "cn", tag: "ap.t_intl", b: "br-kimi", m: "K" },
    "deepseek": { zh: "DeepSeek", en: "DeepSeek", cat: "cn", tag: "ap.t_cn", b: "br-deepseek", m: "D" },
    "zhipu": { zh: "智谱 GLM", en: "Zhipu GLM", cat: "cn", tag: "ap.t_cn", b: "br-zhipu", m: "智" },
    "zai": { zh: "Z.AI GLM", en: "Z.AI GLM", cat: "cn", tag: "ap.t_intl", b: "br-zhipu", m: "Z" },
    "qwen": { zh: "通义千问", en: "Qwen", cat: "cn", tag: "ap.t_cn", b: "br-qwen", m: "通" },
    "kimi-coding": { zh: "Kimi Code", en: "Kimi Code", cat: "coding", tag: "ap.t_cp_cn", b: "br-kimi", m: "K" },
    "kimi-coding-intl": { zh: "Kimi Code", en: "Kimi Code", cat: "coding", tag: "ap.t_cp_intl", b: "br-kimi", m: "K" },
    "qwen-coding": { zh: "通义千问 Coding", en: "Qwen Coding", cat: "coding", tag: "ap.t_cp_cn", b: "br-qwen", m: "通" },
    "qwen-coding-intl": { zh: "通义千问 Coding", en: "Qwen Coding", cat: "coding", tag: "ap.t_cp_intl", b: "br-qwen", m: "通" },
    "magpie": { zh: "Magpie", en: "Magpie", cat: "local", tag: "ap.t_local", b: "br-magpie", m: "M" }
  };
  function presetPicker(presets, pick) {
    var zh = (state.lang || "").indexOf("zh") === 0;
    var cats = [["all", "ap.c_all", "home"], ["official", "ap.c_official", "shield"], ["cn", "ap.c_cn", "globe"], ["coding", "ap.c_coding", "code"], ["local", "ap.c_local", "user"]];
    var custom = presets.filter(function (p) { return p.id === "custom"; })[0];
    var order = Object.keys(PV_BRAND), rank = function (p) { var k = order.indexOf(p.id); return k < 0 ? 99 : k; };
    var list = presets.filter(function (p) { return p.id !== "custom"; }).sort(function (a, b) { return rank(a) - rank(b); });
    var tile = function (p) {
      var m = PV_BRAND[p.id] || { zh: p.name, en: p.name, cat: "local", tag: "ap.t_local", b: "br-other", m: (p.name || "?").slice(0, 1).toUpperCase() };
      var nm = zh ? m.zh : m.en;
      return '<button type="button" role="option" aria-selected="false" class="ap-tile" data-pr="' + presets.indexOf(p) + '" data-cat="' + m.cat + '" data-q="' + esc((p.name + " " + p.id + " " + m.zh + " " + m.en).toLowerCase()) + '" title="' + esc(p.name) + '">' +
        '<span class="ap-chk" aria-hidden="true">' + ic("check", "sm") + "</span>" +
        '<span class="ap-logo ' + m.b + '" aria-hidden="true">' + esc(m.m) + "</span><strong>" + esc(nm) + '</strong><span class="ap-tag">' + esc(T(m.tag)) + '</span><small class="muted">' + esc(T("ap.def_model", { m: p.model || (p.id === "anthropic" ? T("ap.m_claude") : T("ap.m_gw")) })) + "</small></button>";
    };
    modal('<div class="ap"><div class="ap-head"><h2>' + esc(T("pv.add")) + '</h2><button type="button" class="icon-btn" data-close aria-label="' + esc(T("btn.close")) + '">' + ic("x") + "</button></div>" +
      '<div class="ap-search">' + ic("search", "sm") + '<input type="search" id="pr-q" autofocus placeholder="' + esc(T("ap.search")) + '" aria-label="' + esc(T("ap.search")) + '"></div>' +
      '<div class="ap-body"><nav class="ap-rail" role="tablist">' + cats.map(function (c, i) { return '<button type="button" role="tab" data-cat="' + c[0] + '" class="' + (i ? "" : "active") + '" aria-selected="' + !i + '">' + ic(c[2], "sm") + esc(T(c[1])) + "</button>"; }).join("") + "</nav>" +
      '<div class="ap-grid presets" role="listbox" aria-label="' + esc(T("pv.add")) + '">' + list.map(tile).join("") +
      (custom ? '<button type="button" role="option" aria-selected="false" class="ap-tile ap-custom" data-pr="' + presets.indexOf(custom) + '" data-cat="local" data-q="custom 自定义 ' + esc(custom.name.toLowerCase()) + '"><span class="ap-chk" aria-hidden="true">' + ic("check", "sm") + "</span>" + ic("plus") + "<strong>" + esc(T("ap.custom")) + '</strong><small class="muted">' + esc(T("ap.custom_sub")) + "</small></button>" : "") +
      '<p class="muted ap-empty hidden">' + esc(T("ap.none")) + "</p></div></div>" +
      '<div class="ap-foot"><span class="muted">' + ic("info", "sm") + esc(T("ap.hint")) + '</span><span class="grow"></span><button type="button" class="btn" data-close>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn primary" id="ap-next" disabled>' + esc(T("ap.next")) + "</button></div></div>",
    function (root, close) {
      var q = $("#pr-q", root), cat = "all", sel = -1, next = $("#ap-next", root);
      var tiles = function () { return $$(".ap-tile", root).filter(function (t) { return !t.classList.contains("hidden"); }); };
      var select = function (t, focus) {
        $$(".ap-tile", root).forEach(function (x) { x.classList.toggle("on", x === t); x.setAttribute("aria-selected", x === t); });
        sel = t ? +t.getAttribute("data-pr") : -1; next.disabled = sel < 0;
        if (t) { t.scrollIntoView({ block: "nearest" }); if (focus) t.focus(); }
      };
      var filter = function () {
        var v = q.value.trim().toLowerCase();
        $$(".ap-tile", root).forEach(function (t) { t.classList.toggle("hidden", (cat !== "all" && t.getAttribute("data-cat") !== cat) || (v && t.getAttribute("data-q").indexOf(v) < 0)); });
        var vis = tiles(); $(".ap-empty", root).classList.toggle("hidden", vis.length > 0);
        var cur = vis.filter(function (t) { return +t.getAttribute("data-pr") === sel; })[0];
        select(cur || vis[0] || null, false);
      };
      var go = function () { if (sel >= 0) { var p = presets[sel]; close(); pick(p); } };
      q.addEventListener("input", filter);
      $$(".ap-rail button", root).forEach(function (b) { b.addEventListener("click", function () { cat = b.getAttribute("data-cat"); $$(".ap-rail button", root).forEach(function (x) { x.classList.toggle("active", x === b); x.setAttribute("aria-selected", x === b); }); filter(); }); });
      $$(".ap-tile", root).forEach(function (t) { t.addEventListener("click", function () { select(t, false); }); t.addEventListener("dblclick", go); });
      next.addEventListener("click", go);
      root.addEventListener("keydown", function (e) {
        if (e.target.closest && e.target.closest(".ap-rail")) return;
        var vis = tiles(), i = vis.findIndex(function (t) { return +t.getAttribute("data-pr") === sel; });
        if (e.key === "Enter" && !(e.target.tagName === "BUTTON" && !e.target.classList.contains("ap-tile"))) { e.preventDefault(); go(); return; }
        var cols = (getComputedStyle($(".ap-grid", root)).gridTemplateColumns.split(" ").length) || 3;
        var d = { ArrowRight: 1, ArrowLeft: -1, ArrowDown: cols, ArrowUp: -cols }[e.key];
        if (!d || !vis.length) return;
        if (e.target === q && (e.key === "ArrowLeft" || e.key === "ArrowRight")) return;
        e.preventDefault();
        var j = i < 0 ? 0 : Math.max(0, Math.min(vis.length - 1, i + d));
        select(vis[j], e.target !== q);
      });
      filter();
    });
    var box = $("#modal-root .modal"); if (box) box.classList.add("ap-modal");
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
    var seg = function (val, label, sub) { return '<label class="seg-opt"><input type="radio" name="auth_env" value="' + val + '"' + ((val === "ANTHROPIC_API_KEY") === (v.auth_env === "ANTHROPIC_API_KEY") ? " checked" : "") + "><span><strong>" + esc(label) + "</strong><small>" + esc(sub) + "</small></span></label>"; };
    var title = creating ? (p ? T("pv.dup_title") : T("pv.add_named", { n: preset.name })) : pvDisplay(v, presetById);
    host.innerHTML = '<div class="drawer-head">' + pvAvatar(v, presetById) + '<div class="grow-col"><h2>' + esc(title) + "</h2>" +
      (!creating ? '<small class="muted"><code>' + esc(v.name) + "</code> · " + (v.default ? esc(T("pv.default")) + " · " : "") + (v.used_by ? esc(plural(v.used_by, "tasks.count_one", "tasks.count_many")) : esc(T("pv.unused"))) + "</small>" : "") + "</div>" +
      '<button type="button" class="icon-btn" data-drawer-close aria-label="' + esc(T("btn.close")) + '">' + ic("x") + "</button></div>" +
      '<form id="pvf" class="drawer-body" autocomplete="off">' +
      '<h4 class="sec">' + esc(T("pv.sec_basic")) + "</h4>" +
      fieldHtml(T("set.name"), inp("name", v.name, ' required maxlength="40" pattern="[a-z0-9][a-z0-9._\\-]{0,39}"'), T("pv.name_hint")) +
      '<label class="field"><span>Base URL' + tip(T("pv.url_hint")) + '</span><div class="in-group">' + inp("base_url", v.base_url, ' type="url" placeholder="https://api.anthropic.com"') + (base && base.base_url !== undefined ? '<button type="button" class="btn ghost" id="pd-reset" title="' + esc(base.base_url || "https://api.anthropic.com") + '">' + ic("rollback", "sm") + esc(T("pv.reset_url")) + "</button>" : "") + "</div></label>" +
      '<h4 class="sec">' + esc(T("pv.sec_auth")) + " " + link + "</h4>" +
      '<div class="field"><span>' + esc(T("pv.auth")) + '</span><div class="seg" role="radiogroup" aria-label="' + esc(T("pv.auth")) + '">' + seg("ANTHROPIC_AUTH_TOKEN", "Bearer", "ANTHROPIC_AUTH_TOKEN") + seg("ANTHROPIC_API_KEY", "x-api-key", "ANTHROPIC_API_KEY") + "</div></div>" +
      '<label class="field"><span>' + esc(T("pv.api_key")) + tip(T("pv.key_hint")) + '</span><div class="in-group"><input name="api_key" type="password" autocomplete="new-password" spellcheck="false" placeholder="' + esc(!creating && v.key_set && v.key_source === "store" ? T("pv.key_keep", { l: v.key_last4 || "" }) : !creating && v.key_source === "env" ? T("pv.key_env") : T("pv.key_new")) + '"><button type="button" class="btn ghost" id="pd-eye" aria-pressed="false" aria-label="' + esc(T("pv.show_key")) + '">' + esc(T("pv.show")) + "</button></div></label>" +
      (!creating && v.key_source === "store" ? '<label class="check"><input type="checkbox" name="clear_key"> ' + esc(T("pv.clear_key")) + "</label>" : "") +
      '<h4 class="sec">' + esc(T("pv.models")) + "</h4>" +
      '<div class="model-list">' + models.map(function (m) { return '<div class="model-row"><label for="pm-' + m[0] + '">' + esc(m[1]) + '</label><div class="in-group"><input id="pm-' + m[0] + '" name="' + m[0] + '" value="' + esc(v[m[0]] || "") + '" maxlength="128" spellcheck="false"><button type="button" class="btn ghost" data-check="' + m[0] + '"' + (creating ? " disabled" : "") + ">" + esc(T("pv.check")) + '</button></div><span class="mres" data-res="' + m[0] + '"></span></div>'; }).join("") + "</div>" + (creating ? '<small class="hint">' + esc(T("pv.check_after_save")) + "</small>" : "") +
      (!creating ? '<h4 class="sec" id="pd-price-h">' + esc(T("pr.sec")) + '</h4><div id="pd-price" class="pr-box"><p class="muted">…</p></div>' : "") +
      '<details class="sec-adv"><summary class="sec">' + esc(T("pv.sec_adv")) + "</summary>" +
      fieldHtml(T("pv.extra_env"), '<textarea name="extra_env" rows="3" placeholder="API_TIMEOUT_MS=600000">' + esc(envText) + "</textarea>", T("pv.extra_hint")) +
      (!creating ? '<div class="row-actions">' + (v.default ? "" : '<button type="button" class="btn" id="pd-def">' + esc(T("pv.make_default")) + "</button>") + '<button type="button" class="btn" id="pd-dup">' + esc(T("pv.dup")) + "</button></div>" : "") + "</details>" +
      '<div class="pv-test" id="pd-test" aria-live="polite"></div>' +
      '<div class="drawer-foot">' + (!creating && !v.default ? '<button type="button" class="btn ghost danger-text" id="pd-del">' + ic("trash", "sm") + esc(T("btn.remove")) + "</button>" : "") + '<span class="grow"></span>' +
      '<button type="submit" class="btn" data-mode="save">' + esc(T("btn.save")) + '</button><button type="submit" class="btn primary" data-mode="test">' + ic("check", "sm") + esc(T("pv.save_test")) + "</button></div></form>";
    $$("[data-drawer-close]", host).forEach(function (b) { b.addEventListener("click", closeDrawer); });
    var f = $("#pvf", host), out = $("#pd-test", host), mode = "save";
    if (!creating) pricingEditor($("#pd-price", host), v.name);
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
    var dl = $("#pd-del", host); if (dl) dl.addEventListener("click", function () { confirmDialog({ title: T("pv.del_title"), body: T("pv.confirm_del", { n: v.name }), ok: T("btn.remove"), danger: true }).then(function (y) { if (y) api("/api/providers/" + enc(v.name), { method: "DELETE" }).then(function () { toast(T("pv.deleted")); provSel = null; closeDrawer(); done(); }).catch(fail); }); });
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

  // ---------- provider pricing (drawer section) ----------
  // Per-model prices per 1M tokens + billing mode. Official prices ship with the
  // presets (source URL + date); editing makes them "custom"; restore drops edits.
  function pricingEditor(box, name) {
    var nf = function (v) { return v == null ? "" : String(v); };
    var paint = function (d) {
      var P = d.pricing, b = P.billing || { mode: "metered" }, q = b.quota || {};
      var prices = (P.prices || []).map(function (x) { return JSON.parse(JSON.stringify(x)); });
      var mode = b.mode || "metered", sub = mode === "subscription";
      // Drawer shows numbers + one pill; where the numbers come from lives in the ⓘ popover.
      var pill = mode !== "metered" ? "" :
        P.origin === "official" ? '<span class="pill ok sm">' + esc(T("pr.o_official")) + "</span>" :
        P.origin === "custom" ? '<span class="pill info sm">' + esc(T("pr.o_custom")) + "</span>" : '<span class="pill warn sm">' + esc(T("pr.o_none")) + "</span>";
      var src = {}; (P.official || []).forEach(function (x) { if (x.source) src[x.source] = x.fetched; });
      var fb = d.fallback;
      var info = '<details class="pop-info"><summary class="info-tip" aria-label="' + esc(T("pr.info")) + '">' + ic("info", "sm") + '</summary><div class="pop-body">' +
        (Object.keys(src).length ? "<strong>" + esc(T("pr.src_h")) + "</strong>" + Object.keys(src).map(function (u) { return '<a class="link" href="' + esc(u) + '" target="_blank" rel="noopener noreferrer">' + esc(u.replace(/^https?:\/\//, "").split("/")[0]) + '</a><small class="muted">' + esc(T("pr.src", { d: src[u] })) + "</small>"; }).join("") : "") +
        (fb ? "<p>" + esc(T("pr.fallback", { m: d.model, i: fb.input, o: fb.output, c: fb.currency })) + "</p>" : "") +
        "<p>" + esc(T(sub ? "pr.sub_hint" : mode === "free" ? "pr.free_hint" : P.anthropic ? "pr.anth_hint" : "pr.metered_hint")) + "</p>" +
        "<p>" + esc(T("pr.unit_hint")) + '</p><a class="link" href="#/settings/pricing">' + esc(T("pr.catalog_link")) + " →</a></div></details>";
      var row = function (x, i) {
        var inp = function (k, ph) { return '<input type="number" min="0" step="any" inputmode="decimal" data-pi="' + i + '" data-pk="' + k + '" value="' + esc(nf(x[k])) + '" placeholder="' + esc(ph || "—") + '" aria-label="' + esc((x.model || "") + " " + T("pr.c_" + k)) + '">'; };
        var meta = [];
        if (x.tiers && x.tiers.length) meta.push(x.tiers.map(function (t) { return (t.up_to ? "< " + num(t.up_to) : "≥") + ": " + t.input + " / " + t.output; }).join(" · "));
        if (x.note) meta.push(x.note);
        return '<div class="pr-row"><input class="pr-model" data-pi="' + i + '" data-pk="model" value="' + esc(x.model || "") + '" maxlength="128" spellcheck="false" placeholder="model-id" aria-label="' + esc(T("f.model")) + '">' +
          inp("input") + inp("output") + inp("cache_write", "=" + T("pr.c_input")) + inp("cache_read", "—") +
          '<span class="pr-x">' + (meta.length ? '<span class="tag sm" tabindex="0" data-tip="' + esc(meta.join("\n")) + '">' + esc(x.tiers && x.tiers.length ? T("pr.tiered", { n: x.tiers.length }) : "i") + "</span>" : "") +
          '<button type="button" class="icon-btn sm" data-pdel="' + i + '" aria-label="' + esc(T("btn.remove")) + '">' + ic("x", "sm") + "</button></span></div>";
      };
      var cur0 = (prices[0] || {}).currency || "USD";
      box.innerHTML = '<div class="pr-head"><div class="seg sm" role="radiogroup" aria-label="' + esc(T("pr.mode")) + '">' + ["metered", "subscription", "free"].map(function (m) { return '<button type="button" role="radio" data-pm="' + m + '" aria-checked="' + (m === mode) + '" class="' + (m === mode ? "active" : "") + '">' + esc(T("pr.m_" + m)) + "</button>"; }).join("") + '</div><span class="grow"></span>' + pill + info + "</div>" +
        (sub ? '<div class="pr-sub"><label class="field"><span>' + esc(T("pr.q_window")) + '</span><select id="pr-qw">' + opt("", T("pr.q_none"), !q.window) + ["5h", "day", "week", "month"].map(function (w) { return opt(w, T("pr.w_" + w), q.window === w); }).join("") + "</select></label>" +
          '<label class="field"><span>' + esc(T("pr.q_tokens")) + '</span><input id="pr-qt" type="number" min="0" step="1000" value="' + esc(q.max_tokens || "") + '"></label>' +
          '<label class="field"><span>' + esc(T("pr.q_reqs")) + '</span><input id="pr-qr" type="number" min="0" step="1" value="' + esc(q.max_requests || "") + '"></label>' +
          '<label class="field"><span>' + esc(T("pr.fee")) + tip(T("pr.fee_hint")) + '</span><div class="in-group"><input id="pr-fee" type="number" min="0" step="any" value="' + esc(b.monthly_fee || "") + '"><select id="pr-feec">' + opt("CNY", "¥", b.fee_currency !== "USD") + opt("USD", "$", b.fee_currency === "USD") + "</select></div></label></div>" : "") +
        (mode === "metered" ? '<div class="pr-table"><div class="pr-row pr-th"><select id="pr-cur" aria-label="' + esc(T("pr.c_currency")) + '">' + opt("USD", "USD $", cur0 !== "CNY") + opt("CNY", "CNY ¥", cur0 === "CNY") + "</select><span>" + esc(T("pr.c_input")) + "</span><span>" + esc(T("pr.c_output")) + "</span><span>" + esc(T("pr.c_cache_write")) + "</span><span>" + esc(T("pr.c_cache_read")) + "</span><span></span></div>" +
          (prices.length ? prices.map(row).join("") : '<p class="muted pr-empty">' + esc(T("pr.unset")) + "</p>") + "</div>" : "") +
        '<div class="row-actions">' + (mode === "metered" ? '<button type="button" class="btn ghost sm" id="pr-add">' + ic("plus", "sm") + esc(T("pr.add")) + "</button>" : "") + ((P.official || []).length && P.origin === "custom" ? '<button type="button" class="btn ghost sm" id="pr-restore">' + ic("rollback", "sm") + esc(T("pr.restore")) + "</button>" : "") + '<span class="grow"></span><button type="button" class="btn sm" id="pr-save">' + esc(T("pr.save")) + "</button></div>";
      var collect = function () {
        $$("[data-pi]", box).forEach(function (el) {
          var x = prices[+el.getAttribute("data-pi")], k = el.getAttribute("data-pk"), v = el.value.trim();
          if (k === "model") { x[k] = v; return; }
          var old = x[k] == null ? null : x[k], nv = v === "" ? null : parseFloat(v);
          x[k] = nv;
          if (x.tiers && old !== nv) delete x.tiers; // edited base price: the bands no longer apply
        });
        var c = $("#pr-cur", box);
        if (c) prices.forEach(function (x) { x.currency = c.value; });
        return prices.filter(function (x) { return x.model; });
      };
      var billing = function () {
        var out = { mode: mode };
        if (mode === "subscription") {
          var w = $("#pr-qw", box) ? $("#pr-qw", box).value : (q.window || "");
          if (w) out.quota = { window: w, max_tokens: parseInt(($("#pr-qt", box) || {}).value, 10) || 0, max_requests: parseInt(($("#pr-qr", box) || {}).value, 10) || 0 };
          out.monthly_fee = parseFloat(($("#pr-fee", box) || {}).value) || 0; out.fee_currency = ($("#pr-feec", box) || {}).value || "CNY";
        } else if (b.quota || b.monthly_fee) { out.quota = b.quota; out.monthly_fee = b.monthly_fee; out.fee_currency = b.fee_currency; } // keep plan details when switching back and forth
        return out;
      };
      var repaint = function (list) { P.prices = list; P.billing = billing(); paint(d); };
      $$("[data-pm]", box).forEach(function (bt) { bt.addEventListener("click", function () { var list = $("#pr-cur", box) ? collect() : prices; P.billing = billing(); P.billing.mode = bt.getAttribute("data-pm"); if (P.billing.mode === "subscription") { P.billing.quota = P.billing.quota || b.quota; } P.prices = list; paint(d); var nb = $('[data-pm="' + P.billing.mode + '"]', box); if (nb) nb.focus(); }); });
      var add = $("#pr-add", box); if (add) add.addEventListener("click", function () {
        var cur = collect(); cur.push({ model: "", currency: (cur[0] || {}).currency || cur0, input: null, output: null }); repaint(cur);
        var ins = $$('[data-pk="model"]', box); if (ins.length) ins[ins.length - 1].focus();
      });
      $$("[data-pdel]", box).forEach(function (bt) { bt.addEventListener("click", function () { var cur = collect(); cur.splice(+bt.getAttribute("data-pdel"), 1); repaint(cur); }); });
      $("#pr-save", box).addEventListener("click", function () {
        var list = $("#pr-cur", box) ? collect() : prices, bad = list.filter(function (x) { return x.input == null || x.output == null; })[0];
        if (bad && mode === "metered") { toast(T("pr.need_io", { m: bad.model }), true); return; }
        rpc("pricing.set", { provider: name, billing: billing(), prices: list }).then(function (r) { toast(T("pr.saved")); paint(r); }).catch(fail);
      });
      var rs = $("#pr-restore", box); if (rs) rs.addEventListener("click", function () { var prev = { billing: billing(), prices: collect() }; rpc("pricing.set", { provider: name, billing: billing(), restore: true }).then(function (r) { paint(r); toastUndo(T("pr.restored"), function () { rpc("pricing.set", { provider: name, billing: prev.billing, prices: prev.prices }).then(paint).catch(fail); }); }).catch(fail); });
    };
    return rpc("pricing.get", { provider: name }).then(paint).catch(function (e) { box.innerHTML = '<p class="err">' + esc(e.message) + "</p>"; });
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
      plugins.forEach(function (p) { ((((p.current || {}).manifest || {}).contributes || {}).triggers || []).forEach(function (t) { if (p.enabled && t.kind !== "webhook" && t.kind !== "file_change") ptrig.push({ plugin: p.id, pname: plName(p), t: t }); }); });
      modal('<div class="card-head"><span class="ico">' + ic("globe") + "</span><h2>" + esc(T("tr.new")) + '</h2></div><form id="trf">' +
        fieldHtml(T("tr.kind"), '<select name="kind">' + opt("webhook", T("tr.k_webhook"), true) + opt("file_change", T("tr.k_file")) + (ptrig.length ? opt("plugin", T("tr.k_plugin")) : "") + "</select>") +
        fieldHtml(T("col.task"), '<select name="task_id" required>' + tasks.map(function (t) { return opt(t.id, t.name); }).join("") + "</select>", tasks.length ? "" : T("tr.no_tasks")) +
        '<div data-k="file_change" class="hidden">' + fieldHtml(T("tr.path"), '<input name="path" class="mono" placeholder="/home/me/inbox">', T("tr.path_hint")) + "</div>" +
        '<div data-k="plugin" class="hidden">' + fieldHtml(T("tr.ptrig"), '<select name="ptrig">' + ptrig.map(function (x, i) { return opt(i, x.pname + " · " + (state.dict["tk." + x.plugin + "." + x.t.id] || x.t.title || x.t.id)); }).join("") + "</select>") + '<div id="tr-pf"></div><div class="row-actions"><button type="button" class="btn sm" id="tr-prev">' + ic("eye", "sm") + esc(T("tr.preview")) + '</button></div><div id="tr-prev-out"></div></div>' +
        '<p class="hint" data-k="webhook">' + esc(T("tr.webhook_hint")) + "</p>" +
        '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T("tr.create")) + "</button></div></form>", function (root, close) {
        var f = $("#trf", root);
        var pfields = [];
        var loadPF = function () {
          var x = ptrig[+((f.ptrig || {}).value || 0)]; if (!x) return;
          $("#tr-prev-out", root).innerHTML = "";
          rpc("plugins.trigger_fields", { plugin_id: x.plugin, trigger_id: x.t.id }).then(function (fl) { pfields = fl || []; $("#tr-pf", root).innerHTML = schemaForm(pfields, {}, {}, "t-" + x.plugin); }).catch(fail);
        };
        var sync = function () { $$("[data-k]", f).forEach(function (x) { x.classList.toggle("hidden", x.getAttribute("data-k") !== f.kind.value); }); if (f.kind.value === "plugin" && !pfields.length) loadPF(); };
        f.kind.addEventListener("change", sync); sync();
        if (f.ptrig) f.ptrig.addEventListener("change", loadPF);
        var prevBtn = $("#tr-prev", root);
        if (prevBtn) prevBtn.addEventListener("click", function () {
          var x = ptrig[+f.ptrig.value], out = $("#tr-prev-out", root);
          prevBtn.disabled = true; out.innerHTML = '<p class="muted">' + esc(T("tr.preview_wait")) + "</p>";
          rpc("triggers.preview", { plugin_id: x.plugin, config: readSchemaForm($("#tr-pf", root), pfields) }).then(function (r) {
            prevBtn.disabled = false;
            var items = (r && r.items) || [];
            out.innerHTML = items.length ? '<ul class="tr-prev">' + items.map(function (it) { return "<li><strong>" + esc(it.title || it.subject || it.url || "") + '</strong> <span class="muted">' + esc(it.published || it.date || it.from || "") + "</span>" + (it.text || it.summary || it.snippet ? '<small class="muted">' + esc(String(it.text || it.summary || it.snippet).slice(0, 220)) + "</small>" : "") + "</li>"; }).join("") + "</ul>" : '<p class="muted">' + esc(T("tr.preview_none")) + "</p>";
          }).catch(function (e) { prevBtn.disabled = false; out.innerHTML = '<p class="notice bad">' + ic("x", "sm") + esc(e.message) + "</p>"; });
        });
        f.addEventListener("submit", function (e) {
          e.preventDefault();
          var k = f.kind.value, body = { kind: k, task_id: f.task_id.value, config: {} };
          if (k === "file_change") body.config.path = f.path.value.trim();
          if (k === "plugin") { var x = ptrig[+f.ptrig.value]; body.kind = x.t.kind; body.plugin_id = x.plugin; body.config = readSchemaForm($("#tr-pf", root), pfields); body.config.trigger_id = x.t.id; }
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
  function schemaForm(fields, values, secretsSet, ns) {
    return fields.map(function (f) {
      var v = values[f.key] !== undefined ? values[f.key] : f.default, nm = ' name="' + esc(f.key) + '" data-f="' + esc(f.key) + '" data-t="' + esc(f.type) + '"';
      var lk = "pf." + (ns || "") + "." + f.key, lab = (ns && state.dict[lk] ? state.dict[lk] : (f.title || f.key)) + (f.required ? " *" : ""), inner;
      var hk = lk + "_h";
      if (ns && state.dict[hk]) f = Object.assign({}, f, { description: state.dict[hk] });
      if (f.type === "boolean") return '<label class="check field-check"><input type="checkbox"' + nm + (v ? " checked" : "") + "> " + esc(lab) + (f.description ? '<small class="hint">' + esc(f.description) + "</small>" : "") + "</label>";
      if (f.type === "array") inner = '<div class="multi">' + f.items_enum.map(function (x) { return '<label class="check"><input type="checkbox" data-arr="' + esc(f.key) + '" value="' + esc(x) + '"' + ((v || []).map(String).indexOf(String(x)) >= 0 ? " checked" : "") + "> " + esc(x) + "</label>"; }).join("") + "</div>";
      else if (f.enum && f.enum.length) inner = "<select" + nm + ">" + (f.required ? "" : opt("", "—", v == null)) + f.enum.map(function (x) { return opt(x, state.dict["pf.e." + x] || x, String(x) === String(v)); }).join("") + "</select>";
      else if (f.type === "string" && f.max_length >= 2000 && !/url$/.test(f.key)) inner = '<textarea rows="4"' + nm + ' maxlength="' + f.max_length + '"' + (f.required ? " required" : "") + ">" + esc(v == null ? "" : v) + "</textarea>";
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
  var PL_TESTABLE = { wecom: 1, dingtalk: 1, email: 1 };
  function pluginConfigModal(p, after) {
    rpc("plugins.config_get", { id: p.id }).then(function (cv) {
      var testable = PL_TESTABLE[p.id] && p.source === "official";
      modal('<div class="card-head"><span class="ico">' + ic("settings") + "</span><h2>" + esc(T("cfg.title", { n: plName(p) })) + '</h2></div><form id="pcf" novalidate>' + (state.dict["of." + p.id + ".cfg"] ? '<p class="hint">' + esc(T("of." + p.id + ".cfg")) + "</p>" : "") + schemaForm(cv.fields || [], cv.values || {}, cv.secrets_set || {}, p.id) + '<p class="notice bad hidden" id="pcf-err"></p><p class="notice ok hidden" id="pcf-ok"></p><div class="actions">' + (testable ? '<button type="button" class="btn" id="pcf-test">' + ic("send", "sm") + esc(T("of.test_send")) + '</button><span class="grow"></span>' : "") + '<button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T("btn.save")) + "</button></div></form>", function (root, close) {
        var f = $("#pcf", root), err = $("#pcf-err", root), okm = $("#pcf-ok", root);
        var showErr = function (e) { okm.classList.add("hidden"); err.textContent = e.message; err.classList.remove("hidden"); };
        f.addEventListener("submit", function (e) {
          e.preventDefault();
          if (!f.reportValidity()) return;
          rpc("plugins.config_set", { id: p.id, values: readSchemaForm(f, cv.fields || []) }).then(function () { close(); toast(T("cfg.saved")); if (after) after(); }).catch(showErr);
        });
        if (testable) $("#pcf-test", root).addEventListener("click", function (e) {
          var b = e.currentTarget; b.disabled = true; err.classList.add("hidden");
          rpc("plugins.config_set", { id: p.id, values: readSchemaForm(f, cv.fields || []) }).then(function () { return rpc("plugins.test", { id: p.id }); })
            .then(function () { b.disabled = false; okm.textContent = T("of.test_ok"); okm.classList.remove("hidden"); }).catch(function (x) { b.disabled = false; showErr(x); });
        });
      });
    }).catch(fail);
  }
  function plName(p) { return (p && p.source === "official" && state.dict["of." + p.id + ".n"]) ? T("of." + p.id + ".n") : (p.name || p.id); }

  // ---------- official catalog (card wall) ----------
  var OF_BRAND = { "rss-feed": "br-rss", "web-monitor": "br-web", wecom: "br-wecom", dingtalk: "br-ding", email: "br-mail", "daily-digest": "br-digest" };
  var catView = { cat: "all" };
  function catalogWall(cat) {
    var cats = [["all", "of.c_all"], ["trigger", "of.c_trigger"], ["connector", "of.c_connector"], ["pack", "of.c_pack"]];
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
  function rpc(method, params) {
    return api("/api/rpc/" + method, { method: "POST", body: params || {} }).then(function (r) { return r.result; });
  }
  function renderPlugins() {
    return Promise.all([rpc("dashboard"), rpc("plugins.list"), rpc("triggers.list"), rpc("backup.list"), api("/api/tasks").catch(function () { return []; }), rpc("plugins.catalog").catch(function () { return []; })]).then(function (res) {
      var d = res[0] || {}, plugins = res[1] || [], trig = res[2] || [], baks = res[3] || [], tname = {}, cat = res[5] || [];
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
        var src = p.source || "local", logo = src === "official" ? '<span class="ap-logo sm ' + (OF_BRAND[p.id] || "br-other") + '">' + ic(((cat.filter(function (e) { return e.id === p.id; })[0]) || {}).icon || "plugins") + "</span>" : src === "ai-generated" ? '<span class="ap-logo sm br-ai">' + ic("sparkle") + "</span>" : '<span class="ico">' + ic("plugins") + "</span>";
        return '<div class="card plug' + (p.enabled ? "" : " off") + '"><div class="trig-head">' + logo + '<div class="grow"><strong>' + esc(plName(p)) + ' <span class="tag">' + esc(cur.version || "-") + '</span> <span class="pill sm src-' + esc(src) + '">' + esc(T("pl.src_" + src.replace("-", "_"))) + '</span></strong><small class="mono">' + esc(p.id) + '</small></div><button class="switch' + (p.enabled ? " on" : "") + '" data-pl-en="' + esc(p.id) + '" role="switch" aria-checked="' + !!p.enabled + '" aria-label="' + esc(T("pl.enabled")) + '"></button></div>' +
          '<div class="perm-box"><small class="muted">' + esc(T("perm.granted_title")) + "</small>" + permCard(p) + "</div>" +
          '<div class="row-actions">' + (hasCfg ? '<button class="btn sm" data-pl-cfg="' + esc(p.id) + '">' + ic("settings", "sm") + esc(T("cfg.open")) + "</button>" : '<span class="muted sm-t">' + esc(T("cfg.none")) + "</span>") +
          (p.id === "daily-digest" && src === "official" ? '<button class="btn sm" data-pl-pack="' + esc(p.id) + '">' + ic("calendar", "sm") + esc(T("of.apply_pack")) + "</button>" : "") +
          (src === "ai-generated" ? '<button class="btn sm" data-pl-ai="' + esc(p.id) + '">' + ic("sparkle", "sm") + esc(T("ai.iterate")) + "</button>" : "") + '<span class="grow"></span><button class="btn sm ghost danger" data-pl-uninstall="' + esc(p.id) + '">' + esc(T("pl.uninstall")) + "</button></div></div>";
      }).join("") + "</div>" : emptyState("plugins", T("pl.empty"), T("pl.empty_sub"));
      var trow = trig.length ? '<div class="grid g2 trig-cards">' + trig.map(function (t) { return triggerCard(t, tname); }).join("") + "</div>" : '<p class="muted">' + esc(T("pl.no_triggers")) + "</p>";
      var brow = '<p class="muted">' + esc(baks.length ? T("pl.backups_n", { n: baks.length }) : T("pl.no_backups")) + "</p>";
      mount('<div class="page-head"><div><h1>' + esc(T("nav.plugins")) + "</h1><p>" + esc(T("pl.sub")) + '</p></div><div class="actions"><button class="btn ghost" id="bk-now">' + ic("download", "sm") + esc(T("pl.backup_now")) + '</button><button class="btn" id="pl-add">' + ic("upload", "sm") + esc(T("pl.install")) + '</button><button class="btn primary" id="pl-ai">' + ic("sparkle", "sm") + esc(T("ai.title")) + "</button></div></div>" +
        '<h2 class="sec-h">' + ic("star") + esc(T("of.title")) + '<span class="muted sec-sub">' + esc(T("of.sub")) + "</span></h2>" + catalogWall(cat) +
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

  // ---------- RUNS ----------
  var runsView = { q: "", sort: "new", seq: 0 };
  function renderRuns(params) {
    var st = params.get("status") || "", task = params.get("task") || "", v = runsView;
    var url = function () { return "/api/runs?limit=200&sort=" + enc(v.sort) + (st ? "&status=" + enc(st) : "") + (task ? "&task_id=" + enc(task) : "") + (v.q ? "&q=" + enc(v.q) : ""); };
    return api(url()).then(function (runs) {
      var f = [["", T("runs.all_f")], ["running", T("st.running")], ["succeeded", T("st.succeeded")], ["failed", T("st.failed")]];
      mount('<div class="page-head"><div><h1>' + esc(T("nav.runs")) + "</h1><p>" + esc(T("runs.times_in", { tz: tzLabel() })) + '</p></div><div class="actions"><button class="btn primary" id="r-quick">' + ic("sparkle", "sm") + esc(T("home.quick")) + "</button></div></div>" +
        '<section class="card flush"><div class="toolbar"><label class="search-in">' + ic("search", "sm") + '<input id="rq" type="search" maxlength="100" placeholder="' + esc(T("runs.search")) + '" value="' + esc(v.q) + '"></label>' +
        '<div class="filters">' + f.map(function (x) { return '<button data-f="' + x[0] + '" class="' + (x[0] === st ? "active" : "") + '">' + esc(x[1]) + "</button>"; }).join("") + "</div>" +
        '<select id="rsort" aria-label="' + esc(T("runs.sort")) + '">' + [["new", T("runs.s_new")], ["old", T("runs.s_old")], ["cost", T("runs.s_cost")], ["tokens", T("runs.s_tokens")], ["duration", T("runs.s_duration")]].map(function (x) { return opt(x[0], x[1], x[0] === v.sort); }).join("") + "</select>" +
        '<span class="grow"></span><span class="muted">' + esc(task && runs[0] ? T("runs.for", { name: runs[0].task_name }) : "") + '</span></div><div id="rtable"></div></section>', "runs", [[T("nav.runs")]]);
      $("#r-quick").addEventListener("click", function () { quickRunModal(); });
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
        '<div class="card stat"><small>' + esc(T("col.cost")) + "</small><strong>" + esc(r.cost_source && r.cost_currency && r.cost_currency !== state.currency ? money(r.cost_amount, r.cost_currency, 4) : usd(r.cost_usd, 4)) + costBadge(r.cost_source) + "</strong><span>" + esc(r.cost_source && r.cost_currency && r.cost_currency !== state.currency ? T("rd.approx_in", { v: usd(r.cost_usd, 4) }) : T("rd.bucket", { b: r.bucket })) + "</span></div>" +
        '<div class="card stat"><small>Tokens</small><strong>' + esc(num(r.input_tokens + r.output_tokens + (r.cache_read_tokens || 0) + (r.cache_write_tokens || 0))) + "</strong><span>" + esc(T("rd.in_out", { i: num(r.input_tokens), o: num(r.output_tokens) }) + (r.cache_read_tokens || r.cache_write_tokens ? " · " + T("rd.cache_rw", { r: num(r.cache_read_tokens || 0), w: num(r.cache_write_tokens || 0) }) : "")) + "</span></div></div>" +
        costCalcHtml(r) +
        '<div class="grid g-side"><section class="card"><div class="card-head"><span class="ico">' + ic("terminal") + '</span><h2>' + esc(T("rd.log")) + '</h2><span class="link" id="live-ind">' + (live ? '<span class="live">' + esc(T("rd.live")) + "</span>" : '<span class="muted">' + esc(T("rd.finished")) + "</span>") + '</span></div><div id="lv"></div></section>' +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("info") + '</span><h3>' + esc(T("rd.details")) + '</h3></div><dl class="kv"><dt>' + esc(T("col.run")) + "</dt><dd><code>" + esc(r.id) + "</code></dd><dt>" + esc(T("col.task")) + "</dt><dd>" + (r.task_id ? '<a href="#/tasks/' + enc(r.task_id) + '">' + esc(r.task_name) + "</a>" + (r.version_no ? " · v" + esc(r.version_no) : "") : esc(T("run.adhoc"))) +
        "</dd><dt>" + esc(T("f.provider")) + "</dt><dd>" + esc(r.provider || "—") + "</dd><dt>" + esc(T("f.model")) + "</dt><dd>" + esc(r.model || "—") + "</dd><dt>" + esc(T("f.prompt")) + "</dt><dd>" + esc(r.prompt) + "</dd>" + (r.error ? "<dt>" + esc(T("rd.error")) + '</dt><dd class="err">' + esc(runErr(r.error)) + "</dd>" : "") + "</dl></section></div>",
        "runs", [[T("nav.runs"), "#/runs"], [runTitle(r)]]);
      bumpIdle();
      logView($("#lv"), id, live, function (why) {
        if (why === "idle") { $("#live-ind").innerHTML = '<span class="muted">' + esc(T("rd.idle")) + "</span>"; return; }
        if (live) setTimeout(function () { if (location.hash === "#/runs/" + id) refresh(); }, 300);
      });
    });
  }
  // Run detail: how the cost was computed (tokens × price per 1M, per model).
  function costCalcHtml(r) {
    var c = r.cost_detail, src = r.cost_source;
    var head = '<section class="card"><div class="card-head"><span class="ico">' + ic("receipt") + '</span><h3>' + esc(T("cost.calc")) + "</h3>" + costBadge(src) + "</div>";
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
  // ---------- SPEND ----------
  var taskNames = {};
  function bucketName(b) { return b.scope === "global" ? T("sp.global") : b.scope === "bucket" ? (T("bucket." + b.scope_id) === "bucket." + b.scope_id ? cap(b.scope_id) : T("bucket." + b.scope_id)) : (taskNames[b.scope_id] || T("col.task") + " " + String(b.scope_id).slice(0, 12)); }
  var spendView = { range: "7d", by: "task" }, spendCache = {};
  function renderSpend() {
    spendCache = {};
    var spendP = api("/api/spend"); // start all while the route's cache window is open
    var bkKey = spendView.range + "|" + spendView.by;
    var bkP = api("/api/spend/breakdown?range=" + spendView.range + "&by=" + spendView.by).then(function (r) { spendCache[bkKey] = r; }, function () {});
    return api("/api/tasks").then(function (ts) { (ts.tasks || ts || []).forEach(function (t) { taskNames[t.id] = t.name; }); }, function () {}).then(function () { return bkP; }).then(function () { return spendP; }).then(function (d) {
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
        '<nav class="tabs" role="tablist">' + [["task", T("sp.by_task")], ["provider_model", T("sp.by_provider_model")], ["model", T("sp.by_model")], ["provider", T("sp.by_provider")]].map(function (x) { return '<button role="tab" data-by="' + x[0] + '" class="' + (x[0] === spendView.by ? "active" : "") + '" aria-selected="' + (x[0] === spendView.by) + '">' + esc(x[1]) + "</button>"; }).join("") + '</nav><div id="sp-body" class="sp-body"></div></section>' +
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
        $("#sp-body").innerHTML = '<div class="sp-total"><strong>' + esc(usd(r.total_usd || 0, 4)) + "</strong>" + (r.estimated_runs ? costBadge("estimate") : "") + ' <span class="muted">' + esc(T("sp.since", { d: spendView.range === "all" ? T("sp.rall") : r.since })) + (r.estimated_runs ? " · " + T("sp.est_runs", { n: r.estimated_runs }) : "") + "</span></div>" +
          ((r.days || []).length ? '<div class="bars sp-bars">' + r.days.map(function (x) { return '<div class="bar" title="' + esc(x.day + " · " + usd(x.usd, 4)) + '"><span data-h="' + (x.usd / mxd * 100) + '"></span><small>' + esc(x.day.slice(5)) + "</small></div>"; }).join("") + "</div>" : "") +
          (rows.length ? '<table class="table"><thead><tr><th>' + esc(T("sp.col_" + spendView.by)) + "</th><th>" + esc(T("nav.runs")) + "</th><th>Tokens</th><th>" + esc(T("sp.share")) + '</th><th class="right">' + esc(T("col.cost")) + "</th></tr></thead><tbody>" + rows.map(function (x) {
            var sh = r.total_usd ? x.usd / r.total_usd * 100 : 0;
            var tokTip = T("sp.tok_tip", { i: num(x.input_tokens || 0), o: num(x.output_tokens || 0), r: num(x.cache_read_tokens || 0), w: num(x.cache_write_tokens || 0), q: num(x.requests || 0) });
            var badge = x.estimated_runs || x.legacy_runs ? costBadge(x.estimated_runs ? "estimate" : "legacy") : (x.subscription_runs === x.runs ? costBadge("subscription") : "");
            return "<tr><td><strong>" + esc(nm(x)) + "</strong>" + badge + "</td><td>" + esc(x.runs) + '</td><td title="' + esc(tokTip) + '">' + esc(num(x.tokens)) + '<small class="muted">' + esc(T("rd.in_out", { i: num(x.input_tokens || 0), o: num(x.output_tokens || 0) })) + (x.cache_read_tokens ? " · " + esc(T("sp.cache_n", { n: num(x.cache_read_tokens) })) : "") + '</small></td><td><div class="progress thin"><span data-w="' + sh + '"></span></div></td><td class="right"><strong>' + esc(usd(x.usd, 4)) + "</strong></td></tr>";
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
  // Left nav + one section at a time. Every row has an id, so #/settings/<section>/<item>
  // scrolls to it. Autosaved fields show "Saved ✓" next to them. Dangerous actions
  // live in the danger zone at the bottom of General.
  var SET_SECTIONS = ["general", "providers", "budgets", "pricing", "security", "notifications", "network", "data", "about"];
  var SET_ICONS = { general: "settings", providers: "key", budgets: "wallet", pricing: "receipt", security: "shield", notifications: "bell", network: "globe", data: "folder", about: "info" };
  var RELEASES_PAGE = "https://github.com/Zhengmingming1/tidebot-releases/releases/latest";
  var NET_CODES = ["timeout", "dns", "tls", "proxy", "refused", "http_404", "http", "signature", "checksum", "unknown"];
  // Classified network error with a "how to fix" hint and the manual download link.
  function netErrHtml(code, detail, attempts) {
    code = NET_CODES.indexOf(code) >= 0 ? code : "unknown";
    var h = '<div class="net-err" role="alert"><p class="err"><strong>' + esc(T("net.e." + code)) + "</strong></p>" +
      '<p class="muted">' + esc(T("net.h." + code)) + "</p>";
    if (attempts && attempts.length > 1) h += '<ul class="net-att">' + attempts.map(function (a) { return "<li><code>" + esc(a.source) + "</code> " + esc(T("net.e." + (NET_CODES.indexOf(a.code) >= 0 ? a.code : "unknown"))) + "</li>"; }).join("") + "</ul>";
    if (detail) h += '<details><summary>' + esc(T("net.detail")) + '</summary><pre class="mono">' + esc(detail) + "</pre></details>";
    return h + '<p class="net-fix"><a class="btn sm" href="#/settings/network">' + ic("globe", "sm") + esc(T("net.set_proxy")) + '</a> <a class="btn sm ghost" href="' + esc(RELEASES_PAGE) + '" target="_blank" rel="noopener">' + ic("download", "sm") + esc(T("net.manual")) + "</a></p></div>";
  }
  function proxyLabel(e) {
    if (!e || e.source === "none") return T(e && e.note === "pac" ? "net.eff_pac" : "net.eff_direct");
    return T("net.eff_" + (e.source.indexOf("system") === 0 ? "system" : e.source), { p: e.https || e.http || "", s: e.source.replace("system:", "") });
  }
  function savedMark(el) {
    if (!el) return;
    var host = el.closest(".set-row, .field") || el.parentNode, m = $(".saved-mark", host);
    if (!m) { m = document.createElement("span"); m.className = "saved-mark"; m.setAttribute("role", "status"); host.appendChild(m); }
    m.textContent = T("set.saved");
    m.classList.remove("fade"); void m.offsetWidth; m.classList.add("fade");
  }
  function renderSettings(section, item) {
    section = SET_SECTIONS.indexOf(section) >= 0 ? section : "general";
    return api("/api/settings").then(function (st) {
      // title/sub are trusted dictionary strings; "{cmd}" is replaced with a <code> chip after escaping.
      var row = function (key, title, sub, right, cmd) { return '<div class="set-row" id="set-' + key + '"><div class="lbl"><strong>' + esc(title) + "</strong><small>" + esc(sub).replace("{cmd}", cmd ? "<code>" + esc(cmd) + "</code>" : "") + "</small></div>" + right + "</div>"; };
      var nav = '<nav class="set-nav" aria-label="' + esc(T("nav.settings")) + '">' + SET_SECTIONS.map(function (k) {
        return '<a href="#/settings/' + k + '"' + (k === section ? ' class="active" aria-current="page"' : "") + ">" + ic(SET_ICONS[k], "sm") + "<span>" + esc(T("setnav." + k)) + "</span></a>";
      }).join("") + "</nav>";
      var head = '<div class="card-head"><span class="ico">' + ic(SET_ICONS[section]) + "</span><h2>" + esc(T("setnav." + section)) + "</h2></div>";
      var body = "", after = function () {};
      if (section === "general") {
        body = '<section class="card">' + head +
          row("language", T("set.language"), T("set.language_sub"), langSelect("set-lang")) +
          row("timezone", T("set.tz"), T("set.tz_sub"), "<span>" + esc(tzLabel()) + "</span>") +
          row("webui", T("set.addr"), T(st.allow_lan ? "set.lan_on" : "set.lan_off"), "<code>" + esc(st.addr) + "</code>") +
          row("https", "HTTPS", T("set.https_sub"), '<span class="pill ' + (st.tls ? "ok" : "plain") + '">' + esc(T(st.tls ? "set.enabled" : "set.off")) + "</span>") +
          row("idle", T("set.idle"), T("set.idle_sub"), "<span>" + esc(T("set.min", { n: st.idle_minutes })) + "</span>") + "</section>" +
          '<section class="card danger-zone" id="set-danger"><div class="card-head"><span class="ico">' + ic("bolt") + "</span><h2>" + esc(T("set.danger")) + "</h2></div>" +
          row("webui_off", T("set.webui_on"), T("set.webui_on_sub"), '<button class="btn danger sm" id="dz-webui">' + esc(T("set.turn_off")) + "</button>", "agentctl webui on") +
          row("reset_ui", T("set.reset_ui"), T("set.reset_ui_sub"), '<button class="btn danger sm" id="dz-reset">' + esc(T("set.reset")) + "</button>") + "</section>";
        after = function () {
          $("#set-lang").addEventListener("change", function (e) { setLanguage(e.target.value).then(function () { savedMark($("#set-lang")); }); });
          $("#dz-webui").addEventListener("click", function () {
            confirmDialog({ title: T("set.off_title"), body: T("set.confirm_off"), ok: T("set.turn_off"), danger: true }).then(function (y) { if (y) api("/api/webui", { method: "POST", body: { enabled: false } }).then(function () { toast(T("toast.webui_off")); }).catch(fail); });
          });
          $("#dz-reset").addEventListener("click", function () {
            confirmDialog({ title: T("set.reset_ui"), body: T("set.reset_ui_confirm"), ok: T("set.reset"), danger: true }).then(function (y) {
              if (!y) return;
              try { Object.keys(localStorage).filter(function (k) { return /^tb_/.test(k); }).forEach(function (k) { localStorage.removeItem(k); }); } catch (e) {}
              state.notifSeen = ""; toast(T("set.reset_done"));
            });
          });
        };
      } else if (section === "providers") {
        body = '<section class="card">' + head + '<div id="pv-box" class="pad-x"><p class="muted">…</p></div></section>';
        after = function () { providerCards($("#pv-box")).catch(fail); };
      } else if (section === "budgets") {
        var g = (st.budgets || []).filter(function (x) { return x.scope === "global" && x.period === "day"; })[0] || {};
        var buckets = (st.budgets || []).filter(function (x) { return x.scope === "bucket"; });
        var fx = st.fx || { display: "USD", usd_cny: 7.1 };
        body = '<section class="card">' + head.replace("</h2>", "</h2>" + tip(T("set.bud_auto"))) +
          '<div class="grid g3 pad-x" id="set-fx"><label class="field"><span>' + esc(T("set.fx_cur")) + tip(T("set.fx_cur_hint")) + '</span><select id="fx-cur">' + opt("USD", T("set.fx_usd"), fx.display === "USD") + opt("CNY", T("set.fx_cny"), fx.display === "CNY") + '</select></label>' +
          '<label class="field"><span>' + esc(T("set.fx_rate")) + tip(T("set.fx_rate_hint")) + '</span><div class="in-group"><span class="in-pre">1 USD =</span><input id="fx-rate" type="number" step="0.01" min="0.1" max="100" value="' + esc(fx.usd_cny) + '"><span class="in-pre">CNY</span></div></label></div>' +
          '<div class="grid g3 pad-x" id="bud">' +
          '<label class="field" id="set-g_usd"><span>' + esc(T("set.g_usd") + " (" + curSym(fx.display) + ")") + '</span><input data-bud="g_usd" type="number" step="0.01" min="0" value="' + esc(g.max_usd || "") + '"></label>' +
          '<label class="field" id="set-g_tok"><span>' + esc(T("set.g_tok")) + '</span><input data-bud="g_tok" type="number" step="1000" min="0" value="' + esc(g.max_tokens || "") + '"></label>' +
          buckets.map(function (x) { return '<label class="field" id="set-b_' + esc(x.scope_id) + '"><span>' + esc(T("set.b_usd", { b: bucketName(x) }) + " (" + curSym(fx.display) + ")") + '</span><input data-bud="b" data-sid="' + esc(x.scope_id) + '" type="number" step="0.01" min="0" value="' + esc(x.max_usd || "") + '"></label>'; }).join("") +
          '</div><p class="pad-x"><a href="#/spend">' + esc(T("set.bud_more")) + " " + ic("arrow", "sm") + "</a></p></section>";
        after = function () {
          var saveFx = function (el) {
            rpc("pricing.fx.set", { display: $("#fx-cur").value, usd_cny: parseFloat($("#fx-rate").value) || 0 }).then(function (f) { state.currency = f.display; savedMark(el); setTimeout(refresh, 400); }).catch(fail);
          };
          $("#fx-cur").addEventListener("change", function (e) { saveFx(e.target); });
          $("#fx-rate").addEventListener("change", function (e) { saveFx(e.target); });
          $$("[data-bud]").forEach(function (inp) {
            inp.addEventListener("change", function () {
              var k = inp.getAttribute("data-bud"), v = inp.value, req;
              if (k === "g_usd" || k === "g_tok") {
                var u = $('[data-bud="g_usd"]').value, t = $('[data-bud="g_tok"]').value;
                req = { scope: "global", period: "day", max_usd: parseFloat(u) || 0, max_tokens: parseInt(t, 10) || 0 };
              } else {
                var sid = inp.getAttribute("data-sid");
                req = { scope: "bucket", scope_id: sid, bucket: sid, period: "day", max_usd: parseFloat(v) || 0 };
              }
              api("/api/budgets", { method: "POST", body: req }).then(function () { savedMark(inp); }).catch(fail);
            });
          });
        };
      } else if (section === "security") {
        body = '<section class="card">' + head +
          row("password", T("login.password"), T("set.pw_sub"), '<span class="pill ok">' + esc(T("set.set")) + "</span>", "agentctl auth set-password") +
          row("2fa", T("set.totp"), T(st.totp_enabled ? "set.totp_on_sub" : "set.totp_off_sub"), st.totp_enabled ?
            '<span class="row-gap"><span class="pill ok">' + esc(T("set.on")) + '</span><button class="btn sm" id="tf-off">' + esc(T("tf.disable")) + "</button></span>" :
            '<button class="btn primary sm" id="tf-on">' + esc(T("tf.enable")) + "</button>") +
          row("cookies", T("set.cookies"), T("set.cookies_sub"), "") +
          row("session", T("set.session"), T("set.session_sub"), '<button class="btn sm" id="btn-logout">' + ic("logout", "sm") + esc(T("set.signout")) + "</button>") + "</section>";
        after = function () {
          $("#btn-logout").addEventListener("click", function () {
            api("/api/logout", { method: "POST", body: {} }).catch(function () {}).then(function () { state.csrf = ""; showLogin(); });
          });
          if ($("#tf-on")) $("#tf-on").addEventListener("click", twoFASetup);
          if ($("#tf-off")) $("#tf-off").addEventListener("click", twoFADisable);
        };
      } else if (section === "notifications") {
        body = '<section class="card">' + head + '<div id="nt-box"><p class="muted pad-x">…</p></div></section>';
        after = function () {
          rpc("notify.overview", {}).then(function (o) {
            var hours = '<select id="nt-digest" aria-label="' + esc(T("set.digest")) + '"><option value="">' + esc(T("set.off")) + "</option>" + Array.from({ length: 24 }, function (_, h) { return '<option value="' + h + '"' + (String(h) === o.digest_hour ? " selected" : "") + ">" + (h < 10 ? "0" : "") + h + ":00</option>"; }).join("") + "</select>";
            $("#nt-box").innerHTML =
              row("quiet", T("set.quiet"), T("set.quiet_sub"), '<input id="nt-quiet" maxlength="11" placeholder="22:00-08:00" value="' + esc(o.quiet || "") + '">') +
              row("digest", T("set.digest"), T("set.digest_sub"), hours) +
              row("channels", T("set.channels"), T("set.channels_sub"), "", "agentctl notify add") +
              '<ul class="plain-list pad-x">' + ((o.channels || []).length ? o.channels.map(function (c) { return "<li>" + ic("bell", "sm") + "<strong>" + esc(c.name) + '</strong> <span class="pill plain">' + esc(c.kind) + "</span> " + '<span class="pill ' + (c.enabled ? "ok" : "plain") + '">' + esc(T(c.enabled ? "set.on" : "set.off")) + "</span></li>"; }).join("") : '<li class="muted">' + esc(T("set.no_channels")) + "</li>") + "</ul>";
            $("#nt-quiet").addEventListener("change", function (e) {
              var v = e.target.value.trim();
              if (v && !/^\d{2}:\d{2}-\d{2}:\d{2}$/.test(v)) { toast(T("set.quiet_bad"), true); return; }
              rpc("notify.settings", { quiet: v }).then(function () { savedMark(e.target); }).catch(fail);
            });
            $("#nt-digest").addEventListener("change", function (e) {
              if (e.target.value === "") return;
              rpc("notify.settings", { digest_hour: +e.target.value }).then(function () { savedMark(e.target); }).catch(fail);
            });
            jumpTo(item);
          }).catch(fail);
        };
      } else if (section === "data") {
        body = '<section class="card">' + head +
          row("backup", T("set.backup"), T("set.backup_sub"), '<button class="btn sm" id="bk-now">' + esc(T("set.backup_now")) + "</button>", "agentctl backup create --passphrase") +
          '<div id="bk-list" class="pad-x"><p class="muted">…</p></div>' +
          row("restore", T("set.restore"), T("set.restore_sub"), "", "agentctl backup restore <file>") + "</section>";
        after = function () {
          var load = function () {
            return rpc("backups.list", {}).then(function (l) {
              $("#bk-list").innerHTML = l && l.length ? '<table class="tbl"><thead><tr><th>' + esc(T("set.bk_when")) + "</th><th>" + esc(T("set.bk_kind")) + '</th><th class="right">' + esc(T("set.bk_size")) + "</th></tr></thead><tbody>" + l.slice(0, 10).map(function (x) {
                return "<tr><td>" + esc(when(x.created_at)) + "</td><td>" + esc(T("set.bk_" + (x.kind === "auto" ? "auto" : "manual"))) + '</td><td class="right">' + esc(fmtBytes(x.size_bytes)) + "</td></tr>";
              }).join("") + "</tbody></table>" : '<p class="muted">' + esc(T("set.bk_none")) + "</p>";
            });
          };
          load().catch(fail);
          $("#bk-now").addEventListener("click", function (e) {
            e.target.disabled = true;
            rpc("backups.create", {}).then(function () { toast(T("set.bk_done")); return load(); }).catch(fail).then(function () { e.target.disabled = false; });
          });
        };
      } else if (section === "pricing") {
        body = '<section class="card" id="set-pcat">' + head +
          row("pcat-auto", T("pc.auto"), T("pc.auto_sub"), '<button class="switch" id="pc-auto" role="switch" aria-checked="false" aria-label="' + esc(T("pc.auto")) + '"></button>') +
          '<div class="pad-x" id="pc-body"><p class="muted">…</p></div></section>';
        after = function () {
          var paint = function (st) {
            var a = st.active, s0 = st.state || {}, on = !!(st.settings && st.settings.auto);
            var sw = $("#pc-auto"); sw.classList.toggle("on", on); sw.setAttribute("aria-checked", on);
            var res = s0.result ? '<span class="pill sm ' + (s0.result === "error" ? "bad" : s0.result === "installed" ? "ok" : "plain") + '">' + esc(T("pc.r_" + s0.result)) + "</span>" : '<span class="muted">' + esc(T("pc.never")) + "</span>";
            $("#pc-body").innerHTML = '<dl class="kv">' +
              "<dt>" + esc(T("pc.in_use")) + "</dt><dd>" + '<span class="pill sm ' + (st.origin === "remote" ? "info" : "plain") + '">' + esc(T("pc.o_" + st.origin)) + "</span> " + esc(when(a.generated)) + "</dd>" +
              "<dt>" + esc(T("pc.count")) + "</dt><dd>" + esc(T("pc.count_v", { p: a.presets, m: a.models })) + "</dd>" +
              "<dt>" + esc(T("pc.last")) + "</dt><dd>" + res + (s0.checked ? ' <span class="muted">' + esc(when(s0.checked)) + (s0.from ? " · " + esc(s0.from) : "") + "</span>" : "") + (s0.error ? '<small class="err">' + esc(s0.error) + "</small>" : "") + "</dd>" +
              "<dt>" + esc(T("pc.order")) + "</dt><dd>" + esc(T("pc.order_v")) + "</dd></dl>" +
              '<table class="table compact mt"><thead><tr><th>' + esc(T("pc.src")) + "</th><th>" + esc(T("pc.fetched")) + "</th></tr></thead><tbody>" +
              (a.sources || []).map(function (x) { return '<tr><td><a class="link" href="' + esc(x.url) + '" target="_blank" rel="noopener noreferrer">' + esc(x.id === "models.dev" ? "models.dev" : x.name) + "</a>" + (x.id === "official" ? ' <span class="pill sm ok plain">' + esc(T("pr.o_official")) + "</span>" : "") + "</td><td>" + esc(x.fetched) + "</td></tr>"; }).join("") + "</tbody></table>" +
              '<p class="row-actions"><button class="btn" id="pc-check">' + ic("refresh", "sm") + esc(T("pc.check")) + '</button><span class="muted sm-t">' + esc(T("pc.privacy")) + "</span></p>";
            $("#pc-check").addEventListener("click", function (e) {
              var b = e.currentTarget; b.disabled = true;
              rpc("pricing.catalog.check", {}).then(function (r) { var x = (r.state || {}).result; toast(T("pc.r_" + (x || "error")), x === "error"); paint(r); }).catch(fail).then(function () { if (document.contains(b)) b.disabled = false; });
            });
          };
          rpc("pricing.catalog.get", {}).then(function (st) {
            paint(st);
            $("#pc-auto").addEventListener("click", function (e) {
              var on = !e.currentTarget.classList.contains("on");
              rpc("pricing.catalog.set", { auto: on }).then(function (r) { paint(r); savedMark($("#pc-auto")); }).catch(fail);
            });
          }).catch(fail);
        };
      } else if (section === "network") {
        body = '<section class="card" id="set-net">' + head +
          row("proxy", T("net.proxy"), T("net.proxy_sub"), '<div class="seg" id="net-mode" role="radiogroup" aria-label="' + esc(T("net.proxy")) + '">' + [["auto", T("net.m_auto")], ["manual", T("net.m_manual")], ["off", T("net.m_off")]].map(function (m) { return '<button type="button" role="radio" data-m="' + m[0] + '">' + esc(m[1]) + "</button>"; }).join("") + "</div>") +
          '<div class="pad-x net-manual hidden" id="net-man">' + fieldHtml(T("net.url"), '<input id="net-url" class="mono" autocomplete="off" spellcheck="false" placeholder="http://127.0.0.1:7890">', T("net.url_hint")) +
          fieldHtml(T("net.bypass"), '<input id="net-bypass" class="mono" autocomplete="off" spellcheck="false" placeholder="localhost, 127.0.0.1, .corp.example">', T("net.bypass_hint")) + "</div>" +
          '<div class="pad-x"><p class="muted" id="net-eff"></p><p><button class="btn" id="net-test">' + ic("pulse", "sm") + esc(T("net.test")) + '</button> <button class="btn primary" id="net-save">' + esc(T("btn.save")) + '</button></p><div id="net-out" aria-live="polite"></div></div></section>' +
          '<section class="card" id="set-mirrors"><div class="card-head"><span class="ico">' + ic("download") + "</span><h2>" + esc(T("net.mirrors")) + '</h2></div><div class="pad-x"><p class="muted">' + esc(T("net.mirrors_sub")) + '</p><div id="net-mlist"></div>' +
          '<p class="net-presets" id="net-presets"></p><p><button class="btn sm" id="net-madd">' + ic("plus", "sm") + esc(T("net.m_add")) + "</button></p></div></section>";
        after = function () {
          var cur = null, view = null;
          var paintMode = function () {
            $$("#net-mode button").forEach(function (b) { var on = b.getAttribute("data-m") === cur.proxy.mode; b.classList.toggle("active", on); b.setAttribute("aria-checked", on); });
            $("#net-man").classList.toggle("hidden", cur.proxy.mode !== "manual");
          };
          var paintMirrors = function () {
            var ms = cur.mirrors || [];
            $("#net-mlist").innerHTML = '<ol class="net-mlist"><li class="fixed"><code>github.com</code> <span class="muted">' + esc(T("net.m_direct")) + "</span></li>" + ms.map(function (m, i) {
              return '<li><input class="mono" data-mi="' + i + '" value="' + esc(m.prefix || m.manifest || "") + '" aria-label="' + esc(T("net.m_url")) + '"><button type="button" class="icon-btn" data-mdel="' + i + '" aria-label="' + esc(T("btn.remove")) + '">' + ic("x") + "</button></li>";
            }).join("") + "</ol>";
            $$("[data-mi]").forEach(function (inp) { inp.addEventListener("change", function () { var m = cur.mirrors[+inp.getAttribute("data-mi")], v = inp.value.trim(); if (m.manifest) m.manifest = v; else m.prefix = v; m.name = hostOf(v); }); });
            $$("[data-mdel]").forEach(function (b) { b.addEventListener("click", function () { cur.mirrors.splice(+b.getAttribute("data-mdel"), 1); paintMirrors(); }); });
            $("#net-presets").innerHTML = '<span class="muted">' + esc(T("net.presets")) + "</span> " + (view.presets || []).map(function (p, i) {
              var used = ms.some(function (m) { return m.prefix === p.prefix; });
              return '<button type="button" class="chip" data-pre="' + i + '"' + (used ? " disabled" : "") + ">" + ic("plus", "sm") + esc(p.name) + "</button>";
            }).join(" ") + ' <small class="muted">' + esc(T("net.presets_note")) + "</small>";
            $$("[data-pre]").forEach(function (b) { b.addEventListener("click", function () { var p = view.presets[+b.getAttribute("data-pre")]; cur.mirrors.push({ name: p.name, prefix: p.prefix }); paintMirrors(); }); });
          };
          var hostOf = function (v) { try { return new URL(v).host; } catch (e) { return v; } };
          var read = function () {
            cur.proxy.url = $("#net-url").value.trim(); cur.proxy.bypass = $("#net-bypass").value.trim();
            cur.mirrors = (cur.mirrors || []).filter(function (m) { return (m.prefix || m.manifest || "").trim(); });
            return cur;
          };
          var paint = function (v) {
            view = v; cur = JSON.parse(JSON.stringify(v.settings));
            $("#net-url").value = cur.proxy.url || ""; $("#net-bypass").value = cur.proxy.bypass || "";
            $("#net-eff").textContent = T("net.now", { p: proxyLabel(v.effective) });
            paintMode(); paintMirrors();
          };
          $$("#net-mode button").forEach(function (b) { b.addEventListener("click", function () { cur.proxy.mode = b.getAttribute("data-m"); paintMode(); if (cur.proxy.mode === "manual") $("#net-url").focus(); }); });
          $("#net-madd").addEventListener("click", function () { cur.mirrors.push({ name: "", prefix: "" }); paintMirrors(); var l = $$("[data-mi]"); if (l.length) l[l.length - 1].focus(); });
          $("#net-save").addEventListener("click", function (e) {
            e.currentTarget.disabled = true; var btn = e.currentTarget;
            rpc("network.set", read()).then(function (v) { paint(v); savedMark(btn); }).catch(fail).then(function () { btn.disabled = false; });
          });
          $("#net-test").addEventListener("click", function (e) {
            var btn = e.currentTarget; btn.disabled = true; $("#net-out").innerHTML = '<p class="muted"><span class="spin"></span> ' + esc(T("net.testing")) + "</p>";
            rpc("network.test", { settings: read() }).then(function (r) {
              btn.disabled = false;
              var ok = (r.results || []).filter(function (x) { return x.ok; })[0];
              var h = '<p class="muted">' + esc(T("net.used", { p: proxyLabel(r.effective) })) + '</p><ul class="net-res">' + (r.results || []).map(function (x) {
                return '<li class="' + (x.ok ? "ok" : "bad") + '">' + ic(x.ok ? "check" : "x", "sm") + "<code>" + esc(x.source) + "</code> " + (x.ok ? esc(T("net.ok", { v: x.version, ms: x.ms })) : esc(T("net.e." + (NET_CODES.indexOf(x.code) >= 0 ? x.code : "unknown")))) + "</li>";
              }).join("") + "</ul>";
              if (!ok) { var f = (r.results || [])[0] || {}; h += netErrHtml(f.code, (r.results || []).map(function (x) { return x.source + ": " + (x.error || "ok"); }).join("\n")); }
              $("#net-out").innerHTML = h;
            }).catch(function (err) { btn.disabled = false; $("#net-out").innerHTML = '<p class="err">' + esc(err.message) + "</p>"; });
          });
          rpc("network.get", {}).then(paint).catch(fail);
        };
      } else if (section === "about") {
        body = '<section class="card">' + head +
          row("version", T("set.version"), T("set.version_sub"), "<code>" + esc(state.version || "dev") + "</code>") +
          row("terms", T("set.terms"), T("set.terms_sub"), "") +
          row("source", T("set.source"), T("set.source_sub"), '<a href="' + esc(RELEASES_PAGE) + '" target="_blank" rel="noopener"><code>github.com/Zhengmingming1/tidebot-releases</code></a>') + "</section>" +
          '<section class="card" id="set-update"><div class="card-head"><span class="ico">' + ic("refresh") + "</span><h2>" + esc(T("up.title")) + "</h2></div>" +
          row("up_auto", T("up.auto"), T("up.auto_sub"), '<label class="check"><input type="checkbox" id="up-auto"> ' + esc(T("up.on")) + "</label>") +
          row("up_every", T("up.every"), T("up.every_sub"), '<select id="up-every"><option value="24">' + esc(T("up.daily")) + '</option><option value="168">' + esc(T("up.weekly")) + "</option></select>") +
          row("up_channel", T("up.channel"), T("up.channel_sub"), "<code>stable</code>") +
          '<div id="up-box" class="pad-x" aria-live="polite"><p class="muted">…</p></div></section>';
        after = function () {
          var paint = function (st) {
            var s = st.settings || {}, last = st.last, inst = st.install || {}, res = st.result, h = "";
            $("#up-auto").checked = !!s.enabled;
            $("#up-every").value = String(s.interval_hours >= 168 ? 168 : 24);
            if (res && res.status === "rolled_back") h += '<p class="err">' + esc(T("up.rolled_back", { v: res.to, why: res.detail || "" })) + "</p>";
            if (st.error) h += netErrHtml(st.error_code, st.error, st.attempts);
            if (last && last.available) {
              h += "<h3>" + esc(T("up.avail", { v: last.latest, cur: last.current })) + "</h3>" +
                (last.date ? '<p class="muted">' + esc(last.date) + "</p>" : "") +
                (last.notes ? '<pre class="changelog">' + esc(last.notes) + "</pre>" : "") +
                (last.notes_url ? '<p><a href="' + esc(last.notes_url) + '" target="_blank" rel="noopener">' + esc(T("up.notes")) + "</a></p>" : "");
              if (last.can_apply) h += '<p><button class="btn primary" id="up-apply">' + esc(T("up.apply")) + '</button> <small class="muted">' + esc(T("up.apply_sub")) + "</small></p>";
              else h += '<p class="muted">' + esc(T("up.notify_only", { kind: inst.kind || "?" })) + (inst.reason ? " — " + esc(inst.reason) : "") + "</p>";
            } else if (last) {
              h += '<p class="muted">' + esc(T("up.none", { v: st.current })) + "</p>";
            }
            h += '<p><button class="btn" id="up-check">' + esc(T("up.check")) + '</button> <small class="muted">' + esc(T("up.privacy")) + " " + esc(T("net.via", { p: proxyLabel(st.proxy) })) + "</small></p>";
            $("#up-box").innerHTML = h;
            $("#up-check").addEventListener("click", function (e) {
              e.target.disabled = true;
              rpc("update.check", {}).then(paint).catch(function () { return rpc("update.status", {}).then(paint); }).catch(function (err) { fail(err); e.target.disabled = false; });
            });
            var ap = $("#up-apply");
            if (ap) ap.addEventListener("click", function () {
              if (!confirm(T("up.confirm", { v: last.latest }))) return;
              ap.disabled = true; ap.textContent = T("up.applying");
              rpc("update.apply", {}).then(function () {
                toast(T("up.restarting"));
                setTimeout(function () { location.reload(); }, 8000);
              }).catch(function (err) { fail(err); ap.disabled = false; ap.textContent = T("up.apply"); });
            });
          };
          var save = function () {
            rpc("update.settings", { enabled: $("#up-auto").checked, interval_hours: parseInt($("#up-every").value, 10) }).then(function () { savedMark($("#up-auto")); }).catch(fail);
          };
          $("#up-auto").addEventListener("change", save);
          $("#up-every").addEventListener("change", save);
          rpc("update.status", {}).then(paint).catch(fail);
        };
      }
      mount('<div class="page-head"><div><h1>' + esc(T("nav.settings")) + "</h1></div></div>" + '<div class="settings-layout">' + nav + '<div class="set-main">' + body + "</div></div>",
        "settings", [[T("nav.settings"), "#/settings"], [T("setnav." + section)]]);
      after();
      jumpTo(item);
    });
  }
  function jumpTo(item) {
    if (!item) return;
    var el = document.getElementById("set-" + item);
    if (!el) return;
    el.scrollIntoView({ block: "center" });
    el.classList.add("flash");
    setTimeout(function () { el.classList.remove("flash"); }, 1600);
  }
  // 2FA: scan → verify → recovery codes. Nothing is enabled until a code verifies;
  // the codes are shown once, in this dialog only.
  function twoFASetup() {
    api("/api/auth/2fa/begin", { method: "POST", body: {} }).then(function (b) {
      var steps = '<ol class="steps"><li class="on" data-st="1">' + esc(T("tf.s1")) + '</li><li data-st="2">' + esc(T("tf.s2")) + '</li><li data-st="3">' + esc(T("tf.s3")) + "</li></ol>";
      modal('<div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>" + esc(T("tf.title")) + "</h2></div>" + steps +
        '<div id="tf-body"><div class="tf-scan"><img id="tf-qr" alt="' + esc(T("tf.qr_alt")) + '" width="180" height="180"><div><p>' + esc(T("tf.scan")) + '</p><p class="muted">' + esc(T("tf.manual")) + '</p><code class="secret-code" id="tf-secret"></code></div></div>' +
        '<label class="field"><span>' + esc(T("tf.code")) + '</span><input id="tf-code" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" autocomplete="one-time-code" placeholder="123456"></label><p class="err" id="tf-err"></p>' +
        '<div class="actions"><button class="btn" id="tf-cancel">' + esc(T("btn.cancel")) + '</button><button class="btn primary" id="tf-verify">' + esc(T("tf.verify")) + "</button></div></div>",
        function (root, close) {
          $("#tf-qr").src = b.qr;
          $("#tf-secret").textContent = b.secret.replace(/(.{4})/g, "$1 ").trim();
          $("#tf-cancel").addEventListener("click", close);
          var code = $("#tf-code"), busy = false;
          code.focus();
          var verify = function () {
            if (busy) return; busy = true;
            $("#tf-err").textContent = "";
            api("/api/auth/2fa/confirm", { method: "POST", body: { code: code.value } }).then(function (r) {
              $$(".steps li", root).forEach(function (li) { li.classList.toggle("on", li.getAttribute("data-st") === "3"); li.classList.toggle("done", li.getAttribute("data-st") !== "3"); });
              var txt = r.recovery_codes.join("\n");
              $("#tf-body").innerHTML = '<p class="ok-text">' + esc(T("tf.enabled")) + '</p><p>' + esc(T("tf.rec_intro")) + '</p><pre class="rec-codes" id="tf-rec"></pre><p class="muted">' + esc(T("tf.rec_once")) + "</p>" +
                '<div class="actions"><button class="btn" id="tf-copy">' + esc(T("tf.copy")) + '</button><button class="btn" id="tf-dl">' + esc(T("tf.download")) + '</button><button class="btn primary" id="tf-done">' + esc(T("tf.done")) + "</button></div>";
              $("#tf-rec").textContent = txt;
              $("#tf-copy").addEventListener("click", function () { copyText(txt); });
              $("#tf-dl").addEventListener("click", function () {
                var a = document.createElement("a");
                a.href = URL.createObjectURL(new Blob(["tidebot recovery codes\n\n" + txt + "\n"], { type: "text/plain" }));
                a.download = "tidebot-recovery-codes.txt"; document.body.appendChild(a); a.click(); a.remove();
                setTimeout(function () { URL.revokeObjectURL(a.href); }, 1000);
              });
              $("#tf-done").addEventListener("click", function () { txt = ""; close(); refresh(); });
            }).catch(function (e) { $("#tf-err").textContent = T("tf.bad_code"); code.select(); }).then(function () { busy = false; });
          };
          code.addEventListener("input", function () { code.value = code.value.replace(/\D/g, "").slice(0, 6); if (code.value.length === 6) verify(); });
          $("#tf-verify").addEventListener("click", verify);
        });
    }).catch(fail);
  }
  function twoFADisable() {
    modal('<div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>" + esc(T("tf.disable")) + '</h2></div><p>' + esc(T("tf.disable_sub")) + '</p><label class="field"><span>' + esc(T("login.password")) + '</span><input type="password" id="tf-pw" autocomplete="current-password" maxlength="1024"></label><p class="err" id="tf-err"></p>' +
      '<div class="actions"><button class="btn" id="tf-cancel">' + esc(T("btn.cancel")) + '</button><button class="btn danger" id="tf-go">' + esc(T("tf.disable")) + "</button></div>", function (root, close) {
        $("#tf-pw").focus();
        $("#tf-cancel").addEventListener("click", close);
        $("#tf-go").addEventListener("click", function () {
          api("/api/auth/2fa/disable", { method: "POST", body: { password: $("#tf-pw").value } }).then(function () { close(); toast(T("tf.disabled")); refresh(); })
            .catch(function () { $("#tf-err").textContent = T("tf.bad_pw"); });
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
    if (!soft) { $("#modal-root").innerHTML = ""; closeDrawer(); }
    var y = window.scrollY, pg = page(), sy = pg ? pg.scrollTop : 0;
    if (!soft && location.hash !== lastHash && pg) {
      // Keep the old page on screen; only show a skeleton if loading is slow.
      if (lastHash != null) scrollPos[lastHash] = Math.max(y, sy);
      pendingRestore = scrollPos[location.hash] || 0;
      var seq = mountSeq, kind = /^#\/(buttons)?$/.test(location.hash || "#/") ? "cards" : "list";
      setTimeout(function () { if (mountSeq === seq && page()) { page().innerHTML = skeleton(kind); page().setAttribute("aria-busy", "true"); } }, 200);
    }
    lastHash = location.hash;
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
      case "tasks": p = parts[1] ? renderTaskDetail(parts[1], parts[2]) : renderTasks(); break;
      case "skills": p = parts[1] ? renderSkillDetail(parts[1], params) : renderSkills(); break;
      case "buttons": p = renderButtons(); break;
      case "plugins": p = renderPlugins(); break;
      case "runs": p = parts[1] ? renderRunDetail(parts[1]) : renderRuns(params); break;
      case "spend": p = renderSpend(); break;
      case "settings": p = renderSettings(parts[1], parts[2]); break;
      default: p = renderHome();
    }
    } finally { swr.on = false; }
    p = Promise.resolve(p).then(function () { if (soft) { window.scrollTo(0, y); if (page()) page().scrollTop = sy; } }, function (e) {
      if (!e.auth) { renderTopbar([]); page().innerHTML = errorState(e.message); $("#err-retry").addEventListener("click", function () { lastHash = null; route(); }); }
    });
    loadCC(false);
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
      state.currency = me.currency || "USD";
      $("#login").classList.add("hidden");
      $("#shell").classList.remove("hidden");
      dropBoot();
      renderShell();
      route();
    }).catch(function () { showLogin(); });
  }
  var storedLang = "auto";
  try { storedLang = localStorage.getItem("tb_lang") || "auto"; } catch (e) {}
  loadLang(storedLang).then(boot);
})();
