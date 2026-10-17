#!/usr/bin/env python3
"""Fail if a shipped file (binary, bundle, package) points users at the private
source repo. Users can only reach github.com/Zhengmingming1/tidebot-releases;
anything under github.com/Zhengmingming1/tidebot itself 404s for them.
Go module/package paths (github.com/Zhengmingming1/tidebot/internal/...) are fine:
they're symbol names, not URLs.

usage: check-no-private-url.py FILE_OR_DIR...
"""
import os, re, sys

PAT = re.compile(rb"(?:https?://(?:api\.)?github\.com/(?:repos/)?Zhengmingming1/tidebot(?![-\w])(?!/(?:internal|cmd|tools)/)"
                 rb"|Zhengmingming1/tidebot/(?:releases|issues|archive)\b"
                 rb"|Zhengmingming1/tidebot\.git)")

def scan(path):
    with open(path, "rb") as f:
        data = f.read()
    return [m.group(0).decode("latin-1") for m in PAT.finditer(data)]

def main(args):
    bad = 0
    files = []
    for a in args:
        if os.path.isdir(a):
            for root, _, names in os.walk(a):
                files += [os.path.join(root, n) for n in names]
        else:
            files.append(a)
    for p in files:
        if os.path.islink(p) or not os.path.isfile(p):
            continue
        hits = scan(p)
        if hits:
            bad += 1
            print(f"{p}: references the private repo: {sorted(set(hits))}")
    print(f"checked {len(files)} files, {bad} with private repo URLs")
    return 1 if bad else 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:] or ["."]))
