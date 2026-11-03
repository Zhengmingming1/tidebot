  // ---------- providers: list on the left, details on the right ----------
  var provSel = null, pvOpen = null;
  var PV_COLORS = ["#2563eb", "#0d9488", "#7c3aed", "#db2777", "#ea580c", "#16a34a", "#0891b2", "#4f46e5"];
  function pvDisplay(p, presetById) { if (p.managed_by === "kilo-free-pool") return T("of.kilo-free-pool.n"); var pr = presetById[p.preset] || presetById[p.name]; return pr && pr.name && pr.id !== "custom" ? pr.name : p.name; }
  function pvAvatar(p, presetById) {
    var n = pvDisplay(p || {}, presetById || {}) || "?", h = 0;
    for (var i = 0; i < n.length; i++) h = (h * 31 + n.charCodeAt(i)) >>> 0;
    return '<span class="pv-av av' + (h % PV_COLORS.length) + '" aria-hidden="true">' + esc(n.replace(/[^A-Za-z0-9\u4e00-\u9fff]/g, "").slice(0, 1).toUpperCase() || "?") + "</span>";
  }
  // Right-side drawer for editing one provider. Esc / backdrop closes.
  function openDrawer(fill) {
    closeDrawer();
    var bg = document.createElement("div"), opener = document.activeElement;
    bg.className = "drawer-bg"; bg.id = "drawer";
    bg.innerHTML = '<aside class="drawer" role="dialog" aria-modal="true" tabindex="-1"></aside>';
    document.body.appendChild(bg);
    var d = $(".drawer", bg);
    var onKey = function (e) { if (e.key === "Escape" && !$("#modal-root").innerHTML && !$(".mdl-pop", bg)) { e.preventDefault(); closeDrawer(); } };
    bg._off = function () { document.removeEventListener("keydown", onKey, true); if (opener && opener.focus && document.contains(opener)) opener.focus(); };
    document.addEventListener("keydown", onKey, true);
    bg.addEventListener("mousedown", function (e) { if (e.target === bg) closeDrawer(); });
    dialogKeys(d);
    fill(d);
        requestAnimationFrame(function () { bg.classList.add("open"); });
    var first = $("input:not([type=hidden]):not([type=radio])", d); (first || d).focus();
    return d;
  }
  function closeDrawer() { var bg = $("#drawer"); if (bg) { if (bg._off) bg._off(); bg.remove(); } }
  function providerCards(box) {
    return api("/api/providers").then(function (d) {
      var ps = d.providers || [], presets = d.presets || [], byName = {}, presetById = {};
      ps.forEach(function (p) { byName[p.name] = p; });
      presets.forEach(function (p) { presetById[p.id] = p; });
      var configured = function (p) { return p.default || p.key_set || p.used_by > 0; };
      var shown = ps.filter(configured), hidden = ps.filter(function (p) { return !configured(p); });
      var card = function (p) {
        return '<li class="pv-card" draggable="true" data-pv="' + esc(p.name) + '" tabindex="0" role="button" aria-label="' + esc(pvDisplay(p, presetById)) + '">' + pvAvatar(p, presetById) +
          '<div class="pv-main"><div class="pv-title"><strong>' + esc(pvDisplay(p, presetById)) + "</strong>" + (p.default ? '<span class="pill ok sm">' + esc(T("pv.default")) + "</span>" : "") + "</div>" +
          '<small class="pv-meta"><code>' + esc(p.managed_by ? T("fp.managed") : (p.base_url || "https://api.anthropic.com").replace(/^https?:\/\//, "")) + "</code>" + (p.model ? " · " + esc(p.model) : "") + "</small></div>" +
          '<span class="pv-key ' + (p.key_set ? "ok" : "") + '"><span class="dot ' + (p.key_set ? "ok" : "") + '"></span>' + esc(T(p.managed_by ? "fp.no_key" : p.key_set ? "pv.key_ok" : "pv.key_none")) + "</span>" + ic("chev", "sm") + "</li>";
      };
      box.innerHTML = (shown.length ? '<ul class="pv-cards" id="pv-ul" aria-label="' + esc(T("set.providers")) + '">' + shown.map(card).join("") + "</ul>" : '<p class="muted pv-empty">' + esc(T("pv.none_configured")) + "</p>") +
        (hidden.length ? '<details class="pv-more"><summary>' + esc(T("pv.more", { n: hidden.length })) + '</summary><ul class="pv-cards">' + hidden.map(card).join("") + "</ul></details>" : "") +
        '<small class="hint">' + esc(T("pv.sort_hint", { k: keyLabel("Alt+↑/↓") })) + "</small>";
      // Re-render, then reopen through the *new* render's opener: the old closure's
      // byName predates a just-created provider (that crashed on "preset.id").
      var again = function (sel) { return providerCards(box).then(function () { if (sel && $("#drawer") && pvOpen) pvOpen(sel); }); };
      var open = pvOpen = function (n) { provSel = n; openDrawer(function (dw) { providerDetail(dw, byName[n] || null, null, presetById, again); }); };
      $$(".pv-card", box).forEach(function (li) {
        li.addEventListener("click", function () { open(li.getAttribute("data-pv")); });
        li.addEventListener("keydown", function (e) {
          if (e.key === "Enter" || e.key === " ") { e.preventDefault(); open(li.getAttribute("data-pv")); }
          if (e.altKey && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
            e.preventDefault();
            var sib = e.key === "ArrowUp" ? li.previousElementSibling : li.nextElementSibling;
            if (!sib) return;
            li.parentNode.insertBefore(li, e.key === "ArrowUp" ? sib : sib.nextSibling); li.focus(); saveOrder();
          }
        });
        li.addEventListener("dragstart", function (e) { li.classList.add("dragging"); e.dataTransfer.effectAllowed = "move"; e.dataTransfer.setData("text/plain", li.getAttribute("data-pv")); });
        li.addEventListener("dragend", function () { li.classList.remove("dragging"); saveOrder(); });
      });
      $$(".pv-cards", box).forEach(function (ul) {
        ul.addEventListener("dragover", function (e) {
          e.preventDefault();
          var drag = $(".dragging", ul); if (!drag) return;
          var after = $$("li:not(.dragging)", ul).filter(function (x) { var r = x.getBoundingClientRect(); return e.clientY < r.top + r.height / 2; })[0];
          if (after) ul.insertBefore(drag, after); else ul.appendChild(drag);
        });
      });
      function saveOrder() {
        var names = $$(".pv-card", box).map(function (x) { return x.getAttribute("data-pv"); });
        api("/api/providers/order", { method: "POST", body: { names: names } }).then(function () { toast(T("pv.order_saved")); }).catch(fail);
      }
      // "Add provider" sits in the card head, outside box: assign (not add) so a re-render
      // doesn't stack handlers, and honour a click that came before the list loaded.
      var addBtn = $("#pv-add");
      if (addBtn) {
        addBtn.onclick = function () { presetPicker(presets, function (pr) { provSel = "+new"; openDrawer(function (dw) { providerDetail(dw, null, pr, presetById, again); }); }); };
        if (addBtn._want) { addBtn._want = false; addBtn.onclick(); }
      }
    });
  }
  // Brand metadata for the "add provider" card wall. cat: official|cn|coding|local; tag: i18n key; b: brand color class.
  var PV_BRAND = {
    "anthropic": { zh: "Anthropic", en: "Anthropic", cat: "official", tag: "ap.t_official", b: "br-anthropic", m: "A" },
    "kimi-cn": { zh: "Kimi", en: "Kimi (China)", cat: "cn", tag: "ap.t_cn", b: "br-kimi", m: "K" },
    "kimi": { zh: "Kimi 国际版", en: "Kimi (global)", cat: "cn", tag: "ap.t_intl", b: "br-kimi", m: "K" },
    "deepseek": { zh: "DeepSeek", en: "DeepSeek", cat: "cn", tag: "ap.t_cn", b: "br-deepseek", m: "D" },
    "zhipu": { zh: "智谱 GLM", en: "Zhipu GLM", cat: "cn", tag: "ap.t_cn", b: "br-zhipu", m: "智" },
    "zai": { zh: "Z.AI GLM", en: "Z.AI GLM", cat: "cn", tag: "ap.t_intl", b: "br-zhipu", m: "Z" },
    "qwen": { zh: "通义千问", en: "Qwen", cat: "cn", tag: "ap.t_cn", b: "br-qwen", m: "通" },
    "kimi-coding": { zh: "Kimi Code", en: "Kimi Code", cat: "coding", tag: "ap.t_cp_cn", b: "br-kimi", m: "K" },
    "kimi-coding-intl": { zh: "Kimi Code", en: "Kimi Code", cat: "coding", tag: "ap.t_cp_intl", b: "br-kimi", m: "K" },
    "qwen-coding": { zh: "通义千问 Coding", en: "Qwen Coding", cat: "coding", tag: "ap.t_cp_cn", b: "br-qwen", m: "通" },
    "qwen-coding-intl": { zh: "通义千问 Coding", en: "Qwen Coding", cat: "coding", tag: "ap.t_cp_intl", b: "br-qwen", m: "通" },
    "magpie": { zh: "Magpie", en: "Magpie", cat: "local", tag: "ap.t_local", b: "br-magpie", m: "M" }
  };
  function presetPicker(presets, pick) {
    var zh = (state.lang || "").indexOf("zh") === 0;
    var cats = [["all", "ap.c_all", "home"], ["official", "ap.c_official", "shield"], ["cn", "ap.c_cn", "globe"], ["coding", "ap.c_coding", "code"], ["local", "ap.c_local", "user"]];
    var custom = presets.filter(function (p) { return p.id === "custom"; })[0];
    var order = Object.keys(PV_BRAND), rank = function (p) { var k = order.indexOf(p.id); return k < 0 ? 99 : k; };
    var list = presets.filter(function (p) { return p.id !== "custom"; }).sort(function (a, b) { return rank(a) - rank(b); });
    var tile = function (p) {
      var m = PV_BRAND[p.id] || { zh: p.name, en: p.name, cat: "local", tag: "ap.t_local", b: "br-other", m: (p.name || "?").slice(0, 1).toUpperCase() };
      var nm = zh ? m.zh : m.en;
      return '<button type="button" role="option" aria-selected="false" class="ap-tile" data-pr="' + presets.indexOf(p) + '" data-cat="' + m.cat + '" data-q="' + esc((p.name + " " + p.id + " " + m.zh + " " + m.en).toLowerCase()) + '" title="' + esc(p.name) + '">' +
        '<span class="ap-chk" aria-hidden="true">' + ic("check", "sm") + "</span>" +
        '<span class="ap-logo ' + m.b + '" aria-hidden="true">' + esc(m.m) + "</span><strong>" + esc(nm) + '</strong><span class="ap-tag">' + esc(T(m.tag)) + '</span><small class="muted">' + esc(T("ap.def_model", { m: p.model || (p.id === "anthropic" ? T("ap.m_claude") : T("ap.m_gw")) })) + "</small></button>";
    };
    modal('<div class="ap"><div class="ap-head"><h2>' + esc(T("pv.add")) + '</h2><button type="button" class="icon-btn" data-close aria-label="' + esc(T("btn.close")) + '">' + ic("x") + "</button></div>" +
      '<div class="ap-search">' + ic("search", "sm") + '<input type="search" id="pr-q" autofocus placeholder="' + esc(T("ap.search")) + '" aria-label="' + esc(T("ap.search")) + '"></div>' +
      '<div class="ap-body"><nav class="ap-rail" role="tablist">' + cats.map(function (c, i) { return '<button type="button" role="tab" data-cat="' + c[0] + '" class="' + (i ? "" : "active") + '" aria-selected="' + !i + '">' + ic(c[2], "sm") + esc(T(c[1])) + "</button>"; }).join("") + "</nav>" +
      '<div class="ap-grid presets" role="listbox" aria-label="' + esc(T("pv.add")) + '">' + list.map(tile).join("") +
      (custom ? '<button type="button" role="option" aria-selected="false" class="ap-tile ap-custom" data-pr="' + presets.indexOf(custom) + '" data-cat="local" data-q="custom 自定义 ' + esc(custom.name.toLowerCase()) + '"><span class="ap-chk" aria-hidden="true">' + ic("check", "sm") + "</span>" + ic("plus") + "<strong>" + esc(T("ap.custom")) + '</strong><small class="muted">' + esc(T("ap.custom_sub")) + "</small></button>" : "") +
      '<p class="muted ap-empty hidden">' + esc(T("ap.none")) + "</p></div></div>" +
      '<div class="ap-foot"><span class="muted">' + ic("info", "sm") + esc(T("ap.hint")) + '</span><span class="grow"></span><button type="button" class="btn" data-close>' + esc(T("btn.cancel")) + '</button><button type="button" class="btn primary" id="ap-next" disabled>' + esc(T("ap.next")) + "</button></div></div>",
    function (root, close) {
      var q = $("#pr-q", root), cat = "all", sel = -1, next = $("#ap-next", root);
      var tiles = function () { return $$(".ap-tile", root).filter(function (t) { return !t.classList.contains("hidden"); }); };
      var select = function (t, focus) {
        $$(".ap-tile", root).forEach(function (x) { x.classList.toggle("on", x === t); x.setAttribute("aria-selected", x === t); });
        sel = t ? +t.getAttribute("data-pr") : -1; next.disabled = sel < 0;
        if (t) { t.scrollIntoView({ block: "nearest" }); if (focus) t.focus(); }
      };
      var filter = function () {
        var v = q.value.trim().toLowerCase();
        $$(".ap-tile", root).forEach(function (t) { t.classList.toggle("hidden", (cat !== "all" && t.getAttribute("data-cat") !== cat) || (v && t.getAttribute("data-q").indexOf(v) < 0)); });
        var vis = tiles(); $(".ap-empty", root).classList.toggle("hidden", vis.length > 0);
        var cur = vis.filter(function (t) { return +t.getAttribute("data-pr") === sel; })[0];
        select(cur || vis[0] || null, false);
      };
      var go = function () { if (sel >= 0) { var p = presets[sel]; close(); pick(p); } };
      imeInput(q, 0, filter);
      $$(".ap-rail button", root).forEach(function (b) { b.addEventListener("click", function () { cat = b.getAttribute("data-cat"); $$(".ap-rail button", root).forEach(function (x) { x.classList.toggle("active", x === b); x.setAttribute("aria-selected", x === b); }); filter(); }); });
      $$(".ap-tile", root).forEach(function (t) { t.addEventListener("click", function () { select(t, false); }); t.addEventListener("dblclick", go); });
      next.addEventListener("click", go);
      root.addEventListener("keydown", function (e) {
        if (e.target.closest && e.target.closest(".ap-rail")) return;
        var vis = tiles(), i = vis.findIndex(function (t) { return +t.getAttribute("data-pr") === sel; });
        if (e.key === "Enter" && !(e.target.tagName === "BUTTON" && !e.target.classList.contains("ap-tile"))) { e.preventDefault(); go(); return; }
        var grid = $(".ap-grid", root); if (!grid) return; // Esc already closed the picker
        var cols = (getComputedStyle(grid).gridTemplateColumns.split(" ").length) || 3;
        var d = { ArrowRight: 1, ArrowLeft: -1, ArrowDown: cols, ArrowUp: -cols }[e.key];
        if (!d || !vis.length) return;
        if (e.target === q && (e.key === "ArrowLeft" || e.key === "ArrowRight")) return;
        e.preventDefault();
        var j = i < 0 ? 0 : Math.max(0, Math.min(vis.length - 1, i + d));
        select(vis[j], e.target !== q);
      });
      filter();
    });
    var box = $("#modal-root .modal"); if (box) box.classList.add("ap-modal");
  }
  // Parse a pasted env block: `export ANTHROPIC_BASE_URL=…`, `set K=V`, `$env:K="V"`, `K: V`,
  // or JSON ({"env": {...}} from a Claude Code settings.json, or a flat object).
  function parseEnvPaste(text) {
    var out = {}, t = String(text || "").trim();
    try { var j = JSON.parse(t); if (j && typeof j === "object") { var o = j.env && typeof j.env === "object" ? j.env : j; Object.keys(o).forEach(function (k) { if (/^[A-Z_][A-Z0-9_]*$/.test(k) && (typeof o[k] === "string" || typeof o[k] === "number")) out[k] = String(o[k]); }); return out; } } catch (e) { /* not JSON */ }
    t.split(/\r?\n/).forEach(function (l) {
      var m = l.trim().replace(/^(export|set|setx)\s+/i, "").replace(/^\$env:/i, "").replace(/[,;]$/, "").match(/^"?([A-Z_][A-Z0-9_]*)"?\s*(?:=|:)\s*(.*)$/);
      if (m) out[m[1]] = m[2].trim().replace(/^(["'])(.*)\1$/, "$2");
    });
    return out;
  }
  // Add-provider auto-fill: a pasted Base URL (or a whole env block) picks the matching preset
  // (POST /api/providers/infer) and fills name, auth header and models. Only the first 7 key
  // characters are ever sent, and only fields the user hasn't typed into are filled.
  var PV_MODEL_ENV = { ANTHROPIC_MODEL: "model", ANTHROPIC_SMALL_FAST_MODEL: "small_fast_model", ANTHROPIC_DEFAULT_OPUS_MODEL: "opus_model", ANTHROPIC_DEFAULT_SONNET_MODEL: "sonnet_model", ANTHROPIC_DEFAULT_HAIKU_MODEL: "haiku_model" };
  function wireProviderAutofill(host, f, v, presetById) {
    var det = $("#pd-detect", host), auto = {}, mine = {}, models = {}, timer = null, seq = 0, startPreset = v.preset;
    var zh = (state.lang || "").indexOf("zh") === 0;
    ["name", "model", "small_fast_model", "opus_model", "sonnet_model", "haiku_model"].forEach(function (n) {
      f[n].addEventListener("input", function (e) { if (e.isTrusted) mine[n] = f[n].value !== auto[n]; });
    });
    $$('[name="auth_env"]', f).forEach(function (r) { r.addEventListener("change", function (e) { if (e.isTrusted) mine.auth_env = true; }); });
    var flash = function (el) { el.classList.remove("af-flash"); void el.offsetWidth; el.classList.add("af-flash"); };
    var put = function (n, val) {
      if (!val || mine[n]) return 0;
      if (f[n].value && f[n].value !== auto[n] && f[n].value !== (v[n] || "")) return 0;
      if (f[n].value === val) return 0;
      f[n].value = val; auto[n] = val; flash(f[n]); return 1;
    };
    var setAuth = function (a) {
      if (!a || mine.auth_env) return 0;
      var r = $('[name="auth_env"][value="' + a + '"]', f); if (!r || r.checked) return 0;
      r.checked = true; flash(r.closest(".seg")); return 1;
    };
    var paint = function (g) {
      if (!g || !g.preset) { det.classList.remove("on"); det.innerHTML = ic("sparkle", "sm") + "<span>" + esc(g && g.name ? T("pv.af_detected", { n: g.name }) : T("pv.af_paste_hint")) + "</span>"; return; }
      var m = PV_BRAND[g.preset] || { b: "br-other", m: (g.display || "?").slice(0, 1), zh: g.display, en: g.display };
      det.classList.add("on");
      det.innerHTML = '<span class="ap-logo ' + m.b + '" aria-hidden="true">' + esc(m.m) + '</span><span class="af-pill" data-tip="' + esc(T("pv.af_tip")) + '">' + ic("sparkle", "sm") + esc(T("pv.af_detected", { n: (zh ? m.zh : m.en) || g.display })) + "</span>" + (g.fixed && g.base_url ? '<span class="muted">' + esc(T("pv.af_completed")) + "</span>" : "");
    };
    var infer = function (quiet) {
      var my = ++seq, url = f.base_url.value.trim(), key = f.api_key.value.trim();
      if (!url && !key) { paint(null); return Promise.resolve(0); }
      return apiRaw("/api/providers/infer", { method: "POST", body: { base_url: url, key_prefix: key.slice(0, 7), env: models } }).then(function (g) {
        if (my !== seq || !g) return 0;
        var n = 0;
        if (g.fixed && url && g.base_url && g.base_url !== url && (quiet === "paste" || document.activeElement !== f.base_url)) { f.base_url.value = g.base_url; flash(f.base_url); n++; }
        if (startPreset === "custom" && v.preset !== (g.preset || "custom")) {
          v.preset = g.preset || "custom";
          var av = $(".drawer-head > :first-child", host), h2 = $(".drawer-head h2", host), pr = presetById[v.preset];
          if (av) av.outerHTML = pvAvatar(v, presetById);
          if (h2 && pr) h2.textContent = T("pv.add_named", { n: pr.name });
        }
        n += put("name", g.name) + setAuth(g.auth_env);
        Object.keys(g.models || {}).forEach(function (k) { n += put(k, g.models[k]); });
        paint(g);
        return n;
      }).catch(function () { return 0; });
    };
    f.base_url.addEventListener("input", function () { clearTimeout(timer); timer = setTimeout(infer, 400); });
    f.base_url.addEventListener("blur", function () { clearTimeout(timer); infer(); });
    // A pasted env block anywhere in the basics fills every field it names; a URL pasted
    // into the key box moves to Base URL; an sk-ant- key selects x-api-key.
    var onPaste = function (e) {
      var text = (e.clipboardData || window.clipboardData).getData("text") || "", env = parseEnvPaste(text);
      var target = e.target.name, filled = 0;
      if (!Object.keys(env).length) {
        if (target === "api_key" && /^\s*(https?:\/\/|[\w-]+(\.[\w-]+)+\/)/i.test(text)) { e.preventDefault(); f.base_url.value = text.trim(); toast(T("pv.af_url_moved")); infer("paste"); return; }
        if (target === "api_key") setTimeout(function () { if (/^sk-ant-/.test(f.api_key.value.trim())) setAuth("ANTHROPIC_API_KEY"); if (!f.base_url.value.trim()) infer(); }, 0);
        if (target === "base_url") { e.preventDefault(); f.base_url.value = text.trim(); infer("paste"); }
        return;
      }
      e.preventDefault();
      var extra = [];
      Object.keys(env).forEach(function (k) {
        var val = env[k];
        if (k === "ANTHROPIC_BASE_URL") { f.base_url.value = val; filled++; }
        else if (k === "ANTHROPIC_AUTH_TOKEN" || k === "ANTHROPIC_API_KEY") { f.api_key.value = val; filled++; mine.auth_env = false; setAuth(k); }
        else if (PV_MODEL_ENV[k]) { models[k] = val; }
        else extra.push(k + "=" + val);
      });
      if (extra.length) {
        var have = f.extra_env.value.split("\n").map(function (l) { return l.split("=")[0].trim(); });
        var add = extra.filter(function (l) { return have.indexOf(l.split("=")[0]) < 0; });
        if (add.length) { f.extra_env.value = (f.extra_env.value.trim() ? f.extra_env.value.trim() + "\n" : "") + add.join("\n"); filled += add.length; var adv = f.querySelector(".sec-adv"); if (adv) adv.open = true; }
      }
      infer("paste").then(function (n) { toast(T("pv.af_filled", { n: filled + n })); });
    };
    ["name", "base_url", "api_key"].forEach(function (n) { f[n].addEventListener("paste", onPaste); });
  }
  var pvJustCreated = "";
  function freePoolProviderDetail(host, p, done) {
    host.innerHTML = '<div class="drawer-head"><div class="grow-col"><h2>' + esc(T("of.kilo-free-pool.n")) + '</h2><small class="muted">kilo-free · ' + esc(T("fp.managed")) + '</small></div><button type="button" class="icon-btn" data-drawer-close aria-label="' + esc(T("btn.close")) + '">' + ic("x") + '</button></div>' +
      '<div class="drawer-body"><p class="notice">' + esc(T("fp.managed_hint")) + '</p><p><strong>' + esc(T("fp.model")) + '</strong><br><code>' + esc(p.model) + '</code></p><p>' + esc(T("fp.no_key")) + ' · ' + esc(T("fp.free_only")) + '</p><p class="notice warn">' + esc(T("fp.privacy")) + '</p><p class="hint">' + esc(T("fp.use_hint")) + '</p><div id="fp-test-result" aria-live="polite"></div></div>' +
      '<div class="drawer-foot">' + (p.default ? '<span class="pill ok">' + esc(T("pv.default")) + '</span>' : '<button type="button" class="btn ghost" id="fp-default">' + esc(T("pv.make_default")) + '</button>') + '<span class="grow"></span><button type="button" class="btn" id="fp-test">' + esc(T("pv.check")) + '</button><button type="button" class="btn primary" id="fp-config">' + esc(T("cfg.open")) + '</button></div>';
    $("[data-drawer-close]", host).addEventListener("click", closeDrawer);
    $("#fp-config", host).addEventListener("click", function () { closeDrawer(); freePoolConfigModal(function () { done(); }); });
    var def = $("#fp-default", host);
    if (def) def.addEventListener("click", function () { api("/api/providers/" + enc(p.name) + "/default", { method: "POST", body: {} }).then(function () { toast(T("pv.default_set")); done(p.name); }).catch(fail); });
    $("#fp-test", host).addEventListener("click", function (e) {
      var btn = e.currentTarget, result = $("#fp-test-result", host); btn.disabled = true; result.textContent = T("pv.testing");
      api("/api/providers/" + enc(p.name) + "/test", { method: "POST", body: { model: p.model } }).then(function (r) {
        result.textContent = r.ok ? T("pv.test_ok", { ms: r.latency_ms, m: r.model || p.model }) : r.error || T("pv.test_bad", { s: r.status || "—" });
      }).catch(function (err) { result.textContent = err.message; }).finally(function () { btn.disabled = false; });
    });
  }
  function providerDetail(host, p, preset, presetById, done) {
    if (p && p.managed_by === "kilo-free-pool") return freePoolProviderDetail(host, p, done);
    var creating = !p || p.dup;
    var base = p ? (presetById[p.preset] || null) : preset;
    var v = p || { name: preset.id === "custom" ? "" : preset.id, preset: preset.id, base_url: preset.base_url || "", auth_env: preset.auth_env || "ANTHROPIC_AUTH_TOKEN", model: preset.model || "", small_fast_model: preset.small_fast_model || "", opus_model: preset.opus_model || "", sonnet_model: preset.sonnet_model || "", haiku_model: preset.haiku_model || "", extra_env: preset.extra_env || {} };
    if (p && p.dup) v = Object.assign({}, p, { name: p.name + "-copy", key_set: false, key_source: "" });
    var envText = Object.keys(v.extra_env || {}).map(function (k) { return k + "=" + v.extra_env[k]; }).join("\n");
    var inp = function (n, val, extra) { return '<input name="' + n + '" value="' + esc(val || "") + '"' + (extra || "") + ">"; };
    var models = [["model", T("pv.m_main")], ["small_fast_model", T("pv.m_fast")], ["opus_model", "Opus"], ["sonnet_model", "Sonnet"], ["haiku_model", "Haiku"]];
    var link = base && base.source_url ? '<a class="link" href="' + esc(base.source_url) + '" target="_blank" rel="noopener noreferrer">' + ic("globe", "sm") + esc(T("pv.get_key")) + "</a>" : "";
    var seg = function (val, label, sub) { return '<label class="seg-opt"><input type="radio" name="auth_env" value="' + val + '"' + ((val === "ANTHROPIC_API_KEY") === (v.auth_env === "ANTHROPIC_API_KEY") ? " checked" : "") + "><span><strong>" + esc(label) + "</strong><small>" + esc(sub) + "</small></span></label>"; };
    var title = creating ? (p ? T("pv.dup_title") : T("pv.add_named", { n: preset.name })) : pvDisplay(v, presetById);
    host.innerHTML = '<div class="drawer-head">' + pvAvatar(v, presetById) + '<div class="grow-col"><h2>' + esc(title) + "</h2>" +
      (!creating ? '<small class="muted"><code>' + esc(v.name) + "</code> · " + (v.default ? esc(T("pv.default")) + " · " : "") + (v.used_by ? esc(plural(v.used_by, "tasks.count_one", "tasks.count_many")) : esc(T("pv.unused"))) + "</small>" : "") + "</div>" +
      '<button type="button" class="icon-btn" data-drawer-close aria-label="' + esc(T("btn.close")) + '">' + ic("x") + "</button></div>" +
      '<form id="pvf" class="drawer-body" autocomplete="off">' +
      '<h4 class="sec">' + esc(T("pv.sec_basic")) + "</h4>" +
      fieldHtml(T("set.name"), inp("name", v.name, ' required maxlength="40" pattern="[a-z0-9][a-z0-9._\\-]{0,39}"'), T("pv.name_hint")) +
      '<label class="field"><span>Base URL' + tip(T("pv.url_hint")) + '</span><div class="in-group">' + inp("base_url", v.base_url, ' type="url" placeholder="https://api.anthropic.com"') + (base && base.base_url !== undefined ? '<button type="button" class="btn ghost" id="pd-reset" title="' + esc(base.base_url || "https://api.anthropic.com") + '">' + ic("rollback", "sm") + esc(T("pv.reset_url")) + "</button>" : "") + "</div></label>" +
      (creating && !p ? '<div class="pv-detect" id="pd-detect" aria-live="polite">' + ic("sparkle", "sm") + "<span>" + esc(T("pv.af_paste_hint")) + "</span></div>" : "") +
      '<h4 class="sec">' + esc(T("pv.sec_auth")) + " " + link + "</h4>" +
      '<div class="field"><span>' + esc(T("pv.auth")) + '</span><div class="seg" role="radiogroup" aria-label="' + esc(T("pv.auth")) + '">' + seg("ANTHROPIC_AUTH_TOKEN", "Bearer", "ANTHROPIC_AUTH_TOKEN") + seg("ANTHROPIC_API_KEY", "x-api-key", "ANTHROPIC_API_KEY") + "</div></div>" +
      '<label class="field"><span>' + esc(T("pv.api_key")) + tip(T("pv.key_hint")) + '</span><div class="in-group"><input name="api_key" type="password" autocomplete="new-password" spellcheck="false" placeholder="' + esc(!creating && v.key_set && v.key_source === "store" ? T("pv.key_keep", { l: v.key_last4 || "" }) : !creating && v.key_source === "env" ? T("pv.key_env") : T("pv.key_new")) + '"><button type="button" class="btn ghost" id="pd-eye" aria-pressed="false" aria-label="' + esc(T("pv.show_key")) + '">' + esc(T("pv.show")) + "</button></div></label>" +
      (!creating && v.key_source === "store" ? '<label class="check"><input type="checkbox" name="clear_key"> ' + esc(T("pv.clear_key")) + "</label>" : "") +
      '<h4 class="sec">' + esc(T("pv.models")) + '<button type="button" class="btn ghost sm pv-fetch" id="pd-fetch"' + (creating ? ' disabled data-tip="' + esc(T("pv.check_after_save")) + '"' : "") + ">" + ic("download", "sm") + "<span>" + esc(T("pv.fetch_models")) + "</span></button></h4>" +
      '<div class="model-list">' + models.map(function (m) { return '<div class="model-row"><label for="pm-' + m[0] + '">' + esc(m[1]) + '</label><div class="in-group"><input id="pm-' + m[0] + '" name="' + m[0] + '" value="' + esc(v[m[0]] || "") + '" maxlength="128" spellcheck="false"><button type="button" class="btn ghost" data-check="' + m[0] + '"' + (creating ? " disabled" : "") + ">" + esc(T("pv.check")) + '</button></div><span class="mres" data-res="' + m[0] + '"></span></div>'; }).join("") + "</div>" + (creating ? '<small class="hint">' + esc(T("pv.check_after_save")) + "</small>" : "") +
      (!creating ? '<h4 class="sec" id="pd-price-h">' + esc(T("pr.sec")) + '</h4><div id="pd-price" class="pr-box"><p class="muted">…</p></div>' : "") +
      '<details class="sec-adv"><summary class="sec">' + esc(T("pv.sec_adv")) + "</summary>" +
      fieldHtml(T("pv.extra_env"), '<textarea name="extra_env" rows="3" placeholder="API_TIMEOUT_MS=600000">' + esc(envText) + "</textarea>", T("pv.extra_hint")) +
      (!creating ? '<div class="row-actions">' + (v.default ? "" : '<button type="button" class="btn" id="pd-def">' + esc(T("pv.make_default")) + "</button>") + '<button type="button" class="btn" id="pd-dup">' + esc(T("pv.dup")) + "</button></div>" : "") + "</details>" +
      '<div class="pv-test" id="pd-test" aria-live="polite"></div>' +
      '<div class="drawer-foot">' + (!creating && !v.default ? '<button type="button" class="btn ghost danger-text" id="pd-del">' + ic("trash", "sm") + esc(T("btn.remove")) + "</button>" : "") + '<span class="grow"></span>' +
      '<button type="submit" class="btn" data-mode="save">' + esc(T("btn.save")) + '</button><button type="submit" class="btn primary" data-mode="test">' + ic("check", "sm") + esc(T("pv.save_test")) + "</button></div></form>";
    $$("[data-drawer-close]", host).forEach(function (b) { b.addEventListener("click", closeDrawer); });
    var f = $("#pvf", host), out = $("#pd-test", host), mode = "save";
    if (!creating) pricingEditor($("#pd-price", host), v.name);
    if (!creating && pvJustCreated && pvJustCreated === v.name) {
      pvJustCreated = "";
      var ms = models.filter(function (m) { return (v[m[0]] || "").trim(); });
      if (ms.length && v.key_set) {
        var offer = document.createElement("div"); offer.className = "pv-offer"; $("#pd-test", host).parentNode.insertBefore(offer, $("#pd-test", host));
        offer.innerHTML = '<p class="notice">' + ic("sparkle", "sm") + "<span>" + esc(T("pv.af_check_offer", { n: ms.length })) + '</span><button type="button" class="btn sm" id="pd-check-all">' + ic("check", "sm") + esc(T("pv.af_check_all")) + "</button></p>";
        $("#pd-check-all", host).addEventListener("click", function (e) {
          e.currentTarget.disabled = true;
          ms.reduce(function (p, m) { return p.then(function () { return runTest(v.name, v[m[0]], $('[data-res="' + m[0] + '"]', host)); }); }, Promise.resolve()).then(function () { offer.innerHTML = ""; });
        });
      }
    }
    var runTest = function (name, model, el) {
      el.innerHTML = '<span class="muted">' + esc(T("pv.testing")) + "</span>";
      return api("/api/providers/" + enc(name) + "/test", { method: "POST", body: { model: model || "" } }).then(function (r) {
        el.innerHTML = r.ok ? '<span class="pill ok">' + ic("check", "sm") + esc(T("pv.test_ok", { ms: r.latency_ms, m: r.model || "" })) + "</span>" : '<span class="pill bad">' + esc(T("pv.test_bad", { s: r.status || "—" })) + '</span> <small class="err">' + esc(r.error || "") + "</small>";
      }).catch(function (e) { el.innerHTML = '<span class="pill bad">' + esc(e.message) + "</span>"; });
    };
    $("#pd-eye", host).addEventListener("click", function (e) { var k = f.api_key, on = k.type === "password"; k.type = on ? "text" : "password"; e.currentTarget.setAttribute("aria-pressed", on); e.currentTarget.textContent = T(on ? "pv.hide" : "pv.show"); });
    var rs = $("#pd-reset", host); if (rs) rs.addEventListener("click", function () { f.base_url.value = base.base_url || ""; f.base_url.focus(); });
    if (creating && !p) wireProviderAutofill(host, f, v, presetById);
    if (!creating) wireModelPicker(host, f, v, models);
    $$("[data-check]", host).forEach(function (b) { b.addEventListener("click", function () { var k = b.getAttribute("data-check"); if (!f[k].value.trim()) { toast(T("pv.model_empty"), true); return; } runTest(v.name, f[k].value.trim(), $('[data-res="' + k + '"]', host)); }); });
    var tb = $("#pd-test-btn", host); if (tb) tb.addEventListener("click", function () { runTest(v.name, "", out); });
    $$("[data-mode]", f).forEach(function (b) { b.addEventListener("click", function () { mode = b.getAttribute("data-mode"); }); });
    var dd = $("#pd-def", host); if (dd) dd.addEventListener("click", function () { api("/api/providers/" + enc(v.name) + "/default", { method: "POST", body: {} }).then(function () { toast(T("pv.default_set")); done(v.name); }).catch(fail); });
    var dp = $("#pd-dup", host); if (dp) dp.addEventListener("click", function () { var c = JSON.parse(JSON.stringify(p)); c.dup = true; providerDetail(host, c, null, presetById, done); });
    var dl = $("#pd-del", host); if (dl) dl.addEventListener("click", function () { confirmDialog({ title: T("pv.del_title"), body: T("pv.confirm_del", { n: v.name }), ok: T("btn.remove"), danger: true }).then(function (y) { if (y) api("/api/providers/" + enc(v.name), { method: "DELETE" }).then(function () { toast(T("pv.deleted")); provSel = null; closeDrawer(); done(); }).catch(fail); }); });
    f.addEventListener("submit", function (e) {
      e.preventDefault();
      var env = {}, bad = "";
      f.extra_env.value.split("\n").forEach(function (l) { l = l.trim(); if (!l) return; var i = l.indexOf("="); if (i < 1) { bad = l; return; } env[l.slice(0, i).trim()] = l.slice(i + 1).trim(); });
      if (bad) { toast(T("pv.env_bad", { l: bad }), true); return; }
      var body = { original_name: creating ? "" : v.name, name: f.name.value.trim(), preset: v.preset || "", base_url: f.base_url.value.trim(), auth_env: f.auth_env.value, model: f.model.value.trim(), small_fast_model: f.small_fast_model.value.trim(),
        opus_model: f.opus_model.value.trim(), sonnet_model: f.sonnet_model.value.trim(), haiku_model: f.haiku_model.value.trim(), extra_env: env, clear_key: !!(f.clear_key && f.clear_key.checked) };
      if (f.api_key.value) body.api_key = f.api_key.value;
      var testAfter = mode === "test";
      api("/api/providers", { method: "POST", body: body }).then(function (saved) {
        if (creating) pvJustCreated = saved.name;
        f.api_key.value = "";
        toast(T("pv.saved"));
        return done(saved.name).then(function () { if (testAfter) { var el = $("#pd-test"); if (el) runTest(saved.name, "", el); } });
      }).catch(fail);
    });
  }

  // Model fields pick from the provider's own list (POST /api/providers/{name}/models asks its
  // GET …/v1/models with the saved key; the server keeps it 10 minutes). Fetched by the button,
  // or by itself the first time a model field gets focus when a key is saved. Typing filters,
  // ↑/↓ + Enter picks, Esc closes; any other name can still be typed. A filled-in model that is
  // not on the list gets a quiet note. Idea from cc-switch ModelInputWithFetch / ModelDropdown.
  function wireModelPicker(host, f, v, models) {
    var btn = $("#pd-fetch", host), list = null, busy = null, pop = null, popFor = null, sel = -1, items = [], picking = false;
    var label = function (t, icon) { btn.innerHTML = ic(icon || "download", "sm") + "<span>" + esc(t) + "</span>"; };
    var noteUnlisted = function () {
      if (!list) return;
      var ids = {}; list.models.forEach(function (m) { ids[m.id] = true; });
      models.forEach(function (m) {
        var el = $('[data-res="' + m[0] + '"]', host), val = f[m[0]].value.trim();
        if (!el || (el.innerHTML && !el.querySelector(".mdl-unlisted"))) return; // a check result stays
        el.innerHTML = val && list.models.length && !ids[val] ? '<small class="mdl-unlisted">' + ic("info", "sm") + esc(T("pv.models_unlisted", { n: list.models.length })) + "</small>" : "";
      });
    };
    var load = function (refresh, quiet) {
      if (busy) return busy;
      if (!v.key_set) { if (!quiet) toast(T("pv.models_nokey"), true); return Promise.resolve(null); }
      btn.disabled = true; btn.classList.add("busy"); label(T("pv.fetching"), "refresh");
      busy = api("/api/providers/" + enc(v.name) + "/models", { method: "POST", body: { refresh: !!refresh } }).then(function (r) {
        if (!r.ok) {
          list = null;
          var why = r.unsupported ? T("pv.models_unsupported") : T("pv.models_failed", { e: (r.status ? r.status + " " : "") + (r.error || "") });
          label(T("pv.fetch_models")); btn.setAttribute("data-tip", why);
          if (!quiet || !r.unsupported) toast(why, true);
          return null;
        }
        list = r;
        label(T("pv.models_n", { n: r.models.length }), "refresh");
        btn.setAttribute("data-tip", T("pv.models_tip", { h: r.host + r.path, t: when(r.fetched_at) }));
        if (!r.models.length && !quiet) toast(T("pv.models_empty"), true);
        noteUnlisted();
        if (popFor && document.activeElement === popFor) show(popFor);
        return r;
      }).catch(function (e) { label(T("pv.fetch_models")); if (!quiet) fail(e); return null; }).then(function (r) { btn.disabled = false; btn.classList.remove("busy"); busy = null; return r; });
      return busy;
    };
    var hide = function () { if (pop) pop.remove(); pop = null; sel = -1; if (popFor) popFor.setAttribute("aria-expanded", "false"); };
    var mark = function (id, q) {
      var i = q ? id.toLowerCase().indexOf(q) : -1;
      return i < 0 ? esc(id) : esc(id.slice(0, i)) + "<b>" + esc(id.slice(i, i + q.length)) + "</b>" + esc(id.slice(i + q.length));
    };
    var show = function (input) {
      if (!list || !list.models.length) return;
      var q = input.value.trim().toLowerCase(), cur = input.value.trim();
      // The field's own value shouldn't hide the rest: filter only once it's been edited.
      var filt = input._typed && q ? q : "";
      items = list.models.filter(function (m) { return !filt || (m.id + " " + (m.name || "")).toLowerCase().indexOf(filt) >= 0; });
      if (!pop) {
        pop = document.createElement("ul");
        pop.className = "mdl-pop"; pop.id = "mdl-pop"; pop.setAttribute("role", "listbox"); pop.setAttribute("aria-label", T("pv.models"));
        input.parentNode.appendChild(pop);
        pop.addEventListener("mousedown", function (e) { e.preventDefault(); }); // keep focus in the field
        pop.addEventListener("click", function (e) { var li = e.target.closest("li[data-i]"); if (li) pick(+li.getAttribute("data-i")); });
        pop.addEventListener("mousemove", function (e) { var li = e.target.closest("li[data-i]"); if (li && +li.getAttribute("data-i") !== sel) move(+li.getAttribute("data-i")); });
      }
      popFor = input; input.setAttribute("aria-expanded", "true"); input.setAttribute("aria-controls", "mdl-pop");
      sel = Math.max(0, items.map(function (m) { return m.id; }).indexOf(cur));
      pop.innerHTML = items.length ? items.map(function (m, i) {
        return '<li role="option" id="mdl-o' + i + '" data-i="' + i + '" aria-selected="' + (i === sel) + '" class="' + (i === sel ? "sel" : "") + (m.id === cur ? " cur" : "") + '"' + (m.owner ? ' title="' + esc(m.owner) + '"' : "") + '><span class="ck">' + (m.id === cur ? ic("check", "sm") : "") + '</span><code class="id">' + mark(m.id, filt) + "</code>" + (m.name ? '<small class="nm">' + esc(m.name) + "</small>" : "") + "</li>";
      }).join("") : '<li class="none">' + esc(T("pv.models_none")) + "</li>";
      // Open upwards when the drawer has no room below (the Haiku row near the footer).
      var body = input.closest(".drawer-body"), r = input.getBoundingClientRect(), b = body ? body.getBoundingClientRect() : { top: 0, bottom: window.innerHeight };
      pop.classList.toggle("up", b.bottom - r.bottom < 216 && r.top - b.top > b.bottom - r.bottom);
      move(sel);
    };
    var move = function (i) {
      if (!pop || !items.length) return;
      var was = $("#mdl-o" + sel, pop); if (was) { was.classList.remove("sel"); was.setAttribute("aria-selected", "false"); }
      sel = (i + items.length) % items.length;
      var li = $("#mdl-o" + sel, pop); if (li) { li.classList.add("sel"); li.setAttribute("aria-selected", "true"); if (li.scrollIntoView) li.scrollIntoView({ block: "nearest" }); }
      if (popFor) popFor.setAttribute("aria-activedescendant", "mdl-o" + sel);
    };
    var pick = function (i) {
      var m = items[i], input = popFor;
      if (!m || !input) return;
      input.value = m.id; input._typed = false;
      hide(); picking = true;
      input.dispatchEvent(new Event("input", { bubbles: true })); // the unsaved-edit guard sees it
      picking = false; noteUnlisted();
    };
    btn.addEventListener("click", function () { load(true, false).then(function (r) { if (r && r.models.length) { var first = f[models[0][0]]; first.focus(); show(first); } }); });
    models.forEach(function (m) {
      var input = f[m[0]];
      input.setAttribute("role", "combobox"); input.setAttribute("aria-autocomplete", "list"); input.setAttribute("aria-expanded", "false"); input.setAttribute("autocomplete", "off");
      input.addEventListener("focus", function () { popFor = input; if (list) show(input); else if (v.key_set && !btn.hasAttribute("data-tip")) load(false, true); });
      input.addEventListener("click", function () { if (list && !pop) show(input); });
      input.addEventListener("blur", function () { setTimeout(function () { if (document.activeElement !== input && popFor === input) hide(); noteUnlisted(); }, 0); });
      input.addEventListener("input", function (e) { if (e.isTrusted) input._typed = true; if (list && !picking && document.activeElement === input) show(input); });
      input.addEventListener("keydown", function (e) {
        if (!list || !list.models.length) return;
        if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); if (!pop) show(input); else move(sel + (e.key === "ArrowDown" ? 1 : -1)); }
        else if (e.key === "PageDown" || e.key === "PageUp") { if (pop) { e.preventDefault(); move(Math.max(0, Math.min(items.length - 1, sel + (e.key === "PageDown" ? 8 : -8)))); } }
        else if (e.key === "Enter" && pop && items.length) { e.preventDefault(); pick(sel); }
        else if (e.key === "Escape" && pop) { e.preventDefault(); e.stopPropagation(); hide(); }
        else if (e.key === "Tab") hide();
      });
    });
  }

  // ---------- provider pricing (drawer section) ----------
  // Per-model prices per 1M tokens + billing mode. Official prices ship with the
  // presets (source URL + date); editing makes them "custom"; restore drops edits.
  function pricingEditor(box, name) {
    var nf = function (v) { return v == null ? "" : String(v); };
    var paint = function (d) {
      var P = d.pricing, b = P.billing || { mode: "metered" }, q = b.quota || {};
      var prices = (P.prices || []).map(function (x) { return JSON.parse(JSON.stringify(x)); });
      var mode = b.mode || "metered", sub = mode === "subscription";
      // Drawer shows numbers + one pill; where the numbers come from lives in the ⓘ popover.
      var pill = mode !== "metered" ? "" :
        P.origin === "official" ? '<span class="pill ok sm">' + esc(T("pr.o_official")) + "</span>" :
        P.origin === "custom" ? '<span class="pill info sm">' + esc(T("pr.o_custom")) + "</span>" : '<span class="pill warn sm">' + esc(T("pr.o_none")) + "</span>";
      var src = {}; (P.official || []).forEach(function (x) { if (x.source) src[x.source] = x.fetched; });
      var fb = d.fallback;
      var info = '<details class="pop-info"><summary class="info-tip" aria-label="' + esc(T("pr.info")) + '">' + ic("info", "sm") + '</summary><div class="pop-body">' +
        (Object.keys(src).length ? "<strong>" + esc(T("pr.src_h")) + "</strong>" + Object.keys(src).map(function (u) { return '<a class="link" href="' + esc(u) + '" target="_blank" rel="noopener noreferrer">' + esc(u.replace(/^https?:\/\//, "").split("/")[0]) + '</a><small class="muted">' + esc(T("pr.src", { d: src[u] })) + "</small>"; }).join("") : "") +
        (fb ? "<p>" + esc(T("pr.fallback", { m: d.model, i: fb.input, o: fb.output, c: fb.currency })) + "</p>" : "") +
        "<p>" + esc(T(sub ? "pr.sub_hint" : mode === "free" ? "pr.free_hint" : P.anthropic ? "pr.anth_hint" : "pr.metered_hint")) + "</p>" +
        "<p>" + esc(T("pr.unit_hint")) + '</p><a class="link" href="#/settings/pricing">' + esc(T("pr.catalog_link")) + " →</a></div></details>";
      var row = function (x, i) {
        var inp = function (k, ph) { return '<input type="number" min="0" step="any" inputmode="decimal" data-pi="' + i + '" data-pk="' + k + '" value="' + esc(nf(x[k])) + '" placeholder="' + esc(ph || "—") + '" aria-label="' + esc((x.model || "") + " " + T("pr.c_" + k)) + '">'; };
        var meta = [];
        if (x.tiers && x.tiers.length) meta.push(x.tiers.map(function (t) { return (t.up_to ? "< " + num(t.up_to) : "≥") + ": " + t.input + " / " + t.output; }).join(" · "));
        if (x.note) meta.push(x.note);
        return '<div class="pr-row"><input class="pr-model" data-pi="' + i + '" data-pk="model" value="' + esc(x.model || "") + '" maxlength="128" spellcheck="false" placeholder="model-id" aria-label="' + esc(T("f.model")) + '">' +
          inp("input") + inp("output") + inp("cache_write", "=" + T("pr.c_input")) + inp("cache_read", "—") +
          '<span class="pr-x">' + (meta.length ? '<span class="tag sm" tabindex="0" data-tip="' + esc(meta.join("\n")) + '">' + esc(x.tiers && x.tiers.length ? T("pr.tiered", { n: x.tiers.length }) : "i") + "</span>" : "") +
          '<button type="button" class="icon-btn sm" data-pdel="' + i + '" aria-label="' + esc(T("btn.remove")) + '">' + ic("x", "sm") + "</button></span></div>";
      };
      var cur0 = (prices[0] || {}).currency || "USD";
      box.innerHTML = '<div class="pr-head"><div class="seg sm" role="radiogroup" aria-label="' + esc(T("pr.mode")) + '">' + ["metered", "subscription", "free"].map(function (m) { return '<button type="button" role="radio" data-pm="' + m + '" aria-checked="' + (m === mode) + '" class="' + (m === mode ? "active" : "") + '">' + esc(T("pr.m_" + m)) + "</button>"; }).join("") + '</div><span class="grow"></span>' + pill + info + "</div>" +
        (sub ? '<div class="pr-sub"><label class="field"><span>' + esc(T("pr.q_window")) + '</span><select id="pr-qw">' + opt("", T("pr.q_none"), !q.window) + ["5h", "day", "week", "month"].map(function (w) { return opt(w, T("pr.w_" + w), q.window === w); }).join("") + "</select></label>" +
          '<label class="field"><span>' + esc(T("pr.q_tokens")) + '</span><input id="pr-qt" type="number" min="0" step="1000" value="' + esc(q.max_tokens || "") + '"></label>' +
          '<label class="field"><span>' + esc(T("pr.q_reqs")) + '</span><input id="pr-qr" type="number" min="0" step="1" value="' + esc(q.max_requests || "") + '"></label>' +
          '<label class="field"><span>' + esc(T("pr.fee")) + tip(T("pr.fee_hint")) + '</span><div class="in-group"><input id="pr-fee" type="number" min="0" step="any" value="' + esc(b.monthly_fee || "") + '"><select id="pr-feec">' + opt("CNY", "¥", b.fee_currency !== "USD") + opt("USD", "$", b.fee_currency === "USD") + "</select></div></label></div>" : "") +
        (mode === "metered" ? '<div class="pr-table"><div class="pr-row pr-th"><select id="pr-cur" aria-label="' + esc(T("pr.c_currency")) + '">' + opt("USD", "USD $", cur0 !== "CNY") + opt("CNY", "CNY ¥", cur0 === "CNY") + "</select><span>" + esc(T("pr.c_input")) + "</span><span>" + esc(T("pr.c_output")) + "</span><span>" + esc(T("pr.c_cache_write")) + "</span><span>" + esc(T("pr.c_cache_read")) + "</span><span></span></div>" +
          (prices.length ? prices.map(row).join("") : '<p class="muted pr-empty">' + esc(T("pr.unset")) + "</p>") + "</div>" : "") +
        '<div class="row-actions">' + (mode === "metered" ? '<button type="button" class="btn ghost sm" id="pr-add">' + ic("plus", "sm") + esc(T("pr.add")) + "</button>" : "") + ((P.official || []).length && P.origin === "custom" ? '<button type="button" class="btn ghost sm" id="pr-restore">' + ic("rollback", "sm") + esc(T("pr.restore")) + "</button>" : "") + '<span class="grow"></span><button type="button" class="btn sm" id="pr-save">' + esc(T("pr.save")) + "</button></div>";
      var collect = function () {
        $$("[data-pi]", box).forEach(function (el) {
          var x = prices[+el.getAttribute("data-pi")], k = el.getAttribute("data-pk"), v = el.value.trim();
          if (k === "model") { x[k] = v; return; }
          var old = x[k] == null ? null : x[k], nv = v === "" ? null : parseFloat(v);
          x[k] = nv;
          if (x.tiers && old !== nv) delete x.tiers; // edited base price: the bands no longer apply
        });
        var c = $("#pr-cur", box);
        if (c) prices.forEach(function (x) { x.currency = c.value; });
        return prices.filter(function (x) { return x.model; });
      };
      var billing = function () {
        var out = { mode: mode };
        if (mode === "subscription") {
          var w = $("#pr-qw", box) ? $("#pr-qw", box).value : (q.window || "");
          if (w) out.quota = { window: w, max_tokens: parseInt(($("#pr-qt", box) || {}).value, 10) || 0, max_requests: parseInt(($("#pr-qr", box) || {}).value, 10) || 0 };
          out.monthly_fee = parseFloat(($("#pr-fee", box) || {}).value) || 0; out.fee_currency = ($("#pr-feec", box) || {}).value || "CNY";
        } else if (b.quota || b.monthly_fee) { out.quota = b.quota; out.monthly_fee = b.monthly_fee; out.fee_currency = b.fee_currency; } // keep plan details when switching back and forth
        return out;
      };
      var repaint = function (list) { P.prices = list; P.billing = billing(); paint(d); };
      $$("[data-pm]", box).forEach(function (bt) { bt.addEventListener("click", function () { var list = $("#pr-cur", box) ? collect() : prices; P.billing = billing(); P.billing.mode = bt.getAttribute("data-pm"); if (P.billing.mode === "subscription") { P.billing.quota = P.billing.quota || b.quota; } P.prices = list; paint(d); var nb = $('[data-pm="' + P.billing.mode + '"]', box); if (nb) nb.focus(); }); });
      var add = $("#pr-add", box); if (add) add.addEventListener("click", function () {
        var cur = collect(); cur.push({ model: "", currency: (cur[0] || {}).currency || cur0, input: null, output: null }); repaint(cur);
        var ins = $$('[data-pk="model"]', box); if (ins.length) ins[ins.length - 1].focus();
      });
      $$("[data-pdel]", box).forEach(function (bt) { bt.addEventListener("click", function () { var cur = collect(); cur.splice(+bt.getAttribute("data-pdel"), 1); repaint(cur); }); });
      $("#pr-save", box).addEventListener("click", function () {
        var list = $("#pr-cur", box) ? collect() : prices, bad = list.filter(function (x) { return x.input == null || x.output == null; })[0];
        if (bad && mode === "metered") { toast(T("pr.need_io", { m: bad.model }), true); return; }
        rpc("pricing.set", { provider: name, billing: billing(), prices: list }).then(function (r) { toast(T("pr.saved")); paint(r); }).catch(fail);
      });
      var rs = $("#pr-restore", box); if (rs) rs.addEventListener("click", function () { var prev = { billing: billing(), prices: collect() }; rpc("pricing.set", { provider: name, billing: billing(), restore: true }).then(function (r) { paint(r); toastUndo(T("pr.restored"), function () { rpc("pricing.set", { provider: name, billing: prev.billing, prices: prev.prices }).then(paint).catch(fail); }); }).catch(fail); });
    };
    return rpc("pricing.get", { provider: name }).then(paint).catch(function (e) { box.innerHTML = '<p class="err">' + esc(e.message) + "</p>"; });
  }

