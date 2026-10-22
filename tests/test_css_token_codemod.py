from __future__ import annotations

import unittest

from scripts.css_token_codemod import classify, delta_e, parse_colour

PALETTE = {
    "surface": (255, 255, 255),
    "surface-raised": (255, 255, 255),
    "surface-muted": (237, 241, 242),
    "text": (23, 33, 38),
    "muted": (97, 112, 120),
    "accent": (8, 127, 107),
    "accent-text": (6, 105, 88),
    "border": (211, 218, 222),
    "border-subtle": (229, 234, 237),
    "success": (24, 121, 78),
    "status-success-bg": (234, 247, 239),
}


class ColourParsingTests(unittest.TestCase):
    def test_parses_hex_and_rgb_forms(self) -> None:
        self.assertEqual(parse_colour("#fff"), (255, 255, 255, 1.0))
        self.assertEqual(parse_colour("#087f6b"), (8, 127, 107, 1.0))
        self.assertEqual(parse_colour("rgb(8 127 107 / 50%)"), (8.0, 127.0, 107.0, 0.5))
        self.assertEqual(parse_colour("rgba(255, 255, 255, 0.05)")[3], 0.05)
        self.assertIsNone(parse_colour("hsl(10 20% 30%)"))

    def test_delta_e_is_zero_for_identical_and_large_for_opposites(self) -> None:
        self.assertEqual(delta_e((10, 20, 30), (10, 20, 30)), 0.0)
        self.assertGreater(delta_e((0, 0, 0), (255, 255, 255)), 99)


class ClassificationTests(unittest.TestCase):
    def test_close_unambiguous_match_is_auto(self) -> None:
        bucket, token, *_ = classify("#627079", "color", PALETTE)
        self.assertEqual((bucket, token), ("AUTO", "muted"))

    def test_translucent_and_unknown_roles_are_unmapped(self) -> None:
        self.assertEqual(classify("rgba(0, 0, 0, 0.2)", "color", PALETTE)[0], "UNMAPPED")
        self.assertEqual(classify("#123456", "box-shadow", PALETTE)[0], "UNMAPPED")

    def test_white_background_maps_to_surface(self) -> None:
        self.assertEqual(classify("#ffffff", "background", PALETTE)[:2], ("AUTO", "surface"))

    def test_white_or_near_white_text_needs_review_unless_on_accent(self) -> None:
        self.assertEqual(classify("#fff", "color", PALETTE)[0], "REVIEW")
        self.assertEqual(classify("#f5fbfa", "color", PALETTE)[0], "REVIEW")
        self.assertEqual(classify("#f5fbfa", "color", PALETTE, accent_background=True)[:2], ("AUTO", "text-on-accent"))

    def test_dark_override_hint_wins_when_close(self) -> None:
        self.assertEqual(classify("#ffffff", "background", PALETTE, hint="surface-raised")[:2], ("AUTO", "surface-raised"))
        self.assertNotEqual(classify("#102030", "background", PALETTE, hint="surface-raised")[1], "surface-raised")

    def test_status_selector_with_non_status_match_goes_to_review(self) -> None:
        bucket, token, *_ = classify("#16806e", "border-left-color", PALETTE, selector=".conclusion--success")
        self.assertEqual((bucket, token), ("REVIEW", "accent"))
        self.assertEqual(classify("#eaf7ef", "background", PALETTE, selector=".plan.is-ready")[:2], ("AUTO", "status-success-bg"))

    def test_ambiguous_match_goes_to_review(self) -> None:
        bucket, *_ = classify("#dce2e5", "border", PALETTE)
        self.assertEqual(bucket, "REVIEW")


if __name__ == "__main__":
    unittest.main()
