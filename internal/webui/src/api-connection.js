  // ---------- api ----------
  // Stale-while-revalidate for page loads: GETs issued while a route renders
  // resolve at once from cache (no blank frame between tabs), then refetch in
  // the background; if the data changed the page re-renders softly (scroll and
  // focus kept). Any write clears the cache.
  var swr = { on: false, cache: {}, order: [], pending: {}, timer: null, gen: 0 };
  // pf: filled by an intent prefetch (hover / press / focus / j-k on a link) shortly before the
  // click, so the page renders from it without asking the server a second time.
  function swrPut(key, data, pf) {
    if (!swr.cache[key]) { swr.order.push(key); if (swr.order.length > 80) delete swr.cache[swr.order.shift()]; }
    var str = JSON.stringify(data), changed = swr.cache[key] && swr.cache[key].s !== str;
    swr.cache[key] = { s: str, t: Date.now(), pf: !!pf };
    return changed;
  }
  function swrClear() { swr.cache = {}; swr.order = []; swr.gen++; }
  function swrRerender() {
    clearTimeout(swr.timer);
    swr.timer = setTimeout(function () {
      var a = document.activeElement, pg = page();
      if ($("#modal-root").innerHTML || (a && pg && pg.contains(a) && /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName))) return;
      route(true);
    }, 120);
  }
  var SWR_PF_MS = 10000, SWR_MAX_MS = 5 * 60 * 1000;
  // opts.cacheKey: a read that goes out as POST (read-only RPC) shares the same cache.
  // opts.read: a POST that changes nothing, so it doesn't drop the cache either.
  function api(path, opts) {
    opts = opts || {};
    var method = opts.method || "GET", key = method === "GET" ? path : opts.cacheKey || "";
    if (key && swr.on && !opts.fresh) {
      var c = swr.cache[key], age = c ? Date.now() - c.t : Infinity;
      if (c && c.err) {
        // A prefetched refusal (e.g. "no canary to compare") stands for a moment like an answer does.
        if (c.pf && age < SWR_PF_MS) { var pe = new Error(c.err.m); pe.status = c.err.status; pe.data = c.err.data; return Promise.reject(pe); }
        c = null; age = Infinity;
      }
      if (c && ((swr.soft && age < 3000) || (c.pf && age < SWR_PF_MS))) return Promise.resolve(JSON.parse(c.s)); // just refreshed / prefetched: no second trip
      var net = swr.pending[key] || (swr.pending[key] = apiRaw(path, opts).then(function (d) { delete swr.pending[key]; if (swrPut(key, d)) swrRerender(); return d; }, function (e) { delete swr.pending[key]; throw e; }));
      if (c && age < SWR_MAX_MS) { net.catch(function () {}); return Promise.resolve(JSON.parse(c.s)); }
      return net.then(function (d) { return JSON.parse(JSON.stringify(d)); });
    }
    return apiRaw(path, opts).then(function (d) {
      if (method !== "GET" && !opts.read) swrClear();
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
    // opts.timeout (ms): abort and reject with a readable error instead of waiting forever.
    var ac = opts.timeout && window.AbortController ? new AbortController() : null, tm = null;
    if (ac) tm = setTimeout(function () { ac.abort(); }, opts.timeout);
    return netFetch(path, { method: method, headers: headers, body: body, credentials: "same-origin", signal: ac ? ac.signal : undefined }).catch(function (err) {
      if (tm) clearTimeout(tm);
      if (ac && ac.signal.aborted) { var et = new Error(T("err.req_timeout")); et.timeout = true; throw et; }
      throw err;
    }).then(function (res) {
      if (tm) clearTimeout(tm);
      var ct = res.headers.get("content-type") || "";
      var p = ct.indexOf("json") >= 0 ? res.json() : res.text();
      return p.then(function (data) {
        if (res.status === 401 && path !== "/api/login" && !opts.quiet) { showLogin(); var e1 = new Error(T("err.sign_in")); e1.auth = true; throw e1; }
        if (!res.ok) { var e2 = new Error(data && data.error_info ? errInfoText(data.error_info, data.error) : (data && data.error) || res.statusText); e2.status = res.status; e2.data = data; throw e2; }
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
  // undo: optional action (default label "撤销"; act names another action, e.g. "在文件夹中显示").
  function toast(msg, bad, undo, act) {
    var el = document.createElement("div");
    el.className = "toast" + (bad ? " bad" : "") + (undo ? " undo" : "");
    el.setAttribute("role", bad ? "alert" : "status");
    el.innerHTML = '<span class="t-msg"></span>' + (undo ? '<button class="t-undo">' + esc(act || T("t.undo")) + "</button>" : "") + (bad || undo ? '<button class="t-x" aria-label="' + esc(T("btn.close")) + '">' + ic("x", "sm") + "</button>" : "");
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
  // GET a server-made file (CSV export…) and save it under the server's filename.
  // Blob keeps the bytes as sent (a UTF-8 BOM survives; res.text() would strip it).
  function downloadFile(path, fallback, btn) {
    if (btn) btn.disabled = true;
    return netFetch(path, { credentials: "same-origin" }).then(function (res) {
      if (res.status === 401) { showLogin(); throw new Error(T("err.sign_in")); }
      if (!res.ok) return res.json().catch(function () { return {}; }).then(function (d) { throw new Error(d.error || res.statusText); });
      return res.blob().then(function (b) { return saveBlob(b, dispName(res.headers.get("content-disposition"), fallback)); });
    }).catch(fail).then(function () { if (btn) btn.disabled = false; });
  }
  // File name from Content-Disposition: the UTF-8 filename* (non-ASCII skill names) wins
  // over the plain ASCII fallback.
  function dispName(cd, fallback) {
    cd = cd || "";
    var u = /filename\*=UTF-8''([^;\s]+)/i.exec(cd);
    if (u) { try { return decodeURIComponent(u[1]); } catch (e) { /* fall through */ } }
    var m = /filename="([^"]+)"/.exec(cd);
    return m ? m[1] : fallback;
  }
  // One skill version as a .zip that Import takes back as is. vid empty = current version.
  function exportSkill(id, vid, btn) {
    return downloadFile("/api/skills/" + enc(id) + "/export" + (vid ? "?v=" + enc(vid) : ""), "skill.zip", btn);
  }
  // Save bytes the page made. In the desktop app this goes through the native "Save as"
  // dialog (macOS's webview ignores <a download>, Linux's drops files silently in the
  // download folder); in a browser it is the usual download.
  var desktopSave = null;
  function nativeSave() {
    if (!desktopSave) desktopSave = api("/api/desktop/save").then(function (r) { return !!(r && r.available); }, function () { return false; });
    return desktopSave;
  }
  function saveBlob(b, name) {
    return nativeSave().then(function (native) {
      if (!native) {
        var a = document.createElement("a"); a.href = URL.createObjectURL(b); a.download = name;
        document.body.appendChild(a); a.click(); a.remove();
        setTimeout(function () { URL.revokeObjectURL(a.href); }, 1000);
        toast(T("dl.saved", { n: name }));
        return;
      }
      return netFetch("/api/desktop/save?name=" + enc(name), { method: "POST", headers: { "Accept": "application/json", "Content-Type": "application/octet-stream", "X-CSRF-Token": state.csrf }, body: b, credentials: "same-origin" }).then(function (res) {
        return res.json().catch(function () { return {}; }).then(function (d) {
          if (!res.ok) throw new Error(d.error || res.statusText);
          if (d.canceled) return;
          // "Show in folder" names the file by the id the server gave it, never by path.
          desktopOpenable().then(function (ok) {
            toast(T("dl.saved_to", { p: d.path }), false, ok && d.reveal ? function () { desktopOpen({ target: "saved", id: d.reveal }); } : null, T("dl.show"));
          });
        });
      });
    });
  }
  // Desktop app only: show tidebot's data folder or a just-saved file in Finder / Explorer / the Linux file manager.
  var desktopOpenOK = null, desktopOS = "";
  // The button is named after the OS file manager, like VS Code's "Reveal in Finder / File Explorer".
  function fmLabel() { return T(desktopOS === "darwin" ? "ws.open_mac" : desktopOS === "windows" ? "ws.open_win" : "ws.open_linux"); }
  function desktopOpenable() {
    if (!desktopOpenOK) desktopOpenOK = api("/api/desktop/open").then(function (r) { desktopOS = (r && r.os) || ""; return !!(r && r.available); }, function () { return false; });
    return desktopOpenOK;
  }
  function desktopOpen(body) { return api("/api/desktop/open", { method: "POST", body: body }).catch(fail); }
  function toastUndo(msg, undo) { return toast(msg, false, undo); }
  // A click that failed because tidebot is unreachable says so once, not once per click;
  // the toast goes away by itself when the connection is back.
  function fail(e) {
    if (e && e.offline) { if (!net.toast) { var c = toast(e.message, true); net.toast = function () { c(); net.toast = null; }; } return; }
    toast(e.message, true);
  }
  // ---------- connection ----------
  // When tidebot can't be reached (agentd restarting or upgraded, the laptop waking from sleep,
  // Wi-Fi gone in server mode) each engine says it its own way and in English: "Failed to
  // fetch" (Chrome / WebView2), "Load failed" (Safari / WKWebView), "NetworkError when
  // attempting to fetch resource." (Firefox), WebKitGTK varies. All of them become one
  // translated error, and one bar at the bottom retries with backoff, counts down, offers
  // "Retry now" and repaints the page once the server answers again — instead of an English
  // toast per click and a dead page. A short blip shows nothing: the bar waits for a probe.
  // Ideas: Uptime Kuma src/mixins/socket.js (grace delay before "Lost connection… Reconnecting"),
  // VS Code src/vs/workbench/contrib/remote/browser/remote.ts ("Attempting to reconnect in N
  // seconds…", "Reconnect Now") and platform/remote/common/remoteAgentConnection.ts (wait list).
  var NET_WAIT = [2, 3, 5, 5, 10, 10, 15, 30];
  function netDelay(n) { return NET_WAIT[Math.max(0, Math.min(n, NET_WAIT.length - 1))]; }
  // No answer at all: fetch rejects with a TypeError (refused, reset, DNS, offline). Our own
  // timeout is an AbortError and stays a timeout. A reverse proxy answering 502 / 503 / 504
  // with its own page means the same thing; tidebot's own errors are always JSON.
  function netLost(err, res) {
    if (res) return (res.status === 502 || res.status === 503 || res.status === 504) && ((res.headers && res.headers.get("content-type")) || "").indexOf("json") < 0;
    return !!err && err.name !== "AbortError" && (err.name === "TypeError" || /network ?error|failed to fetch|load failed/i.test(err.message || ""));
  }
  function netError() { var e = new Error(T("err.offline")); e.offline = true; return e; }
  var net = { down: false, shown: false, n: 0, left: 0, t: null, tick: null, at: 0, busy: false, toast: null, hideT: null };
  // fetch() for tidebot's own endpoints: no answer → netDown() and the translated error;
  // any answer from tidebot → netUp().
  function netFetch(path, init) {
    return fetch(path, init).then(function (res) {
      if (netLost(null, res)) { netDown(); throw netError(); }
      if (net.down) netUp();
      return res;
    }, function (err) {
      if (netLost(err)) { netDown(); throw netError(); }
      throw err;
    });
  }
  function netDown() {
    if (net.down) return;
    net.down = true; net.shown = false; net.n = 0; net.left = 0; net.at = Date.now();
    clearTimeout(net.t); net.t = setTimeout(netProbe, 1500);
  }
  // One cheap authenticated GET; any answer from tidebot (401 included) means it is back.
  function netProbe() {
    clearTimeout(net.t); clearInterval(net.tick); net.t = null;
    if (!net.down || net.busy) return;
    net.busy = true; netPaint();
    var ac = window.AbortController ? new AbortController() : null, tm = ac ? setTimeout(function () { ac.abort(); }, 8000) : null;
    fetch("/api/rev", { credentials: "same-origin", cache: "no-store", headers: { "Accept": "application/json" }, signal: ac ? ac.signal : undefined }).then(function (res) {
      return !netLost(null, res);
    }, function () { return false; }).then(function (ok) {
      if (tm) clearTimeout(tm);
      net.busy = false;
      if (ok) { netUp(); return; }
      net.n++; net.shown = true; net.left = netDelay(net.n - 1);
      netPaint();
      // The countdown stands still in a hidden tab (no requests there); coming back retries.
      net.tick = setInterval(function () {
        if (document.hidden) return;
        if (--net.left <= 0) { netProbe(); return; }
        netPaint();
      }, 1000);
    });
  }
  function netUp() {
    if (!net.down) return;
    var was = net.shown;
    net.down = false; net.shown = false; net.busy = false;
    clearTimeout(net.t); clearInterval(net.tick); net.t = null;
    if (net.toast) { net.toast(); net.toast = null; }
    if (!was) { netPaint(); return; }
    netPaint(true);
    // The page under the bar is stale or shows "Failed": repaint it in place, unless a dialog
    // is open or someone is typing on the page (their edits stay; live pages catch up alone).
    if (document.getElementById("boot")) { boot(); return; }
    if (!state.csrf) return;
    loadCC(false);
    api("/api/notifications/center").then(noteNotifs).catch(function () {});
    if ($("#err-retry")) { lastHash = null; route(); return; }
    var a = document.activeElement, pg = page();
    if ($("#modal-root").innerHTML || (a && pg && pg.contains(a) && /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName))) return;
    refresh();
  }
  // Something saw a hiccup (a live log stream dropped): check quietly, show the bar only if it lasts.
  function netCheck() { if (!net.down) netDown(); else if (!net.busy && !net.t && !net.left) netProbe(); }
  function netNow() { if (net.down && !net.busy) netProbe(); }
  function netPaint(back) {
    var bar = $("#net-bar");
    if (!net.shown && !back) {
      if (bar && !bar.classList.contains("back")) { bar.remove(); document.body.classList.remove("net-on"); }
      return;
    }
    if (!bar) {
      bar = document.createElement("div"); bar.id = "net-bar"; bar.className = "net-bar";
      bar.setAttribute("role", "status"); bar.setAttribute("aria-live", "polite");
      document.body.appendChild(bar);
    }
    document.body.classList.add("net-on");
    if (back) {
      bar.className = "net-bar back";
      bar.innerHTML = ic("check", "sm") + '<span class="nb-msg">' + esc(T("conn.back")) + "</span>";
      bar.removeAttribute("data-tip");
      clearTimeout(net.hideT);
      net.hideT = setTimeout(function () { var b = $("#net-bar"); if (b && b.classList.contains("back")) { b.remove(); document.body.classList.remove("net-on"); } }, 2000);
      return;
    }
    // Built once per outage; each second only the countdown text changes, so the button keeps
    // focus and a click, and screen readers hear "lost connection" once, not every second.
    if (!bar.classList.contains("lost")) {
      bar.className = "net-bar lost";
      bar.innerHTML = '<span class="nb-ico">' + ic("offline", "sm") + '</span><span class="nb-msg">' + esc(T("conn.lost")) + '<span class="nb-sub" aria-hidden="true"></span></span><button type="button" class="btn sm" id="nb-now">' + esc(T("conn.now")) + "</button>";
      $("#nb-now", bar).addEventListener("click", netNow);
    }
    bar.setAttribute("data-tip", T("conn.tip", { t: clock(net.at), n: num(net.n) }));
    $(".nb-sub", bar).textContent = net.busy ? T("conn.retrying") : T("conn.retry_in", { n: net.left });
  }
  window.addEventListener("online", netNow);
  document.addEventListener("visibilitychange", function () { if (!document.hidden && net.down && net.shown) netNow(); });

  // CSP forbids inline style attributes; sizes are applied through CSSOM.
  function applySizes(root) {
    $$("[data-w]", root).forEach(function (el) { el.style.width = Math.max(0, Math.min(100, Number(el.getAttribute("data-w")))) + "%"; });
    $$("[data-h]", root).forEach(function (el) { el.style.height = Math.max(1, Math.min(100, Number(el.getAttribute("data-h")))) + "%"; });
    fitTables(root);
  }
  // Keep real table semantics and all columns. Only the table region scrolls on narrow windows.
  function fitTables(root) {
    $$("table.table", root).forEach(function (table) {
      if (table.parentElement.classList.contains("table-scroll")) return;
      var wrap = document.createElement("div");
      wrap.className = "table-scroll"; wrap.tabIndex = 0;
      wrap.setAttribute("role", "region"); wrap.setAttribute("aria-label", T("layout.table"));
      table.parentNode.insertBefore(wrap, table); wrap.appendChild(table);
    });
  }

