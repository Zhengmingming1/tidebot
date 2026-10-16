#!/usr/bin/env python3
"""Measure blank/skeleton frames and layout shift while switching pages and tabs.

Needs a running demo agentd (scripts/demo-webui.sh) and Playwright:
  python3 scripts/flicker-check.py [base_url] [password]
For every switch it records, frame by frame, whether #page showed a skeleton or
nothing at all, plus the CLS (layout-shift) accumulated during the switch.
Exit code 1 if any switch shows a blank/skeleton frame or CLS > 0.02.
"""
import json, sys, time
from playwright.sync_api import sync_playwright

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:17421"
PW = sys.argv[2] if len(sys.argv) > 2 else "demo-pass-99"

PROBE = """
window.__fl = {frames: [], cls: 0};
new PerformanceObserver(l => { for (const e of l.getEntries()) if (!e.hadRecentInput) window.__fl.cls += e.value; })
  .observe({type: 'layout-shift', buffered: false});
(function tick() {
  const pg = document.getElementById('page');
  let s = 'ok';
  if (!pg || !pg.firstElementChild) s = 'blank';
  else if (pg.querySelector('.skel')) s = 'skeleton';
  window.__fl.frames.push(s);
  requestAnimationFrame(tick);
})();
"""

def main():
    with sync_playwright() as p:
        br = p.chromium.launch()
        pg = br.new_context(viewport={"width": 1440, "height": 900}, locale="zh-CN").new_page()
        pg.goto(BASE + "/")
        pg.wait_for_selector("#login-form")
        pg.fill("#lp", PW)
        pg.click("#login-form button[type=submit]")
        pg.wait_for_selector(".qa")
        tasks = pg.evaluate("fetch('/api/tasks').then(r=>r.json())")
        tasks = tasks.get("tasks", tasks) if isinstance(tasks, dict) else tasks
        tid = tasks[0]["id"] if tasks else None
        skills = pg.evaluate("fetch('/api/skills').then(r=>r.json())")
        skills = skills.get("skills", skills) if isinstance(skills, dict) else skills
        sid = skills[0]["id"] if skills else None
        steps = [("hash", h) for h in ["#/tasks", "#/skills", "#/runs", "#/plugins", "#/spend", "#/buttons", "#/"]]
        steps += [("hash", "#/settings/" + s) for s in ["general", "providers", "budgets", "security", "notifications", "data", "about", "general"]]
        if tid:
            steps += [("hash", "#/tasks/" + tid)] + [("tab", t) for t in ["thread", "runs", "history", "overview"]]
        if sid:
            steps += [("hash", "#/skills/" + sid)]
        steps += [("hash", h) for h in ["#/tasks", "#/runs", "#/", "#/tasks", "#/runs", "#/"]]  # second visits
        pg.evaluate(PROBE)
        time.sleep(0.5)
        rows, bad = [], 0
        for kind, target in steps:
            pg.evaluate("window.__fl.frames = []; window.__fl.cls = 0")
            if kind == "hash":
                pg.evaluate("h => { location.hash = h }", target)
            else:
                pg.click(f'[data-tab="{target}"]')
            time.sleep(1.0)
            fl = pg.evaluate("window.__fl")
            blank = sum(1 for f in fl["frames"] if f != "ok")
            label = target if kind == "hash" else "tab:" + target
            rows.append((label, blank, round(fl["cls"], 4)))
            if blank or fl["cls"] > 0.02:
                bad += 1
        br.close()
    w = max(len(r[0]) for r in rows)
    print(f"{'switch'.ljust(w)}  blank/skeleton frames  CLS")
    for r in rows:
        print(f"{r[0].ljust(w)}  {str(r[1]).rjust(21)}  {r[2]}")
    tot = sum(r[1] for r in rows)
    print(f"\n{len(rows)} switches, {bad} with flicker, {tot} blank/skeleton frames total")
    sys.exit(1 if bad else 0)

if __name__ == "__main__":
    main()
