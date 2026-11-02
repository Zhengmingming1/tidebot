  // ---------- pause all schedules ----------
  // One switch for every schedule, for a holiday, a demo or a flaky provider (idea: Uptime
  // Kuma's maintenance windows — "manual" until switched off or a window with an end, picked
  // from quick lengths: server/model/maintenance.js getStatus, src/pages/EditMaintenance.vue
  // setQuickDuration). A timed pause ends by itself; runs by hand, buttons and webhooks still
  // go; tasks that came due follow their own missed-runs setting on resume. Shown in the
  // sidebar on every page (fed by the status poll) and on Home's "next 24 hours".
  var SPZ_FOR = ["1h", "4h", "tomorrow", "manual"];
  var spState = null, spAt = 0;
  // spAdopt keeps the newest answer: a status poll that left before a change doesn't undo it.
  // Returns true when what's shown changes.
  function spAdopt(v, asked) {
    if (!v || (asked || 0) < spAt) return false;
    var sig = function (x) { return x ? [!!x.paused, x.until || "", x.since || "", x.due || 0, x.quiet_until || ""].join("|") : ""; };
    var changed = sig(v) !== sig(spState);
    spState = v; spAt = asked || Date.now();
    return changed;
  }
  function spEnds(v) { return v.until ? T("spz.ends", { t: when(v.until) }) : T("spz.ends_manual"); }
  function spTip(v) {
    var t = [spEnds(v)];
    if (v.since) t.push(T("spz.tip_since", { t: when(v.since) }));
    if (v.due) t.push(T("spz.note_due", { n: v.due }));
    return t.join("\n");
  }
  function spPillHtml() {
    var v = spState;
    // quiet hours holding scheduled runs: a quieter pill that opens Settings › General › Quiet hours
    if (v && !v.paused && v.quiet_until) {
      var qt = T("qh.active_tip", { t: when(v.quiet_until) }) + (v.due ? "\n" + T("qh.due", { n: v.due }) : "");
      return '<a class="spz-pill qh-pill" id="qh-pill" href="#/settings/general/quiet" data-tip="' + esc(qt) + '" aria-label="' + esc(T("qh.pill") + " · " + qt) + '">' + ic("moon", "sm") +
        '<span class="spz-txt"><strong>' + esc(T("qh.pill")) + "</strong><small>" + esc(T("spz.pill_until", { t: when(v.quiet_until) })) + "</small></span></a>";
    }
    if (!v || !v.paused) return "";
    return '<button class="spz-pill" id="spz-pill" aria-haspopup="menu" aria-expanded="false" data-tip="' + esc(spTip(v)) + '" aria-label="' + esc(T("spz.pill") + " · " + spEnds(v)) + '">' + ic("pause", "sm") +
      '<span class="spz-txt"><strong>' + esc(T("spz.pill")) + "</strong><small>" + esc(v.until ? T("spz.pill_until", { t: when(v.until) }) : T("spz.pill_manual")) + "</small></span></button>";
  }
  // spApply shows a new state everywhere at once: sidebar, Home's card, the open menu.
  function spApply(v) {
    spAdopt(v, Date.now());
    paintCC();
    paintHomePause();
    paintHeldNext();
  }
  // setSchedPause(true, "1h" | "4h" | "tomorrow" | "manual") or setSchedPause(false).
  function setSchedPause(on, choice) {
    var prev = spState;
    return rpc("scheduler.pause.set", on ? { paused: true, "for": choice } : { paused: false }).then(function (v) {
      spApply(v);
      if (on && !(prev && prev.paused)) toastUndo(v.until ? T("spz.t_paused_until", { t: when(v.until) }) : T("spz.t_paused"), function () { setSchedPause(false); });
      else if (on) toast(v.until ? T("spz.t_paused_until", { t: when(v.until) }) : T("spz.t_paused"));
      else toast(prev && prev.due ? T("spz.t_resumed_due", { n: prev.due }) : T("spz.t_resumed"));
      return v;
    }).catch(fail);
  }
  // spMenu: the "pause all" menu at the anchor ("up" opens above it, for the sidebar).
  function spMenu(anchor, dir) {
    if ($("#spz-menu")) { $("#spz-menu")._close(); return; }
    api("/api/rpc/scheduler.pause.get", { method: "POST", body: {}, read: true }).then(function (r) {
      var v = r.result;
      if (!v) return;
      spAdopt(v, Date.now());
      pzMenu(anchor, dir, {
        title: T(v.paused ? "spz.menu_title_on" : "spz.menu_title"), sub: v.paused ? spEnds(v) : "", paused: v.paused,
        options: v.options, note: T("spz.note") + " " + (v.paused && v.due ? T("spz.note_due", { n: v.due }) : T("spz.note_resume")),
        pick: function (f) { if (f) setSchedPause(true, f); else setSchedPause(false); }
      });
    }).catch(fail);
  }
  // pzMenu: a small "pause for…" menu (1 hour, 4 hours, until 9:00 tomorrow, until resumed)
  // shared by "pause all" and one task's Pause button. o: title, sub (current end), paused
  // (adds "resume now"), options (from scheduler.pause.get, with the end each gives), note,
  // pick(for) — "" = resume. Arrow keys move, Enter / Space pick, Esc or a click outside
  // closes and gives focus back.
  function pzMenu(anchor, dir, o) {
    var opts = o.options || SPZ_FOR.map(function (f) { return { "for": f }; });
    var item = function (x) {
      return '<button type="button" role="menuitem" class="spz-item" data-for="' + esc(x["for"]) + '"><span>' + esc(T("spz.f_" + x["for"])) + "</span><small>" + esc(x.until ? T("spz.ends", { t: when(x.until) }) : T("spz.ends_manual")) + "</small></button>";
    };
    popMenu(anchor, dir, {
      id: "spz-menu", label: o.title,
      html: '<div class="spz-head">' + ic("pause", "sm") + "<div><strong>" + esc(o.title) + "</strong>" + (o.sub ? "<small>" + esc(o.sub) + "</small>" : "") + "</div></div>" +
        (o.paused ? '<button type="button" role="menuitem" class="spz-item spz-resume" data-resume="1"><span>' + ic("play", "sm") + esc(T("spz.resume")) + "</span></button><div class=\"spz-sep\">" + esc(T("spz.change")) + "</div>" : "") +
        (o.skipAt ? '<button type="button" role="menuitem" class="spz-item spz-skip" data-skip="1"><span>' + ic("skipFwd", "sm") + esc(T("skip.menu")) + "</span><small>" + esc(T("skip.menu_sub", { t: when(o.skipAt) })) + '</small></button><div class="spz-sep">' + esc(T("skip.or_pause")) + "</div>" : "") +
        opts.map(item).join("") +
        '<p class="spz-note">' + ic("info", "sm") + "<span>" + esc(o.note) + "</span></p>",
      pick: function (b) { o.pick(b.hasAttribute("data-resume") ? "" : b.hasAttribute("data-skip") ? "skip" : b.getAttribute("data-for")); }
    });
  }
  // popMenu: a small menu (.popover.spz-menu) at the anchor ("up" opens above it). o: id,
  // label, html (items are [role=menuitem] buttons), pick(button). Arrow keys / Home / End
  // move, Enter / Space pick, Esc or a click outside closes and gives focus back.
  function popMenu(anchor, dir, o) {
    if ($("#" + o.id) || !document.contains(anchor)) return;
    var m = document.createElement("div");
    m.id = o.id; m.className = "popover spz-menu" + (o.cls ? " " + o.cls : ""); m.setAttribute("role", "menu"); m.setAttribute("aria-label", o.label);
    m.innerHTML = o.html;
    document.body.appendChild(m);
    var r0 = anchor.getBoundingClientRect(), w = m.offsetWidth, h = m.offsetHeight;
    var left = dir === "up" ? r0.left : r0.right - w;
    left = Math.max(8, Math.min(left, window.innerWidth - w - 8));
    var top = dir === "up" ? r0.top - h - 8 : r0.bottom + 8;
    if (dir !== "up" && top + h > window.innerHeight - 8) top = Math.max(8, r0.top - h - 8);
    if (top < 8) top = 8;
    m.style.left = Math.round(left) + "px"; m.style.top = Math.round(top) + "px";
    anchor.setAttribute("aria-expanded", "true");
    var items = $$('[role="menuitem"]', m);
    var close = function (refocus) {
      m.remove(); anchor.setAttribute("aria-expanded", "false");
      document.removeEventListener("click", off, true); document.removeEventListener("keydown", key, true); window.removeEventListener("hashchange", m._close);
      if (refocus && document.contains(anchor)) anchor.focus();
    };
    m._close = function () { close(false); };
    var off = function (e) { if (!m.contains(e.target) && !anchor.contains(e.target)) close(false); };
    var key = function (e) {
      var i = items.indexOf(document.activeElement);
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(true); }
      else if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); items[(i + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length].focus(); }
      else if (e.key === "Home" || e.key === "End") { e.preventDefault(); items[e.key === "Home" ? 0 : items.length - 1].focus(); }
      else if (e.key === "Tab") close(false);
    };
    m.addEventListener("click", function (e) {
      var b = e.target.closest('[role="menuitem"]');
      if (!b) return;
      close(true);
      o.pick(b);
    });
    setTimeout(function () { document.addEventListener("click", off, true); document.addEventListener("keydown", key, true); }, 0);
    window.addEventListener("hashchange", m._close);
    if (items[0]) items[0].focus();
  }
  // heldNext: "next 10:00" for a task's schedule; while every schedule is paused and that
  // time falls inside the pause it reads "next 10:00 · paused" (dimmed), so the task list
  // and the task page don't promise a run the pause will hold back. Kept up to date by
  // paintHeldNext when the pause changes ([data-next] spans).
  function heldNext(iso) {
    var held = spHolds(iso);
    return '<span class="nx' + (held ? " held" : "") + '" data-next="' + esc(iso) + '"' + (held ? ' data-tip="' + esc(spEnds(spState)) + '"' : "") + ">" + esc(T("tl.next", { t: when(iso) })) + (held ? '<span class="tl-held"> · ' + esc(T("spz.held")) + "</span>" : "") + "</span>";
  }
  function spHolds(iso) {
    var v = spState;
    if (!iso || !v || !v.paused) return false;
    return new Date(iso).getTime() < (v.until ? new Date(v.until).getTime() : Infinity);
  }
  // taskPauseMenu: one task's Pause button offers the same lengths as "pause all"; a
  // timed pause resumes by itself and skips the fire times inside it (Uptime Kuma puts one
  // monitor into a maintenance window the same way: server/model/monitor.js isUnderMaintenance).
  function taskPauseMenu(anchor, t, done) {
    if ($("#spz-menu")) { $("#spz-menu")._close(); return; }
    api("/api/rpc/scheduler.pause.get", { method: "POST", body: {}, read: true }).then(function (r) {
      var v = r.result || {};
      var nx = t.cron && t.status === "active" && t.next_run_at && new Date(t.next_run_at) > new Date() ? t.next_run_at : "";
      pzMenu(anchor, "down", {
        title: T("tpz.title"), options: v.options, note: T(t.cron ? "tpz.note" : "tpz.note_nocron"), skipAt: nx,
        pick: function (f) {
          if (f === "skip") { skipRun(t, nx, false, done); return; }
          rpc("task.pause_for", { id: t.id, "for": f }).then(function (res) {
            var u = res && res.until;
            toastUndo(u ? T("tpz.t_until", { t: when(u) }) : T("tpz.t_manual"), function () { api("/api/tasks/" + enc(t.id) + "/resume", { method: "POST", body: {} }).then(done).catch(fail); });
            done();
          }).catch(fail);
        }
      });
    }).catch(fail);
  }
  function paintHeldNext() {
    $$("[data-next]").forEach(function (el) {
      var iso = el.getAttribute("data-next");
      if (el.classList.contains("held") !== spHolds(iso)) el.outerHTML = heldNext(iso);
    });
  }
  // ---------- skip one run ----------
  // "Skip next run" leaves one fire time out and keeps the schedule on — a day off, or a run
  // already done by hand — instead of pausing and having to remember to resume. Idea from
  // scaleapi/scale-agentex (agentex-ui/components/scheduled-tasks/all-schedules-list.tsx:
  // "Skip next run" / "Unskip run" per upcoming time; the server only accepts a real upcoming
  // fire time). Offered in the task's Pause menu and on each run in Home › next 24 hours; a
  // skipped run stays listed, dimmed, so it can be put back.
  function skipErr(e) {
    var m = (e && e.message) || "";
    if (/at most \d+ upcoming/.test(m)) return T("skip.e_many");
    if (/not one of the task's upcoming/.test(m)) return T("skip.e_time");
    if (/only an active scheduled/.test(m)) return T("skip.e_sched");
    return m;
  }
  // skipRun(task {id, name}, at ISO, undo, done(result)): toast with Undo / "put back".
  function skipRun(t, at, undo, done) {
    return rpc("task.skip_run", { id: t.id, at: at, undo: !!undo }).then(function (res) {
      res = res || {};
      var again = function () { skipRun(t, at, !undo, done); };
      if (undo) toast(T("skip.t_back", { t: when(at) }));
      else toastUndo(res.next_run_at ? T("skip.t_done_next", { t: when(at), n: when(res.next_run_at) }) : T("skip.t_done", { t: when(at) }), again);
      if (done) done(res);
      return res;
    }).catch(function (e) { toast(skipErr(e), true); });
  }
  // skipChip: "skipped 1" beside a task's next run; the tooltip lists the times, a click
  // opens a menu to put one back.
  function skipChip(t) {
    var sk = t.skip_runs || [];
    if (!sk.length) return "";
    var tipTxt = T("skip.chip_tip", { l: sk.map(when).join(T("list.sep")) });
    return '<button type="button" class="skip-chip" data-skipchip="' + esc(t.id) + '" aria-haspopup="menu" aria-expanded="false" data-tip="' + esc(tipTxt) + '" aria-label="' + esc(tipTxt) + '">' + ic("skipFwd", "sm") + "<span>" + esc(T("skip.chip", { n: sk.length })) + "</span></button>";
  }
  function wireSkipChip(t, done) {
    var b = document.querySelector('[data-skipchip="' + (window.CSS && CSS.escape ? CSS.escape(t.id) : t.id) + '"]');
    if (!b) return;
    b.addEventListener("click", function (e) {
      e.preventDefault(); e.stopPropagation();
      if ($("#skip-menu")) { $("#skip-menu")._close(); return; }
      popMenu(b, "down", {
        id: "skip-menu", label: T("skip.put_back_title"),
        html: '<div class="spz-head">' + ic("skipFwd", "sm") + "<div><strong>" + esc(T("skip.put_back_title")) + "</strong><small>" + esc(T("skip.put_back_sub")) + "</small></div></div>" +
          (t.skip_runs || []).map(function (x) { return '<button type="button" role="menuitem" class="spz-item" data-at="' + esc(x) + '"><span>' + ic("undo", "sm") + esc(when(x)) + "</span><small>" + esc(T("skip.put_back")) + "</small></button>"; }).join(""),
        pick: function (it) { skipRun(t, it.getAttribute("data-at"), true, done); }
      });
    });
  }
  // Home › next 24 hours: each row's skip / put back button, updated in place (the list
  // keeps skipped runs, so nothing moves).
  function next24Row(s) {
    // in quiet hours: says when it will go instead of the schedule (that moves to the tooltip)
    var quiet = !s.skipped && !!s.quiet_until;
    var lbl = T(s.skipped ? "skip.put_back" : "skip.this") + " · " + s.task_name + " " + when(s.at);
    return '<li data-at="' + esc(s.at) + '" data-task="' + esc(s.task_id) + '" class="' + (s.skipped ? "skipped" : quiet ? "quiet" : "") + '"><span class="tl-dot"></span><span class="tl-time">' + esc(clock(s.at)) + '</span><div class="tl-name"><strong>' + esc(s.task_name) + "</strong><small>" + esc(relDay(s.at) + " · ") + (quiet ? '<span class="tl-quiet" data-tip="' + esc(T("qh.row_tip", { t: when(s.quiet_until) }) + "\n" + cronHuman(s.cron)) + '">' + ic("moon", "sm") + esc(T("qh.row", { t: clock(s.quiet_until) })) + "</span>" : esc(cronHuman(s.cron))) + (s.skipped ? '<span class="tl-skipped"> · ' + esc(T("skip.skipped")) + "</span>" : "") + "</small></div>" +
      '<button type="button" class="icon-btn sm tl-skip" data-skip-at="' + esc(s.at) + '" data-tip="' + esc(T(s.skipped ? "skip.put_back" : "skip.this_tip")) + '" aria-label="' + esc(lbl) + '">' + ic(s.skipped ? "undo" : "skipFwd", "sm") + "</button>" +
      '<a class="link" href="#/tasks/' + enc(s.task_id) + '" aria-label="' + esc(s.task_name) + '">' + ic("chev", "sm") + "</a></li>";
  }
  function wireNext24(list) {
    var box = $("#next24");
    if (!box) return;
    box._list = list;
    if (box._skipWired) return;
    box._skipWired = true;
    box.addEventListener("click", function (e) {
      var b = e.target.closest(".tl-skip");
      if (!b) return;
      e.preventDefault(); e.stopPropagation();
      var li = b.closest("li"), at = li.getAttribute("data-at"), id = li.getAttribute("data-task");
      var name = $(".tl-name strong", li).textContent;
      var undo = li.classList.contains("skipped");
      b.disabled = true;
      var apply = function (sk) {
        $$("#next24 li[data-at]").forEach(function (x) {
          var xa = x.getAttribute("data-at"), on = sk.indexOf(xa) >= 0;
          if (x.getAttribute("data-task") !== id || on === x.classList.contains("skipped")) return;
          var row = (box._list || []).filter(function (y) { return y.task_id === id && y.at === xa; })[0] || { at: xa, task_id: id, task_name: name, cron: "" };
          row.skipped = on;
          var focus = x.contains(document.activeElement);
          x.outerHTML = next24Row(row);
          if (focus) { var nb = $('#next24 li[data-at="' + xa + '"][data-task="' + id + '"] .tl-skip'); if (nb) nb.focus(); }
        });
        paintHomePause();
      };
      skipRun({ id: id, name: name }, at, undo, function (res) { apply(res.skipped || []); }).then(function () { if (document.contains(b)) b.disabled = false; });
    });
  }
  // Home › next 24 hours: the card's pause button, the "paused" line and runs the pause holds back.
  function paintHomePause() {
    var btn = $("#spz-home"), ban = $("#spz-banner"), v = spState || {};
    if (!btn || !ban) return;
    btn.classList.toggle("on", !!v.paused);
    btn.innerHTML = ic("pause", "sm");
    btn.setAttribute("aria-label", T(v.paused ? "spz.btn_on" : "spz.btn"));
    btn.setAttribute("data-tip", v.paused ? T("spz.btn_on") + "\n" + spTip(v) : T("spz.btn_tip"));
    ban.innerHTML = v.paused ? '<p class="notice warn spz-banner">' + ic("pause", "sm") + '<span><strong>' + esc(T("spz.banner")) + "</strong> · " + esc(spEnds(v)) + '</span><button type="button" class="btn sm" id="spz-resume">' + ic("play", "sm") + esc(T("spz.resume")) + "</button></p>" : "";
    var rb = $("#spz-resume"); if (rb) rb.addEventListener("click", function () { setSchedPause(false); });
    var end = v.paused ? (v.until ? new Date(v.until).getTime() : Infinity) : 0;
    $$("#next24 .timeline li[data-at]").forEach(function (li) {
      var held = new Date(li.getAttribute("data-at")).getTime() < end;
      li.classList.toggle("held", held);
      var tag = $(".tl-held", li);
      if (held && !tag) $(".tl-name small", li).insertAdjacentHTML("beforeend", '<span class="tl-held"> · ' + esc(T("spz.held")) + "</span>");
      if (!held && tag) tag.remove();
    });
  }
