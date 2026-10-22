"""Snap raw corner radii and font sizes onto the design scales.

Usage:
    python scripts/css_scale_codemod.py --radius --report
    python scripts/css_scale_codemod.py --radius --apply
    python scripts/css_scale_codemod.py --fonts --report
    python scripts/css_scale_codemod.py --fonts --apply

The mapping tables are fixed so the result is deterministic. Values the
tables cannot express (clamp(), em, unusual rem) are reported as REVIEW and
left untouched.

Radius scale: --radius-sm 2px, --radius 4px, --radius-lg 6px.
    1-2px -> sm, 3-4px -> radius, 5-8px -> lg, pills (>= 9px) -> sm,
    50% -> sm (indicator dots become square lights), except avatars, which
    stay round with a design-allow comment.
Type scale: --text-xs 11px, --text-sm 12px, --text-base 14px, --text-md 16px,
    --text-lg 20px, --text-xl 28px.
"""

from __future__ import annotations

import argparse
import re
import sys

from scripts.css_audit import STYLES_DIR, TOKEN_FILES, load_stylesheets

LENGTH = re.compile(r"(?<![\w.-])(\d*\.?\d+)(px|%|rem)(?![\w-])")
ROUND_KEEP = ("avatar",)


def radius_token(number: float, unit: str, selector: str) -> str | None:
    if unit == "%":
        if number != 50:
            return None
        if any(word in selector for word in ROUND_KEEP):
            return "KEEP"
        return "var(--radius-sm)"
    if unit != "px":
        return None
    if number == 0:
        return "0"
    if number <= 2:
        return "var(--radius-sm)"
    if number <= 4:
        return "var(--radius)"
    if number <= 8:
        return "var(--radius-lg)"
    return "var(--radius-sm)"


def font_token(number: float, unit: str) -> str | None:
    px = number if unit == "px" else number * 16 if unit == "rem" else None
    if px is None:
        return None
    for limit, token in ((11.5, "xs"), (12.5, "sm"), (14, "base"), (17, "md"), (22, "lg")):
        if px <= limit:
            return f"var(--text-{token})"
    return "var(--text-xl)"


def convert(value: str, prop: str, selector: str) -> tuple[str | None, str]:
    """Return (new value or None when unchanged/review, reason)."""
    important = " !important" if "!important" in value else ""
    core = value.replace("!important", "").strip()
    if core.startswith("var(--text-") or core in ("inherit", "0") or re.fullmatch(r"var\(--radius(-sm|-md|-lg)?\)", core):
        return None, "already on scale"
    if prop == "font-size":
        match = LENGTH.fullmatch(core)
        if not match:
            return None, "REVIEW: not a single px/rem length"
        token = font_token(float(match.group(1)), match.group(2))
        return (token + important, "") if token else (None, "REVIEW")
    if core == "calc(var(--radius) + 2px)":
        return "var(--radius-lg)" + important, ""
    parts = core.split()
    out = []
    for part in parts:
        match = LENGTH.fullmatch(part)
        if not match:
            if re.fullmatch(r"var\(--radius(-sm|-md|-lg)?\)|0", part):
                out.append(part)
                continue
            return None, f"REVIEW: unsupported radius part {part!r}"
        token = radius_token(float(match.group(1)), match.group(2), selector)
        if token is None:
            return None, f"REVIEW: {part}"
        if token == "KEEP":
            return "KEEP", "round avatar kept"
        out.append(token)
    new = " ".join(out) + important
    return (new if new != value else None), ""


def plan(mode: str) -> list[tuple[str, int, str, str, str, str]]:
    rows = []
    for sheet in load_stylesheets():
        if sheet.name in TOKEN_FILES:
            continue
        for rule in sheet.rules:
            for decl in rule.declarations:
                is_radius = decl.prop == "border-radius" or (decl.prop.startswith("border-") and decl.prop.endswith("-radius"))
                if (mode == "radius" and is_radius) or (mode == "fonts" and decl.prop == "font-size"):
                    new, reason = convert(decl.value, decl.prop, rule.selector)
                    if new or reason.startswith("REVIEW") or reason.startswith("round"):
                        rows.append((sheet.name, decl.line, decl.prop, decl.value, new or "", reason))
    return rows


def apply(rows: list[tuple[str, int, str, str, str, str]]) -> int:
    changed = 0
    by_file: dict[str, list[tuple[int, str, str, str]]] = {}
    for name, line, prop, old, new, reason in rows:
        if new:
            by_file.setdefault(name, []).append((line, prop, old, new))
    for name, edits in by_file.items():
        path = STYLES_DIR / name
        lines = path.read_text(encoding="utf-8").split("\n")
        for line, prop, old, new in edits:
            text = lines[line - 1]
            pattern = re.compile(rf"({re.escape(prop)}\s*:\s*){re.escape(old)}")
            if new == "KEEP":
                if "design-allow" not in text:
                    lines[line - 1] = text.rstrip() + " /* design-allow: round avatar */"
                    changed += 1
                continue
            replaced, count = pattern.subn(lambda m: m.group(1) + new, text, count=1)
            if count:
                lines[line - 1] = replaced
                changed += 1
        with open(path, "w", encoding="utf-8", newline="\n") as handle:
            handle.write("\n".join(lines))
    return changed


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    scale = parser.add_mutually_exclusive_group(required=True)
    scale.add_argument("--radius", action="store_true")
    scale.add_argument("--fonts", action="store_true")
    action = parser.add_mutually_exclusive_group(required=True)
    action.add_argument("--report", action="store_true")
    action.add_argument("--apply", action="store_true")
    args = parser.parse_args(argv)
    rows = plan("radius" if args.radius else "fonts")
    if args.report:
        for name, line, prop, old, new, reason in rows:
            print(f"{name}:{line:<5} {prop:14} {old:28} -> {new or '-':24} {reason}")
        print(f"{sum(1 for r in rows if r[4])} to change, {sum(1 for r in rows if r[5].startswith('REVIEW'))} REVIEW")
        return 0
    print(f"changed {apply(rows)} declarations")
    return 0


if __name__ == "__main__":
    sys.exit(main())
