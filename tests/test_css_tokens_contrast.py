from __future__ import annotations

import re
import unittest

from scripts.css_audit import STYLES_DIR, parse_css

TOKENS = STYLES_DIR / "tokens.css"
DARK = 'html[data-theme="dark"]'

# (foreground, background, minimum ratio). A background written as "a over b"
# composites a translucent token over an opaque one.
TEXT = 4.5
UI = 3.0
PAIRS = [
    *[("text", bg, TEXT) for bg in ("background", "surface", "surface-raised", "surface-selected")],
    *[("muted", bg, TEXT) for bg in ("background", "surface", "surface-muted", "surface-hover")],
    *[("accent-text", bg, TEXT) for bg in ("background", "surface-selected", "accent-soft")],
    ("text-on-accent", "accent", TEXT),
    ("text-on-accent", "accent-hover", TEXT),
    ("success", "status-success-bg", TEXT),
    ("warning", "status-warning-bg", TEXT),
    ("error", "status-danger-bg", TEXT),
    ("status-info", "status-info-bg", TEXT),
    *[(status, "surface", TEXT) for status in ("success", "warning", "error", "status-info")],
    ("error", "background", TEXT),
    ("sidebar-text", "sidebar", TEXT),
    ("sidebar-muted", "sidebar", TEXT),
    ("sidebar-text", "sidebar-selected over sidebar", TEXT),
    ("sidebar-text", "sidebar-hover over sidebar", TEXT),
    ("brand-tile", "sidebar", UI),
    ("sidebar-marker", "sidebar", UI),
    ("focus", "background", UI),
    ("focus", "surface", UI),
    ("border-strong", "surface", UI),
]


def load_themes() -> dict[str, dict[str, str]]:
    sheet = parse_css("tokens.css", TOKENS.read_text(encoding="utf-8"))
    light: dict[str, str] = {}
    dark: dict[str, str] = {}
    for rule in sheet.rules:
        target = dark if rule.selector.strip() == DARK else light if rule.selector.strip() == ":root" else None
        if target is None:
            continue
        for decl in rule.declarations:
            if decl.prop.startswith("--"):
                target[decl.prop[2:]] = decl.value
    return {"light": light, "dark": {**light, **dark}}


def resolve(tokens: dict[str, str], name: str, seen: frozenset[str] = frozenset()) -> str:
    if name in seen:
        raise ValueError(f"circular token reference: {name}")
    value = tokens[name]
    match = re.fullmatch(r"var\(--([\w-]+)\)", value)
    return resolve(tokens, match.group(1), seen | {name}) if match else value


def rgba(value: str) -> tuple[float, float, float, float]:
    value = value.strip()
    if value.startswith("#"):
        hex_value = value[1:]
        if len(hex_value) == 3:
            hex_value = "".join(ch * 2 for ch in hex_value)
        return int(hex_value[0:2], 16), int(hex_value[2:4], 16), int(hex_value[4:6], 16), 1.0
    match = re.fullmatch(r"rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)\s*(?:[/,]\s*([\d.]+)(%?))?\s*\)", value)
    if not match:
        raise ValueError(f"unsupported colour {value!r}")
    alpha = 1.0 if match.group(4) is None else float(match.group(4)) / (100 if match.group(5) else 1)
    return float(match.group(1)), float(match.group(2)), float(match.group(3)), alpha


def colour(tokens: dict[str, str], spec: str) -> tuple[float, float, float]:
    if " over " in spec:
        top_name, base_name = spec.split(" over ")
        top, base = rgba(resolve(tokens, top_name)), colour(tokens, base_name)
        a = top[3]
        return tuple(top[i] * a + base[i] * (1 - a) for i in range(3))
    r, g, b, a = rgba(resolve(tokens, spec))
    if a < 1:
        raise ValueError(f"{spec} is translucent; compare it 'over' an opaque token")
    return r, g, b


def contrast(fg: tuple[float, float, float], bg: tuple[float, float, float]) -> float:
    def luminance(rgb: tuple[float, float, float]) -> float:
        channels = [c / 255 for c in rgb]
        linear = [c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4 for c in channels]
        return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2]

    high, low = sorted((luminance(fg), luminance(bg)), reverse=True)
    return (high + 0.05) / (low + 0.05)


class TokenContrastTests(unittest.TestCase):
    def test_every_required_pair_meets_its_minimum(self) -> None:
        failures = []
        for theme, tokens in load_themes().items():
            for fg, bg, minimum in PAIRS:
                ratio = contrast(colour(tokens, fg), colour(tokens, bg))
                if ratio < minimum:
                    failures.append(f"{theme}: --{fg} on --{bg} is {ratio:.2f}:1 (needs {minimum}:1)")
        self.assertEqual(failures, [])

    def test_aliases_resolve_without_cycles(self) -> None:
        for theme, tokens in load_themes().items():
            for name in tokens:
                with self.subTest(theme=theme, token=name):
                    resolve(tokens, name)

    def test_contrast_matches_known_values(self) -> None:
        self.assertAlmostEqual(contrast((255, 255, 255), (0, 0, 0)), 21.0, places=2)
        self.assertAlmostEqual(contrast((43, 90, 138), (255, 255, 255)), 7.16, places=1)


if __name__ == "__main__":
    unittest.main()
