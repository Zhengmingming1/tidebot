#!/usr/bin/env python3
"""Regenerate the theme tokens block of internal/webui/static/styles.css.

Three themes (graphite = silver, tide = frosted blue, midnight = graphite studio),
each with a light and a dark palette. Keep the saved theme IDs stable.
<html data-theme="…" data-mode="system|light|dark"> picks one; "system" follows
prefers-color-scheme in CSS, so the right colours are there before the first paint with no
script. Only this block between the @themes markers is rewritten.

    python3 scripts/gen-themes.py
"""
import os, re

# Colour hues for icons and tags per mode. The hue itself is for icons (≥ 3:1 on every surface);
# tiles are the hue at low alpha so they sit on any surface; text on a tile uses --hue-*-fg,
# derived below so it reads at 4.5:1 in every theme.
HUES = {
    "light": {
        "blue": "#2f6fed", "teal": "#0d8f80", "violet": "#7357d6", "pink": "#c4316f", "orange": "#c86a12",
        "red": "#d14343", "green": "#2e8540", "cyan": "#0a83a8", "amber": "#a87100", "slate": "#5a6b7d", "indigo": "#4f52d6",
    },
    "dark": {
        "blue": "#6d9eff", "teal": "#3cc6b2", "violet": "#a58bff", "pink": "#f075a8", "orange": "#f09a52",
        "red": "#f27474", "green": "#5cc274", "cyan": "#45b8e0", "amber": "#e9b44c", "slate": "#98a8ba", "indigo": "#8f91ff",
    },
}

def rgba(hexc, a):
    h = hexc.lstrip("#")
    return "rgba(%d, %d, %d, %s)" % (int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16), a)

T = {
 "graphite": {
  "light": dict(
    bg="#f9fafb", bg_sunk="#f1f3f6", surface="#ffffff", surface_2="#f5f7fa", row_hover="#f1f5fa",
    border="#e1e5ec", border_strong="#cbd2dc",
    text="#1c2431", text_2="#4d596a", text_3="#626e7e",
    accent="#0867d5", accent_600="#0055b8", accent_50="#eaf2ff", accent_100="#d2e4ff", accent_fg="#ffffff",
    primary_bg="#0867d5", primary_hover="#0055b8", primary_fg="#ffffff",
    ok="#1a7f4b", ok_50="#e9f6ef", ok_100="#bfe5cf", warn="#a35a00", warn_50="#fdf4e5", warn_100="#f2d7a8",
    danger="#cf2e3f", danger_50="#fdeef0", danger_100="#f6c3c9", info="#386acd", info_50="#edf2fd",
    beat_ok="#3fa76c",
    side_bg="#eef1f5", side_fg="#4d596a", side_hover="#e5eaf1", side_active_bg="#dceaff", side_active_fg="#0757b4", side_line="#dce2eb",
    shadow="0 1px 3px rgba(25, 43, 68, .025)", shadow_hover="0 2px 8px rgba(0, 0, 0, .06)",
    shadow_pop="0 0 0 1px rgba(0, 0, 0, .04), 0 4px 12px rgba(0, 0, 0, .06), 0 16px 40px rgba(0, 0, 0, .10)",
    overlay="rgba(9, 9, 11, .32)", code_bg="#f1f3f6", sb_thumb="#c9d1dc", sb_thumb_hover="#a1a1aa",
    tip_bg="#1c2431", tip_fg="#fafafa", tip_link="#a8ceff", mark_bg="#fde68a", mark_cur="#fef3c7",
    mark_a="#0865ef", mark_b="#0644d7", mark_sun="#ffd98a", mark_sand="#efe4cf"),
  "dark": dict(
    bg="#17191e", bg_sunk="#242830", surface="#1f2228", surface_2="#252932", row_hover="#2a2f38",
    border="#343a44", border_strong="#4b5461",
    text="#eef1f6", text_2="#b4bdca", text_3="#a1acbc",
    accent="#8bbcff", accent_600="#a7ccff", accent_50="#213450", accent_100="#2d4769", accent_fg="#13243b",
    primary_bg="#8bbcff", primary_hover="#a7ccff", primary_fg="#13243b",
    ok="#4cc485", ok_50="#11261a", ok_100="#1d4a31", warn="#e8a547", warn_50="#2a1f0e", warn_100="#4d3a17",
    danger="#ff7b86", danger_50="#2d1418", danger_100="#5a2229", info="#7aa5f8", info_50="#16203a",
    beat_ok="#3fa76c",
    side_bg="#13161b", side_fg="#b4bdca", side_hover="#252c37", side_active_bg="#233d5f", side_active_fg="#d8e9ff", side_line="#303640",
    shadow="0 0 0 0 transparent", shadow_hover="0 2px 8px rgba(0, 0, 0, .4)",
    shadow_pop="0 0 0 1px rgba(255, 255, 255, .06), 0 8px 24px rgba(0, 0, 0, .5)",
    overlay="rgba(0, 0, 0, .6)", code_bg="#242830", sb_thumb="#3a3a40", sb_thumb_hover="#55555c",
    tip_bg="#eef1f6", tip_fg="#17191e", tip_link="#0757b4", mark_bg="#6b5310", mark_cur="#3a2f0c",
    mark_a="#0865ef", mark_b="#0644d7", mark_sun="#ffd98a", mark_sand="#e3d6bd"),
 },
 "tide": {
  "light": dict(
    bg="#f2f8fc", bg_sunk="#e6f0f8", surface="#ffffff", surface_2="#f6fafe", row_hover="#edf5fc",
    border="#dce7f0", border_strong="#bfd1e2",
    text="#173044", text_2="#405d73", text_3="#526773",
    accent="#0064c4", accent_600="#0053a5", accent_50="#e3f0ff", accent_100="#c7e1ff", accent_fg="#ffffff",
    primary_bg="#0064c4", primary_hover="#0053a5", primary_fg="#ffffff",
    ok="#157c48", ok_50="#e6f5ec", ok_100="#bde3cb", warn="#9e5c0a", warn_50="#fdf4e4", warn_100="#f0d6a6",
    danger="#c73344", danger_50="#fcedef", danger_100="#f3c2c8", info="#1f6fb8", info_50="#e8f2fb",
    beat_ok="#2fa36b",
    side_bg="#e4eff8", side_fg="#405d73", side_hover="#dceaf6", side_active_bg="#dcecfb", side_active_fg="#074f98", side_line="#d3e2ef",
    shadow="0 2px 8px rgba(26, 85, 130, .035)", shadow_hover="0 3px 8px rgba(26, 85, 130, .06), 0 10px 24px rgba(26, 85, 130, .07)",
    shadow_pop="0 0 0 1px rgba(15, 60, 80, .06), 0 6px 16px rgba(15, 60, 80, .08), 0 18px 44px rgba(15, 60, 80, .12)",
    overlay="rgba(8, 30, 40, .32)", code_bg="#eaf2f9", sb_thumb="#bed0df", sb_thumb_hover="#93aab3",
    tip_bg="#173044", tip_fg="#f2f8f9", tip_link="#abd7ff", mark_bg="#fde68a", mark_cur="#fef3c7",
    mark_a="#0865ef", mark_b="#0644d7", mark_sun="#ffd98a", mark_sand="#efe4cf"),
  "dark": dict(
    bg="#14212b", bg_sunk="#203341", surface="#1b2c39", surface_2="#203341", row_hover="#283e4f",
    border="#344e60", border_strong="#4a6578",
    text="#eef6fc", text_2="#b9d0df", text_3="#a3bdcf",
    accent="#89c5ff", accent_600="#abd8ff", accent_50="#213f5b", accent_100="#2b5478", accent_fg="#10283c",
    primary_bg="#89c5ff", primary_hover="#abd8ff", primary_fg="#10283c",
    ok="#45c486", ok_50="#0f2a1e", ok_100="#1a4a33", warn="#e6a74a", warn_50="#2a1f0f", warn_100="#4d3a1b",
    danger="#ff8291", danger_50="#2b141b", danger_100="#58222f", info="#5fb0f0", info_50="#0f2638",
    beat_ok="#35a873",
    side_bg="#10202c", side_fg="#b9d0df", side_hover="#223d51", side_active_bg="#244a6b", side_active_fg="#e4f3ff", side_line="#2c4455",
    shadow="0 0 0 0 transparent", shadow_hover="0 2px 10px rgba(0, 0, 0, .4)",
    shadow_pop="0 0 0 1px rgba(255, 255, 255, .05), 0 10px 28px rgba(0, 0, 0, .5)",
    overlay="rgba(0, 8, 12, .62)", code_bg="#203341", sb_thumb="#2a4756", sb_thumb_hover="#3d6070",
    tip_bg="#eef6fc", tip_fg="#14212b", tip_link="#0053a5", mark_bg="#6b5310", mark_cur="#3a2f0c",
    mark_a="#0865ef", mark_b="#0644d7", mark_sun="#ffd98a", mark_sand="#d9cdb6"),
 },
 "midnight": {
  "light": dict(
    bg="#f5f6f8", bg_sunk="#eef1f5", surface="#ffffff", surface_2="#f3f5f8", row_hover="#edf1f7",
    border="#dde2e9", border_strong="#c5cdd8",
    text="#202735", text_2="#4e5b6d", text_3="#606b7b",
    accent="#1c61bd", accent_600="#174f9d", accent_50="#e7effc", accent_100="#cfdef7", accent_fg="#ffffff",
    primary_bg="#1c61bd", primary_hover="#174f9d", primary_fg="#ffffff",
    ok="#157c48", ok_50="#e7f5ed", ok_100="#bfe4cd", warn="#c0400c", warn_50="#fdf0e8", warn_100="#f5cdb6",
    danger="#cb2d3e", danger_50="#fdeef0", danger_100="#f6c3c9", info="#3768c9", info_50="#edf2fd",
    beat_ok="#3fa76c",
    side_bg="#e9edf2", side_fg="#4e5b6d", side_hover="#e8edf4", side_active_bg="#e2ecfb", side_active_fg="#164b90", side_line="#d6dde7",
    shadow="0 1px 2px rgba(0, 0, 0, .04)", shadow_hover="0 2px 8px rgba(0, 0, 0, .07)",
    shadow_pop="0 0 0 1px rgba(0, 0, 0, .05), 0 6px 16px rgba(0, 0, 0, .08), 0 18px 44px rgba(0, 0, 0, .12)",
    overlay="rgba(0, 0, 0, .36)", code_bg="#eef1f5", sb_thumb="#d2d2d8", sb_thumb_hover="#9e9ea8",
    tip_bg="#202735", tip_fg="#f4f4f5", tip_link="#b0ceff", mark_bg="#fde68a", mark_cur="#fef3c7",
    mark_a="#0865ef", mark_b="#0644d7", mark_sun="#f2b33d", mark_sand="#3a3226"),
  "dark": dict(
    bg="#181b20", bg_sunk="#282d35", surface="#22262d", surface_2="#282d35", row_hover="#303640",
    border="#373e49", border_strong="#4d5765",
    text="#f1f3f6", text_2="#bac1cd", text_3="#a3abba",
    accent="#83b4ff", accent_600="#9fc6ff", accent_50="#213451", accent_100="#2b4365", accent_fg="#102034",
    primary_bg="#83b4ff", primary_hover="#9fc6ff", primary_fg="#102034",
    ok="#4ccf8a", ok_50="#11281c", ok_100="#1d4a33", warn="#ff8f4d", warn_50="#2e1a10", warn_100="#55301b",
    danger="#ff7d84", danger_50="#2e1416", danger_100="#5c2328", info="#7aa5f8", info_50="#16203a",
    beat_ok="#3dbb7a",
    side_bg="#14171c", side_fg="#bac1cd", side_hover="#262d37", side_active_bg="#213a5b", side_active_fg="#dceaff", side_line="#303742",
    shadow="inset 0 1px 0 rgba(255, 255, 255, .03)", shadow_hover="0 2px 10px rgba(0, 0, 0, .5)",
    shadow_pop="0 0 0 1px rgba(255, 255, 255, .07), inset 0 1px 0 rgba(255, 255, 255, .05), 0 12px 32px rgba(0, 0, 0, .55)",
    overlay="rgba(0, 0, 0, .64)", code_bg="#282d35", sb_thumb="#34343b", sb_thumb_hover="#50505a",
    tip_bg="#f1f3f6", tip_fg="#181b20", tip_link="#174f9d", mark_bg="#6b5310", mark_cur="#3a2f0c",
    mark_a="#0865ef", mark_b="#0644d7", mark_sun="#f2b33d", mark_sand="#3a3226"),
 },
}

# WCAG 2 contrast. Text needs 4.5:1, icons and other non-text marks 3:1. The pairs the UI
# actually uses are checked by TestThemeContrast (internal/webui/contrast_test.go); run
# `go test ./internal/webui -run Contrast` after editing a palette.
def _rgb(hexc):
    h = hexc.lstrip("#")
    return [int(h[i:i + 2], 16) for i in (0, 2, 4)]

def _hex(c):
    return "#" + "".join("%02x" % max(0, min(255, int(round(x)))) for x in c)

def _lum(hexc):
    c = [x / 255 for x in _rgb(hexc)]
    c = [x / 12.92 if x <= 0.04045 else ((x + 0.055) / 1.055) ** 2.4 for x in c]
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]

def contrast(a, b):
    la, lb = _lum(a), _lum(b)
    return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)

def _mix(a, b, p):
    x, y = _rgb(a), _rgb(b)
    return _hex([x[i] * (1 - p) + y[i] * p for i in range(3)])

def tile_alpha(mode):
    return ".14" if mode == "light" else ".18"

def hue_fg(v, c, mode):
    """Text ink for a hue on its own tile (tags, chips): the hue, moved toward black (light) or
    white (dark) in 1% steps until it reads at 4.5:1 on the tile over every surface it sits on.
    The plain hue stays for icons, which only need 3:1."""
    a = float(tile_alpha(mode))
    tiles = [_mix(c[s], v, a) for s in ("bg", "bg_sunk", "surface", "surface_2")]
    to = "#000000" if mode == "light" else "#ffffff"
    for step in range(101):
        f = _mix(v, to, step / 100)
        # CSS composites fractional RGB values; leave room for the hex rounding above.
        if min(contrast(f, t) for t in tiles) >= 4.55:
            return f
    return to

def block(theme, mode):
    c = dict(T[theme][mode])
    out = ["  color-scheme: %s;" % mode]
    for k, v in c.items():
        out.append("  --%s: %s;" % (k.replace("_", "-"), v))
    # High-opacity navigation materials keep text readable over either dark or light content.
    if theme == "tide":
        out.append("  --nav-glass: %s; --bar-glass: %s;" % (rgba(c["side_bg"], ".97"), rgba(c["bg"], ".98")))
    out.append("  --focus-ring: 0 0 0 2px var(--bg), 0 0 0 4px %s;" % rgba(c["accent"], ".55"))
    out.append("  --ok-line: var(--ok-100); --warn-line: var(--warn-100); --danger-line: var(--danger-100);")
    out.append("  --surface-tint: var(--accent-50); --muted: var(--text-3); --bad: var(--danger); --card: var(--surface);")
    # Text on solid status / hue fills (danger button, finished step, avatar): white in light
    # palettes, the page background in dark ones (white on a light red is under 3:1).
    out.append("  --on-fill: %s;" % ("#ffffff" if mode == "light" else c["bg"]))
    for h, v in HUES[mode].items():
        out.append("  --hue-%s: %s; --hue-%s-bg: %s; --hue-%s-fg: %s;" % (h, v, h, rgba(v, tile_alpha(mode)), h, hue_fg(v, c, mode)))
    return "\n".join(out)

def css():
    parts = ["/* @themes:begin — generated by scripts/gen-themes.py; edit the script, not this block. */"]
    for theme in ("graphite", "tide", "midnight"):
        sel = ':root, html[data-theme="graphite"]' if theme == "graphite" else 'html[data-theme="%s"]' % theme
        parts.append("%s {\n%s\n}" % (sel, block(theme, "light")))
        dk = block(theme, "dark")
        parts.append('html[data-theme="%s"][data-mode="dark"]%s {\n%s\n}' % (theme, ', html:not([data-theme])[data-mode="dark"]' if theme == "graphite" else "", dk))
        parts.append('@media (prefers-color-scheme: dark) {\nhtml[data-theme="%s"][data-mode="system"] {\n%s\n}\n}' % (theme, dk))
    # Theme picker swatches: each previews its theme in the current colour mode.
    def pv(theme, mode):
        c = T[theme][mode]
        return "--pv-bg: %s; --pv-side: %s; --pv-surface: %s; --pv-border: %s; --pv-text: %s; --pv-accent: %s;" % (
            c["bg"], c["side_bg"], c["surface"], c["border_strong"], c["text"], c["accent"])
    for theme in ("graphite", "tide", "midnight"):
        # .pv-dot: the small swatch in the command palette's theme page.
        parts.append('.tsw[data-pv="%s"], .pv-dot[data-pv="%s"] { %s }' % (theme, theme, pv(theme, "light")))
        parts.append('html[data-mode="dark"] .tsw[data-pv="%s"], html[data-mode="dark"] .pv-dot[data-pv="%s"] { %s }' % (theme, theme, pv(theme, "dark")))
        parts.append('@media (prefers-color-scheme: dark) { html[data-mode="system"] .tsw[data-pv="%s"], html[data-mode="system"] .pv-dot[data-pv="%s"] { %s } }' % (theme, theme, pv(theme, "dark")))
    parts.append("/* @themes:end */")
    return "\n".join(parts)

if __name__ == "__main__":
    p = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "internal", "webui", "static", "styles.css")
    s = open(p, encoding="utf-8").read()
    new = css()
    if "/* @themes:begin" in s:
        s = re.sub(r"/\* @themes:begin.*?/\* @themes:end \*/", lambda m: new, s, flags=re.S)
    else:
        raise SystemExit("markers not found")
    open(p, "w", encoding="utf-8", newline="\n").write(s)
    print("themes block written")
