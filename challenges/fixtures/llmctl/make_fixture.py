#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 Milos Vasic
# SPDX-License-Identifier: Apache-2.0
"""Deterministic golden fixture for the llmctl vision challenge.

Renders TEXT with a built-in 5x7 block font (black on white, 8-bit grayscale PNG, stdlib only) so the ground
truth of what the image says is known BY CONSTRUCTION - the oracle is the generator, never a model.
Usage: make_fixture.py [--text STR] [--scale N] --out PNG [--manifest JSON]
Also: make_fixture.py --pixel-hash PNG   prints the sha256 of the decoded pixel rows (zlib-version independent).
"""
import hashlib, json, struct, sys, zlib

FONT = {
    "A": "01110 10001 10001 11111 10001 10001 10001",
    "C": "01110 10001 10000 10000 10000 10001 01110",
    "E": "11111 10000 10000 11110 10000 10000 11111",
    "G": "01110 10001 10000 10111 10001 10001 01111",
    "I": "01110 00100 00100 00100 00100 00100 01110",
    "L": "10000 10000 10000 10000 10000 10000 11111",
    "N": "10001 11001 10101 10011 10001 10001 10001",
    "O": "01110 10001 10001 10001 10001 10001 01110",
    "R": "11110 10001 10001 11110 10100 10010 10001",
    "T": "11111 00100 00100 00100 00100 00100 00100",
    "Z": "11111 00001 00010 00100 01000 10000 11111",
    " ": "00000 00000 00000 00000 00000 00000 00000",
}


def render(text, scale):
    cell_w, cell_h, pad = 6, 7, 2
    w = (len(text) * cell_w + 2 * pad) * scale
    h = (cell_h + 2 * pad) * scale
    rows = [[255] * w for _ in range(h)]
    for ci, ch in enumerate(text):
        if ch not in FONT:
            raise SystemExit("unsupported character %r (font: %s)" % (ch, "".join(sorted(FONT))))
        glyph = FONT[ch].split()
        for gy, line in enumerate(glyph):
            for gx, bit in enumerate(line):
                if bit != "1":
                    continue
                x0 = (pad + ci * cell_w + gx) * scale
                y0 = (pad + gy) * scale
                for dy in range(scale):
                    for dx in range(scale):
                        rows[y0 + dy][x0 + dx] = 0
    return w, h, rows


def png_bytes(w, h, rows):
    def chunk(tag, data):
        c = struct.pack(">I", len(data)) + tag + data
        return c + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
    raw = b"".join(b"\x00" + bytes(r) for r in rows)
    ihdr = struct.pack(">IIBBBBB", w, h, 8, 0, 0, 0, 0)
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b"")


def pixel_hash(png):
    pos, idat = 8, b""
    while pos < len(png):
        n = struct.unpack(">I", png[pos:pos + 4])[0]
        tag = png[pos + 4:pos + 8]
        if tag == b"IDAT":
            idat += png[pos + 8:pos + 8 + n]
        pos += 12 + n
    return hashlib.sha256(zlib.decompress(idat)).hexdigest()


def main(argv):
    if len(argv) == 3 and argv[1] == "--pixel-hash":
        print(pixel_hash(open(argv[2], "rb").read()))
        return 0
    text, scale, out, manifest = "CATALOGIZER LOGIN", 8, None, None
    i = 1
    while i < len(argv):
        a = argv[i]
        if a == "--text": text = argv[i + 1]; i += 2
        elif a == "--scale": scale = int(argv[i + 1]); i += 2
        elif a == "--out": out = argv[i + 1]; i += 2
        elif a == "--manifest": manifest = argv[i + 1]; i += 2
        else: raise SystemExit("usage: see header")
    if not out:
        raise SystemExit("--out required")
    w, h, rows = render(text, scale)
    png = png_bytes(w, h, rows)
    open(out, "wb").write(png)
    if manifest:
        json.dump({"text": text, "scale": scale, "width": w, "height": h, "pixel_sha256": pixel_hash(png),
                   "oracle": "ground truth by construction (block-font renderer), not a model"},
                  open(manifest, "w"), indent=2, sort_keys=True)
        open(manifest, "a").write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
