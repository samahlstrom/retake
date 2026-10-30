"""Renders a line of text with Pillow, as raw RGBA, for crates/render/tests/pillow.rs (a reference to measure against).

usage: pillow_text.py <font> <weight> <size> <stroke> <w> <h> <x> <y> <text> <out.rgba>

White text (black stroke when stroke > 0), anchor "mm" at (x, y), on a transparent w x h canvas. Pillow is only a tool
here: the test is skipped where it isn't installed.
"""
import sys

from PIL import Image, ImageDraw, ImageFont

font, weight, size, stroke, w, h, x, y, text, out = sys.argv[1:11]
f = ImageFont.truetype(font, int(size))
try:
    f.set_variation_by_axes([float(weight)])
except OSError:
    pass
im = Image.new("RGBA", (int(w), int(h)), (0, 0, 0, 0))
d = ImageDraw.Draw(im)
d.text((float(x), float(y)), text, font=f, fill=(255, 255, 255, 255), anchor="mm", stroke_width=int(stroke),
       stroke_fill=(0, 0, 0, 255))
open(out, "wb").write(im.tobytes())
