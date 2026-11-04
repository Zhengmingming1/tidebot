  // ---------- 会话 page (P0) ----------
  // Sessions on the left (168 px, most recent first), the open session's thread and message
  // box on the right, the thread scrolling between them. #/chat is a new blank session: the
  // first message creates it (origin "chat", its own empty folder) and the page moves to
  // #/chat/<id>; a blank session whose first message could not start is dropped again. A
  // session made from a run shows that run as its first turn. Every turn is read-only and
  // without web access unless the session has it (the server enforces both). Deleting asks in
  // an in-app dialog. Ideas: the approved wireframe (wire-session-confirm.png) and
  // siteboon/claudecodeui src/modules/sidebar/SidebarSessionItem.tsx
  // (title line + muted time line; a processing session is marked in its row).
  var CS_ID = /^[\w-]{1,64}$/;
  function csTitle(s) { return (s && s.title) || T("cs.untitled"); }
  function csAll(s) { return (s && s.turn_list) || []; }
  function csItemHtml(s, cur) {
    var on = s.id === cur, sub = relDay(s.updated_at) + " · " + plural(s.turns, "cs.turns_one", "cs.turns_many");
    var tip = T(s.origin === "run" ? "cs.item_tip_run" : "cs.item_tip", { t: when(s.updated_at), n: num(s.turns), c: usd(s.cost_usd, 4) });
    return '<a class="cs-item' + (on ? " active" : "") + '" href="#/chat/' + enc(s.id) + '" data-cs="' + esc(s.id) + '"' + (on ? ' aria-current="page"' : "") + ' data-tip="' + esc(tip) + '">' +
      '<strong class="ell">' + esc(csTitle(s)) + "</strong><small class=\"ell\">" + (s.status === "running" ? '<span class="rc-dot" aria-hidden="true"></span>' : s.origin === "run" ? ic("runs", "sm") : "") + esc(sub) + "</small></a>";
  }
  // The folder the session reads: the run's folder, or its own empty one.
  function csDirTag(s) {
    var p = (s && s.workspace_dir) || "", base = p.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || p;
    return '<span class="rc-tag rc-dir" tabindex="0" data-tip="' + esc(p ? T("cs.dir_tip", { p: p }) : T("cs.scratch_tip")) + '">' + ic("folder", "sm") + '<span class="ell">' + esc(p ? base : T("cs.scratch")) + "</span></span>";
  }
  function csHeadHtml(s) {
    if (!s) return '<header class="cs-head"><h2 class="ell">' + esc(T("cs.new")) + "</h2></header>";
    var from = s.origin === "run" && CS_ID.test(s.origin_run_id || "") ? '<a class="pill plain sm rc-of" href="#/runs/' + enc(s.origin_run_id) + '" data-tip="' + esc(T("cs.from_run_tip")) + '">' + ic("runs", "sm") + esc(T("cs.from_run")) + "</a>" : "";
    return '<header class="cs-head"><h2 class="ell" data-tip="' + esc(T("cs.created_tip", { t: when(s.created_at) })) + '">' + esc(csTitle(s)) + "</h2>" + from + '<span class="rc-sp"></span>' +
      '<button type="button" class="icon-btn sm danger-text" id="cs-del" aria-label="' + esc(T("cs.del")) + '" data-tip="' + esc(T("cs.del")) + '">' + ic("trash", "sm") + "</button></header>";
  }
  function csPageHtml(list, s) {
    var cur = s ? s.id : "";
    var items = list.length ? list.map(function (x) { return csItemHtml(x, cur); }).join("") : '<p class="cs-none">' + esc(T("cs.none")) + "</p>";
    var thread = s ? csAll(s).map(rcTurnHtml).join("") :
      '<div class="cs-empty">' + ic("chat") + "<strong>" + esc(T("cs.empty")) + "</strong><span>" + esc(T("cs.empty_sub")) + "</span></div>";
    return '<h1 class="sr-only">' + esc(T("nav.chat")) + '</h1><div class="cs" id="cs"><aside class="cs-side" aria-label="' + esc(T("cs.list")) + '">' +
      '<a class="btn sm cs-new' + (s ? "" : " active") + '" href="#/chat">' + ic("plus", "sm") + esc(T("cs.new")) + '</a><nav class="cs-list" id="cs-list">' + items + "</nav></aside>" +
      '<section class="cs-main" id="rc" aria-label="' + esc(csTitle(s)) + '">' + csHeadHtml(s) +
      '<div class="rc-thread cs-thread" id="rc-thread" aria-live="polite" tabindex="-1">' + thread + "</div>" +
      rcBoxHtml({ ph: s ? "cs.ph" : "cs.ph_new", label: "cs.label", read_tip: "cs.read_tip", tags: csDirTag(s) }) + "</section></div>";
  }
  function renderChat(id) {
    if (id && !CS_ID.test(id)) id = "";
    return Promise.all([api("/api/chat-sessions?limit=200", { fresh: true }), id ? api("/api/chat-sessions/" + enc(id), { fresh: true }) : null]).then(function (r) {
      var list = (r[0] && r[0].sessions) || [], s = r[1];
      mount(csPageHtml(list, s), "chat", s ? [[T("nav.chat"), "#/chat"], [csTitle(s)]] : [[T("nav.chat")]]);
      var sec = $("#rc"), thread = $("#rc-thread", sec), stick = true;
      thread.addEventListener("scroll", function () { stick = thread.scrollTop + thread.clientHeight >= thread.scrollHeight - 32; }, { passive: true });
      var bottom = function () { if (stick) thread.scrollTop = thread.scrollHeight; };
      if (!s) {
        bindChat(sec, {
          id: "", key: "cs_new", ph: "cs.ph_new", send_tip: "cs.send_tip", dropEmpty: true, turns: csAll,
          create: function () { return api("/api/chat-sessions", { method: "POST", body: { origin: "chat" } }); },
          onSent: function (x) { location.hash = "#/chat/" + enc(x.id); return true; }
        });
        $("#rc-in", sec).focus();
        return;
      }
      bindChat(sec, {
        id: s.id, key: "cs_" + s.id, ph: "cs.ph", send_tip: "cs.send_tip", pre: s, turns: csAll,
        // the list row follows the session (busy dot, turns, title) without redrawing the list
        onPaint: function (x) {
          var el = $('[data-cs="' + s.id + '"]'), html = csItemHtml(x, s.id);
          if (el && el._h !== html && el.outerHTML !== html) { var tmp = document.createElement("div"); tmp.innerHTML = html; el.replaceWith(tmp.firstChild); $('[data-cs="' + s.id + '"]')._h = html; }
          requestAnimationFrame(bottom);
        },
        onGone: function () { location.hash = "#/chat"; }
      });
      thread.scrollTop = thread.scrollHeight;
      $("#cs-del").addEventListener("click", function (e) {
        var b = e.currentTarget;
        confirmDialog({ title: T("cs.del_title"), body: T("cs.del_body"), ok: T("cs.del"), danger: true }).then(function (y) {
          if (!y) return;
          b.disabled = true;
          return api("/api/chat-sessions/" + enc(s.id), { method: "DELETE" }).then(function () {
            toast(T("cs.deleted"));
            draftClear("cs_" + s.id);
            if (location.hash === "#/chat/" + enc(s.id) || location.hash === "#/chat/" + s.id) location.hash = "#/chat";
          });
        }).catch(function (x) { b.disabled = false; if (!x.auth) toast(rcErrText(x), true); });
      });
    });
  }
