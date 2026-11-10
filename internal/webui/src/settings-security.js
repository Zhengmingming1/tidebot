  // ---- notification channels (Settings → Notifications) ----
  // Add / edit / test / switch off / remove a channel without agentctl. Ideas from Uptime Kuma
  // src/components/NotificationDialog.vue: the default name follows the type until you edit
  // it ("钉钉 2" when "钉钉" is taken), and "Test" sends what is in the form before saving.
  // Robot URLs carry their access token, so the page only ever gets the host back.
  var NT_KINDS = ["feishu", "dingtalk", "wecom", "telegram", "email", "webhook"];
  var NT_BRAND = { feishu: "br-feishu", dingtalk: "br-ding", wecom: "br-wecom", telegram: "br-tg", email: "br-mail", webhook: "br-other" };
  var NT_ICON = { feishu: "chat", dingtalk: "chat", wecom: "chat", telegram: "send", email: "mail", webhook: "globe" };
  var NT_GROUPS = ["failed", "confirm", "budget", "breaker", "finished", "system"];
  // Chat robots get a one-letter mark (they would all be the same blue bubble otherwise).
  var NT_MARK = { feishu: 1, dingtalk: 1, wecom: 1 };
  function ntLogo(kind) { return '<span class="ap-logo sm ' + (NT_BRAND[kind] || "br-other") + '" aria-hidden="true">' + (NT_MARK[kind] ? '<b class="nt-mark">' + esc(T("nt.m." + kind)) + "</b>" : ic(NT_ICON[kind] || "bell")) + "</span>"; }
  // ntKindFromURL: a pasted robot URL tells which kind it is.
  function ntKindFromURL(u) {
    var h = "";
    try { h = new URL(String(u || "").trim()).hostname.toLowerCase(); } catch (e) { return ""; }
    if (/(^|\.)(feishu\.cn|larksuite\.com|larkoffice\.com)$/.test(h)) return "feishu";
    if (/(^|\.)dingtalk\.com$/.test(h)) return "dingtalk";
    if (/^qyapi\.weixin\.qq\.com$/.test(h)) return "wecom";
    if (/^api\.telegram\.org$/.test(h)) return "telegram";
    return "";
  }
  function ntUniqueName(kind, list, selfId) {
    var base = T("nt.k." + kind), name = base, n = 2;
    var taken = function (x) { return (list || []).some(function (c) { return c.name === x && c.id !== selfId; }); };
    while (taken(name)) name = base + " " + n++;
    return name;
  }
  function ntErr(e) {
    var m = /^notify: (\w+)$/.exec((e && e.message) || ""), k = m ? "nt.err." + m[1] : "";
    return k && T(k) !== k ? T(k) : (e && e.message) || "";
  }
  function ntGroupsText(c) {
    if (c.all) return T("nt.g_all");
    return c.groups.length ? c.groups.map(function (g) { return T("nt.gs." + g); }).join(" · ") : T("nt.g_none");
  }
  function ntTip(c) {
    var parts = [T("nt.k." + c.kind)];
    var cfg = c.config || {};
    if (c.host) parts.push(c.host);
    if (cfg.chat_id) parts.push("chat_id " + cfg.chat_id);
    if (cfg.to) parts.push(T("nt.f_to") + " " + cfg.to);
    if (cfg.host) parts.push(cfg.host + (cfg.port ? ":" + cfg.port : ""));
    if (c.has_secret) parts.push(T(c.kind === "dingtalk" || c.kind === "feishu" ? "nt.tip_signed" : "nt.tip_secret"));
    if (c.tasks) parts.push(T("nt.tip_tasks", { n: c.tasks }));
    return parts.join(" · ");
  }
  // ntLastHtml: how the last send to this channel went (healthchecks' "Delivered / Failed,
  // 2 hours ago" column). The row says only the state and when; the error, the time it took
  // and the last success sit in the tooltip.
  function ntLastHtml(c) {
    var d = c.last;
    if (!d || !d.at) return '<span class="nt-last none" tabindex="0" data-tip="' + esc(T("nt.last_never_tip")) + '"><i class="nt-dot" aria-hidden="true"></i>' + esc(T("nt.last_never")) + "</span>";
    var tip = [T(d.ok ? "nt.last_ok_tip" : "nt.last_bad_tip", { t: when(d.at) })];
    if (d.ms > 0) tip.push(T("nt.last_ms", { n: d.ms < 1000 ? d.ms + " ms" : (d.ms / 1000).toFixed(1) + " s" }));
    if (!d.ok) {
      if (d.error) tip.push(ntErr({ message: d.error }));
      tip.push(d.ok_at ? T("nt.last_ok_at", { t: when(d.ok_at) }) : T("nt.last_ok_never"));
    }
    var label = esc(T(d.ok ? "nt.last_ok" : d.streak > 1 ? "nt.last_bad_n" : "nt.last_bad", { n: d.streak })) + " · ";
    return '<span class="nt-last ' + (d.ok ? "ok" : "bad") + '" tabindex="0" data-tip="' + esc(tip.join("\n")) + '"><i class="nt-dot" aria-hidden="true"></i>' + label +
      '<span data-ago="' + esc(d.at) + '">' + esc(ago(d.at)) + "</span></span>";
  }
  // ---------- signed-in devices (Settings → Security) ----------
  // One row per browser / desktop window that is signed in, this window first: browser and OS
  // read from the User-Agent at sign-in, last activity as relative time; sign-in time, address
  // and expiry sit in the tooltip. Every other row can be signed out on its own (Immich's
  // device list does the same), "sign out others" stays for all at once.
  function sesLabel(x) {
    var who = x.kind === "desktop" ? T("ses.desktop") : (x.client || T("ses.browser"));
    return x.os ? who + " · " + x.os : who;
  }
  function sesIcon(x) {
    if (x.kind === "desktop") return "monitor";
    return /^(iOS|iPadOS|Android)$/.test(x.os || "") ? "phone" : "globe";
  }
  function sesIP(ip) {
    if (!ip) return "";
    return /^(127\.|::1$|localhost$)/.test(ip) ? T("ses.local") : ip;
  }
  function sesTip(x) {
    var parts = [T("ses.since", { t: when(x.created_at) })], ip = sesIP(x.ip);
    if (ip) parts.push(T("ses.from", { ip: ip }));
    parts.push(T("ses.until", { t: when(x.expires_at) }));
    return parts.join("\n");
  }
  function sesOthersHtml(n) {
    return n > 0 ? '<button class="btn sm ghost" id="ses-others" data-tip="' + esc(T("ses.others_tip")) + '">' + esc(T("ses.others", { n: n })) + "</button>" : "";
  }
  function sesListHtml(list) {
    list = list || [];
    if (!list.length) return "";
    return '<ul class="nt-list ses-list" aria-label="' + esc(T("ses.list")) + '">' + list.map(function (x) {
      var label = sesLabel(x);
      return '<li class="nt-ch ses-row' + (x.current ? " cur" : "") + '" data-ses="' + esc(x.id) + '"><span class="ses-logo" aria-hidden="true">' + ic(sesIcon(x), "sm") + "</span>" +
        '<div class="grow nt-txt"><strong tabindex="0" data-tip="' + esc(sesTip(x)) + '">' + esc(label) + "</strong><small>" +
        (x.current ? esc(T("ses.this_window")) : tAgo("ses.seen", {}, "t", x.last_seen_at)) + "</small></div>" +
        (x.current ? '<span class="pill ok">' + esc(T("ses.current")) + "</span>" :
          '<button class="icon-btn sm" data-ses-out="' + esc(x.id) + '" data-kind="' + esc(x.kind) + '" data-name="' + esc(label) + '" aria-label="' + esc(T("ses.out", { n: label })) + '" data-tip="' + esc(T("ses.out_tip")) + '">' + ic("logout", "sm") + "</button>") + "</li>";
    }).join("") + "</ul>";
  }
  // sesWire: the row's "sign out others" and the per-device buttons; both repaint the list
  // in place (no page re-render), keeping focus on the list.
  function sesWire(row, box) {
    var reload = function () {
      return api("/api/auth/sessions", { fresh: true }).then(function (r) {
        var list = r.sessions || [], others = list.filter(function (x) { return !x.current; }).length;
        box.innerHTML = sesListHtml(list);
        var old = $("#ses-others", row), gap = $(".row-gap", row);
        if (old) old.remove();
        if (others && gap) gap.insertAdjacentHTML("afterbegin", sesOthersHtml(others));
      }).catch(fail);
    };
    row.addEventListener("click", function (e) {
      var b = e.target.closest("#ses-others"); if (!b) return;
      b.disabled = true;
      api("/api/auth/sessions/others", { method: "POST", body: {} }).then(function (r) { toast(T("ses.others_done", { n: r.signed_out })); return reload(); }).catch(function (err) { b.disabled = false; fail(err); });
    });
    box.addEventListener("click", function (e) {
      var b = e.target.closest("[data-ses-out]"); if (!b || !box.contains(b)) return;
      var id = b.getAttribute("data-ses-out"), name = b.getAttribute("data-name");
      confirmDialog({ title: T("ses.out_title", { n: name }), body: T(b.getAttribute("data-kind") === "desktop" ? "ses.out_body_desktop" : "ses.out_body"), ok: T("ses.out_ok"), danger: true, icon: "logout" }).then(function (yes) {
        if (!yes) return;
        b.disabled = true;
        api("/api/auth/sessions/signout", { method: "POST", body: { id: id } }).then(function () { toast(T("ses.out_done", { n: name })); }).catch(function (err) {
          if (err && err.status === 404) toast(T("ses.out_gone")); else fail(err);
        }).then(function () {
          return reload().then(function () { var nb = $("[data-ses-out]", box) || $(".ses-row strong", box); if (nb) nb.focus(); });
        });
      });
    });
  }
  function ntListHtml(o) {
    var chs = o.channels || [];
    if (!chs.length) return '<div class="nt-empty">' + ic("bell", "sm") + "<span>" + esc(T("set.no_channels")) + "</span></div>";
    return '<ul class="nt-list">' + chs.map(function (c) {
      return '<li class="nt-ch' + (c.enabled ? "" : " off") + '" data-ch="' + esc(c.id) + '">' + ntLogo(c.kind) +
        '<div class="grow nt-txt"><strong data-tip="' + esc(ntTip(c)) + '">' + esc(c.name) + '</strong><small>' + esc(ntGroupsText(c)) + "</small></div>" + ntLastHtml(c) +
        '<button class="icon-btn sm" data-nt-test="' + esc(c.id) + '" aria-label="' + esc(T("nt.test")) + '" data-tip="' + esc(T("nt.test")) + '"' + (c.enabled ? "" : " disabled") + ">" + ic("send", "sm") + "</button>" +
        '<button class="icon-btn sm" data-nt-edit="' + esc(c.id) + '" aria-label="' + esc(T("btn.edit")) + '" data-tip="' + esc(T("btn.edit")) + '">' + ic("edit", "sm") + "</button>" +
        '<button class="icon-btn sm" data-nt-del="' + esc(c.id) + '" aria-label="' + esc(T("btn.remove")) + '" data-tip="' + esc(T("btn.remove")) + '">' + ic("trash", "sm") + "</button>" +
        '<button class="switch' + (c.enabled ? " on" : "") + '" data-nt-en="' + esc(c.id) + '" role="switch" aria-checked="' + !!c.enabled + '" aria-label="' + esc(T("nt.enabled", { n: c.name })) + '"></button></li>';
    }).join("") + "</ul>";
  }
  // ntWire: list actions. reload() repaints the card in place (no page re-render, no flash).
  function ntWire(box, o, reload) {
    var byId = function (id) { return (o.channels || []).filter(function (c) { return c.id === id; })[0]; };
    box.addEventListener("click", function (e) {
      var b = e.target.closest("button"); if (!b || !box.contains(b)) return;
      var id;
      if ((id = b.getAttribute("data-nt-en"))) {
        var on = !b.classList.contains("on"); b.disabled = true;
        rpc("notify.channel_enable", { id: id, enabled: on }).then(function () { toast(T(on ? "nt.on_done" : "nt.off_done", { n: byId(id).name })); return reload(); }).catch(function (x) { b.disabled = false; toast(ntErr(x), true); });
      } else if ((id = b.getAttribute("data-nt-test"))) {
        var c = byId(id); b.disabled = true;
        rpc("notify.channel_test", { id: id, kind: c.kind, name: c.name, config: c.config }, 30000).then(function () { toast(T("nt.test_ok", { n: c.name })); }).catch(function (x) { toast(T("nt.test_bad", { e: ntErr(x) }), true); }).then(function () {
          var had = document.activeElement === b; b.disabled = false;
          return Promise.resolve(reload()).then(function () { var nb = had && $('[data-nt-test="' + id + '"]', box); if (nb) nb.focus(); });
        });
      } else if ((id = b.getAttribute("data-nt-edit"))) {
        ntModal(o, byId(id), reload);
      } else if ((id = b.getAttribute("data-nt-del"))) {
        var d = byId(id);
        confirmDialog({ title: T("nt.del_title", { n: d.name }), body: T(d.tasks ? "nt.del_body_tasks" : "nt.del_body", { n: d.tasks }), ok: T("btn.remove"), danger: true }).then(function (yes) {
          if (!yes) return;
          rpc("notify.channel_remove", { id: id }).then(function () { toast(T("nt.del_done", { n: d.name })); return reload(); }).catch(fail);
        });
      }
    });
  }
  // ntModal(o, ch, done): ch null = add. URL / token / password are write-only: when editing,
  // a blank field keeps what is saved (the placeholder says so).
  function ntModal(o, ch, done) {
    var edit = !!ch, kind0 = edit ? ch.kind : "feishu", cfg = (ch && ch.config) || {};
    var groups = edit ? (ch.all ? NT_GROUPS.slice() : ch.groups) : (o.defaults || ["failed", "confirm", "budget", "breaker", "system"]);
    var kinds = '<div class="nt-kinds' + (edit ? " hidden" : "") + '" role="radiogroup" aria-label="' + esc(T("nt.kind")) + '">' + NT_KINDS.map(function (k) {
      return '<label class="nt-kind"><input type="radio" name="kind" value="' + k + '"' + (k === kind0 ? " checked" : "") + (edit ? " disabled" : "") + ">" + ntLogo(k) + "<span>" + esc(T("nt.k." + k)) + "</span></label>";
    }).join("") + "</div>";
    var keep = function (has) { return edit && has ? ' placeholder="' + esc(T("nt.keep")) + '"' : ""; };
    var inp = function (name, attrs) { return '<input name="' + name + '" autocomplete="off" spellcheck="false" ' + (attrs || "") + ">"; };
    var f = function (k, label, inner, hint) { return '<div data-k="' + k + '">' + fieldHtml(label, inner, hint) + "</div>"; };
    var html = '<div class="card-head"><span class="ico">' + ic("bell") + "</span><h2>" + esc(T(edit ? "nt.edit_title" : "nt.add_title")) + "</h2></div>" +
      '<form id="nt-form" novalidate>' + (edit ? '<div class="nt-kinds one">' + ntLogo(kind0) + "<span>" + esc(T("nt.k." + kind0)) + "</span></div>" + kinds : kinds) +
      f("url", T("nt.f_url"), inp("url", 'type="url" maxlength="2048"' + (keep(ch && ch.has_url) || ' placeholder="https://…"'))) +
      '<p class="hint nt-hint" id="nt-hint"></p>' +
      f("sign", T("nt.f_sign"), inp("secret_sign", 'type="password" maxlength="200"' + keep(ch && ch.has_secret)), T("nt.f_sign_tip")) +
      f("bearer", T("nt.f_bearer"), inp("secret_bearer", 'type="password" maxlength="2000"' + keep(ch && ch.has_secret)), T("nt.f_bearer_tip")) +
      '<div class="form-row" data-k="telegram">' + fieldHtml(T("nt.f_bot"), inp("secret_bot", 'type="password" maxlength="200"' + (keep(ch && ch.has_secret) || ' placeholder="123456:ABC…"'))) + fieldHtml(T("nt.f_chat"), inp("chat_id", 'maxlength="64" placeholder="-1001234567890" value="' + esc(cfg.chat_id || "") + '"'), T("nt.f_chat_tip")) + "</div>" +
      '<div class="form-row" data-k="email">' + fieldHtml(T("nt.f_from"), inp("from", 'type="email" maxlength="200" placeholder="you@example.com" value="' + esc(cfg.from || "") + '"')) + fieldHtml(T("nt.f_to"), inp("to", 'maxlength="500" placeholder="you@example.com" value="' + esc(cfg.to || "") + '"'), T("nt.f_to_tip")) + "</div>" +
      '<div class="form-row three" data-k="email">' + fieldHtml(T("nt.f_host"), inp("host", 'maxlength="200" placeholder="smtp.example.com" value="' + esc(cfg.host || "") + '"')) + fieldHtml(T("nt.f_port"), inp("port", 'inputmode="numeric" maxlength="5" placeholder="465" value="' + esc(cfg.port || "") + '"'), T("nt.f_port_tip")) + fieldHtml(T("nt.f_user"), inp("username", 'maxlength="200" value="' + esc(cfg.username || "") + '"')) + "</div>" +
      f("email", T("nt.f_pass"), inp("secret_pass", 'type="password" maxlength="500"' + keep(ch && ch.has_secret)), T("nt.f_pass_tip")) +
      '<p class="hint nt-hint" id="nt-mail-note"></p>' +
      fieldHtml(T("nt.f_name"), inp("name", 'maxlength="60" required value="' + esc(edit ? ch.name : ntUniqueName(kind0, o.channels)) + '"')) +
      '<fieldset class="nt-groups"><legend>' + esc(T("nt.f_groups")) + "</legend>" + NT_GROUPS.map(function (g) {
        return '<label class="check"><input type="checkbox" name="g" value="' + g + '"' + (groups.indexOf(g) >= 0 ? " checked" : "") + "> <span>" + esc(T("nt.g." + g)) + "</span>" + tip(T("nt.gt." + g)) + "</label>";
      }).join("") + "</fieldset>" +
      (edit && ch.all ? '<p class="hint">' + esc(T("nt.all_note")) + "</p>" : "") +
      '<p class="nt-status" id="nt-status" role="status" aria-live="polite"></p>' +
      '<div class="actions"><button type="button" class="btn" id="nt-test">' + ic("send", "sm") + esc(T("nt.test")) + '</button><span class="grow"></span><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary" type="submit">' + esc(T("btn.save")) + "</button></div></form>";
    modal(html, function (root, close) {
      var form = $("#nt-form", root), st = $("#nt-status", root), autoName = edit ? null : form.name.value;
      var kind = function () { var r = $("input[name=kind]:checked", form); return r ? r.value : kind0; };
      var show = function () {
        var k = kind(), isURL = k !== "telegram" && k !== "email";
        $$(".nt-kind", form).forEach(function (l) { l.classList.toggle("on", $("input", l).checked); });
        $$("[data-k]", form).forEach(function (el) {
          var dk = el.getAttribute("data-k");
          var on = dk === "url" ? isURL : dk === "sign" ? (k === "dingtalk" || k === "feishu") : dk === "bearer" ? k === "webhook" : dk === k;
          el.classList.toggle("hidden", !on);
        });
        $("#nt-hint", root).textContent = T("nt.h." + k);
        if (!edit && form.name.value === autoName) { form.name.value = autoName = ntUniqueName(k, o.channels); }
        st.textContent = ""; st.className = "nt-status";
      };
      $$("input[name=kind]", form).forEach(function (r) { r.addEventListener("change", show); });
      show();
      // Pasting a DingTalk / WeCom / Feishu robot URL picks the kind for you.
      form.url.addEventListener("input", function () {
        if (edit) return;
        var k = ntKindFromURL(form.url.value);
        if (k && k !== "telegram" && k !== kind()) { $('input[name=kind][value="' + k + '"]', form).checked = true; show(); st.textContent = T("nt.detected", { k: T("nt.k." + k) }); st.className = "nt-status ok"; }
      });
      // Email: the sender address fills server, port, user name and recipient (same lookup
      // as the email plugin); fields you typed into are left alone.
      var mine = {}, auto = {};
      ["host", "port", "username", "to"].forEach(function (n) { form[n].addEventListener("input", function (e) { if (e.isTrusted) mine[n] = true; }); });
      var put = function (n, v) { if (mine[n] || v == null || v === "") return; if (form[n].value && form[n].value !== auto[n]) return; form[n].value = auto[n] = String(v); };
      var lookT;
      form.from.addEventListener("input", function () {
        clearTimeout(lookT);
        var a = form.from.value.trim();
        put("to", a);
        if (!/@.+\./.test(a)) return;
        lookT = setTimeout(function () {
          apiRaw("/api/plugins/prefill", { method: "POST", body: { plugin: "email", field: "address", value: a } }).then(function (r) {
            var note = $("#nt-mail-note", root);
            if (!r.found) { note.textContent = T("pf.af_mail_unknown"); return; }
            put("host", r.values.smtp_host); put("port", r.values.smtp_port); put("username", r.values.smtp_username);
            var pn = T("pf.mp_" + r.provider) !== "pf.mp_" + r.provider ? T("pf.mp_" + r.provider) : r.provider;
            note.textContent = T("nt.mail_found", { p: pn, s: r.values.smtp_host + ":" + r.values.smtp_port }) + (r.note ? " " + T("pf.mn_" + r.note, { p: pn }) : "");
          }).catch(function () {});
        }, 400);
      });
      var payload = function () {
        var k = kind(), secret = k === "telegram" ? form.secret_bot.value : k === "email" ? form.secret_pass.value : k === "webhook" ? form.secret_bearer.value : k === "wecom" ? "" : form.secret_sign.value;
        var c = {};
        if (k === "telegram") c.chat_id = form.chat_id.value.trim();
        if (k === "email") ["from", "to", "host", "port", "username"].forEach(function (n) { c[n] = form[n].value.trim(); });
        return { id: edit ? ch.id : "", kind: k, name: form.name.value.trim(), url: k === "telegram" || k === "email" ? "" : form.url.value.trim(), secret: secret.trim(), config: c,
          groups: $$("input[name=g]:checked", form).map(function (x) { return x.value; }) };
      };
      var busy = false;
      $("#nt-test", root).addEventListener("click", function (e) {
        if (busy) return; busy = true;
        var b = e.currentTarget; b.disabled = true;
        st.className = "nt-status"; st.textContent = T("nt.testing");
        rpc("notify.channel_test", payload(), 30000).then(function () { st.className = "nt-status ok"; st.textContent = T("nt.test_ok_form"); })
          .catch(function (x) { st.className = "nt-status bad"; st.textContent = T("nt.test_bad", { e: ntErr(x) }); })
          .then(function () { busy = false; b.disabled = false; });
      });
      form.addEventListener("submit", function (e) {
        e.preventDefault();
        if (busy) return; busy = true;
        var p = payload();
        rpc("notify.channel_save", p).then(function () {
          close(); toast(T(edit ? "nt.saved" : "nt.added", { n: p.name })); return done && done();
        }).catch(function (x) { st.className = "nt-status bad"; st.textContent = ntErr(x); }).then(function () { busy = false; });
      });
    });
  }
  function renderSettings(section, item) {
    section = SET_SECTIONS.indexOf(section) >= 0 ? section : "general";
    return api("/api/settings").then(function (st) {
      // title/sub are trusted dictionary strings; "{cmd}" is replaced with a <code> chip after escaping.
      var row = function (key, title, sub, right, cmd) { return '<div class="set-row" id="set-' + key + '"><div class="lbl"><strong>' + esc(title) + "</strong><small>" + esc(sub).replace("{cmd}", cmd ? "<code>" + esc(cmd) + "</code>" : "") + "</small></div>" + right + "</div>"; };
      var nav = '<nav class="set-nav" aria-label="' + esc(T("nav.settings")) + '">' + SET_SECTIONS.map(function (k) {
        // the notifications entry carries the failing-channel dot (painted again by paintChannelDot)
        var dot = k === "notifications" && state.chFailing ? ' data-tip="' + esc(T("nt.failing_tip", { n: state.chFailing })) + '"' : "";
        return '<a href="#/settings/' + k + '"' + (k === section ? ' class="active" aria-current="page"' : "") + dot + ">" + ic(SET_ICONS[k], "sm") + "<span>" + esc(T("setnav." + k)) + "</span>" + (dot ? '<b class="nav-dot" aria-hidden="true"></b>' : "") + "</a>";
      }).join("") + "</nav>";
      var head = '<div class="card-head"><span class="ico">' + ic(SET_ICONS[section]) + "</span><h2>" + esc(T("setnav." + section)) + "</h2></div>";
      var body = "", after = function () {};
      if (section === "general") {
        body = lookCardHtml() + '<section class="card">' + head +
          row("language", T("set.language"), T("set.language_sub"), langSelect("set-lang")) +
          row("timezone", T("set.tz"), T("set.tz_sub"), tzButtonHtml(st.tz_offset)) +
          row("webui", T("set.addr"), T(st.allow_lan ? "set.lan_on" : "set.lan_off"), "<code>" + esc(st.addr) + "</code>") +
          row("https", "HTTPS", T("set.https_sub"), '<span class="pill ' + (st.tls ? "ok" : "plain") + '">' + esc(T(st.tls ? "set.enabled" : "set.off")) + "</span>") +
          row("idle", T("set.idle"), T("set.idle_sub"), "<span>" + esc(T("set.min", { n: st.idle_minutes })) + "</span>") + "</section>" +
          powerCardHtml() + quietCardHtml() +
          '<section class="card danger-zone" id="set-danger"><div class="card-head"><span class="ico">' + ic("bolt") + "</span><h2>" + esc(T("set.danger")) + "</h2></div>" +
          row("webui_off", T("set.webui_on"), T("set.webui_on_sub"), '<button class="btn danger sm" id="dz-webui">' + esc(T("set.turn_off")) + "</button>", "agentctl webui on") +
          row("reset_ui", T("set.reset_ui"), T("set.reset_ui_sub"), '<button class="btn danger sm" id="dz-reset">' + esc(T("set.reset")) + "</button>") + "</section>";
        after = function () {
          $("#set-lang").addEventListener("change", function (e) { setLanguage(e.target.value).then(function () { savedMark($("#set-lang")); }); });
          lookWire();
          powerWire();
          quietWire();
          $("#tz-open").addEventListener("click", function () { tzPicker(function () { route(); }); });
          $("#dz-webui").addEventListener("click", function () {
            confirmDialog({ title: T("set.off_title"), body: T("set.confirm_off"), ok: T("set.turn_off"), danger: true }).then(function (y) { if (y) api("/api/webui", { method: "POST", body: { enabled: false } }).then(function () { toast(T("toast.webui_off")); }).catch(fail); });
          });
          $("#dz-reset").addEventListener("click", function () {
            confirmDialog({ title: T("set.reset_ui"), body: T("set.reset_ui_confirm"), ok: T("set.reset"), danger: true }).then(function (y) {
              if (!y) return;
              try { Object.keys(localStorage).filter(function (k) { return /^tb_/.test(k); }).forEach(function (k) { localStorage.removeItem(k); }); } catch (e) {}
              draftsClearAll();
              state.notifSeen = ""; state.seenSent = ""; fav.failed = 0; paintFavicon(); toast(T("set.reset_done"));
              // the read mark is tidebot's, not this browser's: take it back from there
              api("/api/notifications/center", { fresh: true }).then(noteNotifs).catch(function () {});
            });
          });
        };
      } else if (section === "providers") {
        body = '<section class="card">' + head.replace("</h2></div>", "</h2>" + tip(T("pv.sub")) + '<button type="button" class="btn primary sm card-act" id="pv-add">' + ic("plus", "sm") + esc(T("pv.add")) + "</button></div>") +
          '<div id="pv-box" class="pad-x"><p class="muted">…</p></div></section>';
        after = function () {
          $("#pv-add").onclick = function (e) { e.currentTarget._want = true; };
          providerCards($("#pv-box")).catch(fail);
        };
      } else if (section === "budgets") {
        var g = (st.budgets || []).filter(function (x) { return x.scope === "global" && x.period === "day"; })[0] || {};
        var fx = st.fx || { display: "USD", usd_cny: 7.1 };
        body = '<section class="card">' + head.replace("</h2>", "</h2>" + tip(T("set.bud_auto"))) +
          '<div class="grid g3 pad-x" id="set-fx"><label class="field"><span>' + esc(T("set.fx_cur")) + tip(T("set.fx_cur_hint")) + '</span><select id="fx-cur">' + opt("USD", T("set.fx_usd"), fx.display === "USD") + opt("CNY", T("set.fx_cny"), fx.display === "CNY") + '</select></label>' +
          '<label class="field"><span>' + esc(T("set.fx_rate")) + tip(T("set.fx_rate_hint")) + '</span><div class="in-group"><span class="in-pre">1 USD =</span><input id="fx-rate" type="number" step="0.01" min="0.1" max="100" value="' + esc(fx.usd_cny) + '"><span class="in-pre">CNY</span></div></label></div>' +
          '<p class="muted pad-x">' + esc(T("set.bud_scope")) + '</p><div class="grid g3 pad-x" id="bud">' +
          '<label class="field" id="set-g_usd"><span>' + esc(T("set.g_usd") + " (" + curSym(fx.display) + ")") + '</span><input data-bud="g_usd" type="number" step="0.0001" min="0" max="1000000" value="' + esc(g.max_usd || "") + '"></label>' +
          '<label class="field"><span>' + esc(T("sp.on_breach")) + '</span><select data-bud="action">' + opt("pause_notify", T("sp.a_pause"), g.action !== "block_notify") + opt("block_notify", T("sp.a_block"), g.action === "block_notify") + '</select></label>' +
          '<label class="field"><span>' + esc(T("sp.warn_at")) + '</span><input data-bud="warn_at_pct" type="number" required min="1" max="100" step="1" value="' + esc(g.warn_at_pct || 80) + '"></label>' +
          '</div><p class="pad-x"><a href="#/spend">' + esc(T("set.bud_more")) + " " + ic("arrow", "sm") + "</a></p></section>";
        after = function () {
          var saveFx = function (el) {
            rpc("pricing.fx.set", { display: $("#fx-cur").value, usd_cny: parseFloat($("#fx-rate").value) || 0 }).then(function (f) { state.currency = f.display; savedMark(el); setTimeout(refresh, 400); }).catch(fail);
          };
          $("#fx-cur").addEventListener("change", function (e) { saveFx(e.target); });
          $("#fx-rate").addEventListener("change", function (e) { saveFx(e.target); });
          $$("[data-bud]").forEach(function (inp) {
            inp.addEventListener("change", function () {
              var k = inp.getAttribute("data-bud"), v = inp.value, req;
              if (!inp.checkValidity()) { inp.reportValidity(); return; }
              req = { scope: "global", period: "day" };
              if (k === "g_usd") req.max_usd = v === "" ? 0 : Number(v);
              else if (k === "action") req.action = v;
              else req.warn_at_pct = Number(v);
              api("/api/budgets", { method: "POST", body: req }).then(function () { savedMark(inp); }).catch(fail);
            });
          });
        };
      } else if (section === "tags") {
        body = '<section class="card" id="set-tags">' + head.replace("</h2>", "</h2>" + tip(T("tgm.sub"))) + '<div id="tgm-box"><p class="muted pad-x">…</p></div></section>';
        after = function () { tagManager($("#tgm-box")); };
      } else if (section === "security") {
        body = '<section class="card">' + head +
          row("password", T("login.password"), T("set.pw_sub"), '<span class="row-gap">' + (st.password_set === false ? '<span class="pill plain">' + esc(T("pw.unset")) + "</span>" : '<span class="pill ok">' + esc(T("set.set")) + "</span>") +
            '<button class="btn sm" id="pw-change">' + esc(T(st.password_set === false ? "pw.set" : "pw.change")) + "</button></span>", "agentctl auth set-password") +
          row("2fa", T("set.totp"), T(st.totp_enabled ? "set.totp_on_sub" : "set.totp_off_sub"), st.totp_enabled ?
            '<span class="row-gap"><span class="pill ok">' + esc(T("set.on")) + '</span><button class="btn sm" id="tf-off">' + esc(T("tf.disable")) + "</button></span>" :
            '<button class="btn primary sm" id="tf-on">' + esc(T("tf.enable")) + "</button>") +
          row("cookies", T("set.cookies"), T("set.cookies_sub"), "") +
          row("sandbox", T("set.sandbox"), T("set.sandbox_sub"), sandboxPill(st.sandbox)) +
          row("session", T("set.session"), T("set.session_sub"), '<span class="row-gap">' + sesOthersHtml(st.other_sessions) +
            '<button class="btn sm" id="btn-logout">' + ic("logout", "sm") + esc(T("set.signout")) + "</button></span>") +
          '<div id="ses-box">' + sesListHtml(st.sessions) + "</div></section>";
        after = function () {
          $("#btn-logout").addEventListener("click", function () {
            api("/api/logout", { method: "POST", body: {} }).catch(function () {}).then(function () { state.csrf = ""; draftsClearAll(); showLogin(); });
          });
          $("#pw-change").addEventListener("click", function () { pwChange(st.password_set !== false); });
          sesWire($("#set-session"), $("#ses-box"));
          if ($("#tf-on")) $("#tf-on").addEventListener("click", twoFASetup);
          if ($("#pl-sb")) $("#pl-sb").addEventListener("click", function () { sandboxHelp(st.sandbox); });
          if ($("#tf-off")) $("#tf-off").addEventListener("click", function () { twoFADisable(st.password_set !== false); });
        };
      } else if (section === "notifications") {
        body = '<section class="card">' + head + '<div id="nt-box"><p class="muted pad-x">…</p></div></section>';
        after = function () {
          rpc("notify.overview", {}).then(function (o) {
            noteChannels(o.channels);
            var hours = '<select id="nt-digest" aria-label="' + esc(T("set.digest")) + '"><option value="">' + esc(T("set.off")) + "</option>" + Array.from({ length: 24 }, function (_, h) { return '<option value="' + h + '"' + (String(h) === o.digest_hour ? " selected" : "") + ">" + (h < 10 ? "0" : "") + h + ":00</option>"; }).join("") + "</select>";
            $("#nt-box").innerHTML =
              row("quiet", T("set.quiet"), T("set.quiet_sub"), '<input id="nt-quiet" maxlength="11" placeholder="22:00-08:00" value="' + esc(o.quiet || "") + '">') +
              row("digest", T("set.digest"), T("set.digest_sub"), hours) +
              row("channels", T("set.channels"), T("set.channels_sub"), '<button class="btn sm" id="nt-add">' + ic("plus", "sm") + esc(T("nt.add")) + "</button>") +
              '<div id="nt-chs">' + ntListHtml(o) + "</div>";
            var chBox = $("#nt-chs");
            var reload = function () { return rpc("notify.overview", {}).then(function (o2) { o.channels = o2.channels; chBox.innerHTML = ntListHtml(o); noteChannels(o.channels); }).catch(fail); };
            ntWire(chBox, o, reload);
            $("#nt-add").addEventListener("click", function () { ntModal(o, null, reload); });
            $("#nt-quiet").addEventListener("change", function (e) {
              var v = e.target.value.trim();
              if (v && !/^\d{2}:\d{2}-\d{2}:\d{2}$/.test(v)) { toast(T("set.quiet_bad"), true); return; }
              rpc("notify.settings", { quiet: v }).then(function () { savedMark(e.target); }).catch(fail);
            });
            $("#nt-digest").addEventListener("change", function (e) {
              if (e.target.value === "") return;
              rpc("notify.settings", { digest_hour: +e.target.value }).then(function () { savedMark(e.target); }).catch(fail);
            });
            jumpTo(item);
          }).catch(fail);
        };
      } else if (section === "data") {
        body = storageCardHtml() + '<section class="card" id="set-backups"><div class="card-head"><span class="ico">' + ic("download") + "</span><h2>" + esc(T("set.backup")) + "</h2></div>" +
          row("backup", T("set.backup"), T("set.backup_sub"), '<button class="btn sm" id="bk-now">' + esc(T("set.backup_now")) + "</button>", "agentctl backup create --passphrase") +
          '<div id="bk-list" class="pad-x"><p class="muted">…</p></div>' +
          row("restore", T("set.restore"), T("set.restore_sub"), "", "agentctl backup restore <file>") + "</section>";
        after = function () {
          storageWire();
          var load = function () {
            return rpc("backups.list", {}).then(function (l) {
              $("#bk-list").innerHTML = l && l.length ? '<table class="tbl"><thead><tr><th>' + esc(T("set.bk_when")) + "</th><th>" + esc(T("set.bk_kind")) + '</th><th class="right">' + esc(T("set.bk_size")) + "</th></tr></thead><tbody>" + l.slice(0, 10).map(function (x) {
                return "<tr><td>" + esc(when(x.created_at)) + "</td><td>" + esc(T("set.bk_" + (x.kind === "auto" ? "auto" : "manual"))) + '</td><td class="right">' + esc(fmtBytes(x.size_bytes)) + "</td></tr>";
              }).join("") + "</tbody></table>" : '<p class="muted">' + esc(T("set.bk_none")) + "</p>";
            });
          };
          load().catch(fail);
          $("#bk-now").addEventListener("click", function (e) {
            e.target.disabled = true;
            rpc("backups.create", {}).then(function () { toast(T("set.bk_done")); return load(); }).catch(fail).then(function () { e.target.disabled = false; });
          });
        };
      } else if (section === "pricing") {
        body = '<section class="card" id="set-pcat">' + head +
          row("pcat-auto", T("pc.auto"), T("pc.auto_sub"), '<button class="switch" id="pc-auto" role="switch" aria-checked="false" aria-label="' + esc(T("pc.auto")) + '"></button>') +
          '<div class="pad-x" id="pc-body"><p class="muted">…</p></div></section>';
        after = function () {
          var paint = function (st) {
            var a = st.active, s0 = st.state || {}, on = !!(st.settings && st.settings.auto);
            var sw = $("#pc-auto"); sw.classList.toggle("on", on); sw.setAttribute("aria-checked", on);
            var res = s0.result ? '<span class="pill sm ' + (s0.result === "error" ? "bad" : s0.result === "installed" ? "ok" : "plain") + '">' + esc(T("pc.r_" + s0.result)) + "</span>" : '<span class="muted">' + esc(T("pc.never")) + "</span>";
            $("#pc-body").innerHTML = '<dl class="kv">' +
              "<dt>" + esc(T("pc.in_use")) + "</dt><dd>" + '<span class="pill sm ' + (st.origin === "remote" ? "info" : "plain") + '">' + esc(T("pc.o_" + st.origin)) + "</span> " + esc(when(a.generated)) + "</dd>" +
              "<dt>" + esc(T("pc.count")) + "</dt><dd>" + esc(T("pc.count_v", { p: a.presets, m: a.models })) + "</dd>" +
              "<dt>" + esc(T("pc.last")) + "</dt><dd>" + res + (s0.checked ? ' <span class="muted">' + esc(when(s0.checked)) + (s0.from ? " · " + esc(s0.from) : "") + "</span>" : "") + (s0.error ? '<small class="err">' + esc(s0.error) + "</small>" : "") + "</dd>" +
              "<dt>" + esc(T("pc.order")) + "</dt><dd>" + esc(T("pc.order_v")) + "</dd></dl>" +
              '<table class="table compact mt"><thead><tr><th>' + esc(T("pc.src")) + "</th><th>" + esc(T("pc.fetched")) + "</th></tr></thead><tbody>" +
              (a.sources || []).map(function (x) { return '<tr><td><a class="link" href="' + esc(x.url) + '" target="_blank" rel="noopener noreferrer">' + esc(x.id === "models.dev" ? "models.dev" : x.name) + "</a>" + (x.id === "official" ? ' <span class="pill sm ok plain">' + esc(T("pr.o_official")) + "</span>" : "") + "</td><td>" + esc(x.fetched) + "</td></tr>"; }).join("") + "</tbody></table>" +
              '<p class="row-actions"><button class="btn" id="pc-check">' + ic("refresh", "sm") + esc(T("pc.check")) + '</button><span class="muted sm-t">' + esc(T("pc.privacy")) + "</span></p>";
            $("#pc-check").addEventListener("click", function (e) {
              var b = e.currentTarget; b.disabled = true;
              rpc("pricing.catalog.check", {}).then(function (r) { var x = (r.state || {}).result; toast(T("pc.r_" + (x || "error")), x === "error"); paint(r); }).catch(fail).then(function () { if (document.contains(b)) b.disabled = false; });
            });
          };
          rpc("pricing.catalog.get", {}).then(function (st) {
            paint(st);
            $("#pc-auto").addEventListener("click", function (e) {
              var on = !e.currentTarget.classList.contains("on");
              rpc("pricing.catalog.set", { auto: on }).then(function (r) { paint(r); savedMark($("#pc-auto")); }).catch(fail);
            });
          }).catch(fail);
        };
      } else if (section === "network") {
        body = '<section class="card" id="set-net">' + head +
          row("proxy", T("net.proxy"), T("net.proxy_sub"), '<div class="seg" id="net-mode" role="radiogroup" aria-label="' + esc(T("net.proxy")) + '">' + [["auto", T("net.m_auto")], ["manual", T("net.m_manual")], ["off", T("net.m_off")]].map(function (m) { return '<button type="button" role="radio" data-m="' + m[0] + '">' + esc(m[1]) + "</button>"; }).join("") + "</div>") +
          '<div class="pad-x net-manual hidden" id="net-man">' + fieldHtml(T("net.url"), '<input id="net-url" class="mono" autocomplete="off" spellcheck="false" placeholder="http://127.0.0.1:7890">', T("net.url_hint")) +
          fieldHtml(T("net.bypass"), '<input id="net-bypass" class="mono" autocomplete="off" spellcheck="false" placeholder="localhost, 127.0.0.1, .corp.example">', T("net.bypass_hint")) + "</div>" +
          '<div class="pad-x"><p class="muted" id="net-eff"></p><div class="network-actions"><button type="button" class="btn" id="net-test">' + ic("pulse", "sm") + esc(T("net.test")) + '</button><button type="button" class="btn primary" id="net-save">' + esc(T("btn.save")) + '</button></div><div id="net-out" aria-live="polite"></div></div></section>' +
          '<section class="card" id="set-mirrors"><div class="card-head"><span class="ico">' + ic("download") + "</span><h2>" + esc(T("net.mirrors")) + '</h2></div><div class="pad-x"><p class="muted">' + esc(T("net.mirrors_sub")) + '</p><div id="net-mlist"></div>' +
          '<p class="net-presets" id="net-presets"></p><p><button class="btn sm" id="net-madd">' + ic("plus", "sm") + esc(T("net.m_add")) + "</button></p></div></section>";
        after = function () {
          var cur = null, view = null;
          var paintMode = function () {
            $$("#net-mode button").forEach(function (b) { var on = b.getAttribute("data-m") === cur.proxy.mode; b.classList.toggle("active", on); b.setAttribute("aria-checked", on); });
            $("#net-man").classList.toggle("hidden", cur.proxy.mode !== "manual");
          };
          var paintMirrors = function () {
            var ms = cur.mirrors || [];
            $("#net-mlist").innerHTML = '<ol class="net-mlist"><li class="fixed"><code>github.com</code> <span class="muted">' + esc(T("net.m_direct")) + "</span></li>" + ms.map(function (m, i) {
              return '<li><input class="mono" data-mi="' + i + '" value="' + esc(m.prefix || m.manifest || "") + '" aria-label="' + esc(T("net.m_url")) + '"><button type="button" class="icon-btn" data-mdel="' + i + '" aria-label="' + esc(T("btn.remove")) + '">' + ic("x") + "</button></li>";
            }).join("") + "</ol>";
            $$("[data-mi]").forEach(function (inp) { inp.addEventListener("change", function () { var m = cur.mirrors[+inp.getAttribute("data-mi")], v = inp.value.trim(); if (m.manifest) m.manifest = v; else m.prefix = v; m.name = hostOf(v); }); });
            $$("[data-mdel]").forEach(function (b) { b.addEventListener("click", function () { cur.mirrors.splice(+b.getAttribute("data-mdel"), 1); paintMirrors(); }); });
            $("#net-presets").innerHTML = '<span class="muted">' + esc(T("net.presets")) + "</span> " + (view.presets || []).map(function (p, i) {
              var used = ms.some(function (m) { return m.prefix === p.prefix; });
              return '<button type="button" class="chip" data-pre="' + i + '"' + (used ? " disabled" : "") + ">" + ic("plus", "sm") + esc(p.name) + "</button>";
            }).join(" ") + ' <small class="muted">' + esc(T("net.presets_note")) + "</small>";
            $$("[data-pre]").forEach(function (b) { b.addEventListener("click", function () { var p = view.presets[+b.getAttribute("data-pre")]; cur.mirrors.push({ name: p.name, prefix: p.prefix }); paintMirrors(); }); });
          };
          var hostOf = function (v) { try { return new URL(v).host; } catch (e) { return v; } };
          var read = function () {
            cur.proxy.url = $("#net-url").value.trim(); cur.proxy.bypass = $("#net-bypass").value.trim();
            cur.mirrors = (cur.mirrors || []).filter(function (m) { return (m.prefix || m.manifest || "").trim(); });
            return cur;
          };
          var paint = function (v) {
            view = v; cur = JSON.parse(JSON.stringify(v.settings));
            $("#net-url").value = cur.proxy.url || ""; $("#net-bypass").value = cur.proxy.bypass || "";
            $("#net-eff").textContent = T("conn.now", { p: proxyLabel(v.effective) });
            paintMode(); paintMirrors();
          };
          $$("#net-mode button").forEach(function (b) { b.addEventListener("click", function () { cur.proxy.mode = b.getAttribute("data-m"); paintMode(); if (cur.proxy.mode === "manual") $("#net-url").focus(); }); });
          $("#net-madd").addEventListener("click", function () { cur.mirrors.push({ name: "", prefix: "" }); paintMirrors(); var l = $$("[data-mi]"); if (l.length) l[l.length - 1].focus(); });
          $("#net-save").addEventListener("click", function (e) {
            e.currentTarget.disabled = true; var btn = e.currentTarget;
            rpc("network.set", read()).then(function (v) { paint(v); savedMark(btn); }).catch(fail).then(function () { btn.disabled = false; });
          });
          $("#net-test").addEventListener("click", function (e) {
            var btn = e.currentTarget; btn.disabled = true; $("#net-out").innerHTML = '<p class="muted"><span class="spin"></span> ' + esc(T("net.testing")) + "</p>";
            rpc("network.test", { settings: read() }).then(function (r) {
              btn.disabled = false;
              var ok = (r.results || []).filter(function (x) { return x.ok; })[0];
              var h = '<p class="muted">' + esc(T("net.used", { p: proxyLabel(r.effective) })) + '</p><ul class="net-res">' + (r.results || []).map(function (x) {
                return '<li class="' + (x.ok ? "ok" : "bad") + '">' + ic(x.ok ? "check" : "x", "sm") + "<code>" + esc(x.source) + "</code> " + (x.ok ? esc(T("net.ok", { v: x.version, ms: x.ms })) : esc(T("net.e." + (NET_CODES.indexOf(x.code) >= 0 ? x.code : "unknown")))) + "</li>";
              }).join("") + "</ul>";
              if (!ok) { var f = (r.results || [])[0] || {}; h += netErrHtml(f.code, (r.results || []).map(function (x) { return x.source + ": " + (x.error || "ok"); }).join("\n")); }
              $("#net-out").innerHTML = h;
            }).catch(function (err) { btn.disabled = false; $("#net-out").innerHTML = '<p class="err">' + esc(err.message) + "</p>"; });
          });
          rpc("network.get", {}).then(paint).catch(fail);
        };
      } else if (section === "about") {
        // Short link text with an external-link mark; the full address is the tooltip.
        var ext = function (href, text) { return '<a class="ext-link" href="' + esc(href) + '" target="_blank" rel="noopener" data-tip="' + esc(href.replace(/^https:\/\//, "")) + '">' + esc(text) + ic("ext", "sm") + "</a>"; };
        var src = state.sourceURL || SOURCE_REPO, ref = (/\/tree\/([^/?#]+)/.exec(src) || [])[1];
        body = '<section class="card about-hero" id="set-version"><span class="about-mark">' + tideMark() + '</span><div class="about-t"><strong>tidebot ' + esc(state.version || "dev") + "</strong><small>" + esc(T("set.version_sub")) + "</small></div>" +
          '<span class="pill plain mono">' + esc(state.license || "AGPL-3.0-only") + "</span></section>" +
          '<section class="card">' +
          row("source", T("set.source"), T("set.source_sub"), ext(RELEASES_PAGE, "tidebot-releases")) +
          row("code", T("set.code"), T("set.code_sub"), ext(src, "tidebot" + (ref ? " @ " + ref : ""))) +
          row("license", T("set.license"), T("set.license_sub"), ext(SOURCE_REPO + "/blob/main/LICENSE", state.license || "AGPL-3.0-only")) +
          row("terms", T("set.terms"), T("set.terms_sub"), "") + "</section>" +
          '<section class="card" id="set-update"><div class="card-head"><span class="ico">' + ic("refresh") + "</span><h2>" + esc(T("up.title")) + "</h2></div>" +
          row("up_auto", T("up.auto"), T("up.auto_sub"), '<input type="checkbox" class="tgl" role="switch" id="up-auto" aria-label="' + esc(T("up.auto")) + '">') +
          row("up_every", T("up.every"), T("up.every_sub"), '<select id="up-every"><option value="24">' + esc(T("up.daily")) + '</option><option value="168">' + esc(T("up.weekly")) + "</option></select>") +
          row("up_channel", T("up.channel"), T("up.channel_sub"), '<span class="pill plain">' + esc(T("up.stable")) + "</span>") +
          '<div id="up-box" class="pad-x" aria-live="polite"><p class="muted">…</p></div></section>';
        after = function () {
          upAbout();
          var save = function () {
            rpc("update.settings", { enabled: $("#up-auto").checked, interval_hours: parseInt($("#up-every").value, 10) }).then(function () { savedMark($("#up-auto")); }).catch(fail);
          };
          $("#up-auto").addEventListener("change", save);
          $("#up-every").addEventListener("change", save);
        };
      }
      mount('<h1 class="sr-only">' + esc(T("nav.settings")) + "</h1>" + '<div class="settings-layout">' + nav + '<div class="set-col"><div class="set-main">' + body + "</div></div></div>",
        "settings", [[T("nav.settings"), "#/settings"], [T("setnav." + section)]]);
      setFlow($(".set-main"));
      after();
      jumpTo(item);
    });
  }
  // Settings fill wide windows: with two or more ordinary cards the page may flow
  // them into two balanced columns (CSS decides when, by the column's own width —
  // see .set-flow in styles.css); the about header and the danger zone always span.
  function setFlow(main) {
    if (!main) return;
    var n = 0;
    for (var i = 0; i < main.children.length; i++) {
      var c = main.children[i];
      if (!c.classList.contains("about-hero") && !c.classList.contains("danger-zone") && !c.classList.contains("set-wide")) n++;
    }
    main.classList.toggle("set-flow", n >= 2);
  }
  // Settings › About › Updates. Check and update run as a background job on the
  // server (update.check / update.apply with async), this view polls update.status
  // and always shows where it is: checking → up to date (time in the tooltip) /
  // new version → downloading (bar) → installing → restarting, or a readable error
  // with Retry. Every request has a timeout, and no native confirm(): WKWebView in
  // the macOS app returns false from it without showing anything.
  var UP_BUSY = ["checking", "downloading", "installing", "restarting"];
  function upAbout() {
    var timer = null, from = "", restartAt = 0, settled = false;
    var box = function () { return $("#up-box"); };
    var mb = function (n) { return (n / 1048576).toFixed(1); };
    var poll = function (ms) {
      clearTimeout(timer);
      timer = setTimeout(function () {
        if (!box()) return; // left the page
        rpc("update.status", {}, 8000).then(paint).catch(function (err) {
          if (restartAt) return poll(1000); // server is down while it restarts
          paint(null, err);
        });
      }, ms);
    };
    var paint = function (st, reqErr) {
      var el = box(); if (!el) return;
      if (!st) { // the status request itself failed: keep the last view's facts, show why
        el.innerHTML = '<div class="up-state err" role="alert"><p class="err">' + ic("x", "sm") + esc(T("up.e_request", { e: reqErr.message })) + "</p></div>" +
          '<div class="up-actions"><button class="btn" id="up-check">' + ic("refresh", "sm") + esc(T("err.retry")) + "</button></div>";
        $("#up-check").addEventListener("click", check);
        return;
      }
      var s = st.settings || {}, last = st.last, inst = st.install || {}, res = st.result, ph = st.phase || "idle", pr = st.progress || {}, h = "";
      var busy = UP_BUSY.indexOf(ph) >= 0, cur = (st.current || "").replace(/^v/, "");
      if (!settled) { $("#up-auto").checked = !!s.enabled; $("#up-every").value = String(s.interval_hours >= 168 ? 168 : 24); settled = true; }
      if (!from) from = cur;
      if (restartAt && !busy) { // back up: new version → reload into it; same version → it rolled back
        if (cur !== from) { location.reload(); return; }
        restartAt = 0;
      }
      if (res && res.status === "rolled_back") h += '<p class="err">' + esc(T("up.rolled_back", { v: res.to, why: res.detail || "" })) + "</p>";
      if (ph === "checking") {
        h += '<p class="up-state" role="status"><span class="spin"></span>' + esc(T("up.checking")) + "</p>";
      } else if (ph === "downloading") {
        var pct = pr.total > 0 ? Math.min(100, Math.floor(pr.done * 100 / pr.total)) : 0;
        h += '<div class="up-state" role="status"><p>' + esc(T("up.downloading", { v: last ? last.latest : "" })) + '<span class="muted up-bytes">' + esc(pr.total > 0 ? T("up.bytes", { a: mb(pr.done), b: mb(pr.total), p: pct }) : T("up.bytes_n", { a: mb(pr.done || 0) })) + "</span></p>" +
          '<div class="progress up-bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="' + pct + '"><span data-w="' + pct + '"></span></div></div>';
      } else if (ph === "installing") {
        h += '<p class="up-state" role="status"><span class="spin"></span>' + esc(T("up.installing")) + "</p>";
      } else if (ph === "restarting" || restartAt) {
        if (!restartAt) restartAt = Date.now();
        var slow = Date.now() - restartAt > 90000;
        h += '<p class="up-state" role="status">' + (slow ? "" : '<span class="spin"></span>') + esc(T(slow ? "up.restart_slow" : "up.restarting")) + "</p>";
      } else {
        if (st.apply_error) h += netErrHtml(st.apply_code, st.apply_error, null, proxyHint(st));
        else if (st.error) h += netErrHtml(st.error_code, st.error, st.attempts, proxyHint(st));
        if (last && last.available) {
          h += '<div class="up-new"><h3>' + esc(T("up.avail", { v: last.latest, cur: last.current })) + "</h3>" +
            (last.date ? '<p class="muted">' + esc(last.date) + "</p>" : "") +
            (last.notes ? '<pre class="changelog">' + esc(last.notes) + "</pre>" : "") +
            (last.notes_url ? '<p><a href="' + esc(last.notes_url) + '" target="_blank" rel="noopener">' + esc(T("up.notes")) + "</a></p>" : "") + "</div>";
          if (!last.can_apply && inst.kind === "self-built") h += '<p class="muted">' + esc(T("up.self_built")) + ' <a href="' + esc(st.releases_page || RELEASES_PAGE) + '" target="_blank" rel="noopener">' + esc(T("up.download_page")) + "</a></p>";
          else if (!last.can_apply) h += '<p class="muted">' + esc(T("up.notify_only", { kind: inst.kind || "?" })) + (inst.reason ? " — " + esc(inst.reason) : "") + "</p>";
        } else if (last && inst.kind === "self-built") {
          h += '<p class="muted">' + esc(T("up.self_built_dev", { v: last.latest })) + ' <a href="' + esc(st.releases_page || RELEASES_PAGE) + '" target="_blank" rel="noopener">' + esc(T("up.download_page")) + "</a></p>";
        } else if (last && !st.error) {
          h += '<p class="up-state ok-text"' + (st.checked_at ? ' data-tip="' + esc(T("up.checked_at", { t: fullTime(st.checked_at) })) + '"' : "") + ">" + ic("check", "sm") + esc(T("up.none", { v: cur })) + "</p>";
        }
      }
      var canApply = !busy && !restartAt && last && last.available && last.can_apply;
      h += '<div class="up-actions">' +
        (canApply ? '<button class="btn primary" id="up-apply">' + ic("download", "sm") + esc(T(st.apply_error ? "up.apply_retry" : "up.apply")) + "</button>" : "") +
        '<button class="btn" id="up-check"' + (busy || restartAt ? " disabled" : "") + ">" + (ph === "checking" ? '<span class="spin"></span>' : ic("refresh", "sm")) + esc(T(st.error && !busy ? "err.retry" : "up.check")) + "</button>" +
        '<small class="muted">' + esc(T("net.via", { p: proxyLabel(st.proxy) })) + tip(T("up.privacy")) + "</small></div>" +
        (canApply ? '<p class="muted up-sub">' + esc(T("up.apply_sub")) + "</p>" : "");
      el.innerHTML = h;
      applySizes(el);
      $("#up-check").addEventListener("click", check);
      var ap = $("#up-apply");
      if (ap) ap.addEventListener("click", function () {
        confirmDialog({ title: T("up.confirm_title", { v: last.latest }), body: T("up.confirm", { v: last.latest }), ok: T("up.apply"), icon: "download" }).then(function (y) {
          if (!y) return;
          ap.disabled = true;
          rpc("update.apply", { async: true }, 15000).then(function (st2) { paint(st2); }).catch(function (err) { toast(err.message, true); poll(0); });
        });
      });
      if (busy || restartAt) poll(restartAt ? 1000 : 500);
    };
    var check = function () {
      var b = $("#up-check"); if (b) b.disabled = true;
      rpc("update.check", { async: true }, 15000).then(paint).catch(function (err) { paint(null, err); });
    };
    rpc("update.status", {}, 8000).then(paint).catch(function (err) { paint(null, err); });
  }
  // A stalled or dead system proxy is the usual reason a check times out on a desktop;
  // then "you need a proxy" is the wrong advice, so say which proxy and what to try.
  function proxyHint(st) {
    var p = st.proxy || {};
    if (!p.source || p.source === "none") return "";
    var code = st.apply_error ? st.apply_code : st.error_code;
    if (["timeout", "proxy", "refused", "tls"].indexOf(code) < 0) return "";
    return T("up.proxy_hint", { p: p.https || p.http || "" });
  }
  function jumpTo(item) {
    if (!item) return;
    var el = document.getElementById("set-" + item);
    if (!el) return;
    el.scrollIntoView({ block: "center" });
    el.classList.add("flash");
    setTimeout(function () { el.classList.remove("flash"); }, 1600);
  }
  // 2FA: scan → verify → recovery codes. Nothing is enabled until a code verifies;
  // the codes are shown once, in this dialog only.
  function twoFASetup() {
    api("/api/auth/2fa/begin", { method: "POST", body: {} }).then(function (b) {
      var steps = '<ol class="steps"><li class="on" data-st="1">' + esc(T("tf.s1")) + '</li><li data-st="2">' + esc(T("tf.s2")) + '</li><li data-st="3">' + esc(T("tf.s3")) + "</li></ol>";
      modal('<div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>" + esc(T("tf.title")) + "</h2></div>" + steps +
        '<div id="tf-body"><div class="tf-scan"><img id="tf-qr" alt="' + esc(T("tf.qr_alt")) + '" width="180" height="180"><div><p>' + esc(T("tf.scan")) + '</p><p class="muted">' + esc(T("tf.manual")) + '</p><code class="secret-code" id="tf-secret"></code></div></div>' +
        '<label class="field"><span>' + esc(T("tf.code")) + '</span><input id="tf-code" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" autocomplete="one-time-code" placeholder="123456"></label><p class="err" id="tf-err"></p>' +
        '<div class="actions"><button class="btn" id="tf-cancel">' + esc(T("btn.cancel")) + '</button><button class="btn primary" id="tf-verify">' + esc(T("tf.verify")) + "</button></div></div>",
        function (root, close) {
          $("#tf-qr").src = b.qr;
          $("#tf-secret").textContent = b.secret.replace(/(.{4})/g, "$1 ").trim();
          $("#tf-cancel").addEventListener("click", close);
          var code = $("#tf-code"), busy = false;
          code.focus();
          var verify = function () {
            if (busy) return; busy = true;
            $("#tf-err").textContent = "";
            api("/api/auth/2fa/confirm", { method: "POST", body: { code: code.value } }).then(function (r) {
              $$(".steps li", root).forEach(function (li) { li.classList.toggle("on", li.getAttribute("data-st") === "3"); li.classList.toggle("done", li.getAttribute("data-st") !== "3"); });
              var txt = r.recovery_codes.join("\n");
              $("#tf-body").innerHTML = '<p class="ok-text">' + esc(T("tf.enabled")) + '</p><p>' + esc(T("tf.rec_intro")) + '</p><pre class="rec-codes" id="tf-rec"></pre><p class="muted">' + esc(T("tf.rec_once")) + "</p>" +
                '<div class="actions"><button class="btn" id="tf-copy">' + esc(T("tf.copy")) + '</button><button class="btn" id="tf-dl">' + esc(T("tf.download")) + '</button><button class="btn primary" id="tf-done">' + esc(T("tf.done")) + "</button></div>";
              $("#tf-rec").textContent = txt;
              $("#tf-copy").addEventListener("click", function () { copyText(txt); });
              $("#tf-dl").addEventListener("click", function () {
                saveBlob(new Blob(["tidebot recovery codes\n\n" + txt + "\n"], { type: "text/plain" }), "tidebot-recovery-codes.txt").catch(fail);
              });
              $("#tf-done").addEventListener("click", function () { txt = ""; close(); refresh(); });
            }).catch(function (e) { $("#tf-err").textContent = T("tf.bad_code"); code.select(); }).then(function () { busy = false; });
          };
          code.addEventListener("input", function () { code.value = code.value.replace(/\D/g, "").slice(0, 6); if (code.value.length === 6) verify(); });
          $("#tf-verify").addEventListener("click", verify);
        });
    }).catch(fail);
  }
  // Change (or, from a desktop window without one, set) the owner password. Other
  // sessions are signed out by the server; this one stays. Inputs carry
  // current-password / new-password so password managers fill and save them.
  function pwChange(hasPw) {
    var pw = function (id, label, ac, extra) {
      return '<label class="field"><span>' + esc(label) + '</span><div class="in-group"><input type="password" id="' + id + '" autocomplete="' + ac + '" maxlength="1024" spellcheck="false" autocapitalize="off">' + (extra || "") + "</div></label>";
    };
    modal('<div class="card-head"><span class="ico">' + ic("key") + "</span><h2>" + esc(T(hasPw ? "pw.change" : "pw.set")) + '</h2></div><p class="muted pw-sub">' + esc(T("pw.sub")) + "</p>" +
      '<form id="pw-form" class="pw-form" novalidate><input type="text" class="hidden" autocomplete="username" value="owner" readonly tabindex="-1" aria-hidden="true">' +
      (hasPw ? pw("pw-cur", T("pw.current"), "current-password") + '<p class="err" id="pw-cur-err"></p>' : "") +
      pw("pw-new", T("pw.new"), "new-password", '<button type="button" class="btn ghost" id="pw-eye" aria-pressed="false">' + esc(T("pv.show")) + "</button>") +
      '<p class="hint pw-len" id="pw-len">' + ic("check", "sm") + esc(T("pw.len")) + "</p>" +
      pw("pw-rep", T("pw.repeat"), "new-password") + '<p class="err" id="pw-err"></p>' +
      '<div class="actions"><button type="button" class="btn" id="pw-cancel">' + esc(T("btn.cancel")) + '</button><button type="submit" class="btn primary" id="pw-go">' + esc(T("pw.save")) + "</button></div></form>",
      function (root, close) {
        var cur = $("#pw-cur", root), nw = $("#pw-new", root), rep = $("#pw-rep", root), go = $("#pw-go", root), err = $("#pw-err", root), cerr = $("#pw-cur-err", root);
        var busy = false, waitTimer = null, waitUntil = 0;
        (cur || nw).focus();
        var check = function (final) {
          var a = nw.value, b = rep.value, msg = "";
          $("#pw-len", root).classList.toggle("ok", a.length >= 8);
          if (final && a.length < 8) msg = T("pw.len");
          else if (b && (final || b.length >= a.length) && a !== b) msg = T("pw.mismatch");
          else if (final && hasPw && cur.value && a === cur.value) msg = T("pw.same");
          if (Date.now() >= waitUntil) err.textContent = msg;
          return !msg;
        };
        var wait = function (secs) {
          waitUntil = Date.now() + secs * 1000; go.disabled = true; clearInterval(waitTimer);
          var tick = function () {
            var left = Math.ceil((waitUntil - Date.now()) / 1000);
            if (left <= 0 || !document.contains(go)) { clearInterval(waitTimer); waitUntil = 0; go.disabled = false; err.textContent = ""; return; }
            err.textContent = T("pw.too_many", { n: left });
          };
          tick(); waitTimer = setInterval(tick, 1000);
        };
        nw.addEventListener("input", function () { check(false); });
        rep.addEventListener("input", function () { check(false); });
        if (cur) cur.addEventListener("input", function () { cerr.textContent = ""; });
        $("#pw-eye", root).addEventListener("click", function (e) {
          var on = nw.type === "password";
          nw.type = rep.type = on ? "text" : "password";
          e.currentTarget.setAttribute("aria-pressed", on); e.currentTarget.textContent = T(on ? "pv.hide" : "pv.show");
        });
        $("#pw-cancel", root).addEventListener("click", function () { clearInterval(waitTimer); close(); });
        $("#pw-form", root).addEventListener("submit", function (e) {
          e.preventDefault();
          if (busy || go.disabled) return;
          if (hasPw && !cur.value) { cerr.textContent = T("pw.bad_current"); cur.focus(); return; }
          if (!check(true)) { (nw.value.length < 8 ? nw : rep).focus(); return; }
          busy = true; go.disabled = true;
          api("/api/auth/password", { method: "POST", body: { current: cur ? cur.value : "", new: nw.value } }).then(function (r) {
            $$("input", root).forEach(function (x) { x.value = ""; });
            clearInterval(waitTimer); close();
            toast(r.signed_out > 0 ? T("pw.done_n", { n: r.signed_out }) : T("pw.done"));
            refresh();
          }).catch(function (x) {
            go.disabled = false;
            var code = x.data && x.data.code;
            if (x.status === 429) { wait((x.data && x.data.retry_after) || 60); return; }
            if (code === "bad_current") { cerr.textContent = T("pw.bad_current"); cur.select(); return; }
            if (code === "same") { err.textContent = T("pw.same"); nw.select(); return; }
            if (code === "length") { err.textContent = T("pw.len"); nw.focus(); return; }
            err.textContent = x.message;
          }).then(function () { busy = false; });
        });
      });
  }
  // Turn 2FA off after re-entering the password. Wrong passwords count toward the
  // sign-in lockout on the server; a 429 counts down on the button like the
  // change-password dialog. A real <form>, so Enter submits and managers fill it.
  function twoFADisable(hasPw) {
    modal('<div class="card-head"><span class="ico">' + ic("shield") + "</span><h2>" + esc(T("tf.disable")) + '</h2></div><p class="muted pw-sub">' + esc(T(hasPw ? "tf.disable_sub" : "tf.disable_sub_nopw")) + "</p>" +
      '<form id="tf-off-form" class="pw-form" novalidate><input type="text" class="hidden" autocomplete="username" value="owner" readonly tabindex="-1" aria-hidden="true">' +
      (hasPw ? '<label class="field"><span>' + esc(T("login.password")) + '</span><input type="password" id="tf-pw" autocomplete="current-password" maxlength="1024" spellcheck="false" autocapitalize="off" aria-describedby="tf-err"></label>' : "") +
      '<p class="err" id="tf-err" role="alert"></p>' +
      '<div class="actions"><button type="button" class="btn" id="tf-cancel">' + esc(T("btn.cancel")) + '</button><button type="submit" class="btn danger" id="tf-go">' + esc(T("tf.disable")) + "</button></div></form>", function (root, close) {
        var pwIn = $("#tf-pw", root), go = $("#tf-go", root), err = $("#tf-err", root);
        var busy = false, waitTimer = null, waitUntil = 0;
        (pwIn || go).focus();
        var wait = function (secs) {
          waitUntil = Date.now() + secs * 1000; go.disabled = true; clearInterval(waitTimer);
          var tick = function () {
            var left = Math.ceil((waitUntil - Date.now()) / 1000);
            if (left <= 0 || !document.contains(go)) { clearInterval(waitTimer); waitUntil = 0; go.disabled = false; err.textContent = ""; return; }
            err.textContent = T("pw.too_many", { n: left });
          };
          tick(); waitTimer = setInterval(tick, 1000);
        };
        if (pwIn) pwIn.addEventListener("input", function () { if (Date.now() >= waitUntil) err.textContent = ""; });
        $("#tf-cancel", root).addEventListener("click", function () { clearInterval(waitTimer); close(); });
        $("#tf-off-form", root).addEventListener("submit", function (e) {
          e.preventDefault();
          if (busy || go.disabled) return;
          if (pwIn && !pwIn.value) { err.textContent = T("tf.need_pw"); pwIn.focus(); return; }
          busy = true; go.disabled = true;
          api("/api/auth/2fa/disable", { method: "POST", body: { password: pwIn ? pwIn.value : "" } }).then(function () {
            if (pwIn) pwIn.value = "";
            clearInterval(waitTimer); close(); toast(T("tf.disabled")); refresh();
          }).catch(function (x) {
            go.disabled = false;
            if (x.status === 429) { wait((x.data && x.data.retry_after) || 60); return; }
            if (x.data && x.data.code === "bad_password") { err.textContent = T("tf.bad_pw"); if (pwIn) pwIn.select(); return; }
            err.textContent = x.message;
          }).then(function () { busy = false; });
        });
      });
  }

