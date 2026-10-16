(function () {
  const $ = (sel) => document.querySelector(sel);
  const page = $("#page");
  const nav = $("#nav");
  const loginView = $("#login-view");
  let csrf = "";
  let sse = null;
  let idleTimer = null;
  const IDLE_MS = 30 * 60 * 1000;

  function esc(s) {
    return String(s == null ? "" : s)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  async function api(path, opts = {}) {
    const headers = Object.assign({ "Accept": "application/json" }, opts.headers || {});
    if (opts.method && opts.method !== "GET" && csrf) {
      headers["X-CSRF-Token"] = csrf;
    }
    if (opts.body && typeof opts.body === "object" && !(opts.body instanceof FormData)) {
      headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(opts.body);
    }
    const res = await fetch(path, Object.assign({}, opts, { headers, credentials: "same-origin" }));
    if (res.status === 401) {
      showLogin();
      throw new Error("unauthorized");
    }
    const ct = res.headers.get("content-type") || "";
    const data = ct.includes("json") ? await res.json() : await res.text();
    if (!res.ok) {
      const msg = (data && data.error) || res.statusText;
      throw new Error(msg);
    }
    return data;
  }

  function showLogin() {
    nav.classList.add("hidden");
    page.classList.add("hidden");
    loginView.classList.remove("hidden");
    stopSSE();
  }

  function showApp() {
    loginView.classList.add("hidden");
    nav.classList.remove("hidden");
    page.classList.remove("hidden");
    bumpIdle();
  }

  function bumpIdle() {
    if (idleTimer) clearTimeout(idleTimer);
    idleTimer = setTimeout(() => { stopSSE(); }, IDLE_MS);
  }
  ["mousemove", "keydown", "click"].forEach((e) => document.addEventListener(e, bumpIdle, { passive: true }));

  function stopSSE() {
    if (sse) { sse.close(); sse = null; }
  }

  async function refreshMe() {
    try {
      const me = await api("/api/me");
      csrf = me.csrf_token || "";
      if (me.totp_enabled) $(".totp-row").classList.remove("hidden");
      showApp();
      route();
      return true;
    } catch (_) {
      showLogin();
      return false;
    }
  }

  $("#login-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    $("#login-error").textContent = "";
    try {
      const body = {
        password: $("#login-password").value,
        totp: $("#login-totp").value,
      };
      const me = await api("/api/login", { method: "POST", body });
      csrf = me.csrf_token || "";
      showApp();
      location.hash = "#/";
      route();
    } catch (e) {
      $("#login-error").textContent = e.message || "登录失败";
    }
  });

  $("#btn-logout").addEventListener("click", async () => {
    try { await api("/api/logout", { method: "POST", body: {} }); } catch (_) {}
    csrf = "";
    showLogin();
  });

  function setActive(name) {
    nav.querySelectorAll("a").forEach((a) => {
      a.classList.toggle("active", a.dataset.page === name);
    });
  }

  async function renderHome() {
    setActive("home");
    page.innerHTML = `
      <h1>首页</h1>
      <div class="card grid btn-panel">
        <button type="button" data-go="#/tasks">任务</button>
        <button type="button" data-go="#/skills">技能</button>
        <button type="button" data-go="#/runs">运行</button>
        <button type="button" data-go="#/spend">花费</button>
        <button type="button" data-go="#/settings">设置</button>
      </div>
      <p class="muted">单用户本地控制台 · WebUI 可运行时开关</p>`;
    page.querySelectorAll("[data-go]").forEach((b) => {
      b.addEventListener("click", () => { location.hash = b.dataset.go; });
    });
  }

  async function renderTasks() {
    setActive("tasks");
    const tasks = await api("/api/tasks");
    const rows = (tasks || []).map((t) => {
      const id = t.Task ? t.Task.ID : t.id;
      const name = t.Task ? t.Task.Name : (t.name || id);
      const status = t.Task ? t.Task.Status : (t.status || "");
      return `<tr><td><a href="#/tasks/${esc(id)}">${esc(name)}</a></td><td>${esc(status)}</td><td class="muted">${esc(id)}</td></tr>`;
    }).join("");
    page.innerHTML = `
      <h1>任务</h1>
      <div class="card"><table><thead><tr><th>名称</th><th>状态</th><th>ID</th></tr></thead>
      <tbody>${rows || '<tr><td colspan="3" class="muted">暂无任务</td></tr>'}</tbody></table></div>`;
  }

  async function renderTaskDetail(id) {
    setActive("tasks");
    const t = await api("/api/tasks/" + encodeURIComponent(id));
    const amends = await api("/api/tasks/" + encodeURIComponent(id) + "/amendments").catch(() => []);
    const vers = await api("/api/tasks/" + encodeURIComponent(id) + "/versions").catch(() => ({ versions: [] }));
    const name = t.Task ? t.Task.Name : id;
    const status = t.Task ? t.Task.Status : "";
    const bubbles = (amends || []).map((a) => `
      <div class="bubble">
        <div class="meta">${esc(a.Status)} · ${esc(a.CreatedAt || "")}</div>
        <div>${esc(a.Content || a.content || "")}</div>
        <div class="row" style="margin-top:.5rem">
          ${a.Status === "pending" ? `
            <button class="primary" data-act="confirm" data-id="${esc(a.ID)}">确认</button>
            <button class="danger" data-act="reject" data-id="${esc(a.ID)}">拒绝</button>` : ""}
          ${a.Status === "needs_decision" ? `<button class="ghost" data-act="resolve" data-id="${esc(a.ID)}">解决冲突</button>` : ""}
        </div>
      </div>`).join("");
    const vrows = ((vers && vers.versions) || []).map((v) =>
      `<tr><td>${esc(v.ID || v.id)}</td><td>${esc(v.CreatedAt || "")}</td>
       <td><button class="ghost" data-rb="${esc(v.ID || v.id)}">回滚</button></td></tr>`).join("");
    page.innerHTML = `
      <h1>${esc(name)}</h1>
      <p class="muted">${esc(id)} · ${esc(status)}</p>
      <div class="card"><h2>修订（聊天式）</h2><div class="chat">${bubbles || '<p class="muted">暂无修订</p>'}</div>
        <form id="amend-form" class="row" style="margin-top:1rem">
          <input name="text" placeholder="用自然语言提出修订…" style="flex:1"/>
          <button class="primary" type="submit">提出</button>
        </form>
      </div>
      <div class="card"><h2>版本树</h2>
        <table><thead><tr><th>版本</th><th>时间</th><th></th></tr></thead>
        <tbody>${vrows || '<tr><td colspan="3" class="muted">无版本</td></tr>'}</tbody></table>
      </div>`;
    page.querySelectorAll("[data-act]").forEach((b) => {
      b.addEventListener("click", async () => {
        const act = b.dataset.act, aid = b.dataset.id;
        if (act === "confirm") await api("/api/amendments/" + aid + "/confirm", { method: "POST", body: {} });
        if (act === "reject") await api("/api/amendments/" + aid + "/reject", { method: "POST", body: {} });
        if (act === "resolve") {
          const res = prompt("resolution (replace|coexist):", "replace");
          if (!res) return;
          await api("/api/amendments/" + aid + "/resolve", { method: "POST", body: { resolution: res } });
        }
        renderTaskDetail(id);
      });
    });
    page.querySelectorAll("[data-rb]").forEach((b) => {
      b.addEventListener("click", async () => {
        await api("/api/tasks/" + encodeURIComponent(id) + "/rollback", { method: "POST", body: { version_id: b.dataset.rb } });
        renderTaskDetail(id);
      });
    });
    const form = $("#amend-form");
    if (form) form.addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const text = form.text.value.trim();
      if (!text) return;
      await api("/api/tasks/" + encodeURIComponent(id) + "/amendments", { method: "POST", body: { utterance: text } });
      form.reset();
      renderTaskDetail(id);
    });
  }

  async function renderSkills() {
    setActive("skills");
    const skills = await api("/api/skills");
    const rows = (skills || []).map((s) =>
      `<tr><td>${esc(s.Name || s.name)}</td><td class="muted">${esc(s.ID || s.id)}</td>
       <td><button class="ghost" data-rb-skill="${esc(s.ID || s.id)}" data-ver="${esc(s.CurrentVersionID || "")}">回滚当前</button></td></tr>`
    ).join("");
    page.innerHTML = `
      <h1>技能</h1>
      <div class="card">
        <form id="skill-upload" class="row">
          <input name="name" placeholder="名称（可选）"/>
          <input name="dir" placeholder="技能目录绝对路径" style="flex:1" required/>
          <button class="primary" type="submit">导入</button>
        </form>
      </div>
      <div class="card"><table><thead><tr><th>名称</th><th>ID</th><th></th></tr></thead>
      <tbody>${rows || '<tr><td colspan="3" class="muted">暂无技能</td></tr>'}</tbody></table></div>`;
    $("#skill-upload").addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const fd = ev.target;
      await api("/api/skills/import", { method: "POST", body: { name: fd.name.value, dir: fd.dir.value } });
      renderSkills();
    });
  }

  async function renderRuns() {
    setActive("runs");
    const runs = await api("/api/runs");
    const rows = (runs || []).map((r) =>
      `<tr><td><a href="#/runs/${esc(r.ID)}">${esc(r.ID)}</a></td><td>${esc(r.Status)}</td>
       <td>${esc(r.ProviderName)}</td><td>${esc(r.CreatedAt)}</td></tr>`).join("");
    page.innerHTML = `
      <h1>运行</h1>
      <div class="card"><table><thead><tr><th>ID</th><th>状态</th><th>Provider</th><th>时间</th></tr></thead>
      <tbody>${rows || '<tr><td colspan="4" class="muted">暂无运行</td></tr>'}</tbody></table></div>`;
  }

  async function renderRunDetail(id) {
    setActive("runs");
    page.innerHTML = `<h1>运行 ${esc(id)}</h1><div class="card"><div class="log" id="run-log">连接 SSE…</div></div>`;
    stopSSE();
    bumpIdle();
    sse = new EventSource("/api/runs/" + encodeURIComponent(id) + "/events");
    const log = $("#run-log");
    sse.onmessage = (ev) => {
      log.textContent += ev.data + "\n";
      log.scrollTop = log.scrollHeight;
      bumpIdle();
    };
    sse.onerror = () => { log.textContent += "\n[SSE closed]\n"; };
  }

  async function renderSpend() {
    setActive("spend");
    const s = await api("/api/spend");
    const buckets = Object.entries(s.by_bucket_usd || {}).map(([k, v]) =>
      `<tr><td>${esc(k)}</td><td>$${Number(v).toFixed(4)}</td><td>${esc(s.by_bucket_tokens[k] || 0)}</td></tr>`).join("");
    const buds = (s.budgets || []).map((b) =>
      `<tr><td>${esc(b.scope)}/${esc(b.scope_id || "-")}</td><td>${esc(b.period)}</td>
       <td>$${Number(b.used_usd).toFixed(4)} / $${Number(b.max_usd).toFixed(4)}</td>
       <td>${esc(b.used_tokens)} / ${esc(b.max_tokens)}</td></tr>`).join("");
    page.innerHTML = `
      <h1>花费</h1>
      <div class="card"><p>日期 <strong>${esc(s.day)}</strong> · 合计 <strong>$${Number(s.total_usd).toFixed(4)}</strong> · tokens <strong>${esc(s.total_tokens)}</strong></p></div>
      <div class="card"><h2>按桶</h2><table><thead><tr><th>桶</th><th>USD</th><th>Tokens</th></tr></thead>
      <tbody>${buckets || '<tr><td colspan="3" class="muted">今日无花费</td></tr>'}</tbody></table></div>
      <div class="card"><h2>预算</h2><table><thead><tr><th>范围</th><th>周期</th><th>USD</th><th>Tokens</th></tr></thead>
      <tbody>${buds || '<tr><td colspan="4" class="muted">无预算</td></tr>'}</tbody></table></div>`;
  }

  async function renderSettings() {
    setActive("settings");
    const st = await api("/api/settings");
    const prov = (st.providers || []).map((p) =>
      `<tr><td>${esc(p.name)}</td><td>${esc(p.base_url)}</td><td>${esc(p.model)}</td><td class="muted">${esc(p.auth_env)} (secret hidden)</td></tr>`).join("");
    page.innerHTML = `
      <h1>设置</h1>
      <div class="card">
        <h2>WebUI</h2>
        <p>当前：<strong class="${st.webui_enabled ? "ok" : "muted"}">${st.webui_enabled ? "开启" : "关闭"}</strong></p>
        <div class="row">
          <button class="primary" id="webui-on">开启</button>
          <button class="ghost" id="webui-off">关闭</button>
        </div>
        <p class="hint">关闭后本页将不可达；可用 <code>agentctl webui on</code> 重新开启。</p>
      </div>
      <div class="card">
        <h2>预算（全局日限额）</h2>
        <form id="budget-form" class="row">
          <label>Max USD <input name="max_usd" type="number" step="0.01" value="${esc(st.global_max_usd || 5)}"/></label>
          <label>Max Tokens <input name="max_tokens" type="number" value="${esc(st.global_max_tokens || 2000000)}"/></label>
          <button class="primary" type="submit">保存</button>
        </form>
      </div>
      <div class="card"><h2>Providers（不显示密钥）</h2>
        <table><thead><tr><th>名称</th><th>Base URL</th><th>Model</th><th>Auth</th></tr></thead>
        <tbody>${prov}</tbody></table>
      </div>`;
    $("#webui-on").addEventListener("click", async () => {
      await api("/api/webui", { method: "POST", body: { enabled: true } });
      renderSettings();
    });
    $("#webui-off").addEventListener("click", async () => {
      if (!confirm("关闭 WebUI？")) return;
      await api("/api/webui", { method: "POST", body: { enabled: false } });
    });
    $("#budget-form").addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const f = ev.target;
      await api("/api/budgets", { method: "POST", body: {
        scope: "global", period: "day",
        max_usd: parseFloat(f.max_usd.value),
        max_tokens: parseInt(f.max_tokens.value, 10),
      }});
      alert("已保存");
    });
  }

  async function route() {
    stopSSE();
    const hash = location.hash || "#/";
    const parts = hash.replace(/^#\/?/, "").split("/").filter(Boolean);
    try {
      if (parts.length === 0) return renderHome();
      if (parts[0] === "tasks" && parts[1]) return renderTaskDetail(parts[1]);
      if (parts[0] === "tasks") return renderTasks();
      if (parts[0] === "skills") return renderSkills();
      if (parts[0] === "runs" && parts[1]) return renderRunDetail(parts[1]);
      if (parts[0] === "runs") return renderRuns();
      if (parts[0] === "spend") return renderSpend();
      if (parts[0] === "settings") return renderSettings();
      return renderHome();
    } catch (e) {
      page.innerHTML = `<div class="card error">${esc(e.message)}</div>`;
    }
  }

  window.addEventListener("hashchange", () => { if (!loginView.classList.contains("hidden") === false) route(); else if (!page.classList.contains("hidden")) route(); });
  window.addEventListener("hashchange", route);
  refreshMe();
})();
