"""Replace hard-coded colours with the design token they were imitating.

Usage:
    python scripts/css_token_codemod.py --report                 # all modules, CSV in %TEMP%/ch-css
    python scripts/css_token_codemod.py --report --file jobs.css
    python scripts/css_token_codemod.py --apply --file jobs.css  # rewrites AUTO rows only

Each colour literal is matched against the OLD palette (commit 1118ae1),
because the literals were written to imitate those values. The replacement
keeps the old token *name*, so the line picks up the new Ink & paper value
automatically. Buckets:
    AUTO      best match within dE 8 and the runner-up at least 3 further
    REVIEW    anything plausible but uncertain (left for a human or large model)
    UNMAPPED  no token within dE 20, translucent colours, shadows, gradients
A matching dark-mode override (`html[data-theme="dark"] ...` setting the same
property on the same selector to a token) is treated as the author's intent.
"""

from __future__ import annotations

import argparse
import csv
import math
import os
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

from scripts.css_audit import COLOR, DARK_PREFIX, REPO_ROOT, STYLES_DIR, TOKEN_FILES, load_stylesheets, parse_css, split_top_level

OLD_REVISION = "1118ae1"
OLD_SOURCES = ("foundation.css", "refinements.css", "themes.css")
AUTO_DE, REVIEW_DE, MARGIN, HINT_DE = 8.0, 20.0, 3.0, 12.0
REPORT_DIR = Path(os.environ.get("TEMP", "/tmp")) / "ch-css"

ROLE_CANDIDATES = {
    "background": ["background", "surface", "surface-raised", "surface-muted", "surface-subtle", "surface-hover",
                   "surface-selected", "accent-soft", "status-info-bg", "status-success-bg", "status-warning-bg",
                   "status-danger-bg", "accent", "accent-hover", "sidebar"],
    # text-on-accent is excluded on purpose: it flips dark in dark mode, so it is
    # only chosen when the same rule paints an accent background.
    "text": ["text", "muted", "accent-text", "success", "warning", "error", "status-info", "sidebar-muted"],
    "border": ["border", "border-strong", "border-subtle", "status-info-border", "status-success-border",
               "status-warning-border", "status-danger-border", "accent"],
}
WHITE = (255.0, 255.0, 255.0)
NEAR_WHITE_DE = 8.0
STATUS_WORDS = {"success": "success", "ready": "success", "warning": "warning", "danger": "error", "error": "error",
                "invalid": "error", "blocked": "error"}
STATUS_TOKENS = ("success", "warning", "error", "danger", "status-")


@dataclass
class Row:
    file: str
    line: int
    selector: str
    prop: str
    literal: str
    bucket: str
    token: str = ""
    delta: float = 0.0
    runner_up: str = ""
    reason: str = ""


def parse_colour(literal: str) -> tuple[float, float, float, float] | None:
    text = literal.strip().lower()
    if text.startswith("#"):
        digits = text[1:]
        if len(digits) in (3, 4):
            digits = "".join(ch * 2 for ch in digits)
        if len(digits) not in (6, 8):
            return None
        alpha = int(digits[6:8], 16) / 255 if len(digits) == 8 else 1.0
        return int(digits[0:2], 16), int(digits[2:4], 16), int(digits[4:6], 16), alpha
    match = re.fullmatch(r"rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)\s*(?:[/,]\s*([\d.]+)(%?))?\s*\)", text)
    if not match:
        return None
    alpha = 1.0 if match.group(4) is None else float(match.group(4)) / (100 if match.group(5) else 1)
    return float(match.group(1)), float(match.group(2)), float(match.group(3)), alpha


def lab(rgb: tuple[float, float, float]) -> tuple[float, float, float]:
    def linear(c: float) -> float:
        c /= 255
        return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4

    r, g, b = (linear(c) for c in rgb)
    x = (0.4124 * r + 0.3576 * g + 0.1805 * b) / 0.95047
    y = 0.2126 * r + 0.7152 * g + 0.0722 * b
    z = (0.0193 * r + 0.1192 * g + 0.9505 * b) / 1.08883

    def f(t: float) -> float:
        return t ** (1 / 3) if t > 0.008856 else 7.787 * t + 16 / 116

    fx, fy, fz = f(x), f(y), f(z)
    return 116 * fy - 16, 500 * (fx - fy), 200 * (fy - fz)


def delta_e(a: tuple[float, float, float], b: tuple[float, float, float]) -> float:
    return math.dist(lab(a), lab(b))


def role_of(prop: str) -> str | None:
    if prop in ("background", "background-color"):
        return "background"
    if prop in ("color", "fill", "stroke", "caret-color", "text-decoration-color", "accent-color"):
        return "text"
    if prop.startswith("border") or prop.startswith("outline") or prop.startswith("column-rule"):
        return "border"
    return None


def load_old_palette() -> dict[str, dict[str, tuple[float, float, float]]]:
    light: dict[str, str] = {}
    dark: dict[str, str] = {}
    for name in OLD_SOURCES:
        text = subprocess.run(["git", "show", f"{OLD_REVISION}:src/intake_ui_static/styles/{name}"], cwd=REPO_ROOT,
                              capture_output=True, text=True, encoding="utf-8", check=True).stdout
        for rule in parse_css(name, text).rules:
            target = light if rule.selector.strip() == ":root" else dark if rule.selector.strip() == DARK_PREFIX else None
            if target is not None:
                target.update({d.prop[2:]: d.value for d in rule.declarations if d.prop.startswith("--")})

    def colours(values: dict[str, str]) -> dict[str, tuple[float, float, float]]:
        out = {}
        for name, value in values.items():
            seen = set()
            while (m := re.fullmatch(r"var\(--([\w-]+)\)", value)) and m.group(1) in values and m.group(1) not in seen:
                seen.add(m.group(1))
                value = values[m.group(1)]
            parsed = parse_colour(value)
            if parsed and parsed[3] == 1:
                out[name] = parsed[:3]
        return out

    return {"light": colours(light), "dark": colours({**light, **dark})}


def dark_hints(sheets) -> dict[tuple[str, str], str]:
    """(selector, property) -> token from dark overrides that set var(--token)."""
    hints = {}
    for sheet in sheets:
        for rule in sheet.rules:
            for selector in rule.selectors:
                if not selector.startswith(DARK_PREFIX):
                    continue
                rest = selector[len(DARK_PREFIX):].strip()
                inner = re.fullmatch(r":is\((.*)\)", rest)
                targets = [" ".join(t.split()) for t in split_top_level(inner.group(1), ",")] if inner else [rest]
                for decl in rule.declarations:
                    match = re.fullmatch(r"var\(--([\w-]+)\)", decl.value.replace("!important", "").strip())
                    if match:
                        for target in targets:
                            hints[(target, decl.prop)] = match.group(1)
    return hints


def classify(literal: str, prop: str, palette: dict[str, tuple[float, float, float]], *,
             hint: str | None = None, accent_background: bool = False, selector: str = "") -> tuple[str, str, float, str, str]:
    """Return (bucket, token, delta, runner_up, reason)."""
    result = _classify(literal, prop, palette, hint=hint, accent_background=accent_background)
    bucket, token = result[0], result[1]
    words = [status for word, status in STATUS_WORDS.items() if re.search(rf"(?<![a-z]){word}(?![a-z])", selector.lower())]
    if bucket == "AUTO" and words and not token.startswith(STATUS_TOKENS):
        return "REVIEW", token, result[2], result[3], f"selector implies {words[0]} but the match is {token}"
    return result


def _classify(literal: str, prop: str, palette: dict[str, tuple[float, float, float]], *,
              hint: str | None, accent_background: bool) -> tuple[str, str, float, str, str]:
    parsed = parse_colour(literal)
    role = role_of(prop)
    if parsed is None:
        return "UNMAPPED", "", 0.0, "", "unsupported colour syntax"
    if parsed[3] < 1:
        return "UNMAPPED", "", 0.0, "", "translucent colour"
    if role is None:
        return "UNMAPPED", "", 0.0, "", f"property {prop} is not a plain colour role"
    rgb = parsed[:3]
    if hint and hint in palette and delta_e(rgb, palette[hint]) <= HINT_DE:
        return "AUTO", hint, delta_e(rgb, palette[hint]), "", "matches the dark-override token"
    if rgb == WHITE and role == "background":
        return "AUTO", "surface", 0.0, "", "white background is the old surface"
    if role != "background" and delta_e(rgb, WHITE) <= NEAR_WHITE_DE:
        if role == "text" and accent_background:
            return "AUTO", "text-on-accent", delta_e(rgb, WHITE), "", "white text on an accent background"
        return "REVIEW", "", delta_e(rgb, WHITE), "", "white-ish text or border needs a human decision"
    scored = sorted((delta_e(rgb, palette[name]), name) for name in ROLE_CANDIDATES[role] if name in palette)
    best_delta, best = scored[0]
    runner_delta, runner = scored[1] if len(scored) > 1 else (math.inf, "")
    runner_text = f"{runner} ({runner_delta:.1f})" if runner else ""
    if best_delta <= AUTO_DE and runner_delta - best_delta >= MARGIN:
        return "AUTO", best, best_delta, runner_text, ""
    if best_delta <= REVIEW_DE:
        return "REVIEW", best, best_delta, runner_text, "ambiguous or not close enough"
    return "UNMAPPED", best, best_delta, runner_text, "no token within dE 20"


def scan(file_filter: str | None = None) -> list[Row]:
    palettes = load_old_palette()
    sheets = load_stylesheets()
    hints = dark_hints(sheets)
    rows: list[Row] = []
    for sheet in sheets:
        if sheet.name in TOKEN_FILES or (file_filter and sheet.name != file_filter):
            continue
        for rule in sheet.rules:
            dark = any(s.startswith(DARK_PREFIX) for s in rule.selectors)
            palette = palettes["dark" if dark else "light"]
            accent_background = any(
                d.prop in ("background", "background-color") and "var(--accent" in d.value for d in rule.declarations
            )
            for decl in rule.declarations:
                if decl.allowed:
                    continue
                for match in COLOR.finditer(decl.value):
                    literal = match.group(0)
                    if literal.lower().startswith(("rgb", "hsl")):
                        end = decl.value.find(")", match.start())
                        literal = decl.value[match.start():end + 1]
                    if decl.prop.startswith("--"):
                        rows.append(Row(sheet.name, decl.line, rule.selector, decl.prop, literal, "REVIEW",
                                        reason="custom property defined outside tokens.css"))
                        continue
                    if any(word in decl.prop for word in ("shadow", "image")) or "gradient" in decl.value:
                        rows.append(Row(sheet.name, decl.line, rule.selector, decl.prop, literal, "UNMAPPED",
                                        reason="shadow or gradient"))
                        continue
                    hint = next((hints[(s, decl.prop)] for s in rule.selectors if (s, decl.prop) in hints), None)
                    bucket, token, delta, runner, reason = classify(
                        literal, decl.prop, palette, hint=hint, accent_background=accent_background,
                        selector=rule.selector,
                    )
                    rows.append(Row(sheet.name, decl.line, rule.selector, decl.prop, literal, bucket, token,
                                    round(delta, 2), runner, reason))
    return rows


def apply(file_name: str, rows: list[Row]) -> tuple[int, int]:
    """Rewrite AUTO rows and redundant var() fallbacks in one module. Returns (colours, fallbacks)."""
    path = STYLES_DIR / file_name
    lines = path.read_text(encoding="utf-8").split("\n")
    defined = {m for m in re.findall(r"(--[\w-]+)\s*:", (STYLES_DIR / "tokens.css").read_text(encoding="utf-8"))}
    replaced = 0
    for row in rows:
        if row.file != file_name or row.bucket != "AUTO":
            continue
        index = row.line - 1
        if row.literal not in lines[index]:
            continue
        lines[index] = lines[index].replace(row.literal, f"var(--{row.token})", 1)
        replaced += 1
    fallbacks = 0

    def strip(match: re.Match) -> str:
        nonlocal fallbacks
        if match.group(1) in defined:
            fallbacks += 1
            return f"var({match.group(1)})"
        return match.group(0)

    lines = [re.sub(r"var\(\s*(--[\w-]+)\s*,\s*[^()]*?\)", strip, line) for line in lines]
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        handle.write("\n".join(lines))
    return replaced, fallbacks


def write_report(rows: list[Row], name: str) -> Path:
    REPORT_DIR.mkdir(parents=True, exist_ok=True)
    path = REPORT_DIR / f"codemod-{name}.csv"
    with open(path, "w", encoding="utf-8", newline="") as handle:
        writer = csv.writer(handle)
        writer.writerow(["file", "line", "selector", "prop", "literal", "bucket", "token", "dE", "runner_up", "reason"])
        for row in rows:
            writer.writerow([row.file, row.line, row.selector[:80], row.prop, row.literal, row.bucket, row.token,
                             row.delta, row.runner_up, row.reason])
    return path


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--report", action="store_true")
    mode.add_argument("--apply", action="store_true")
    parser.add_argument("--file", help="limit to one module in styles/")
    args = parser.parse_args(argv)
    if args.apply and not args.file:
        parser.error("--apply requires --file")

    rows = scan(args.file)
    counts = {b: sum(1 for r in rows if r.bucket == b) for b in ("AUTO", "REVIEW", "UNMAPPED")}
    if args.report:
        path = write_report(rows, args.file.removesuffix(".css") if args.file else "all")
        print(f"{counts}  report: {path}")
        return 0
    replaced, fallbacks = apply(args.file, rows)
    print(f"{args.file}: replaced {replaced} AUTO colours, stripped {fallbacks} fallbacks; left {counts['REVIEW']} REVIEW, "
          f"{counts['UNMAPPED']} UNMAPPED")
    return 0


if __name__ == "__main__":
    sys.exit(main())
