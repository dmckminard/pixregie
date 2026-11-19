#!/usr/bin/env python3
"""Rasterise the OFL pixel fonts into 1-bit glyph atlases for the Pi.

The display draws text by blitting these atlases, so the Go code needs no
font-rendering dependency and every glyph is pixel-exact.

usage: python3 tools/fontgen/fontgen.py   (from the repo root, needs Pillow)
"""
import json
import os
from PIL import Image, ImageDraw, ImageFont

FONTS = os.path.join("internal", "screen", "fonts")
CHARS = "".join(chr(c) for c in range(32, 127)) + "àâäçéèêëîïôöùûüÿœÀÂÄÇÉÈÊËÎÏÔÖÙÛÜŒ·…×—’«»°"

FACES = {
    "silk16": ("Silkscreen-Regular.ttf", 16),
    "silk24b": ("Silkscreen-Bold.ttf", 24),
    "vt20": ("VT323-Regular.ttf", 20),
}

for name, (ttf, size) in FACES.items():
    font = ImageFont.truetype(os.path.join(FONTS, ttf), size)
    ascent, descent = font.getmetrics()
    height = ascent + descent
    glyphs, x = {}, 0
    advances = []
    for ch in CHARS:
        adv = round(font.getlength(ch))
        advances.append((ch, adv))
    width = sum(a for _, a in advances) + len(advances)
    img = Image.new("L", (width, height), 0)
    draw = ImageDraw.Draw(img)
    draw.fontmode = "1"  # no anti-aliasing: these are pixel fonts
    for ch, adv in advances:
        draw.text((x, 0), ch, font=font, fill=255)
        glyphs[ch] = [x, adv]
        x += adv + 1
    img.save(os.path.join(FONTS, name + ".png"), optimize=True)
    with open(os.path.join(FONTS, name + ".json"), "w") as f:
        json.dump({"height": height, "ascent": ascent, "glyphs": glyphs}, f, ensure_ascii=False, separators=(",", ":"))
    print(name, width, "x", height)
