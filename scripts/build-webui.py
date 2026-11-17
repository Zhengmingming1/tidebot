#!/usr/bin/env python3
"""Assemble feature sources into the shipped WebUI bundle.

Sources in internal/webui/src stay readable. The checked-in static/app.js and
static/styles.css are minified (comments and unnecessary whitespace removed;
identifiers and literals are kept) so Go builds do not need a JS/CSS toolchain.
The private closure, function bindings, event order, CSP and /static/*.js|css
URLs are unchanged. No source map is emitted; debug against the feature sources.
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

# CSS comment/whitespace minifier (no identifier renaming).
# Spaces that change tokenization or meaning are kept: descendant combinators,
# calc()/min()/max()/clamp() + and -, and/or/not / named container pretudes
# before '(', and adjacent strings (grid-template-areas rows).

CALC_FUNCS = frozenset({'calc', 'min', 'max', 'clamp'})
NESTED_AT = frozenset({
    'media', 'supports', 'container', 'keyframes', 'layer',
    '-webkit-keyframes', '-moz-keyframes',
})
WORDISH = frozenset({
    'ident', 'function', 'url', 'string', 'hash',
    'number', 'dimension', 'percent', 'at',
})


def _css_ident_start(c: str) -> bool:
    return bool(c) and (c.isalpha() or c == '_' or ord(c) > 127)


def _css_ident_cont(c: str) -> bool:
    return _css_ident_start(c) or (bool(c) and (c.isdigit() or c == '-'))


def tokenize_css(src: str) -> list[tuple[str, str]]:
    n = len(src)
    i = 0
    out: list[tuple[str, str]] = []

    def peek(k=0) -> str:
        j = i + k
        return src[j] if j < n else ''

    def read_ident() -> str:
        nonlocal i
        start = i
        if peek() == '-' and peek(1) == '-':
            i += 2
        elif peek() == '-':
            i += 1
        if peek() == '\\':
            i += 1
            if i < n:
                i += 1
        elif _css_ident_start(peek()):
            i += 1
        while i < n:
            c = src[i]
            if _css_ident_cont(c):
                i += 1
                continue
            if c == '\\':
                i += 1
                if i < n:
                    i += 1
                continue
            break
        return src[start:i]

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
            if c == '\n':
                break
            i += 1
        return src[start:i]

    def read_number() -> str:
        nonlocal i
        start = i
        if peek() in '+-':
            i += 1
        while i < n and src[i].isdigit():
            i += 1
        if peek() == '.' and peek(1).isdigit():
            i += 1
            while i < n and src[i].isdigit():
                i += 1
        if peek() in 'eE' and (peek(1).isdigit() or (peek(1) in '+-' and peek(2).isdigit())):
            i += 1
            if peek() in '+-':
                i += 1
            while i < n and src[i].isdigit():
                i += 1
        return src[start:i]

    def starts_number() -> bool:
        c = peek()
        if c.isdigit():
            return True
        if c in '+-' and (peek(1).isdigit() or (peek(1) == '.' and peek(2).isdigit())):
            return True
        if c == '.' and peek(1).isdigit():
            return True
        return False

    def read_url_body() -> str:
        nonlocal i
        start = i
        while i < n and src[i] in ' \t\r\f\v\n':
            i += 1
        if peek() in '"\'':
            read_string(peek())
            while i < n and src[i] in ' \t\r\f\v\n':
                i += 1
            if peek() == ')':
                i += 1
        else:
            while i < n:
                c = src[i]
                if c == '\\':
                    i += 2
                    continue
                if c == ')' or c in ' \t\r\f\v\n':
                    break
                i += 1
            while i < n and src[i] in ' \t\r\f\v\n':
                i += 1
            if peek() == ')':
                i += 1
        return src[start:i]

    while i < n:
        c = peek()
        if c in ' \t\r\f\v\n':
            start = i
            while i < n and src[i] in ' \t\r\f\v\n':
                i += 1
            out.append(('space', src[start:i]))
            continue
        if c == '/' and peek(1) == '*':
            start = i
            i += 2
            while i < n and not (src[i] == '*' and peek(1) == '/'):
                i += 1
            i += 2 if i < n else 0
            out.append(('comment', src[start:i]))
            continue
        if c in '"\'':
            out.append(('string', read_string(c)))
            continue
        if c == '#':
            i += 1
            start_h = i
            if peek() == '\\' or _css_ident_start(peek()) or (peek() and (peek().isdigit() or peek() == '-')):
                if peek() == '\\':
                    i += 1
                    if i < n:
                        i += 1
                else:
                    i += 1
                while i < n:
                    ch = src[i]
                    if _css_ident_cont(ch):
                        i += 1
                        continue
                    if ch == '\\':
                        i += 1
                        if i < n:
                            i += 1
                        continue
                    break
                out.append(('hash', '#' + src[start_h:i]))
            else:
                out.append(('delim', '#'))
            continue
        if c == '@':
            i += 1
            name = read_ident()
            out.append(('at', '@' + name) if name else ('delim', '@'))
            continue
        if _css_ident_start(c) or (c == '-' and (_css_ident_start(peek(1)) or peek(1) == '-' or peek(1) == '\\')) or c == '\\':
            ident = read_ident()
            if peek() == '(':
                i += 1
                if ident.lower() == 'url':
                    out.append(('url', 'url(' + read_url_body()))
                else:
                    out.append(('function', ident + '('))
            else:
                out.append(('ident', ident))
            continue
        if starts_number():
            num = read_number()
            if peek() == '%':
                i += 1
                out.append(('percent', num + '%'))
            elif _css_ident_start(peek()) or peek() == '-' or peek() == '\\':
                out.append(('dimension', num + read_ident()))
            else:
                out.append(('number', num))
            continue
        punct = {
            '{': 'lbrace', '}': 'rbrace', '(': 'lparen', ')': 'rparen',
            '[': 'lbracket', ']': 'rbracket', ':': 'colon', ';': 'semi',
            ',': 'comma',
        }
        if c in punct:
            out.append((punct[c], c))
            i += 1
            continue
        out.append(('delim', c))
        i += 1
    return out


def _css_would_glue(a_kind, a_val, b_kind, b_val) -> bool:
    toks = [(k, v) for k, v in tokenize_css(a_val + b_val) if k not in ('space', 'comment')]
    return toks != [(a_kind, a_val), (b_kind, b_val)]


def minify_css(src: str) -> str:
    tokens = tokenize_css(src)
    func_stack: list[tuple[str, int]] = []
    paren_depth = 0
    ctx = ['rules']
    pending_at = ''
    emitted: list[tuple[str, str]] = []

    def in_calc() -> bool:
        return any(name.lower() in CALC_FUNCS for name, _ in func_stack)

    def in_decls() -> bool:
        return bool(ctx) and ctx[-1] == 'decls'

    def wordish(kind: str) -> bool:
        return kind in WORDISH

    def keep_space(prev, nxt) -> bool:
        pk, pv = prev
        nk, nv = nxt
        if in_calc() and ((pk == 'delim' and pv in '+-') or (nk == 'delim' and nv in '+-')):
            return True
        # Adjacent strings are separate grid-template-areas (or similar) rows.
        if pk == 'string' and nk == 'string':
            return True
        if pk in ('function', 'url', 'lparen', 'lbracket', 'lbrace', 'colon', 'semi', 'comma'):
            return False
        if nk in ('rparen', 'rbracket', 'rbrace', 'semi', 'comma'):
            return False
        # Space before ':' is optional in declarations and in (...) features,
        # but is a descendant combinator in selectors: `h1 :hover`.
        if nk == 'colon':
            return (not in_decls()) and paren_depth == 0
        if pk == 'colon':
            return False
        # ident( must not become a function token: `and (`, `or (`, `setcol (`.
        if nk == 'lparen':
            return pk == 'ident'
        if pk in ('rparen', 'rbracket'):
            if nk in ('lbrace', 'semi', 'comma', 'rparen', 'rbracket', 'rbrace', 'colon'):
                return False
            return wordish(nk) or nk == 'lbracket' or (nk == 'delim' and nv in '.*#')
        if _css_would_glue(pk, pv, nk, nv):
            return True
        if (wordish(pk) or pk in ('rparen', 'rbracket')) and (
            (nk == 'delim' and nv in '.*#') or nk == 'lbracket'
        ):
            if nk == 'lbracket' and in_decls():
                return False
            return True
        if pk == 'delim' and pv == '*' and (
            wordish(nk) or (nk == 'delim' and nv in '.*#') or nk in ('colon', 'lbracket', 'function')
        ):
            return True
        return False

    i = 0
    while i < len(tokens):
        kind, val = tokens[i]
        if kind == 'comment':
            i += 1
            continue
        if kind == 'space':
            j = i + 1
            while j < len(tokens) and tokens[j][0] in ('space', 'comment'):
                j += 1
            if emitted and j < len(tokens) and keep_space(emitted[-1], tokens[j]):
                emitted.append(('space', ' '))
            i += 1
            continue
        emitted.append((kind, val))
        if kind == 'at':
            pending_at = val[1:]
        elif kind == 'lbrace':
            name = pending_at.lower()
            ctx.append('rules' if name in NESTED_AT else 'decls')
            pending_at = ''
        elif kind == 'rbrace':
            if len(ctx) > 1:
                ctx.pop()
            pending_at = ''
        elif kind == 'semi' and not in_decls():
            pending_at = ''
        elif kind == 'function':
            func_stack.append((val[:-1], paren_depth))
            paren_depth += 1
        elif kind == 'lparen':
            paren_depth += 1
        elif kind == 'rparen':
            paren_depth -= 1
            if func_stack and paren_depth == func_stack[-1][1]:
                func_stack.pop()
        i += 1

    out: list[str] = []
    for kind, val in emitted:
        if kind == 'rbrace':
            if out and out[-1] == ' ':
                out.pop()
            out.append('}\n')
            continue
        if kind == 'space':
            if out and (out[-1].endswith('\n') or out[-1].endswith(' ')):
                continue
            out.append(' ')
            continue
        out.append(val)
    text = ''.join(out).strip()
    return (text + '\n') if text else ''


def _css_nonspace_tokens(src: str) -> list[tuple[str, str]]:
    return [(k, v) for k, v in tokenize_css(src) if k not in ('space', 'comment')]


def _run_css_self_test() -> None:
    cases = [
        ('/* c */.foo { color: red; }', '.foo{color:red;}\n'),
        ('.log .tool-chip { color: red; }', '.log .tool-chip{color:red;}\n'),
        ('.log.tool-chip { color: red; }', '.log.tool-chip{color:red;}\n'),
        ('div > span { color: red; }', 'div>span{color:red;}\n'),
        ('h1:hover { color: red; }', 'h1:hover{color:red;}\n'),
        ('h1 :hover { color: red; }', 'h1 :hover{color:red;}\n'),
        ('.foo { margin: calc(100% + 6px); }', '.foo{margin:calc(100% + 6px);}\n'),
        ('.foo { margin: calc(100vh - 32px); }', '.foo{margin:calc(100vh - 32px);}\n'),
        ('.foo { margin: calc(-1 * var(--s3)); }', '.foo{margin:calc(-1 * var(--s3));}\n'),
        ('.foo { w: min(320px, calc(100vh - 248px)); }', '.foo{w:min(320px,calc(100vh - 248px));}\n'),
        ('.foo { content: "/* not comment */"; }', '.foo{content:"/* not comment */";}\n'),
        ('.foo { background: url("data:image/svg+xml,%3Csvg xmlns=\'http://x\'/>"); }',
         '.foo{background:url("data:image/svg+xml,%3Csvg xmlns=\'http://x\'/>");}\n'),
        ('@media (max-width: 900px) { .a { color: red; } }', '@media(max-width:900px){.a{color:red;}\n}\n'),
        (':root { --bg: #f9fafb; --x: rgba(25, 43, 68, .025); }', ':root{--bg:#f9fafb;--x:rgba(25,43,68,.025);}\n'),
        ('.foo { grid-template-areas: "n a" "p p" "s w"; }', '.foo{grid-template-areas:"n a" "p p" "s w";}\n'),
        ('.foo { color: red !important; }', '.foo{color:red!important;}\n'),
        ('.foo:nth-child(n+3) { color: red; }', '.foo:nth-child(n+3){color:red;}\n'),
        ('@container setcol (min-width: 1040px) { .a { color: red; } }',
         '@container setcol (min-width:1040px){.a{color:red;}\n}\n'),
        ('.set-col { container: setcol / inline-size; }', '.set-col{container:setcol/inline-size;}\n'),
        ('html { overflow-y: scroll; }', 'html{overflow-y:scroll;}\n'),
        ('/* @themes:begin */ :root { --bg: #fff; } /* @themes:end */', ':root{--bg:#fff;}\n'),
        ('.foo { content: " ▸"; }', '.foo{content:" ▸";}\n'),
        ('a, b { color: red; }', 'a,b{color:red;}\n'),
        ('* { box-sizing: border-box; }', '*{box-sizing:border-box;}\n'),
        ('* .bar { color: red; }', '* .bar{color:red;}\n'),
        ('*.bar { color: red; }', '*.bar{color:red;}\n'),
        ('.foo { --focus-ring: 0 0 0 2px var(--bg); }', '.foo{--focus-ring:0 0 0 2px var(--bg);}\n'),
        ('@supports ((backdrop-filter: blur(1px)) or (-webkit-backdrop-filter: blur(1px))) { .a { color: red; } }',
         '@supports((backdrop-filter:blur(1px)) or (-webkit-backdrop-filter:blur(1px))){.a{color:red;}\n}\n'),
        ('@supports not selector(::-webkit-scrollbar) { html { scrollbar-width: thin; } }',
         '@supports not selector(::-webkit-scrollbar){html{scrollbar-width:thin;}\n}\n'),
        ('.foo { margin: 0 -1px; }', '.foo{margin:0 -1px;}\n'),
        (':root, html[data-theme="graphite"] { --bg: #fff; }',
         ':root,html[data-theme="graphite"]{--bg:#fff;}\n'),
    ]
    failed = []
    for src, want in cases:
        got = minify_css(src)
        if got != want:
            failed.append('output %r: want %r got %r' % (src, want, got))
        if _css_nonspace_tokens(src) != _css_nonspace_tokens(got):
            failed.append('tokens %r' % (src,))
        if minify_css(got) != got:
            failed.append('unstable %r' % (src,))
    if failed:
        raise SystemExit('CSS minify self-test failed:\n' + '\n'.join(failed))



root = Path(__file__).resolve().parents[1]
src = root / 'internal/webui/src'
js_output = root / 'internal/webui/static/app.js'
css_source = src / 'styles.css'
css_output = root / 'internal/webui/static/styles.css'
parser = argparse.ArgumentParser()
parser.add_argument('--check', action='store_true', help='fail if the checked-in bundle is stale')
parser.add_argument('--self-test', action='store_true', help='run minifier unit tests and exit')
args = parser.parse_args()
if args.self_test:
    _run_css_self_test()
    source = (src / 'styles.css').read_text(encoding='utf-8').replace('\r\n', '\n')
    mini = (root / 'internal/webui/static/styles.css').read_text(encoding='utf-8').replace('\r\n', '\n')
    if _css_nonspace_tokens(source) != _css_nonspace_tokens(mini):
        raise SystemExit('minified CSS tokens differ from the source stylesheet')
    if minify_css(source) != mini:
        raise SystemExit('static/styles.css is not minify_css of src/styles.css')
    if minify_css(mini) != mini:
        raise SystemExit('CSS minify is not stable on the shipped stylesheet')
    if '/*' in mini:
        raise SystemExit('shipped styles.css still contains a comment')
    raise SystemExit(0)
names = json.loads((src / 'bundle.json').read_text(encoding='utf-8'))
assert len(names) == len(set(names)), 'duplicate source in bundle manifest'
assert set(names) == {p.name for p in src.glob('*.js')}, 'source missing from bundle manifest'
body = ''.join((src / name).read_text(encoding='utf-8').replace('\r\n', '\n') for name in names)
js_bundle = minify_js(body).encode('utf-8')
css_text = css_source.read_text(encoding='utf-8').replace('\r\n', '\n')
css_bundle = minify_css(css_text).encode('utf-8')
if args.check:
    stale = []
    if js_output.read_bytes().replace(b'\r\n', b'\n') != js_bundle:
        stale.append('static/app.js')
    if css_output.read_bytes().replace(b'\r\n', b'\n') != css_bundle:
        stale.append('static/styles.css')
    if stale:
        raise SystemExit('WebUI bundle is stale (%s): run python3 scripts/build-webui.py' % ', '.join(stale))
else:
    js_output.write_bytes(js_bundle)
    css_output.write_bytes(css_bundle)
