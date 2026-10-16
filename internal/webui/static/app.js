/* tidebot WebUI — vanilla JS, no framework. All dynamic text goes through esc(). */
(function () {
  "use strict";
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };
  var state = { csrf: "", tz: "UTC", version: "", es: null, idleTimer: null, notifSeen: localStorage.getItem("tb_notif_seen") || "" };
  var IDLE_MS = 30 * 60 * 1000;

  function esc(v) {
    return String(v == null ? "" : v).replace(/[&<>"'`]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;", "`": "&#96;" }[c];
    });
  }
  function enc(v) { return encodeURIComponent(String(v == null ? "" : v)); }

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
    return new Intl.DateTimeFormat("en-US", Object.assign({ timeZone: state.tz }, opts)).format(d);
  }
  function dayKey(d) { return new Intl.DateTimeFormat("en-CA", { timeZone: state.tz, year: "numeric", month: "2-digit", day: "2-digit" }).format(d); }
  function relDay(iso) {
    var d = new Date(iso), now = Date.now();
    var k = dayKey(d);
    if (k === dayKey(new Date(now))) return "Today";
    if (k === dayKey(new Date(now - 864e5))) return "Yesterday";
    if (k === dayKey(new Date(now + 864e5))) return "Tomorrow";
    return fmtTime(iso, { month: "short", day: "numeric" });
  }
  function clock(iso) { return fmtTime(iso, { hour: "numeric", minute: "2-digit" }); }
  function when(iso) { return iso ? relDay(iso) + ", " + clock(iso) : "—"; }
  function ago(iso) {
    if (!iso) return "—";
    var s = (Date.now() - new Date(iso).getTime()) / 1000;
    if (s < 60) return "just now";
    if (s < 3600) return Math.floor(s / 60) + "m ago";
    if (s < 86400) return Math.floor(s / 3600) + "h ago";
    return Math.floor(s / 86400) + "d ago";
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
    if (s < 60) return s + "s";
    return Math.floor(s / 60) + "m " + String(s % 60).padStart(2, "0") + "s";
  }
  function usd(v, d) { return "$" + Number(v || 0).toFixed(d == null ? 2 : d); }
  function num(v) { return Number(v || 0).toLocaleString("en-US"); }
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
        if (res.status === 401 && path !== "/api/login") { showLogin(); throw new Error("Please sign in"); }
        if (!res.ok) throw new Error((data && data.error) || res.statusText);
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
    var map = { succeeded: ["ok", "Success"], running: ["run", "Running"], queued: ["info", "Queued"], failed: ["bad", "Failed"], timeout: ["bad", "Timeout"], canceled: ["warn", "Canceled"], skipped: ["", "Skipped"] };
    var m = map[st] || ["", st || "—"];
    return '<span class="pill ' + m[0] + '">' + esc(m[1]) + "</span>";
  }
  function taskPill(st) {
    var m = { active: ["ok", "Active"], paused: ["warn", "Paused"], archived: ["", "Archived"] }[st] || ["", st];
    return '<span class="pill ' + m[0] + '">' + esc(m[1]) + "</span>";
  }
  function triggerText(t) {
    return { cron: "Triggered by schedule", manual: "Triggered manually", quick: "Quick run", button: "Triggered by button" }[t] || ("Trigger: " + t);
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
  function runTitle(r) { return (r.task_name || "Quick run") + " #" + shortId(r.id); }
  function cronHuman(c) {
    if (!c) return "Manual only";
    var p = c.split(/\s+/);
    if (p.length === 5 && /^\d+$/.test(p[0]) && /^\d+$/.test(p[1])) {
      var h = +p[1], t = (h % 12 || 12) + ":" + String(p[0]).padStart(2, "0") + (h < 12 ? " AM" : " PM");
      if (p[2] === "*" && p[3] === "*" && p[4] === "*") return "Daily at " + t;
      if (p[2] === "*" && p[3] === "*" && p[4] === "1-5") return "Weekdays at " + t;
      if (p[2] === "*" && p[3] === "*" && /^\d$/.test(p[4])) return ["Sundays", "Mondays", "Tuesdays", "Wednesdays", "Thursdays", "Fridays", "Saturdays"][+p[4]] + " at " + t;
    }
    return c;
  }
  function emptyState(icon, title, sub) { return '<div class="empty">' + ic(icon) + "<strong>" + esc(title) + "</strong>" + esc(sub || "") + "</div>"; }
  // ---------- shell ----------
  var NAV = [["home", "Home", "#/"], ["tasks", "Tasks", "#/tasks"], ["skills", "Skills", "#/skills"], ["buttons", "Buttons", "#/buttons"], ["plugins", "Plugins", "#/plugins"], ["runs", "Runs", "#/runs"], ["spend", "Spend", "#/spend"], ["settings", "Settings", "#/settings"]];

  function renderShell() {
    $("#brand-mark").innerHTML = waveMark();
    $("#nav").innerHTML = NAV.map(function (n) { return '<a href="' + n[2] + '" data-nav="' + n[0] + '">' + ic(n[0]) + "<span>" + n[1] + "</span></a>"; }).join("");
  }
  function setNav(name) { $$("#nav a").forEach(function (a) { a.classList.toggle("active", a.getAttribute("data-nav") === name); }); }

  function renderTopbar(crumbs) {
    var c = crumbs && crumbs.length ? '<div class="crumbs">' + crumbs.map(function (x, i) {
      return (i ? '<span class="sep">' + ic("chev", "sm") + "</span>" : "") + (x[1] ? '<a href="' + esc(x[1]) + '">' + esc(x[0]) + "</a>" : "<span>" + esc(x[0]) + "</span>");
    }).join("") + "</div>" : "";
    $("#topbar").innerHTML = c + '<div class="spacer"></div>' +
      '<div class="search">' + ic("search") + '<input id="q" type="search" placeholder="Search anything…" autocomplete="off" aria-label="Search"><kbd>K</kbd><div id="qres" class="search-results hidden"></div></div>' +
      '<button class="icon-btn" id="btn-refresh" title="Refresh" aria-label="Refresh">' + ic("refresh") + "</button>" +
      '<div class="rel"><button class="icon-btn" id="btn-bell" title="Notifications" aria-label="Notifications">' + ic("bell") + '<span class="badge-dot hidden" id="bell-dot"></span></button><div id="notif" class="popover hidden"></div></div>';
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
            return '<a href="' + href + '"><span class="kind">' + esc(h.kind) + "</span><span>" + esc(h.kind === "run" ? "Run #" + shortId(h.id) : h.name) + '</span><span class="sub">' + esc(h.sub) + "</span></a>";
          }).join("") : '<div class="none">No matches</div>';
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
    $("#btn-bell").addEventListener("click", function (e) {
      e.stopPropagation();
      var pop = $("#notif");
      if (!pop.classList.contains("hidden")) { pop.classList.add("hidden"); return; }
      api("/api/notifications").then(function (list) {
        pop.innerHTML = "<h4>Notifications</h4>" + (list.length ? list.slice(0, 8).map(function (n) {
          return '<div class="item"><strong>' + esc(cap(n.event.replace(/_/g, " "))) + "</strong><div>" + esc(n.body) + '</div><small class="muted">' + esc(when(n.created_at)) + "</small></div>";
        }).join("") : '<div class="item muted">You\'re all caught up.</div>');
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
    $("#sys-status").innerHTML = '<span class="dot' + (warn ? " warn" : "") + '"></span><div><strong class="' + (warn ? "warn" : "") + '">' + (warn ? "Daily budget reached" : "All systems go") + "</strong><small>" +
      (o && o.active_runs ? esc(o.active_runs) + " run" + (o.active_runs > 1 ? "s" : "") + " in progress" : "tidebot is running smoothly") + "</small></div>";
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
    l.innerHTML = '<div class="card login-card"><div class="brand"><span class="brand-mark">' + waveMark() + '</span><span class="brand-text"><strong>tidebot</strong><small>AI Agent Automation</small></span></div>' +
      '<form id="login-form" autocomplete="on"><label class="field"><span>Password</span><input type="password" id="lp" required minlength="8" autocomplete="current-password" autofocus></label>' +
      '<label class="field' + (totp ? "" : " hidden") + '" id="totp-field"><span>Authenticator code</span><input id="lt" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" autocomplete="one-time-code" placeholder="123456"></label>' +
      '<p class="err" id="lerr"></p><button class="btn primary" type="submit">Sign in</button></form>' +
      '<p class="login-foot">Single-user console · set or reset the password with <code>agentctl auth set-password</code></p></div>';
    $("#login-form").addEventListener("submit", function (e) {
      e.preventDefault();
      $("#lerr").textContent = "";
      api("/api/login", { method: "POST", body: { password: $("#lp").value, totp: $("#lt").value } }).then(function () { boot(); })
        .catch(function (err) {
          if (/credentials/.test(err.message)) $("#totp-field").classList.remove("hidden");
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
    if (!runs.length) return emptyState("runs", "No runs yet", "Runs appear here as soon as a task or button fires.");
    return '<table class="table"><thead><tr><th>Run</th><th>Agent / Task</th><th>Status</th><th>Started</th><th>Duration</th><th class="right">Cost</th><th></th></tr></thead><tbody>' +
      runs.map(function (r) {
        var running = r.status === "running";
        return '<tr class="click" data-href="#/runs/' + enc(r.id) + '"><td><div class="cell-main"><span class="ico">' + ic(runIcon(r), "sm") + "</span><div><strong>" + esc(runTitle(r)) + "</strong><small>" + esc(triggerText(r.trigger)) + "</small></div></div></td>" +
          "<td><strong>" + esc(r.task_name || "Ad-hoc") + "</strong><small>" + esc(r.provider || "—") + (r.version_no ? " · v" + esc(r.version_no) : "") + "</small></td>" +
          "<td>" + statusPill(r.status) + '</td><td class="nowrap">' + esc(when(r.started_at || r.created_at)) + '</td><td class="nowrap">' + esc(running ? dur(Date.now() - new Date(r.started_at).getTime()) : dur(r.duration_ms)) +
          '</td><td class="right"><strong>' + esc(usd(r.cost_usd)) + '</strong></td><td class="right"><a class="icon-btn" href="#/runs/' + enc(r.id) + '" aria-label="Open run">' + ic("more", "sm") + "</a></td></tr>";
      }).join("") + "</tbody></table>";
  }
  // ---------- HOME ----------
  function runButton(id, el) {
    if (el) el.classList.add("busy");
    return api("/api/buttons/" + enc(id) + "/run", { method: "POST", body: {} })
      .then(function (r) { toast("Started " + r.started); setTimeout(route, 700); })
      .catch(fail)
      .then(function () { if (el) el.classList.remove("busy"); });
  }
  function renderHome() {
    return api("/api/overview").then(function (o) {
      renderStatus(o);
      var qa = (o.buttons || []).slice(0, 4).map(function (b) {
        return '<button class="card qa" data-btn="' + esc(b.id) + '"><span class="qa-ico">' + ic(btnIcon(b), "lg") + "</span><strong>" + esc(b.label) + "</strong><small>" + esc(b.description || "") + "</small></button>";
      });
      while (qa.length < 4) qa.push('<a class="card qa" href="#/buttons"><span class="qa-ico">' + ic("plus", "lg") + "</span><strong>Add button</strong><small>Pin a task or prompt</small></a>");
      qa.push('<button class="card qa tint" id="qa-new"><span class="qa-ico">' + ic("sparkle", "lg") + "</span><strong>New Quick Run</strong><small>Run an agent on demand " + ic("arrow", "sm") + "</small></button>");

      var sched = o.schedule || [];
      var schedHtml = sched.length ? '<ul class="timeline">' + sched.map(function (s) {
        return '<li><span class="tl-dot"></span><span class="tl-time">' + esc(clock(s.at)) + '</span><div class="tl-name"><strong>' + esc(s.task_name) + "</strong><small>" + esc(cronHuman(s.cron)) + '</small></div><span class="tag">' + esc(relDay(s.at)) + "</span></li>";
      }).join("") + "</ul>" : emptyState("calendar", "Nothing scheduled", "Give a task a cron schedule to see it here.");

      var sp = o.spend || {}, max = sp.max_usd || 0, pct = max ? Math.min(100, sp.used_usd / max * 100) : 0;
      var resetMs = new Date(sp.resets_at).getTime() - Date.now();
      var resetTxt = resetMs > 0 ? Math.floor(resetMs / 3600e3) + "h " + Math.floor(resetMs % 3600e3 / 60e3) + "m" : "soon";

      mount(
        '<div class="grid g5">' + qa.join("") + "</div>" +
        '<div class="grid g2">' +
        '<section class="card fill"><div class="card-head"><span class="ico">' + ic("calendar") + "</span><h2>Upcoming Schedule</h2>" + '<a class="link" href="#/tasks">View all tasks ' + ic("arrow", "sm") + "</a></div>" +
        '<div class="grow">' + schedHtml + "</div>" +
        '<div class="card-foot">' + ic("info", "sm") + "<span>All times in " + esc(tzLabel()) + '</span><a class="link" href="#/tasks">Manage schedule ' + ic("arrow", "sm") + "</a></div></section>" +
        '<section class="card spend-card">' + waveArt() + '<div class="card-head"><span class="ico">' + ic("receipt") + "</span><h2>Today's Spend</h2>" + '<a class="link" href="#/spend">View spend ' + ic("arrow", "sm") + "</a></div>" +
        '<div class="spend-big">' + esc(usd(sp.used_usd)) + " <span>/ " + (max ? esc(usd(max)) : "no cap") + "</span></div>" +
        '<div class="meta">' + (max ? esc(Math.round(pct)) + "% of daily budget" : "No global daily budget set") + " · " + esc(num(sp.tokens)) + " tokens</div>" +
        '<div class="progress' + (pct >= 100 ? " bad" : pct >= 80 ? " warn" : "") + '"><span data-w="' + pct + '"></span></div>' +
        '<div class="meta">Resets in ' + esc(resetTxt) + " (UTC midnight)</div></section>" +
        "</div>" +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("pulse") + "</span><h2>Recent Runs</h2>" + '<a class="link" href="#/runs">View all runs ' + ic("arrow", "sm") + "</a></div>" + runsTable(o.runs || []) + "</section>",
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
      modal('<div class="card-head"><span class="ico">' + ic("sparkle") + "</span><h2>New Quick Run</h2></div>" +
        '<form id="qr"><label class="field"><span>Prompt</span><textarea name="prompt" required maxlength="8000" placeholder="What should the agent do?"></textarea></label>' +
        '<label class="field"><span>Provider</span><select name="provider">' + st.providers.map(function (p) { return '<option value="' + esc(p.name) + '"' + (p.name === st.default_provider ? " selected" : "") + ">" + esc(p.name) + (p.key_set ? "" : " (no key)") + "</option>"; }).join("") + "</select></label>" +
        '<p class="hint">Counts against today\'s budget. Output streams live on the run page.</p>' +
        '<div class="actions"><button type="button" class="btn ghost" data-close>Cancel</button><button class="btn primary" type="submit">' + ic("play", "sm") + "Run</button></div></form>",
        function (root, close) {
          $("#qr", root).addEventListener("submit", function (e) {
            e.preventDefault();
            var f = e.target;
            api("/api/runs/quick", { method: "POST", body: { prompt: f.prompt.value, provider: f.provider.value } }).then(function () {
              close(); toast("Quick run started");
              setTimeout(function () { api("/api/runs?limit=1").then(function (r) { location.hash = r[0] ? "#/runs/" + r[0].id : "#/runs"; }); }, 500);
            }).catch(fail);
          });
        });
    }).catch(fail);
  }

  // ---------- TASKS ----------
  function renderTasks() {
    return api("/api/tasks").then(function (tasks) {
      var rows = tasks.length ? '<table class="table"><thead><tr><th>Task</th><th>Schedule</th><th>Status</th><th>Version</th><th>Last run</th><th>Runs today</th><th></th></tr></thead><tbody>' +
        tasks.map(function (t) {
          return '<tr class="click" data-href="#/tasks/' + enc(t.id) + '"><td><div class="cell-main"><span class="ico">' + ic(runIcon({ task_name: t.name }), "sm") + "</span><div><strong>" + esc(t.name) + "</strong><small>" + esc(t.goal || t.prompt) + "</small></div></div></td>" +
            '<td class="nowrap">' + esc(cronHuman(t.cron)) + "</td><td>" + taskPill(t.status) + '</td><td><span class="tag">v' + esc(t.version_no) + "</span></td><td>" + (t.last_status ? statusPill(t.last_status) : '<span class="muted">Never</span>') + "</td><td>" + esc(t.runs_today) + '</td><td class="right"><span class="muted">' + ic("chev", "sm") + "</span></td></tr>";
        }).join("") + "</tbody></table>" : emptyState("tasks", "No tasks yet", "Create one with agentctl tasks create.");
      mount('<div class="page-head"><div><h1>Tasks</h1><p>' + esc(tasks.length) + " task" + (tasks.length === 1 ? "" : "s") + " · versioned specs with natural-language amendments</p></div></div>" +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("tasks") + "</span><h2>All tasks</h2></div>" + rows + "</section>", "tasks", [["Tasks"]]);
      wireRows();
    });
  }
  // ---------- TASK DETAIL ----------
  function versionTree(vers, current) {
    if (!vers.length) return emptyState("branch", "No versions", "");
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
    var s = '<svg class="vtree" viewBox="0 0 ' + W + " " + H + '" role="img" aria-label="Version tree">';
    vers.forEach(function (v) {
      if (v.parent_id && pos[v.parent_id]) {
        var a = pos[v.parent_id], b = pos[v.id];
        s += '<path class="edge" d="M' + a.x + " " + (a.y + NH) + " C" + a.x + " " + (a.y + NH + VG / 2) + " " + b.x + " " + (b.y - VG / 2) + " " + b.x + " " + b.y + '"/>';
      }
    });
    vers.forEach(function (v) {
      var p = pos[v.id], cur = v.id === current;
      s += '<g class="node' + (cur ? " cur" : "") + '" data-ver="' + esc(v.id) + '" data-no="' + esc(v.no) + '"><title>' + esc((v.message || v.source) + (cur ? " (current)" : " — click to roll back")) + "</title>" +
        '<rect x="' + (p.x - NW / 2) + '" y="' + p.y + '" width="' + NW + '" height="' + NH + '" rx="8"/>' +
        '<text x="' + p.x + '" y="' + (p.y + 19) + '" text-anchor="middle" font-size="13" font-weight="600">v' + esc(v.no) + "</text>" +
        '<text class="sub" x="' + p.x + '" y="' + (p.y + 34) + '" text-anchor="middle">' + esc(ago(v.created_at)) + "</text></g>";
      if (cur) {
        var right = p.x + NW / 2 + 56 <= W, tx = right ? p.x + NW / 2 + 8 : p.x - NW / 2 - 8;
        s += '<text class="ptr" x="' + tx + '" y="' + (p.y + 26) + '" text-anchor="' + (right ? "start" : "end") + '">current</text>';
      }
    });
    return s + "</svg>";
  }

  var REQ_ST = { pending: ["info", "Pending"], applied: ["ok", "Applied"], scheduled: ["warn", "Next run"], consumed: ["ok", "Used"], rejected: ["", "Rejected"], needs_decision: ["bad", "Conflict"] };
  function threadHtml(items) {
    if (!items.length) return emptyState("sparkle", "No amendments yet", "Describe a change below — tidebot turns it into structured requirements you confirm.");
    var groups = [], idx = {};
    items.forEach(function (it) {
      var k = it.message_id || it.id;
      if (!(k in idx)) { idx[k] = groups.length; groups.push({ utter: it.utterance, at: it.created_at, items: [] }); }
      groups[idx[k]].items.push(it);
    });
    return groups.map(function (g) {
      var all = function (f) { return g.items.every(f); };
      var scope = all(function (i) { return i.scope === "next_run"; }) ? "next" : all(function (i) { return i.scope === "permanent"; }) ? "perm" : "mixed";
      var pending = g.items.filter(function (i) { return i.status === "pending"; });
      var reply = pending.length ? (scope === "next" ? "Got it. Once you confirm, this applies to the next run only." : "I've turned your request into requirements. Please confirm.")
        : g.items.some(function (i) { return i.status === "needs_decision"; }) ? "This conflicts with an existing requirement — choose how to resolve it."
        : all(function (i) { return i.status === "rejected"; }) ? "Discarded — nothing changed."
        : scope === "next" ? "Scheduled for the next run." : "Done. The task spec has a new version.";
      var ids = esc(pending.map(function (i) { return i.id; }).join(","));
      return '<div class="msg"><span class="avatar">ME</span><div><div class="msg-meta"><strong>You</strong><small>' + esc(ago(g.at)) + '</small></div><div class="msg-body">' + esc(g.utter || "(amendment)") + "</div></div></div>" +
        '<div class="msg"><span class="avatar bot">' + waveMark() + '</span><div><div class="msg-meta"><strong>tidebot</strong><small>' + esc(ago(g.at)) + '</small></div><div class="msg-body">' + esc(reply) + "</div>" +
        '<div class="req-card"><div class="req-card-head">' + (scope === "next" ? "Additional requirements" : "Updated requirements") +
        (scope === "next" ? '<span class="tag next">next run only</span>' : scope === "perm" ? '<span class="tag perm">permanent</span>' : '<span class="tag">mixed</span>') + "</div>" +
        g.items.map(function (i) {
          var sl = REQ_ST[i.status] || ["", i.status];
          var ck = i.status === "rejected" ? ic("x", "sm") : (i.status === "pending" || i.status === "needs_decision") ? ic("circle", "sm") : ic("checkc", "sm");
          return '<div class="req-item"><span class="ck ' + (i.status === "rejected" ? "no" : i.status === "pending" ? "pending" : "") + '">' + ck + '</span><span class="op">' + esc(i.op) + '</span><span class="txt">' + esc(i.content || i.target_spec_item) +
            (scope === "mixed" ? ' <span class="tag ' + (i.scope === "next_run" ? "next" : "perm") + '">' + (i.scope === "next_run" ? "next run" : "permanent") + "</span>" : "") +
            '</span><span class="st"><span class="pill ' + sl[0] + '">' + esc(sl[1]) + "</span></span></div>" +
            (i.status === "needs_decision" ? '<div class="req-actions"><button class="btn sm" data-resolve="' + esc(i.id) + '" data-res="replace">Replace existing</button><button class="btn sm" data-resolve="' + esc(i.id) + '" data-res="coexist">Keep both</button></div>' : "");
        }).join("") +
        (pending.length ? '<div class="req-actions"><button class="btn ok" data-confirm="' + ids + '">' + ic("check", "sm") + 'Confirm</button><button class="btn danger" data-reject="' + ids + '">' + ic("x", "sm") + "Reject</button></div>" : "") +
        "</div></div></div>";
    }).join("");
  }

  function taskTab(tab, t, items, vers) {
    if (tab === "thread") {
      return '<div class="thread scroll" id="thread">' + threadHtml(items) + "</div>" +
        '<form class="composer" id="composer"><input name="u" placeholder="Describe changes or ask tidebot anything…" autocomplete="off" maxlength="2000" aria-label="Amendment"><button class="btn primary sm" type="submit" aria-label="Send">' + ic("send", "sm") + "</button></form>";
    }
    if (tab === "requirements") {
      return '<div class="thread"><dl class="kv"><dt>Goal</dt><dd>' + esc(t.goal || "—") + "</dd><dt>Prompt</dt><dd>" + esc(t.prompt) + "</dd><dt>Requirements</dt><dd>" +
        (t.constraints.length ? '<ul class="list-plain">' + t.constraints.map(function (c) { return "<li>" + esc(c) + "</li>"; }).join("") + "</ul>" : '<span class="muted">None yet</span>') +
        "</dd><dt>Skills</dt><dd>" + (t.skills.length ? t.skills.map(esc).join(", ") : '<span class="muted">None</span>') + "</dd></dl></div>";
    }
    if (tab === "settings") {
      return '<div class="thread"><dl class="kv"><dt>Schedule</dt><dd>' + esc(cronHuman(t.cron)) + (t.cron ? " <code>" + esc(t.cron) + "</code>" : "") + "</dd><dt>Provider</dt><dd>" + esc(t.provider || "default") +
        "</dd><dt>Workspace</dt><dd><code>" + esc(t.workspace || "—") + "</code></dd><dt>Status</dt><dd>" + taskPill(t.status) + "</dd><dt>Task ID</dt><dd><code>" + esc(t.id) + "</code></dd></dl></div>";
    }
    return '<div class="thread"><table class="table"><thead><tr><th>Version</th><th>Source</th><th>Note</th><th>Created</th><th></th></tr></thead><tbody>' +
      vers.versions.slice().reverse().map(function (v) {
        var cur = v.id === vers.current;
        return "<tr><td><strong>v" + esc(v.no) + "</strong>" + (cur ? ' <span class="tag perm">current</span>' : "") + "</td><td>" + esc(v.source) + "</td><td>" + esc(v.message || "—") + '</td><td class="nowrap">' + esc(when(v.created_at)) +
          '</td><td class="right">' + (cur ? "" : '<button class="btn sm" data-rollback="' + esc(v.id) + '" data-no="' + esc(v.no) + '">' + ic("rollback", "sm") + "Roll back</button>") + "</td></tr>";
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
      }).join("") + "</ul>" : emptyState("runs", "No runs yet", "");
      var tabs = [["thread", "Thread"], ["requirements", "Requirements"], ["settings", "Settings"], ["history", "History"]];
      mount('<div class="grid g-detail">' +
        '<section class="card"><div class="task-head"><div class="grow-col"><h1>' + esc(t.name) + "</h1><p>" + esc(t.goal || t.prompt) + "</p>" +
        '<div class="chips"><span class="chip">' + ic("user", "sm") + "Owned by you</span><span class=\"chip\">" + ic("clock", "sm") + esc(cronHuman(t.cron)) + "</span>" +
        '<span class="chip">' + ic("calendar", "sm") + "Created " + esc(fmtTime(t.created_at, { month: "short", day: "numeric", year: "numeric" })) + " · Updated " + esc(ago(t.updated_at)) + "</span>" + taskPill(t.status) + "</div></div>" +
        '<div class="row-actions">' + (t.status === "active" ? '<button class="btn" id="t-toggle" data-act="pause">' + ic("pause", "sm") + "Pause</button>" : '<button class="btn" id="t-toggle" data-act="resume">' + ic("play", "sm") + "Resume</button>") +
        '<button class="btn primary" id="t-run">' + ic("play", "sm") + "Run now</button></div></div>" +
        '<nav class="tabs">' + tabs.map(function (x) { return '<button data-tab="' + x[0] + '" class="' + (x[0] === tab ? "active" : "") + '">' + x[1] + (x[0] === "thread" && items.some(function (i) { return i.status === "pending"; }) ? ' <span class="pill info plain">' + items.filter(function (i) { return i.status === "pending"; }).length + "</span>" : "") + "</button>"; }).join("") + "</nav>" +
        taskTab(tab, t, items, vers) + "</section>" +
        '<div class="stack"><section class="card"><div class="card-head"><span class="ico">' + ic("branch") + '</span><h3>Version tree</h3><span class="link muted">v' + esc(t.version_no) + " current</span></div>" + versionTree(vers.versions, vers.current) + '<p class="hint mt">Click a version to roll back — history is never lost.</p></section>' +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("runs") + '</span><h3>Recent runs</h3><a class="link" href="#/runs?task=' + enc(t.id) + '">View all</a></div>' + mini + "</section></div></div>",
        "tasks", [["Tasks", "#/tasks"], [t.name]]);
      var reload = function () { renderTaskDetail(id, tab); };
      wireRows();
      $$("[data-tab]").forEach(function (b) { b.addEventListener("click", function () { renderTaskDetail(id, b.getAttribute("data-tab")); }); });
      $("#t-run").addEventListener("click", function () { api(base + "/run", { method: "POST", body: {} }).then(function () { toast("Run started"); setTimeout(reload, 800); }).catch(fail); });
      var tg = $("#t-toggle");
      tg.addEventListener("click", function () { api(base + "/" + tg.getAttribute("data-act"), { method: "POST", body: {} }).then(reload).catch(fail); });
      var each = function (ids, fn) { return ids.split(",").reduce(function (p, x) { return p.then(function () { return fn(x); }); }, Promise.resolve()); };
      $$("[data-confirm]").forEach(function (b) { b.addEventListener("click", function () { b.disabled = true; each(b.getAttribute("data-confirm"), function (x) { return api("/api/amendments/" + enc(x) + "/confirm", { method: "POST", body: {} }); }).then(function () { toast("Requirements confirmed"); reload(); }).catch(fail); }); });
      $$("[data-reject]").forEach(function (b) { b.addEventListener("click", function () { b.disabled = true; each(b.getAttribute("data-reject"), function (x) { return api("/api/amendments/" + enc(x) + "/reject", { method: "POST", body: {} }); }).then(function () { toast("Rejected"); reload(); }).catch(fail); }); });
      $$("[data-resolve]").forEach(function (b) { b.addEventListener("click", function () { api("/api/amendments/" + enc(b.getAttribute("data-resolve")) + "/resolve", { method: "POST", body: { resolution: b.getAttribute("data-res") } }).then(reload).catch(fail); }); });
      var rollback = function (vid, no) {
        if (vid === vers.current) return;
        if (!confirm("Roll back to v" + no + "? This moves the current pointer; history is kept.")) return;
        api(base + "/rollback", { method: "POST", body: { version_id: vid } }).then(function () { toast("Rolled back to v" + no); reload(); }).catch(fail);
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
      var rows = skills.length ? '<table class="table"><thead><tr><th>Skill</th><th>Current</th><th>Files</th><th>Used by</th><th>Updated</th><th></th></tr></thead><tbody>' +
        skills.map(function (s) {
          return '<tr><td><div class="cell-main"><span class="ico">' + ic("skills", "sm") + "</span><div><strong>" + esc(s.name) + '</strong><small class="mono">' + esc(s.id) + '</small></div></div></td><td><span class="tag perm">v' + esc(s.version_no) + "</span></td><td>" + esc(s.files) + "</td><td>" + esc(s.used_by) + " task" + (s.used_by === 1 ? "" : "s") +
            '</td><td class="nowrap">' + esc(when(s.updated_at)) + '</td><td class="right"><button class="btn sm" data-versions="' + esc(s.id) + '" data-name="' + esc(s.name) + '">' + ic("branch", "sm") + "Versions</button></td></tr>";
        }).join("") + "</tbody></table>" : emptyState("skills", "No skills yet", "Import a skill directory to get started.");
      mount('<div class="page-head"><div><h1>Skills</h1><p>Versioned Claude Code skills, materialized into task workspaces.</p></div><div class="actions"><button class="btn primary" id="sk-import">' + ic("upload", "sm") + "Import skill</button></div></div>" +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("skills") + "</span><h2>Library</h2></div>" + rows + "</section>", "skills", [["Skills"]]);
      $("#sk-import").addEventListener("click", function () {
        modal('<div class="card-head"><span class="ico">' + ic("upload") + '</span><h2>Import skill</h2></div><form id="ski"><label class="field"><span>Directory on the agentd host</span><input name="dir" required placeholder="/path/to/skill (contains SKILL.md)"></label><label class="field"><span>Name (optional)</span><input name="name" maxlength="64"></label><p class="hint">Re-importing the same name creates a new version.</p><div class="actions"><button type="button" class="btn ghost" data-close>Cancel</button><button class="btn primary">Import</button></div></form>', function (root, close) {
          $("#ski", root).addEventListener("submit", function (e) {
            e.preventDefault();
            api("/api/skills/import", { method: "POST", body: { dir: e.target.dir.value, name: e.target.name.value } }).then(function () { close(); toast("Skill imported"); renderSkills(); }).catch(fail);
          });
        });
      });
      $$("[data-versions]").forEach(function (b) {
        b.addEventListener("click", function () {
          var sid = b.getAttribute("data-versions");
          api("/api/skills/" + enc(sid) + "/versions").then(function (v) {
            modal('<div class="card-head"><span class="ico">' + ic("branch") + "</span><h2>" + esc(b.getAttribute("data-name")) + '</h2></div><table class="table"><tbody>' + v.versions.slice().reverse().map(function (x) {
              return "<tr><td><strong>v" + esc(x.no) + "</strong>" + (x.id === v.current ? ' <span class="tag perm">current</span>' : "") + '</td><td class="nowrap">' + esc(when(x.created_at)) + '</td><td class="right">' + (x.id === v.current ? "" : '<button class="btn sm" data-srb="' + esc(x.id) + '">Roll back</button>') + "</td></tr>";
            }).join("") + '</tbody></table><div class="actions"><button class="btn ghost" data-close>Close</button></div>', function (root, close) {
              $$("[data-srb]", root).forEach(function (x) { x.addEventListener("click", function () { api("/api/skills/" + enc(sid) + "/rollback", { method: "POST", body: { version_id: x.getAttribute("data-srb") } }).then(function () { close(); toast("Skill rolled back"); renderSkills(); }).catch(fail); }); });
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
        return '<div class="card qa fill" data-run="' + esc(b.id) + '"><span class="qa-ico">' + ic(btnIcon(b), "lg") + "</span><strong>" + esc(b.label) + "</strong><small>" + esc(b.task_id ? "Runs task · " + (tname[b.task_id] || b.task_id) : "Prompt · " + b.prompt) + '</small><div class="req-actions"><button class="btn sm" data-edit="' + i + '">Edit</button><button class="btn sm danger" data-del="' + i + '">Remove</button></div></div>';
      });
      cards.push('<button class="card qa tint" id="b-add"><span class="qa-ico">' + ic("plus", "lg") + "</span><strong>New button</strong><small>Bind a task or a prompt " + ic("arrow", "sm") + "</small></button>");
      mount('<div class="page-head"><div><h1>Buttons</h1><p>One-click presets. The first four appear in the Home quick action row; click a card to run it.</p></div></div><div class="grid g4">' + cards.join("") + "</div>", "buttons", [["Buttons"]]);
      var save = function (nl) { return api("/api/buttons", { method: "PUT", body: nl }).then(function () { toast("Buttons saved"); renderButtons(); }); };
      var edit = function (idx) {
        var b = idx == null ? { id: "", label: "", description: "", icon: "bolt", task_id: "", prompt: "" } : list[idx];
        modal('<div class="card-head"><span class="ico">' + ic("buttons") + "</span><h2>" + (idx == null ? "New button" : "Edit button") + '</h2></div><form id="bf">' +
          '<div class="form-row"><label class="field"><span>Label</span><input name="label" required maxlength="40" value="' + esc(b.label) + '"></label><label class="field"><span>ID</span><input name="bid" required pattern="[a-z0-9_\\-]{1,40}" value="' + esc(b.id) + '"' + (idx == null ? "" : " readonly") + "></label></div>" +
          '<label class="field"><span>Description</span><input name="description" maxlength="60" value="' + esc(b.description) + '"></label>' +
          '<div class="form-row"><label class="field"><span>Icon</span><select name="icon">' + BUTTON_ICONS.map(function (x) { return '<option value="' + x + '"' + (x === b.icon ? " selected" : "") + ">" + x + "</option>"; }).join("") + '</select></label><label class="field"><span>Task</span><select name="task_id"><option value="">— use a prompt —</option>' +
          tasks.map(function (t) { return '<option value="' + esc(t.id) + '"' + (t.id === b.task_id ? " selected" : "") + ">" + esc(t.name) + "</option>"; }).join("") + "</select></label></div>" +
          '<label class="field"><span>Prompt (when no task)</span><textarea name="prompt" maxlength="8000">' + esc(b.prompt) + '</textarea></label><div class="actions"><button type="button" class="btn ghost" data-close>Cancel</button><button class="btn primary">Save</button></div></form>', function (root, close) {
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
      $$("[data-del]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); if (!confirm("Remove this button?")) return; var nl = list.slice(); nl.splice(+b.getAttribute("data-del"), 1); save(nl).catch(fail); }); });
      $$("[data-run]").forEach(function (c) { c.addEventListener("click", function (e) { if (e.target.closest("button")) return; runButton(c.getAttribute("data-run"), c); }); });
    });
  }

  // ---------- PLUGINS ----------
  function renderPlugins() {
    var kinds = [["terminal", "MCP servers", "Expose tools to Claude Code runs. Each server runs as its own process.", "None installed"], ["bell", "Notifiers", "Deliver budget and run events. Built-in: log channel.", "1 built-in"], ["globe", "Triggers", "Webhooks and file watchers that start tasks.", "None installed"]];
    mount('<div class="page-head"><div><h1>Plugins</h1><p>Independent processes speaking MCP / JSON-RPC — a crash never takes down agentd.</p></div></div>' +
      '<div class="grid g3">' + kinds.map(function (x) {
        return '<section class="card fill"><div class="card-head"><span class="ico">' + ic(x[0]) + "</span><h3>" + x[1] + '</h3></div><p class="grow muted">' + x[2] + '</p><div class="card-foot"><span class="pill plain">' + x[3] + "</span></div></section>";
      }).join("") + '</div><section class="card">' + emptyState("plugins", "No external plugins installed", "Install and lifecycle management arrive in a later phase.") + "</section>", "plugins", [["Plugins"]]);
    return Promise.resolve();
  }

  // ---------- RUNS ----------
  function renderRuns(params) {
    var st = params.get("status") || "", task = params.get("task") || "";
    var qs = "?limit=100" + (st ? "&status=" + enc(st) : "") + (task ? "&task_id=" + enc(task) : "");
    return api("/api/runs" + qs).then(function (runs) {
      var f = [["", "All"], ["running", "Running"], ["succeeded", "Success"], ["failed", "Failed"]];
      mount('<div class="page-head"><div><h1>Runs</h1><p>Every execution, with logs streamed live while it runs.</p></div><div class="actions filters">' + f.map(function (x) { return '<button data-f="' + x[0] + '" class="' + (x[0] === st ? "active" : "") + '">' + x[1] + "</button>"; }).join("") + "</div></div>" +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("pulse") + "</span><h2>" + (task && runs[0] ? "Runs · " + esc(runs[0].task_name) : "All runs") + '</h2><span class="link muted">Times in ' + esc(tzLabel()) + "</span></div>" + runsTable(runs) + "</section>", "runs", [["Runs"]]);
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
        '<div class="card stat"><small>Status</small><strong>' + statusPill(r.status) + "</strong><span>" + esc(triggerText(r.trigger)) + "</span></div>" +
        '<div class="card stat"><small>Duration</small><strong>' + esc(live ? "Running…" : dur(r.duration_ms)) + "</strong><span>Started " + esc(when(r.started_at || r.created_at)) + "</span></div>" +
        '<div class="card stat"><small>Cost</small><strong>' + esc(usd(r.cost_usd, 4)) + "</strong><span>Bucket · " + esc(r.bucket) + "</span></div>" +
        '<div class="card stat"><small>Tokens</small><strong>' + esc(num(r.input_tokens + r.output_tokens)) + "</strong><span>" + esc(num(r.input_tokens)) + " in · " + esc(num(r.output_tokens)) + " out</span></div></div>" +
        '<div class="grid g-side"><section class="card"><div class="card-head"><span class="ico">' + ic("terminal") + '</span><h2>Log</h2><span class="link" id="live-ind">' + (live ? '<span class="live">Live</span>' : '<span class="muted">Finished</span>') + '</span></div><div class="log" id="log" aria-live="polite"></div></section>' +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("info") + '</span><h3>Details</h3></div><dl class="kv"><dt>Run</dt><dd><code>' + esc(r.id) + "</code></dd><dt>Task</dt><dd>" + (r.task_id ? '<a href="#/tasks/' + enc(r.task_id) + '">' + esc(r.task_name) + "</a>" + (r.version_no ? " · v" + esc(r.version_no) : "") : "Ad-hoc") +
        "</dd><dt>Provider</dt><dd>" + esc(r.provider || "—") + "</dd><dt>Model</dt><dd>" + esc(r.model || "—") + "</dd><dt>Prompt</dt><dd>" + esc(r.prompt) + "</dd>" + (r.error ? '<dt>Error</dt><dd class="err">' + esc(r.error) + "</dd>" : "") + "</dl></section></div>",
        "runs", [["Runs", "#/runs"], [runTitle(r)]]);
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
      es.addEventListener("idle", function () { es.close(); $("#live-ind").innerHTML = '<span class="muted">Paused (idle)</span>'; });
      es.onerror = function () { es.close(); };
    });
  }
  // ---------- SPEND ----------
  var taskNames = {};
  function bucketName(b) { return b.scope === "global" ? "Global" : b.scope === "bucket" ? cap(b.scope_id) : (taskNames[b.scope_id] || "Task " + String(b.scope_id).slice(0, 12)); }
  function renderSpend() {
    return api("/api/tasks").then(function (ts) { (ts.tasks || ts || []).forEach(function (t) { taskNames[t.id] = t.name; }); }, function () {}).then(function () { return api("/api/spend"); }).then(function (d) {
      var s = d.summary, budgets = s.budgets || [];
      var global = budgets.filter(function (b) { return b.scope === "global" && b.period === "day"; })[0] || { max_usd: 0, max_tokens: 0 };
      var runsToday = d.top_tasks.reduce(function (a, t) { return a + t.runs; }, 0);
      var byDay = {}, days = [];
      d.days.forEach(function (x) { byDay[x.day] = x; });
      for (var i = 13; i >= 0; i--) { var k = new Date(Date.now() - i * 864e5).toISOString().slice(0, 10); days.push({ day: k, usd: (byDay[k] || {}).usd || 0 }); }
      var mx = Math.max.apply(null, days.map(function (x) { return x.usd; }).concat([0.0001]));
      var pct = function (u, m) { return m ? Math.min(100, u / m * 100) : 0; };
      var cls = function (p) { return p >= 100 ? " bad" : p >= 80 ? " warn" : ""; };
      var gp = pct(s.total_usd, global.max_usd);
      mount('<div class="page-head"><div><h1>Spend</h1><p>Enforced before every run and via --max-budget-usd · ledger day is UTC.</p></div></div>' +
        '<div class="grid g4">' +
        '<div class="card stat"><small>Today</small><strong>' + esc(usd(s.total_usd, 4)) + "</strong><span>" + (global.max_usd ? esc(Math.round(gp)) + "% of " + esc(usd(global.max_usd)) : "No cap") + "</span></div>" +
        '<div class="card stat"><small>Remaining</small><strong>' + esc(global.max_usd ? usd(Math.max(0, global.max_usd - s.total_usd)) : "∞") + "</strong><span>Global daily budget</span></div>" +
        '<div class="card stat"><small>Tokens</small><strong>' + esc(num(s.total_tokens)) + "</strong><span>" + (global.max_tokens ? "of " + esc(num(global.max_tokens)) + " cap" : "No token cap") + "</span></div>" +
        '<div class="card stat"><small>Runs today</small><strong>' + esc(runsToday) + "</strong><span>From " + esc(d.top_tasks.length) + " source" + (d.top_tasks.length === 1 ? "" : "s") + "</span></div></div>" +
        '<div class="grid g2"><section class="card fill"><div class="card-head"><span class="ico">' + ic("receipt") + '</span><h2>Last 14 days</h2><span class="link muted">Peak ' + esc(usd(mx, 4)) + '</span></div><div class="bars grow">' + days.map(function (x, j) {
          return '<div class="bar' + (j === 13 ? " today" : "") + '" title="' + esc(x.day + " · " + usd(x.usd, 4)) + '"><span data-h="' + (x.usd / mx * 100) + '"></span><small>' + esc(x.day.slice(8)) + "</small></div>";
        }).join("") + "</div></section>" +
        '<section class="card fill"><div class="card-head"><span class="ico">' + ic("wallet") + '</span><h2>Budgets</h2><a class="link" href="#/settings">Edit ' + ic("arrow", "sm") + '</a></div><div class="grow">' + budgets.map(function (b) {
          var p = pct(b.used_usd, b.max_usd);
          return '<div class="budget-row"><div><strong>' + esc(bucketName(b)) + '</strong> <span class="muted">· per ' + esc(b.period) + '</span></div><div class="right">' + esc(usd(b.used_usd, 4)) + ' <span class="muted">/ ' + esc(usd(b.max_usd)) + '</span></div><div class="progress' + cls(p) + '"><span data-w="' + p + '"></span></div></div>';
        }).join("") + "</div></section></div>" +
        '<div class="grid g2"><section class="card flush fill"><div class="card-head"><span class="ico">' + ic("tasks") + "</span><h2>Top sources today</h2></div>" + (d.top_tasks.length ? '<table class="table"><thead><tr><th>Source</th><th>Runs</th><th class="right">Cost</th></tr></thead><tbody>' + d.top_tasks.map(function (t) { return "<tr><td><strong>" + esc(t.name) + "</strong></td><td>" + esc(t.runs) + '</td><td class="right"><strong>' + esc(usd(t.usd, 4)) + "</strong></td></tr>"; }).join("") + "</tbody></table>" : emptyState("receipt", "No spend today", "")) + "</section>" +
        '<section class="card flush fill"><div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>Budget events</h2></div>" + (d.events.length ? '<table class="table"><thead><tr><th>When</th><th>Scope</th><th>Detail</th></tr></thead><tbody>' + d.events.map(function (e) { return '<tr><td class="nowrap">' + esc(when(e.at)) + '</td><td><span class="pill bad">' + esc(e.scope) + "</span></td><td>" + esc(e.detail) + "<small>Action · " + esc(e.action) + "</small></td></tr>"; }).join("") + "</tbody></table>" : emptyState("shield", "No breaches", "Tasks pause automatically when a cap is hit.")) + "</section></div>",
        "spend", [["Spend"]]);
    });
  }

  // ---------- SETTINGS ----------
  function renderSettings() {
    return api("/api/settings").then(function (st) {
      var g = (st.budgets || []).filter(function (b) { return b.scope === "global" && b.period === "day"; })[0] || {};
      var buckets = (st.budgets || []).filter(function (b) { return b.scope === "bucket"; });
      var row = function (title, sub, right) { return '<div class="set-row"><div class="lbl"><strong>' + title + "</strong><small>" + sub + "</small></div>" + right + "</div>"; };
      mount('<div class="page-head"><div><h1>Settings</h1><p>Single-user configuration. Secrets are never displayed.</p></div></div>' +
        '<div class="grid g2"><section class="card fill"><div class="card-head"><span class="ico">' + ic("globe") + "</span><h2>WebUI</h2></div>" +
        row("Enable WebUI", "Turning off stops this listener at once. Re-enable with <code>agentctl webui on</code>.", '<button class="switch' + (st.webui_enabled ? " on" : "") + '" id="sw-webui" aria-label="Toggle WebUI"></button>') +
        row("Listen address", st.allow_lan ? "LAN access allowed" : "Loopback only · LAN off", "<code>" + esc(st.addr) + "</code>") +
        row("HTTPS", "Required for any non-loopback bind.", '<span class="pill ' + (st.tls ? "ok" : "plain") + '">' + (st.tls ? "Enabled" : "Off") + "</span>") +
        row("Idle mode", "Live log streams stop after inactivity.", "<span>" + esc(st.idle_minutes) + " min</span>") + "</section>" +
        '<section class="card fill"><div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>Security</h2></div>" +
        row("Password", "argon2id · change with <code>agentctl auth set-password</code>", '<span class="pill ok">Set</span>') +
        row("Two-factor (TOTP)", "Enable with <code>agentctl auth totp-setup</code>", '<span class="pill ' + (st.totp_enabled ? "ok" : "plain") + '">' + (st.totp_enabled ? "On" : "Off") + "</span>") +
        row("Cookies & CSRF", "HttpOnly · SameSite=Strict · Secure over HTTPS · CSRF token · rate-limited login", "") +
        row("Timezone", "Used for every time shown here.", "<span>" + esc(tzLabel()) + "</span>") +
        row("Session", "Signed in as owner on this browser.", '<button class="btn sm" id="btn-logout">' + ic("logout", "sm") + "Sign out</button>") + "</section></div>" +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("wallet") + '</span><h2>Budgets</h2><span class="link muted">Pause + notify on breach</span></div><form id="bud" class="grid g4">' +
        '<label class="field"><span>Global daily USD</span><input name="g_usd" type="number" step="0.01" min="0" value="' + esc(g.max_usd || "") + '"></label>' +
        '<label class="field"><span>Global daily tokens</span><input name="g_tok" type="number" step="1000" min="0" value="' + esc(g.max_tokens || "") + '"></label>' +
        buckets.map(function (b) { return '<label class="field"><span>' + esc(cap(b.scope_id)) + ' daily USD</span><input name="b_' + esc(b.scope_id) + '" type="number" step="0.01" min="0" value="' + esc(b.max_usd || "") + '"></label>'; }).join("") +
        '<div class="field"><span>&nbsp;</span><button class="btn primary">Save budgets</button></div></form></section>' +
        '<section class="card flush"><div class="card-head"><span class="ico">' + ic("key") + '</span><h2>Providers</h2><span class="link muted">Default · ' + esc(st.default_provider) + '</span></div><table class="table"><thead><tr><th>Name</th><th>Base URL</th><th>Model</th><th>Credential</th></tr></thead><tbody>' +
        st.providers.map(function (p) { return "<tr><td><strong>" + esc(p.name) + "</strong></td><td><code>" + esc(p.base_url || "default") + "</code></td><td>" + esc(p.model || "default") + '</td><td><span class="pill ' + (p.key_set ? "ok" : "plain") + '">' + (p.key_set ? "Configured" : "Missing") + "</span><small>env " + esc(p.auth_env || "—") + " · value hidden</small></td></tr>"; }).join("") + "</tbody></table></section>",
        "settings", [["Settings"]]);
      $("#btn-logout").addEventListener("click", function () {
        api("/api/logout", { method: "POST", body: {} }).catch(function () {}).then(function () { state.csrf = ""; showLogin(); });
      });
      $("#sw-webui").addEventListener("click", function () {
        if (!confirm("Turn off the WebUI? This page stops responding until you run `agentctl webui on`.")) return;
        api("/api/webui", { method: "POST", body: { enabled: false } }).then(function () { toast("WebUI turning off…"); }).catch(fail);
      });
      $("#bud").addEventListener("submit", function (e) {
        e.preventDefault();
        var f = e.target, reqs = [api("/api/budgets", { method: "POST", body: { scope: "global", period: "day", max_usd: parseFloat(f.g_usd.value) || 0, max_tokens: parseInt(f.g_tok.value, 10) || 0 } })];
        buckets.forEach(function (b) { reqs.push(api("/api/budgets", { method: "POST", body: { scope: "bucket", scope_id: b.scope_id, bucket: b.scope_id, period: "day", max_usd: parseFloat(f["b_" + b.scope_id].value) || 0 } })); });
        Promise.all(reqs).then(function () { toast("Budgets saved"); }).catch(fail);
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
    p.catch(function (e) { if (e.message !== "Please sign in") { renderTopbar([]); page().innerHTML = '<section class="card">' + emptyState("info", "Something went wrong", e.message) + "</section>"; } });
    if (parts[0]) api("/api/overview").then(renderStatus).catch(function () {});
  }
  window.addEventListener("hashchange", route);

  function boot() {
    api("/api/me").then(function (me) {
      state.csrf = me.csrf_token;
      state.tz = me.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone;
      state.version = me.version || "";
      $("#login").classList.add("hidden");
      $("#shell").classList.remove("hidden");
      renderShell();
      route();
    }).catch(function () { showLogin(); });
  }
  boot();
})();
