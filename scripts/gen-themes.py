#!/usr/bin/env python3
"""Regenerate the theme tokens block of internal/webui/static/styles.css.

Three themes (graphite = default, tide, midnight), each with a light and a dark palette.
<html data-theme="…" data-mode="system|light|dark"> picks one; "system" follows
prefers-color-scheme in CSS, so the right colours are there before the first paint with no
script. Only this block between the @themes markers is rewritten.

    python3 scripts/gen-themes.py
"""
import os, re

# Colour hues for icons and tags: (ink, tile background) per mode. Inks are ≥ 4.5:1 on the
# matching surface; tiles are the hue at low alpha so they sit on any surface.
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
    bg="#ffffff", bg_sunk="#f4f4f5", surface="#ffffff", surface_2="#fafafa", row_hover="#f8f8f9",
    border="#e7e7ea", border_strong="#d6d6db",
    text="#18181b", text_2="#52525b", text_3="#6b6b74",
    accent="#5b5bd6", accent_600="#4a4ac2", accent_50="#f1f1fd", accent_100="#e1e1fa", accent_fg="#ffffff",
    primary_bg="#18181b", primary_hover="#3f3f46", primary_fg="#ffffff",
    ok="#1a7f4b", ok_50="#e9f6ef", ok_100="#bfe5cf", warn="#a35a00", warn_50="#fdf4e5", warn_100="#f2d7a8",
    danger="#cf2e3f", danger_50="#fdeef0", danger_100="#f6c3c9", info="#3b6fd8", info_50="#edf2fd",
    beat_ok="#3fa76c",
    side_bg="#f7f7f8", side_fg="#52525b", side_hover="#ededef", side_active_bg="#e6e6e9", side_active_fg="#18181b", side_line="#ececef",
    shadow="0 0 0 0 transparent", shadow_hover="0 2px 8px rgba(0, 0, 0, .06)",
    shadow_pop="0 0 0 1px rgba(0, 0, 0, .04), 0 4px 12px rgba(0, 0, 0, .06), 0 16px 40px rgba(0, 0, 0, .10)",
    overlay="rgba(9, 9, 11, .32)", code_bg="#f4f4f5", sb_thumb="#d4d4d8", sb_thumb_hover="#a1a1aa",
    tip_bg="#18181b", tip_fg="#fafafa", tip_link="#b8b8ff", mark_bg="#fde68a", mark_cur="#fef3c7",
    mark_a="#4c8df2", mark_b="#1640a3", mark_sun="#ffd98a", mark_sand="#efe4cf"),
  "dark": dict(
    bg="#111113", bg_sunk="#1c1c1f", surface="#161618", surface_2="#19191c", row_hover="#1b1b1e",
    border="#26262a", border_strong="#36363c",
    text="#ededef", text_2="#a1a1aa", text_3="#8a8a93",
    accent="#8484f2", accent_600="#9a9af6", accent_50="#1e1e38", accent_100="#2c2c52", accent_fg="#0b0b1a",
    primary_bg="#ededef", primary_hover="#ffffff", primary_fg="#111113",
    ok="#4cc485", ok_50="#11261a", ok_100="#1d4a31", warn="#e8a547", warn_50="#2a1f0e", warn_100="#4d3a17",
    danger="#f2616f", danger_50="#2d1418", danger_100="#5a2229", info="#7aa5f8", info_50="#16203a",
    beat_ok="#3fa76c",
    side_bg="#0c0c0e", side_fg="#a1a1aa", side_hover="#18181b", side_active_bg="#1f1f23", side_active_fg="#fafafa", side_line="#1c1c20",
    shadow="0 0 0 0 transparent", shadow_hover="0 2px 8px rgba(0, 0, 0, .4)",
    shadow_pop="0 0 0 1px rgba(255, 255, 255, .06), 0 8px 24px rgba(0, 0, 0, .5)",
    overlay="rgba(0, 0, 0, .6)", code_bg="#1c1c1f", sb_thumb="#3a3a40", sb_thumb_hover="#55555c",
    tip_bg="#ededef", tip_fg="#111113", tip_link="#4a4ac2", mark_bg="#6b5310", mark_cur="#3a2f0c",
    mark_a="#4c8df2", mark_b="#1640a3", mark_sun="#ffd98a", mark_sand="#e3d6bd"),
 },
 "tide": {
  "light": dict(
    bg="#f4f7f8", bg_sunk="#e9f0f2", surface="#ffffff", surface_2="#f8fbfb", row_hover="#f3f8f9",
    border="#e1e9ec", border_strong="#cbd8dd",
    text="#0f2530", text_2="#44606d", text_3="#5e7783",
    accent="#0e7c86", accent_600="#0a6870", accent_50="#e5f3f3", accent_100="#c6e6e7", accent_fg="#ffffff",
    primary_bg="#0e7c86", primary_hover="#0a6870", primary_fg="#ffffff",
    ok="#16804a", ok_50="#e6f5ec", ok_100="#bde3cb", warn="#a5600a", warn_50="#fdf4e4", warn_100="#f0d6a6",
    danger="#c93445", danger_50="#fcedef", danger_100="#f3c2c8", info="#1f6fb8", info_50="#e8f2fb",
    beat_ok="#2fa36b",
    side_bg="#e8f0f2", side_fg="#3f5866", side_hover="#dde8eb", side_active_bg="#ffffff", side_active_fg="#0b3a4a", side_line="#dbe5e8",
    shadow="0 1px 2px rgba(15, 60, 80, .05)", shadow_hover="0 2px 4px rgba(15, 60, 80, .06), 0 8px 20px rgba(15, 60, 80, .08)",
    shadow_pop="0 0 0 1px rgba(15, 60, 80, .06), 0 6px 16px rgba(15, 60, 80, .08), 0 18px 44px rgba(15, 60, 80, .12)",
    overlay="rgba(8, 30, 40, .32)", code_bg="#eef4f5", sb_thumb="#c6d5da", sb_thumb_hover="#93aab3",
    tip_bg="#0f2530", tip_fg="#f2f8f9", tip_link="#8fdde2", mark_bg="#fde68a", mark_cur="#fef3c7",
    mark_a="#22a5ae", mark_b="#0b5f78", mark_sun="#ffd98a", mark_sand="#efe4cf"),
  "dark": dict(
    bg="#0b171e", bg_sunk="#132631", surface="#10212a", surface_2="#122530", row_hover="#13262f",
    border="#1d3540", border_strong="#2a4756",
    text="#e4eef1", text_2="#9fb3bc", text_3="#7f97a1",
    accent="#2bb3bb", accent_600="#4cc6cd", accent_50="#0f3036", accent_100="#164850", accent_fg="#04161a",
    primary_bg="#2bb3bb", primary_hover="#4cc6cd", primary_fg="#04161a",
    ok="#45c486", ok_50="#0f2a1e", ok_100="#1a4a33", warn="#e6a74a", warn_50="#2a1f0f", warn_100="#4d3a1b",
    danger="#f0687a", danger_50="#2b141b", danger_100="#58222f", info="#5fb0f0", info_50="#0f2638",
    beat_ok="#35a873",
    side_bg="#08131a", side_fg="#9fb3bc", side_hover="#0f1f28", side_active_bg="#163039", side_active_fg="#e6f4f6", side_line="#13252e",
    shadow="0 0 0 0 transparent", shadow_hover="0 2px 10px rgba(0, 0, 0, .4)",
    shadow_pop="0 0 0 1px rgba(255, 255, 255, .05), 0 10px 28px rgba(0, 0, 0, .5)",
    overlay="rgba(0, 8, 12, .62)", code_bg="#132631", sb_thumb="#2a4756", sb_thumb_hover="#3d6070",
    tip_bg="#e4eef1", tip_fg="#0b171e", tip_link="#0a6870", mark_bg="#6b5310", mark_cur="#3a2f0c",
    mark_a="#22a5ae", mark_b="#0b5f78", mark_sun="#ffd98a", mark_sand="#d9cdb6"),
 },
 "midnight": {
  "light": dict(
    bg="#f7f7f8", bg_sunk="#eeeef1", surface="#ffffff", surface_2="#fbfbfc", row_hover="#f6f6f8",
    border="#e4e4e8", border_strong="#d2d2d8",
    text="#111114", text_2="#4f4f58", text_3="#6b6b75",
    accent="#a76800", accent_600="#8c5700", accent_50="#fbf2df", accent_100="#f3dfb4", accent_fg="#ffffff",
    primary_bg="#111114", primary_hover="#33333a", primary_fg="#ffffff",
    ok="#16804a", ok_50="#e7f5ed", ok_100="#bfe4cd", warn="#c2410c", warn_50="#fdf0e8", warn_100="#f5cdb6",
    danger="#cf2e3f", danger_50="#fdeef0", danger_100="#f6c3c9", info="#3b6fd8", info_50="#edf2fd",
    beat_ok="#3fa76c",
    side_bg="#ededf0", side_fg="#4f4f58", side_hover="#e3e3e7", side_active_bg="#dcdce1", side_active_fg="#111114", side_line="#e2e2e6",
    shadow="0 1px 2px rgba(0, 0, 0, .04)", shadow_hover="0 2px 8px rgba(0, 0, 0, .07)",
    shadow_pop="0 0 0 1px rgba(0, 0, 0, .05), 0 6px 16px rgba(0, 0, 0, .08), 0 18px 44px rgba(0, 0, 0, .12)",
    overlay="rgba(0, 0, 0, .36)", code_bg="#f0f0f3", sb_thumb="#d2d2d8", sb_thumb_hover="#9e9ea8",
    tip_bg="#111114", tip_fg="#f4f4f5", tip_link="#f2b33d", mark_bg="#fde68a", mark_cur="#fef3c7",
    mark_a="#24304d", mark_b="#0b1020", mark_sun="#f2b33d", mark_sand="#3a3226"),
  "dark": dict(
    bg="#0d0d10", bg_sunk="#1a1a1e", surface="#141417", surface_2="#17171b", row_hover="#18181c",
    border="#232328", border_strong="#303037",
    text="#ececef", text_2="#a0a0a9", text_3="#85858f",
    accent="#f2b33d", accent_600="#f6c464", accent_50="#2a2214", accent_100="#41321a", accent_fg="#1a1204",
    primary_bg="#f4f4f5", primary_hover="#ffffff", primary_fg="#0b0b0d",
    ok="#4ccf8a", ok_50="#11281c", ok_100="#1d4a33", warn="#ff8f4d", warn_50="#2e1a10", warn_100="#55301b",
    danger="#ff6b72", danger_50="#2e1416", danger_100="#5c2328", info="#7aa5f8", info_50="#16203a",
    beat_ok="#3dbb7a",
    side_bg="#09090b", side_fg="#9a9aa3", side_hover="#141417", side_active_bg="#1c1c21", side_active_fg="#f4f4f5", side_line="#18181c",
    shadow="inset 0 1px 0 rgba(255, 255, 255, .03)", shadow_hover="0 2px 10px rgba(0, 0, 0, .5)",
    shadow_pop="0 0 0 1px rgba(255, 255, 255, .07), inset 0 1px 0 rgba(255, 255, 255, .05), 0 12px 32px rgba(0, 0, 0, .55)",
    overlay="rgba(0, 0, 0, .64)", code_bg="#1a1a1e", sb_thumb="#34343b", sb_thumb_hover="#50505a",
    tip_bg="#ececef", tip_fg="#0d0d10", tip_link="#8c5700", mark_bg="#6b5310", mark_cur="#3a2f0c",
    mark_a="#24304d", mark_b="#0b1020", mark_sun="#f2b33d", mark_sand="#3a3226"),
 },
}

def block(theme, mode):
    c = dict(T[theme][mode])
    out = ["  color-scheme: %s;" % mode]
    for k, v in c.items():
        out.append("  --%s: %s;" % (k.replace("_", "-"), v))
    out.append("  --focus-ring: 0 0 0 2px var(--bg), 0 0 0 4px %s;" % rgba(c["accent"], ".55"))
    out.append("  --ok-line: var(--ok-100); --warn-line: var(--warn-100); --danger-line: var(--danger-100);")
    out.append("  --surface-tint: var(--accent-50); --muted: var(--text-3); --bad: var(--danger); --card: var(--surface);")
    for h, v in HUES[mode].items():
        out.append("  --hue-%s: %s; --hue-%s-bg: %s;" % (h, v, h, rgba(v, ".14" if mode == "light" else ".18")))
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
        parts.append('.tsw[data-pv="%s"] { %s }' % (theme, pv(theme, "light")))
        parts.append('html[data-mode="dark"] .tsw[data-pv="%s"] { %s }' % (theme, pv(theme, "dark")))
        parts.append('@media (prefers-color-scheme: dark) { html[data-mode="system"] .tsw[data-pv="%s"] { %s } }' % (theme, pv(theme, "dark")))
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
