"""A component backend (spec/components.md, "Backends", protocol retake-raster/1), standard library only.

Reads one JSON request line on stdin; for each frame k in request["frames"] = [a, b) (composition time t = k / fps)
writes a JSON header line and the pixels to stdout:
  {"box": [x, y, w, h]} then w * h * 4 bytes of straight-alpha RGBA, rows top to bottom;
  {"same": true}  the previous frame again;
  {"empty": true} nothing drawn.

It draws `text` in a rounded badge that slides up 40 design pixels and fades in over `enter` seconds. The letters
come from a built-in 5x7 pixel font, or from Pillow's default font when `pillow` is true and Pillow is installed.
The badge is drawn once; frames only move it and fade it, so a 1080p frame costs a few milliseconds.
"""
import json
import sys

# 5x7 glyphs: seven rows, top to bottom, five bits each (bit 4 is the leftmost column).
FONT = {
    "A": (0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11), "B": (0x1E, 0x11, 0x11, 0x1E, 0x11, 0x11, 0x1E),
    "C": (0x0E, 0x11, 0x10, 0x10, 0x10, 0x11, 0x0E), "D": (0x1E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x1E),
    "E": (0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x1F), "F": (0x1F, 0x10, 0x10, 0x1E, 0x10, 0x10, 0x10),
    "G": (0x0E, 0x11, 0x10, 0x17, 0x11, 0x11, 0x0F), "H": (0x11, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11),
    "I": (0x0E, 0x04, 0x04, 0x04, 0x04, 0x04, 0x0E), "J": (0x07, 0x02, 0x02, 0x02, 0x02, 0x12, 0x0C),
    "K": (0x11, 0x12, 0x14, 0x18, 0x14, 0x12, 0x11), "L": (0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F),
    "M": (0x11, 0x1B, 0x15, 0x15, 0x11, 0x11, 0x11), "N": (0x11, 0x11, 0x19, 0x15, 0x13, 0x11, 0x11),
    "O": (0x0E, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E), "P": (0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10),
    "Q": (0x0E, 0x11, 0x11, 0x11, 0x15, 0x12, 0x0D), "R": (0x1E, 0x11, 0x11, 0x1E, 0x14, 0x12, 0x11),
    "S": (0x0F, 0x10, 0x10, 0x0E, 0x01, 0x01, 0x1E), "T": (0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x04),
    "U": (0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x0E), "V": (0x11, 0x11, 0x11, 0x11, 0x11, 0x0A, 0x04),
    "W": (0x11, 0x11, 0x11, 0x15, 0x15, 0x15, 0x0A), "X": (0x11, 0x11, 0x0A, 0x04, 0x0A, 0x11, 0x11),
    "Y": (0x11, 0x11, 0x11, 0x0A, 0x04, 0x04, 0x04), "Z": (0x1F, 0x01, 0x02, 0x04, 0x08, 0x10, 0x1F),
    "0": (0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E), "1": (0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E),
    "2": (0x0E, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1F), "3": (0x1F, 0x02, 0x04, 0x02, 0x01, 0x11, 0x0E),
    "4": (0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02), "5": (0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E),
    "6": (0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E), "7": (0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08),
    "8": (0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E), "9": (0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C),
    " ": (0, 0, 0, 0, 0, 0, 0), ".": (0, 0, 0, 0, 0, 0x0C, 0x0C), ",": (0, 0, 0, 0, 0x0C, 0x04, 0x08),
    "!": (0x04, 0x04, 0x04, 0x04, 0x04, 0, 0x04), "?": (0x0E, 0x11, 0x01, 0x02, 0x04, 0, 0x04),
    "-": (0, 0, 0, 0x1F, 0, 0, 0), ":": (0, 0x0C, 0x0C, 0, 0x0C, 0x0C, 0), "'": (0x04, 0x04, 0x08, 0, 0, 0, 0),
    "%": (0x18, 0x19, 0x02, 0x04, 0x08, 0x13, 0x03), "+": (0, 0x04, 0x04, 0x1F, 0x04, 0x04, 0),
    "/": (0, 0x01, 0x02, 0x04, 0x08, 0x10, 0),
}


def rgb(hex_color):
    h = hex_color.lstrip("#")
    return bytes(int(h[i:i + 2], 16) for i in (0, 2, 4))


def round_corners(img, w, h, r):
    """Anti-aliased rounded corners: alpha = the corner pixel's coverage by a circle of radius r (4x4 samples)."""
    if r <= 0:
        return
    for cy in range(r):
        for cx in range(r):
            inside = 0
            for sy in range(4):
                for sx in range(4):
                    dx = r - (cx + (sx + 0.5) / 4)
                    dy = r - (cy + (sy + 0.5) / 4)
                    inside += dx * dx + dy * dy <= r * r
            a = (255 * inside + 8) // 16
            if a == 255:
                continue
            for x, y in ((cx, cy), (w - 1 - cx, cy), (cx, h - 1 - cy), (w - 1 - cx, h - 1 - cy)):
                i = (y * w + x) * 4
                img[i + 3] = img[i + 3] * a // 255


def badge_bitmap(text, px, color, fill):
    """The badge with the 5x7 font at `px` output pixels per font pixel: (rgba, w, h)."""
    glyphs = [FONT.get(ch, FONT["?"]) for ch in text]
    cols = max(1, len(glyphs) * 6 - 1)
    pad = 2
    w, h = (cols + 2 * pad) * px, (7 + 2 * pad) * px
    fg, bg = color + b"\xff", fill + b"\xff"
    blank = bg * w
    img = bytearray(blank * (pad * px))
    for row in range(7):
        cells = [bg] * pad
        for j, g in enumerate(glyphs):
            bits = g[row]
            cells += [fg if bits >> (4 - c) & 1 else bg for c in range(5)]
            if j + 1 < len(glyphs):
                cells.append(bg)
        cells += [bg] * pad
        line = b"".join(c * px for c in cells)
        img += line * px
    img += blank * (pad * px)
    round_corners(img, w, h, pad * px)
    return img, w, h


def badge_pillow(text, size, color, fill):
    """The badge with Pillow's default font (None when Pillow isn't installed)."""
    try:
        from PIL import Image, ImageDraw, ImageFont
    except ImportError:
        return None
    font = ImageFont.load_default(size=max(8, size))
    probe = ImageDraw.Draw(Image.new("RGBA", (1, 1)))
    x0, y0, x1, y1 = probe.textbbox((0, 0), text, font=font)
    pad = max(2, size // 4)
    w, h = (x1 - x0) + 2 * pad, (y1 - y0) + 2 * pad
    im = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    d.rounded_rectangle((0, 0, w - 1, h - 1), radius=pad, fill=tuple(fill) + (255,))
    d.text((pad - x0, pad - y0), text, font=font, fill=tuple(color) + (255,))
    return bytearray(im.tobytes()), w, h


def main():
    req = json.loads(sys.stdin.readline())
    if req.get("protocol") != "retake-raster/1":
        sys.exit(f"python-text speaks retake-raster/1, not {req.get('protocol')}")
    p = req["props"]
    sw, sh = req["size"]
    cw, ch = req.get("canvas", [1920, 1080])
    fps = req["fps"]
    a, b = req["frames"]
    # The design canvas fits the output like `contain`: scale k, centred (the badge is centred horizontally).
    k = min(sw / cw, sh / ch)
    oy = (sh - ch * k) / 2
    text = str(p.get("text", "")).upper()
    color, fill = rgb(p.get("color", "#ffd23f")), rgb(p.get("fill", "#1b1d24"))
    size = float(p.get("size", 112))
    made = badge_pillow(text, round(size * k), color, fill) if p.get("pillow") else None
    if made is None:
        if p.get("pillow"):
            print("python-text: Pillow isn't installed; using the pixel font", file=sys.stderr)
        made = badge_bitmap(text, max(1, round(size * k / 7)), color, fill)
    img, w, h = made
    enter = float(p.get("enter", 0.3))
    cx, cy = sw / 2, oy + float(p.get("y", 860)) * k
    out = sys.stdout.buffer
    last = None
    for f in range(a, b):
        t = f / fps
        if t < 0:
            state = None
        else:
            e = 1.0 if enter <= 0 else min(1.0, t / enter)
            e3 = 1 - (1 - e) ** 3
            x, y = round(cx - w / 2), round(cy + 40 * k * (1 - e3) - h / 2)
            state = (x, y, round(255 * e))
        if state is not None and state == last:
            out.write(b'{"same": true}\n')
            continue
        last = state
        if state is None:
            out.write(b'{"empty": true}\n')
            continue
        x, y, alpha = state
        px = img
        if alpha < 255:
            table = bytes(v * alpha // 255 for v in range(256))
            px = bytearray(img)
            px[3::4] = bytes(img[3::4]).translate(table)
        out.write(json.dumps({"box": [x, y, w, h]}).encode() + b"\n")
        out.write(px)
    out.flush()


if __name__ == "__main__":
    main()
