  // ---------- SKILLS ----------
  function renderSkills() {
    return api("/api/skills").then(function (skills) {
      var rows = skills.length ? '<table class="table"><thead><tr><th>' + esc(T("col.skill")) + "</th><th>" + esc(T("col.current")) + "</th><th>" + esc(T("col.files")) + "</th><th>" + esc(T("col.used_by")) + "</th><th>" + esc(T("col.updated")) + '</th><th></th></tr></thead><tbody>' +
        skills.map(function (s) {
          return '<tr class="click" data-href="#/skills/' + enc(s.id) + '"><td><div class="cell-main"><span class="ico" data-hue="' + iconHue("skills") + '">' + ic("skills", "sm") + "</span><div><div class=\"nm-line\"><a class=\"strong-link\" href=\"#/skills/" + enc(s.id) + '">' + esc(s.name) + "</a>" + idChip(T("sk.id"), s.id) + "</div><small>" + esc(s.description || T("sk.no_desc_short")) + '</small><span data-cap-skill="' + esc(s.id) + '"></span></div></div></td><td><span class="tag perm">v' + esc(s.version_no) + "</span></td><td>" + esc(s.files) + '</td><td class="nowrap">' + esc(plural(s.used_by, "tasks.count_one", "tasks.count_many")) +
            '</td><td class="nowrap">' + esc(when(s.updated_at)) + '</td><td class="right nowrap"><div class="row-actions end"><button class="btn sm" data-sk-run="' + esc(s.id) + '" data-tip="' + esc(T("rn.run_skill_tip", { name: s.name })) + '">' + ic("play", "sm") + esc(T("btn.run")) + '</button><button class="icon-btn sm" data-sk-export="' + esc(s.id) + '" data-tip="' + esc(T("sk.export_tip", { n: s.version_no })) + '" aria-label="' + esc(T("sk.export")) + '">' + ic("download", "sm") + '</button><button class="btn sm" data-versions="' + esc(s.id) + '" data-name="' + esc(s.name) + '">' + ic("branch", "sm") + esc(T("sk.versions")) + "</button></div></td></tr>";
        }).join("") + "</tbody></table>" : emptyState("skills", T("sk.none"), T("sk.none_sub"));
      mount('<div class="page-head"><div><h1>' + esc(T("nav.skills")) + "</h1><p>" + esc(T("sk.sub")) + '</p></div><div class="actions"><button class="btn primary" id="sk-import">' + ic("upload", "sm") + esc(T("sk.import")) + "</button></div></div>" +
        skillTabs("skills") + '<section class="card flush"><div class="card-head"><span class="ico">' + ic("skills") + "</span><h2>" + esc(T("sk.library")) + "</h2></div>" + rows + "</section>", "skills", [[T("nav.skills")]]);
      wireRows();
      capLoadSkillBadges(page());
      $("#sk-import").addEventListener("click", function () { skillImportModal(renderSkills); });
      $$("[data-versions]").forEach(function (b) { b.addEventListener("click", function () { location.hash = "#/skills/" + enc(b.getAttribute("data-versions")); }); });
      $$("[data-sk-export]").forEach(function (b) { b.addEventListener("click", function () { exportSkill(b.getAttribute("data-sk-export"), "", b); }); });
      $$("[data-sk-run]").forEach(function (b) { var check=document.createElement("button");check.className="icon-btn sm";check.setAttribute("aria-label",T("dep.check"));check.setAttribute("data-tip",T("dep.check"));check.innerHTML=ic("shield","sm");check.onclick=function(e){e.stopPropagation();dependencyCheck({kind:"skill",id:b.getAttribute("data-sk-run")});};b.parentNode.insertBefore(check,b); });
      $$("[data-sk-run]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); quickRunModal({ skill_id: b.getAttribute("data-sk-run") }); }); });
    });
  }

  // ---------- skill detail: version tree, diff, optimizer ----------
  function diffView(d) {
    var files = (d.files || []).filter(function (f) { return f.status !== "same"; });
    if (!files.length) return '<p class="muted pad">' + esc(T("diff.identical")) + "</p>";
    return '<p class="diff-sum"><span class="add">+' + esc(d.added) + '</span> <span class="del">−' + esc(d.removed) + "</span> · " + esc(T("diff.files_n", { n: files.length })) + "</p>" + files.map(function (f) {
      var body = f.lines && f.lines.length ? '<table class="diff"><tbody>' + f.lines.map(function (l) {
        if (l.op === "@") return '<tr class="gap"><td colspan="4">⋯</td></tr>';
        var c = l.op === "+" ? "ins" : l.op === "-" ? "del" : "";
        return '<tr class="' + c + '"><td class="ln-a">' + (l.a || "") + '</td><td class="ln-b">' + (l.b || "") + '</td><td class="sg">' + esc(l.op === " " ? "" : l.op === "-" ? "−" : "+") + '</td><td class="tx">' + esc(l.text) + "</td></tr>";
      }).join("") + "</tbody></table>" : '<p class="muted pad">' + esc(T("diff.st_" + f.status)) + "</p>";
      return '<details class="diff-file" open><summary><code>' + esc(f.path) + '</code><span class="pill sm ' + (f.status === "added" ? "ok" : f.status === "removed" ? "bad" : "info") + '">' + esc(T("diff.st_" + f.status)) + '</span><span class="grow"></span>' + (f.added ? '<span class="add">+' + f.added + "</span> " : "") + (f.removed ? '<span class="del">−' + f.removed + "</span>" : "") + "</summary>" + body + "</details>";
    }).join("");
  }
  function renderSkillDetail(id, params) {
    var base = "/api/skills/" + enc(id);
    return Promise.all([api("/api/skills"), api(base + "/versions"), rpcRead("skills.optimize_settings", { skill_id: id }).catch(function () { return null; }), rpc("skills.proposals", { skill_id: id }).catch(function () { return []; }), rpc("skills.compare", { skill_id: id }).catch(function () { return null; }), api(base + "/repository-source").catch(function () { return null; })]).then(function (r) {
      var sk = (r[0] || []).filter(function (x) { return x.id === id; })[0];
      if (!sk) throw new Error(T("sk.not_found"));
      var vers = r[1], oset = r[2] || { mode: "off" }, props = r[3] || [], cmp = r[4];
      var list = vers.versions.slice().reverse(), byId = {};
      list.forEach(function (v) { byId[v.id] = v; });
      var cur = byId[vers.current] || list[0];
      var b = params.get("b") && byId[params.get("b")] ? params.get("b") : cur.id;
      var a = params.get("a") && byId[params.get("a")] ? params.get("a") : ((byId[b] && byId[b].parent_id && byId[byId[b].parent_id]) ? byId[b].parent_id : (list[1] || cur).id);
      var tab = params.get("tab") === "files" ? "files" : "diff";
      var fv = params.get("v") && byId[params.get("v")] ? params.get("v") : cur.id, ff = params.get("f") || "";
      var vopt = function (sel) { return list.map(function (v) { return opt(v.id, "v" + v.no + (v.id === vers.current ? " · " + T("ver.current") : v.id === vers.canary ? " · " + T("ver.canary") : ""), v.id === sel); }).join(""); };
      var tree = '<ul class="vlist">' + list.map(function (v) {
        var isCur = v.id === vers.current, isCan = v.id === vers.canary;
        return '<li class="' + (isCur ? "cur" : "") + '"><span class="vdot' + (isCur ? " cur" : isCan ? " can" : "") + '"></span><div class="grow"><strong>v' + esc(v.no) + "</strong>" + (isCur ? ' <span class="tag perm">' + esc(T("ver.current")) + "</span>" : "") + (isCan ? ' <span class="tag next">' + esc(T("ver.canary")) + "</span>" : "") +
          "<small>" + esc((T("src." + v.source) === "src." + v.source ? v.source : T("src." + v.source)) + " · " + when(v.created_at)) + (v.message ? " · " + esc(v.message) : "") + '</small></div><div class="row-actions"><button class="icon-btn sm" data-v-export="' + esc(v.id) + '" data-tip="' + esc(T("sk.export_tip", { n: v.no })) + '" aria-label="' + esc(T("sk.export_v", { n: v.no })) + '">' + ic("download", "sm") + '</button><button class="icon-btn sm" data-v-files="' + esc(v.id) + '" data-tip="' + esc(T("fb.view_v", { n: v.no })) + '" aria-label="' + esc(T("fb.view_v", { n: v.no })) + '">' + ic("folder", "sm") + '</button><button class="btn sm ghost" data-cmp="' + esc(v.id) + '">' + esc(T("diff.compare")) + "</button>" +
          (isCur ? "" : '<button class="btn sm" data-srb="' + esc(v.id) + '" data-no="' + esc(v.no) + '">' + ic("rollback", "sm") + esc(T("ver.rollback")) + "</button>") + "</div></li>";
      }).join("") + "</ul>";
      var modes = [["off", T("opt.m_off"), T("opt.m_off_sub")], ["propose", T("opt.m_propose"), T("opt.m_propose_sub")], ["canary", T("opt.m_canary"), T("opt.m_canary_sub")]];
      var pct = function (x) { return Math.round((x || 0) * 100) + "%"; };
      var cmpTable = function (c) {
        if (!c) return "";
        var row = function (label, f) { return "<tr><th>" + esc(label) + "</th><td>" + esc(f(c.current)) + "</td><td>" + esc(f(c.candidate)) + "</td></tr>"; };
        return '<table class="table cmp"><thead><tr><th></th><th>' + esc(T("opt.cur_col")) + "</th><th>" + esc(T("opt.cand_col")) + "</th></tr></thead><tbody>" + row(T("opt.runs"), function (x) { return x.runs; }) + row(T("opt.success"), function (x) { return x.runs ? pct(x.success_rate) : "—"; }) + row(T("opt.avg_cost"), function (x) { return x.runs ? usd(x.avg_cost_usd, 4) : "—"; }) + row(T("opt.p50"), function (x) { return x.runs ? dur(x.p50_ms) : "—"; }) + "</tbody></table>" + (c.note === "low_sample" ? '<p class="hint">' + ic("info", "sm") + esc(T("opt.low_sample")) + "</p>" : "");
      };
      var propHtml = props.length ? props.slice().reverse().map(function (p) {
        var live = p.status === "pending" || p.status === "canary";
        var v = byId[p.version_id];
        return '<div class="prop' + (live ? "" : " done") + '"><div class="prop-head"><strong>' + esc(v ? "v" + v.no : shortId(p.version_id)) + '</strong><span class="pill sm ' + (p.status === "canary" ? "info" : p.status === "promoted" ? "ok" : live ? "warn" : "") + '">' + esc(T("opt.st_" + p.status)) + '</span><span class="tag">' + esc(T("opt.risk_" + (p.risk || "low"))) + '</span><small class="muted grow right">' + agoHtml(p.created_at) + "</small></div>" +
          (p.rationale ? '<p class="prop-why">' + esc(p.rationale) + "</p>" : "") +
          (p.checks && p.checks.length ? '<ul class="checks">' + p.checks.map(function (c) { return '<li class="' + (c.ok ? "ok" : "bad") + '">' + ic(c.ok ? "check" : "x", "sm") + esc(c.name) + "</li>"; }).join("") + "</ul>" : "") +
          (p.status === "canary" && cmp && cmp.proposal_id === p.id ? cmpTable(cmp) : "") +
          '<div class="row-actions"><button class="btn sm ghost" data-cmp-pair="' + esc(p.version_id) + '">' + esc(T("opt.view_diff")) + "</button>" +
          (live ? (p.status === "pending" ? '<button class="btn sm" data-canary="' + esc(p.id) + '">' + esc(T("opt.start_canary")) + "</button>" : "") + '<button class="btn sm danger" data-discard="' + esc(p.id) + '">' + esc(T("opt.discard")) + '</button><button class="btn sm primary" data-promote="' + esc(p.id) + '" data-no="' + esc(v ? v.no : "") + '">' + esc(T("opt.promote")) + "</button>" : "") + "</div></div>";
      }).join("") : '<p class="muted">' + esc(T("opt.none")) + "</p>";
      mount('<div class="grid g-detail">' +
        '<section class="card"><div class="task-head"><div class="grow-col"><div class="td-title"><h1>' + esc(sk.name) + "</h1>" + idChip(T("sk.id"), sk.id) + "</div>" + (sk.description ? "<p>" + esc(sk.description) + "</p>" : "") + '<div class="chips"><span class="chip">' + ic("branch", "sm") + esc(T("ver.n_current", { n: sk.version_no })) + '</span><span class="chip">' + esc(plural(sk.used_by, "tasks.count_one", "tasks.count_many")) + '</span></div></div><div class="row-actions"><button class="btn sm" id="sk-manifest">' + esc(T("sm.title")) + '</button><button class="btn sm" id="sk-export" data-tip="' + esc(T("sk.export_tip", { n: sk.version_no })) + '">' + ic("download", "sm") + esc(T("sk.export")) + "</button></div></div>" +
        '<nav class="tabs sk-tabs" role="tablist" aria-label="' + esc(sk.name) + '">' + [["diff", T("sk.tab_diff")], ["files", T("sk.tab_files")]].map(function (x) { var on = x[0] === tab; return '<button type="button" role="tab" data-sktab="' + x[0] + '" class="' + (on ? "active" : "") + '" aria-selected="' + on + '" tabindex="' + (on ? 0 : -1) + '">' + esc(x[1]) + "</button>"; }).join("") + "</nav>" +
        '<div id="pane-diff" class="sk-pane' + (tab === "diff" ? "" : " hidden") + '" role="tabpanel"><div class="diff-bar"><label>' + esc(T("diff.from")) + ' <select id="d-a">' + vopt(a) + '</select></label><span aria-hidden="true">→</span><label>' + esc(T("diff.to")) + ' <select id="d-b">' + vopt(b) + '</select></label><button class="btn sm ghost" id="d-swap" title="' + esc(T("diff.swap")) + '">⇄</button><label class="check"><input type="checkbox" id="d-all"> ' + esc(T("diff.full")) + '</label></div><div id="diff" class="diff-host">' + skeleton("list") + "</div></div>" +
        '<div id="pane-files" class="sk-pane' + (tab === "files" ? "" : " hidden") + '" role="tabpanel"><div class="diff-bar fb-bar"><label>' + esc(T("fb.version")) + ' <select id="fb-v">' + vopt(fv) + '</select></label><small class="muted" id="fb-sum"></small></div><div class="fb"><div class="fb-tree" id="fb-tree" role="tree" aria-label="' + esc(T("fb.tree")) + '">' + skeleton("list") + '</div><div class="fb-view" id="fb-view"></div></div></div></section>' +
        '<div class="stack">' + repositorySourceHtml(r[5]) + '<section class="card"><div class="card-head"><span class="ico">' + ic("branch") + "</span><h3>" + esc(T("ver.tree")) + "</h3></div>" + tree + '<p class="hint mt">' + ic("info", "sm") + esc(T("ver.rb_pointer")) + "</p></section>" +
        '<section class="card"><div class="card-head"><span class="ico">' + ic("sparkle") + "</span><h3>" + esc(T("opt.title")) + '</h3></div><div class="seg" id="opt-mode" role="radiogroup">' + modes.map(function (m) { return '<button type="button" role="radio" aria-checked="' + (oset.mode === m[0]) + '" data-mode="' + m[0] + '" class="' + (oset.mode === m[0] ? "active" : "") + '">' + esc(m[1]) + "</button>"; }).join("") + '</div><p class="hint" id="opt-sub">' + esc((modes.filter(function (m) { return m[0] === oset.mode; })[0] || modes[0])[2]) + '</p><p class="notice">' + ic("shield", "sm") + esc(T("opt.never_auto")) + "</p>" +
        '<h4 class="mt">' + esc(T("opt.proposals")) + "</h4>" + propHtml + "</section></div></div>",
        "skills", [[T("nav.skills"), "#/skills"], [sk.name]]);
      wireRepositorySource(r[5], page());
      $("#sk-manifest").onclick=function(){skillManifestModal(id);};
      var diffLoaded = false, filesLoaded = false, fgen = 0, tcache = {}, fcache = {}, fkeys = [];
      // The address keeps the tab, the browsed version and file, so a reload or a shared link opens the same view.
      var syncHash = function () {
        var q = "a=" + enc($("#d-a").value) + "&b=" + enc($("#d-b").value);
        if (tab === "files") q += "&tab=files&v=" + enc(fv) + (ff ? "&f=" + enc(ff) : "");
        history.replaceState(null, "", "#/skills/" + enc(id) + "?" + q); lastHash = location.hash;
      };
      var showTab = function (t, noLoad) {
        tab = t;
        $$("[data-sktab]").forEach(function (b) { var on = b.getAttribute("data-sktab") === t; b.classList.toggle("active", on); b.setAttribute("aria-selected", String(on)); b.tabIndex = on ? 0 : -1; });
        $("#pane-diff").classList.toggle("hidden", t !== "diff");
        $("#pane-files").classList.toggle("hidden", t !== "files");
        if (noLoad) return;
        if (t === "diff" && !diffLoaded) load(); else if (t === "files" && !filesLoaded) loadTree(); else syncHash();
      };
      // File browser (idea: siteboon/claudecodeui file-tree + code-editor: a load counter drops answers
      // for a file that is no longer open; binary files get a placeholder instead of garbage).
      var loadTree = function () {
        filesLoaded = true;
        var g = ++fgen, v = fv, host = $("#fb-tree");
        var got = tcache[v] ? Promise.resolve(tcache[v]) : api(base + "/files?v=" + enc(v)).then(function (l) { tcache[v] = l || []; return tcache[v]; });
        got.then(function (list) {
          if (g !== fgen) return;
          var total = list.reduce(function (n, x) { return n + (x.size || 0); }, 0);
          $("#fb-sum").textContent = T("fb.sum", { n: list.length, size: fmtBytes(total) });
          if (!list.length) { ff = ""; host.innerHTML = '<p class="muted pad">' + esc(T("fb.empty")) + "</p>"; $("#fb-view").innerHTML = ""; syncHash(); return; }
          var paths = list.map(function (x) { return x.path; });
          if (paths.indexOf(ff) < 0) ff = pickSkillFile(paths);
          host.innerHTML = browseTree(list);
          openFile(ff);
        }).catch(function (e) { if (g === fgen) host.innerHTML = errorState(e.message); });
      };
      var openFile = function (p) {
        ff = p; syncHash();
        $$("#fb-tree [data-file]").forEach(function (b) { var on = b.getAttribute("data-file") === p; b.classList.toggle("active", on); b.tabIndex = on ? 0 : -1; if (on) b.setAttribute("aria-selected", "true"); else b.removeAttribute("aria-selected"); });
        var g = ++fgen, key = fv + "\n" + p, view = $("#fb-view");
        // Keep the previous file on screen (dimmed) until the new one arrives: no blank flash.
        if (!fcache[key]) view.classList.add("busy");
        var got = fcache[key] ? Promise.resolve(fcache[key]) : api(base + "/file?v=" + enc(fv) + "&path=" + enc(p)).then(function (f) {
          fcache[key] = f; fkeys.push(key);
          if (fkeys.length > 24) delete fcache[fkeys.shift()]; // a version's files never change; keep only the recent ones
          return f;
        });
        got.then(function (f) {
          if (g !== fgen) return;
          view.classList.remove("busy");
          showFile(f);
        }).catch(function (e) { if (g === fgen) { view.classList.remove("busy"); view.innerHTML = errorState(e.message); } });
      };
      // Markdown files open formatted (preview) or as source; the choice is remembered for every .md file.
      var mdMode = function () { try { return localStorage.getItem("tb_md_view") === "source" ? "source" : "preview"; } catch (e) { return "preview"; } };
      var showFile = function (f, focusMode) {
        var view = $("#fb-view");
        view.innerHTML = fileViewHtml(f, mdMode());
        view.scrollTop = 0;
        var cp = $("[data-fb-copy]", view); if (cp) cp.addEventListener("click", function () { copyText(f.text || "").catch(fail); });
        var dl = $("[data-fb-dl]", view); if (dl) dl.addEventListener("click", function () { exportSkill(id, fv, dl); });
        var segEl = $(".fb-mode", view);
        if (segEl) {
          var pick = function (m) { try { localStorage.setItem("tb_md_view", m); } catch (e) {} showFile(f, true); };
          $$("[data-fb-mode]", segEl).forEach(function (b) { b.addEventListener("click", function () { if (b.getAttribute("aria-checked") !== "true") pick(b.getAttribute("data-fb-mode")); }); });
          segEl.addEventListener("keydown", function (e) { if (e.key === "ArrowLeft" || e.key === "ArrowRight") { e.preventDefault(); pick(mdMode() === "source" ? "preview" : "source"); } });
          if (focusMode) { var on = $('[aria-checked="true"]', segEl); if (on) on.focus(); }
        }
      };
      $("#fb-view").addEventListener("click", function (e) {
        var a = e.target.closest("a[data-md-file], a[data-md-anchor]");
        if (!a) return;
        e.preventDefault();
        if (a.hasAttribute("data-md-anchor")) {
          var box = $(".fb-md", this), h = box && box.querySelector('[data-md-id="' + (window.CSS && CSS.escape ? CSS.escape(a.getAttribute("data-md-anchor")) : a.getAttribute("data-md-anchor")) + '"]');
          if (h) box.scrollTop = h.offsetTop - 8;
          return;
        }
        var p = a.getAttribute("data-md-file"), have = (tcache[fv] || []).some(function (x) { return x.path === p; });
        if (!have) { toast(T("fb.no_file", { p: p }), true); return; }
        var node = $('#fb-tree [data-file="' + (window.CSS && CSS.escape ? CSS.escape(p) : p) + '"]');
        for (var ul = node && node.parentNode.parentNode; ul && ul.id !== "fb-tree"; ul = ul.parentNode) if (ul.tagName === "UL" && ul.previousElementSibling && ul.previousElementSibling.hasAttribute("data-dir")) toggleDir(ul.previousElementSibling, true);
        openFile(p);
        if (node) node.scrollIntoView({ block: "nearest" });
      });
      var load = function () {
        var qa = $("#d-a").value, qb = $("#d-b").value;
        diffLoaded = true; syncHash();
        if (qa === qb) { $("#diff").innerHTML = '<p class="muted pad">' + esc(T("diff.same_ver")) + "</p>"; return; }
        api(base + "/diff?a=" + enc(qa) + "&b=" + enc(qb) + ($("#d-all").checked ? "&ctx=all" : "")).then(function (d) { $("#diff").innerHTML = diffView(d); }).catch(function (e) { $("#diff").innerHTML = errorState(e.message); });
      };
      ["#d-a", "#d-b", "#d-all"].forEach(function (s) { $(s).addEventListener("change", load); });
      $("#d-swap").addEventListener("click", function () { var x = $("#d-a").value; $("#d-a").value = $("#d-b").value; $("#d-b").value = x; load(); });
      $("#sk-export").addEventListener("click", function () { exportSkill(id, "", $("#sk-export")); });
      $$("[data-v-export]").forEach(function (bt) { bt.addEventListener("click", function () { exportSkill(id, bt.getAttribute("data-v-export"), bt); }); });
      $$("[data-cmp]").forEach(function (bt) { bt.addEventListener("click", function () { $("#d-a").value = bt.getAttribute("data-cmp"); $("#d-b").value = vers.current; showTab("diff", true); load(); $("#diff").scrollIntoView({ block: "nearest" }); }); });
      $$("[data-cmp-pair]").forEach(function (bt) { bt.addEventListener("click", function () { $("#d-a").value = vers.current; $("#d-b").value = bt.getAttribute("data-cmp-pair"); showTab("diff", true); load(); $("#diff").scrollIntoView({ block: "nearest" }); }); });
      var reload = function () { syncHash(); renderSkillDetail(id, new URLSearchParams(location.hash.split("?")[1] || "")); };
      $$("[data-srb]").forEach(function (bt) { bt.addEventListener("click", function () {
        var vid = bt.getAttribute("data-srb"), no = bt.getAttribute("data-no"), prev = vers.current, prevNo = (byId[prev] || {}).no;
        confirmDialog({ title: T("ver.rb_title", { n: no }), body: T("ver.rb_pointer"), ok: T("ver.rollback") }).then(function (y) {
          if (!y) return;
          api(base + "/rollback", { method: "POST", body: { version_id: vid } }).then(function () {
            reload();
            toastUndo(T("toast.rolled_back", { n: no }), function () { api(base + "/rollback", { method: "POST", body: { version_id: prev } }).then(function () { toast(T("toast.rolled_back", { n: prevNo })); reload(); }).catch(fail); });
          }).catch(fail);
        });
      }); });
      $$("#opt-mode [data-mode]").forEach(function (bt) { bt.addEventListener("click", function () {
        rpc("skills.optimize_settings", { skill_id: id, mode: bt.getAttribute("data-mode") }).then(function () { toast(T("opt.mode_saved")); reload(); }).catch(fail);
      }); });
      $$("[data-canary]").forEach(function (bt) { bt.addEventListener("click", function () { rpc("skills.canary_start", { proposal_id: bt.getAttribute("data-canary") }).then(function () { toast(T("opt.canary_started")); reload(); }).catch(fail); }); });
      $$("[data-discard]").forEach(function (bt) { bt.addEventListener("click", function () { confirmDialog({ title: T("opt.discard_title"), body: T("opt.discard_body"), ok: T("opt.discard"), danger: true }).then(function (y) { if (y) rpc("skills.discard", { proposal_id: bt.getAttribute("data-discard") }).then(function () { toast(T("opt.discarded")); reload(); }).catch(fail); }); }); });
      $$("[data-promote]").forEach(function (bt) { bt.addEventListener("click", function () { confirmDialog({ title: T("opt.promote_title", { n: bt.getAttribute("data-no") }), body: T("opt.promote_body"), ok: T("opt.promote") }).then(function (y) { if (y) rpc("skills.promote", { proposal_id: bt.getAttribute("data-promote") }).then(function () { toast(T("opt.promoted")); reload(); }).catch(fail); }); }); });
      $$("[data-sktab]").forEach(function (b) {
        b.addEventListener("click", function () { showTab(b.getAttribute("data-sktab")); });
        b.addEventListener("keydown", function (e) {
          if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
          e.preventDefault();
          var nt = tab === "diff" ? "files" : "diff"; showTab(nt); $('[data-sktab="' + nt + '"]').focus();
        });
      });
      $("#fb-v").addEventListener("change", function () { fv = $("#fb-v").value; loadTree(); });
      $$("[data-v-files]").forEach(function (bt) { bt.addEventListener("click", function () {
        fv = bt.getAttribute("data-v-files"); $("#fb-v").value = fv; showTab("files", true); loadTree();
        $(".sk-tabs").scrollIntoView({ block: "nearest" });
      }); });
      var ftree = $("#fb-tree");
      ftree.addEventListener("click", function (e) {
        var d = e.target.closest("[data-dir]"), f = e.target.closest("[data-file]");
        if (d) toggleDir(d); else if (f && f.getAttribute("data-file") !== ff) openFile(f.getAttribute("data-file"));
      });
      ftree.addEventListener("keydown", function (e) { treeKeys(ftree, e); });
      if (tab === "files") loadTree(); else load();
    });
  }
  // pickSkillFile: the file to show first — SKILL.md at the top, else the shallowest SKILL.md, else the first file.
  function pickSkillFile(paths) {
    if (paths.indexOf("SKILL.md") >= 0) return "SKILL.md";
    var sk = paths.filter(function (p) { return /(^|\/)SKILL\.md$/i.test(p); }).sort(function (a, b) { return a.split("/").length - b.split("/").length; })[0];
    return sk || paths[0];
  }
  // browseTree renders a version's files as a clickable tree: folders first (collapsible), sizes in the tooltip.
  function browseTree(files) {
    var root = { d: {}, f: [] };
    files.forEach(function (x) {
      var parts = x.path.split("/"), n = root;
      parts.slice(0, -1).forEach(function (p) { n = n.d[p] = n.d[p] || { d: {}, f: [] }; });
      n.f.push({ name: parts[parts.length - 1], path: x.path, size: x.size });
    });
    var walk = function (n, pre) {
      return '<ul role="group">' + Object.keys(n.d).sort().map(function (k) {
        return '<li role="none"><button type="button" class="fb-node dir" role="treeitem" aria-expanded="true" tabindex="-1" data-dir="' + esc(pre + k) + '">' + ic("chev", "sm") + ic("folder", "sm") + "<span>" + esc(k) + "</span></button>" + walk(n.d[k], pre + k + "/") + "</li>";
      }).join("") + n.f.sort(function (a, b) { return a.name < b.name ? -1 : a.name > b.name ? 1 : 0; }).map(function (f) {
        return '<li role="none"><button type="button" class="fb-node' + (/^SKILL\.md$/i.test(f.name) ? " key" : "") + '" role="treeitem" tabindex="-1" data-file="' + esc(f.path) + '" data-tip="' + esc(f.path + " · " + fmtBytes(f.size)) + '">' + ic("doc", "sm") + "<span>" + esc(f.name) + "</span></button></li>";
      }).join("") + "</ul>";
    };
    return walk(root, "");
  }
  function toggleDir(b, open) {
    var ul = b.nextElementSibling, now = b.getAttribute("aria-expanded") === "true";
    if (open === undefined) open = !now;
    b.setAttribute("aria-expanded", String(open));
    if (ul) ul.classList.toggle("hidden", !open);
  }
  // Arrow keys walk the visible rows; → opens a folder, ← closes it or moves to its folder.
  function treeKeys(tree, e) {
    var rows = $$(".fb-node", tree).filter(function (b) { return !b.closest("ul.hidden"); });
    var i = rows.indexOf(document.activeElement);
    if (i < 0) return;
    var cur = rows[i], dir = cur.hasAttribute("data-dir"), go = function (b) { if (b) { e.preventDefault(); b.focus(); } };
    if (e.key === "ArrowDown") go(rows[i + 1]);
    else if (e.key === "ArrowUp") go(rows[i - 1]);
    else if (e.key === "Home") go(rows[0]);
    else if (e.key === "End") go(rows[rows.length - 1]);
    else if (e.key === "ArrowRight" && dir) { e.preventDefault(); if (cur.getAttribute("aria-expanded") === "true") go(rows[i + 1]); else toggleDir(cur, true); }
    else if (e.key === "ArrowLeft") {
      e.preventDefault();
      if (dir && cur.getAttribute("aria-expanded") === "true") toggleDir(cur, false);
      else { var li = cur.parentNode.parentNode.closest("li"); go(li && li.querySelector(".fb-node")); }
    }
  }
  // fileViewHtml: read-only view of one file with line numbers. Text keeps its line breaks (CRLF shown as
  // one break); binary and very large files get a short note and the version download instead.
  function isMdPath(p) { return /\.(md|markdown|mdown|mkd)$/i.test(p || ""); }
  function fileViewHtml(f, mode) {
    var meta = [fmtBytes(f.size)], md = f.kind === "text" && isMdPath(f.path) && !!f.text, pv = md && mode !== "source";
    if (f.kind === "text") { meta.push(T("fb.lines", { n: f.lines })); if (f.eol) meta.push(T("fb.eol_" + f.eol)); }
    var seg = md ? '<div class="seg sm fb-mode" role="radiogroup" aria-label="' + esc(T("fb.view_as")) + '">' + [["preview", T("fb.preview")], ["source", T("fb.source")]].map(function (x) {
      var on = (x[0] === "preview") === pv;
      return '<button type="button" role="radio" data-fb-mode="' + x[0] + '" aria-checked="' + on + '" tabindex="' + (on ? 0 : -1) + '" class="' + (on ? "active" : "") + '" data-tip="' + esc(T("fb." + x[0] + "_tip")) + '">' + esc(x[1]) + "</button>";
    }).join("") + "</div>" : "";
    var head = '<div class="fb-head">' + ic("doc", "sm") + '<code class="fb-path">' + esc(f.path) + '</code><small class="muted fb-meta" data-tip="' + esc(meta.join(" · ")) + '">' + esc(fmtBytes(f.size)) + "</small>" + seg +
      (f.kind === "text" ? '<button type="button" class="icon-btn sm" data-fb-copy data-tip="' + esc(T("fb.copy")) + '" aria-label="' + esc(T("fb.copy")) + '">' + ic("copy", "sm") + "</button>" : "") + "</div>";
    if (f.kind !== "text") return head + '<div class="fb-none">' + ic("doc", "lg") + "<strong>" + esc(T(f.kind === "binary" ? "fb.binary" : "fb.too_large")) + "</strong><small>" + esc(T("fb.none_sub")) + '</small><button type="button" class="btn sm" data-fb-dl>' + ic("download", "sm") + esc(T("sk.export")) + "</button></div>";
    if (!f.text) return head + '<p class="muted pad">' + esc(T("fb.empty_file")) + "</p>";
    if (pv) return head + '<div class="fb-md md" tabindex="0">' + mdHtml(f.text, f.path.split("/").slice(0, -1).join("/")) + "</div>";
    var text = f.text.replace(/\r\n?/g, "\n").replace(/\n$/, ""), n = text.split("\n").length, nums = [];
    for (var k = 1; k <= n; k++) nums.push(k);
    return head + '<div class="fb-code"><pre class="fb-ln" aria-hidden="true">' + nums.join("\n") + '</pre><pre class="fb-tx" tabindex="0">' + esc(text) + "</pre></div>";
  }
