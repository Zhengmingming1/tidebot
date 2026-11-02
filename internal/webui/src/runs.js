  // ---------- RUNS ----------
  // Filter chips carry their counts (same task + search as the list). "Failed" includes
  // timeouts; "Stopped" is runs stopped by hand or skipped (budget / paused task), which
  // aren't faults. The list loads RUNS_PAGE at a time with "Load more"; for the time sorts
  // the next page starts after the last run shown (keyset cursor, so runs starting
  // meanwhile don't shift it), the other sorts page by offset. Pages already loaded stay
  // when the list refreshes live or re-renders. Idea: n8n findManyAndCount (lastId + count).
  var RUNS_PAGE = 100, RUNS_MAX = 1000;
  var RUN_FILTERS = [["", "runs.all_f", "all"], ["running", "st.running", "running"], ["succeeded", "st.succeeded", "succeeded"], ["failed", "st.failed", "failed", "runs.f_failed_tip"], ["stopped", "runs.f_stopped", "stopped", "runs.f_stopped_tip"]];
  var runsView = { q: "", sort: "new", seq: 0, key: "", n: RUNS_PAGE };
  function runsQuery(st, task, v, rg) { return "sort=" + enc(v.sort) + (st ? "&status=" + enc(st) : "") + (task ? "&task_id=" + enc(task) : "") + (v.q ? "&q=" + enc(v.q) : "") + rangeQuery(rg); }
  function runsHash(st, task, rg) { var p = []; if (st) p.push("status=" + enc(st)); if (task) p.push("task=" + enc(task)); var r = rangeQuery(rg).slice(1); if (r) p.push(r); return "#/runs" + (p.length ? "?" + p.join("&") : ""); }
  // what the total for the current filter is, from the counts (null for a filter without a chip)
  function runsTotal(counts, st) { var f = RUN_FILTERS.filter(function (x) { return x[0] === st; })[0]; return f && counts && typeof counts[f[2]] === "number" ? counts[f[2]] : null; }
  function runsMoreNext(list, sort) {
    if (!list.length) return "";
    return sort === "new" || sort === "old" ? "&before=" + enc(list[list.length - 1].id) : "&offset=" + list.length;
  }

  // ---------- runs: time range ----------
  // Quick ranges plus a small calendar for any span of days. Days are calendar days in the
  // scheduler's zone (state.tz), the same zone every time on the page is shown in, and the
  // server resolves them in that zone too, so "today" here matches the home page count.
  // Idea: n8n ExecutionsFilter.vue (startDate / endDate → startedAfter / startedBefore,
  // counted as an active filter); we keep it to whole days and put it in the address.
  var RUN_RANGES = ["", "today", "yesterday", "7d", "30d"];
  function isDayKey(k) { return typeof k === "string" && /^\d{4}-\d\d-\d\d$/.test(k) && !isNaN(Date.parse(k + "T00:00:00Z")) && new Date(k + "T00:00:00Z").toISOString().slice(0, 10) === k; }
  function dayAdd(k, n) { var d = new Date(k + "T00:00:00Z"); d.setUTCDate(d.getUTCDate() + n); return d.toISOString().slice(0, 10); }
  function todayKey() { return dayKey(new Date()); }
  function runsRangeOf(params) {
    var r = params.get("range") || "", f = params.get("from") || "", t = params.get("to") || "";
    if (r && RUN_RANGES.indexOf(r) > 0) return { range: r, from: "", to: "" };
    f = isDayKey(f) ? f : ""; t = isDayKey(t) ? t : "";
    if (f && t && f > t) { var x = f; f = t; t = x; }
    return { range: "", from: f, to: t };
  }
  function rangeOn(rg) { return !!(rg && (rg.range || rg.from || rg.to)); }
  function rangeQuery(rg) { return !rg ? "" : rg.range ? "&range=" + enc(rg.range) : (rg.from ? "&from=" + enc(rg.from) : "") + (rg.to ? "&to=" + enc(rg.to) : ""); }
  // first and last day the range covers (inclusive), "" for an open end
  function rangeDays(rg) {
    var t = todayKey();
    switch (rg.range) {
      case "today": return [t, t];
      case "yesterday": return [dayAdd(t, -1), dayAdd(t, -1)];
      case "7d": return [dayAdd(t, -6), t];
      case "30d": return [dayAdd(t, -29), t];
    }
    return [rg.from, rg.to];
  }
  function fmtDayKey(k, long) {
    var d = new Date(k + "T00:00:00Z"), o = { timeZone: "UTC", month: "short", day: "numeric" };
    if (long || k.slice(0, 4) !== todayKey().slice(0, 4)) o.year = "numeric";
    try { return new Intl.DateTimeFormat(state.lang, o).format(d); } catch (e) { return k; }
  }
  function rangeLabel(rg) {
    if (rg.range) return T("rr." + rg.range);
    if (!rg.from && !rg.to) return T("rr.all");
    if (rg.from && !rg.to) return T("rr.since", { d: fmtDayKey(rg.from) });
    if (!rg.from) return T("rr.until", { d: fmtDayKey(rg.to) });
    return rg.from === rg.to ? fmtDayKey(rg.from) : fmtDayKey(rg.from) + " – " + fmtDayKey(rg.to);
  }
  function rangeTip(rg) {
    if (!rangeOn(rg)) return T("rr.tip_all", { tz: state.tz });
    var d = rangeDays(rg);
    return T("rr.tip", { from: d[0] ? d[0] + " 00:00" : "…", to: (d[1] || todayKey()) + " 23:59", tz: state.tz });
  }
  // Popover next to the button: quick ranges on the left (apply at once), a month calendar
  // on the right (click the first day, then the last; hover previews), Apply / Clear below.
  // Keyboard: Tab between parts, arrows move by day / week, Enter picks, Esc closes.
  function rangePicker(btn, rg, onPick) {
    var old = $("#rr-pop");
    if (old) { old.remove(); btn.setAttribute("aria-expanded", "false"); return; }
    var today = todayKey(), cur = rangeDays(rg), sel = [cur[0] || "", cur[1] || (cur[0] ? today : "")], anchor = "", hover = "";
    var view = (sel[1] || today).slice(0, 7), focusKey = sel[1] || today;
    var weekStart = /^zh/.test(state.lang) ? 1 : 0;
    var pop = document.createElement("div");
    pop.id = "rr-pop"; pop.className = "popover rr-pop"; pop.setAttribute("role", "dialog"); pop.setAttribute("aria-label", T("rr.title"));
    btn.parentNode.appendChild(pop);
    btn.setAttribute("aria-expanded", "true");
    var fmt = function (o, d) { try { return new Intl.DateTimeFormat(state.lang, Object.assign({ timeZone: "UTC" }, o)).format(d); } catch (e) { return ""; } };
    var close = function (refocus) {
      pop.remove(); btn.setAttribute("aria-expanded", "false");
      document.removeEventListener("mousedown", off, true); document.removeEventListener("keydown", key, true);
      if (refocus && document.contains(btn)) btn.focus();
    };
    var off = function (e) { if (!pop.contains(e.target) && !btn.contains(e.target)) close(false); };
    var lo = function () { var a = anchor ? anchor : sel[0], b = anchor ? (hover || anchor) : sel[1]; return a && b && a > b ? [b, a] : [a, b]; };
    var paint = function () {
      var y = +view.slice(0, 4), m = +view.slice(5, 7) - 1, first = new Date(Date.UTC(y, m, 1));
      var lead = (first.getUTCDay() - weekStart + 7) % 7, days = new Date(Date.UTC(y, m + 1, 0)).getUTCDate(), r = lo(), cells = "";
      for (var i = 0; i < 7; i++) cells += '<span class="rr-wd" aria-hidden="true">' + esc(fmt({ weekday: "narrow" }, new Date(Date.UTC(2024, 0, 7 + ((i + weekStart) % 7))))) + "</span>";
      for (i = 0; i < lead; i++) cells += "<span></span>";
      for (var dd = 1; dd <= days; dd++) {
        var k = view + "-" + (dd < 10 ? "0" : "") + dd, fut = k > today, cls = "rr-d";
        if (r[0] && r[1] && k >= r[0] && k <= r[1]) cls += " in";
        if (k === r[0]) cls += " start";
        if (k === r[1]) cls += " end";
        if (k === today) cls += " today";
        cells += '<button type="button" class="' + cls + '" data-d="' + k + '" tabindex="' + (k === focusKey ? "0" : "-1") + '"' + (fut ? " disabled" : "") + ' aria-pressed="' + (cls.indexOf(" in") > 0 ? "true" : "false") + '" aria-label="' + esc(fmtDayKey(k, true)) + '">' + dd + "</button>";
      }
      var nextOk = dayAdd(view + "-01", 31).slice(0, 7) <= today.slice(0, 7);
      var picked = r[0] && r[1] ? (r[0] === r[1] ? fmtDayKey(r[0], true) : fmtDayKey(r[0], true) + " – " + fmtDayKey(r[1], true)) : "";
      pop.innerHTML = '<div class="rr-presets" role="group" aria-label="' + esc(T("rr.quick")) + '">' + RUN_RANGES.map(function (x) {
          var on = x ? rg.range === x : !rangeOn(rg);
          return '<button type="button" data-rr="' + x + '" class="' + (on ? "on" : "") + '">' + esc(T(x ? "rr." + x : "rr.all")) + "</button>";
        }).join("") + "</div>" +
        '<div class="rr-cal"><div class="rr-head"><button type="button" class="icon-btn sm" id="rr-prev" aria-label="' + esc(T("rr.prev")) + '">' + ic("chevLeft", "sm") + "</button><strong>" + esc(fmt({ year: "numeric", month: "long" }, first)) + '</strong><button type="button" class="icon-btn sm" id="rr-next" aria-label="' + esc(T("rr.next")) + '"' + (nextOk ? "" : " disabled") + ">" + ic("chevRight", "sm") + "</button></div>" +
        '<div class="rr-grid">' + cells + "</div>" +
        '<div class="rr-foot"><span class="rr-sel num" id="rr-sel">' + esc(anchor ? T("rr.pick_end") : picked || T("rr.pick_start")) + '</span><button type="button" class="btn sm ghost" id="rr-clear"' + (rangeOn(rg) ? "" : " disabled") + ">" + esc(T("rr.clear")) + '</button><button type="button" class="btn sm primary" id="rr-apply"' + (r[0] && r[1] && !anchor ? "" : " disabled") + ">" + esc(T("rr.apply")) + "</button></div></div>";
      $$("[data-rr]", pop).forEach(function (b) { b.addEventListener("click", function () { close(false); onPick({ range: b.getAttribute("data-rr"), from: "", to: "" }); }); });
      $("#rr-prev", pop).addEventListener("click", function () { view = dayAdd(view + "-01", -1).slice(0, 7); focusKey = ""; paint(); });
      $("#rr-next", pop).addEventListener("click", function () { if (nextOk) { view = dayAdd(view + "-01", 31).slice(0, 7); focusKey = ""; paint(); } });
      $("#rr-clear", pop).addEventListener("click", function () { close(true); onPick({ range: "", from: "", to: "" }); });
      $("#rr-apply", pop).addEventListener("click", function () { var q = lo(); close(true); onPick({ range: "", from: q[0], to: q[1] }); });
      $$("[data-d]", pop).forEach(function (b) {
        b.addEventListener("click", function () { pick(b.getAttribute("data-d")); });
        b.addEventListener("mouseenter", function () { if (anchor) { hover = b.getAttribute("data-d"); shade(); } });
      });
      if (!$('[data-d][tabindex="0"]', pop)) { var f = $("[data-d]:not([disabled])", pop); if (f) f.tabIndex = 0; }
    };
    // hover preview without rebuilding the grid (keeps the pointer target stable)
    var shade = function () {
      var r = lo();
      $$("[data-d]", pop).forEach(function (b) {
        var k = b.getAttribute("data-d"), on = r[0] && r[1] && k >= r[0] && k <= r[1];
        b.classList.toggle("in", !!on); b.classList.toggle("start", k === r[0]); b.classList.toggle("end", k === r[1]);
      });
    };
    var pick = function (k) {
      if (!k || k > today) return;
      if (!anchor) { anchor = k; hover = k; sel = [k, k]; }
      else { sel = anchor <= k ? [anchor, k] : [k, anchor]; anchor = ""; hover = ""; }
      focusKey = k; paint();
      var b = $('[data-d="' + k + '"]', pop); if (b) b.focus();
    };
    var key = function (e) {
      if (!document.contains(pop)) { close(false); return; }
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(true); return; }
      var t = e.target, k = t && t.getAttribute && t.getAttribute("data-d");
      if (!k || !pop.contains(t)) return;
      var step = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7 }[e.key];
      if (!step) return;
      e.preventDefault(); e.stopPropagation();
      var n = dayAdd(k, step);
      if (n > today) return;
      focusKey = n;
      if (anchor) hover = n;
      if (n.slice(0, 7) !== view) { view = n.slice(0, 7); paint(); } else { $$("[data-d]", pop).forEach(function (b) { b.tabIndex = b.getAttribute("data-d") === n ? 0 : -1; }); if (anchor) shade(); }
      var nb = $('[data-d="' + n + '"]', pop); if (nb) nb.focus();
    };
    paint();
    document.addEventListener("mousedown", off, true);
    document.addEventListener("keydown", key, true);
    var fb = $("[data-rr].on", pop) || $("[data-rr]", pop); if (fb) fb.focus();
  }
  function renderRuns(params) {
    var st = params.get("status") || "", task = params.get("task") || "", v = runsView, rg = runsRangeOf(params);
    var key = [st, task, v.sort, v.q, rangeQuery(rg)].join("|");
    if (key !== v.key) { v.key = key; v.n = RUNS_PAGE; }
    var url = function () { return "/api/runs?limit=" + Math.min(Math.max(v.n, RUNS_PAGE), RUNS_MAX) + "&" + runsQuery(st, task, v, rg); };
    var curl = function () { return "/api/runs/counts?" + runsQuery("", task, v, rg).replace(/^sort=[^&]*&?/, ""); };
    var noCounts = function () { return {}; };
    return Promise.all([api(url()), api(curl()).catch(noCounts)]).then(function (res) {
      var runs = res[0], counts = res[1] || {};
      var chip = function (x) {
        var n = counts[x[2]];
        return '<button data-f="' + x[0] + '" class="' + (x[0] === st ? "active" : "") + '"' + (x[3] ? ' data-tip="' + esc(T(x[3])) + '"' : "") + ">" + esc(T(x[1])) + (typeof n === "number" ? ' <span class="cnt" data-c="' + x[2] + '">' + esc(num(n)) + "</span>" : "") + "</button>";
      };
      mount('<div class="page-head"><div><h1>' + esc(T("nav.runs")) + "</h1><p>" + (task && runs[0] ? esc(T("runs.for", { name: runs[0].task_name })) + " · " : "") + esc(T("runs.times_in", { tz: tzLabel() })) + '</p></div><div class="actions"><div class="rel rr-wrap"><button type="button" class="btn rr-btn' + (rangeOn(rg) ? " on" : "") + '" id="r-range" aria-haspopup="dialog" aria-expanded="false" data-tip="' + esc(rangeTip(rg)) + '">' + ic("calendar", "sm") + "<span>" + esc(rangeLabel(rg)) + "</span>" + ic("chevDown", "sm") + '</button></div><button type="button" class="btn ghost rx-btn" id="r-export" aria-haspopup="menu" aria-expanded="false" data-tip="' + esc(T("rx.tip")) + '"' + (runs.length ? "" : " disabled") + ">" + ic("download", "sm") + "<span>" + esc(T("rx.export")) + "</span>" + ic("chevDown", "sm") + '</button><button class="btn primary" id="r-quick">' + ic("sparkle", "sm") + esc(T("home.quick")) + "</button></div></div>" +
        '<section class="card flush"><div class="toolbar run-tb"><label class="search-in">' + ic("search", "sm") + '<input id="rq" type="search" maxlength="100" placeholder="' + esc(T("runs.search")) + '" value="' + esc(v.q) + '">' + KB_SLASH + '</label>' +
        '<div class="filters run-f">' + RUN_FILTERS.map(chip).join("") + "</div>" +
        '<select id="rsort" aria-label="' + esc(T("runs.sort")) + '">' + [["new", T("runs.s_new")], ["old", T("runs.s_old")], ["cost", T("runs.s_cost")], ["tokens", T("runs.s_tokens")], ["duration", T("runs.s_duration")]].map(function (x) { return opt(x[0], x[1], x[0] === v.sort); }).join("") + "</select>" +
        '</div><div id="rtable"></div><div class="runs-more hidden" id="rmore"></div></section>', "runs", [[T("nav.runs")]]);
      $("#r-quick").addEventListener("click", function () { quickRunModal(); });
      var setRange = function (n) { location.hash = runsHash(st, task, n); };
      $("#r-range").addEventListener("click", function (e) { rangePicker(e.currentTarget, rg, setRange); });
      $("#r-export").addEventListener("click", function (e) {
        if ($("#rx-menu")) { $("#rx-menu")._close(); return; }
        runsExportMenu(e.currentTarget, { st: st, task: task, taskName: task && shown[0] ? shown[0].task_name : "", v: v, rg: rg, total: runsTotal(counts, st), shown: shown.length });
      });
      var shown = runs, loading = false, ended = false;
      var foot = function () {
        var total = runsTotal(counts, st), box = $("#rmore");
        if (!box) return;
        var more = total != null ? shown.length < total : !ended && shown.length >= v.n;
        if (shown.length >= RUNS_MAX) more = false;
        box.classList.toggle("hidden", !more);
        if (!more) { box.innerHTML = ""; return; }
        box.innerHTML = '<span class="muted num">' + esc(total != null ? T("runs.shown_of", { n: num(shown.length), total: num(total) }) : T("runs.shown", { n: num(shown.length) })) + '</span><button type="button" class="btn sm" id="rmore-btn">' + ic("chevDown", "sm") + esc(T("runs.more")) + "</button>";
        $("#rmore-btn").addEventListener("click", loadMore);
      };
      var paint = function (list) {
        shown = list;
        $("#rtable").innerHTML = list.length || !(v.q || rangeOn(rg)) ? runsTable(list, true, v.q) : v.q ? emptyState("search", T("runs.no_match"), "") :
          '<div class="empty">' + ic("calendar") + "<strong>" + esc(T("rr.empty", { r: rangeLabel(rg) })) + "</strong>" + esc(T("rr.empty_sub")) + '<div class="empty-act"><button type="button" class="btn sm" id="rr-empty-clear">' + esc(T("rr.show_all")) + "</button></div></div>";
        var ec = $("#rr-empty-clear"); if (ec) ec.addEventListener("click", function () { setRange({ range: "", from: "", to: "" }); });
        wireRows($("#rtable"));
        var xb = $("#r-export"); if (xb) xb.disabled = !list.length;
        $$("[data-stop]", $("#rtable")).forEach(function (b) { b.addEventListener("click", function (e) {
          e.stopPropagation(); b.disabled = true;
          stopRun(b.getAttribute("data-stop"), b.getAttribute("data-st")).then(function (y) { if (y) reload(); else b.disabled = false; }).catch(function (err) { b.disabled = false; fail(err); });
        }); });
        $$("[data-rerun]", $("#rtable")).forEach(function (b) { b.addEventListener("click", function (e) {
          e.stopPropagation(); b.disabled = true;
          api("/api/runs/" + enc(b.getAttribute("data-rerun")) + "/rerun", { method: "POST", body: {} }).then(function (res) { toast(startedText(res, "runs.rerun_started")); reload(); }).catch(function (err) { b.disabled = false; fail(err); });
        }); });
        foot();
      };
      var paintCounts = function (c) {
        counts = c || {};
        RUN_FILTERS.forEach(function (x) { var el = $('.run-f [data-c="' + x[2] + '"]'); if (el && typeof counts[x[2]] === "number") el.textContent = num(counts[x[2]]); });
      };
      var loadMore = function () {
        if (loading) return;
        loading = true;
        var btn = $("#rmore-btn"); if (btn) btn.disabled = true;
        var my = v.seq;
        apiRaw("/api/runs?limit=" + RUNS_PAGE + "&" + runsQuery(st, task, v, rg) + runsMoreNext(shown, v.sort)).then(function (page) {
          if (my !== v.seq || !$("#rtable")) return;
          var seen = {}; shown.forEach(function (r) { seen[r.id] = 1; });
          var list = shown.concat(page.filter(function (r) { return !seen[r.id]; }));
          // nothing new came back: the total was stale, stop offering more
          if (page.length < RUNS_PAGE) ended = true;
          if (!page.length) counts = {};
          v.n = Math.max(v.n, list.length);
          var y = window.scrollY; paint(list); window.scrollTo(0, y);
        }).catch(function (err) { if (btn) btn.disabled = false; fail(err); }).then(function () { loading = false; });
      };
      // a newer request always wins; slow responses for old search text are dropped
      var reload = function () {
        var my = ++v.seq;
        return Promise.all([api(url()), api(curl()).catch(noCounts)]).then(function (r) { if (my === v.seq && $("#rtable")) { paintCounts(r[1]); paint(r[0]); live(r[0]); } });
      };
      var gate = revGate();
      // While something runs: check every 2 s. With the change stream up it also keeps
      // checking when nothing runs (a check is free then), so a scheduled run that starts
      // shows up here within 2 s instead of on the next visit.
      var live = function (list) {
        var busy = list.some(function (r) { return r.status === "running" || r.status === "queued"; });
        if (busy || evs.on) livePoll(function () { return busy || evsLive() ? gate(reload) : null; }, 2000); else stopPoll();
      };
      paint(runs); live(runs);
      imeInput($("#rq"), 250, function (val) { if (val.trim() === v.q) return; v.q = val.trim(); v.key = [st, task, v.sort, v.q, rangeQuery(rg)].join("|"); v.n = RUNS_PAGE; reload().catch(fail); });
      $("#rsort").addEventListener("change", function (e) { v.sort = e.target.value; v.key = [st, task, v.sort, v.q, rangeQuery(rg)].join("|"); v.n = RUNS_PAGE; reload().catch(fail); });
      // switching the status keeps the task filter (it used to drop it)
      $$("[data-f]").forEach(function (b) { b.addEventListener("click", function () { location.hash = runsHash(b.getAttribute("data-f"), task, rg); }); });
    });
  }

  // runsExportMenu: "Export" on the runs page — every run the list matches (status chip,
  // task, search, time range, sort), not only the loaded pages, as CSV for a spreadsheet or
  // JSONL for scripts. The head says how many and which filters, so the file holds no
  // surprises. Idea: langfuse web/src/components/BatchExportTableButton.tsx (an export
  // menu on the table that takes its filter, search and order, one entry per format);
  // ours downloads at once instead of queueing a job.
  function runsExportMenu(anchor, o) {
    var f = RUN_FILTERS.filter(function (x) { return x[0] === o.st; })[0], parts = [];
    if (o.st) parts.push(T(f ? f[1] : "runs.all_f"));
    if (o.taskName) parts.push(o.taskName);
    if (rangeOn(o.rg)) parts.push(rangeLabel(o.rg));
    if (o.v.q) parts.push(T("rx.f_q", { q: o.v.q }));
    var n = o.total != null ? o.total : o.shown;
    var item = function (fmt, ico) {
      return '<button type="button" role="menuitem" class="spz-item rx-item" data-fmt="' + fmt + '"><span>' + ic(ico, "sm") + esc(T("rx." + fmt)) + "</span><small>" + esc(T("rx." + fmt + "_sub")) + "</small></button>";
    };
    popMenu(anchor, "down", {
      id: "rx-menu", cls: "rx-menu", label: T("rx.export"),
      html: '<div class="spz-head rx-head">' + ic("download", "sm") + "<div><strong>" + esc(o.total != null ? T("rx.title_n", { n: num(n) }) : T("rx.title")) + "</strong><small>" + esc(parts.length ? parts.join(" · ") : T("runs.all")) + "</small></div></div>" +
        item("csv", "list") + item("jsonl", "code") +
        '<p class="spz-note">' + ic("info", "sm") + "<span>" + esc(T("rx.note", { tz: tzLabel() }) + (n > 100000 ? " " + T("rx.cap") : "")) + "</span></p>",
      pick: function (b) {
        var fmt = b.getAttribute("data-fmt");
        downloadFile("/api/runs/export?format=" + fmt + "&lang=" + enc(state.lang) + "&" + runsQuery(o.st, o.task, o.v, o.rg), "tidebot-runs." + fmt, anchor);
      }
    });
  }

