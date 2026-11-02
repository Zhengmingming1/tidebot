  // ---------- Markdown preview (skill files) ----------
  // mdHtml renders the Markdown a skill ships (SKILL.md, references) as formatted text. Idea from
  // siteboon/claudecodeui src/modules/code-editor (CodeEditor.tsx: .md files get a preview / edit toggle;
  // markdown/MarkdownPreview.tsx: GFM tables, task lists, links in a new tab). No library: everything is
  // escaped first and only a fixed set of tags is produced — raw HTML in the file stays text, images are
  // not fetched (a link instead), links only keep http(s) / mailto, in-file anchors and relative paths
  // (data-md-file / data-md-anchor, opened by the viewer). YAML front matter becomes a small table.
  function mdSlug(s) { return String(s).toLowerCase().replace(/<[^>]*>/g, "").replace(/&[a-z#0-9]+;/g, "").replace(/[^\p{L}\p{N}\s_-]/gu, "").trim().replace(/\s/g, "-"); }
  function mdHref(u, base) {
    u = String(u).trim();
    if (/^(https?:|mailto:)/i.test(u)) return { href: u };
    if (/^[a-z][a-z0-9+.-]*:/i.test(u) || /^\/\//.test(u) || /^[\\/]/.test(u)) return null;
    if (u.charAt(0) === "#") return { anchor: mdSlug(decodeURIComponentSafe(u.slice(1))) };
    if (base === null) return null; // a run's answer: no files to open, relative links stay text
    var path = decodeURIComponentSafe(u.split("#")[0].split("?")[0]), out = (base ? base.split("/") : []);
    path.replace(/\\/g, "/").split("/").forEach(function (p) { if (p === "..") out.pop(); else if (p && p !== ".") out.push(p); });
    return out.length ? { file: out.join("/") } : null;
  }
  function decodeURIComponentSafe(s) { try { return decodeURIComponent(s); } catch (e) { return s; } }
  function mdInline(s, base) {
    var keep = [], put = function (h) { keep.push(h); return "\u0000" + (keep.length - 1) + "\u0000"; };
    s = s.replace(/\u0000/g, "");
    s = s.replace(/(`+)([\s\S]*?[^`])\1(?!`)/g, function (m, t, c) { return put("<code>" + esc(c.replace(/^ ([\s\S]*) $/, "$1")) + "</code>"); });
    s = s.replace(/\\([!-\/:-@[-`{-~])/g, function (m, c) { return put(esc(c)); });
    var link = function (txt, url, img, bare) {
      var h = mdHref(url, base), label = img ? "🖼 " + (txt || url) : txt, inner = bare || img ? esc(label) : mdInline(label, base);
      if (!h) return put(inner);
      if (h.href) return put('<a href="' + esc(h.href) + '" target="_blank" rel="noopener noreferrer" data-tip="' + esc(h.href) + '">' + inner + "</a>");
      if (h.anchor != null) return put('<a href="#" data-md-anchor="' + esc(h.anchor) + '">' + inner + "</a>");
      return put('<a href="#" data-md-file="' + esc(h.file) + '" data-tip="' + esc(h.file) + '">' + inner + "</a>");
    };
    s = s.replace(/(!?)\[([^\]]*)\]\(\s*<?((?:[^()\s<>]|\([^()\s]*\))*)>?(?:\s+"[^"]*")?\s*\)/g, function (m, bang, txt, url) { return link(txt, url, !!bang); });
    s = s.replace(/<((?:https?:\/\/|mailto:)[^>\s]+)>/g, function (m, u) { return link(u, u, false, true); });
    s = s.replace(/(^|[\s(])(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/g, function (m, pre, u) { return pre + link(u, u, false, true); });
    s = esc(s);
    s = s.replace(/(\*\*|__)(?=\S)([\s\S]*?\S)\1/g, "<strong>$2</strong>")
      .replace(/(^|[^*\w])\*(?=\S)([^*]*?\S)\*(?!\*)/g, "$1<em>$2</em>")
      .replace(/(^|[^_\w])_(?=\S)([^_]*?\S)_(?![_\w])/g, "$1<em>$2</em>")
      .replace(/~~(?=\S)([\s\S]*?\S)~~/g, "<del>$1</del>")
      .replace(/(?: {2,}|\\)\n/g, "<br>");
    return s.replace(/\u0000(\d+)\u0000/g, function (m, i) { return keep[+i]; });
  }
  function mdHtml(src, base) {
    var lines = String(src).replace(/\r\n?/g, "\n").replace(/\t/g, "    ").split("\n"), out = [], i = 0, fm = "";
    if (lines[0] === "---" && base !== null) {
      for (var e = 1; e < Math.min(lines.length, 200); e++) if (lines[e] === "---" || lines[e] === "...") break;
      if (e < lines.length && e < 200) {
        var rows = [];
        lines.slice(1, e).forEach(function (l) {
          var m = /^([A-Za-z0-9_.-]+):\s?(.*)$/.exec(l);
          if (m) rows.push([m[1], /^[>|][-+]?$/.test(m[2].trim()) ? "" : m[2]]); else if (rows.length && l.trim()) rows[rows.length - 1][1] += (rows[rows.length - 1][1] ? " " : "") + l.trim();
        });
        fm = rows.length ? '<table class="md-fm"><tbody>' + rows.map(function (r) { return "<tr><th>" + esc(r[0]) + "</th><td>" + esc(r[1].replace(/^(["'])(.*)\1$/, "$2").replace(/^[>|][-+]?$/, "")) + "</td></tr>"; }).join("") + "</tbody></table>" : "";
        i = e + 1;
      }
    }
    var blank = function (l) { return !l || !l.trim(); };
    var isFence = function (l) { return /^ {0,3}(`{3,}|~{3,})/.exec(l); };
    var isHr = function (l) { return /^ {0,3}([-*_])( *\1){2,} *$/.test(l); };
    var isList = function (l) { return /^( *)([-*+]|\d{1,9}[.)]) +(.*)$/.exec(l); };
    var isTableSep = function (l) { return /^ *\|? *:?-+:? *(\| *:?-+:? *)*\|? *$/.test(l) && l.indexOf("-") >= 0 && l.indexOf("|") >= 0; };
    var cells = function (l) { return l.trim().replace(/\\\|/g, "\u0001").replace(/^\|/, "").replace(/\|$/, "").split("|").map(function (c) { return c.trim().replace(/\u0001/g, "\\|"); }); };
    var startsBlock = function (l) { return isFence(l) || /^ {0,3}#{1,6}(\s|$)/.test(l) || isHr(l) || /^ {0,3}>/.test(l) || isList(l); };
    while (i < lines.length) {
      var l = lines[i], m;
      if (blank(l)) { i++; continue; }
      if ((m = isFence(l))) {
        var fence = m[1], lang = l.slice(l.indexOf(fence) + fence.length).trim().split(/\s/)[0], code = [];
        for (i++; i < lines.length && !(lines[i].trim().indexOf(fence.charAt(0).repeat(fence.length)) === 0 && /^[`~]+$/.test(lines[i].trim())); i++) code.push(lines[i]);
        i++;
        out.push('<pre class="md-code">' + (lang ? '<span class="md-lang">' + esc(lang) + "</span>" : "") + "<code>" + esc(code.join("\n")) + "</code></pre>");
        continue;
      }
      if ((m = /^ {0,3}(#{1,6})\s+(.*?)(?:\s+#+)?\s*$/.exec(l)) || (m = /^ {0,3}(#{1,6})$/.exec(l))) {
        var lv = m[1].length, txt = m[2] || "";
        out.push("<h" + lv + ' data-md-id="' + esc(mdSlug(txt)) + '">' + mdInline(txt, base) + "</h" + lv + ">"); i++; continue;
      }
      if (isHr(l)) { out.push("<hr>"); i++; continue; }
      if (/^ {0,3}>/.test(l)) {
        var q = [];
        while (i < lines.length && !blank(lines[i]) && (/^ {0,3}>/.test(lines[i]) || q.length)) { q.push(lines[i].replace(/^ {0,3}> ?/, "")); i++; }
        out.push("<blockquote>" + mdHtml(q.join("\n"), base) + "</blockquote>"); continue;
      }
      if ((m = isList(l))) {
        var ind = m[1].length, ordered = /\d/.test(m[2]), items = [], cur = null, loose = false, sawBlank = false;
        while (i < lines.length) {
          var ln = lines[i], mm = isList(ln);
          if (mm && mm[1].length <= ind + 1 && /\d/.test(mm[2]) === ordered) { if (sawBlank && cur) loose = true; cur = [mm[3]]; items.push(cur); sawBlank = false; i++; continue; }
          if (blank(ln)) { sawBlank = true; i++; continue; }
          var lead = /^ */.exec(ln)[0].length;
          if (lead > ind && cur) { if (sawBlank) cur.push(""); cur.push(ln.slice(Math.min(lead, ind + (mm ? 2 : 2 + (ordered ? 1 : 0))))); sawBlank = false; i++; continue; }
          if (!sawBlank && cur && !startsBlock(ln)) { cur.push(ln.trim()); i++; continue; }
          break;
        }
        var start = ordered ? parseInt(m[2], 10) : 1;
        out.push((ordered ? "<ol" + (start !== 1 ? ' start="' + start + '"' : "") + ">" : "<ul>") + items.map(function (it) {
          var first = it[0], task = /^\[([ xX])\]\s+/.exec(first), cls = "";
          if (task) { first = first.slice(task[0].length); cls = ' class="md-task' + (task[1] !== " " ? " done" : "") + '"'; }
          var body = [first].concat(it.slice(1)).join("\n"), inner = mdHtml(body, base);
          if (!loose) inner = inner.replace(/^<p>([\s\S]*?)<\/p>/, "$1");
          return "<li" + cls + ">" + (task ? '<span class="md-box" aria-hidden="true"></span>' : "") + inner + "</li>";
        }).join("") + (ordered ? "</ol>" : "</ul>"));
        continue;
      }
      if (l.indexOf("|") >= 0 && i + 1 < lines.length && isTableSep(lines[i + 1])) {
        var head = cells(l), al = cells(lines[i + 1]).map(function (c) { return /^:-+:$/.test(c) ? "c" : /-:$/.test(c) ? "r" : ""; }), body2 = [];
        for (i += 2; i < lines.length && !blank(lines[i]) && lines[i].indexOf("|") >= 0; i++) body2.push(cells(lines[i]));
        var td = function (tag, c, k) { return "<" + tag + (al[k] ? ' class="md-' + al[k] + '"' : "") + ">" + mdInline(c, base) + "</" + tag + ">"; };
        out.push('<div class="md-tbl"><table><thead><tr>' + head.map(function (c, k) { return td("th", c, k); }).join("") + "</tr></thead><tbody>" + body2.map(function (r) { return "<tr>" + head.map(function (x, k) { return td("td", r[k] || "", k); }).join("") + "</tr>"; }).join("") + "</tbody></table></div>");
        continue;
      }
      if (/^ {4}/.test(l)) {
        var ic4 = [];
        while (i < lines.length && (/^ {4}/.test(lines[i]) || blank(lines[i]))) { ic4.push(lines[i].slice(4)); i++; }
        while (ic4.length && blank(ic4[ic4.length - 1])) ic4.pop();
        out.push('<pre class="md-code"><code>' + esc(ic4.join("\n")) + "</code></pre>"); continue;
      }
      var para = [l];
      for (i++; i < lines.length && !blank(lines[i]) && !startsBlock(lines[i]) && !(lines[i].indexOf("|") >= 0 && i + 1 < lines.length && isTableSep(lines[i + 1])); i++) {
        if (/^ {0,3}(=+|-+) *$/.test(lines[i]) && para.length === 1) break;
        para.push(lines[i]);
      }
      if (i < lines.length && para.length === 1 && /^ {0,3}(=+|-+) *$/.test(lines[i]) && !blank(lines[i])) {
        var lv2 = lines[i].trim().charAt(0) === "=" ? 1 : 2; i++;
        out.push("<h" + lv2 + ' data-md-id="' + esc(mdSlug(para[0].trim())) + '">' + mdInline(para[0].trim(), base) + "</h" + lv2 + ">"); continue;
      }
      out.push("<p>" + mdInline(para.map(function (x) { return x.replace(/^ +/, ""); }).join("\n"), base) + "</p>");
    }
    return fm + out.join("");
  }
  // ---------- end Markdown preview ----------
  // ---------- BUTTONS ----------
  function renderButtons() {
    return Promise.all([api("/api/buttons"), api("/api/tasks"), api("/api/skills")]).then(function (r) {
      var list = r[0], tasks = r[1], skills = r[2] || [], tname = {}, sname = {};
      tasks.forEach(function (t) { tname[t.id] = t.name; });
      skills.forEach(function (x) { sname[x.id] = x.name; });
      var cards = list.map(function (b, i) {
        var target = b.task_id ? T("bt.runs_task", { name: tname[b.task_id] || b.task_id }) : b.skill_id ? T("bt.runs_skill", { name: sname[b.skill_id] || b.skill_id }) : T("bt.prompt", { p: b.prompt });
        var tIco = b.task_id ? "tasks" : b.skill_id ? "skills" : "chat";
        var tName = b.task_id ? tname[b.task_id] || b.task_id : b.skill_id ? sname[b.skill_id] || b.skill_id : b.prompt;
        return '<div class="card qa bt-card' + (b.color ? " colored" : "") + '" draggable="true" tabindex="0" data-idx="' + i + '" aria-roledescription="' + esc(T("bt.sortable")) + '" aria-label="' + esc(b.label) + '" data-hue="' + esc(iconHue(btnIcon(b))) + '" data-color="' + esc(b.color || "") + '" data-run="' + esc(b.id) + '">' +
          '<div class="bt-top"><span class="qa-ico">' + ic(btnIcon(b)) + '</span><div class="bt-acts"><span class="grip qa-grip" aria-hidden="true" data-tip="' + esc(T("bt.sort_hint", { k: keyLabel("Alt+← →") })) + '">⋮⋮</span>' +
          '<button class="icon-btn sm" data-edit="' + i + '" data-tip="' + esc(T("btn.edit")) + '" aria-label="' + esc(T("btn.edit")) + '">' + ic("edit", "sm") + '</button>' +
          '<button class="icon-btn sm danger-text" data-del="' + i + '" data-tip="' + esc(T("btn.remove")) + '" aria-label="' + esc(T("btn.remove")) + '">' + ic("trash", "sm") + "</button></div></div>" +
          "<strong>" + esc(b.label) + "</strong>" + (b.description ? '<small class="bt-sub">' + esc(b.description) + "</small>" : "") +
          '<div class="bt-foot"><span class="bt-target" data-tip="' + esc(target) + '">' + ic(tIco, "sm") + "<span>" + esc(tName) + '</span></span><span class="bt-go">' + ic("play", "sm") + esc(T("bt.run")) + "</span></div></div>";
      });
      if (!list.length) cards.push('<div class="bt-empty">' + emptyState("bolt", T("bt.none"), T("bt.new_sub")) + "</div>");
      mount('<div class="page-head"><div><h1>' + esc(T("nav.buttons")) + "</h1><p>" + esc(T("bt.sub")) + tip(T("bt.sort_hint", { k: keyLabel("Alt+← →") })) + '</p></div><div class="actions"><button class="btn primary" id="b-add">' + ic("plus", "sm") + esc(T("bt.new")) + '</button></div></div><div class="grid g4" id="bt-grid">' + cards.join("") + '</div><div class="sr-only" aria-live="polite" id="bt-live"></div>', "buttons", [[T("nav.buttons")]]);
      applyColors(page());
      var save = function (nl, quiet) { return api("/api/buttons", { method: "PUT", body: nl }).then(function () { if (!quiet) toast(T("toast.buttons_saved")); renderButtons(); }); };
      $("#b-add").addEventListener("click", function () { buttonModal(list, null, tasks, skills, save); });
      $$("[data-edit]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); buttonModal(list, +b.getAttribute("data-edit"), tasks, skills, save); }); });
      $$("[data-del]").forEach(function (b) { b.addEventListener("click", function (e) { e.stopPropagation(); var k = +b.getAttribute("data-del"); var before = list.slice(), nl = list.slice(), gone = nl.splice(k, 1)[0]; save(nl, true).then(function () { toastUndo(T("bt.removed", { n: gone.label }), function () { save(before, true).catch(fail); }); }).catch(fail); }); });
      $$("[data-run]").forEach(function (c) { c.addEventListener("click", function (e) { if (e.target.closest("button")) return; runButton(c.getAttribute("data-run"), c); }); });
      // ordering: drag with the mouse, or focus a card and press Alt+←/→ (Alt+↑/↓)
      var grid = $("#bt-grid"), order = function () { return $$("[data-idx]", grid).map(function (c) { return list[+c.getAttribute("data-idx")]; }); };
      var persist = function (focusId) {
        var nl = order();
        if (nl.every(function (b, k) { return b === list[k]; })) return;
        api("/api/buttons", { method: "PUT", body: nl }).then(function () {
          $("#bt-live").textContent = T("bt.order_saved");
          toast(T("bt.order_saved"));
          renderButtons().then(function () { var c = $('[data-run="' + focusId + '"]'); if (c) c.focus(); });
        }).catch(fail);
      };
      $$("[data-idx]", grid).forEach(function (c) {
        c.addEventListener("dragstart", function (e) { c.classList.add("dragging"); e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", c.getAttribute("data-run")); });
        c.addEventListener("dragend", function () { c.classList.remove("dragging"); persist(c.getAttribute("data-run")); });
        c.addEventListener("keydown", function (e) {
          if (e.target !== c) return;
          if (e.key === "Enter" || e.key === " ") { e.preventDefault(); runButton(c.getAttribute("data-run"), c); return; }
          if (!e.altKey) return;
          var back = e.key === "ArrowLeft" || e.key === "ArrowUp", fwd = e.key === "ArrowRight" || e.key === "ArrowDown";
          if (!back && !fwd) return;
          e.preventDefault();
          var sib = back ? c.previousElementSibling : c.nextElementSibling;
          if (!sib || !sib.hasAttribute("data-idx")) return;
          grid.insertBefore(c, back ? sib : sib.nextSibling);
          persist(c.getAttribute("data-run"));
        });
      });
      grid.addEventListener("dragover", function (e) {
        var drag = $(".dragging", grid); if (!drag) return;
        e.preventDefault();
        var after = $$("[data-idx]:not(.dragging)", grid).filter(function (x) { var r = x.getBoundingClientRect(); return e.clientY < r.bottom && e.clientX < r.left + r.width / 2 || e.clientY < r.top; })[0];
        if (after) grid.insertBefore(drag, after); else grid.appendChild(drag);
      });
    });
  }

