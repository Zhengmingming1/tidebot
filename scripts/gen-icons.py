#!/usr/bin/env python3
"""Rasterize branding/*.svg into every icon the app ships.

Needs Playwright (Chromium renders the SVG) and Pillow.
  python3 scripts/gen-icons.py
Outputs:
  cmd/tidebot-desktop/assets/{icon.png, tray-*.png, tray-*-template.png}
  packaging/macos/tidebot.icns, packaging/windows/tidebot.ico, packaging/linux/icons/<n>x<n>/tidebot.png
  internal/webui/static/{favicon.svg, favicon.png, apple-touch-icon.png}
"""
import io, os, pathlib, tempfile
from PIL import Image, ImageDraw
from playwright.sync_api import sync_playwright

R = pathlib.Path(__file__).resolve().parent.parent
B = R / "branding"

def render(pg, svg, size):
    html = f"<html><body style='margin:0;background:transparent'><img src='data:image/svg+xml;utf8,{svg.replace('#','%23')}' width='{size}' height='{size}'></body></html>"
    pg.set_viewport_size({"width": size, "height": size})
    pg.set_content(html)
    pg.wait_for_timeout(50)
    return Image.open(io.BytesIO(pg.screenshot(omit_background=True, clip={"x": 0, "y": 0, "width": size, "height": size}))).convert("RGBA")

def badge(img, color):
    """Status dot in the top-right corner for colored (non-template) tray icons."""
    im = img.copy(); d = ImageDraw.Draw(im); s = im.width
    r = s * 0.2; cx, cy = s - r - 1, r + 1
    d.ellipse([cx - r - s*0.06, cy - r - s*0.06, cx + r + s*0.06, cy + r + s*0.06], fill=(255, 255, 255, 255))
    d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=color)
    return im

def main():
    icon, mark = (B / "icon.svg").read_text(), (B / "mark.svg").read_text()
    tray = {s: (B / f"tray-{s}.svg").read_text() for s in ("normal", "running", "error")}
    with sync_playwright() as p:
        br = p.chromium.launch(); pg = br.new_page(device_scale_factor=1)
        big = {n: render(pg, icon, n) for n in (16, 32, 48, 64, 128, 256, 512, 1024)}
        small = {n: render(pg, mark, n) for n in (16, 32, 48, 64, 180, 256)}
        trayt = {s: render(pg, svg, 44) for s, svg in tray.items()}  # 22pt @2x
        br.close()
    A = R / "cmd/tidebot-desktop/assets"; A.mkdir(parents=True, exist_ok=True)
    big[512].save(A / "icon.png")
    # colored tray icons (Windows/Linux): the small mark, + status dot
    t = small[64]
    t.save(A / "tray-normal.png"); badge(t, (22, 163, 74, 255)).save(A / "tray-running.png"); badge(t, (220, 38, 38, 255)).save(A / "tray-error.png")
    for s, im in trayt.items():
        im.save(A / f"tray-{s}-template.png")  # macOS menubar, monochrome
    # macOS .icns (Pillow writes all standard sizes from the 1024 master)
    (R / "packaging/macos").mkdir(parents=True, exist_ok=True)
    big[1024].save(R / "packaging/macos/tidebot.icns")
    # Windows .ico: small sizes from the simplified mark (sharper), big ones from the master
    ico = [small[16], small[32], small[48], big[64], big[128], big[256]]
    (R / "packaging/windows").mkdir(parents=True, exist_ok=True)
    ico[-1].save(R / "packaging/windows/tidebot.ico", sizes=[(i.width, i.height) for i in ico], append_images=ico[:-1])
    for n in (16, 32, 48, 64, 128, 256, 512):
        d = R / f"packaging/linux/icons/{n}x{n}"; d.mkdir(parents=True, exist_ok=True)
        (small[n] if n <= 32 else big[n]).save(d / "tidebot.png")
    S = R / "internal/webui/static"
    (S / "favicon.svg").write_text(mark)
    small[32].save(S / "favicon.png"); small[180].save(S / "apple-touch-icon.png")
    # preview sheet
    out = pathlib.Path(os.environ.get("ICON_PREVIEW_DIR") or pathlib.Path(tempfile.gettempdir()) / "tidebot-icons"); out.mkdir(parents=True, exist_ok=True)
    sheet = Image.new("RGBA", (1400, 760), (244, 239, 230, 255)); d = ImageDraw.Draw(sheet)
    sheet.alpha_composite(big[512], (40, 40))
    x = 600
    for n in (256, 128, 64, 48, 32, 16):
        sheet.alpha_composite(big[n] if n >= 64 else small[n], (x, 40 + (256 - n)))
        x += n + 28
    x = 600
    dark = Image.new("RGBA", (760, 120), (30, 30, 34, 255)); light = Image.new("RGBA", (760, 120), (236, 236, 236, 255))
    for i, s in enumerate(("normal", "running", "error")):
        m = trayt[s]
        inv = Image.new("RGBA", m.size, (255, 255, 255, 0)); inv.putalpha(m.getchannel("A"))
        white = Image.new("RGBA", m.size, (255, 255, 255, 255)); white.putalpha(m.getchannel("A"))
        light.alpha_composite(m, (40 + i * 90, 38)); dark.alpha_composite(white, (40 + i * 90, 38))
        col = Image.open(A / f"tray-{s}.png").resize((40, 40), Image.LANCZOS)
        light.alpha_composite(col, (340 + i * 90, 40)); dark.alpha_composite(col, (340 + i * 90, 40))
    sheet.alpha_composite(light, (600, 360)); sheet.alpha_composite(dark, (600, 500))
    d.text((600, 340), "tray: macOS template (left) / Windows+Linux colored (right), light menubar", fill=(80, 70, 60, 255))
    d.text((600, 480), "same on dark menubar", fill=(80, 70, 60, 255))
    d.text((40, 580), "app icon master 512 · sizes 256-16 (16/32 use the simplified mark)", fill=(80, 70, 60, 255))
    sheet.alpha_composite(small[64], (40, 620)); d.text((120, 640), "favicon / sidebar mark", fill=(80, 70, 60, 255))
    sheet.convert("RGB").save(out / "preview-sheet.png")
    print("icons written")

if __name__ == "__main__":
    main()
