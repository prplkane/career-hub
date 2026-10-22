"""Build the Windows app icon from the Steel briefcase pixel-art SVG.

The SVG is a 16x16 grid of one-pixel rects, so it is rasterised exactly and
scaled by whole multiples; no renderer or image library is needed.
"""
from __future__ import annotations

import argparse
import re
import struct
import sys
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "src" / "intake_ui_static" / "favicon.svg"
TARGET = ROOT / "desktop" / "assets" / "career-hub.ico"
GRID = 16
SIZES = (16, 32, 48, 64, 128, 256)  # Whole multiples keep pixel edges crisp.
TOKEN = re.compile(r"<g\b([^>]*)>|</g>|<rect\b([^>]*)/>")
ATTRIBUTE = re.compile(r'([\w-]+)="([^"]*)"')
COLOUR = re.compile(r"#([0-9a-fA-F]{6})\Z")


def rasterise(svg: str) -> list[list[bytes]]:
    if 'viewBox="0 0 16 16"' not in svg:
        raise ValueError("The icon source must be a 16x16 viewBox.")
    grid = [[b"\0\0\0\0"] * GRID for _ in range(GRID)]
    fills: list[str | None] = [None]
    for match in TOKEN.finditer(svg):
        group, rect = match.groups()
        if match.group(0) == "</g>":
            fills.pop()
            continue
        attributes = dict(ATTRIBUTE.findall(group if group is not None else rect))
        fill = attributes.get("fill", fills[-1])
        if group is not None:
            fills.append(fill)
            continue
        colour = COLOUR.fullmatch(fill or "")
        if colour is None:
            raise ValueError(f"Unsupported fill {fill!r}.")
        pixel = bytes.fromhex(colour.group(1)) + b"\xff"
        x, y = int(attributes.get("x", 0)), int(attributes.get("y", 0))
        for row in range(y, y + int(attributes["height"])):
            for column in range(x, x + int(attributes["width"])):
                grid[row][column] = pixel
    return grid


def png(grid: list[list[bytes]], size: int) -> bytes:
    scale = size // GRID
    rows = b"".join(b"\0" + b"".join(pixel * scale for pixel in line)
                    for line in grid for _ in range(scale))

    def chunk(kind: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))

    header = struct.pack(">IIBBBBB", size, size, 8, 6, 0, 0, 0)  # 8-bit RGBA.
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header)
            + chunk(b"IDAT", zlib.compress(rows, 9)) + chunk(b"IEND", b""))


def build(svg: str) -> bytes:
    grid = rasterise(svg)
    images = [png(grid, size) for size in SIZES]
    offset = 6 + 16 * len(images)
    directory = b""
    for size, image in zip(SIZES, images):
        dimension = 0 if size == 256 else size  # ICO stores 256 as 0.
        directory += struct.pack("<BBBBHHII", dimension, dimension, 0, 0, 1, 32, len(image), offset)
        offset += len(image)
    return struct.pack("<HHH", 0, 1, len(images)) + directory + b"".join(images)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="Fail if the committed icon is out of date")
    args = parser.parse_args()
    icon = build(SOURCE.read_text(encoding="utf-8"))
    if args.check:
        if not TARGET.exists() or TARGET.read_bytes() != icon:
            print("ERROR desktop/assets/career-hub.ico is out of date; run python -m scripts.build_app_icon.", file=sys.stderr)
            return 1
        print("OK    desktop/assets/career-hub.ico matches favicon.svg.")
        return 0
    TARGET.parent.mkdir(parents=True, exist_ok=True)
    TARGET.write_bytes(icon)
    print(f"WROTE {TARGET.relative_to(ROOT).as_posix()} ({len(icon)} bytes, sizes {', '.join(map(str, SIZES))}).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
