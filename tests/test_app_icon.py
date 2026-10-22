"""The Windows app icon stays a faithful, current build of the briefcase SVG."""
import struct
import unittest
import zlib

from scripts.build_app_icon import SIZES, SOURCE, TARGET, build, rasterise


class AppIconTests(unittest.TestCase):
    def test_committed_icon_matches_the_svg(self):
        self.assertEqual(TARGET.read_bytes(), build(SOURCE.read_text(encoding="utf-8")),
                         "Run python -m scripts.build_app_icon after changing favicon.svg.")

    def test_icon_holds_every_size_as_exact_scaled_pixels(self):
        icon = TARGET.read_bytes()
        reserved, kind, count = struct.unpack_from("<HHH", icon)
        self.assertEqual((reserved, kind, count), (0, 1, len(SIZES)))
        grid = rasterise(SOURCE.read_text(encoding="utf-8"))
        for index, size in enumerate(SIZES):
            width, height, _, _, planes, bits, length, offset = struct.unpack_from("<BBBBHHII", icon, 6 + 16 * index)
            self.assertEqual((width or 256, height or 256, planes, bits), (size, size, 1, 32))
            image = icon[offset:offset + length]
            self.assertTrue(image.startswith(b"\x89PNG\r\n\x1a\n"))
            self.assertEqual(struct.unpack(">II", image[16:24]), (size, size))
            # One IDAT chunk follows IHDR; check a pixel inside the handle and one outside.
            idat_length = struct.unpack(">I", image[33:37])[0]
            rows = zlib.decompress(image[41:41 + idat_length])
            stride = 1 + size * 4
            scale = size // 16
            for row, column in [(3, 6), (0, 0), (8, 7)]:
                start = (row * scale) * stride + 1 + (column * scale) * 4
                self.assertEqual(rows[start:start + 4], grid[row][column], (size, row, column))

    def test_unsupported_sources_fail_loudly(self):
        with self.assertRaises(ValueError):
            rasterise('<svg viewBox="0 0 32 32"></svg>')
        with self.assertRaises(ValueError):
            rasterise('<svg viewBox="0 0 16 16"><rect width="1" height="1" fill="red"/></svg>')


if __name__ == "__main__":
    unittest.main()
