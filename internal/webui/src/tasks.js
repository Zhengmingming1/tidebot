  // ---------- task tags ----------
  // Group tasks by a few words ("日报", "监控", "客户 A"), as Uptime Kuma tags its monitors
  // (src/components/TagsManager.vue, MonitorList.vue): picked from the ones in use, case
  // doesn't make a new tag, the list search matches tag names and a tag filters the list.
  // No colour picker: each name gets one of six quiet sand-theme hues from a hash, the same
  // on every page and machine.
  var TAG_MAX = 8, TAG_LEN = 24, TAG_ROW = 2;
  function tagHue(name) {
    // FNV-1a: a multiplier like 31 is 1 mod 6, which would make the hue the sum of the codes.
    var h = 2166136261, k = String(name).toLowerCase();
    for (var i = 0; i < k.length; i++) { h ^= k.charCodeAt(i); h = Math.imul(h, 16777619) >>> 0; }
    return (h >>> 8) % 6;
  }
  function tagChip(name, attrs) { return '<span class="ttag h' + tagHue(name) + '"' + (attrs || "") + ">" + esc(name) + "</span>"; }
  // tagChipsRow: up to TAG_ROW chips that filter the list, the rest as "+N" with the names in the tooltip.
  function tagChipsRow(tags) {
    if (!tags || !tags.length) return "";
    var shown = tags.slice(0, TAG_ROW), rest = tags.slice(TAG_ROW);
    return '<span class="ttags">' + shown.map(function (g) { return '<button type="button" class="ttag h' + tagHue(g) + '" data-tagf="' + esc(g) + '" data-tip="' + esc(T("tg.filter_tip", { t: g })) + '">' + esc(g) + "</button>"; }).join("") +
      (rest.length ? '<span class="ttag more" data-tip="' + esc(rest.join(T("list.sep"))) + '">+' + rest.length + "</span>" : "") + "</span>";
  }
  function cleanTag(raw) { return String(raw || "").trim().replace(/^[#＃]+/, "").replace(/\s+/g, " ").replace(/[,，]/g, "").slice(0, TAG_LEN).trim(); }
  // tagInput: chips + a text box. Enter, comma or Tab (with text) adds; Backspace on an empty
  // box removes the last; × removes one; tags already in use are offered below (filtered by
  // what is typed, click to add). Input-method composition is left alone.
  function tagInput(host, initial, inUse) {
    var tags = (initial || []).slice(), inp, box, sug;
    host.innerHTML = '<div class="tag-in" id="tg-box"><input id="tg-inp" maxlength="' + TAG_LEN + '" autocomplete="off" spellcheck="false" aria-label="' + esc(T("tg.label")) + '"></div><div class="tag-sugg" id="tg-sugg"></div>';
    box = $("#tg-box", host); inp = $("#tg-inp", host); sug = $("#tg-sugg", host);
    var has = function (t) { return tags.some(function (x) { return x.toLowerCase() === t.toLowerCase(); }); };
    var canon = function (t) { var u = (inUse || []).filter(function (x) { return x.tag.toLowerCase() === t.toLowerCase(); })[0]; return u ? u.tag : t; };
    function paint() {
      $$(".ttag", box).forEach(function (c) { c.remove(); });
      inp.insertAdjacentHTML("beforebegin", tags.map(function (g, i) { return '<span class="ttag h' + tagHue(g) + '">' + esc(g) + '<button type="button" class="x" data-rm="' + i + '" aria-label="' + esc(T("tg.remove", { t: g })) + '">×</button></span>'; }).join(""));
      inp.placeholder = tags.length ? "" : T("tg.ph");
      inp.disabled = tags.length >= TAG_MAX;
      var q = inp.value.trim().toLowerCase();
      var list = (inUse || []).filter(function (u) { return !has(u.tag) && (!q || u.tag.toLowerCase().indexOf(q) >= 0); }).slice(0, 8);
      sug.innerHTML = list.length && tags.length < TAG_MAX ? '<span class="muted">' + esc(T("tg.in_use")) + "</span>" + list.map(function (u) { return '<button type="button" class="ttag h' + tagHue(u.tag) + '" data-add="' + esc(u.tag) + '" data-tip="' + esc(T("tg.n_tasks", { n: u.n })) + '">+ ' + esc(u.tag) + "</button>"; }).join("") : "";
    }
    function add(raw) {
      var t = cleanTag(raw);
      if (!t || has(t) || tags.length >= TAG_MAX) return false;
      tags.push(canon(t)); return true;
    }
    box.addEventListener("click", function (e) {
      var rm = e.target.closest("[data-rm]");
      if (rm) { tags.splice(+rm.getAttribute("data-rm"), 1); paint(); inp.focus(); return; }
      if (e.target === box) inp.focus();
    });
    sug.addEventListener("click", function (e) { var a = e.target.closest("[data-add]"); if (a && add(a.getAttribute("data-add"))) { paint(); if (!inp.disabled) inp.focus(); } });
    inp.addEventListener("keydown", function (e) {
      if (e.isComposing || e.keyCode === 229) return;
      if ((e.key === "Enter" || e.key === "," || e.key === "，" || (e.key === "Tab" && inp.value.trim())) && inp.value.trim()) {
        e.preventDefault(); add(inp.value); inp.value = ""; paint(); inp.focus();
      } else if (e.key === "Enter") e.preventDefault(); // never submits the form from here
      else if (e.key === "Backspace" && !inp.value && tags.length) { tags.pop(); paint(); }
    });
    inp.addEventListener("input", function () { if (/[,，]/.test(inp.value)) { inp.value.split(/[,，]/).forEach(add); inp.value = ""; } paint(); });
    inp.addEventListener("blur", function () { if (inp.value.trim() && add(inp.value)) { inp.value = ""; paint(); } });
    paint();
    // value(): the tags, with whatever is still typed in the box.
    return { value: function () { if (inp.value.trim()) add(inp.value); return tags.slice(); } };
  }
  // tagBatchModal: add a tag to the selected tasks, or take one off them.
  function tagBatchModal(ids, inUse, done) {
    modal('<div class="card-head"><span class="ico">' + ic("tag") + "</span><h2>" + esc(T("tg.batch_title", { n: ids.length })) + "</h2></div>" +
      '<form id="tgb"><label class="field"><span>' + esc(T("tg.label")) + '</span><input name="tag" maxlength="' + TAG_LEN + '" autocomplete="off" spellcheck="false" required placeholder="' + esc(T("tg.one_ph")) + '"></label><div class="tag-sugg" id="tgb-sugg"></div>' +
      '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn" id="tgb-rm">' + esc(T("tg.batch_remove")) + '</button><button class="btn primary" id="tgb-add">' + ic("plus", "sm") + esc(T("tg.batch_add")) + "</button></div></form>", function (root, close) {
      var f = $("#tgb", root), sug = $("#tgb-sugg", root);
      var paint = function () {
        var q = f.tag.value.trim().toLowerCase(), l = inUse.filter(function (u) { return !q || u.tag.toLowerCase().indexOf(q) >= 0; }).slice(0, 10);
        sug.innerHTML = l.length ? '<span class="muted">' + esc(T("tg.in_use")) + "</span>" + l.map(function (u) { return '<button type="button" class="ttag h' + tagHue(u.tag) + '" data-pick="' + esc(u.tag) + '" data-tip="' + esc(T("tg.n_tasks", { n: u.n })) + '">' + esc(u.tag) + "</button>"; }).join("") : "";
      };
      sug.addEventListener("click", function (e) { var b = e.target.closest("[data-pick]"); if (b) { f.tag.value = b.getAttribute("data-pick"); paint(); f.tag.focus(); } });
      f.tag.addEventListener("input", paint);
      var go = function (act) {
        var t = cleanTag(f.tag.value);
        if (!t) { f.tag.focus(); return; }
        $$(".actions .btn", root).forEach(function (b) { b.disabled = true; });
        api("/api/tasks/batch", { method: "POST", body: { ids: ids, action: act, tag: t } }).then(function (r) {
          close();
          if (r.failed && r.failed.length) toast(T("tl.batch_partial", { n: r.done, f: r.failed.length }), true);
          else toast(T(act === "add_tag" ? "tg.added" : "tg.removed", { t: t, n: r.done }));
          done();
        }).catch(function (e) { $$(".actions .btn", root).forEach(function (b) { b.disabled = false; }); fail(e); });
      };
      f.addEventListener("submit", function (e) { e.preventDefault(); go("add_tag"); });
      $("#tgb-rm", root).addEventListener("click", function () { go("remove_tag"); });
      paint();
      setTimeout(function () { f.tag.focus(); }, 0);
    });
  }

  // ---------- TASKS ----------
  var taskView = { costMode: "", q: "", status: "", skill: "", tag: "", sort: "name", dir: 1, sel: {} };
  // TASK_FILTERS: [value, label key, tooltip key]. "failing" = the newest run with a result failed.
  var TASK_FILTERS = [["", "runs.all_f"], ["active", "task.active"], ["paused", "task.paused"], ["failing", "tl.f_failing", "tl.f_failing_tip"]];
  function taskMatchesStatus(t, st) { return !st || (st === "failing" ? !!t.failing : t.status === st); }
  function tasksHash(st) {
    var q = [];
    if (st) q.push("status=" + enc(st));
    if (taskView.tag) q.push("tag=" + enc(taskView.tag));
    return "#/tasks" + (q.length ? "?" + q.join("&") : "");
  }
  function renderTasks(params) {
    var want = params && params.get("status");
    if (want != null) taskView.status = TASK_FILTERS.some(function (x) { return x[0] === want; }) ? want : "";
    var wantTag = params && params.get("tag");
    if (wantTag != null) taskView.tag = cleanTag(wantTag);
    return api("/api/tasks").then(function (tasks) {
      var v = taskView, skills = {}, tagN = {};
      tasks.forEach(function (t) { (t.tags || []).forEach(function (g) { tagN[g] = (tagN[g] || 0) + 1; }); });
      var tagList = Object.keys(tagN).sort(function (a, b) { return tagN[b] - tagN[a] || (a < b ? -1 : 1); });
      if (v.tag && !tagN[v.tag]) v.tag = "";
      // The address shows the filter, so a link or reload opens the same view; replaceState
      // changes it without a hashchange (no refetch, no repaint).
      if (location.hash.indexOf("#/tasks") === 0 && location.hash !== tasksHash(v.status)) history.replaceState(null, "", tasksHash(v.status));
      tasks.forEach(function (t) { (t.skills || []).forEach(function (k) { skills[k] = 1; }); });
      v.sel = {};
      var sorters = {
        name: function (t) { return t.name.toLowerCase(); }, schedule: function (t) { return t.next_run_at || "~"; }, status: function (t) { return t.status; },
        version: function (t) { return t.version_no; }, last: function (t) { return t.last_run_at || ""; }, today: function (t) { return t.runs_today; }
      };
      var th = function (k, label) { return '<th><button class="th-sort' + (v.sort === k ? " on" : "") + '" data-sort="' + k + '" aria-sort="' + (v.sort === k ? (v.dir > 0 ? "ascending" : "descending") : "none") + '">' + esc(label) + (v.sort === k ? '<span class="arr">' + (v.dir > 0 ? "↑" : "↓") + "</span>" : "") + "</button></th>"; };
      mount('<div class="page-head"><div><h1>' + esc(T("nav.tasks")) + "</h1><p>" + esc(plural(tasks.length, "tasks.count_one", "tasks.count_many")) + '</p></div><div class="actions"><button class="btn ghost" id="t-import" data-tip="' + esc(T("tx.import_tip")) + '">' + ic("upload", "sm") + esc(T("tx.import")) + '</button><button class="btn ghost" id="t-export" data-tip="' + esc(T("tx.export_tip")) + '"' + (tasks.length ? "" : " disabled") + ">" + ic("download", "sm") + esc(T("tx.export")) + '</button><button class="btn primary" id="t-new">' + ic("plus", "sm") + esc(T("tf.new")) + "</button></div></div>" +
        '<section class="card flush"><div class="toolbar task-tb"><label class="search-in">' + ic("search", "sm") + '<input id="tq" type="search" maxlength="100" placeholder="' + esc(T("tl.search")) + '" value="' + esc(v.q) + '">' + KB_SLASH + '</label>' +
        '<div class="filters task-f" role="group">' + TASK_FILTERS.map(function (x) { return '<button data-st="' + x[0] + '" aria-pressed="' + (x[0] === v.status) + '" class="' + (x[0] === v.status ? "active" : "") + '"' + (x[2] ? ' data-tip="' + esc(T(x[2])) + '"' : "") + ">" + esc(T(x[1])) + ' <span class="cnt" data-c="' + x[0] + '"></span></button>'; }).join("") + "</div>" +
        '<select id="tcost" aria-label="' + esc(T("costmode.title")) + '"><option value="">' + esc(T("costmode.all")) + '</option>' + ['task','global','bypass'].map(function(m){return opt(m,costModeLabel(m),v.costMode===m);}).join('') + '</select>' +
        '<select id="tsk" aria-label="' + esc(T("bt.skill")) + '"><option value="">' + esc(T("tl.any_skill")) + "</option>" + Object.keys(skills).sort().map(function (k) { return opt(k, k, k === v.skill); }).join("") + "</select>" +
        (tagList.length ? '<select id="ttag" aria-label="' + esc(T("tg.label")) + '"><option value="">' + esc(T("tg.any")) + "</option>" + tagList.map(function (g) { return opt(g, g + " · " + tagN[g], g === v.tag); }).join("") + '</select><a class="icon-btn sm" id="tgm-link" href="#/settings/tags" data-tip="' + esc(T("tgm.manage")) + '" aria-label="' + esc(T("tgm.manage")) + '">' + ic("settings", "sm") + "</a>" : "") +
        '<span class="grow"></span><span class="muted" id="tcount"></span></div>' +
        '<div class="batchbar hidden" id="tbatch"><strong id="tbn"></strong><button class="btn sm" data-batch="pause">' + ic("pause", "sm") + esc(T("td.pause")) + '</button><button class="btn sm" data-batch="resume">' + ic("play", "sm") + esc(T("td.resume")) + '</button><button class="btn sm" id="tb-tag">' + ic("tag", "sm") + esc(T("tg.batch")) + '</button><button class="btn sm" id="tb-export" data-tip="' + esc(T("tx.export_tip")) + '">' + ic("download", "sm") + esc(T("tx.export")) + '</button><button class="btn sm danger" data-batch="delete">' + ic("trash", "sm") + esc(T("td.delete")) + '</button><button class="btn sm ghost" id="tclear">' + esc(T("tl.clear")) + "</button></div>" +
        '<table class="table"><thead><tr><th class="ck"><input type="checkbox" id="tall" aria-label="' + esc(T("tl.select_all")) + '"></th>' + th("name", T("col.task")) + th("schedule", T("col.schedule")) + th("status", T("col.status")) + th("version", T("col.version")).replace("<th>", '<th class="c-opt">') + th("last", T("col.recent_runs")) + th("today", T("col.runs_today")).replace("<th>", '<th class="c-opt">') + '<th class="c-act"></th></tr></thead><tbody id="tbody"></tbody></table></section>', "tasks", [[T("nav.tasks")]]);
      var body = $("#tbody");
      function rows() {
        var q = v.q.trim().toLowerCase(), key = sorters[v.sort];
        // Counts on the chips follow the search and skill filter, not the chip itself (like the runs list).
        var base = tasks.filter(function (t) {
          return (!v.costMode || (t.cost_control_mode || "task") === v.costMode) && (!v.skill || (t.skills || []).indexOf(v.skill) >= 0) && (!v.tag || (t.tags || []).indexOf(v.tag) >= 0) && (!q || (t.name + " " + (t.goal || "") + " " + t.prompt + " " + (t.tags || []).join(" ")).toLowerCase().indexOf(q) >= 0);
        });
        TASK_FILTERS.forEach(function (x) {
          var n = base.filter(function (t) { return taskMatchesStatus(t, x[0]); }).length, c = $('.task-f [data-c="' + x[0] + '"]');
          if (c) { c.textContent = num(n); c.classList.toggle("bad", x[0] === "failing" && n > 0); }
        });
        var list = base.filter(function (t) { return taskMatchesStatus(t, v.status); }).sort(function (a, b) { var x = key(a), y = key(b); return (x < y ? -1 : x > y ? 1 : 0) * v.dir; });
        $("#tcount").textContent = T("tl.showing", { n: list.length, total: tasks.length });
        body.innerHTML = list.length ? list.map(function (t) {
          return '<tr class="click" data-href="#/tasks/' + enc(t.id) + '"><td class="ck"><input type="checkbox" data-pick="' + esc(t.id) + '"' + (v.sel[t.id] ? " checked" : "") + ' aria-label="' + esc(t.name) + '"></td><td class="tl-name"><div class="cell-main"><span class="ico" data-hue="' + iconHue(runIcon({ task_name: t.name })) + '">' + ic(runIcon({ task_name: t.name }), "sm") + '</span><div><div class="tl-line"><a class="row-link" href="#/tasks/' + enc(t.id) + '"><strong>' + esc(t.name) + "</strong></a>" + costModeBadge(t.cost_control_mode) + tagChipsRow(t.tags) + "</div><small class=\"clamp1\">" + esc(t.goal || t.prompt) + "</small></div></div></td>" +
            '<td class="nowrap">' + esc(cronHuman(t.cron)) + (t.next_run_at && t.status === "active" ? "<small>" + heldNext(t.next_run_at) + ((t.skip_runs || []).length ? '<span class="nx-skip" data-tip="' + esc(T("skip.chip_tip", { l: t.skip_runs.map(when).join(T("list.sep")) })) + '">' + ic("skipFwd", "sm") + "</span>" : "") + "</small>" : pauseKind(t) === "snooze" ? "<small>" + esc(T("tpz.wakes", { t: when(t.pause_detail) })) + "</small>" : "") + "</td><td>" + taskPill(t.status, t) + '</td><td class="c-opt"><span class="tag">v' + esc(t.version_no) + "</span></td><td class=\"tl-last\">" + taskBeatsCell(t) + '</td><td class="num c-opt">' + esc(t.runs_today) + '</td><td class="right c-act"><button type="button" class="icon-btn sm row-act" data-runnow="' + esc(t.id) + '" title="' + esc(T("td.run_now")) + '" aria-label="' + esc(T("td.run_now") + " · " + t.name) + '">' + ic("play", "sm") + "</button></td></tr>";
        }).join("") : '<tr><td colspan="8">' + (tasks.length ? (v.status === "failing" && !q ? emptyState("checkc", T("tl.no_failing"), T("tl.no_failing_sub")) : emptyState("search", T("tl.no_match"), T("tl.no_match_sub"))) : emptyState("tasks", T("tasks.none"), T("tasks.none_sub"))) + "</td></tr>";
        wireRows(body);
        $$("[data-runnow]", body).forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); runTaskNow(b.getAttribute("data-runnow"), b); }); });
        $$("[data-tagf]", body).forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); setTag(b.getAttribute("data-tagf")); }); });
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
      imeInput($("#tq"), 120, function (val) { v.q = val; rows(); });
      $$("[data-st]").forEach(function (b) { b.addEventListener("click", function () {
        v.status = b.getAttribute("data-st");
        $$("[data-st]").forEach(function (x) { x.classList.toggle("active", x === b); x.setAttribute("aria-pressed", String(x === b)); });
        history.replaceState(null, "", tasksHash(v.status));
        rows();
      }); });
      $("#tcost").addEventListener("change", function(e){v.costMode=e.target.value;rows();});
      $("#tsk").addEventListener("change", function (e) { v.skill = e.target.value; rows(); });
      // A tag chip in a row or the tag menu filters the list; the address keeps it.
      function setTag(g) {
        v.tag = v.tag === g ? "" : g;
        if ($("#ttag")) $("#ttag").value = v.tag;
        history.replaceState(null, "", tasksHash(v.status));
        rows();
      }
      if ($("#ttag")) $("#ttag").addEventListener("change", function (e) { v.tag = ""; setTag(e.target.value); });
      $("#tb-tag").addEventListener("click", function () { tagBatchModal(Object.keys(v.sel), tagList.map(function (g) { return { tag: g, n: tagN[g] }; }), function () { v.sel = {}; refresh(); }); });
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
      $("#t-import").addEventListener("click", function () { taskImportModal(refresh); });
      $("#t-export").addEventListener("click", function (e) { exportTasks([], e.currentTarget); });
      $("#tb-export").addEventListener("click", function (e) { exportTasks(Object.keys(v.sel), e.currentTarget); });
      rows();
      // Follow changes: only the rows repaint (search text, filters, sort, ticks and scroll stay);
      // a new skill or tag for the menus redraws the page, unless you're using the toolbar or
      // have rows ticked. Checks every 2 s with the change stream up (free); without it only
      // while a task's newest run is going, like the runs list.
      var menus = function (ts) {
        var sk = {}, tg = {};
        ts.forEach(function (t) { (t.skills || []).forEach(function (k) { sk[k] = 1; }); (t.tags || []).forEach(function (g) { tg[g] = (tg[g] || 0) + 1; }); });
        return JSON.stringify([Object.keys(sk).sort(), tg]);
      };
      var going = function (ts) { return ts.some(function (t) { var b = t.beats || [], l = b[b.length - 1]; return !!l && (l.status === "running" || l.status === "queued"); }); };
      var check = liveFollow(function () { return apiRaw("/api/tasks"); }, tasks, function (d) {
        if (!$("#tbody")) return;
        var pg = page();
        if (menus(d) !== menus(tasks) && !Object.keys(v.sel).length && liveQuiet(pg)) { swrPut("/api/tasks", d); refresh(); return; }
        var ids = {};
        d.forEach(function (t) { ids[t.id] = 1; });
        Object.keys(v.sel).forEach(function (k) { if (!ids[k]) delete v.sel[k]; });
        tasks = d;
        var p = $(".page-head p", pg); if (p) p.textContent = plural(tasks.length, "tasks.count_one", "tasks.count_many");
        keepFocus(body, rows);
        $("#t-export").disabled = !tasks.length;
      }, function () {
        // The rows hold no text you type: only dialogs, menus and selected text hold a repaint back.
        if ($("#modal-root").innerHTML || $("#drawer") || $(".popover[role=menu]") || !document.contains(body)) return false;
        var sl = window.getSelection ? window.getSelection() : null;
        return !(sl && !sl.isCollapsed && sl.anchorNode && body.contains(sl.anchorNode));
      });
      livePoll(function () { return evsLive() || going(tasks) ? check() : null; }, 2000, function (h) { return /^#\/tasks(\?|$)/.test(h); });
    });
  }
  // ---------- task export / import ----------
  // One JSON file per export: what the tasks do, never this machine's folders, history,
  // tokens or keys. Import checks first (nothing written), shows what changes, then
  // creates the ticked rows — paused unless asked otherwise.
  function exportTasks(ids, btn) {
    return downloadFile("/api/tasks/export" + (ids.length ? "?ids=" + ids.map(enc).join(",") : ""), "tidebot-tasks.json", btn);
  }
  var TX_NOTES = { renamed: "tx.n_renamed", provider_missing: "tx.n_provider", skill_missing: "tx.n_skill" };
  function taskImportModal(done) {
    modal('<div class="card-head"><span class="ico">' + ic("upload") + "</span><h2>" + esc(T("tx.title")) + "</h2></div>" +
      '<div class="drop" id="tx-drop">' + ic("upload", "lg") + "<strong>" + esc(T("tx.drop")) + "</strong><small>" + esc(T("tx.drop_sub")) + '</small><div class="row-actions"><label class="btn sm">' + esc(T("up.pick_file")) + '<input type="file" class="hidden" id="tx-file" accept=".json,application/json" data-noguard></label></div></div>' +
      '<div id="tx-prev"></div><div class="actions" id="tx-foot"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button></div>", function (root, close) {
      var prev = $("#tx-prev", root), foot = $("#tx-foot", root), z = $("#tx-drop", root), doc = null, rows = [];
      var load = function (text) {
        var d = null;
        try { d = JSON.parse(String(text).replace(/^\uFEFF/, "")); } catch (e) { d = null; }
        if (!d || typeof d !== "object" || d.kind !== "tidebot.tasks") { toast(T("tx.not_file"), true); return; }
        api("/api/tasks/import", { method: "POST", body: { file: d } }).then(function (r) { doc = d; rows = r.tasks || []; show(r); }).catch(fail);
      };
      var readFile = function (file) {
        if (!file) return;
        if (file.size > 4 * 1048576) { toast(T("tx.too_big"), true); return; }
        var fr = new FileReader();
        fr.onload = function () { load(fr.result); };
        fr.onerror = function () { toast(T("tx.not_file"), true); };
        fr.readAsText(file);
      };
      ["dragenter", "dragover"].forEach(function (ev) { z.addEventListener(ev, function (e) { e.preventDefault(); z.classList.add("over"); }); });
      z.addEventListener("dragleave", function () { z.classList.remove("over"); });
      z.addEventListener("drop", function (e) { e.preventDefault(); z.classList.remove("over"); readFile(e.dataTransfer.files && e.dataTransfer.files[0]); });
      var inp = $("#tx-file", root);
      inp.addEventListener("change", function () { readFile(inp.files[0]); inp.value = ""; });
      // Pasting the file's text works too (handy over remote desktop / SSH).
      root.addEventListener("paste", function (e) {
        var t = e.clipboardData && e.clipboardData.getData("text");
        if (t && /^\s*\{/.test(t) && !/^(INPUT|TEXTAREA)$/.test((e.target && e.target.tagName) || "")) { e.preventDefault(); load(t); }
      });
      function show(r) {
        var ok = rows.filter(function (x) { return !x.error; }).length;
        z.classList.add("compact");
        prev.innerHTML = '<p class="muted tx-meta" data-tip="' + esc((r.app || "") + (r.exported_at ? " · " + fullTime(r.exported_at) : "")) + '">' + esc(T("tx.found", { n: rows.length, ok: ok })) + "</p>" +
          '<ul class="tx-list">' + rows.map(function (x) {
            var notes = (x.notes || []).indexOf("cost_bypass") >= 0 ? costModeBadge("bypass") : ""; notes += (x.notes || []).map(function (n) { return TX_NOTES[n] ? '<span class="tag sm warn" data-tip="' + esc(T(TX_NOTES[n] + "_tip", { n: x.from || "" })) + '">' + esc(T(TX_NOTES[n])) + "</span>" : ""; }).join("");
            return '<li class="' + (x.error ? "bad" : "") + '"><label class="check"><input type="checkbox" data-noguard data-tx="' + x.index + '"' + (x.error ? " disabled" : " checked") + ' aria-label="' + esc(x.name) + '"></label><div class="tx-main"><div class="tx-name"><strong class="ell">' + esc(x.name) + "</strong>" + notes + "</div>" +
              '<small class="ell">' + (x.error ? esc(T("tx.cant", { e: x.error })) : esc((x.cron ? cronHuman(x.cron) + " · " : T("tx.no_cron") + " · ") + (x.prompt || ""))) + "</small></div></li>";
          }).join("") + "</ul>" +
          '<label class="check tx-enable"><input type="checkbox" id="tx-en" data-noguard> ' + esc(T("tx.enable")) + "</label>";
        foot.innerHTML = '<button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn primary" id="tx-go"></button>';
        $$("[data-close]", foot).forEach(function (b) { b.addEventListener("click", close); });
        var go = $("#tx-go", foot);
        var picked = function () { return $$("[data-tx]", prev).filter(function (c) { return c.checked && !c.disabled; }).map(function (c) { return +c.getAttribute("data-tx"); }); };
        var count = function () { var n = picked().length; go.textContent = T("tx.go", { n: n }); go.disabled = !n; };
        $$("[data-tx]", prev).forEach(function (c) { c.addEventListener("change", count); });
        count();
        go.addEventListener("click", async function () {
          var pick = picked(), on = $("#tx-en", prev).checked;
          if (pick.some(function(i){return doc.tasks[i].cost_control && doc.tasks[i].cost_control.mode === "bypass";}) && !await confirmCostBypass(root,pick.map(function(i){return doc.tasks[i].name;}).join(", "))) return;
          go.disabled = true;
          api("/api/tasks/import", { method: "POST", body: { file: doc, apply: true, pick: pick, enable: on } }).then(function (res) {
            var bad = (res.tasks || []).filter(function (x) { return x.error && pick.indexOf(x.index) >= 0; }).length;
            close();
            toast(T(on ? "tx.done_on" : "tx.done", { n: res.created }) + (bad ? " · " + T("tx.some_failed", { n: bad }) : ""), !!bad);
            done();
          }).catch(function (e) { go.disabled = false; fail(e); });
        });
        go.focus();
      }
    });
  }
