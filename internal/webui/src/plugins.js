  // ---------- plugin install & triggers ----------
  function pluginInstallModal() {
    modal('<div class="card-head"><span class="ico">' + ic("plugins") + "</span><h2>" + esc(T("pl.install")) + "</h2></div>" + dropZone("pl-drop", ".zip,.tar.gz,.tgz", false) + '<div id="pl-prev"></div><div class="actions" id="pl-foot"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + "</button></div>", function (root, close) {
      var prev = $("#pl-prev", root), foot = $("#pl-foot", root);
      wireDrop(root, "pl-drop", function (fd) {
        prev.innerHTML = '<p class="muted">' + esc(T("up.reading")) + "</p>";
        upload("/api/plugins/upload", fd).then(function (r) {
          var v = r.result || r, ver = v.version || {}, m = ver.manifest || {}, fresh = v.new_permissions || [], req = ver.requested_permissions || [];
          var trig = ((m.contributes || {}).triggers || []);
          prev.innerHTML = '<div class="preview"><p><strong>' + esc(m.name || m.id) + '</strong> <span class="tag">' + esc(m.version || "") + '</span> <code class="muted">' + esc(m.id || "") + "</code></p>" +
            '<p class="muted">' + esc(T("pl.hash", { h: String(ver.content_hash || "").slice(0, 16) })) + "</p>" +
            "<h4>" + esc(T("pl.perms")) + "</h4>" + (req.length ? permChooser(req, fresh) : '<p class="muted">' + esc(T("pl.no_perms")) + "</p>") +
            (trig.length ? "<h4>" + esc(T("pl.contrib_triggers")) + "</h4><p>" + trig.map(function (t) { return '<span class="pill plain">' + esc(t.kind + ": " + (t.title || t.id)) + "</span>"; }).join(" ") + "</p>" : "") +
            '<p class="hint">' + esc(T("pl.grant_hint")) + "</p></div>";
          foot.innerHTML = '<button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn primary" id="pl-ok">' + ic("shield", "sm") + esc(T("pl.approve")) + "</button>";
          $$("[data-close]", foot).forEach(function (b) { b.addEventListener("click", close); });
          $("#pl-ok", foot).addEventListener("click", function (e) {
            e.target.disabled = true;
            var granted = $$("[data-perm]", prev).filter(function (c) { return c.checked; }).map(function (c) { return c.value; });
            rpc("plugins.approve", { version_id: ver.id, permissions: granted }).then(function () { close(); toast(T("pl.installed_ok", { n: m.name || m.id })); refresh(); }).catch(function (err) { e.target.disabled = false; fail(err); });
          });
        }).catch(function (e) { prev.innerHTML = '<p class="notice bad">' + ic("x", "sm") + esc(e.message) + "</p>"; });
      });
    });
  }
  // Plugin trigger forms remember the last numbers/switches/choices per trigger kind
  // (poll interval, max items, threshold…), never URLs, text or secrets.
  var trigMemKey = function (pid, tid) { return "tb_trig_" + pid + "_" + tid; };
  function trigMemory(pid, tid, fields) {
    var out = {};
    try { var m = JSON.parse(localStorage.getItem(trigMemKey(pid, tid)) || "{}"); fields.forEach(function (fd) { if (m[fd.key] !== undefined && !fd.write_only) out[fd.key] = m[fd.key]; }); } catch (e) { /* ignore */ }
    return out;
  }
  function trigRemember(pid, tid, fields, cfg) {
    var keep = {};
    fields.forEach(function (fd) { if (!fd.write_only && (fd.type === "integer" || fd.type === "number" || fd.type === "boolean" || (fd.enum && fd.enum.length)) && cfg[fd.key] !== undefined) keep[fd.key] = cfg[fd.key]; });
    try { localStorage.setItem(trigMemKey(pid, tid), JSON.stringify(keep)); } catch (e) { /* ignore */ }
  }
  function triggerModal(plugins) {
    api("/api/tasks").then(function (tasks) {
      var ptrig = [];
      plugins.forEach(function (p) { ((((p.current || {}).manifest || {}).contributes || {}).triggers || []).forEach(function (t) { if (p.enabled && t.kind !== "webhook" && t.kind !== "file_change") ptrig.push({ plugin: p.id, pname: plName(p), t: t }); }); });
      modal('<div class="card-head"><span class="ico">' + ic("globe") + "</span><h2>" + esc(T("tr.new")) + '</h2></div><form id="trf">' +
        fieldHtml(T("tr.kind"), '<select name="kind">' + opt("webhook", T("tr.k_webhook"), true) + opt("file_change", T("tr.k_file")) + (ptrig.length ? opt("plugin", T("tr.k_plugin")) : "") + "</select>") +
        fieldHtml(T("col.task"), '<select name="task_id" required>' + tasks.map(function (t) { return opt(t.id, t.name); }).join("") + "</select>", tasks.length ? "" : T("tr.no_tasks")) +
        '<div data-k="file_change" class="hidden">' + fieldHtml(T("tr.path"), '<input name="path" class="mono" placeholder="/home/me/inbox">', T("tr.path_hint")) + "</div>" +
        '<div data-k="plugin" class="hidden">' + fieldHtml(T("tr.ptrig"), '<select name="ptrig">' + ptrig.map(function (x, i) { return opt(i, x.pname + " · " + (state.dict["tk." + x.plugin + "." + x.t.id] || x.t.title || x.t.id)); }).join("") + "</select>") + '<div id="tr-pf"></div><div class="row-actions"><button type="button" class="btn sm" id="tr-prev">' + ic("eye", "sm") + esc(T("tr.preview")) + '</button></div><div id="tr-prev-out"></div></div>' +
        '<p class="hint" data-k="webhook">' + esc(T("tr.webhook_hint")) + "</p>" +
        '<div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T("tr.create")) + "</button></div></form>", function (root, close) {
        var f = $("#trf", root);
        var pfields = [];
        var loadPF = function () {
          var x = ptrig[+((f.ptrig || {}).value || 0)]; if (!x) return;
          $("#tr-prev-out", root).innerHTML = "";
          rpc("plugins.trigger_fields", { plugin_id: x.plugin, trigger_id: x.t.id }).then(function (fl) { pfields = fl || []; $("#tr-pf", root).innerHTML = schemaForm(pfields, trigMemory(x.plugin, x.t.id, pfields), {}, "t-" + x.plugin); wirePluginAutofill($("#tr-pf", root), x.plugin, pfields); }).catch(fail);
        };
        var sync = function () { $$("[data-k]", f).forEach(function (x) { x.classList.toggle("hidden", x.getAttribute("data-k") !== f.kind.value); }); if (f.kind.value === "plugin" && !pfields.length) loadPF(); };
        f.kind.addEventListener("change", sync); sync();
        if (f.ptrig) f.ptrig.addEventListener("change", loadPF);
        var prevBtn = $("#tr-prev", root);
        if (prevBtn) prevBtn.addEventListener("click", function () {
          var x = ptrig[+f.ptrig.value], out = $("#tr-prev-out", root);
          prevBtn.disabled = true; out.innerHTML = '<p class="muted">' + esc(T("tr.preview_wait")) + "</p>";
          rpc("triggers.preview", { plugin_id: x.plugin, config: readSchemaForm($("#tr-pf", root), pfields) }).then(function (r) {
            prevBtn.disabled = false;
            var items = (r && r.items) || [];
            out.innerHTML = items.length ? '<ul class="tr-prev">' + items.map(function (it) { return "<li><strong>" + esc(it.title || it.subject || it.url || "") + '</strong> <span class="muted">' + esc(it.published || it.date || it.from || "") + "</span>" + (it.text || it.summary || it.snippet ? '<small class="muted">' + esc(String(it.text || it.summary || it.snippet).slice(0, 220)) + "</small>" : "") + "</li>"; }).join("") + "</ul>" : '<p class="muted">' + esc(T("tr.preview_none")) + "</p>";
          }).catch(function (e) { prevBtn.disabled = false; out.innerHTML = '<p class="notice bad">' + ic("x", "sm") + esc(e.message) + "</p>"; });
        });
        f.addEventListener("submit", function (e) {
          e.preventDefault();
          var k = f.kind.value, body = { kind: k, task_id: f.task_id.value, config: {} };
          if (k === "file_change") body.config.path = f.path.value.trim();
          if (k === "plugin") { var x = ptrig[+f.ptrig.value]; body.kind = x.t.kind; body.plugin_id = x.plugin; body.config = readSchemaForm($("#tr-pf", root), pfields); body.config.trigger_id = x.t.id; }
          if (k === "plugin") trigRemember(ptrig[+f.ptrig.value].plugin, ptrig[+f.ptrig.value].t.id, pfields, body.config);
          rpc("triggers.create", body).then(function (r) {
            close();
            if (r && r.token) {
              var url = location.origin + r.url_path;
              modal('<div class="card-head"><span class="ico">' + ic("key") + "</span><h2>" + esc(T("tr.token_title")) + '</h2></div><p class="notice">' + ic("info", "sm") + esc(T("tr.token_once")) + "</p>" +
                fieldHtml("URL", '<input readonly class="mono" value="' + esc(url) + '">') + fieldHtml(T("tr.token"), '<input readonly class="mono" id="tok" value="' + esc(r.token) + '">') +
                '<p class="hint">' + esc(T("tr.curl_hint")) + '</p><pre class="code">' + esc("curl -X POST -H 'Authorization: Bearer <token>' " + url) + '</pre><div class="actions"><button class="btn" id="tok-copy">' + esc(T("tr.copy")) + '</button><button class="btn primary" data-close>' + esc(T("tr.saved_it")) + "</button></div>", function (r2) {
                $$("[data-close]", r2).forEach(function (b) { b.addEventListener("click", function () { refresh(); }); });
                $("#tok-copy", r2).addEventListener("click", function () { var t = $("#tok", r2); t.select(); (navigator.clipboard ? navigator.clipboard.writeText(t.value) : Promise.resolve(document.execCommand("copy"))).then(function () { toast(T("tr.copied")); }); });
              });
            } else { toast(T("tr.created")); refresh(); }
          }).catch(fail);
        });
      });
    }).catch(fail);
  }


  // ---------- plugin permissions, config forms, trigger cards (I5) ----------
  var PERM_GROUPS = [["fs", /^fs\./], ["net", /^network:/], ["exec", /^host_api:/], ["secret", /^secret:/]];
  function permRisk(p) {
    if (/^fs\.write:|^secret:|^host_api:runs\.start$/.test(p) || /^network:\*/.test(p) || /^fs\.read:(\/|~)$/.test(p)) return "high";
    if (/^network:|^fs\.read:|^host_api:notify\.send$/.test(p)) return "med";
    return "low";
  }
  function permLabel(p) { var i = p.indexOf(":"); return { kind: p.slice(0, i), val: p.slice(i + 1) }; }
  function groupPerms(list) {
    return PERM_GROUPS.map(function (g) { return { key: g[0], items: list.filter(function (p) { return g[1].test(p); }) }; }).filter(function (g) { return g.items.length; });
  }
  function permChooser(req, fresh) {
    return '<div class="perm-groups">' + groupPerms(req).map(function (g) {
      return '<fieldset class="perm-g"><legend>' + ic(g.key === "fs" ? "folder" : g.key === "net" ? "globe" : g.key === "exec" ? "bolt" : "key", "sm") + esc(T("perm.g_" + g.key)) + "</legend>" + g.items.map(function (p) {
        var r = permRisk(p), l = permLabel(p);
        return '<label class="perm-row"><input type="checkbox" data-perm value="' + esc(p) + '"' + (r === "high" ? "" : " checked") + '><span class="grow"><span class="muted">' + esc(T("perm.k_" + l.kind.replace(".", "_"))) + "</span> <code>" + esc(l.val) + "</code>" + (fresh && fresh.indexOf(p) >= 0 ? ' <span class="pill warn sm">' + esc(T("pl.new_perm")) + "</span>" : "") + '</span><span class="risk ' + r + '">' + esc(T("perm.r_" + r)) + "</span></label>";
      }).join("") + "</fieldset>";
    }).join("") + "</div>";
  }
  function permCard(p) {
    var cur = p.current || {}, req = cur.requested_permissions || [], ok = cur.approved_permissions || [];
    if (!req.length) return '<p class="muted">' + esc(T("pl.no_perms")) + "</p>";
    return groupPerms(req).map(function (g) {
      return '<div class="perm-line"><small class="muted">' + esc(T("perm.g_" + g.key)) + "</small>" + g.items.map(function (x) {
        var on = ok.indexOf(x) >= 0, l = permLabel(x);
        return '<span class="perm-chip ' + (on ? "on " : "off ") + permRisk(x) + '"><code>' + esc(l.val) + "</code>" + '<button class="linkish" data-perm-t="' + esc(p.id) + '" data-perm-v="' + esc(x) + '" data-on="' + (on ? "1" : "") + '">' + esc(T(on ? "perm.revoke" : "perm.grant")) + "</button></span>";
      }).join("") + "</div>";
    }).join("");
  }
  function schemaForm(fields, values, secretsSet, ns) {
    return fields.map(function (f) {
      var v = values[f.key] !== undefined ? values[f.key] : f.default, nm = ' name="' + esc(f.key) + '" data-f="' + esc(f.key) + '" data-t="' + esc(f.type) + '"';
      var lk = "pf." + (ns || "") + "." + f.key, lab = (ns && state.dict[lk] ? state.dict[lk] : (f.title || f.key)) + (f.required ? " *" : ""), inner;
      var hk = lk + "_h";
      if (ns && state.dict[hk]) f = Object.assign({}, f, { description: state.dict[hk] });
      if (f.type === "boolean") return '<label class="check field-check"><input type="checkbox"' + nm + (v ? " checked" : "") + "> " + esc(lab) + (f.description ? '<small class="hint">' + esc(f.description) + "</small>" : "") + "</label>";
      if (f.type === "array") inner = '<div class="multi">' + f.items_enum.map(function (x) { return '<label class="check"><input type="checkbox" data-arr="' + esc(f.key) + '" value="' + esc(x) + '"' + ((v || []).map(String).indexOf(String(x)) >= 0 ? " checked" : "") + "> " + esc(x) + "</label>"; }).join("") + "</div>";
      else if (f.enum && f.enum.length) inner = "<select" + nm + ">" + (f.required ? "" : opt("", "—", v == null)) + f.enum.map(function (x) { return opt(x, state.dict["pf.e." + x] || x, String(x) === String(v)); }).join("") + "</select>";
      else if (f.type === "string" && f.max_length >= 2000 && !/url$/.test(f.key)) inner = '<textarea rows="4"' + nm + ' maxlength="' + f.max_length + '"' + (f.required ? " required" : "") + ">" + esc(v == null ? "" : v) + "</textarea>";
      else if (f.write_only) inner = '<input type="password" autocomplete="new-password"' + nm + ' placeholder="' + esc(T(secretsSet[f.key] ? "cfg.secret_keep" : "cfg.secret_new")) + '"' + (f.max_length ? ' maxlength="' + f.max_length + '"' : "") + ">";
      else if (f.type === "number" || f.type === "integer") inner = '<input type="number"' + nm + (f.type === "integer" ? ' step="1"' : ' step="any"') + (f.minimum != null ? ' min="' + f.minimum + '"' : "") + (f.maximum != null ? ' max="' + f.maximum + '"' : "") + ' value="' + esc(v == null ? "" : v) + '"' + (f.required ? " required" : "") + ">";
      else inner = "<input" + nm + ' value="' + esc(v == null ? "" : v) + '"' + (f.max_length ? ' maxlength="' + f.max_length + '"' : ' maxlength="4000"') + (f.required ? " required" : "") + ">";
      return fieldHtml(lab, inner, f.description || (f.write_only ? T("cfg.secret_hint") : ""));
    }).join("");
  }
  function readSchemaForm(root, fields) {
    var out = {};
    fields.forEach(function (f) {
      if (f.type === "array") { out[f.key] = $$('[data-arr="' + f.key + '"]', root).filter(function (c) { return c.checked; }).map(function (c) { var n = Number(c.value); return f.items_enum.some(function (x) { return typeof x === "number"; }) && !isNaN(n) ? n : c.value; }); return; }
      var el = $('[data-f="' + f.key + '"]', root); if (!el) return;
      if (f.type === "boolean") { out[f.key] = el.checked; return; }
      if (el.value === "") return;
      out[f.key] = f.type === "number" || f.type === "integer" ? Number(el.value) : el.value;
    });
    return out;
  }
  // ---- official plugin prefill (POST /api/plugins/prefill) ----
  // Fills what can be inferred from one value: an email address → SMTP/IMAP servers, a
  // pasted robot webhook (or bare key/token) → normalized URL + masked display, a site
  // URL → its RSS/Atom feed, a page URL → title + content selector. Fields the user typed
  // into are never overwritten. Network lookups run server-side behind an SSRF guard.
  function wirePluginAutofill(root, pid, fields) {
    var has = function (k) { return !!$('[data-f="' + k + '"]', root); };
    var el = function (k) { return $('[data-f="' + k + '"]', root); };
    var auto = {}, mine = {};
    fields.forEach(function (fd) { var e = el(fd.key); if (e) e.addEventListener("input", function (ev) { if (ev.isTrusted) mine[fd.key] = e.value !== auto[fd.key] && e.value !== ""; }); });
    var flash = function (e) { e.classList.remove("af-flash"); void e.offsetWidth; e.classList.add("af-flash"); };
    var put = function (k, v, force) {
      var e = el(k); if (!e || v == null || v === "") return 0;
      if (!force && (mine[k] || (e.value !== "" && e.value !== auto[k] && String(e.value) !== String(fieldDefault(k))))) return 0;
      if (String(e.value) === String(v)) { auto[k] = e.value; return 0; }
      e.value = v; auto[k] = e.value; flash(e); return 1;
    };
    var fieldDefault = function (k) { var fd = fields.filter(function (x) { return x.key === k; })[0]; return fd && fd.default != null ? fd.default : ""; };
    var note = function (k, html, tone) {
      var e = el(k); if (!e) return;
      var host = e.closest(".field") || e.parentNode, n = $('[data-afn="' + k + '"]', host);
      if (!n) { n = document.createElement("span"); n.setAttribute("data-afn", k); host.appendChild(n); }
      n.className = "af-note" + (html ? " on" : "") + (tone ? " " + tone : "");
      n.innerHTML = html ? (tone === "warn" ? ic("info", "sm") : ic("sparkle", "sm")) + "<span>" + html + "</span>" : "";
    };
    var ask = function (field, value, extra) { return apiRaw("/api/plugins/prefill", { method: "POST", body: Object.assign({ plugin: pid, field: field, value: value }, extra || {}) }); };
    var debounce = function (fn, ms) { var t; return function () { var a = arguments; clearTimeout(t); t = setTimeout(function () { fn.apply(null, a); }, ms); }; };

    if (pid === "email") {
      var bar = document.createElement("div");
      bar.className = "af-bar";
      bar.innerHTML = '<label class="field"><span>' + esc(T("pf.af_mail")) + '</span><input type="email" id="af-mail" autocomplete="email" spellcheck="false" placeholder="you@example.com"></label>';
      root.insertBefore(bar, root.firstChild);
      var mailIn = $("#af-mail", bar), mailNote = document.createElement("span");
      mailNote.className = "af-note"; mailNote.innerHTML = ic("sparkle", "sm") + "<span>" + esc(T("pf.af_mail_hint")) + "</span>"; bar.firstChild.appendChild(mailNote);
      if (el("from") && el("from").value) mailIn.value = el("from").value;
      var lookup = debounce(function (addr) {
        if (!/@.+\./.test(addr)) return;
        ask("address", addr).then(function (r) {
          if (!r.found) { mailNote.className = "af-note warn on"; mailNote.innerHTML = ic("info", "sm") + "<span>" + esc(T("pf.af_mail_unknown")) + "</span>"; return; }
          var v = r.values, n = 0;
          ["smtp_host", "smtp_port", "smtp_username", "from", "imap_host", "imap_port", "imap_username", "default_to"].forEach(function (k) { n += put(k, v[k]); });
          var name = T("pf.mp_" + r.provider) !== "pf.mp_" + r.provider ? T("pf.mp_" + r.provider) : r.provider;
          mailNote.className = "af-note on";
          mailNote.innerHTML = ic("sparkle", "sm") + "<span>" + esc(T(r.via === "mx" ? "pf.af_mail_mx" : "pf.af_mail_ok", { p: name, s: v.smtp_host + ":" + v.smtp_port, i: v.imap_host + ":" + v.imap_port })) + "</span>";
          note("smtp_password", r.note ? esc(T("pf.mn_" + r.note, { p: name })) : "", "warn");
          if (n) toast(T("pf.af_filled", { n: n }));
        }).catch(function () {});
      }, 400);
      mailIn.addEventListener("input", function () { lookup(mailIn.value.trim()); });
      ["from", "smtp_username"].forEach(function (k) { if (has(k)) el(k).addEventListener("change", function () { if (!mailIn.value) { mailIn.value = el(k).value; lookup(el(k).value.trim()); } }); });
      if (mailIn.value) lookup(mailIn.value);
    }

    if ((pid === "wecom" || pid === "dingtalk") && has("webhook_url")) {
      var wh = el("webhook_url");
      var check = function (text) {
        if (!text.trim()) { note("webhook_url", ""); return; }
        ask("webhook_url", text).then(function (r) {
          if (r.mismatch) { note("webhook_url", esc(T("pf.wh_mismatch", { k: T("pf.wh_" + r.kind) })), "warn"); return; }
          if (!r.found) { note("webhook_url", esc(T("pf.wh_unknown")), "warn"); return; }
          if (r.kind !== pid) { note("webhook_url", esc(T("pf.wh_unsupported", { k: T("pf.wh_" + r.kind) })), "warn"); return; }
          if (r.values.webhook_url) put("webhook_url", r.values.webhook_url, true);
          var msg = T("pf.wh_ok", { k: T("pf.wh_" + r.kind), m: r.masked }) + (r.built ? T("pf.wh_built") : "");
          note("webhook_url", esc(msg));
          if (r.values.sign_secret) { put("sign_secret", r.values.sign_secret, true); note("sign_secret", esc(T("pf.wh_secret", { m: r.secret_masked }))); }
        }).catch(function () {});
      };
      wh.addEventListener("paste", function (e) { var t = (e.clipboardData || window.clipboardData).getData("text") || ""; if (t) { e.preventDefault(); wh.value = t.trim(); check(t); } });
      wh.addEventListener("change", function () { check(wh.value); });
    }

    // Feed / page lookups share the private-address retry.
    var discover = function (field, value, cb) {
      var go = function (allow) {
        note(field, esc(T("pf.looking")));
        ask(field, value, { allow_private: !!allow }).then(function (r) {
          if (!r.found && r.error_code === "private") {
            note(field, esc(T("pf.private")) + ' <button type="button" class="link" data-af-allow="' + esc(field) + '">' + esc(T("pf.private_go")) + "</button>", "warn");
            var b = $('[data-af-allow="' + field + '"]', root); if (b) b.addEventListener("click", function () { go(true); });
            return;
          }
          if (!r.found) { note(field, esc(T("pf.fetch_fail", { e: r.error || "" })), "warn"); return; }
          cb(r.discovery);
        }).catch(function () { note(field, ""); });
      };
      go(false);
    };
    var looksURL = function (v) { return /^(https?:\/\/)?[\w-]+(\.[\w-]+)+(:\d+)?(\/\S*)?$/i.test(v.trim()); };

    if (pid === "rss-feed" && has("url")) {
      var u = el("url");
      var run = function () {
        var v = u.value.trim(); if (!looksURL(v)) { note("url", ""); return; }
        discover("url", v, function (d) {
          if (d.is_feed) { note("url", esc(T("pf.feed_ok", { t: d.feeds[0].title || d.title || "RSS", n: d.feeds[0].items || 0 }))); return; }
          if (!d.feeds.length) { note("url", esc(T("pf.feed_none")), "warn"); return; }
          u.value = d.feeds[0].url; auto.url = u.value; flash(u);
          var chips = d.feeds.length > 1 ? " " + d.feeds.map(function (f, i) { return '<button type="button" class="chip' + (i ? "" : " on") + '" data-feed="' + esc(f.url) + '" data-tip="' + esc(f.url) + '">' + esc(f.title || f.kind.toUpperCase()) + "</button>"; }).join("") : "";
          note("url", esc(T(d.probed ? "pf.feed_probed" : "pf.feed_found", { t: d.feeds[0].title || d.title || d.feeds[0].url })) + chips);
          $$("[data-feed]", root).forEach(function (b) { b.addEventListener("click", function () { u.value = b.getAttribute("data-feed"); auto.url = u.value; flash(u); $$("[data-feed]", root).forEach(function (x) { x.classList.toggle("on", x === b); }); }); });
        });
      };
      u.addEventListener("change", run);
      u.addEventListener("paste", function () { setTimeout(run, 0); });
    }

    if (pid === "web-monitor" && has("url")) {
      var pu = el("url");
      var runW = function () {
        var v = pu.value.trim(); if (!looksURL(v)) { note("url", ""); return; }
        discover("url", v, function (d) {
          var sel = d.selector || "";
          if (sel) put("selector", sel);
          note("url", esc(T(sel ? "pf.page_sel" : "pf.page_ok", { t: d.title || v, s: sel })));
          if (d.feeds && d.feeds.length) note("selector", esc(T("pf.page_has_feed", { t: d.feeds[0].title || d.feeds[0].url })), "warn");
        });
      };
      pu.addEventListener("change", runW);
      pu.addEventListener("paste", function () { setTimeout(runW, 0); });
    }

    if (pid === "daily-digest") {
      if (has("hour")) note("hour", esc(T("pf.dd_tz", { tz: state.tz || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC" })));
      // Send via the first IM/mail connector that is configured.
      if (has("notify_via")) {
        var via = el("notify_via");
        Promise.all(["wecom", "dingtalk", "email"].map(function (id) { return rpc("plugins.config_get", { id: id }).then(function (cv) { var ss = cv.secrets_set || {}, vals = cv.values || {}; return ss.webhook_url || vals.smtp_host || ss.app_secret ? id : ""; }, function () { return ""; }); })).then(function (ids) {
          var ok = ids.filter(Boolean);
          if (!ok.length) { note("notify_via", esc(T("pf.dd_none")), "warn"); return; }
          if (ok.indexOf(via.value) < 0 && !mine.notify_via) { via.value = ok[0]; auto.notify_via = ok[0]; flash(via); }
          note("notify_via", esc(T("pf.dd_via", { list: ok.map(function (x) { return T("of." + x + ".n"); }).join(T("af.sep")) })));
        });
      }
      if (has("feeds")) {
        var ta = el("feeds");
        var fix = function () {
          var lines = ta.value.split("\n").map(function (l) { return l.trim(); }).filter(Boolean);
          var todo = lines.filter(looksURL).slice(0, 10);
          if (!todo.length) return;
          note("feeds", esc(T("pf.looking")));
          var changed = 0, bad = 0;
          var chain = todo.reduce(function (p, line) {
            return p.then(function () {
              return ask("feeds", line).then(function (r) {
                if (!r.found) { bad++; return; }
                var d = r.discovery;
                if (!d.is_feed && d.feeds.length) { lines[lines.indexOf(line)] = d.feeds[0].url; changed++; }
                else if (!d.is_feed) bad++;
              }).catch(function () { bad++; });
            });
          }, Promise.resolve());
          chain.then(function () {
            if (changed) { ta.value = lines.join("\n"); auto.feeds = ta.value; flash(ta); }
            note("feeds", changed || bad ? esc(T("pf.dd_feeds", { n: changed, b: bad })) : esc(T("pf.dd_feeds_ok", { n: todo.length })), bad ? "warn" : "");
          });
        };
        ta.addEventListener("change", fix);
        ta.addEventListener("paste", function () { setTimeout(fix, 0); });
      }
    }
  }
  var PL_TESTABLE = { wecom: 1, dingtalk: 1, email: 1 };
  function freePoolStatusText(s) {
    if (s.ready) return T("fp.ready");
    if (!s.installed) return T("fp.need_install");
    if (!s.enabled) return T("fp.need_enable");
    if (!s.approved) return T("fp.need_network");
    if (!s.training_acknowledged) return T("fp.need_consent");
    return s.error || T("fp.inactive");
  }
  function freePoolConfigModal(after) {
    return Promise.all([rpc("plugins.config_get", { id: "kilo-free-pool" }), rpc("free_pool.status")]).then(function (r) {
      var values = r[0].values || {}, status = r[1] || {}, selected = values.model || "kilo-auto/free";
      var option = function (id, label) { return '<option value="' + esc(id) + '">' + esc(label || id) + "</option>"; };
      modal('<div class="card-head"><span class="ico">' + ic("sparkle") + '</span><h2>' + esc(T("of.kilo-free-pool.n")) + '</h2></div><form id="free-pool-form">' +
        '<p class="hint">' + esc(T("fp.intro")) + '</p><p class="notice warn">' + esc(T("fp.privacy")) + '</p>' +
        '<label class="check"><input type="checkbox" name="acknowledge_prompt_training"' + (values.acknowledge_prompt_training === true ? " checked" : "") + '> ' + esc(T("fp.consent")) + '</label>' +
        '<label class="field"><span>' + esc(T("fp.model")) + '</span><select name="model">' + option(selected) + (selected !== "kilo-auto/free" ? option("kilo-auto/free") : "") + '</select><small class="hint">' + esc(T("fp.models_hint")) + '</small></label>' +
        '<button type="button" class="btn" id="fp-load">' + ic("refresh", "sm") + esc(T("fp.load")) + '</button>' +
        '<p class="hint" id="fp-model-info" aria-live="polite"></p><p class="notice" id="fp-status" aria-live="polite">' + esc(freePoolStatusText(status)) + '</p>' +
        '<p class="hint">' + esc(T("fp.use_hint")) + '</p><a class="link" href="https://kilo.ai/docs/gateway/models-and-providers" target="_blank" rel="noopener noreferrer">' + esc(T("fp.docs")) + '</a>' +
        '<p class="notice bad hidden" id="fp-error" role="alert"></p><div class="actions"><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button type="submit" class="btn primary">' + esc(T("btn.save")) + '</button></div></form>', function (root, close) {
        var form = $("#free-pool-form", root), ack = form.elements.acknowledge_prompt_training, model = form.elements.model, load = $("#fp-load", root), submit = $('button[type="submit"]', form), error = $("#fp-error", root), info = $("#fp-model-info", root), models = [], busy = false;
        var alive = function () { return document.contains(form); };
        var controls = function () { load.disabled = busy || !ack.checked; submit.disabled = busy; model.disabled = busy; ack.disabled = busy; };
        var showError = function (e) { if (alive()) { error.textContent = e.message; error.classList.remove("hidden"); } };
        var save = function () { return rpc("plugins.config_set", { id: "kilo-free-pool", values: { model: model.value, acknowledge_prompt_training: ack.checked } }); };
        var describe = function () {
          var m = models.filter(function (x) { return x.id === model.value; })[0];
          info.textContent = m ? T("fp.model_info", { n: models.length, context: m.context_length || "—", output: m.max_output_tokens || "—" }) : "";
        };
        ack.addEventListener("change", controls);
        model.addEventListener("change", describe);
        load.addEventListener("click", function () {
          if (busy || !ack.checked) return;
          busy = true; controls(); error.classList.add("hidden"); info.textContent = T("fp.loading");
          save().then(function () { return rpc("free_pool.models", { refresh: true }); }).then(function (d) {
            if (!alive()) return;
            var prev = model.value; models = d.models || [];
            model.innerHTML = models.length ? models.map(function (m) { return option(m.id, m.name ? m.name + " · " + m.id : m.id); }).join("") : option(prev);
            if (models.some(function (m) { return m.id === prev; })) model.value = prev;
            else if (models.some(function (m) { return m.id === "kilo-auto/free"; })) model.value = "kilo-auto/free";
            describe(); if (!models.length) info.textContent = T("fp.no_models");
            return rpc("free_pool.status").then(function (s) { if (alive()) $("#fp-status", root).textContent = freePoolStatusText(s); });
          }).catch(function (e) { info.textContent = ""; showError(e); }).finally(function () { busy = false; if (alive()) controls(); });
        });
        form.addEventListener("submit", function (e) {
          e.preventDefault(); if (busy) return;
          busy = true; controls(); error.classList.add("hidden");
          save().then(function () { if (!alive()) return; close(); toast(T("cfg.saved")); (after || refresh)(); }).catch(showError).finally(function () { busy = false; if (alive()) controls(); });
        });
        controls();
      });
    }).catch(fail);
  }
  function pluginConfigModal(p, after) {
    if (p.id === "kilo-free-pool" && p.source === "official") return freePoolConfigModal(after);
    rpc("plugins.config_get", { id: p.id }).then(function (cv) {
      var testable = PL_TESTABLE[p.id] && p.source === "official";
      modal('<div class="card-head"><span class="ico">' + ic("settings") + "</span><h2>" + esc(T("cfg.title", { n: plName(p) })) + '</h2></div><form id="pcf" novalidate>' + (state.dict["of." + p.id + ".cfg"] ? '<p class="hint">' + esc(T("of." + p.id + ".cfg")) + "</p>" : "") + schemaForm(cv.fields || [], cv.values || {}, cv.secrets_set || {}, p.id) + '<p class="notice bad hidden" id="pcf-err"></p><p class="notice ok hidden" id="pcf-ok"></p><div class="actions">' + (testable ? '<button type="button" class="btn" id="pcf-test">' + ic("send", "sm") + esc(T("of.test_send")) + '</button><span class="grow"></span>' : "") + '<button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T("btn.save")) + "</button></div></form>", function (root, close) {
        var f = $("#pcf", root), err = $("#pcf-err", root), okm = $("#pcf-ok", root);
        if (p.source === "official") wirePluginAutofill(f, p.id, cv.fields || []);
        var showErr = function (e) { okm.classList.add("hidden"); err.textContent = e.message; err.classList.remove("hidden"); };
        f.addEventListener("submit", function (e) {
          e.preventDefault();
          if (!f.reportValidity()) return;
          rpc("plugins.config_set", { id: p.id, values: readSchemaForm(f, cv.fields || []) }).then(function () { close(); toast(T("cfg.saved")); if (after) after(); }).catch(showErr);
        });
        if (testable) $("#pcf-test", root).addEventListener("click", function (e) {
          var b = e.currentTarget; b.disabled = true; err.classList.add("hidden");
          rpc("plugins.config_set", { id: p.id, values: readSchemaForm(f, cv.fields || []) }).then(function () { return rpc("plugins.test", { id: p.id }); })
            .then(function () { b.disabled = false; okm.textContent = T("of.test_ok"); okm.classList.remove("hidden"); }).catch(function (x) { b.disabled = false; showErr(x); });
        });
      });
    }).catch(fail);
  }
  function plName(p) { return (p && p.source === "official" && state.dict["of." + p.id + ".n"]) ? T("of." + p.id + ".n") : (p.name || p.id); }

