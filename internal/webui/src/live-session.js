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
    paintFavicon();
  }
  // ---------- favicon badge ----------
  // The tab icon carries the same status as the title, so a pinned tab (title hidden)
  // still shows it: red count = unread failures (run failed / over budget), amber = Claude
  // Code or the provider needs a look, green = runs going. Idea from Uptime Kuma
  // src/mixins/socket.js (favicon badge with the "down" count, 1s debounce, no animation);
  // drawn here with a plain canvas, no library; redrawn only when the badge changes.
  var fav = { failed: 0, key: "none", timer: null, links: null, img: null };
  function favBadge(o) {
    if (o.failed > 0) return { kind: "failed", color: "#c4352b", text: o.failed > 9 ? "9+" : String(o.failed) };
    if (o.alert) return { kind: "alert", color: "#b26b00", text: "" };
    if (o.running > 0) return { kind: "running", color: "#1f8a4c", text: "" };
    return null;
  }
  function paintFavicon() {
    clearTimeout(fav.timer);
    fav.timer = setTimeout(function () {
      var b = favBadge({ failed: fav.failed, alert: docTitle.alert, running: docTitle.running });
      var key = b ? b.kind + b.text : "none";
      if (key === fav.key) return;
      fav.key = key;
      if (!fav.links) fav.links = $$('link[rel="icon"]').map(function (l) { return { el: l, href: l.getAttribute("href"), type: l.getAttribute("type") }; });
      var set = function (href, type) { fav.links.forEach(function (l) { l.el.setAttribute("href", href || l.href); l.el.setAttribute("type", type || l.type); }); };
      if (!b) { set(); return; }
      var draw = function () {
        if (fav.key !== key) return; // changed again while the image loaded
        var c = document.createElement("canvas"), x = c.getContext && c.getContext("2d");
        if (!x) return;
        c.width = c.height = 64;
        x.drawImage(fav.img, 0, 0, 64, 64);
        var r = b.text ? 17 : 12, cx = 64 - r - 1, cy = 64 - r - 1;
        x.beginPath(); x.arc(cx, cy, r + 3, 0, 2 * Math.PI); x.fillStyle = "#fff"; x.fill();
        x.beginPath(); x.arc(cx, cy, r, 0, 2 * Math.PI); x.fillStyle = b.color; x.fill();
        if (b.text) {
          x.fillStyle = "#fff"; x.textAlign = "center"; x.textBaseline = "middle";
          x.font = "bold " + (b.text.length > 1 ? 19 : 24) + "px system-ui, -apple-system, Segoe UI, sans-serif";
          x.fillText(b.text, cx, cy + 1);
        }
        try { set(c.toDataURL("image/png"), "image/png"); } catch (e) {}
      };
      if (fav.img && fav.img.complete) { draw(); return; }
      if (!fav.img) { fav.img = new Image(); fav.img.src = "/static/favicon.svg" + assetV; }
      fav.img.addEventListener("load", draw, { once: true });
    }, 1000);
  }
  // Unread failures from the notification center feed the badge (and the bell dot).
  function noteNotifs(d) {
    var list = d.items || [], sv = d.seen_at || "";
    state.lastNotifs = d;
    if (sv > state.notifSeen) seenLocal(sv); else if (state.notifSeen > sv) seenPush();
    var dot = $("#bell-dot");
    if (dot) dot.classList.toggle("hidden", !((list[0] && list[0].created_at > state.notifSeen) || (d.pending || []).length));
    fav.failed = list.filter(function (n) { return n.group === "failed" && n.created_at > state.notifSeen; }).length;
    paintFavicon();
    state.chFailing = d.channels_failing || 0;
    paintChannelDot();
  }
  // Red dot on Settings (sidebar) and Settings › Notifications while an enabled channel's
  // last send failed; the count is in the tooltip, the page stays clean.
  function paintChannelDot() {
    var n = state.chFailing || 0, tip = n ? T("nt.failing_tip", { n: n }) : "";
    $$('#nav a[data-nav="settings"], .set-nav a[href="#/settings/notifications"]').forEach(function (a) {
      var dot = $(".nav-dot", a);
      if (!n) { if (dot) dot.remove(); a.removeAttribute("data-tip"); return; }
      if (!dot) { dot = document.createElement("b"); dot.className = "nav-dot"; dot.setAttribute("aria-hidden", "true"); a.appendChild(dot); }
      a.setAttribute("data-tip", tip);
      a.setAttribute("aria-description", tip);
    });
  }
  // after a test / save / switch on the notifications page the dot follows the fresh list
  function noteChannels(chs) {
    state.chFailing = (chs || []).filter(function (c) { return c.enabled && c.last && !c.last.ok; }).length;
    paintChannelDot();
  }
  // Once a minute: the bell and badge stay current without navigating. In a background tab
  // this only runs while something was running, so an idle hidden tab makes no requests,
  // yet a run that ends (or fails) while you're elsewhere still flips the tab icon.
  setInterval(function () {
    if (!state.csrf || ($("#shell") && $("#shell").classList.contains("hidden"))) return;
    if (document.hidden && !docTitle.running) return;
    if (evsLive()) return; // the change stream refreshes the bell as soon as it changes
    // status first: a run that just failed has written its notification by then
    (document.hidden ? loadCC(false) : Promise.resolve()).then(function () { return api("/api/notifications/center"); }).then(noteNotifs).catch(function () {});
  }, 60000);
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
  // With the change stream up, runs starting / ending refresh this at once; the slow timer
  // only keeps uptime and provider health current.
  setInterval(function () { if (state.csrf && !document.hidden && (!evsLive() || Date.now() - cc.at > 120000)) loadCC(false); }, 30000);

  // ---------- idle / SSE ----------
  function stopStream() { if (state.es) { state.es.close(); state.es = null; } }
  function bumpIdle() { state.lastInput = Date.now(); clearTimeout(state.idleTimer); state.idleTimer = setTimeout(stopStream, IDLE_MS); }
  ["mousemove", "click", "scroll", "keydown"].forEach(function (ev) { document.addEventListener(ev, bumpIdle, { passive: true }); });

  // ---------- change stream (/api/events) ----------
  // One small SSE stream says when something changed: {rev, notif, busy}. While it is up,
  // live pages read rev from it instead of asking /api/rev, the bell / tab icon refresh when
  // notif moves and the sidebar status when busy moves (both within about a second instead of
  // the 60 s / 30 s polls). Tabs of one browser share it: the tab holding the "tb-events" lock
  // opens the stream and passes each message on over a BroadcastChannel, so ten tabs still
  // use one connection. Without locks or BroadcastChannel each tab opens its own. Whenever
  // it isn't up (refused, server gone, machine asleep) the polls work exactly as before.
  var evs = { st: null, at: 0, es: null, bc: null, leader: false, on: false, release: null, retry: 0, rt: null, seq: 0 };
  var EVS_STALE_MS = 60000; // the server repeats the state every 25 s
  function evsLive() { return !!(evs.on && evs.st && Date.now() - evs.at < EVS_STALE_MS); }
  function evsApply(st) {
    if (!st || typeof st.rev !== "string") return;
    var prev = evs.st;
    evs.st = st; evs.at = Date.now();
    if (!prev || !state.csrf) return; // the page loaded these itself just now
    if (st.notif !== prev.notif) api("/api/notifications/center", { fresh: true }).then(noteNotifs).catch(function () {});
    if (st.busy !== prev.busy) { cc.at = 0; loadCC(false); }
  }
  function evsDown() { evs.st = null; evs.at = 0; }
  function evsOpen() {
    if (evs.es || !evs.on) return;
    clearTimeout(evs.rt); evs.rt = null;
    var es = new EventSource("/api/events");
    evs.es = es;
    es.onopen = function () { evs.retry = 0; };
    es.onmessage = function (m) {
      var st; try { st = JSON.parse(m.data); } catch (e) { return; }
      evsApply(st);
      if (evs.bc) evs.bc.postMessage({ t: "st", st: st });
    };
    es.onerror = function () {
      evsDown();
      if (evs.bc) evs.bc.postMessage({ t: "down" });
      // The browser retries a dropped stream by itself; a refused one (401, 429, server
      // stopped) is closed for good, so back off like n8n's reconnect timer: 1 s doubling to 60 s.
      if (es.readyState === 2) { evs.es = null; evsLater(); }
    };
  }
  function evsLater() {
    if (!evs.on || evs.rt) return;
    var ms = Math.min(1000 * Math.pow(2, evs.retry++), 60000);
    evs.rt = setTimeout(function () { evs.rt = null; evsOpen(); }, ms);
  }
  function evsClose() { if (evs.es) { evs.es.close(); evs.es = null; } evsDown(); }
  function evsStart() {
    if (evs.on || !window.EventSource || !state.csrf) return;
    evs.on = true;
    var my = ++evs.seq;
    if (window.BroadcastChannel && !evs.bc) {
      evs.bc = new BroadcastChannel("tb-events");
      evs.bc.onmessage = function (m) {
        var d = m.data || {};
        if (d.t === "hello" && evs.leader && evs.st) evs.bc.postMessage({ t: "st", st: evs.st });
        if (evs.leader) return;
        if (d.t === "st") evsApply(d.st);
        if (d.t === "down") evsDown();
      };
    }
    if (evs.bc && navigator.locks && navigator.locks.request) {
      navigator.locks.request("tb-events", function () {
        if (!evs.on || my !== evs.seq) return; // signed out while waiting: let the next tab have it
        evs.leader = true; evsOpen();
        return new Promise(function (res) { evs.release = res; });
      }).catch(function () {});
      evs.bc.postMessage({ t: "hello" });
    } else { evs.leader = true; evsOpen(); }
  }
  // Signed out (or the session ended): close the stream and hand the lock on.
  function evsStop() {
    evs.on = false; evs.leader = false; evs.retry = 0;
    clearTimeout(evs.rt); evs.rt = null;
    evsClose();
    if (evs.release) { evs.release(); evs.release = null; }
  }
  // A laptop that slept can leave a dead connection with no error: no message for a minute
  // means reconnect (and the polls take over meanwhile). Also checked when the window returns.
  function evsCheck() {
    if (!evs.on || !evs.leader || evsLive()) return;
    if (evs.es && evs.es.readyState === 0) return; // the browser is already reconnecting
    evsClose(); evs.retry = 0; evsOpen();
  }
  setInterval(evsCheck, 30000);
  document.addEventListener("visibilitychange", function () { if (!document.hidden) evsCheck(); });

  // ---------- login ----------
  function dropBoot() { var b = document.getElementById("boot"); if (b) b.remove(); }
  function showLogin(totp) {
    dropBoot();
    stopStream();
    evsStop();
    setDocTitle({ page: T("login.submit"), running: 0, alert: false });
    $("#shell").classList.add("hidden");
    var l = $("#login");
    l.classList.remove("hidden");
    l.innerHTML = '<div class="card login-card"><div class="brand"><span class="brand-mark">' + tideMark() + '</span><span class="brand-text"><strong>tidebot</strong><small>' + esc(T("brand.tagline")) + '</small></span></div>' +
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

