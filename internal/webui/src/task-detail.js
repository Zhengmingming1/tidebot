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
        '<div class="msg"><span class="avatar bot">' + tideMark() + '</span><div><div class="msg-meta"><strong>tidebot</strong><small>' + agoHtml(g.at) + '</small></div><div class="msg-body">' + esc(reply) + "</div>" +
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
        '<div class="task-composer-wrap"><div class="task-draft-row">' + draftTagHtml() + '</div><form class="composer" id="composer"><input name="u" placeholder="' + esc(T("thread.composer")) + '" autocomplete="off" maxlength="2000" aria-label="' + esc(T("thread.amendment")) + '"><button class="btn primary sm" type="submit" aria-label="' + esc(T("thread.send")) + '">' + ic("send", "sm") + "</button></form></div>";
    }
    if (tab === "overview") {
      return '<div class="thread"><dl class="kv ov">' +
        "<dt>" + esc(T("f.prompt")) + '</dt><dd><div class="inline-edit" id="ie-prompt" tabindex="0" role="button" aria-label="' + esc(T("ie.edit_prompt")) + '" title="' + esc(T("ie.hint")) + '">' + esc(tk.prompt) + "</div></dd>" +
        (tk.goal && tk.goal !== tk.prompt ? "<dt>" + esc(T("td.goal")) + "</dt><dd>" + esc(tk.goal) + "</dd>" : "") +
        "<dt>" + esc(T("td.requirements")) + "</dt><dd>" + (tk.constraints.length ? '<ul class="list-plain">' + tk.constraints.map(function (c) { return "<li>" + esc(c) + "</li>"; }).join("") + "</ul>" : '<span class="muted">' + esc(T("td.none_yet")) + "</span>") + "</dd>" +
        "<dt>" + esc(T("col.schedule")) + "</dt><dd>" + esc(cronHuman(tk.cron)) + (tk.cron ? '<span class="ov-next" id="ov-next"></span>' : "") + "</dd>" +
        "<dt>" + esc(T("f.provider")) + "</dt><dd>" + esc(tk.provider || T("td.default")) + "</dd>" +
        "<dt>" + esc(T("nav.skills")) + "</dt><dd>" + (tk.skills.length ? tk.skills.map(esc).join(", ") : '<span class="muted">' + esc(T("td.none")) + "</span>") + "</dd>" +
        "<dt>" + esc(T("td.workspace")) + '</dt><dd class="ws-row">' + (tk.workspace ? '<code class="ws-path">' + esc(tk.workspace) + "</code>" +
          (tk.workspace_default ? '<span class="tag" data-tip="' + esc(T("ws.default_tip")) + '">' + esc(T("ws.default")) + "</span>" : "") +
          '<button type="button" class="icon-btn sm" id="ws-copy" data-tip="' + esc(T("ws.copy")) + '" aria-label="' + esc(T("ws.copy")) + '">' + ic("copy", "sm") + "</button>" +
          '<button type="button" class="icon-btn sm hidden" id="ws-open" aria-label="">' + ic("folder", "sm") + "</button>" : '<span class="muted">—</span>') + "</dd></dl></div>";
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
      el.innerHTML = multi ? '<textarea rows="4" maxlength="32000"></textarea><small class="hint">' + esc(T("ie.keys_multi", { k: keyLabel("Mod+Enter") })) + "</small>" : '<input maxlength="80">';
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
        else if (e.key === "Enter" && (!multi || submitKey(e))) { e.preventDefault(); end(true); }
      });
      inp.addEventListener("blur", function () { end(true); });
    };
    el.addEventListener("click", start);
    el.addEventListener("keydown", function (e) { if (e.key === "Enter" && !el.classList.contains("editing")) { e.preventDefault(); start(); } });
  }
  // The task page's first requests; the intent prefetch asks for exactly these.
  function taskDetailPaths(id, tab) {
    var base = "/api/tasks/" + enc(id);
    return [base, base + "/amendments", base + "/versions", base + "/runs?limit=" + (tab === "runs" ? 50 : 6)];
  }
  // The schedule's "next run" line a live repaint is about to replace: shown again at once (the
  // preview behind it is fetched after the page is drawn), so the line doesn't blink.
  var ovKeep = null;
  // The task rail changes only its own DOM. Collapsing or filtering it must never remount
  // a requirement review, clear checked amendments, or interrupt the composer.
  var taskRailView = { q: "", status: "", open: null, off: null };
  function taskRailTasks(tasks, q, status) {
    q = q.trim().toLowerCase();
    return tasks.filter(function (t) {
      return t.status !== "archived" && taskMatchesStatus(t, status) && (!q ||
        [t.name, t.goal, t.prompt, (t.tags || []).join(" ")].join(" ").toLowerCase().indexOf(q) >= 0);
    });
  }
  function taskWorkspaceHtml() {
    return '<div class="task-workspace-tools"><button type="button" class="btn sm ghost" id="task-rail-toggle" aria-controls="task-rail" aria-expanded="false">' + ic("tasks", "sm") + '<span>' + esc(T("tw.list")) + '</span></button><a class="link" href="#/tasks">' + esc(T("tw.manage")) + " " + ic("chev", "sm") + '</a></div>' +
      '<aside class="task-rail" id="task-rail" aria-label="' + esc(T("tw.list")) + '" hidden><div class="task-rail-head"><h2>' + esc(T("nav.tasks")) + '</h2><button type="button" class="icon-btn sm" id="task-rail-new" aria-label="' + esc(T("tf.new")) + '" data-tip="' + esc(T("tf.new")) + '">' + ic("plus", "sm") + '</button></div><label class="search-in">' + ic("search", "sm") + '<input type="search" id="task-rail-search" maxlength="100" aria-label="' + esc(T("tl.search")) + '" placeholder="' + esc(T("tl.search")) + '" value="' + esc(taskRailView.q) + '"></label><div class="task-rail-filter"><select id="task-rail-filter" aria-label="' + esc(T("tw.filter")) + '">' + TASK_FILTERS.map(function (f) { return opt(f[0], T(f[1]), f[0] === taskRailView.status); }).join("") + '</select><span class="muted" id="task-rail-count" aria-live="polite"></span></div><nav class="task-rail-list" id="task-rail-list" aria-label="' + esc(T("tw.list")) + '"></nav></aside>';
  }
  function wireTaskWorkspace(id, tab) {
    var box = $(".task-workspace"), rail = $("#task-rail", box), toggle = $("#task-rail-toggle", box), search = $("#task-rail-search", box), list = $("#task-rail-list", box);
    var media = window.matchMedia("(min-width: 1280px)"), tasks = null, loading = null, stale = false;
    if (taskRailView.off) taskRailView.off();
    var paint = function () {
      if (!tasks || !document.contains(box)) return;
      var shown = taskRailTasks(tasks, taskRailView.q, taskRailView.status);
      $("#task-rail-count", box).textContent = shown.length + " / " + tasks.length;
      keepFocus(list, function () {
        list.innerHTML = shown.length ? shown.map(function (t) {
          var href = "#/tasks/" + enc(t.id) + "/" + enc(tab);
          return '<a class="task-rail-item' + (t.id === id ? " selected" : "") + '" href="' + href + '" data-href="' + href + '"' + (t.id === id ? ' aria-current="page"' : "") + '><span class="task-rail-icon" aria-hidden="true">' + ic(runIcon({ task_name: t.name }), "sm") + '</span><span class="task-rail-copy"><strong>' + esc(t.name) + '</strong><small>' + esc(cronHuman(t.cron)) + '</small><span class="task-rail-status">' + taskPill(t.status, t, true) + '</span></span></a>';
        }).join("") : '<p class="task-rail-empty">' + esc(T("tl.no_match")) + '</p>';
      });
    };
    var load = function (force) {
      if (loading) return force ? loading.then(function () { return load(true); }) : loading;
      if (tasks && !force) return Promise.resolve();
      if (!tasks) list.innerHTML = skeleton("list");
      loading = (force ? apiRaw : api)("/api/tasks").then(function (data) {
        var changed = JSON.stringify(tasks) !== JSON.stringify(data);
        tasks = data; stale = false; if (changed) paint();
      }).catch(function (e) {
        if (!document.contains(box)) return;
        stale = true;
        if (!tasks) {
          list.innerHTML = '<p class="task-rail-empty">' + esc(T("tw.load_error")) + '</p><button type="button" class="btn sm" id="task-rail-retry">' + esc(T("err.retry")) + '</button>';
          $("#task-rail-retry", box).addEventListener("click", function () { load(false); });
        }
        if (force) throw e; // leave the revision gate open so a transient failure is retried
      }).then(function () { loading = null; }, function (e) { loading = null; throw e; });
      return loading;
    };
    var show = function (focus) {
      var open = taskRailView.open == null ? media.matches : taskRailView.open;
      if (!open && rail.contains(document.activeElement)) toggle.focus();
      box.classList.toggle("rail-open", open); rail.hidden = !open;
      toggle.setAttribute("aria-expanded", String(open));
      toggle.setAttribute("aria-label", T(open ? "tw.hide" : "tw.show"));
      if (open) { load(stale).catch(function () {}); if (focus) search.focus(); }
    };
    toggle.addEventListener("click", function () { taskRailView.open = toggle.getAttribute("aria-expanded") !== "true"; show(true); });
    imeInput(search, 120, function (q) { taskRailView.q = q; paint(); });
    $("#task-rail-filter", box).addEventListener("change", function (e) { taskRailView.status = e.target.value; paint(); });
    $("#task-rail-new", box).addEventListener("click", function () { taskFormModal(null); });
    list.addEventListener("click", function (e) { if (e.target.closest("a") && !media.matches) { taskRailView.open = false; show(false); } });
    var resize = function () { if (document.contains(box)) { taskRailView.open = null; show(false); } else taskRailView.off(); };
    media.addEventListener("change", resize);
    taskRailView.off = function () { media.removeEventListener("change", resize); taskRailView.off = null; };
    show(false);
    // Called by the detail page's existing revision gate, even when only another task changed.
    return function () {
      if (!document.contains(box)) return Promise.resolve();
      if (rail.hidden) { stale = true; return Promise.resolve(); }
      return load(true);
    };
  }
  function renderTaskDetail(id, tab) {
    draftFlush();
    tab = tab || "overview";
    var base = "/api/tasks/" + enc(id);
    return Promise.all(taskDetailPaths(id, tab).map(function (u) { return api(u); })).then(function (r) {
      var shown = JSON.parse(JSON.stringify(r));
      var t = r[0], items = r[1] || [], vers = r[2], runs = r[3] || [];
      var mini = runs.length ? '<ul class="mini-runs">' + runs.map(function (x) {
        var d = x.status === "succeeded" ? "" : (x.status === "running" || x.status === "queued") ? "run" : "bad";
        return '<li data-href="#/runs/' + enc(x.id) + '"><span class="d ' + d + '"></span><div><strong>' + (x.version_no ? "v" + esc(x.version_no) : "#" + esc(shortId(x.id))) + "</strong><small>" + esc(when(x.created_at)) + '</small></div><span class="muted">' + esc(dur(x.duration_ms)) + "</span><strong>" + esc(usd(x.cost_usd, 3)) + "</strong></li>";
      }).join("") + "</ul>" : emptyState("runs", T("runs.none"), "");
      var tabs = [["overview", T("td.tab_overview")], ["thread", T("td.tab_req")], ["runs", T("nav.runs")], ["history", T("td.tab_versions")]];
      mount('<div class="task-workspace">' + taskWorkspaceHtml() + '<div class="task-workspace-detail"><div class="grid g-detail">' +
        '<section class="card task-main-card' + (tab === "thread" ? " task-thread-card" : "") + '"><div class="task-head"><div class="grow-col"><div class="td-title"><h1 class="inline-edit" id="ie-name" tabindex="0" role="button" data-tip="' + esc(T("ie.hint")) + '">' + esc(t.name) + "</h1>" + taskPill(t.status, t) + idChip(T("td.task_id"), t.id) + costModeBadge(t.cost_control_mode) + "</div><p>" + esc(t.goal || t.prompt) + "</p>" +
        '<div class="td-meta">' + ic("clock", "sm") + "<span>" + esc(cronHuman(t.cron)) + "</span><span>·</span><span>" + tAgo("td.created_updated", { c: fmtTime(t.created_at, { month: "short", day: "numeric", year: "numeric" }) }, "u", t.updated_at) + "</span>" +
        ((t.tags || []).length ? '<span class="td-tags">' + ic("tag", "sm") + t.tags.map(function (g) { return '<a class="ttag h' + tagHue(g) + '" href="#/tasks?tag=' + enc(g) + '" data-tip="' + esc(T("tg.filter_tip", { t: g })) + '">' + esc(g) + "</a>"; }).join("") + "</span>" : "") + "</div></div>" +
        '<div class="row-actions">' + (t.status === "active" ? '<button class="btn" id="t-toggle" data-act="pause" aria-haspopup="menu" aria-expanded="false">' + ic("pause", "sm") + esc(T("td.pause")) + ic("chevDown", "sm") + "</button>" : '<button class="btn" id="t-toggle" data-act="resume">' + ic("play", "sm") + esc(T("td.resume")) + "</button>") +
        '<button class="btn" id="t-edit">' + ic("doc", "sm") + esc(T("btn.edit")) + '</button><button class="icon-btn" id="t-dup" data-tip="' + esc(T("td.dup_tip")) + '" aria-label="' + esc(T("td.dup")) + '">' + ic("copy", "sm") + '</button><button class="icon-btn danger-text" id="t-del" data-tip="' + esc(T("td.delete")) + '" aria-label="' + esc(T("td.delete")) + '">' + ic("trash", "sm") + '</button><button class="btn primary" id="t-run">' + ic("play", "sm") + esc(T("td.run_now")) + "</button></div></div>" + pauseNotice(t) +
        '<nav class="tabs">' + tabs.map(function (x) { return '<button data-tab="' + x[0] + '" class="' + (x[0] === tab ? "active" : "") + '">' + esc(x[1]) + (x[0] === "thread" && items.some(function (i) { return i.status === "pending"; }) ? ' <span class="pill info plain">' + items.filter(function (i) { return i.status === "pending"; }).length + "</span>" : "") + "</button>"; }).join("") + "</nav>" +
        taskTab(tab, t, items, vers, runs) + "</section>" +
        '<div class="stack task-secondary">' + '<section class="card"><h3>' + esc(T("costmode.title")) + '</h3>' + costModeBadge(t.cost_control_mode) + '<p>' + esc(T("costmode." + (t.cost_control_mode === "bypass" ? "warning" : "global_hint"))) + '</p><p class="hint">' + esc(T("costmode.next_start")) + '</p></section>' + '<section class="card"><div class="card-head"><span class="ico">' + ic("branch") + '</span><h3>' + esc(T("ver.tree")) + '</h3><span class="link muted">' + esc(T("ver.n_current", { n: t.version_no })) + "</span></div>" + versionTree(vers.versions, vers.current) + '<p class="hint mt">' + esc(T("ver.hint")) + "</p></section>" +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("runs") + '</span><h3>' + esc(T("home.recent")) + '</h3><a class="link" href="#/runs?task=' + enc(t.id) + '">' + esc(T("td.view_all")) + "</a></div>" + mini + "</section></div></div></div></div>",
        "tasks", [[T("nav.tasks"), "#/tasks"], [t.name]]);
      var refreshTaskRail = wireTaskWorkspace(id, tab);
      var reload = function () { renderTaskDetail(id, tab); };
      var patch = function (field) {
        return function (val) {
          return api(base + "/form").then(function (d) { var f = d.form; f[field] = val; return api(base, { method: "PUT", body: f }); }).then(function () { toast(T("ie.saved")); reload(); });
        };
      };
      var keep = ovKeep; ovKeep = null;
      if (keep && $("#ov-next") && keep.k === [id, t.cron, t.status, t.pause_detail || ""].join("|")) $("#ov-next").innerHTML = keep.h;
      inlineEdit($("#ie-name"), false, patch("name"));
      if ($("#ie-prompt")) inlineEdit($("#ie-prompt"), true, patch("prompt"));
      // Schedule: the next run inline; the cron expression, time zone and the runs after it in the tooltip.
      if ($("#ov-next")) api("/api/scheduler/preview?cron=" + enc(t.cron) + "&n=" + (4 + (t.skip_runs || []).length)).then(function (d) {
        var ts = d.times || [], box = $("#ov-next"), snz = pauseKind(t) === "snooze", sk = t.skip_runs || [];
        // Runs skipped by hand are left out; the chip beside the next run lists them.
        ts = ts.filter(function (x) { return sk.indexOf(new Date(x).toISOString().replace(/\.\d{3}Z$/, "Z")) < 0; }).slice(0, 4);
        if (!box || !ts.length) return;
        // A snoozed task skips the times before its end; a task paused otherwise has no next run;
        // an active one shows whether "pause all" holds its next run back.
        if (snz) ts = ts.filter(function (x) { return new Date(x) > new Date(t.pause_detail); });
        var head = snz ? esc(T("tpz.wakes", { t: when(t.pause_detail) })) + (ts.length ? " · " + esc(T("td.next_at", { t: when(ts[0]) })) : "") : t.status !== "active" ? esc(T("tpz.no_next")) : "";
        box.innerHTML = (head ? '<span class="muted">' + head + "</span>" : '<span class="muted">' + heldNext(ts[0]) + "</span>" + skipChip(t)) + tip(T("td.sched_tip", { c: t.cron, z: state.tz || "" }) + (ts.length > 1 ? "\n" + T("td.then", { l: ts.slice(1).map(when).join(T("list.sep")) }) : "") + (d.dst ? "\n" + dstNoteText(d.dst, d.scheduler_timezone) : ""));
        if (!head) wireSkipChip(t, reload);
      }).catch(function () {});
      if ($("#ws-copy")) $("#ws-copy").addEventListener("click", function () { copyText(t.workspace).catch(fail); });
      // Desktop app only: open the folder in Finder / File Explorer / the file manager ("o").
      if ($("#ws-open")) desktopOpenable().then(function (ok) {
        var b = $("#ws-open");
        if (!ok || !b) return;
        var l = fmLabel() + " (o)";
        b.setAttribute("data-tip", l); b.setAttribute("aria-label", fmLabel());
        b.classList.remove("hidden");
        b.addEventListener("click", function () {
          api("/api/desktop/open", { method: "POST", body: { target: "workspace", id: t.id } }).catch(function (e) { toast(e.status === 404 ? T("ws.missing") : e.message, true); });
        });
      });
      wireRows();
      $$("[data-tab]").forEach(function (b) { b.addEventListener("click", function () { var tb = b.getAttribute("data-tab"); history.replaceState(null, "", "#/tasks/" + enc(id) + "/" + tb); lastHash = location.hash; renderTaskDetail(id, tb); }); });
      var depButton=document.createElement("button");depButton.className="btn";depButton.textContent=T("dep.check");depButton.id="t-dependencies";
      $("#t-run").parentNode.insertBefore(depButton,$("#t-run"));depButton.onclick=function(){dependencyCheck({kind:"task",id:id});};
      $("#t-run").addEventListener("click", function () { api(base + "/run", { method: "POST", body: {} }).then(function (res) { toast(startedText(res, "toast.run_started")); reload(); }).catch(fail); });
      $("#t-edit").addEventListener("click", function () { taskFormModal(id); });
      $("#t-dup").addEventListener("click", async function () {
        if (t.cost_control_mode === "bypass" && !await confirmDialog({title:T("costmode.confirm_title"),body:T("costmode.warning"),ok:T("costmode.confirm_ok"),danger:true})) return;
        var b = $("#t-dup"); b.disabled = true;
        api(base + "/duplicate", { method: "POST", body: { name: T("td.dup_name", { n: t.name }) } }).then(function (c) {
          location.hash = "#/tasks/" + enc(c.id);
          toastUndo(T("td.dup_done", { n: c.name }), function () { api("/api/tasks/" + enc(c.id), { method: "DELETE" }).then(function () { location.hash = "#/tasks/" + enc(id); }).catch(fail); });
        }).catch(function (e) { b.disabled = false; fail(e); });
      });
      var deleting = false;
      $("#t-del").addEventListener("click", function () { confirmDialog({ title: T("td.del_title"), body: T("td.confirm_del", { n: t.name }), ok: T("td.delete"), danger: true, typeName: t.name }).then(function (y) { if (y) { deleting = true; stopPoll(); } if (y) api(base, { method: "DELETE" }).then(function () { toast(T("td.deleted")); location.hash = "#/tasks"; }).catch(fail); }); });
      var tg = $("#t-toggle");
      tg.addEventListener("click", function (e) {
        var act = tg.getAttribute("data-act"), k = pauseKind(t);
        if (act === "pause") { e.stopPropagation(); taskPauseMenu(tg, t, reload); return; }
        tg.disabled = true;
        api(base + "/" + act, { method: "POST", body: {} }).then(function () {
          if (act === "resume" && k === "breaker") toast(T("pz.resumed_reset"));
          else if (act === "resume" && k === "budget") toast(T("pz.resumed_budget"));
          else if (act === "resume" && k === "snooze") toast(T("tpz.t_resumed"));
          reload();
        }).catch(function (e) { tg.disabled = false; fail(e); });
      });
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
      if (comp) wireDraft($(".task-composer-wrap"), comp.u, "amendment_" + id);
      if (comp) comp.addEventListener("submit", function (e) {
        e.preventDefault();
        var v = comp.u.value.trim();
        if (!v) return;
        comp.querySelector("button").disabled = true;
        api(base + "/amendments", { method: "POST", body: { utterance: v } }).then(function () { draftClear("amendment_" + id); reload(); }).catch(function (err) { fail(err); comp.querySelector("button").disabled = false; });
      });
      // Follow changes (see liveFollow): a run that starts or ends, a pause by the failure
      // streak, an edit or new version from another window. The page redraws in place from the
      // data just fetched (no second request, scroll kept) — but not while you type, edit inline,
      // have amendments ticked or something typed in the composer, read further up the thread,
      // or have a dialog or menu open. A task deleted elsewhere turns into the "not found" page.
      var paths = taskDetailPaths(id, tab), going = runs.some(function (x) { return x.status === "running" || x.status === "queued"; });
      var check = liveFollow(function () {
        var detail = Promise.all(paths.map(function (u) { return apiRaw(u); })).catch(function (e) { if (e && e.status === 404) return { gone: true }; throw e; });
        return Promise.all([detail, refreshTaskRail()]).then(function (data) { return data[0]; });
      }, shown, function (d) {
        if (deleting) return;
        // Deleted in another window (kept as archived): say so and go to the list, as a delete here does.
        if (Array.isArray(d) && d[0] && d[0].status === "archived" && t.status !== "archived") { stopPoll(); toast(T("td.gone")); location.hash = "#/tasks"; return; }
        if (Array.isArray(d)) d.forEach(function (x, i) { swrPut(paths[i], x); });
        else paths.forEach(function (k) { delete swr.cache[k]; }); // gone: no stale copy to redraw from
        var nx = $("#ov-next");
        if (nx && nx.innerHTML) ovKeep = { k: [id, t.cron, t.status, t.pause_detail || ""].join("|"), h: nx.innerHTML };
        refresh();
      }, function () {
        var pg = page();
        if (!liveQuiet(pg)) return false;
        if ($$(".req-sel", pg).some(function (c) { return c.checked; })) return false;
        var cm = $("#composer", pg);
        if (cm && cm.u && cm.u.value.trim()) return false;
        var th = $("#thread", pg);
        return !(th && th.scrollHeight - th.scrollTop - th.clientHeight > 8);
      });
      livePoll(function () { return evsLive() || going ? check() : null; }, 2000);
    });
  }

