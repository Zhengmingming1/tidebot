#!/usr/bin/env python3
"""Mock of Claude Code for the AI plugin builder (tests and demos). Usage:
plugin_ai.py spec|build|iterate  (prompt on stdin, cwd = workspace). Prints the result text."""
import json, os, sys, re

mode = sys.argv[1]
prompt = sys.stdin.read()

SPEC = {
    "id": "github-release-watch",
    "name": "GitHub release watcher",
    "type": "trigger",
    "summary": "Checks a GitHub repository for a new release and starts the bound task with the release notes.",
    "runtime_language": "python",
    "triggers": [{"id": "new_release", "kind": "poll", "title": "New GitHub release"}],
    "tools": [],
    "permissions": {"network": ["api.github.com"], "host_api": ["log.write"], "secrets": []},
    "config_schema": {"type": "object", "required": ["repo"], "properties": {
        "repo": {"type": "string", "title": "Repository (owner/name)", "maxLength": 200, "x-order": 1},
        "interval_minutes": {"type": "integer", "title": "Check every (minutes)", "minimum": 10, "maximum": 1440, "default": 60, "x-order": 2},
        "github_token": {"type": "string", "title": "GitHub token (optional, for rate limits)", "writeOnly": True, "maxLength": 200, "x-order": 3}}},
}

YAML = """id: github-release-watch
name: GitHub release watcher
version: {version}
runtime:
  command: main.py
  lifecycle: keepalive
contributes:
  triggers:
    - id: new_release
      kind: poll
      title: New GitHub release
permissions:
  network: [api.github.com]
  host_api: [log.write]
config_schema:
  type: object
  required: [repo]
  properties:
    repo: {{type: string, title: "Repository (owner/name)", maxLength: 200, x-order: 1}}
    interval_minutes: {{type: integer, title: "Check every (minutes)", minimum: 10, maximum: 1440, default: 60, x-order: 2}}
    github_token: {{type: string, title: "GitHub token (optional, for rate limits)", writeOnly: true, maxLength: 200, x-order: 3}}
{extra}"""

MAIN = r'''#!/usr/bin/env python3
"""GitHub release watcher — a tidebot trigger plugin (JSON-RPC 2.0 over stdio)."""
import json, sys, threading, time, urllib.request

config, subs, lock, next_id = {}, {}, threading.Lock(), [0]
INCLUDE_PRERELEASES = __PRE__


def send(obj):
    with lock:
        sys.stdout.write(json.dumps(obj) + "\n")
        sys.stdout.flush()


def call(method, params):
    next_id[0] += 1
    send({"jsonrpc": "2.0", "id": next_id[0], "method": method, "params": params})


def latest(repo):
    url = "https://api.github.com/repos/%s/releases" % repo
    req = urllib.request.Request(url, headers={"Accept": "application/vnd.github+json", "User-Agent": "tidebot-plugin"})
    tok = config.get("github_token")
    if tok:
        req.add_header("Authorization", "Bearer " + tok)
    with urllib.request.urlopen(req, timeout=20) as r:
        for rel in json.load(r):
            if INCLUDE_PRERELEASES or not rel.get("prerelease"):
                return rel
    return None


def watch(binding, cfg, stop):
    seen = None
    while not stop.is_set():
        try:
            rel = latest(cfg.get("repo") or config.get("repo", ""))
            if rel and seen is not None and rel["tag_name"] != seen:
                call("trigger.fire", {"trigger_id": binding, "payload": {"tag": rel["tag_name"], "name": rel.get("name"), "url": rel.get("html_url"), "notes": (rel.get("body") or "")[:4000]}})
            if rel:
                seen = rel["tag_name"]
        except Exception as e:  # network errors are logged, never fatal
            call("log.write", {"level": "warn", "message": "poll failed: %s" % e})
        stop.wait(max(10, int(cfg.get("interval_minutes") or config.get("interval_minutes") or 60)) * 60)


def handle(msg):
    m, p = msg.get("method"), msg.get("params") or {}
    if m == "plugin.activate":
        config.update(p.get("config") or {})
        return {"ok": True}
    if m == "plugin.shutdown":
        for s in subs.values():
            s.set()
        return {"ok": True}
    if m == "trigger.subscribe":
        b = p["binding_id"]
        if b in subs:
            subs[b].set()
        subs[b] = threading.Event()
        threading.Thread(target=watch, args=(b, p.get("config") or {}, subs[b]), daemon=True).start()
        return {"ok": True}
    if m == "trigger.unsubscribe":
        s = subs.pop(p.get("binding_id"), None)
        if s:
            s.set()
        return {"ok": True}
    raise KeyError(m)


for line in sys.stdin:
    try:
        msg = json.loads(line)
    except ValueError:
        continue
    if "method" not in msg:
        continue  # answers to our own calls
    try:
        res = {"jsonrpc": "2.0", "id": msg.get("id"), "result": handle(msg)}
    except KeyError as e:
        res = {"jsonrpc": "2.0", "id": msg.get("id"), "error": {"code": -32601, "message": "method not found: %s" % e}}
    if msg.get("id") is not None:
        send(res)
'''

README = """# GitHub release watcher

Fires the bound task when `repo` publishes a new release. Settings: repository, interval, optional token.
"""

if mode == "spec":
    print(json.dumps(SPEC, ensure_ascii=False))
elif mode == "build":
    open("plugin.yaml", "w").write(YAML.format(version="0.1.0", extra=""))
    open("main.py", "w").write(MAIN.replace("__PRE__", "False"))
    open("README.md", "w").write(README)
    if os.environ.get("TIDEBOT_MOCK_AI_BREAK"):
        open("main.py", "w").write("#!/usr/bin/env python3\nimport sys\nsys.exit(3)\n")
    print("Created github-release-watch: a polling trigger that checks the latest GitHub release of a repository and fires with the release notes. Settings: repo, interval, optional token.")
else:
    y = open("plugin.yaml").read()
    y = y.replace("    github_token:", "    include_prereleases: {type: boolean, title: Include pre-releases, default: false, x-order: 3}\n    github_token:")
    open("plugin.yaml", "w").write(y)
    src = open("main.py").read().replace("INCLUDE_PRERELEASES = False", "INCLUDE_PRERELEASES = False  # overridden by config")
    src = src.replace('if INCLUDE_PRERELEASES or not rel.get("prerelease"):', 'if config.get("include_prereleases") or INCLUDE_PRERELEASES or not rel.get("prerelease"):')
    open("main.py", "w").write(src)
    print("Added an include_prereleases setting; pre-releases are skipped unless it is on. Version bumped.")
