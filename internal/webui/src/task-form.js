  // ---------- task form ----------
  // ---------- schedule editor (simple / cron / manual) ----------
  var DOW = [1, 2, 3, 4, 5, 6, 0];
  function parseSched(c) {
    var s = { mode: "simple", kind: "daily", time: "09:00", every: 15, minute: 0, dow: 1, dom: 1, cron: c || "" };
    if (!c) { s.mode = "manual"; return s; }
    var p = c.trim().split(/\s+/), num = function (x) { return /^\d+$/.test(x); };
    var hm = function () { return ("0" + p[1]).slice(-2) + ":" + ("0" + p[0]).slice(-2); };
    if (p.length !== 5) { s.mode = "cron"; return s; }
    var m;
    if ((m = /^\*\/(\d+)$/.exec(p[0])) && p[1] === "*" && p[2] === "*" && p[3] === "*" && p[4] === "*") { s.kind = "minutes"; s.every = +m[1]; return s; }
    if (num(p[0]) && p[1] === "*" && p[2] === "*" && p[3] === "*" && p[4] === "*") { s.kind = "hourly"; s.minute = +p[0]; return s; }
    if (num(p[0]) && num(p[1]) && p[3] === "*") {
      s.time = hm();
      if (p[2] === "*" && p[4] === "*") { s.kind = "daily"; return s; }
      if (p[2] === "*" && p[4] === "1-5") { s.kind = "weekdays"; return s; }
      if (p[2] === "*" && /^[0-6]$/.test(p[4])) { s.kind = "weekly"; s.dow = +p[4]; return s; }
      if (num(p[2]) && p[4] === "*") { s.kind = "monthly"; s.dom = +p[2]; return s; }
    }
    s.mode = "cron"; s.unconvertible = true; return s;
  }
  function simpleCron(s) {
    var t = (s.time || "09:00").split(":"), h = +t[0] || 0, m = +t[1] || 0;
    switch (s.kind) {
      case "minutes": return "*/" + Math.min(59, Math.max(1, +s.every || 15)) + " * * * *";
      case "hourly": return Math.min(59, Math.max(0, +s.minute || 0)) + " * * * *";
      case "weekdays": return m + " " + h + " * * 1-5";
      case "weekly": return m + " " + h + " * * " + s.dow;
      case "monthly": return m + " " + h + " " + Math.min(28, Math.max(1, +s.dom || 1)) + " * *";
      default: return m + " " + h + " * * *";
    }
  }
  // Quick local syntax check so obvious mistakes show up while typing; the server has the final say.
  function cronSyntaxError(c) {
    var p = c.trim().split(/\s+/);
    if (p.length !== 5) return T("se.need5");
    var lim = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 7]];
    for (var i = 0; i < 5; i++) {
      var ok = p[i].split(",").every(function (part) {
        var m = /^(\*|(\d+)(?:-(\d+))?)(?:\/(\d+))?$/.exec(part);
        if (!m) return /^[A-Za-z]{3}(-[A-Za-z]{3})?$/.test(part) && (i === 3 || i === 4);
        var a = m[2] == null ? null : +m[2], b = m[3] == null ? null : +m[3];
        return (a == null || (a >= lim[i][0] && a <= lim[i][1])) && (b == null || (b >= lim[i][0] && b <= lim[i][1] && b >= a)) && (m[4] == null || +m[4] > 0);
      });
      if (!ok) return T("se.bad_field", { f: T("se.f_" + i), v: p[i] });
    }
    return "";
  }
  var previewCache = {};
  // dstNoteHtml: one quiet line when a clock change moves or merges a fixed daily time
  // (server rules in internal/scheduler/dst.go); the rule and zone are in the tooltip.
  function dstNoteText(n, zone) {
    if (!n || !n.kind) return "";
    var day = new Intl.DateTimeFormat(state.lang, { timeZone: "UTC", month: "numeric", day: "numeric", weekday: "short" }).format(new Date(n.date + "T12:00:00Z"));
    var at = n.at ? new Intl.DateTimeFormat(state.lang, { timeZone: zone || undefined, hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(new Date(n.at)) : "";
    var key = n.kind === "gap" ? "se.dst_gap" : n.kind === "fold" ? "se.dst_fold" : "se.dst_skip";
    return T(key, { d: day, w: n.wall, at: at });
  }
  function dstNoteHtml(n, zone) {
    var text = dstNoteText(n, zone);
    return text ? ic("clock", "sm") + "<span>" + esc(text) + "</span>" + tip(T("se.dst_tip", { z: zone || "" })) : "";
  }
  function scheduleEditor(host, cron, serverTz) {
    var s = parseSched(cron), tz = serverTz || state.tz || "", timer = null, seq = 0, touched = false;
    var dowName = function (d) { return new Intl.DateTimeFormat(state.lang, { weekday: "long", timeZone: "UTC" }).format(new Date(Date.UTC(2023, 0, 1 + d))); };
    var browserTz = (Intl.DateTimeFormat().resolvedOptions().timeZone) || "";
    var tzs = [serverTz, browserTz, "UTC", "Asia/Shanghai", "America/Los_Angeles", "America/New_York", "Europe/London", "Europe/Berlin", "Asia/Tokyo"].filter(function (x, i, a) { return x && a.indexOf(x) === i; });
    host.innerHTML = '<div class="sched-ed"><div class="seg" role="tablist">' + [["simple", T("se.simple")], ["cron", "Cron"], ["manual", T("se.manual")]].map(function (m) { return '<button type="button" role="tab" data-mode="' + m[0] + '">' + esc(m[1]) + "</button>"; }).join("") + "</div>" +
      '<div class="se-pane" data-pane="simple"><div class="form-row sched"><select data-k="kind" aria-label="' + esc(T("tf.schedule")) + '">' + ["minutes", "hourly", "daily", "weekdays", "weekly", "monthly"].map(function (k) { return opt(k, T("se.k_" + k), k === s.kind); }).join("") + "</select>" +
      '<label class="inl" data-show="minutes">' + esc(T("se.every")) + ' <input type="number" min="1" max="59" data-k="every" value="' + esc(s.every) + '"> ' + esc(T("se.minutes")) + "</label>" +
      '<label class="inl" data-show="hourly">' + esc(T("se.at_minute")) + ' <input type="number" min="0" max="59" data-k="minute" value="' + esc(s.minute) + '"></label>' +
      '<select data-k="dow" data-show="weekly" aria-label="' + esc(T("se.weekday")) + '">' + DOW.map(function (d) { return opt(d, dowName(d), d === s.dow); }).join("") + "</select>" +
      '<label class="inl" data-show="monthly">' + esc(T("se.day")) + ' <input type="number" min="1" max="28" data-k="dom" value="' + esc(s.dom) + '"></label>' +
      '<input type="time" data-k="time" value="' + esc(s.time) + '" data-show="daily weekdays weekly monthly" aria-label="' + esc(T("se.time")) + '"></div></div>' +
      '<div class="se-pane" data-pane="cron"><input class="mono" data-k="cron" maxlength="200" spellcheck="false" placeholder="*/30 9-18 * * 1-5" value="' + esc(s.cron) + '" aria-describedby="se-err"><small class="hint">' + esc(T("se.cron_hint")) + '</small></div>' +
      '<div class="se-pane" data-pane="manual"><p class="muted">' + esc(T("tf.manual_hint")) + "</p></div>" +
      '<p class="field-err" id="se-err" role="alert"></p><p class="se-note muted"></p>' +
      '<div class="sched-prev"><div class="sp-head"><span class="sp-human"></span><label class="inl">' + esc(T("se.show_in")) + ' <select data-k="tz">' + tzs.map(function (z) { return opt(z, z + (z === serverTz ? " · " + T("se.server") : ""), z === tz); }).join("") + '</select></label></div><ul class="next"></ul><p class="sp-dst hidden"></p></div></div>';
    var $k = function (k) { return $('[data-k="' + k + '"]', host); };
    var err = $("#se-err", host), note = $(".se-note", host), list = $(".next", host), human = $(".sp-human", host), dstEl = $(".sp-dst", host);
    function value() {
      if (s.mode === "manual") return "";
      if (s.mode === "cron") return $k("cron").value.trim();
      return simpleCron({ kind: $k("kind").value, every: $k("every").value, minute: $k("minute").value, dow: $k("dow").value, dom: $k("dom").value, time: $k("time").value });
    }
    function show(d, zone) {
      list.innerHTML = (d.times || []).map(function (t) { return "<li>" + esc(new Intl.DateTimeFormat(state.lang, { timeZone: zone, weekday: "short", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(new Date(t))) + "</li>"; }).join("");
      dstEl.innerHTML = dstNoteHtml(d.dst, d.scheduler_timezone || tz);
      dstEl.classList.toggle("hidden", !d.dst);
    }
    function preview() {
      var c = value();
      err.textContent = ""; note.textContent = "";
      $$("[data-mode]", host).forEach(function (b) { var on = b.getAttribute("data-mode") === s.mode; b.classList.toggle("active", on); b.setAttribute("aria-selected", on); });
      $$("[data-pane]", host).forEach(function (p) { p.classList.toggle("hidden", p.getAttribute("data-pane") !== s.mode); });
      var kind = $k("kind").value;
      $$("[data-show]", host).forEach(function (x) { x.classList.toggle("hidden", x.getAttribute("data-show").split(" ").indexOf(kind) < 0); });
      $(".sched-prev", host).classList.toggle("hidden", !c);
      if (s.mode === "cron" && s.unconvertible) note.textContent = T("se.cron_only");
      if (!c) { host.dispatchEvent(new CustomEvent("sched", { detail: { cron: "", ok: true } })); return; }
      var bad = cronSyntaxError(c);
      if (bad) { err.textContent = bad; list.innerHTML = ""; human.textContent = ""; dstEl.classList.add("hidden"); host.dispatchEvent(new CustomEvent("sched", { detail: { cron: c, ok: false } })); return; }
      human.innerHTML = "<strong>" + esc(cronHuman(c)) + '</strong> <code class="muted">' + esc(c) + "</code>";
      var zone = $k("tz").value, key = c + "|" + zone;
      host.dispatchEvent(new CustomEvent("sched", { detail: { cron: c, ok: true } }));
      if (previewCache[key]) { show(previewCache[key], zone); return; }
      clearTimeout(timer);
      var my = ++seq;
      timer = setTimeout(function () {
        api("/api/scheduler/preview?cron=" + enc(c) + "&tz=" + enc(zone)).then(function (d) {
          previewCache[key] = { times: d.times || [], dst: d.dst || null, scheduler_timezone: d.scheduler_timezone };
          if (my === seq) show(previewCache[key], zone);
        }).catch(function (e) { if (my === seq) { err.textContent = e.message; list.innerHTML = ""; dstEl.classList.add("hidden"); host.dispatchEvent(new CustomEvent("sched", { detail: { cron: c, ok: false } })); } });
      }, 300);
    }
    host.addEventListener("input", function (e) { if (e.isTrusted || e._tbUserSelect) touched = true; });
    host.addEventListener("change", function (e) { if ((e.isTrusted || e._tbUserSelect) && e.target.getAttribute("data-k") !== "tz") touched = true; });
    $$("[data-mode]", host).forEach(function (b) { b.addEventListener("click", function (e) {
      if (e.isTrusted || e._tbUserSelect) touched = true;
      var to = b.getAttribute("data-mode");
      if (to === s.mode) return;
      if (to === "simple") {
        var cur = s.mode === "cron" ? parseSched($k("cron").value.trim()) : null;
        if (cur && cur.mode !== "simple") { s.mode = "cron"; preview(); note.textContent = T("se.cron_only"); return; }
        if (cur) { $k("kind").value = cur.kind; $k("every").value = cur.every; $k("minute").value = cur.minute; $k("dow").value = cur.dow; $k("dom").value = cur.dom; $k("time").value = cur.time; }
        s.unconvertible = false;
      }
      if (to === "cron" && s.mode === "simple") $k("cron").value = value();
      s.mode = to; preview();
    }); });
    ["kind", "every", "minute", "dow", "dom", "time", "tz"].forEach(function (k) { $k(k).addEventListener("input", preview); $k(k).addEventListener("change", preview); });
    $k("cron").addEventListener("input", function () { s.unconvertible = parseSched($k("cron").value).mode === "cron"; preview(); });
    preview();
    // set: apply a cron from code (auto-fill) without marking the editor as touched.
    function set(c) {
      var p = parseSched(c);
      if (p.mode === "simple") { ["kind", "every", "minute", "dow", "dom", "time"].forEach(function (k) { $k(k).value = p[k]; }); }
      s.unconvertible = p.mode === "cron";
      $k("cron").value = c; s.mode = p.mode === "manual" ? "manual" : p.mode;
      preview();
      var ed = $(".sched-ed", host); ed.classList.remove("af-flash"); void ed.offsetWidth; ed.classList.add("af-flash");
    }
    return { value: value, valid: function () { return !err.textContent; }, set: set, touched: function () { return touched; } };
  }
  // key_set includes managed/keyless gateways whose plugin supplies authentication.
  // Do not infer readiness from a default flag, a model name or a past task.
  function readyProviders(st) {
    return (st.providers || []).filter(function (p) { return p.key_set === true; });
  }
  function smartProvider(st) {
    var ps = readyProviders(st), d = ps.filter(function (p) { return p.name === st.default_provider; })[0];
    return (d || ps[0] || {}).name || "";
  }
  function providerOptions(st, selected, preserve, models) {
    var ps = readyProviders(st), found = ps.some(function (p) { return p.name === selected; });
    var unavailable = preserve && !found;
    if (!found && !preserve) selected = smartProvider(st);
    // Keep an existing task's exact value, even if deleted or now missing its key.
    // It remains visible as the current value, but cannot be picked for new work.
    var head = unavailable ? '<option value="' + esc(selected || "") + '" selected disabled hidden>' + esc(selected ? selected + " " + T("qr.no_key") : T("td.default")) + '</option>' : "";
    if (!ps.length && !unavailable) head = '<option value="" selected disabled>' + esc(T("cc.no_provider")) + '</option>';
    return head + ps.map(function (p) { return opt(p.name, p.name + (models && p.model ? " · " + p.model : ""), p.name === selected); }).join("");
  }
  // New-task auto-fill: as the instruction is typed, the server guesses a name and a schedule
  // (POST /api/tasks/infer). A field is only filled while the user hasn't edited it themselves.
  function wireTaskAutofill(root, f, se, skills) {
    var lastName = "", timer = null, seq = 0, note = $("#af-note", root), pill = $("#af-name", root), filled = {}, lastCron = "";
    var bPill = $("#af-budget", root), lastBudget = "", budgetMine = false;
    f.budget_usd.addEventListener("input", function (e) { if (e.isTrusted) { budgetMine = f.budget_usd.value !== lastBudget; bPill.classList.toggle("hidden", budgetMine); if (budgetMine) { delete filled.budget; paint(lastCron); } } });
    var why = function (b) {
      var m = function (v) { return money(v, b.currency); }, heavy = b.heavy ? T("af.b_heavy") : "";
      if (b.basis === "similar") return T("af.b_similar", { n: b.runs, names: (b.similar || []).join("、"), p90: m(b.p90), max: m(b.max) });
      if (b.basis === "provider") return T("af.b_provider", { n: b.runs, p90: m(b.p90), max: m(b.max) }) + heavy;
      if (b.basis === "price") return T("af.b_price", { model: b.model || "—", est: m(b.estimate) }) + heavy;
      if (b.basis === "subscription") return T("af.b_sub");
      if (b.basis === "free") return T("af.b_free");
      return "";
    };
    var whyAll = function (b) { return why(b) + (b.amount > 0 && b.per_day > 1 ? T("af.b_perday", { n: b.per_day, run: money(b.per_run, b.currency), amt: money(b.amount, b.currency) }) : ""); };
    var setBudget = function (b) {
      if (!b || b.basis === "none" || budgetMine || (f.cost_enabled && !f.cost_enabled.checked)) return;
      var v = b.amount > 0 ? String(b.amount) : "";
      bPill.setAttribute("data-tip", whyAll(b));
      bPill.classList.remove("hidden");
      bPill.innerHTML = '<button type="button" class="btn sm">' + esc(T("costmode.adopt",{amount:b.amount > 0 ? money(b.amount,b.currency) : T("tf.no_cap")})) + '</button>';
      $("button",bPill).addEventListener("click",function(){
        if(f.cost_enabled && !f.cost_enabled.checked) return;
        f.budget_usd.value=v;lastBudget=v;budgetMine=true;
        filled.budget=b.amount > 0 ? money(b.amount,b.currency) : T("tf.no_cap");
        bPill.classList.add("hidden");paint(lastCron);
      });
    };
    var ask = function (delay) {
      clearTimeout(timer);
      var my = ++seq;
      timer = setTimeout(function () {
        var text = f.prompt.value;
        apiRaw("/api/tasks/infer", { method: "POST", body: { prompt: text.trim().length >= 4 ? text : "", name: f.name.value.trim(), provider: f.provider.value, cron: se.touched() ? se.value() : "" } }).then(function (g) {
          if (my !== seq || !g) return;
          if (text.trim().length >= 4) {
            setName(g.name);
            if (g.cron && !se.touched() && se.value() !== g.cron) { se.set(g.cron); lastCron = g.cron; }
            else if (g.cron && se.value() === g.cron) lastCron = g.cron;
          }
          setBudget(g.budget);
          paint(lastCron);
        }).catch(function () {});
      }, delay);
    };
    var nameMine = function () { var v = f.name.value.trim(); return v !== "" && v !== lastName; };
    var setName = function (v) { if (!v || nameMine()) return; f.name.value = v; lastName = v; pill.classList.remove("hidden"); filled.name = 1; };
    var paint = function (cron) {
      var bits = [];
      if (filled.name) bits.push(T("af.f_name"));
      if (cron) bits.push(T("af.f_sched", { s: cronHuman(cron) }));
      if (filled.budget) bits.push(T("af.f_budget", { s: filled.budget }));
      note.classList.toggle("on", bits.length > 0);
      $("span", note).textContent = bits.length ? T("af.filled", { list: bits.join(T("af.sep")) }) : T("af.hint");
    };
    f.name.addEventListener("input", function (e) { if (e.isTrusted && f.name.value.trim() !== lastName) { pill.classList.add("hidden"); delete filled.name; paint(lastCron); } });
    f.prompt.addEventListener("input", function () { if (f.prompt.value.trim().length >= 4) ask(350); });
    f.provider.addEventListener("change", function () { ask(0); });
    $("#sched-host", root).addEventListener("sched", function () { if (se.touched()) ask(400); });
    ask(0); // budget for the default provider right away
    f.skill_id.addEventListener("change", function () {
      var sk = skills.filter(function (x) { return x.id === f.skill_id.value; })[0];
      if (sk && !f.prompt.value.trim()) setName(sk.name), paint(lastCron);
    });
  }
  // Working folder field (task form): type a path or, in the desktop app, pick one with
  // the native folder dialog; empty = the folder tidebot keeps for the task, shown as the
  // placeholder. The server cleans the path (~, quotes, separators) and says up front
  // whether it is usable or will be created, as a pill in the label row so nothing moves.
  var pickFolderOK = null;
  function folderPickable() {
    if (!pickFolderOK) pickFolderOK = api("/api/desktop/pick-folder").then(function (r) { return !!(r && r.available); }, function () { return false; });
    return pickFolderOK;
  }
  function wsFieldHtml(val) {
    return '<div class="field ws-field"><span id="tf-wlbl">' + esc(T("tf.workspace")) + tip(T("tf.ws_hint")) + '<span class="pill sm hidden" id="tf-ws-st"></span></span>' +
      '<div class="in-group ws-in"><input name="workspace" class="mono" spellcheck="false" autocomplete="off" autocapitalize="off" maxlength="1024" aria-labelledby="tf-wlbl" value="' + esc(val) + '">' +
      '<button type="button" class="icon-btn sm hidden" id="tf-ws-pick" data-tip="' + esc(T("tf.ws_pick")) + '" aria-label="' + esc(T("tf.ws_pick")) + '">' + ic("folder", "sm") + "</button>" +
      '<button type="button" class="icon-btn sm' + (val ? "" : " hidden") + '" id="tf-ws-reset" data-tip="' + esc(T("tf.ws_reset")) + '" aria-label="' + esc(T("tf.ws_reset")) + '">' + ic("rollback", "sm") + "</button></div></div>";
  }
  function wireWsField(root, input, taskId) {
    var pill = $("#tf-ws-st", root), reset = $("#tf-ws-reset", root), pick = $("#tf-ws-pick", root), def = "", problem = "", seq = 0, timer = 0;
    var paint = function (r) {
      problem = r.problem || "";
      var cls = "", txt = "", tipTxt = "";
      if (problem) { cls = "bad"; txt = T("tf.ws_bad"); tipTxt = T("tf.ws_err_" + problem); }
      else if (r.path && !r.exists) { cls = "info"; txt = T("tf.ws_new"); tipTxt = T("tf.ws_new_tip"); }
      pill.className = "pill sm " + (cls || "hidden");
      pill.textContent = txt;
      if (tipTxt) pill.setAttribute("data-tip", tipTxt); else pill.removeAttribute("data-tip");
      if (r.default) { def = r.default; input.placeholder = T("tf.ws_default_ph", { p: def }); input.title = input.value.trim() ? "" : def; }
      reset.classList.toggle("hidden", !input.value.trim());
    };
    var check = function () {
      var my = ++seq;
      api("/api/tasks/workspace-check?path=" + enc(input.value.trim()) + (taskId ? "&id=" + enc(taskId) : "")).then(function (r) { if (my === seq) paint(r || {}); }).catch(function () { /* keep last state */ });
    };
    var set = function (v) { input.value = v; input._tbTouched = true; clearTimeout(timer); check(); };
    input.addEventListener("input", function () { reset.classList.toggle("hidden", !input.value.trim()); clearTimeout(timer); timer = setTimeout(check, 250); });
    reset.addEventListener("click", function () { set(""); input.focus(); });
    folderPickable().then(function (ok) {
      if (!ok || !root.contains(pick)) return;
      pick.classList.remove("hidden");
      pick.addEventListener("click", function () {
        pick.disabled = true;
        var start = input.value.trim() || (taskId ? def : "");
        api("/api/desktop/pick-folder", { method: "POST", body: { start: start } }).then(function (r) { if (r && r.path) set(r.path); })
          .catch(fail).then(function () { pick.disabled = false; });
      });
    });
    check();
    return { problem: function () { return problem; } };
  }
  // Retries & missed runs (task_policies), folded under the form like Uptime Kuma's
  // "Retries" block: the folded line says what the current choices mean, the long
  // explanations sit in tooltips. Tools / side effects are not edited here.
  var GRACE_MIN = [60, 360, 720, 1440, 4320, 10080];
  var TF_PRIOS = ["urgent", "normal", "background"];
  function graceLabel(m) { return m % 1440 === 0 ? T("tf.grace_d", { n: m / 1440 }) : m % 60 === 0 ? T("tf.grace_h", { n: m / 60 }) : T("tf.grace_m", { n: m }); }
  function policySummary(p) {
    var parts = [p.retry_max > 0 ? T("tf.sum_retry", { n: p.retry_max }) : T("tf.sum_noretry"), T(p.catchup === "skip" ? "tf.sum_skip" : p.catchup === "run_all" ? "tf.sum_all" : "tf.sum_once")];
    if (p.priority === "urgent" || p.priority === "background") parts.push(T("tf.sum_pr_" + p.priority));
    if (p.notify_success) parts.push(T(p.notify_answer === false ? "tf.sum_notify" : "tf.sum_notify_ans"));
    if (p.notify_success && p.notify_changed) parts.push(T("tf.sum_changed"));
    return parts.join(" · ");
  }
  function policyFieldsHtml(p, streak, brkAt) {
    var graces = GRACE_MIN.indexOf(p.catchup_grace_minutes) < 0 ? GRACE_MIN.concat([p.catchup_grace_minutes]).sort(function (a, b) { return a - b; }) : GRACE_MIN;
    var retries = [0, 1, 2, 3, 4, 5].map(function (n) { return opt(n, n ? T("tf.retry_n", { n: n }) : T("tf.retry_0"), n === p.retry_max); }).join("");
    var warn = streak > 0;
    return '<details class="sec-adv tf-pol"' + (warn ? " open" : "") + '><summary><span class="tf-pol-t">' + esc(T("tf.pol")) + "</span>" + tip(T("tf.pol_tip", { n: brkAt })) +
      '<span class="tf-pol-sum" id="tf-pol-sum">' + esc(policySummary(p)) + "</span></summary>" +
      (warn ? '<p class="notice warn tf-pol-streak">' + ic("info", "sm") + esc(T("tf.streak", { n: streak, m: Math.max(1, brkAt - streak) })) + "</p>" : "") +
      '<div class="form-row three">' + fieldHtml(T("tf.retry"), '<select name="retry_max">' + retries + "</select>", T("tf.retry_tip")) +
      fieldHtml(T("tf.catchup"), '<select name="catchup">' + opt("skip", T("tf.cu_skip"), p.catchup === "skip") + opt("run_once", T("tf.cu_once"), p.catchup === "run_once") + opt("run_all", T("tf.cu_all"), p.catchup === "run_all") + "</select>", T("tf.catchup_tip")) +
      fieldHtml(T("tf.grace"), '<select name="catchup_grace_minutes"' + (p.catchup === "skip" ? " disabled" : "") + ">" + graces.map(function (m) { return opt(m, graceLabel(m), m === p.catchup_grace_minutes); }).join("") + "</select>", T("tf.grace_tip")) + "</div>" +
      // Priority decides what waits on battery (Settings › General › Power); the
      // option says it in plain words, the tooltip has the rule and the setting.
      '<div class="form-row">' + fieldHtml(T("tf.prio"), '<select name="priority">' + TF_PRIOS.map(function (k) { return opt(k, T("tf.pr_" + k), k === (p.priority || "normal")); }).join("") + "</select>", T("tf.prio_tip", { n: 20 })) +
      // The success notice may carry the start of the final answer (on unless turned off) and
      // may be skipped when the answer is the same as last time (digest / watch tasks): indented
      // lines under it, so the group reads as one setting; greyed while success notices are off.
      '<div class="tf-pol-checks"><label class="check tf-pol-ok"><input type="checkbox" name="notify_success"' + (p.notify_success ? " checked" : "") + "> " + esc(T("tf.notify_ok")) + tip(T("tf.notify_ok_tip")) + "</label>" +
      '<label class="check tf-pol-ans"><input type="checkbox" name="notify_answer"' + (p.notify_answer !== false ? " checked" : "") + (p.notify_success ? "" : " disabled") + "> " + esc(T("tf.notify_ans")) + tip(T("tf.notify_ans_tip")) + "</label>" +
      '<label class="check tf-pol-ans"><input type="checkbox" name="notify_changed"' + (p.notify_changed ? " checked" : "") + (p.notify_success ? "" : " disabled") + "> " + esc(T("tf.notify_changed")) + tip(T("tf.notify_changed_tip")) + "</label></div></div></details>";
  }
  function readPolicyFields(f) {
    return { retry_max: parseInt(f.retry_max.value, 10) || 0, catchup: f.catchup.value, catchup_grace_minutes: parseInt(f.catchup_grace_minutes.value, 10) || 360, notify_success: f.notify_success.checked, notify_answer: f.notify_answer.checked, notify_changed: f.notify_changed.checked, priority: f.priority.value };
  }
  function wirePolicyFields(root, f) {
    var sum = $("#tf-pol-sum", root);
    var sync = function () {
      f.catchup_grace_minutes.disabled = f.catchup.value === "skip";
      [f.notify_answer, f.notify_changed].forEach(function (c) { c.disabled = !f.notify_success.checked; c.parentNode.classList.toggle("off", c.disabled); });
      sum.textContent = policySummary(readPolicyFields(f));
    };
    ["retry_max", "catchup", "notify_success", "notify_answer", "notify_changed", "priority"].forEach(function (n) { f[n].addEventListener("change", sync); });
    sync();
  }
  function costModeLabel(mode) { return T("costmode." + (mode || "task")); }
  function costModeBadge(mode) { return '<span class="pill ' + (mode === "bypass" ? 'warn' : 'plain') + '">' + esc(costModeLabel(mode)) + '</span>'; }
  function costFieldsHtml(cp, currency, daily, run) {
    cp = cp || { mode: "task", run_amount: run, daily_amount: daily };
    var mode = cp.mode || "task", c = cp.currency || currency || "USD";
    return '<section class="cost-policy"><label class="check"><input type="checkbox" name="cost_enabled"' + (mode === "task" ? ' checked' : '') + '> <strong>' + esc(T("costmode.title")) + '</strong></label>' +
      '<div class="form-row cost-limits">' + fieldHtml(T("costmode.run", {c:c}), '<input name="run_amount" type="number" min="0" max="10000" step="0.0001" value="' + esc(cp.run_amount == null ? '' : cp.run_amount) + '" placeholder="' + esc(T("tf.no_cap")) + '">') +
      fieldHtml(T("tf.budget_c", {c:c}), '<input name="budget_usd" type="number" min="0" max="10000" step="0.0001" value="' + esc(cp.daily_amount == null ? '' : cp.daily_amount) + '" placeholder="' + esc(T("tf.no_cap")) + '">', '', '<span class="af-pill hidden" id="af-budget"></span>') + '</div>' +
      '<p class="hint">' + esc(T("costmode.retained")) + '</p><fieldset class="cost-choices"><legend>' + esc(T("costmode.when_off")) + '</legend>' +
      ['global','bypass'].map(function(m){return '<label class="cost-choice"><input type="radio" name="cost_off_mode" value="'+m+'"'+((mode===m || (mode==='task' && m==='global'))?' checked':'')+'><span><strong>'+esc(costModeLabel(m))+'</strong><small>'+esc(T("costmode."+m+"_hint"))+'</small></span></label>';}).join('') + '</fieldset>' +
      '<p class="notice warn cost-warning">' + esc(T("costmode.warning")) + '</p><p class="hint">' + esc(T("costmode.next_start")) + '</p></section>';
  }
  // Inline confirmation preserves the form draft; the global modal helper replaces it.
  function confirmCostBypass(root, name) {
    return new Promise(function(resolve){
      var bar=document.createElement("section");bar.className="notice warn";bar.setAttribute("role","alertdialog");bar.setAttribute("aria-label",T("costmode.confirm_title",{name:name}));
      bar.innerHTML='<h3>'+esc(T("costmode.confirm_title",{name:name}))+'</h3><p>'+esc(T("costmode.warning"))+'</p><div class="actions"><button type="button" class="btn cost-back">'+esc(T("btn.cancel"))+'</button><button type="button" class="btn danger-solid cost-ok">'+esc(T("costmode.confirm_ok"))+'</button></div>';
      (root.querySelector("form") || root).appendChild(bar);
      function finish(ok){bar.remove();resolve(ok);}
      $(".cost-back",bar).addEventListener("click",function(){finish(false);});$(".cost-ok",bar).addEventListener("click",function(){finish(true);});$(".cost-back",bar).focus();
    });
  }
  function wireCostFields(root, f) {
    var choices = $(".cost-choices",root), warning = $(".cost-warning",root);
    function mode(){return f.cost_enabled.checked ? "task" : f.cost_off_mode.value;}
    function sync(){ choices.hidden=f.cost_enabled.checked; f.run_amount.disabled=f.budget_usd.disabled=!f.cost_enabled.checked; warning.hidden=mode()!=="bypass"; }
    f.cost_enabled.addEventListener("change",function(){if(!f.cost_enabled.checked) f.cost_off_mode.value="global";sync();});
    $$('[name="cost_off_mode"]',root).forEach(function(x){x.addEventListener("change",sync);});sync();return mode;
  }
  // preset {prompt, provider}: "Save as task" from a quick run; autofill then infers name, schedule and budget.
  function taskFormModal(taskId, preset) {
    var reqs = [api("/api/skills"), api("/api/settings"), api("/api/tasks/tags").catch(function () { return []; })];
    if (taskId) reqs.push(api("/api/tasks/" + enc(taskId) + "/form"));
    Promise.all(reqs).then(function (r) {
      var inUse = r[2] || [];
      r.splice(2, 1);
      var skills = r[0] || [], st = r[1], pre = preset || {}, tf = taskId ? r[2].form : { name: "", prompt: pre.prompt || "", skill_id: pre.skill_id || "", skill_bindings: pre.skill_bindings, skill_set_source: cleanSetSource(pre.skill_set_source), skill_version_id: "", workspace: pre.workspace || "", provider: pre.provider || smartProvider(st), cron: "0 9 * * *", budget_usd: 0, enabled: true, timeout_minutes: 0 };
      // New tasks start from policy.Default (internal/policy): 2 retries, catch up once within 6 h.
      var pol = tf.policy || { retry_max: 2, catchup: "run_once", catchup_grace_minutes: 360, notify_success: false, notify_answer: true, notify_changed: false, priority: "normal" }, streak = taskId ? r[2].fail_streak || 0 : 0, brkAt = (taskId && r[2].breaker_at) || 3;
      var nameF = fieldHtml(T("tf.name"), '<input name="name" required maxlength="80" value="' + esc(tf.name) + '" placeholder="' + esc(T(taskId ? "tf.name_ph" : "af.name_ph")) + '">', "", taskId ? "" : '<span class="af-pill hidden" id="af-name" data-tip="' + esc(T("af.name_tip")) + '">' + ic("sparkle", "sm") + esc(T("af.auto")) + "</span>");
      var drafty = !taskId && !pre.prompt;
      var promptF = '<div class="field"><span class="qr-lbl"><span id="tf-plbl">' + esc(T("tf.prompt")) + "</span>" + (drafty ? draftTagHtml() : "") + '</span><textarea name="prompt" aria-labelledby="tf-plbl" required maxlength="32000" rows="' + (taskId ? 5 : 3) + '" placeholder="' + esc(T(taskId ? "tf.prompt_ph" : "af.prompt_ph")) + '">' + esc(tf.prompt) + "</textarea>" + (taskId ? "" : '<span class="af-note" id="af-note">' + ic("sparkle", "sm") + "<span>" + esc(T("af.hint")) + "</span></span>") + "</div>";
      modal('<div class="card-head"><span class="ico">' + ic("tasks") + "</span><h2>" + esc(T(taskId ? "tf.edit" : "tf.new")) + '</h2></div><form id="tf" class="tf">' +
        (taskId ? nameF + promptF : promptF + nameF) +
        '<div class="field"><span>' + esc(T("tg.label")) + tip(T("tg.hint")) + '</span><div id="tg-host"></div></div>' +
        '<input type="hidden" name="skill_id"><input type="hidden" name="skill_version_id"><div id="tf-bindings"></div>' +
        fieldHtml(T("f.provider"), '<select name="provider">' + providerOptions(st, tf.provider, !!taskId || !!pre.provider, true) + "</select>") +
        '<div class="field"><span>' + esc(T("tf.schedule")) + '</span><div id="sched-host"></div></div>' +
        costFieldsHtml(tf.cost_control, (st.fx || {}).display || state.currency, tf.budget_usd || null, (st.fx || {}).display === "CNY" ? 0.05 * st.fx.usd_cny : 0.05) +
        '<label class="check"><input type="checkbox" name="timeout_enabled"' + (tf.timeout_minutes > 0 ? ' checked' : '') + '> ' + esc(T("tf.timeout_enabled")) + '</label><div id="tf-timeout"' + (tf.timeout_minutes > 0 ? '' : ' class="hidden"') + '>' +
        fieldHtml(T("tf.timeout"), '<input name="timeout_minutes" type="number" min="1" max="1440" value="' + esc(tf.timeout_minutes > 0 ? tf.timeout_minutes : "") + '">', T("tf.timeout_hint")) + "</div>" +
        wsFieldHtml(tf.workspace || "") + policyFieldsHtml(pol, streak, brkAt) +
        '<label class="check"><input type="checkbox" name="enabled"' + (tf.enabled ? " checked" : "") + "> " + esc(T("tf.task_enabled")) + "</label>" + (taskId && !tf.enabled ? '<p class="hint">' + esc(T("costmode.paused")) + '</p>' : "") +
        '<div class="actions"><button type="button" class="btn" id="tf-dependencies">' + esc(T("ss.dependencies")) + '</button><button type="button" class="btn ghost" data-close>' + esc(T("btn.cancel")) + '</button><button class="btn primary">' + esc(T(taskId ? "btn.save" : "tf.create")) + "</button></div></form>", function (root, close) {
        var f = $("#tf", root);
        var syncTimeout = function () {
          var enabled = f.timeout_enabled.checked;
          $("#tf-timeout", root).classList.toggle("hidden", !enabled);
          f.timeout_minutes.disabled = !enabled;
          f.timeout_minutes.required = enabled;
        };
        f.timeout_enabled.addEventListener("change", syncTimeout);
        syncTimeout();
        var initial=tf.skill_bindings||(tf.skill_id?[{skill_id:tf.skill_id,mode:tf.skill_version_id?"pinned":"follow",version_id:tf.skill_version_id||"",role:"primary"}]:[]);
        var editor=skillBindingEditor($("#tf-bindings",root),skills,initial,function(bs){f.skill_id.value=bs.length?bs[0].skill_id:"";f.skill_version_id.value=bs.length?bs[0].version_id:"";f.skill_id.dispatchEvent(new Event("change",{bubbles:true}));},{source:tf.skill_set_source,checkRequest:function(){return {kind:"draft",execution:"task",id:taskId||"",workspace:f.workspace.value.trim(),prompt:f.prompt.value,provider:f.provider.value,skill_bindings:editor.value()};}});
        var first=editor.value()[0];f.skill_id.value=first?first.skill_id:"";f.skill_version_id.value=first?first.version_id:"";
        $("#tf-dependencies",root).addEventListener("click",function(){dependencyCheck({kind:"draft",execution:"task",id:taskId||"",prompt:f.prompt.value,provider:f.provider.value,skill_bindings:editor.value(),workspace:f.workspace.value.trim()});});
        var se = scheduleEditor($("#sched-host", root), tf.cron, state.tz);
        var tgs = tagInput($("#tg-host", root), tf.tags || pre.tags || [], inUse);
        if (drafty) wireDraft(root, f.prompt, "task");
        if (!taskId) wireTaskAutofill(root, f, se, skills);
        var ws = wireWsField(root, f.workspace, taskId);
        wirePolicyFields(root, f);
        var readCostMode = wireCostFields(root, f);
        f.addEventListener("submit", function (e) {
          e.preventDefault();
          if (!taskId && !readyProviders(st).some(function (p) { return p.name === f.provider.value; })) { toast(T("cc.no_provider"), true); return; }
          if (!se.valid()) { $("#se-err", root).scrollIntoView({ block: "center" }); return; }
          if (ws.problem()) { f.workspace.focus(); toast(T("tf.ws_err_" + ws.problem()), true); return; }
          var body = { name: f.name.value.trim(), prompt: f.prompt.value, skill_id: f.skill_id.value, skill_version_id: f.skill_version_id.value, skill_bindings: editor.value(), skill_set_source: editor.source(), expected_version_id: taskId ? tf.expected_version_id : undefined, provider: f.provider.value, cron: se.value(),
            budget_usd: parseFloat(f.budget_usd.value) || 0, enabled: f.enabled.checked, timeout_minutes: f.timeout_enabled.checked ? Number(f.timeout_minutes.value) : 0, workspace: f.workspace.value.trim(), policy: readPolicyFields(f), tags: tgs.value() };
          body.cost_control = {mode:readCostMode(), run_amount:f.run_amount.value === "" ? null : Number(f.run_amount.value), daily_amount:f.budget_usd.value === "" ? null : Number(f.budget_usd.value), currency:(tf.cost_control || {}).currency || (st.fx || {}).display || "USD", revision:(tf.cost_control || {}).revision || 0, fx_revision:(tf.cost_control || {}).fx_revision || st.fx_revision || ""};
          var btn = f.querySelector(".btn.primary"); btn.disabled = true;
          var consent = body.cost_control.mode === "bypass" && (!tf.cost_control || tf.cost_control.mode !== "bypass") ? confirmCostBypass(root,body.name) : Promise.resolve(true);
          consent.then(function(ok){ if(!ok){btn.disabled=false;return;} return api(taskId ? "/api/tasks/" + enc(taskId) : "/api/tasks", { method: taskId ? "PUT" : "POST", body: body }).then(function (t) {
            if (drafty) draftClear("task");
            close(); toast(T(taskId ? "tf.saved" : "tf.created"));
            if (taskId) refresh(); else location.hash = "#/tasks/" + enc(t.id);
          }).catch(function (err) { btn.disabled = false; fail(err); }); });
        });
      });
    }).catch(fail);
  }

