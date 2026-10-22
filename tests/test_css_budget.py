from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from scripts.css_audit import (
    BUDGET_PATH,
    MANIFEST,
    STYLES_DIR,
    compare,
    compute_metrics,
    load_budget,
    manifest_imports,
    parse_css,
    split_top_level,
)


class CssParserTests(unittest.TestCase):
    def test_selector_lists_split_only_on_top_level_commas(self) -> None:
        sheet = parse_css("a.css", ".a, html :is(.b, .c) .d { color: red; }\n")
        self.assertEqual(sheet.rules[0].selectors, [".a", "html :is(.b, .c) .d"])

    def test_nested_media_records_context_and_line(self) -> None:
        sheet = parse_css("a.css", "@media (max-width: 600px) {\n  @supports (display: grid) {\n    .x { gap: 4px; }\n  }\n}\n")
        rule = sheet.rules[0]
        self.assertEqual(rule.context, ("@media (max-width: 600px)", "@supports (display: grid)"))
        self.assertEqual(rule.line, 3)
        self.assertEqual(rule.declarations[0].line, 3)

    def test_comments_with_braces_do_not_break_parsing(self) -> None:
        sheet = parse_css("a.css", "/* .ghost { color: red; } */\n.real { color: blue; }\n")
        self.assertEqual([r.selector for r in sheet.rules], [".real"])
        self.assertEqual(sheet.rules[0].line, 2)

    def test_keyframes_hold_no_rules(self) -> None:
        sheet = parse_css("a.css", "@keyframes spin { from { opacity: 0; } to { opacity: 1; } }\n.x { color: red; }\n")
        self.assertEqual([r.selector for r in sheet.rules], [".x"])

    def test_values_keep_semicolons_inside_strings_and_parens(self) -> None:
        sheet = parse_css("a.css", '.x { content: "a;b"; background: url("data:image/svg+xml;utf8,x"); }\n')
        self.assertEqual([d.prop for d in sheet.rules[0].declarations], ["content", "background"])

    def test_design_allow_marks_declaration(self) -> None:
        sheet = parse_css("a.css", ".x {\n  color: #fff; /* design-allow: logo ink */\n  background: #000;\n}\n")
        allowed = {d.prop: d.allowed for d in sheet.rules[0].declarations}
        self.assertEqual(allowed, {"color": True, "background": False})

    def test_split_top_level_ignores_nested_separators(self) -> None:
        self.assertEqual(split_top_level("a(1,2), b[x,y], 'c,d'", ","), ["a(1,2)", " b[x,y]", " 'c,d'"])


class CssMetricTests(unittest.TestCase):
    def _metrics(self, files: dict[str, str], manifest: str) -> dict[str, int]:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            styles = root / "styles"
            styles.mkdir()
            for name, text in files.items():
                (styles / name).write_text(text, encoding="utf-8")
            (root / "styles.css").write_text(manifest, encoding="utf-8")
            metrics, _ = compute_metrics(styles, root / "styles.css")
            return metrics

    def test_metrics_count_slop_patterns(self) -> None:
        metrics = self._metrics(
            {
                "tokens.css": ":root { --text: #111; --radius: 4px; }\n",
                "feature.css": (
                    ".a { color: #fff; border-radius: 999px; text-transform: uppercase; }\n"
                    ".b { color: var(--text); background: var(--missing, #eee) !important; }\n"
                    'html[data-theme="dark"] .a { color: var(--text); }\n'
                ),
                "other.css": ".a { font-size: 13px; box-shadow: 0 4px 12px rgb(0 0 0 / 20%); }\n",
            },
            '@import url("/styles/tokens.css") layer(tokens);\n@import url("/styles/feature.css");\n'
            '@import url("/styles/other.css");\n',
        )
        self.assertEqual(metrics["colour_literals"], 3)
        self.assertEqual(metrics["pill_radius"], 1)
        self.assertEqual(metrics["uppercase"], 1)
        self.assertEqual(metrics["important"], 1)
        self.assertEqual(metrics["var_fallback_literals"], 1)
        self.assertEqual(metrics["undefined_custom_properties"], 1)
        self.assertEqual(metrics["dark_override_rules"], 1)
        self.assertEqual(metrics["cross_file_duplicate_selectors"], 1)
        self.assertEqual(metrics["blurred_shadows"], 1)
        self.assertEqual(metrics["font_size_literals"], 1)
        self.assertEqual(metrics["unlayered_imports"], 2)

    def test_compare_reports_regressions_and_unrecorded_improvements(self) -> None:
        budget = {"important": 5, "uppercase": 5}
        metrics = {name: 0 for name in compute_metrics()[0]}
        metrics.update(important=6, uppercase=4)
        regressions, improvements = compare(metrics, {**{k: 0 for k in metrics}, **budget})
        self.assertTrue(any(r.startswith("important") for r in regressions))
        self.assertTrue(any(i.startswith("uppercase") for i in improvements))


class CssBudgetTests(unittest.TestCase):
    def test_repository_css_matches_budget(self) -> None:
        metrics, _ = compute_metrics()
        regressions, improvements = compare(metrics, load_budget(BUDGET_PATH))
        self.assertEqual(regressions, [], "CSS quality regressed; fix the change instead of raising the budget")
        self.assertEqual(improvements, [], "CSS improved; record it with: python scripts/css_audit.py --ratchet")

    def test_manifest_imports_every_module_exactly_once(self) -> None:
        imported = [name for name, _ in manifest_imports(MANIFEST.read_text(encoding="utf-8"))]
        modules = sorted(p.name for p in STYLES_DIR.glob("*.css"))
        self.assertEqual(len(imported), len(set(imported)), "a module is imported twice")
        self.assertEqual(sorted(imported), modules, "styles.css and styles/ disagree")


if __name__ == "__main__":
    unittest.main()
