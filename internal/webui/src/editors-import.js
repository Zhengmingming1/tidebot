  // ---------- v0.1.2 shared helpers ----------
  function upload(path, fd) {
    return netFetch(path, { method: "POST", headers: { "Accept": "application/json", "X-CSRF-Token": state.csrf }, body: fd, credentials: "same-origin" }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (d) {
        if (res.status === 401) { showLogin(); throw new Error(T("err.sign_in")); }
        if (!res.ok) { var e = new Error(d.error || res.statusText); e.status = res.status; e.data = d; throw e; }
        return d;
      });
    });
  }
  // Colors go through CSSOM (CSP blocks inline style attributes).
  // A button's own colour also tints its icon tile (same hue at low alpha, so it reads on light and dark).
  function applyColors(root) {
    $$("[data-color]", root).forEach(function (el) {
      var c = el.getAttribute("data-color");
      if (!/^#[0-9a-fA-F]{6}$/.test(c)) return;
      el.style.setProperty("--qa-color", c);
      el.style.setProperty("--qa-bg", "rgba(" + parseInt(c.slice(1, 3), 16) + ", " + parseInt(c.slice(3, 5), 16) + ", " + parseInt(c.slice(5, 7), 16) + ", .16)");
    });
  }
  // Icons without a chosen colour still get one: a stable hue per icon name.
  var HUES = ["blue", "teal", "violet", "orange", "pink", "green", "cyan", "indigo"];
  function iconHue(name) { var h = 0; for (var i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0; return HUES[h % HUES.length]; }
  var SWATCHES = ["", "#2f6fed", "#0f9d8a", "#7b5cd6", "#d9822b", "#d64545", "#3b8b3b", "#5a6b7d"];
  // Secondary help lives in a tooltip next to the label (hover or keyboard focus), not under every field.
  // An id the user may need (support, the CLI) without it taking a row on the page: a small
  // "ID" chip whose tooltip shows the id; clicking copies it.
  function idChip(label, id) { return '<button type="button" class="id-chip" data-copyid="' + esc(id) + '" data-tip="' + esc(T("id.tip", { l: label, id: id })) + '" aria-label="' + esc(T("id.copy", { l: label })) + '">ID</button>'; }
  document.addEventListener("click", function (e) { var b = e.target.closest && e.target.closest("[data-copyid]"); if (b) copyText(b.getAttribute("data-copyid")).catch(fail); });
  document.addEventListener("click", function (e) { var b = e.target.closest && e.target.closest("[data-chg-jump]"); if (b) state.chgJump = decodeURIComponent((b.getAttribute("href") || "").replace(/^#\/runs\//, "")); });
  function tip(text) { return text ? '<span class="info-tip" tabindex="0" role="img" aria-label="' + esc(text) + '" data-tip="' + esc(text) + '">' + ic("info", "sm") + "</span>" : ""; }
  function fieldHtml(label, inner, hint, extra) { return '<label class="field"><span>' + esc(label) + tip(hint) + (extra || "") + "</span>" + inner + "</label>"; }
  function opt(v, label, sel) { return '<option value="' + esc(v) + '"' + (sel ? " selected" : "") + ">" + esc(label) + "</option>"; }
  // Collect dropped files; folders are walked so the server sees relative paths.
  function droppedFiles(dt) {
    var items = dt.items ? Array.prototype.slice.call(dt.items) : [];
    var entries = items.map(function (it) { return it.webkitGetAsEntry ? it.webkitGetAsEntry() : null; }).filter(Boolean);
    if (!entries.length || (entries.length === 1 && entries[0].isFile)) return Promise.resolve({ single: dt.files[0] });
    var out = [];
    function walk(entry, prefix) {
      if (entry.isFile) return new Promise(function (ok, bad) { entry.file(function (f) { out.push({ file: f, path: prefix + f.name }); ok(); }, bad); });
      return new Promise(function (ok, bad) {
        var rd = entry.createReader(), all = [];
        (function more() { rd.readEntries(function (es) { if (!es.length) { Promise.all(all.map(function (e) { return walk(e, prefix + entry.name + "/"); })).then(ok, bad); } else { all = all.concat(Array.prototype.slice.call(es)); more(); } }, bad); })();
      });
    }
    return Promise.all(entries.map(function (e) { return walk(e, ""); })).then(function () { return { many: out }; });
  }
  function dropZone(id, accept, folder) {
    return '<div class="drop" id="' + id + '">' + ic("upload", "lg") + "<strong>" + esc(T("up.drop")) + "</strong><small>" + esc(T(folder ? "up.drop_sub_skill" : "up.drop_sub_plugin")) + '</small><div class="row-actions"><label class="btn sm">' + esc(T("up.pick_file")) + '<input type="file" class="hidden" data-pick="file" accept="' + accept + '"></label>' +
      (folder ? '<label class="btn sm">' + esc(T("up.pick_folder")) + '<input type="file" class="hidden" data-pick="dir" webkitdirectory multiple></label>' : "") + "</div></div>";
  }
  function wireDrop(root, id, onForm) {
    var z = $("#" + id, root);
    var many = function (list) { var fd = new FormData(); list.forEach(function (x) { fd.append("files", x.file, x.file.name); fd.append("paths", x.path); }); onForm(fd); };
    ["dragenter", "dragover"].forEach(function (ev) { z.addEventListener(ev, function (e) { e.preventDefault(); z.classList.add("over"); }); });
    z.addEventListener("dragleave", function () { z.classList.remove("over"); });
    z.addEventListener("drop", function (e) {
      e.preventDefault(); z.classList.remove("over");
      droppedFiles(e.dataTransfer).then(function (r) {
        if (r.single) { var fd = new FormData(); fd.append("file", r.single, r.single.name); onForm(fd); } else many(r.many);
      }).catch(fail);
    });
    $$("[data-pick]", z).forEach(function (inp) {
      inp.addEventListener("change", function () {
        if (!inp.files.length) return;
        if (inp.getAttribute("data-pick") === "dir") many(Array.prototype.slice.call(inp.files).map(function (f) { return { file: f, path: f.webkitRelativePath || f.name }; }));
        else { var fd = new FormData(); fd.append("file", inp.files[0], inp.files[0].name); onForm(fd); }
        inp.value = "";
      });
    });
  }

  function fmtBytes(n) { n = Math.max(0, n || 0); return n < 1024 ? n + " B" : n < 1048576 ? (n / 1024).toFixed(1) + " KB" : n < 1073741824 ? (n / 1048576).toFixed(1) + " MB" : (n / 1073741824).toFixed(2) + " GB"; }
  // fileTree renders "a/b/c.md" paths as an indented tree; folders first.
  function fileTree(files) {
    var root = { d: {}, f: [] };
    files.forEach(function (x) {
      var parts = x.path.split("/"), n = root;
      parts.slice(0, -1).forEach(function (p) { n = n.d[p] = n.d[p] || { d: {}, f: [] }; });
      n.f.push({ name: parts[parts.length - 1], size: x.size });
    });
    var walk = function (n) {
      return "<ul>" + Object.keys(n.d).sort().map(function (k) { return '<li class="dir">' + ic("folder", "sm") + esc(k) + "/" + walk(n.d[k]) + "</li>"; }).join("") +
        n.f.sort(function (a, b) { return a.name < b.name ? -1 : 1; }).map(function (f) { return '<li class="file' + (f.name === "SKILL.md" ? " key" : "") + '">' + ic("doc", "sm") + esc(f.name) + '<small class="muted">' + esc(fmtBytes(f.size)) + "</small></li>"; }).join("") + "</ul>";
    };
    return '<div class="tree">' + walk(root) + "</div>";
  }
  // ---------- skill import ----------
  function skillImportModal(done) {
    modal('<div class="card-head"><span class="ico">' + ic("upload") + "</span><h2>" + esc(T("sk.import")) + "</h2></div>" + dropZone("sk-drop", ".zip,.skill,.tar.gz,.tgz", true) + '<div id="sk-prev"></div><div class="actions" id="sk-foot"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button></div>", function (root, close) {
      var prev = $("#sk-prev", root), foot = $("#sk-foot", root), uploadSeq = 0;
      function cancelOnly() {
        foot.innerHTML = '<button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button>";
        $$("[data-close]", foot).forEach(function (b) { b.addEventListener("click", close); });
      }
      function uploadError(e) {
        var data = e.data || {}, message = e.message, candidates = [];
        if (data.code === "skill_root_missing") message = T("sk.root_missing");
        if (data.code === "multiple_skills") {
          message = T("sk.multiple_skills");
          candidates = Array.isArray(data.candidates) ? data.candidates.filter(function (p) { return typeof p === "string"; }) : [];
        }
        return '<div class="notice bad" role="alert">' + ic("x", "sm") + '<div>' + esc(message) +
          (candidates.length ? '<ul class="list-plain">' + candidates.map(function (p) { return '<li><code>' + esc(p) + '</code></li>'; }).join("") + '</ul>' : "") + '</div></div>';
      }
      var doImport = function (p, name, mode, btn) {
        var seq = uploadSeq;
        btn.disabled = true;
        api("/api/skills/import", { method: "POST", body: { stage_id: p.stage_id, name: name, mode: mode } }).then(function () {
          if (seq !== uploadSeq || !document.contains(root)) return;
          close(); toast(T(mode === "version" ? "toast.skill_version" : "toast.skill_imported")); done();
        }).catch(function (e) {
          if (seq !== uploadSeq || !document.contains(root)) return;
          btn.disabled = false;
          if (e.status === 409 && e.data && e.data.existing) { p.existing = e.data.existing; show(p, name); return; }
          fail(e);
        });
      };
      function show(p, name) {
        var ex = p.existing;
        prev.innerHTML = '<div class="preview"><form id="sk-form">' + fieldHtml(T("tf.name"), '<input name="name" required maxlength="64" value="' + esc(name || p.name) + '">', T("sk.name_hint")) +
          '<div class="field"><span>' + esc(T("sk.desc")) + '</span><p class="desc-box">' + esc(p.description || T("sk.no_desc")) + "</p></div>" +
          (typeof p.source_root === "string" && p.source_root && p.source_root !== "." ? '<p class="notice">' + ic("folder", "sm") + '<span>' + esc(T("sk.source_root", { path: p.source_root })) + '</span></p>' : "") +
          '<ul class="checks">' + (p.checks || []).map(function (c) { return '<li class="' + (c.ok ? "ok" : "warn") + '">' + ic(c.ok ? "check" : "info", "sm") + esc(T("sk.chk_" + c.key)) + "</li>"; }).join("") + "</ul>" +
          '<details class="ftree" open><summary>' + esc(T("sk.files_n", { n: p.files })) + " · " + esc(fmtBytes(p.size || 0)) + "</summary>" + fileTree(p.tree || []) + "</details>" +
          (ex ? '<p class="notice">' + ic("info", "sm") + esc(T("sk.exists", { n: ex.version_no, next: ex.next_no || ex.version_no + 1 })) + "</p>" : "") + "</form></div>";
        foot.innerHTML = '<button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button>" +
          (ex ? '<button type="button" class="btn primary" id="sk-ver">' + ic("branch", "sm") + esc(T("sk.as_version")) + "</button>" : '<button type="button" class="btn primary" id="sk-go">' + esc(T("sk.import_btn")) + "</button>");
        $$("[data-close]", foot).forEach(function (b) { b.addEventListener("click", close); });
        var f = $("#sk-form", prev);
        var nm = function () { return f.name.value.trim(); };
        f.name.addEventListener("input", function () { if (p.existing && nm() !== name) { p.existing = null; show(p, nm()); f = $("#sk-form", prev); f.name.focus(); f.name.setSelectionRange(f.name.value.length, f.name.value.length); } });
        f.addEventListener("submit", function (e) { e.preventDefault(); });
        var go = $("#sk-go", foot), ver = $("#sk-ver", foot);
        if (go) go.addEventListener("click", function () { if (f.reportValidity()) doImport(p, nm(), "", go); });
        if (ver) ver.addEventListener("click", function () { doImport(p, nm(), "version", ver); });
      }
      wireDrop(root, "sk-drop", function (fd) {
        var seq = ++uploadSeq;
        cancelOnly();
        prev.innerHTML = '<p class="muted">' + esc(T("up.reading")) + "</p>";
        upload("/api/skills/upload", fd).then(function (p) {
          if (seq === uploadSeq && document.contains(root)) show(p, p.name);
        }).catch(function (e) {
          if (seq === uploadSeq && document.contains(root)) prev.innerHTML = uploadError(e);
        });
      });
    });
  }

  // ---------- button editor ----------
  // Icon suggestion for a button: first rule whose words appear in the label, else in the
  // target (task name/instruction, skill name, prompt). Order = most specific first.
  var ICON_RULES = [
    ["receipt", /账单|发票|报销|对账|invoice|billing|\bbills?\b|expense/i],
    ["doc", /日报|周报|月报|报告|报表|总结|汇总|摘要|纪要|文档|\breports?\b|\bsummar|\bdigest|\bdocs?\b|\bnotes?\b|\brecap/i],
    ["rss", /\brss\b|订阅源|\bfeeds?\b|新闻|\bnews\b/i],
    ["rocket", /部署|发布|上线|\bdeploy|\brelease|\bship\b|\blaunch/i],
    ["database", /备份|数据库|\bback ?ups?\b|\bdatabase|\bdb\b|\bsql\b|\bdump\b/i],
    ["trash", /清理|清空|删除|\bclean|\bpurge|\bdelete|\bprune/i],
    ["sync", /同步|拉取|刷新|\bsync|\bpull\b|\brefresh|\bfetch/i],
    ["git", /\bgit(hub|lab)?\b|提交|\bcommits?\b|\bprs?\b|pull request|合并请求|\bmerge\b|分支|\bbranch/i],
    ["bug", /\bbugs?\b|缺陷|故障|报错|工单|\bissues?\b|\btriage|\bincident/i],
    ["mail", /邮件|邮箱|\be?-?mail|\binbox|newsletter/i],
    ["chat", /消息|通知|群聊|企业微信|钉钉|飞书|\bslack\b|\bwecom\b|dingtalk|\bnotify|\bmessage|\bchat\b/i],
    ["calendar", /日程|会议|日历|\bcalendar|\bmeeting|\bagenda/i],
    ["pulse", /监控|健康|巡检|可用性|检查|构建状态|\bci\b|\bbuilds?\b|\bcheck\b|\bmonitor|\bhealth|\buptime|\bping\b/i],
    ["pie", /统计|分析|指标|图表|看板|\banalytics|\bstats?\b|\bmetrics?|\bchart|\bkpi|\bdashboard/i],
    ["shield", /安全|审计|漏洞|权限|\bsecurity|\baudit|\bvuln|\bcve\b/i],
    ["code", /代码|重构|审查|\bcode\b|\brefactor|\breview|\blint/i],
    ["globe", /网页|网站|抓取|爬取|\bweb\b|\bsite\b|\bscrape|\bcrawl|\bwebsite/i],
    ["download", /下载|导出|\bdownload|\bexport/i],
    ["upload", /上传|\bupload/i],
    ["book", /翻译|阅读|学习|论文|\btranslat|\bread\b|\blearn|\bpaper/i],
    ["image", /图片|截图|海报|\bimages?\b|\bphotos?\b|\bscreenshot/i],
    ["cloud", /云盘|对象存储|\bcloud\b|\bs3\b|\boss\b/i],
    ["cpu", /性能|压测|\bperf|\bbenchmark|\bcpu\b|\bload test/i],
    ["terminal", /脚本|命令|终端|\bscript|\bshell\b|\bcommand|\bcli\b/i],
    ["clock", /提醒|闹钟|\bremind|\btimer\b/i],
    ["star", /收藏|精选|\bstar\b|\bfavou?rite/i]
  ];
  function suggestIcon(label, target) {
    for (var pass = 0; pass < 2; pass++) {
      var text = pass ? target : label;
      if (!text) continue;
      for (var i = 0; i < ICON_RULES.length; i++) { var m = text.match(ICON_RULES[i][1]); if (m && P[ICON_RULES[i][0]]) return { icon: ICON_RULES[i][0], word: m[0].trim(), from: pass ? "target" : "label" }; }
    }
    return null;
  }
  // First clause of a task instruction, as a card subtitle (≤ 60 chars).
  function firstClause(text) {
    var t = String(text || "").replace(/\s+/g, " ").trim(), m = t.split(/[。！？!?\n]|\.\s/)[0] || t;
    return Array.from(m).length > 60 ? Array.from(m).slice(0, 59).join("") + "…" : m;
  }
  function buttonModal(list, idx, tasks, skills, save) {
    var b = idx == null ? { id: "", label: "", description: "", icon: "bolt", color: "", task_id: "", skill_id: "", skill_version_id: "", prompt: "", params: [] } : JSON.parse(JSON.stringify(list[idx]));
    b.params = b.params || [];
    var mode = b.task_id ? "task" : b.skill_id ? "skill" : "prompt";
    var icons = Object.keys(P).filter(function (k) { return ["chev", "more", "x", "circle"].indexOf(k) < 0; });
    modal('<div class="card-head"><span class="ico">' + ic("buttons") + "</span><h2>" + esc(T(idx == null ? "bt.new" : "bt.edit")) + '</h2></div><div class="bt-edit"><form id="bf">' +
      '<div class="form-row">' + fieldHtml(T("bt.label"), '<input name="label" required maxlength="40" value="' + esc(b.label) + '">', "", '<span class="af-pill hidden" data-af="label" data-tip="' + esc(T("bt.af_label")) + '">' + ic("sparkle", "sm") + esc(T("af.auto")) + "</span>") + fieldHtml(T("bt.desc"), '<input name="description" maxlength="60" value="' + esc(b.description) + '">', "", '<span class="af-pill hidden" data-af="description" data-tip="' + esc(T("bt.af_desc")) + '">' + ic("sparkle", "sm") + esc(T("af.auto")) + "</span>") + "</div>" +
      '<div class="field"><span>' + esc(T("bt.icon")) + '<span class="af-pill hidden" data-af="icon" data-tip="">' + ic("sparkle", "sm") + esc(T("af.auto")) + '</span></span><input id="ic-q" type="search" placeholder="' + esc(T("bt.icon_search")) + '"><div class="icon-grid" id="ic-grid">' + icons.map(function (k) { return '<button type="button" class="ic-cell' + (k === b.icon ? " on" : "") + '" data-ic="' + k + '" title="' + k + '">' + ic(k) + "</button>"; }).join("") + "</div></div>" +
      '<div class="field"><span>' + esc(T("bt.color")) + '</span><div class="swatches">' + SWATCHES.map(function (c) { return '<button type="button" class="sw' + (c === (b.color || "") ? " on" : "") + (c ? "" : " none") + '" data-sw="' + c + '" data-color="' + c + '" aria-label="' + esc(c || T("bt.color_default")) + '"></button>'; }).join("") + "</div></div>" +
      '<div class="field"><span>' + esc(T("bt.action")) + '</span><div class="seg" id="bt-mode">' + [["prompt", T("bt.m_prompt")], ["skill", T("bt.m_skill")], ["task", T("bt.m_task")]].map(function (m) { return '<button type="button" data-m="' + m[0] + '" class="' + (m[0] === mode ? "active" : "") + '">' + esc(m[1]) + "</button>"; }).join("") + "</div></div>" +
      '<div data-for="task">' + fieldHtml(T("col.task"), '<select name="task_id">' + tasks.map(function (t) { return opt(t.id, t.name, t.id === b.task_id); }).join("") + "</select>") + "</div>" +
      '<div data-for="skill" class="form-row">' + fieldHtml(T("bt.skill"), '<select name="skill_id">' + skills.map(function (s) { return opt(s.id, s.name, s.id === b.skill_id); }).join("") + "</select>") + fieldHtml(T("bt.skill_ver"), '<select name="skill_version_id"><option value="">' + esc(T("bt.latest")) + "</option></select>") + "</div>" +
      '<div data-for="prompt skill">' + fieldHtml(T("bt.prompt_label"), '<textarea name="prompt" maxlength="8000">' + esc(b.prompt) + "</textarea>", T("bt.params_hint")) +
      '<div class="field"><span>' + esc(T("bt.params")) + '</span><div id="pl"></div><button type="button" class="btn sm" id="p-add">' + ic("plus", "sm") + esc(T("bt.param_add")) + "</button></div></div>" +
      '</form><div class="bt-prev"><small class="muted">' + esc(T("bt.preview")) + '</small><div id="bt-card"></div></div></div>' +
      '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary" id="bt-save">' + esc(T("btn.save")) + "</button></div>", function (root, close) {
      var f = $("#bf", root);
      var drawParams = function () {
        $("#pl", root).innerHTML = b.params.map(function (p, i) { return '<div class="param-row"><input data-pn="' + i + '" placeholder="' + esc(T("bt.param_name")) + '" maxlength="32" value="' + esc(p.name) + '"><input data-pd="' + i + '" placeholder="' + esc(T("bt.param_default")) + '" maxlength="200" value="' + esc(p.default) + '"><button type="button" class="btn sm ghost" data-px="' + i + '">' + ic("x", "sm") + "</button></div>"; }).join("");
        $$("[data-pn]", root).forEach(function (x) { x.addEventListener("input", function () { b.params[+x.getAttribute("data-pn")].name = x.value; }); });
        $$("[data-pd]", root).forEach(function (x) { x.addEventListener("input", function () { b.params[+x.getAttribute("data-pd")].default = x.value; }); });
        $$("[data-px]", root).forEach(function (x) { x.addEventListener("click", function () { b.params.splice(+x.getAttribute("data-px"), 1); drawParams(); }); });
      };
      var preview = function () {
        var sub = mode === "task" ? T("bt.runs_task", { name: (f.task_id.selectedOptions[0] || {}).text || "—" }) : mode === "skill" ? T("bt.runs_skill", { name: (f.skill_id.selectedOptions[0] || {}).text || "—" }) : T("bt.prompt", { p: f.prompt.value || "…" });
        $("#bt-card", root).innerHTML = '<div class="card qa fill' + (b.color ? " colored" : "") + '" data-color="' + esc(b.color) + '"><span class="qa-ico">' + ic(b.icon, "lg") + "</span><strong>" + esc(f.label.value || T("bt.label")) + "</strong><small>" + esc(f.description.value || sub) + "</small></div>";
        applyColors($("#bt-card", root));
      };
      var showMode = function () {
        $$("[data-m]", root).forEach(function (x) { x.classList.toggle("active", x.getAttribute("data-m") === mode); });
        $$("[data-for]", root).forEach(function (x) { x.classList.toggle("hidden", x.getAttribute("data-for").split(" ").indexOf(mode) < 0); });
        preview();
      };
      var loadVers = function () {
        var sel = f.skill_version_id;
        if (!f.skill_id.value) { sel.innerHTML = '<option value="">' + esc(T("bt.latest")) + "</option>"; return; }
        api("/api/skills/" + enc(f.skill_id.value) + "/versions").then(function (v) {
          sel.innerHTML = '<option value="">' + esc(T("bt.latest")) + "</option>" + v.versions.slice().reverse().map(function (x) { return opt(x.id, T("bt.pin_v", { n: x.no }), x.id === b.skill_version_id); }).join("");
        }).catch(fail);
      };
      applyColors(root);
      drawParams(); loadVers(); showMode();
      // Auto-fill: icon from the label/target until an icon is clicked; label and subtitle
      // from the chosen task/skill while the user hasn't typed their own.
      var creating = idx == null, auto = {}, mine = { icon: !creating, label: !creating && !!b.label, description: !creating && !!b.description };
      var afPill = function (k, on, tipText) { var x = $('[data-af="' + k + '"]', root); if (!x) return; x.classList.toggle("hidden", !on); if (tipText != null) x.setAttribute("data-tip", tipText); };
      var targetInfo = function () {
        if (mode === "task") { var t = tasks.filter(function (x) { return x.id === f.task_id.value; })[0]; return t ? { name: t.name, desc: firstClause(t.prompt || t.goal || ""), text: t.name + " " + (t.prompt || "") } : null; }
        if (mode === "skill") { var k = skills.filter(function (x) { return x.id === f.skill_id.value; })[0]; return k ? { name: k.name, desc: firstClause(k.description || ""), text: k.name + " " + (k.description || "") + " " + f.prompt.value } : null; }
        return { name: "", desc: "", text: f.prompt.value };
      };
      var putText = function (k, v) {
        if (mine[k] || !v || f[k].value === v) return;
        if (f[k].value && f[k].value !== auto[k]) return;
        f[k].value = Array.from(v).slice(0, +f[k].getAttribute("maxlength") || 60).join(""); auto[k] = f[k].value; afPill(k, true);
      };
      var autofill = function () {
        var ti = targetInfo() || { name: "", desc: "", text: "" };
        if (mode !== "prompt") { putText("label", ti.name); putText("description", ti.desc); }
        if (mine.icon) return;
        var sug = suggestIcon(f.label.value, ti.text);
        if (!sug || sug.icon === b.icon) { if (sug) afPill("icon", true); return; }
        b.icon = sug.icon;
        $$("[data-ic]", root).forEach(function (y) { y.classList.toggle("on", y.getAttribute("data-ic") === sug.icon); });
        var cell = $('[data-ic="' + sug.icon + '"]', root); if (cell) cell.scrollIntoView({ block: "nearest" });
        afPill("icon", true, T(sug.from === "label" ? "bt.af_icon_label" : "bt.af_icon_target", { w: sug.word }));
        preview();
      };
      ["label", "description"].forEach(function (k) { f[k].addEventListener("input", function (e) { if (e.isTrusted && f[k].value !== auto[k]) { mine[k] = !!f[k].value; afPill(k, false); } }); });
      if (creating) {
        f.label.addEventListener("input", autofill); f.prompt.addEventListener("input", autofill);
        f.task_id.addEventListener("change", autofill); f.skill_id.addEventListener("change", autofill);
        $$("[data-m]", root).forEach(function (x) { x.addEventListener("click", function () { setTimeout(autofill, 0); }); });
      }
      imeInput($("#ic-q", root), 0, function (val) { var q = val.trim().toLowerCase(); $$("[data-ic]", root).forEach(function (x) { x.classList.toggle("hidden", q && x.getAttribute("data-ic").indexOf(q) < 0); }); });
      $$("[data-ic]", root).forEach(function (x) { x.addEventListener("click", function (e) { if (e.isTrusted) { mine.icon = true; afPill("icon", false); } b.icon = x.getAttribute("data-ic"); $$("[data-ic]", root).forEach(function (y) { y.classList.toggle("on", y === x); }); preview(); }); });
      $$("[data-sw]", root).forEach(function (x) { x.addEventListener("click", function () { b.color = x.getAttribute("data-sw"); $$("[data-sw]", root).forEach(function (y) { y.classList.toggle("on", y === x); }); preview(); }); });
      $$("[data-m]", root).forEach(function (x) { x.addEventListener("click", function () { mode = x.getAttribute("data-m"); showMode(); }); });
      f.skill_id.addEventListener("change", function () { b.skill_version_id = ""; loadVers(); });
      var dep=document.createElement("button");dep.type="button";dep.className="btn";dep.textContent=T("dep.check");
      var foot=$(".actions",root);if(foot)foot.prepend(dep);
      dep.onclick=function(){
        if(mode==="task"&&f.task_id.value)dependencyCheck({kind:"task",id:f.task_id.value});
        else dependencyCheck({kind:"draft",prompt:f.prompt.value,skill_bindings:mode==="skill"&&f.skill_id.value?[{skill_id:f.skill_id.value,role:"primary",mode:f.skill_version_id.value?"pinned":"follow",version_id:f.skill_version_id.value}]:[]});};
      f.addEventListener("input", preview);
      f.addEventListener("change", preview);
      $("#p-add", root).addEventListener("click", function () { if (b.params.length < 10) { b.params.push({ name: "", default: "" }); drawParams(); } });
      $("#bt-save", root).addEventListener("click", function () {
        if (!f.reportValidity()) return;
        if (mode === "task" && !f.task_id.value) { toast(T("bt.need_task"), true); return; }
        if (mode === "skill" && !f.skill_id.value) { toast(T("bt.need_skill"), true); return; }
        if (mode !== "task" && !f.prompt.value.trim()) { toast(T("bt.need_prompt"), true); return; }
        var nb = { id: b.id, label: f.label.value.trim(), description: f.description.value.trim(), icon: b.icon, color: b.color || "",
          task_id: mode === "task" ? f.task_id.value : "", skill_id: mode === "skill" ? f.skill_id.value : "", skill_version_id: mode === "skill" ? f.skill_version_id.value : "",
          prompt: mode === "task" ? "" : f.prompt.value, params: mode === "task" ? [] : b.params.filter(function (p) { return p.name.trim(); }) };
        var nl = list.slice();
        if (idx == null) nl.push(nb); else nl[idx] = nb;
        save(nl).then(close).catch(fail);
      });
    });
  }

