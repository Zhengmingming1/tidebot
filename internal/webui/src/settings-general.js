  // ---- Settings › Tags ----
  // Every tag in use with how many tasks carry it; rename, merge or delete it on all tasks at
  // once. Idea from Uptime Kuma (src/components/settings/Tags.vue: one list, count per tag,
  // edit / delete per row; TagEditDialog.vue: the new name is checked against the existing
  // ones while typing). Uptime Kuma refuses a name in use — here it merges the two, and the
  // dialog says so before saving. Every change can be undone from the toast (the server
  // answers with the tags the tasks had before, and /restore puts exactly those back).
  // tagNameCheck: what the dialog says about a new name — "" ok, "same", "bad", or "merge".
  function tagNameCheck(from, raw, inUse) {
    var t = String(raw || "").trim().replace(/^[#＃]+/, "").replace(/\s+/g, " ");
    if (!t || t === from) return { k: "same", t: t };
    if (/[,，\u0000-\u001f\u007f]/.test(t) || Array.from(t).length > TAG_LEN) return { k: "bad", t: t };
    var hit = (inUse || []).filter(function (u) { return u.tag !== from && u.tag.toLowerCase() === t.toLowerCase() && t.toLowerCase() !== from.toLowerCase(); })[0];
    return hit ? { k: "merge", t: hit.tag, n: hit.n } : { k: "", t: t };
  }
  function tagManager(box) {
    var list = [], q = "";
    var undoOf = function (before) {
      return function () { api("/api/tasks/tags/restore", { method: "POST", body: { before: before } }).then(function () { toast(T("tgm.restored")); load(); }).catch(fail); };
    };
    var paint = function () {
      if (!list.length) { box.innerHTML = emptyState("tag", T("tgm.none"), T("tgm.none_sub")) + '<p class="pad-x tgm-go"><a class="btn sm" href="#/tasks">' + ic("tasks", "sm") + esc(T("nav.tasks")) + "</a></p>"; return; }
      var ql = q.trim().toLowerCase(), shown = list.filter(function (u) { return !ql || u.tag.toLowerCase().indexOf(ql) >= 0; });
      var rows = shown.map(function (u) {
        var i = list.indexOf(u);
        return '<div class="set-row tgm-row" data-i="' + i + '"><div class="lbl">' + tagChip(u.tag) + "</div>" +
          '<span class="row-gap"><a class="link tgm-n" href="#/tasks?tag=' + enc(u.tag) + '" data-tip="' + esc(T("tg.filter_tip", { t: u.tag })) + '">' + esc(plural(u.n, "tgm.tasks_one", "tgm.tasks_many")) + "</a>" +
          '<button type="button" class="icon-btn sm" data-tgm="rename" data-tip="' + esc(T("tgm.rename")) + '" aria-label="' + esc(T("tgm.rename") + " · " + u.tag) + '">' + ic("edit", "sm") + "</button>" +
          '<button type="button" class="icon-btn sm danger-text" data-tgm="delete" data-tip="' + esc(T("tgm.delete")) + '" aria-label="' + esc(T("tgm.delete") + " · " + u.tag) + '">' + ic("trash", "sm") + "</button></span></div>";
      }).join("");
      var body = rows || '<p class="muted pad-x">' + esc(T("tgm.no_match")) + "</p>";
      if (list.length > 8) {
        if (!$("#tgm-q", box)) {
          box.innerHTML = '<div class="toolbar tgm-tb"><label class="search-in">' + ic("search", "sm") + '<input id="tgm-q" type="search" maxlength="' + TAG_LEN + '" placeholder="' + esc(T("tgm.filter")) + '" aria-label="' + esc(T("tgm.filter")) + '"></label><span class="grow"></span><span class="muted" id="tgm-count"></span></div><div id="tgm-rows"></div>';
          imeInput($("#tgm-q", box), 120, function (v) { q = v; paint(); });
        }
        $("#tgm-count", box).textContent = T("tl.showing", { n: shown.length, total: list.length });
        $("#tgm-rows", box).innerHTML = body;
      } else box.innerHTML = '<div id="tgm-rows">' + body + "</div>";
    };
    var load = function () { return api("/api/tasks/tags", { fresh: true }).then(function (l) { list = l || []; paint(); }).catch(fail); };
    box.addEventListener("click", function (e) {
      var b = e.target.closest("[data-tgm]");
      if (!b) return;
      var u = list[+b.closest("[data-i]").getAttribute("data-i")];
      if (!u) return;
      if (b.getAttribute("data-tgm") === "rename") tagRenameModal(u, list, function (r) {
        toast(T(r.merged ? "tgm.merged" : "tgm.renamed", { f: u.tag, t: r.tag, n: r.tasks }), false, undoOf(r.before));
        load();
      });
      else confirmDialog({ title: T("tgm.del_title", { t: u.tag }), body: T("tgm.del_body", { n: u.n }), ok: T("tgm.delete"), danger: true }).then(function (y) {
        if (!y) return;
        api("/api/tasks/tags/delete", { method: "POST", body: { tag: u.tag } }).then(function (r) {
          toast(T("tgm.deleted", { t: u.tag, n: r.tasks }), false, undoOf(r.before));
          load();
        }).catch(fail);
      });
    });
    return load();
  }
  function tagRenameModal(u, inUse, done) {
    modal('<div class="card-head"><span class="ico">' + ic("tag") + "</span><h2>" + esc(T("tgm.rename_title")) + "</h2></div>" +
      '<form id="tgm-f"><p class="confirm-body tgm-from">' + tagChip(u.tag) + " " + esc(plural(u.n, "tgm.tasks_one", "tgm.tasks_many")) + "</p>" +
      '<label class="field"><span>' + esc(T("tgm.new_name")) + '</span><input name="to" maxlength="' + TAG_LEN + '" autocomplete="off" spellcheck="false" required value="' + esc(u.tag) + '"></label>' +
      '<p class="tgm-hint" id="tgm-hint" aria-live="polite"></p>' +
      '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary" id="tgm-ok" disabled>' + esc(T("tgm.save")) + "</button></div></form>", function (root, close) {
      var f = $("#tgm-f", root), ok = $("#tgm-ok", root), hint = $("#tgm-hint", root), c = { k: "same" };
      var check = function () {
        c = tagNameCheck(u.tag, f.to.value, inUse);
        ok.disabled = c.k === "same" || c.k === "bad";
        ok.textContent = T(c.k === "merge" ? "tgm.merge" : "tgm.save");
        hint.className = "tgm-hint" + (c.k === "bad" ? " err" : c.k === "merge" ? " warn" : "");
        hint.innerHTML = c.k === "bad" ? esc(T("tgm.bad")) : c.k === "merge" ? ic("info", "sm") + "<span>" + esc(T("tgm.merge_hint", { t: c.t, n: c.n, f: u.tag })) + "</span>" : "";
      };
      f.to.addEventListener("input", check);
      f.addEventListener("submit", function (e) {
        e.preventDefault();
        check();
        if (ok.disabled) return;
        ok.disabled = true;
        api("/api/tasks/tags/rename", { method: "POST", body: { from: u.tag, to: c.t } }).then(function (r) { close(); done(r); }).catch(function (err) { ok.disabled = false; fail(err); });
      });
      check();
      setTimeout(function () { f.to.select(); }, 0);
    });
  }
  // ---------- SETTINGS ----------
  // Left nav + one section at a time. Every row has an id, so #/settings/<section>/<item>
  // scrolls to it. Autosaved fields show "Saved ✓" next to them. Dangerous actions
  // live in the danger zone at the bottom of General.
  var SET_SECTIONS = ["general", "providers", "budgets", "pricing", "tags", "security", "notifications", "network", "data", "about"];
  var SET_ICONS = { general: "settings", providers: "key", budgets: "wallet", pricing: "receipt", tags: "tag", security: "shield", notifications: "bell", network: "globe", data: "folder", about: "info" };
  var RELEASES_PAGE = "https://github.com/Zhengmingming1/tidebot-releases/releases/latest";
  // AGPL-3.0-only: the WebUI links its own source (section 13, for people using it over a network).
  var SOURCE_REPO = "https://github.com/Zhengmingming1/tidebot";
  var NET_CODES = ["timeout", "dns", "tls", "proxy", "refused", "http_404", "http", "signature", "checksum", "unknown"];
  // Classified network error with a "how to fix" hint and the manual download link.
  function netErrHtml(code, detail, attempts, hint) {
    code = NET_CODES.indexOf(code) >= 0 ? code : "unknown";
    var h = '<div class="net-err" role="alert"><p class="err"><strong>' + esc(T("net.e." + code)) + "</strong></p>" +
      '<p class="muted">' + esc(hint || T("net.h." + code)) + "</p>";
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
  // ---- Settings › Data › Storage ----
  // How much the data folder takes (run logs, artifacts, database, backups), how long
  // history is kept, and one-click cleanup with a preview of what it frees. Idea from
  // Uptime Kuma's Monitor History settings (keep N days, 0 = forever, DB size next to
  // "shrink database"). Meta info (file counts, last cleanup) lives in tooltips.
  var ST_PARTS = ["logs", "artifacts", "database", "backups"];
  var ST_DAYS = [7, 14, 30, 60, 90, 180, 365, 0];
  var ST_KEEP = [0, 5, 10, 20, 50, 100];
  var ST_CAP_GB = [1, 2, 5, 10, 20, 50, 0];
  var ST_MIN_FREE_MB = [0, 100, 200, 500, 1024];
  function mbLabel(mb) { return mb >= 1024 && mb % 1024 === 0 ? mb / 1024 + " GB" : mb + " MB"; }
  function stOpts(list, cur, label) {
    if (list.indexOf(cur) < 0) list = list.concat([cur]);
    return list.map(function (v) { return opt(v, label(v), v === cur); }).join("");
  }
  function storageCardHtml() {
    var sel = function (id, key) { return '<select id="' + id + '" data-st="' + key + '" aria-label="' + esc(T("st." + key)) + '" disabled><option>…</option></select>'; };
    var r = function (key, sub, right) { return '<div class="set-row" id="set-st_' + key + '"><div class="lbl"><strong>' + esc(T("st." + key)) + '</strong><small id="st-' + key + '-sub">' + esc(sub) + "</small></div>" + right + "</div>"; };
    return '<section class="card" id="set-storage"><div class="card-head"><span class="ico">' + ic("database") + "</span><h2>" + esc(T("st.title")) + '</h2><span class="st-total" id="st-total"></span><button type="button" class="icon-btn sm hidden" id="st-open" data-tip="' + esc(T("st.open_folder")) + '" aria-label="' + esc(T("st.open_folder")) + '">' + ic("folder", "sm") + "</button></div>" +
      '<div id="st-disk"></div><div class="st-usage" id="st-usage" aria-live="polite"><div class="st-bar sk"></div></div>' +
      r("log_days", T("st.log_days_sub"), sel("st-log", "log_days")) +
      r("artifact_days", T("st.artifact_days_sub"), sel("st-art", "artifact_days")) +
      r("keep_per_task", T("st.keep_per_task_sub"), sel("st-keep", "keep_per_task")) +
      r("max_total", T("st.max_total_sub"), sel("st-cap", "max_total")) +
      r("min_free", T("st.min_free_sub"), sel("st-minfree", "min_free")) +
      r("clean", "…", '<button class="btn sm" id="st-clean" disabled>' + ic("trash", "sm") + esc(T("st.clean_btn")) + "</button>") +
      r("shrink", "…", '<button class="btn sm" id="st-shrink" disabled>' + ic("database", "sm") + esc(T("st.shrink_btn")) + "</button>") + "</section>";
  }
  function storagePaint(v) {
    var box = $("#st-usage");
    if (!box) return;
    var parts = v.parts || {}, total = v.total_bytes || 0, p = v.policy || {};
    var tipOf = function (k) { var x = parts[k] || {}; return T("st.p_" + k) + " · " + fmtBytes(x.bytes) + (k === "database" ? "" : " · " + T("st.files", { n: x.count || 0 })) + (x.packed ? "\n" + T("st.packed", { n: x.packed, raw: fmtBytes(x.packed_raw), on: fmtBytes(x.packed_bytes) }) : ""); };
    box.innerHTML = '<div class="st-bar" role="img" aria-label="' + esc(ST_PARTS.map(tipOf).join("; ")) + '">' + ST_PARTS.filter(function (k) { return (parts[k] || {}).bytes > 0; }).map(function (k) {
      var pct = total ? (parts[k].bytes / total) * 100 : 0;
      return '<span class="st-seg s-' + k + '" data-w="' + Math.max(1, pct).toFixed(2) + '" data-tip="' + esc(tipOf(k)) + '"></span>';
    }).join("") + '</div><ul class="st-legend">' + ST_PARTS.map(function (k) {
      return '<li data-tip="' + esc(tipOf(k)) + '" tabindex="0"><i class="dot s-' + k + '"></i><span>' + esc(T("st.p_" + k)) + "</span><b>" + esc(fmtBytes((parts[k] || {}).bytes)) + "</b></li>";
    }).join("") + "</ul>";
    applySizes(box);
    var last = v.last ? T(v.last.auto ? "st.last_auto" : "st.last_manual", { t: fullTime(v.last.at), f: fmtBytes(v.last.freed_bytes) }) + (v.last.deleted_rows > 0 ? "\n" + T("st.rows", { n: v.last.deleted_rows }) : "") : T("st.last_none");
    // Free space on the data volume: in the tooltip normally, as a notice only when low.
    var dk = v.disk, diskLine = dk ? T("st.disk_free", { f: fmtBytes(dk.free_bytes), t: fmtBytes(dk.total_bytes) }) : "";
    $("#st-total").innerHTML = esc(T("st.total", { s: fmtBytes(total) })) + tip(T("st.where") + "\n" + T("st.tidy") + "\n" + (diskLine ? diskLine + "\n" : "") + last);
    // Under the minimum free space runs don't start: that outranks the "low" warning.
    $("#st-disk").innerHTML = dk && v.blocked ? '<p class="notice bad" role="status">' + ic("database", "sm") + "<span>" + esc(T("st.disk_blocked", { f: fmtBytes(dk.free_bytes), m: mbLabel(v.min_free_mb || 0) })) + tip(T("st.disk_blocked_tip")) + "</span></p>"
      : dk && dk.low ? '<p class="notice warn" role="status">' + ic("database", "sm") + "<span>" + esc(T("st.disk_low", { f: fmtBytes(dk.free_bytes) })) + tip(T("st.disk_low_tip", { th: fmtBytes(dk.threshold_bytes) })) + "</span></p>" : "";
    var day = function (n) { return n ? T("st.days", { n: n }) : T("st.forever"); };
    $("#st-log").innerHTML = stOpts(ST_DAYS, p.log_days, day);
    $("#st-art").innerHTML = stOpts(ST_DAYS, p.artifact_days, day);
    $("#st-keep").innerHTML = stOpts(ST_KEEP, p.keep_per_task, function (n) { return n ? T("st.runs", { n: n }) : T("st.none"); });
    var gb = Math.round((p.max_total_bytes || 0) / 1073741824 * 100) / 100;
    $("#st-cap").innerHTML = stOpts(ST_CAP_GB, gb, function (n) { return n ? n + " GB" : T("st.no_cap"); });
    $("#st-minfree").innerHTML = stOpts(ST_MIN_FREE_MB, v.min_free_mb, function (n) { return n ? mbLabel(n) : T("st.min_free_off"); });
    $$("[data-st]").forEach(function (el) { el.disabled = false; });
    var pv = v.preview || {}, cl = $("#st-clean"), sh = $("#st-shrink");
    $("#st-clean-sub").textContent = pv.files ? T("st.clean_sub", { n: pv.files, s: fmtBytes(pv.bytes) }) : T("st.clean_none");
    cl.disabled = !pv.files;
    $("#st-shrink-sub").textContent = v.db_reclaimable >= 1048576 ? T("st.shrink_sub", { s: fmtBytes(v.db_reclaimable) }) : T("st.shrink_none", { s: fmtBytes((parts.database || {}).bytes) });
    sh.disabled = !(v.db_reclaimable >= 1048576);
    state.storage = v;
  }
  function storageWire() {
    rpc("storage.get", {}).then(storagePaint).catch(fail);
    desktopOpenable().then(function (ok) {
      var b = $("#st-open");
      if (!ok || !b) return;
      b.classList.remove("hidden");
      b.addEventListener("click", function () { desktopOpen({ target: "data" }); });
    });
    $$("[data-st]").forEach(function (el) {
      el.addEventListener("change", function () {
        var k = el.getAttribute("data-st"), n = Number(el.value), body = {};
        if (k === "max_total") body.max_total_bytes = Math.round(n * 1073741824);
        else if (k === "min_free") body.min_free_mb = n;
        else body[k] = n;
        rpc("storage.settings", body).then(function (v) { storagePaint(v); savedMark(el); }).catch(function (e) { fail(e); rpc("storage.get", {}).then(storagePaint); });
      });
    });
    $("#st-clean").addEventListener("click", function (e) {
      var b = e.currentTarget, pv = (state.storage || {}).preview || {}, keep = ((state.storage || {}).policy || {}).keep_per_task;
      confirmDialog({ title: T("st.clean_title"), body: T("st.clean_confirm", { n: pv.files, s: fmtBytes(pv.bytes) }) + "\n" + T(keep ? "st.clean_keeps" : "st.clean_keeps0", { k: keep }), ok: T("st.clean_btn"), danger: true }).then(function (y) {
        if (!y) return;
        b.disabled = true;
        rpc("storage.cleanup", {}).then(function (r) { toast(T("st.cleaned", { n: r.report.deleted_files, s: fmtBytes(r.report.freed_bytes) }) + (r.report.deleted_rows > 0 ? " · " + T("st.rows", { n: r.report.deleted_rows }) : "")); storagePaint(r.storage); }).catch(function (err) { fail(err); b.disabled = false; });
      });
    });
    $("#st-shrink").addEventListener("click", function (e) {
      var b = e.currentTarget; b.disabled = true;
      rpc("storage.vacuum", {}).then(function (r) { toast(T("st.shrunk", { s: fmtBytes(r.saved_bytes) })); storagePaint(r.storage); }).catch(function (err) { fail(err); b.disabled = false; });
    });
  }
  // ---- Settings › General › Scheduling timezone ----
  // One zone decides when cron fires, when daily budgets reset and how times are shown.
  // "Automatic" follows the config file / $TZ / system; picking a zone saves at once and
  // the server moves upcoming scheduled runs. Offsets and local times come from the
  // server's embedded tzdata, so the list reads the same in every WebView and browser.
  // Idea from Uptime Kuma (src/components/settings/General.vue + util-frontend.js
  // timezoneList: "Auto: <guess>" first, zones labelled and sorted by UTC offset).
  function tzOffset(min) {
    var s = min < 0 ? "-" : "+", a = Math.abs(min);
    return "UTC" + s + String(Math.floor(a / 60)).padStart(2, "0") + ":" + String(a % 60).padStart(2, "0");
  }
  function tzButtonHtml(off) {
    var tz = state.tz || "UTC";
    return '<button type="button" class="tz-open" id="tz-open" aria-haspopup="dialog" data-tip="' + esc(T("tz.btn_tip", { l: fmtTime(new Date().toISOString()) })) + '"><span class="tz-name">' + esc(tz.replace(/_/g, " ")) + '</span><span class="tz-off">' + esc(tzOffset(off || 0)) + "</span></button>";
  }
  function tzPicker(done) {
    rpc("timezone.get", {}).then(function (v) {
      var now = Date.parse(v.now) || Date.now(), drift = Date.now() - now;
      var at = function (off, o) { return new Intl.DateTimeFormat(state.lang, Object.assign({ timeZone: "UTC" }, o)).format(new Date(Date.now() - drift + off * 60000)); };
      var hm = function (off) { return at(off, { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }); };
      var all = [{ id: "", label: T("tz.auto"), sub: v.auto.replace(/_/g, " "), offset: v.auto_offset || 0 }].concat(v.zones.map(function (z) { return { id: z.id, label: z.id.replace(/_/g, " "), offset: z.offset }; }));
      all.forEach(function (z) {
        var o = tzOffset(z.offset);
        z.key = (z.label + " " + (z.sub || "") + " " + o + " " + o.replace("UTC", "GMT") + " " + o.replace(/^UTC([+-])0?(\d+):00$/, "UTC$1$2") + " " + (z.id ? "" : T("tz.auto_key"))).toLowerCase();
      });
      var items = all, sel = 0, cur = v.zone || "";
      modal('<div class="card-head"><span class="ico">' + ic("globe") + "</span><h2>" + esc(T("tz.title")) + "</h2></div>" +
        '<div class="tz-search">' + ic("search", "sm") + '<input id="tz-q" type="text" autocomplete="off" spellcheck="false" maxlength="64" data-noguard role="combobox" aria-expanded="true" aria-controls="tz-list" aria-autocomplete="list" aria-label="' + esc(T("tz.search")) + '" placeholder="' + esc(T("tz.search")) + '" autofocus></div>' +
        '<ul id="tz-list" role="listbox" aria-label="' + esc(T("tz.title")) + '"></ul>' +
        '<div class="actions"><span class="tz-note">' + esc(T("tz.note")) + '</span><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button></div>", function (root, close) {
        var box = $(".modal", root), ul = $("#tz-list", root), q = $("#tz-q", root), busy = false;
        box.classList.add("tz-modal");
        var draw = function () {
          ul.innerHTML = items.length ? items.map(function (z, i) {
            var on = z.id === cur;
            return '<li role="option" id="tz-o' + i + '" data-i="' + i + '" aria-selected="' + (i === sel) + '" class="' + (i === sel ? "sel" : "") + (on ? " cur" : "") + '" data-tip="' + esc(at(z.offset, { weekday: "short", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }) + " · " + tzOffset(z.offset)) + '">' +
              '<span class="ck">' + (on ? ic("check", "sm") : "") + '</span><span class="nm">' + esc(z.label) + (z.sub ? "<small>" + esc(z.sub) + "</small>" : "") + '</span><span class="meta">' + esc(tzOffset(z.offset) + " · " + hm(z.offset)) + "</span></li>";
          }).join("") : '<li class="none">' + esc(T("search.none")) + "</li>";
          q.setAttribute("aria-activedescendant", items.length ? "tz-o" + sel : "");
        };
        var move = function (i) {
          if (!items.length) return;
          var was = $("#tz-o" + sel, ul); if (was) { was.classList.remove("sel"); was.setAttribute("aria-selected", "false"); }
          sel = (i + items.length) % items.length;
          var li = $("#tz-o" + sel, ul); if (li) { li.classList.add("sel"); li.setAttribute("aria-selected", "true"); if (li.scrollIntoView) li.scrollIntoView({ block: "nearest" }); }
          q.setAttribute("aria-activedescendant", "tz-o" + sel);
        };
        var pick = function (i) {
          var z = items[i];
          if (!z || busy) return;
          if (z.id === cur) { close(); return; }
          busy = true;
          rpc("timezone.set", { zone: z.id }).then(function (r) {
            state.tz = r.active || state.tz;
            close();
            toast(T(r.moved ? "tz.saved_moved" : "tz.saved", { z: (r.active || "").replace(/_/g, " "), n: r.moved || 0 }));
            if (done) done(r);
          }).catch(fail).then(function () { busy = false; });
        };
        imeInput(q, 0, function () {
          var s = q.value.trim().toLowerCase();
          items = s ? all.filter(function (z) { return s.split(/\s+/).every(function (w) { return z.key.indexOf(w) >= 0; }); }) : all;
          sel = 0; draw(); ul.scrollTop = 0;
        });
        q.addEventListener("keydown", function (e) {
          if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); move(sel + (e.key === "ArrowDown" ? 1 : -1)); }
          else if (e.key === "PageDown" || e.key === "PageUp") { e.preventDefault(); move(Math.max(0, Math.min(items.length - 1, sel + (e.key === "PageDown" ? 8 : -8)))); }
          else if (e.key === "Enter") { e.preventDefault(); pick(sel); }
        });
        ul.addEventListener("mousemove", function (e) { var li = e.target.closest("li[data-i]"); if (li && +li.getAttribute("data-i") !== sel) move(+li.getAttribute("data-i")); });
        ul.addEventListener("click", function (e) { var li = e.target.closest("li[data-i]"); if (li) pick(+li.getAttribute("data-i")); });
        // Start on the zone in use so the list opens where you are.
        var i0 = all.map(function (z) { return z.id; }).indexOf(cur);
        sel = i0 < 0 ? 0 : i0;
        draw();
        move(sel);
      });
    }).catch(fail);
  }
  // ---- Settings › General › Power ----
  // Scheduled runs of tasks that can wait are held back on an unplugged laptop and run
  // once when power is back; urgent tasks and runs you start always go. The idea is
  // Duplicati's "disable-on-battery" (scheduled only, manual still runs, unknown power
  // = run); the three task priorities and the low-battery tier are tidebot's. The
  // current reading is a pill in the card head, its details in the tooltip.
  var PWR_MODES = ["off", "low", "battery"];
  function powerCardHtml() {
    return '<section class="card" id="set-power"><div class="card-head"><span class="ico">' + ic("battery") + "</span><h2>" + esc(T("pwr.title")) + '</h2><span class="pwr-now" id="pwr-now"></span></div>' +
      '<div class="set-row" id="set-power_mode"><div class="lbl"><strong>' + esc(T("pwr.mode")) + '</strong><small id="pwr-sub">…</small></div>' +
      '<select id="pwr-mode" aria-label="' + esc(T("pwr.mode")) + '" disabled><option>…</option></select></div><div id="pwr-wait"></div></section>';
  }
  // The reading in a word ("电池 · 62%"); kind is the pill colour.
  function powerNow(s) {
    s = s || {};
    var pct = s.percent >= 0 ? " · " + s.percent + "%" : "";
    if (!s.known) return { kind: "plain", text: T("pwr.unknown") };
    if (!s.on_battery) return { kind: s.has_battery ? "ok" : "plain", text: T(s.has_battery ? "pwr.ac" : "pwr.no_battery") + (s.has_battery ? pct : "") };
    return { kind: s.saver || (s.percent >= 0 && s.percent <= 20) ? "warn" : "info", text: T(s.saver ? "pwr.saver" : "pwr.battery") + pct };
  }
  function powerPaint(v) {
    var sel = $("#pwr-mode");
    if (!sel || !v) return;
    var now = powerNow(v.state), mode = PWR_MODES.indexOf(v.mode) >= 0 ? v.mode : "low";
    $("#pwr-now").innerHTML = '<span class="pill ' + now.kind + '" tabindex="0" data-tip="' + esc(T("pwr.now_tip", { n: v.low_pct || 20 })) + '">' + esc(now.text) + "</span>";
    sel.innerHTML = PWR_MODES.map(function (k) { return opt(k, T("pwr.m_" + k) + (k === v.default_mode ? T("pwr.recommended") : ""), k === mode); }).join("");
    sel.disabled = false;
    $("#pwr-sub").textContent = T("pwr.sub_" + mode, { n: v.low_pct || 20 });
    $("#pwr-wait").innerHTML = v.waiting > 0 ? '<p class="notice warn" role="status">' + ic("clock", "sm") + "<span>" + esc(T("pwr.waiting", { n: v.waiting })) + tip(T("pwr.waiting_tip")) + "</span></p>" : "";
  }
  function powerWire() {
    var sel = $("#pwr-mode");
    if (!sel) return;
    rpc("power.get", {}).then(powerPaint).catch(fail);
    sel.addEventListener("change", function () {
      rpc("power.settings", { mode: sel.value }).then(function (v) { powerPaint(v); savedMark(sel); }).catch(function (e) { fail(e); rpc("power.get", {}).then(powerPaint); });
    });
  }
  // ---- Settings › General › Quiet hours ----
  // A daily window (scheduling timezone) in which scheduled runs don't start — a laptop that
  // shouldn't spin its fan at night, a provider plan that's cheaper by day. A task that comes
  // due inside runs once when the window ends; runs by hand, buttons and webhooks still go.
  // Idea from Uptime Kuma's recurring maintenance (src/pages/EditMaintenance.vue: a time
  // range plus a Mon…Sun weekday picker; server/model/maintenance.js: a window that ends
  // before it starts runs into the next day). Saved on every change, like the power card.
  var QH_DAYS = [1, 2, 3, 4, 5, 6, 0];
  function quietCardHtml() {
    var wd = function (d) { try { return new Intl.DateTimeFormat(state.lang, { weekday: "short", timeZone: "UTC" }).format(new Date(Date.UTC(2023, 0, 1 + d))); } catch (e) { return String(d); } };
    return '<section class="card qh-card" id="set-quiet"><div class="card-head"><span class="ico">' + ic("moon") + "</span><h2>" + esc(T("qh.title")) + "</h2>" + tip(T("qh.tip")) +
      '<span class="qh-now" id="qh-now"></span><button type="button" class="switch" id="qh-on" role="switch" aria-checked="false" aria-label="' + esc(T("qh.title")) + '" disabled></button></div>' +
      '<div class="set-row" id="set-quiet_time"><div class="lbl"><strong>' + esc(T("qh.time")) + '</strong><small id="qh-sub">…</small></div>' +
      '<span class="qh-range"><input type="time" id="qh-from" required aria-label="' + esc(T("qh.from")) + '" disabled><span class="muted" aria-hidden="true">–</span><input type="time" id="qh-to" required aria-label="' + esc(T("qh.to")) + '" disabled></span></div>' +
      '<div class="set-row" id="set-quiet_days"><div class="lbl"><strong>' + esc(T("qh.days")) + "</strong><small>" + esc(T("qh.days_sub")) + "</small></div>" +
      '<div class="qh-days" role="group" aria-label="' + esc(T("qh.days")) + '">' + QH_DAYS.map(function (d) { return '<button type="button" data-qh-day="' + d + '" aria-pressed="true" disabled>' + esc(wd(d)) + "</button>"; }).join("") + "</div></div></section>";
  }
  // "23:00 – 次日 07:00 · 工作日" from the setting.
  function quietSummary(v) {
    var overnight = v.to <= v.from, days = v.days || [];
    var d = !days.length ? T("qh.every_day") : days.join(",") === "1,2,3,4,5" ? T("qh.weekdays") : days.join(",") === "0,6" ? T("qh.weekends") : T("qh.n_days", { n: days.length });
    return T(overnight ? "qh.range_next" : "qh.range", { a: v.from, b: v.to }) + " · " + d;
  }
  function quietPaint(v) {
    var card = $("#set-quiet");
    if (!card || !v) return;
    card._v = v;
    var on = !!v.on, days = v.days && v.days.length ? v.days : QH_DAYS;
    var sw = $("#qh-on", card);
    sw.disabled = false; sw.classList.toggle("on", on); sw.setAttribute("aria-checked", String(on));
    card.classList.toggle("off", !on);
    $("#qh-from", card).value = v.from; $("#qh-to", card).value = v.to;
    $("#qh-from", card).disabled = $("#qh-to", card).disabled = false;
    $("#qh-sub", card).textContent = quietSummary(v);
    $$("[data-qh-day]", card).forEach(function (b) { b.disabled = false; b.setAttribute("aria-pressed", String(days.indexOf(+b.getAttribute("data-qh-day")) >= 0)); });
    // the state in a word in the head; when it holds runs / when it starts next in its tooltip
    var now = $("#qh-now", card);
    if (!on) now.innerHTML = "";
    else if (v.active) now.innerHTML = '<span class="pill info" tabindex="0" data-tip="' + esc(T("qh.active_tip", { t: when(v.until) }) + (v.due ? "\n" + T("qh.due", { n: v.due }) : "")) + '">' + esc(T("qh.active", { t: clock(v.until) })) + "</span>";
    else now.innerHTML = v.next_start ? '<span class="pill plain" tabindex="0" data-tip="' + esc(T("qh.next_tip")) + '">' + esc(T("qh.next", { t: when(v.next_start) })) + "</span>" : "";
  }
  function quietWire() {
    var card = $("#set-quiet");
    if (!card) return;
    var load = function () { return rpc("scheduler.quiet.get", {}).then(quietPaint); };
    load().catch(fail);
    var save = function (patch, el) {
      var v = Object.assign({}, card._v || {}, patch);
      if (v.from === v.to) { toast(T("qh.e_same"), true); quietPaint(card._v); return; }
      rpc("scheduler.quiet.set", { on: !!v.on, from: v.from, to: v.to, days: v.days || [] }).then(function (r) {
        quietPaint(r);
        if (el) savedMark(el);
        if ("on" in patch) toast(T(r.on ? "qh.t_on" : "qh.t_off", { r: quietSummary(r) }));
        // the sidebar pill and Home follow on the next status poll; ask now
        rpc("scheduler.pause.get", {}).then(spApply).catch(function () {});
      }).catch(function (e) { fail(e); load(); });
    };
    $("#qh-on", card).addEventListener("click", function () { save({ on: !(card._v && card._v.on) }, null); });
    ["from", "to"].forEach(function (k) {
      var inp = $("#qh-" + k, card);
      inp.addEventListener("change", function () {
        // a cleared field (some WebViews allow it) goes back to what was saved
        if (!/^\d{2}:\d{2}$/.test(inp.value)) { quietPaint(card._v); return; }
        var p = {}; p[k] = inp.value; save(p, inp);
      });
    });
    $(".qh-days", card).addEventListener("click", function (e) {
      var b = e.target.closest("[data-qh-day]");
      if (!b || b.disabled) return;
      var cur = $$("[data-qh-day]", card).filter(function (x) { return x.getAttribute("aria-pressed") === "true"; }).map(function (x) { return +x.getAttribute("data-qh-day"); });
      var d = +b.getAttribute("data-qh-day"), i = cur.indexOf(d);
      if (i >= 0) {
        if (cur.length === 1) { toast(T("qh.e_no_day"), true); return; }
        cur.splice(i, 1);
      } else cur.push(d);
      cur.sort();
      save({ days: cur.length === 7 ? [] : cur }, b.parentNode);
    });
  }
