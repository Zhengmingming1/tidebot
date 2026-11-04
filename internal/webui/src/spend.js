  // ---------- SPEND ----------
  var taskNames = {};
  // One budget row on the spend page. The cap is a button: click (or Enter) edits it in place.
  function budgetRow(b, k) {
    var pct = b.max_usd ? Math.min(100, b.used_usd / b.max_usd * 100) : 0, hp = b.held_usd > 0 && b.max_usd ? Math.min(100 - pct, b.held_usd / b.max_usd * 100) : 0;
    var htip = hp > 0 ? ' data-tip="' + esc(T("sp.held_tip", { n: b.held_runs, held: usd(b.held_usd, 4) })) + '"' : "";
    var name = bucketName(b), capTxt = b.max_usd ? usd(b.max_usd) : "∞";
    if (b.scope === "global") return '<div class="bud-row" role="row"><span role="cell"><strong>' + esc(name) + '</strong><span class="muted"> · ' + esc(T("sp.per_day")) + '</span></span><span role="cell"><a href="#/settings/budgets">' + esc(T("sp.bud_settings")) + '</a></span><span role="cell" class="right">' + esc(usd(b.used_usd, 4)) + ' / ' + esc(capTxt) + '</span><span role="cell">' + esc(T(b.action === "block_notify" ? "sp.a_block" : "sp.a_pause")) + '</span><span role="cell">' + esc(b.warn_at_pct || 80) + '%</span></div>';
    return '<div class="bud-row" role="row" data-brow="' + k + '"><span class="bud-name" role="cell"' + (b.scope === "task" ? ' title="' + esc(name) + '"' : "") + "><strong>" + esc(name) + '</strong><span class="muted"> · ' + esc(T("sp.per_" + b.period)) + "</span></span>" +
      '<span role="cell"><span class="progress' + (pct >= 100 ? " bad" : pct >= 80 ? " warn" : "") + (hp > 0 ? " split" : "") + '" role="progressbar" aria-valuenow="' + Math.round(pct) + '" aria-valuemin="0" aria-valuemax="100"><span data-w="' + pct + '"></span>' + (hp > 0 ? '<span class="held" data-w="' + hp + '"></span>' : "") + "</span></span>" +
      '<span class="bud-amt right" role="cell"><span' + htip + ">" + esc(usd(b.used_usd, 4)) + (hp > 0 ? '<span class="held-mark" aria-hidden="true"> +' + esc(usd(b.held_usd, 2)) + "</span>" : "") + '</span> <span class="muted">/</span> <button type="button" class="cap-edit" data-bcap="' + k + '" data-tip="' + esc(T("sp.cap_tip")) + '" aria-label="' + esc(T("sp.cap_aria", { name: name, v: capTxt })) + '">' + esc(capTxt) + "</button></span>" +
      '<span role="cell"><select data-bact="' + k + '" aria-label="' + esc(name + " · " + T("sp.on_breach")) + '">' + opt("pause_notify", T("sp.a_pause"), b.action !== "block_notify") + opt("block_notify", T("sp.a_block"), b.action === "block_notify") + "</select></span>" +
      '<span role="cell"><select data-bwarn="' + k + '" aria-label="' + esc(name + " · " + T("sp.warn_at")) + '">' + [50, 70, 80, 90, 100].map(function (n) { return opt(n, n + "%", (b.warn_at_pct || 80) === n); }).join("") + "</select></span></div>";
  }
  // "12.5", "12,5", "$12.50", "¥ 12" -> 12.5; "" -> null (keep); anything else -> NaN.
  function parseCap(v) {
    v = String(v || "").replace(/[\s$¥￥,]/g, function (c) { return c === "," ? "." : ""; });
    if (!v) return null;
    return /^\d{1,7}(\.\d{0,4})?$/.test(v) ? Math.round(parseFloat(v) * 1e4) / 1e4 : NaN;
  }
  // In-place cap editor (idea: n8n N8nInlineTextEdit — outline on hover without moving the
  // text, the input grows with what is typed, Enter / blur submit, Esc cancels, empty reverts).
  // Setting 0 turns the cap off, so that one asks first in the app's own dialog.
  function editCap(btn, b, save) {
    if (!btn || btn.classList.contains("editing")) return;
    var old = b.max_usd ? String(Math.round(b.max_usd * 1e4) / 1e4) : "", name = bucketName(b);
    var wrap = document.createElement("span");
    wrap.className = "cap-in";
    wrap.innerHTML = '<span class="cap-sym">' + esc(curSym(state.currency || "USD")) + '</span><input type="text" inputmode="decimal" autocomplete="off" spellcheck="false" maxlength="12" data-noguard aria-label="' + esc(T("sp.cap_aria", { name: name, v: old || "∞" })) + '">';
    var inp = wrap.querySelector("input");
    inp.value = old; inp.placeholder = "∞";
    var fit = function () { inp.size = Math.max(4, Math.min(12, inp.value.length + 1)); };
    fit();
    btn.classList.add("editing"); btn.hidden = true;
    btn.parentNode.insertBefore(wrap, btn);
    inp.focus(); inp.select();
    var done = false;
    var back = function (refocus) { if (wrap.parentNode) wrap.parentNode.removeChild(wrap); btn.hidden = false; btn.classList.remove("editing"); if (refocus !== false) btn.focus(); };
    // blur = the user went elsewhere: save a valid value, drop a bad one, never pull focus back
    var end = function (commit, blur) {
      if (done) return;
      var v = parseCap(inp.value);
      if (commit && v !== null && isNaN(v)) { if (blur) { done = true; back(false); return; } inp.setAttribute("aria-invalid", "true"); wrap.classList.add("bad"); toast(T("sp.cap_bad"), true); inp.focus(); return; }
      done = true;
      if (!commit || v === null || inp.value.trim() === old || v === (old ? parseCap(old) : 0)) { back(!blur); return; }
      var go = v > 0 ? Promise.resolve(true) : confirmDialog({ title: T("sp.cap_off_title", { name: name }), body: T("sp.cap_off_body"), ok: T("sp.cap_off_ok"), icon: "wallet", danger: true });
      go.then(function (ok) {
        if (!ok) { back(); return; }
        inp.disabled = true;
        return save(v).then(function () { toast(v > 0 ? T("sp.cap_saved", { name: name, v: usd(v) }) : T("sp.cap_off_done", { name: name })); }, function (e) { back(); fail(e); });
      });
    };
    inp.addEventListener("input", function () { inp.removeAttribute("aria-invalid"); wrap.classList.remove("bad"); fit(); });
    inp.addEventListener("keydown", function (e) {
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); done = true; back(); }
      else if (e.key === "Enter") { e.preventDefault(); end(true); }
    });
    inp.addEventListener("blur", function () { end(true, true); });
  }
  function bucketName(b) { return b.scope === "global" ? T("sp.global") : b.scope === "bucket" ? (T("bucket." + b.scope_id) === "bucket." + b.scope_id ? cap(b.scope_id) : T("bucket." + b.scope_id)) : (taskNames[b.scope_id] || T("col.task") + " " + String(b.scope_id).slice(0, 12)); }
  var spendView = { range: "7d", by: "task", day: "" }, spendCache = {};
  var SPEND_RANGES = ["7d", "30d", "all"], SPEND_BYS = ["task", "provider_model", "model", "provider", "tag"];
  // Daily spend columns: every day of the range (empty days included, so the axis never
  // jumps), a rounded top value + mid gridline, the date and amount in each bar's tooltip.
  // Daily bars. Each bar is a button: click / Enter picks that day and the breakdown below
  // shows only it (click again or Esc for the whole range); ←/→/Home/End move between days
  // (one tab stop for the chart). The picked bar stays coloured, the others fade.
  function spendChartKeys(range, days) {
    var keys = [], today = dayKey(new Date());
    var n = range === "7d" ? 7 : range === "30d" ? 30 : 0;
    if (!n) { var first = days && days.length ? days[0].day : today; n = Math.min(90, Math.max(7, Math.round((Date.parse(today) - Date.parse(first)) / 864e5) + 1)); }
    for (var i = n - 1; i >= 0; i--) keys.push(dayKey(new Date(Date.now() - i * 864e5)));
    return keys;
  }
  function spendDayLabel(k) { return fmtTime(k + "T12:00:00Z", { month: "short", day: "numeric", weekday: "short" }); }
  function spendChart(days, range, sel) {
    var by = {}, today = dayKey(new Date());
    days.forEach(function (x) { by[x.day] = x.usd; });
    var keys = spendChartKeys(range, days), n = keys.length;
    if (keys.indexOf(sel) < 0) sel = "";
    var mx = Math.max.apply(null, keys.map(function (k) { return by[k] || 0; }).concat([0]));
    var top = 0;
    if (mx > 0) { var e = Math.pow(10, Math.floor(Math.log10(mx))), f = mx / e; top = (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * e; }
    var dec = top / 2 >= 1 && (top / 2) % 1 === 0 ? 0 : top < 0.02 ? 4 : 2, step = n <= 7 ? 1 : n <= 31 ? 5 : 10;
    var stop = sel || today; // the one bar reachable with Tab
    var cols = keys.map(function (k, idx) {
      var v = by[k] || 0, lab = (n - 1 - idx) % step === 0, on = k === sel;
      var tipTxt = spendDayLabel(k) + " · " + (v ? usd(v, 4) : T("sp.no_spend_day"));
      return '<button type="button" class="spc-col' + (k === today ? " today" : "") + (v ? "" : " zero") + (on ? " on" : "") + '" data-day="' + esc(k) + '" aria-pressed="' + on + '" tabindex="' + (k === stop ? 0 : -1) + '" aria-label="' + esc(tipTxt) + '" data-tip="' + esc(tipTxt + "\n" + T(on ? "sp.day_unpick" : "sp.day_pick")) + '"><span class="spc-bar" data-h="' + (top ? v / top * 100 : 0) + '"></span><small' + (lab || on ? "" : ' class="off"') + ' aria-hidden="true">' + esc(k.slice(5).replace("-", "/")) + "</small></button>";
    }).join("");
    return '<div class="spc' + (n > 31 ? " dense" : "") + '"><div class="spc-y" aria-hidden="true"><span>' + esc(top ? usd(top, dec) : "") + "</span><span>" + esc(top ? usd(top / 2, dec) : "") + "</span><span>" + esc(usd(0, 0)) + '</span></div><div class="spc-plot' + (sel ? " has-sel" : "") + '" role="group" aria-label="' + esc(T("sp.chart_aria", { n: n })) + '">' + cols + (top ? "" : '<p class="spc-empty">' + esc(T("sp.no_spend_range")) + "</p>") + "</div></div>";
  }
  // #/spend?range=30d&by=model&day=2026-10-22 — defaults are left out of the address.
  function spendHash(v) {
    var q = [];
    if (v.range !== "7d") q.push("range=" + enc(v.range));
    if (v.by !== "task") q.push("by=" + enc(v.by));
    if (v.day) q.push("day=" + enc(v.day));
    return "#/spend" + (q.length ? "?" + q.join("&") : "");
  }
  function spendDayOk(range, day) {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(day || "") || day > dayKey(new Date())) return false;
    return range === "all" || day >= spendChartKeys(range, [])[0];
  }
  function spendQuery(v) { return "range=" + enc(v.range) + "&by=" + enc(v.by) + (v.day ? "&day=" + enc(v.day) : ""); }
  function renderSpend(params) {
    spendCache = {};
    // The address wins; plain #/spend keeps the last range and tab. A picked day only comes
    // from the address (a stale day from an earlier visit would be confusing).
    params = params || new URLSearchParams();
    if (SPEND_RANGES.indexOf(params.get("range")) >= 0) spendView.range = params.get("range");
    if (SPEND_BYS.indexOf(params.get("by")) >= 0) spendView.by = params.get("by");
    spendView.day = spendDayOk(spendView.range, params.get("day")) ? params.get("day") : "";
    if (location.hash.indexOf("#/spend") === 0 && location.hash !== spendHash(spendView)) { history.replaceState(null, "", spendHash(spendView)); lastHash = location.hash; }
    var spendP = api("/api/spend"); // start all while the route's cache window is open
    var bkKey = spendQuery(spendView);
    var bkP = api("/api/spend/breakdown?" + bkKey).then(function (r) { spendCache[bkKey] = r; }, function () {});
    // "By tag" only appears once some task has a tag (or the address asks for it).
    var hasTags = spendView.by === "tag", hasBypass = false;
    return api("/api/tasks").then(function (ts) { (ts.tasks || ts || []).forEach(function (t) { taskNames[t.id] = t.name; if(t.cost_control_mode === "bypass") hasBypass = true; if ((t.tags || []).length) hasTags = true; }); }, function () {}).then(function () { return bkP; }).then(function () { return spendP; }).then(function (d) {
      var s = d.summary, budgets = (s.budgets || []).filter(function (b) { return b.scope === "task" || (b.scope === "global" && b.period === "day"); });
      var global = budgets.filter(function (b) { return b.scope === "global" && b.period === "day"; })[0] || { max_usd: 0 };
      var runsToday = d.top_tasks.reduce(function (a, t) { return a + t.runs; }, 0);
      var byDay = {}, days = [];
      d.days.forEach(function (x) { byDay[x.day] = x; });
      for (var i = 13; i >= 0; i--) { var k = dayKey(new Date(Date.now() - i * 864e5)); days.push({ day: k, usd: (byDay[k] || {}).usd || 0 }); }
      var mx = Math.max.apply(null, days.map(function (x) { return x.usd; }).concat([0.0001]));
      var pct = function (u, m) { return m ? Math.min(100, u / m * 100) : 0; };
      var cls = function (p) { return p >= 100 ? " bad" : p >= 80 ? " warn" : ""; };
      var gp = pct(s.total_usd, global.max_usd);
      mount((hasBypass ? '<div class="notice warn">' + esc(T("costmode.spend_notice")) + '</div>' : "") + '<div class="page-head"><div><h1>' + esc(T("nav.spend")) + "</h1><p>" + esc(T("sp.sub", { tz: s.timezone || state.tz })) + "</p></div></div>" +
        '<div class="grid g4">' +
        '<div class="card stat"><small>' + esc(T("time.today")) + "</small><strong>" + esc(usd(s.total_usd, 4)) + "</strong><span>" + esc(global.max_usd ? T("sp.pct_of", { p: Math.round(gp), m: usd(global.max_usd) }) : T("sp.no_cap")) + "</span></div>" +
        '<div class="card stat"' + (global.max_usd && global.held_usd > 0 ? ' data-tip="' + esc(T("sp.remaining_held_tip", { cap: usd(global.max_usd), used: usd(s.total_usd, 4), held: usd(global.held_usd, 4) })) + '"' : "") + "><small>" + esc(T("sp.remaining")) + "</small><strong>" + esc(global.max_usd ? usd(Math.max(0, global.max_usd - s.total_usd - (global.held_usd || 0))) : "∞") + "</strong><span>" + esc(T("sp.global_daily")) + "</span></div>" +
        '<div class="card stat"><small>Tokens</small><strong>' + esc(num(s.total_tokens)) + "</strong><span>" + esc(T("sp.tokens_usage")) + "</span></div>" +
        '<div class="card stat"><small>' + esc(T("col.runs_today")) + "</small><strong>" + esc(runsToday) + "</strong><span>" + esc(plural(d.top_tasks.length, "sp.sources_one", "sp.sources_many")) + "</span></div></div>" +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("receipt") + '</span><h2>' + esc(T("sp.breakdown")) + '</h2><div class="seg sm" id="sp-range" role="radiogroup">' + [["7d", T("sp.r7")], ["30d", T("sp.r30")], ["all", T("sp.rall")]].map(function (x) { return '<button type="button" role="radio" data-r="' + x[0] + '" class="' + (x[0] === spendView.range ? "active" : "") + '" aria-checked="' + (x[0] === spendView.range) + '">' + esc(x[1]) + "</button>"; }).join("") + "</div>" + '<button class="icon-btn" id="sp-export" data-tip="' + esc(T("sp.export_tip")) + '" aria-label="' + esc(T("sp.export")) + '">' + ic("download", "sm") + "</button></div>" +
        '<nav class="tabs" role="tablist">' + [["task", T("sp.by_task")], ["provider_model", T("sp.by_provider_model")], ["model", T("sp.by_model")], ["provider", T("sp.by_provider")]].concat(hasTags ? [["tag", T("sp.by_tag")]] : []).map(function (x) { return '<button role="tab" data-by="' + x[0] + '" class="' + (x[0] === spendView.by ? "active" : "") + '" aria-selected="' + (x[0] === spendView.by) + '">' + esc(x[1]) + "</button>"; }).join("") + '</nav><div id="sp-body" class="sp-body"></div></section>' +
        // Budgets: one aligned row per scope (column heads instead of a label on every select),
        // the cap edits in place. Full width, so the events table below gets room too.
        '<section class="card bud-card"><div class="card-head"><span class="ico">' + ic("wallet") + '</span><h2>' + esc(T("sp.budgets")) + "</h2>" + tip(T("sp.breach_hint")) + '<a class="link" href="#/settings/budgets">' + esc(T("sp.bud_settings")) + "</a></div>" +
          (budgets.length ? '<div class="bud-grid" role="table" aria-label="' + esc(T("sp.budgets")) + '"><div class="bud-head" role="row"><span role="columnheader">' + esc(T("sp.scope")) + '</span><span role="columnheader">' + esc(T("time.today")) + '</span><span role="columnheader" class="right">' + esc(T("sp.used_cap")) + '</span><span role="columnheader">' + esc(T("sp.on_breach")) + '</span><span role="columnheader">' + esc(T("sp.warn_at")) + "</span></div>" + budgets.map(function (b, k) { return budgetRow(b, k); }).join("") + "</div>" : emptyState("wallet", T("sp.no_budgets"), "")) + "</section>" +
        '<section class="card flush sp-events"><div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>" + esc(T("sp.events")) + "</h2></div>" + (d.events.length ? '<table class="table"><thead><tr><th>' + esc(T("sp.when")) + "</th><th>" + esc(T("sp.scope")) + "</th><th>" + esc(T("sp.detail")) + "</th></tr></thead><tbody>" + d.events.map(function (e) { return '<tr><td class="nowrap">' + esc(when(e.at)) + '</td><td><span class="pill bad">' + esc(T("sp.scope_" + e.scope)) + "</span></td><td>" + esc(e.code ? T("be." + e.code, { name: e.name || T("sp.scope_" + e.scope), used: e.used, cap: e.cap, held: e.held, n: e.n }) : e.detail) + "<small>" + esc(T("sp.action", { a: T("act." + e.action) === "act." + e.action ? e.action : T("act." + e.action) })) + "</small></td></tr>"; }).join("") + "</tbody></table>" : emptyState("shield", T("sp.no_breach"), T("sp.no_breach_sub"))) + "</section>",
        "spend", [[T("nav.spend")]]);
      applySizes(page());
      var paintBody = function (r) {
        var rows = r.rows || [], mxd = Math.max.apply(null, (r.days || []).map(function (x) { return x.usd; }).concat([0.0001]));
        var nm = function (x) { return spendView.by === "task" ? (x.key ? (x.name || taskNames[x.key] || x.key) : T("run.adhoc_paren")) : (x.key || T("sp.unknown")); };
        // By tag: the tag is a chip that opens the task list filtered by it (task count in
        // the tooltip); the untagged row is plain text that says what it holds.
        var nameCell = function (x) {
          if (spendView.by !== "tag") return "<strong>" + esc(nm(x)) + "</strong>";
          if (!x.key) return '<strong class="muted" data-tip="' + esc(T("sp.no_tag_tip")) + '">' + esc(T("sp.no_tag")) + "</strong>";
          return '<a class="ttag h' + tagHue(x.key) + '" href="#/tasks?tag=' + enc(x.key) + '" data-tip="' + esc(T(x.tasks === 1 ? "sp.tag_tip_one" : "sp.tag_tip_many", { t: x.key, n: x.tasks || 0 })) + '">' + esc(x.key) + "</a>";
        };
        var dy = r.day || "", hadFocus = document.activeElement && document.activeElement.closest && document.activeElement.closest(".spc-plot");
        var focusDay = hadFocus ? document.activeElement.getAttribute("data-day") : "";
        var head = dy ? '<span class="sp-day" role="status"><span>' + esc(T("sp.day_only", { d: spendDayLabel(dy) })) + '</span><button type="button" class="icon-btn sm" id="sp-day-clear" data-tip="' + esc(T("sp.day_clear")) + '" aria-label="' + esc(T("sp.day_clear")) + '">' + ic("x", "sm") + '</button></span><a class="link sp-day-runs" href="#/runs?from=' + enc(dy) + "&to=" + enc(dy) + '">' + esc(T("sp.day_runs")) + " " + ic("arrow", "sm") + "</a>"
          : '<span class="muted">' + esc(T("sp.since", { d: spendView.range === "all" ? T("sp.rall") : r.since })) + "</span>";
        $("#sp-body").innerHTML = '<div class="sp-total"><strong>' + esc(usd(r.total_usd || 0, 4)) + "</strong>" + (r.estimated_runs ? costBadge("estimate") : "") + " " + head + (r.estimated_runs ? ' <span class="muted">· ' + esc(T("sp.est_runs", { n: r.estimated_runs })) + "</span>" : "") + "</div>" +
          spendChart(r.days || [], spendView.range, dy) +
          (rows.length ? '<table class="table"><thead><tr><th>' + esc(T("sp.col_" + spendView.by)) + "</th><th>" + esc(T("nav.runs")) + "</th><th>Tokens</th><th>" + esc(T("sp.share")) + (spendView.by === "tag" && r.overlap ? tip(T("sp.tag_overlap")) : "") + '</th><th class="right">' + esc(T("col.cost")) + "</th></tr></thead><tbody>" + rows.map(function (x) {
            var sh = r.total_usd ? x.usd / r.total_usd * 100 : 0;
            var tokTip = T("sp.tok_tip", { i: num(x.input_tokens || 0), o: num(x.output_tokens || 0), r: num(x.cache_read_tokens || 0), w: num(x.cache_write_tokens || 0), q: num(x.requests || 0) });
            var badge = x.estimated_runs || x.legacy_runs ? costBadge(x.estimated_runs ? "estimate" : "legacy") : (x.subscription_runs === x.runs ? costBadge("subscription") : "");
            return "<tr><td>" + nameCell(x) + badge + "</td><td>" + esc(x.runs) + '</td><td title="' + esc(tokTip) + '">' + esc(num(x.tokens)) + '<small class="muted">' + esc(T("rd.in_out", { i: num(x.input_tokens || 0), o: num(x.output_tokens || 0) })) + (x.cache_read_tokens ? " · " + esc(T("sp.cache_n", { n: num(x.cache_read_tokens) })) : "") + '</small></td><td><div class="progress thin"><span data-w="' + sh + '"></span></div></td><td class="right"><strong>' + esc(usd(x.usd, 4)) + "</strong></td></tr>";
          }).join("") + "</tbody></table>" : emptyState("receipt", dy ? T("sp.none_day") : T("sp.none_range"), ""));
        applySizes($("#sp-body"));
        var ex = $("#sp-export");
        if (ex) ex.setAttribute("data-tip", dy ? T("sp.export_day_tip", { d: spendDayLabel(dy) }) : T("sp.export_tip"));
        if (focusDay) { var fb = $('#sp-body [data-day="' + focusDay + '"]'); if (fb) fb.focus(); }
      };
      // cached per range+tab: switching back is instant and never flashes a loader
      var load = function () {
        var key = spendQuery(spendView);
        if (location.hash !== spendHash(spendView)) { history.replaceState(null, "", spendHash(spendView)); lastHash = location.hash; }
        if (spendCache[key]) paintBody(spendCache[key]); else if (!$("#sp-body").innerHTML) $("#sp-body").innerHTML = skeleton("list");
        return api("/api/spend/breakdown?" + key).then(function (r) { spendCache[key] = r; if (key === spendQuery(spendView)) paintBody(r); }).catch(function (e) { $("#sp-body").innerHTML = errorState(e.message); });
      };
      // Pick / unpick a day. The bars change at once (no wait, no flash); the table follows
      // when its answer is in (instant when cached).
      var pickDay = function (k) {
        spendView.day = k && k !== spendView.day ? k : "";
        var plot = $("#sp-body .spc-plot");
        if (plot) {
          plot.classList.toggle("has-sel", !!spendView.day);
          $$("[data-day]", plot).forEach(function (b) { var on = b.getAttribute("data-day") === spendView.day; b.classList.toggle("on", on); b.setAttribute("aria-pressed", String(on)); });
        }
        load();
      };
      var body = $("#sp-body");
      body.addEventListener("click", function (e) {
        var b = e.target.closest("[data-day]");
        if (b) { pickDay(b.getAttribute("data-day")); return; }
        if (e.target.closest("#sp-day-clear")) { var cur = spendView.day; pickDay(""); var back = $('#sp-body [data-day="' + cur + '"]'); if (back) back.focus(); }
      });
      body.addEventListener("keydown", function (e) {
        var b = e.target.closest && e.target.closest("[data-day]");
        if (!b) return;
        var all = $$("[data-day]", body), i = all.indexOf(b), j = { ArrowLeft: i - 1, ArrowRight: i + 1, Home: 0, End: all.length - 1 }[e.key];
        if (e.key === "Escape" && spendView.day) { e.preventDefault(); pickDay(""); return; }
        if (j == null) return;
        e.preventDefault();
        j = Math.max(0, Math.min(all.length - 1, j));
        all.forEach(function (x, n) { x.tabIndex = n === j ? 0 : -1; });
        all[j].focus();
      });
      $("#sp-export").addEventListener("click", function () { downloadFile("/api/spend/export?range=" + enc(spendView.range) + (spendView.day ? "&day=" + enc(spendView.day) : "") + "&lang=" + enc(state.lang), "tidebot-spend.csv", $("#sp-export")); });
      $$("[data-r]").forEach(function (b) { b.addEventListener("click", function () { spendView.range = b.getAttribute("data-r"); if (spendView.day && !spendDayOk(spendView.range, spendView.day)) spendView.day = ""; $$("[data-r]").forEach(function (x) { x.classList.toggle("active", x === b); x.setAttribute("aria-checked", x === b); }); load(); }); });
      $$("[data-by]").forEach(function (b) { b.addEventListener("click", function () { spendView.by = b.getAttribute("data-by"); $$("[data-by]").forEach(function (x) { x.classList.toggle("active", x === b); x.setAttribute("aria-selected", x === b); }); load(); }); });
      // Only the field that changed is sent; the server keeps the rest.
      var postBudget = function (k, patch) {
        var b = budgets[k], req = { scope: b.scope, scope_id: b.scope_id, period: b.period, bucket: b.bucket };
        Object.keys(patch).forEach(function (x) { req[x] = patch[x]; });
        return api("/api/budgets", { method: "POST", body: req });
      };
      $$("[data-bact],[data-bwarn]").forEach(function (sel) {
        sel.addEventListener("change", function () {
          var a = sel.hasAttribute("data-bact"), k = +(sel.getAttribute(a ? "data-bact" : "data-bwarn"));
          postBudget(k, a ? { action: sel.value } : { warn_at_pct: +sel.value }).then(function () { budgets[k][a ? "action" : "warn_at_pct"] = a ? sel.value : +sel.value; toast(T("sp.budget_saved")); }).catch(fail);
        });
      });
      $$("[data-bcap]").forEach(function (btn) {
        btn.addEventListener("click", function () {
          var k = +btn.getAttribute("data-bcap");
          editCap(btn, budgets[k], function (v) {
            return postBudget(k, { max_usd: v }).then(function () {
              return refresh().then(function () { var nb = $('[data-bcap="' + k + '"]'); if (nb) nb.focus(); });
            });
          });
        });
      });
      load();
    });
  }

