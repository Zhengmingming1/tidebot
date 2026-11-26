#!/usr/bin/env python3
"""Find hover tooltips ([data-tip]::after) that are cut off at 1024x655.

Needs a running demo agentd (scripts/demo-webui.sh) and Playwright:
  python3 scripts/tip-check.py [base_url] [password]
For every visible [data-tip] element on the main pages (zh-CN and en) it hovers
the element and checks the tooltip: the fixed #tip-layer must show the text and
fit the window; without that layer (older builds) the ::after box is worked out
from the computed style and checked against the viewport and every ancestor that
clips overflow. Exit code 1 if any tooltip loses more than 2 px.
"""
import sys
from playwright.sync_api import sync_playwright

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:17421"
PW = sys.argv[2] if len(sys.argv) > 2 else "demo-pass-99"
W, H = 1024, 655

# Box of el's ::after (absolute, el is its containing block) and how much of it
# is hidden by the viewport or by an ancestor whose overflow is not visible.
MEASURE = """
el => {
  const sel = el.tagName.toLowerCase() + (el.className && typeof el.className === 'string' ? '.' + el.className.trim().split(/\\s+/).join('.') : '');
  const want = el.getAttribute('data-tip').slice(0, 50);
  if (document.documentElement.classList.contains('tip-js')) {
    // the WebUI shows tips in one fixed layer: it must be up, carry this text and fit the window
    if (el.getAttribute('aria-expanded') === 'true') return null;
    const L = document.getElementById('tip-layer');
    if (!L || L.hidden || L.textContent !== el.getAttribute('data-tip')) return {sel, tip: want, box: [], why: 'layer not shown', cut: 0};
    const b = L.getBoundingClientRect();
    const cut = Math.max(0, -b.left) + Math.max(0, b.right - innerWidth) + Math.max(0, -b.top) + Math.max(0, b.bottom - innerHeight);
    return cut > 2 ? {sel, tip: want, box: [b.left, b.top, b.right, b.bottom].map(Math.round), why: 'viewport', cut: Math.round(cut)} : null;
  }
  const cs = getComputedStyle(el, '::after');
  if (!cs || cs.content === 'none' || cs.content === 'normal' || cs.display === 'none') return null;
  if (cs.position !== 'absolute' && cs.position !== 'fixed') return null;
  const px = v => parseFloat(v) || 0;
  const r = el.getBoundingClientRect(), ecs = getComputedStyle(el);
  const w = px(cs.width), h = px(cs.height);
  let x, y;
  if (cs.position === 'fixed') { x = px(cs.left); y = px(cs.top); }
  else {
    const pl = r.left + px(ecs.borderLeftWidth), pr = r.right - px(ecs.borderRightWidth);
    const pt = r.top + px(ecs.borderTopWidth), pb = r.bottom - px(ecs.borderBottomWidth);
    x = cs.left !== 'auto' ? pl + px(cs.left) : pr - px(cs.right) - w;
    y = cs.top !== 'auto' ? pt + px(cs.top) : pb - px(cs.bottom) - h;
  }
  if (cs.transform && cs.transform !== 'none') {
    const m = new DOMMatrix(cs.transform); x += m.e; y += m.f;
  }
  let box = {l: x, t: y, r: x + w, b: y + h};
  const lost = (c, why) => {
    const cut = Math.max(0, c.l - box.l) + Math.max(0, box.r - c.r) + Math.max(0, c.t - box.t) + Math.max(0, box.b - c.b);
    return cut > 2 ? {why, cut: Math.round(cut)} : null;
  };
  let hit = lost({l: 0, t: 0, r: innerWidth, b: innerHeight}, 'viewport');
  if (cs.position !== 'fixed') {
    for (let a = el.parentElement; a && a !== document.documentElement && !hit; a = a.parentElement) {
      const acs = getComputedStyle(a);
      if (acs.overflowX !== 'visible' || acs.overflowY !== 'visible') {
        const ar = a.getBoundingClientRect();
        // a scroll container only clips along the axes it does not let overflow
        const c = {l: -1e9, t: -1e9, r: 1e9, b: 1e9};
        if (acs.overflowX !== 'visible') { c.l = ar.left + a.clientLeft; c.r = c.l + a.clientWidth; }
        if (acs.overflowY !== 'visible') { c.t = ar.top + a.clientTop; c.b = c.t + a.clientHeight; }
        const why = (a.id ? '#' + a.id : a.tagName.toLowerCase()) + (a.className && typeof a.className === 'string' ? '.' + a.className.trim().split(/\\s+/).join('.') : '');
        hit = lost(c, why);
      }
      if (acs.position === 'fixed') break;
    }
  }
  if (!hit) return null;
  return {sel, tip: want, box: [Math.round(box.l), Math.round(box.t), Math.round(box.r), Math.round(box.b)], ...hit};
}
"""

def main():
    bad = 0
    with sync_playwright() as p:
        br = p.chromium.launch(channel="chrome")  # system Chrome; drop channel to use Playwright's own
        for loc in ["zh-CN", "en"]:
            pg = br.new_context(viewport={"width": W, "height": H}, locale=loc).new_page()
            pg.goto(BASE + "/")
            pg.wait_for_selector("#login-form")
            pg.fill("#lp", PW)
            pg.click("#login-form button[type=submit]")
            pg.wait_for_selector("#shell:not(.hidden) #page", state="attached"); pg.wait_for_timeout(800)
            j = lambda u: pg.evaluate("u => fetch(u).then(r => r.json())", u)
            tasks = j("/api/tasks"); tasks = tasks.get("tasks", tasks) if isinstance(tasks, dict) else tasks
            runs = j("/api/runs"); runs = runs.get("runs", runs) if isinstance(runs, dict) else runs
            skills = j("/api/skills"); skills = skills.get("skills", skills) if isinstance(skills, dict) else skills
            pages = ["#/", "#/tasks", "#/runs", "#/skills", "#/workflows", "#/chat", "#/plugins", "#/spend", "#/buttons"]
            pages += ["#/settings/" + s for s in ["general", "providers", "budgets", "security", "notifications", "data", "about"]]
            if tasks: pages += ["#/tasks/" + tasks[-1]["id"]]
            if runs: pages += ["#/runs/" + runs[0]["id"]]
            if skills: pages += ["#/skills/" + skills[0]["id"]]
            for h in pages:
                pg.evaluate("h => { location.hash = h }", h)
                pg.wait_for_timeout(900)
                n = pg.evaluate("document.querySelectorAll('#page [data-tip], #shell [data-tip]').length")
                seen = set()
                for i in range(min(n, 200)):
                    el = pg.locator("#page [data-tip], #shell [data-tip]").nth(i)
                    try:
                        if not el.is_visible():
                            continue
                        el.hover(timeout=800)
                    except Exception:
                        continue
                    pg.wait_for_timeout(30)
                    res = el.evaluate(MEASURE)
                    if res:
                        key = (res["sel"], res["why"])
                        if key in seen:
                            continue
                        seen.add(key)
                        bad += 1
                        print(f"{loc} {h} {res['sel']} cut {res['cut']}px by {res['why']} box={res['box']} tip={res['tip']!r}")
                pg.mouse.move(1, 1)
            pg.context.close()
        br.close()
    print(f"{bad} clipped tooltip(s)")
    sys.exit(1 if bad else 0)

if __name__ == "__main__":
    main()
