#!/usr/bin/env python3
"""Assemble feature sources without changing the private closure or public URL."""
import argparse
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
src = root / 'internal/webui/src'
output = root / 'internal/webui/static/app.js'
parser = argparse.ArgumentParser()
parser.add_argument('--check', action='store_true', help='fail if the checked-in bundle is stale')
args = parser.parse_args()
names = json.loads((src / 'bundle.json').read_text(encoding='utf-8'))
assert len(names) == len(set(names)), 'duplicate source in bundle manifest'
assert set(names) == {p.name for p in src.glob('*.js')}, 'source missing from bundle manifest'
body = b''.join((src / name).read_bytes().replace(b'\r\n', b'\n') for name in names)
if args.check:
    if output.read_bytes().replace(b'\r\n', b'\n') != body:
        raise SystemExit('WebUI bundle is stale: run python3 scripts/build-webui.py')
else:
    output.write_bytes(body)
