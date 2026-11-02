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
    return fetch("/static/i18n/" + lang + ".json" + assetV, { credentials: "same-origin" }).then(function (r) { return r.json(); }).then(function (d) {
      state.dict = d; state.lang = lang;
      document.documentElement.lang = lang;
      try { localStorage.setItem("tb_lang", state.langPref); } catch (e) {}
    }).catch(function () {
      if (lang !== "en") return fetch("/static/i18n/en.json" + assetV).then(function (r) { return r.json(); }).then(function (d) { state.dict = d; state.lang = "en"; }).catch(function () {});
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
    undo: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 5.5 5.5a5.5 5.5 0 0 1-5.5 5.5H11"/>',
    redo: '<path d="m15 14 5-5-5-5"/><path d="M20 9H9.5A5.5 5.5 0 0 0 4 14.5A5.5 5.5 0 0 0 9.5 20H13"/>',
    chev: '<path d="m9 18 6-6-6-6"/>',
    sidebar: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>',
    info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>',
    folder: '<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>',
    doc: '<path d="M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 20 8v12a2 2 0 0 1-2 2z"/><path d="M14 2v5a1 1 0 0 0 1 1h5"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>',
    sync: '<path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16"/><path d="M16 16h5v5"/>',
    pie: '<path d="M21 12c.552 0 1.005-.449.95-.998a10 10 0 0 0-8.953-8.951c-.55-.055-.998.398-.998.95v8a1 1 0 0 0 1 1z"/><path d="M21.21 15.89A10 10 0 1 1 8 2.83"/>',
    trash: '<path d="M10 11v6"/><path d="M14 11v6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M3 6h18"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>',
    sparkle: '<path d="M11.017 2.814a1 1 0 0 1 1.966 0l1.051 5.558a2 2 0 0 0 1.594 1.594l5.558 1.051a1 1 0 0 1 0 1.966l-5.558 1.051a2 2 0 0 0-1.594 1.594l-1.051 5.558a1 1 0 0 1-1.966 0l-1.051-5.558a2 2 0 0 0-1.594-1.594l-5.558-1.051a1 1 0 0 1 0-1.966l5.558-1.051a2 2 0 0 0 1.594-1.594z"/><path d="M20 2v4"/><path d="M22 4h-4"/><circle cx="4" cy="20" r="2"/>',
    play: '<path d="M5 5a2 2 0 0 1 3.008-1.728l11.997 6.998a2 2 0 0 1 .003 3.458l-12 7A2 2 0 0 1 5 19z"/>',
    skipFwd: '<path d="M5 5.5v13a1 1 0 0 0 1.55.83l9-6.5a1 1 0 0 0 0-1.66l-9-6.5A1 1 0 0 0 5 5.5z"/><path d="M19 5v14"/>',
    pause: '<rect x="14" y="3" width="5" height="18" rx="1"/><rect x="5" y="3" width="5" height="18" rx="1"/>',
    stop: '<rect x="5" y="5" width="14" height="14" rx="2"/>',
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
    monitor: '<rect width="20" height="14" x="2" y="3" rx="2"/><path d="M8 21h8"/><path d="M12 17v4"/>',
    phone: '<rect width="14" height="20" x="5" y="2" rx="2" ry="2"/><path d="M12 18h.01"/>',
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
    ext: '<path d="M14 5h5v5"/><path d="m19 5-8 8"/><path d="M18 14v5H5V6h5"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M5.6 18.4 7 17M17 7l1.4-1.4"/>',
    moon: '<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>',
    palette: '<path d="M12 3.5a8.5 8.5 0 0 0 0 17c1.2 0 1.8-.8 1.8-1.7 0-1.3-1-1.6-1-2.8 0-1 .8-1.7 1.8-1.7h2.2a3.7 3.7 0 0 0 3.7-3.7c0-4-3.8-7.1-8.5-7.1z"/><circle cx="8" cy="11" r="1"/><circle cx="11" cy="7.5" r="1"/><circle cx="15.5" cy="8" r="1"/>',
    link: '<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>',
    download: '<path d="M12 15V3"/><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/>',
    chevUp: '<path d="m18 15-6-6-6 6"/>',
    chevLeft: '<path d="m15 18-6-6 6-6"/>',
    chevRight: '<path d="m9 18 6-6-6-6"/>',
    chevDown: '<path d="m6 9 6 6 6-6"/>',
    maximize: '<path d="M15 3h6v6"/><path d="m21 3-7 7"/><path d="m3 21 7-7"/><path d="M9 21H3v-6"/>',
    copy: '<rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>',
    star: '<path d="M11.525 2.295a.53.53 0 0 1 .95 0l2.31 4.679a2.123 2.123 0 0 0 1.595 1.16l5.166.756a.53.53 0 0 1 .294.904l-3.736 3.638a2.123 2.123 0 0 0-.611 1.878l.882 5.14a.53.53 0 0 1-.771.56l-4.618-2.428a2.122 2.122 0 0 0-1.973 0L6.396 21.01a.53.53 0 0 1-.77-.56l.881-5.139a2.122 2.122 0 0 0-.611-1.879L2.16 9.795a.53.53 0 0 1 .294-.906l5.165-.755a2.122 2.122 0 0 0 1.597-1.16z"/>',
    flag: '<path d="M4 22V4a1 1 0 0 1 .4-.8A6 6 0 0 1 8 2c3 0 5 2 7.333 2q2 0 3.067-.8A1 1 0 0 1 20 4v10a1 1 0 0 1-.4.8A6 6 0 0 1 16 16c-3 0-5-2-8-2a6 6 0 0 0-4 1.528"/>',
    cpu: '<path d="M12 20v2"/><path d="M12 2v2"/><path d="M17 20v2"/><path d="M17 2v2"/><path d="M2 12h2"/><path d="M2 17h2"/><path d="M2 7h2"/><path d="M20 12h2"/><path d="M20 17h2"/><path d="M20 7h2"/><path d="M7 20v2"/><path d="M7 2v2"/><rect x="4" y="4" width="16" height="16" rx="2"/><rect x="8" y="8" width="8" height="8" rx="1"/>',
    server: '<rect width="20" height="8" x="2" y="2" rx="2" ry="2"/><rect width="20" height="8" x="2" y="14" rx="2" ry="2"/><line x1="6" x2="6.01" y1="6" y2="6"/><line x1="6" x2="6.01" y1="18" y2="18"/>',
    list: '<path d="M3 5h.01"/><path d="M3 12h.01"/><path d="M3 19h.01"/><path d="M8 5h13"/><path d="M8 12h13"/><path d="M8 19h13"/>',
    edit: '<path d="M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z"/><path d="m15 5 4 4"/>',
    eye: '<path d="M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0"/><circle cx="12" cy="12" r="3"/>',
    battery: '<rect width="16" height="10" x="2" y="7" rx="2" ry="2"/><path d="M22 11v2"/><path d="M6 11v2"/><path d="M10 11v2"/>',
    lock: '<rect width="18" height="11" x="3" y="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
    offline: '<path d="M12 20h.01"/><path d="M8.5 16.429a5 5 0 0 1 7 0"/><path d="M5 12.859a10 10 0 0 1 5.17-2.69"/><path d="M19 12.859a10 10 0 0 0-2.007-1.523"/><path d="M2 8.82a15 15 0 0 1 4.177-2.643"/><path d="M22 8.82a15 15 0 0 0-11.288-3.764"/><path d="m2 2 20 20"/>',
    tag: '<path d="M12.6 2.6A2 2 0 0 0 11.2 2H4a2 2 0 0 0-2 2v7.2a2 2 0 0 0 .6 1.4l8.7 8.7a2.4 2.4 0 0 0 3.4 0l6.6-6.6a2.4 2.4 0 0 0 0-3.4z"/><circle cx="7.5" cy="7.5" r="1"/>'
  };
  function ic(name, cls) { if (name === "workflows") name = "branch"; return '<svg class="i ' + (cls || "") + '" viewBox="0 0 24 24" aria-hidden="true">' + (P[name] || P.circle) + "</svg>"; }
  var BUTTON_ICONS = ["doc", "sync", "pie", "trash", "bolt", "terminal", "calendar", "pulse", "shield", "globe", "mail", "database", "code", "rocket", "bug", "cloud", "camera", "book", "box", "git", "chat", "image", "link", "download", "star", "cpu", "server", "edit"];
  function btnIcon(b) { return P[b.icon] ? b.icon : "bolt"; }

  var markN = 0;
  // The curled-wave brand mark shares its geometry with branding/mark.svg. CSS gives
  // the crest and foreground a gentle swell, faster while tasks run, paused when hidden
  // and disabled for prefers-reduced-motion. No animation timers are needed.
  // The favicon / app icon (branding/mark.svg) stays static.
  function wavePath(layer) {
    return ["M-4 45C10 33 21 35 31 43S55 49 68 39V68H-4Z", "M4 40C12 38 14 23 25 20C33 18 41 22 43 28C37 24 31 26 31 32C31 40 44 44 59 35C57 46 48 51 37 49C23 47 23 35 13 37C9 38 6 40 4 43Z", "M-3 55C9 45 20 46 31 51S53 58 67 47V68H-3Z"][layer];
  }
  function waveMark() {
    var g = "tbg" + (markN++), c = "tbc" + markN, w = "tbw" + markN;
    return '<svg class="tide" viewBox="0 0 64 64" aria-hidden="true"><defs><linearGradient id="' + g + '" x1="0" y1="0" x2=".3" y2="1"><stop class="mk-a" offset="0"/><stop class="mk-b" offset="1"/></linearGradient><linearGradient id="' + w + '" x1="0" y1="0" x2=".8" y2="1"><stop offset="0" stop-color="#fff"/><stop offset="1" stop-color="#b9efff"/></linearGradient><clipPath id="' + c + '"><rect width="64" height="64" rx="15"/></clipPath></defs>' +
      '<g clip-path="url(#' + c + ')"><rect width="64" height="64" fill="url(#' + g + ')"/>' +
      '<path d="' + wavePath(0) + '" fill="#0756bf" opacity=".55"/>' +
      '<path class="tw tw1" d="' + wavePath(1) + '" fill="url(#' + w + ')"/>' +
      '<path class="tw tw2" d="' + wavePath(2) + '" fill="#169aee" opacity=".85"/>' +
      '<rect x=".5" y=".5" width="63" height="63" rx="14.5" fill="none" stroke="#fff" stroke-opacity=".2"/></g></svg>';
  }
  document.addEventListener("visibilitychange", function () { document.documentElement.classList.toggle("tb-hidden", document.hidden); });
  function waveArt() {
    return '<svg class="wave" viewBox="0 0 300 170" aria-hidden="true">' +
      '<path d="M0 170V120c40-10 70-40 110-38 30 2 40 26 66 22 22-3 26-36 4-46-16-7-34 4-30 18-14-26 10-56 46-50 44 7 66 54 104 60v84z" class="wa1"/>' +
      '<path d="M0 170v-28c50-6 90-34 140-30 40 3 60 30 100 26 24-2 44-14 60-24v56z" class="wa2"/>' +
      '<path d="M150 98c-6-18 14-38 40-32" class="wa3" stroke-width="2" fill="none"/>' +
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

