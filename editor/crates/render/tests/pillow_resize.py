"""Resizes raw RGB pixels with Pillow's LANCZOS, for crates/render/tests/pillow.rs.

usage: pillow_resize.py <in.rgb> <w> <h> <x0> <y0> <x1> <y1> <ow> <oh> <out.rgb>
"""
import sys

from PIL import Image

src, w, h, x0, y0, x1, y1, ow, oh, out = sys.argv[1:11]
im = Image.frombytes("RGB", (int(w), int(h)), open(src, "rb").read())
r = im.resize((int(ow), int(oh)), Image.LANCZOS, box=(float(x0), float(y0), float(x1), float(y1)))
open(out, "wb").write(r.tobytes())
