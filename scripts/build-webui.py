#!/usr/bin/env python3
"""Assemble feature sources into the shipped WebUI bundle.

Sources in internal/webui/src stay readable. The checked-in static/app.js is
minified (comments and unnecessary whitespace removed; identifiers and
literals are kept) so Go builds do not need a JavaScript toolchain. The
private closure, function bindings, event order, CSP and /static/app.js URL
are unchanged. No source map is emitted; debug against the feature sources.
"""
import argparse
import json
from pathlib import Path

# JS comment/whitespace minifier (no identifier renaming).

REGEX_PREFIX_WORDS = frozenset({
    'case', 'catch', 'continue', 'default', 'delete', 'do', 'else', 'extends',
    'finally', 'in', 'instanceof', 'new', 'return', 'throw', 'typeof', 'void',
    'yield', 'await', 'of', 'import', 'export',
})

OPS = (
    '>>>=', '===', '!==', '>>>', '<<=', '>>=', '**=', '&&=', '||=', '??=',
    '...', '=>', '++', '--', '<<', '>>', '<=', '>=', '==', '!=', '&&', '||',
    '??', '?.', '+=', '-=', '*=', '/=', '%=', '&=', '|=', '^=', '**',
)


def _wordish(c: str) -> bool:
    return bool(c) and (c.isalnum() or c in '_$' or ord(c) > 127)


def _needs_space(prev_kind, prev_val, kind, val, a, b) -> bool:
    # Two word-like tokens (idents, numbers, strings, regex) must not glue.
    if prev_kind == 'word' and kind == 'word':
        return True
    # + +  =>  ++ ;  - -  =>  -- ;  / /  starts a comment;  < !  etc.
    glue = a + b
    if glue in ('++', '--', '//', '/*', '*/', '==', '===', '!=', '!==',
                '<=', '>=', '<<', '>>', '>>>', '&&', '||', '??', '?.',
                '=>', '+=', '-=', '*=', '/=', '%=', '&=', '|=', '^=',
                '**', '...', '<<=', '>>=', '>>>=', '**=', '&&=', '||=', '??='):
        return True
    # 0x and similar already tokenized. <!  or !-- in comparison: `< !` rare.
    if glue in ('<!', '--') or (a == '/' and _wordish(b)):
        # `/foo` after division would look like a regex; keep a space: a / foo
        return True
    return False


def minify_js(src: str) -> str:
    n = len(src)
    i = 0
    prev_kind = None
    prev_val = ''
    out: list[str] = []

    def peek(k=0) -> str:
        j = i + k
        return src[j] if j < n else ''

    def emit(kind: str, val: str) -> None:
        nonlocal prev_kind, prev_val
        if not val:
            return
        if kind == 'nl':
            if not out or out[-1].endswith('\n'):
                return
            out.append('\n')
            return
        if out and not out[-1].endswith('\n'):
            a, b = out[-1][-1], val[0]
            if _needs_space(prev_kind, prev_val, kind, val, a, b):
                out.append(' ')
        out.append(val)
        prev_kind, prev_val = kind, val

    def read_string(q: str) -> str:
        nonlocal i
        start = i
        i += 1
        while i < n:
            c = src[i]
            if c == '\\':
                i += 2
                continue
            if c == q:
                i += 1
                return src[start:i]
            if c == '\n' and q != '`':
                break
            i += 1
        return src[start:i]

    def read_regex() -> str:
        nonlocal i
        start = i
        i += 1
        in_class = False
        while i < n:
            c = src[i]
            if c == '\\':
                i += 2
                continue
            if c == '\n':
                break
            if c == '[' and not in_class:
                in_class = True
                i += 1
                continue
            if c == ']' and in_class:
                in_class = False
                i += 1
                continue
            if c == '/' and not in_class:
                i += 1
                while i < n and src[i].isalpha():
                    i += 1
                return src[start:i]
            i += 1
        return src[start:i]

    def can_regex() -> bool:
        if prev_kind is None:
            return True
        if prev_kind == 'word':
            return prev_val in REGEX_PREFIX_WORDS
        if prev_kind == 'punct':
            return prev_val not in (')', ']', '++', '--')
        return False

    def is_ident_start(c: str) -> bool:
        return c.isalpha() or c in '_$' or (c and ord(c) > 127)

    def is_ident_part(c: str) -> bool:
        return c.isalnum() or c in '_$' or (c and ord(c) > 127)

    def read_template() -> str:
        """Keep template literals intact except minify ${ ... } expressions."""
        nonlocal i
        parts = ['`']
        i += 1
        chunk_start = i
        while i < n:
            c = src[i]
            if c == '\\':
                i += 2
                continue
            if c == '`':
                parts.append(src[chunk_start:i])
                parts.append('`')
                i += 1
                return ''.join(parts)
            if c == '$' and peek(1) == '{':
                parts.append(src[chunk_start:i])
                i += 2
                expr, i2 = read_balanced_expr(src, i)
                i = i2
                parts.append('${')
                parts.append(minify_js(expr).strip())
                parts.append('}')
                chunk_start = i
                continue
            i += 1
        parts.append(src[chunk_start:i])
        return ''.join(parts)

    while i < n:
        c = peek()
        if c in ' \t\r\f\v':
            i += 1
            continue
        if c == '\n':
            emit('nl', '\n')
            i += 1
            continue
        if c == '/' and peek(1) == '/':
            i += 2
            while i < n and src[i] != '\n':
                i += 1
            emit('nl', '\n')
            continue
        if c == '/' and peek(1) == '*':
            i += 2
            nl = False
            while i < n:
                if src[i] == '*' and peek(1) == '/':
                    i += 2
                    break
                if src[i] == '\n':
                    nl = True
                i += 1
            if nl:
                emit('nl', '\n')
            continue
        if c in '"\'':
            emit('word', read_string(c))
            continue
        if c == '`':
            emit('word', read_template())
            continue
        if c == '/' and can_regex():
            emit('word', read_regex())
            continue
        if is_ident_start(c) or (c == '\\' and peek(1) == 'u'):
            start = i
            if c == '\\':
                i += 6 if peek(1) == 'u' else 1
            else:
                i += 1
            while i < n:
                if is_ident_part(src[i]):
                    i += 1
                elif src[i] == '\\' and peek(1) == 'u':
                    i += 6
                else:
                    break
            emit('word', src[start:i])
            continue
        if c.isdigit() or (c == '.' and peek(1).isdigit()):
            start = i
            i += 1
            while i < n:
                d = src[i]
                if d.isalnum() or d in '._':
                    if d in 'eE' and peek(1) in '+-':
                        i += 2
                        continue
                    i += 1
                    continue
                break
            emit('word', src[start:i])
            continue
        matched = None
        for op in OPS:
            if src.startswith(op, i):
                matched = op
                break
        if matched:
            emit('punct', matched)
            i += len(matched)
            continue
        emit('punct', c)
        i += 1

    text = ''.join(out).strip()
    return (text + '\n') if text else ''


def read_balanced_expr(src: str, i: int) -> tuple[str, int]:
    """Read a ${ ... } expression; return (source, index just after closing })."""
    n = len(src)
    start = i
    depth = 1
    while i < n and depth:
        c = src[i]
        if c in '"\'`':
            q = c
            i += 1
            while i < n:
                if src[i] == '\\':
                    i += 2
                    continue
                if src[i] == q:
                    i += 1
                    break
                i += 1
            continue
        if c == '/' and i + 1 < n and src[i + 1] == '/':
            i += 2
            while i < n and src[i] != '\n':
                i += 1
            continue
        if c == '/' and i + 1 < n and src[i + 1] == '*':
            i += 2
            while i + 1 < n and not (src[i] == '*' and src[i + 1] == '/'):
                i += 1
            i += 2
            continue
        if c == '{':
            depth += 1
        elif c == '}':
            depth -= 1
            if depth == 0:
                return src[start:i], i + 1
        i += 1
    return src[start:i], i

root = Path(__file__).resolve().parents[1]
src = root / 'internal/webui/src'
output = root / 'internal/webui/static/app.js'
parser = argparse.ArgumentParser()
parser.add_argument('--check', action='store_true', help='fail if the checked-in bundle is stale')
args = parser.parse_args()
names = json.loads((src / 'bundle.json').read_text(encoding='utf-8'))
assert len(names) == len(set(names)), 'duplicate source in bundle manifest'
assert set(names) == {p.name for p in src.glob('*.js')}, 'source missing from bundle manifest'
body = ''.join((src / name).read_text(encoding='utf-8').replace('\r\n', '\n') for name in names)
bundle = minify_js(body).encode('utf-8')
if args.check:
    if output.read_bytes().replace(b'\r\n', b'\n') != bundle:
        raise SystemExit('WebUI bundle is stale: run python3 scripts/build-webui.py')
else:
    output.write_bytes(bundle)
