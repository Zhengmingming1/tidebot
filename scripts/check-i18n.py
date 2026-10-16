#!/usr/bin/env python3
"""Fail if the zh-CN and en dictionaries don't have exactly the same keys.

Checks the WebUI dictionaries and the agentctl/desktop-tray ones. Also flags
empty values and {placeholders} / %verbs that differ between the two languages.
Usage: python3 scripts/check-i18n.py   (run from the repo root)
"""
import json, re, sys, pathlib

PAIRS = [
    ("internal/webui/static/i18n/zh-CN.json", "internal/webui/static/i18n/en.json"),
    ("internal/i18n/locales/zh-CN.json", "internal/i18n/locales/en.json"),  # agentctl + desktop tray
]
PH = re.compile(r"\{\w+\}|%[-+ #0-9.]*[sdfvqx]")
bad = 0
for zh_path, en_path in PAIRS:
    zp, ep = pathlib.Path(zh_path), pathlib.Path(en_path)
    if not zp.exists() and not ep.exists():
        continue
    zh, en = json.loads(zp.read_text("utf-8")), json.loads(ep.read_text("utf-8"))
    for k in sorted(set(en) - set(zh)):
        print(f"{zh_path}: missing key {k}"); bad += 1
    for k in sorted(set(zh) - set(en)):
        print(f"{en_path}: missing key {k}"); bad += 1
    for k in sorted(set(zh) & set(en)):
        if not str(zh[k]).strip() or not str(en[k]).strip():
            print(f"{k}: empty value"); bad += 1
        elif set(PH.findall(str(zh[k]))) != set(PH.findall(str(en[k]))):
            print(f"{k}: placeholders differ: zh {sorted(set(PH.findall(zh[k])))} en {sorted(set(PH.findall(en[k])))}"); bad += 1
    print(f"{zh_path} / {en_path}: {len(zh)} keys")
# Every structured notification key the server stores needs a WebUI title.
src = pathlib.Path("internal/app/notify.go").read_text("utf-8")
m = re.search(r"NotificationKeys = \[\]string\{([^}]*)\}", src)
if not m:
    print("internal/app/notify.go: NotificationKeys not found"); bad += 1
else:
    keys = re.findall(r'"(\w+)"', m.group(1))
    for zh_path, en_path in PAIRS[:1]:
        for path in (zh_path, en_path):
            d = json.loads(pathlib.Path(path).read_text("utf-8"))
            for k in keys:
                if f"ntf.{k}.t" not in d:
                    print(f"{path}: missing notification key ntf.{k}.t"); bad += 1
    print(f"notification keys: {len(keys)}")
if bad:
    print(f"i18n check failed: {bad} problem(s)"); sys.exit(1)
print("i18n check ok")
