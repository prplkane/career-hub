"""Audit the Career Hub stylesheets against the design budget.

Usage:
    python scripts/css_audit.py --json            # print every metric
    python scripts/css_audit.py --check           # compare with scripts/css_budget.json
    python scripts/css_audit.py --ratchet         # lower the budget to current values
    python scripts/css_audit.py --list colour_literals
    python scripts/css_audit.py --selector primary-button
    python scripts/css_audit.py --dark-redundancy

Enforced metrics form a ratchet: they may only go down, and an improvement
must be recorded with --ratchet so the budget stays tight. Size metrics are
reported for information only, because legitimate slices can grow a file.
A line containing a `design-allow` comment is exempt from colour checks.
"""

from __future__ import annotations

import argparse
import bisect
import json
import re
import sys
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
STATIC_ROOT = REPO_ROOT / "src" / "intake_ui_static"
STYLES_DIR = STATIC_ROOT / "styles"
MANIFEST = STATIC_ROOT / "styles.css"
BUDGET_PATH = REPO_ROOT / "scripts" / "css_budget.json"
TOKEN_FILES = frozenset({"tokens.css"})

INFORMATIONAL = ("lines_total", "max_module_lines", "module_count")
ENFORCED = (
    "colour_literals",
    "important",
    "pill_radius",
    "circle_radius",
    "radius_over_6",
    "distinct_radius_values",
    "distinct_font_sizes",
    "font_size_literals",
    "uppercase",
    "gradients",
    "blurred_shadows",
    "var_fallback_literals",
    "undefined_custom_properties",
    "cross_file_duplicate_selectors",
    "dark_override_rules",
    "unlayered_imports",
)

COLOR = re.compile(
    r"(?<![\w&])#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})(?![\w-])|\b(?:rgba?|hsla?)\(\s*[\d.]"
)
NAMED_BW = re.compile(r"\b(?:white|black)\b", re.I)
COLOUR_PROPS = re.compile(
    r"^(?:color|background(?:-color)?|border(?:-(?:top|right|bottom|left))?(?:-color)?|outline(?:-color)?|"
    r"fill|stroke|box-shadow|text-shadow|caret-color|accent-color|column-rule(?:-color)?|text-decoration(?:-color)?)$"
)
LENGTH = re.compile(r"(-?\d*\.?\d+)(px|rem|em|%)")
SHADOW_LENGTH = re.compile(r"-?\d*\.?\d+(?:px)?")
VAR_USE = re.compile(r"var\(\s*(--[\w-]+)")
VAR_FALLBACK_LITERAL = re.compile(r"var\(\s*--[\w-]+\s*,\s*(?!var\()[^)\s]")
GRADIENT = re.compile(r"\b(?:repeating-)?(?:linear|radial|conic)-gradient\(", re.I)
IMPORT = re.compile(r"@import\s+(?:url\(\s*)?[\"']([^\"']+)[\"']\s*\)?([^;]*);")
DARK_PREFIX = 'html[data-theme="dark"]'
GROUPING_AT_RULES = ("@media", "@supports", "@layer", "@container", "@document")
SKIPPED_AT_RULES = ("@keyframes", "@-webkit-keyframes", "@font-face", "@page", "@property")


@dataclass
class Declaration:
    prop: str
    value: str
    line: int
    allowed: bool


@dataclass
class Rule:
    file: str
    line: int
    selector: str
    context: tuple[str, ...]
    order: int
    declarations: list[Declaration] = field(default_factory=list)

    @property
    def selectors(self) -> list[str]:
        return [" ".join(part.split()) for part in split_top_level(self.selector, ",") if part.strip()]


@dataclass
class Stylesheet:
    name: str
    lines: int
    rules: list[Rule]
    statements: list[tuple[int, str]]


def split_top_level(text: str, separator: str) -> list[str]:
    """Split on a separator that is not inside parentheses, brackets, or strings."""
    parts, depth, quote, start = [], 0, "", 0
    for index, char in enumerate(text):
        if quote:
            if char == quote and text[index - 1] != "\\":
                quote = ""
        elif char in "\"'":
            quote = char
        elif char in "([":
            depth += 1
        elif char in ")]":
            depth -= 1
        elif char == separator and depth == 0:
            parts.append(text[start:index])
            start = index + 1
    parts.append(text[start:])
    return parts


def strip_comments(text: str) -> str:
    """Blank out comments while keeping every newline, so line numbers survive."""
    return re.sub(r"/\*.*?\*/", lambda m: re.sub(r"[^\n]", " ", m.group(0)), text, flags=re.S)


def parse_css(name: str, raw: str, order_start: int = 0) -> Stylesheet:
    allowed_lines = {number for number, line in enumerate(raw.splitlines(), 1) if "design-allow" in line}
    text = strip_comments(raw)
    newlines = [index for index, char in enumerate(text) if char == "\n"]

    def line_of(offset: int) -> int:
        return bisect.bisect_right(newlines, offset - 1) + 1

    rules: list[Rule] = []
    statements: list[tuple[int, str]] = []
    counter = [order_start]

    def matching_brace(start: int) -> int:
        depth, quote = 0, ""
        for index in range(start, len(text)):
            char = text[index]
            if quote:
                if char == quote and text[index - 1] != "\\":
                    quote = ""
            elif char in "\"'":
                quote = char
            elif char == "{":
                depth += 1
            elif char == "}":
                depth -= 1
                if depth == 0:
                    return index
        raise ValueError(f"{name}: unbalanced braces starting near line {line_of(start)}")

    def parse_declarations(rule: Rule, start: int, end: int) -> None:
        offset = start
        for chunk in split_top_level(text[start:end], ";"):
            if ":" in chunk:
                prop, value = chunk.split(":", 1)
                stripped = len(chunk) - len(chunk.lstrip())
                line = line_of(offset + stripped)
                if prop.strip():
                    rule.declarations.append(
                        Declaration(prop.strip().lower(), " ".join(value.split()), line, line in allowed_lines)
                    )
            offset += len(chunk) + 1

    def parse_block(start: int, end: int, context: tuple[str, ...]) -> None:
        index, prelude_start, depth, quote = start, start, 0, ""
        while index < end:
            char = text[index]
            if quote:
                if char == quote and text[index - 1] != "\\":
                    quote = ""
            elif char in "\"'":
                quote = char
            elif char == "(":
                depth += 1
            elif char == ")":
                depth -= 1
            elif char == ";" and depth == 0:
                statement = text[prelude_start:index].strip()
                if statement:
                    statements.append((line_of(prelude_start + text[prelude_start:index].find(statement[0])), statement))
                prelude_start = index + 1
            elif char == "{" and depth == 0:
                prelude = " ".join(text[prelude_start:index].split())
                close = matching_brace(index)
                lowered = prelude.lower()
                if lowered.startswith(GROUPING_AT_RULES):
                    parse_block(index + 1, close, context + (prelude,))
                elif lowered.startswith("@"):
                    pass  # keyframes, font-face and unknown at-rules hold no selectors
                elif prelude:
                    first = prelude_start + len(text[prelude_start:index]) - len(text[prelude_start:index].lstrip())
                    counter[0] += 1
                    rule = Rule(name, line_of(first), prelude, context, counter[0])
                    parse_declarations(rule, index + 1, close)
                    rules.append(rule)
                index = close
                prelude_start = close + 1
            index += 1

    parse_block(0, len(text), ())
    return Stylesheet(name, raw.count("\n") + (0 if raw.endswith("\n") else 1), rules, statements)


def manifest_imports(manifest_text: str) -> list[tuple[str, str]]:
    return [(Path(path).name, extra.strip()) for path, extra in IMPORT.findall(strip_comments(manifest_text))]


def load_stylesheets(styles_dir: Path = STYLES_DIR, manifest: Path = MANIFEST) -> list[Stylesheet]:
    """Load modules in manifest order (cascade order), then any unlisted files."""
    ordered = [name for name, _ in manifest_imports(manifest.read_text(encoding="utf-8"))] if manifest.exists() else []
    names = ordered + sorted(p.name for p in styles_dir.glob("*.css") if p.name not in ordered)
    sheets, order = [], 0
    for name in names:
        path = styles_dir / name
        if not path.exists():
            continue
        sheet = parse_css(name, path.read_text(encoding="utf-8"), order)
        order += len(sheet.rules) + 1
        sheets.append(sheet)
    return sheets


def _occurrences(sheets: list[Stylesheet], manifest_text: str) -> dict[str, list[str]]:
    found: dict[str, list[str]] = defaultdict(list)
    declared, used = set(), defaultdict(list)
    selector_files: dict[str, set[str]] = defaultdict(set)
    radius_values, font_values = defaultdict(list), defaultdict(list)

    for sheet in sheets:
        is_tokens = sheet.name in TOKEN_FILES
        for rule in sheet.rules:
            where = f"{sheet.name}:{rule.line}"
            for selector in rule.selectors:
                selector_files[selector].add(sheet.name)
            if not is_tokens and any(s.startswith(DARK_PREFIX) for s in rule.selectors):
                found["dark_override_rules"].append(where)
            for decl in rule.declarations:
                at = f"{sheet.name}:{decl.line}"
                value = decl.value
                if decl.prop.startswith("--"):
                    declared.add(decl.prop)
                for name in VAR_USE.findall(value):
                    used[name].append(at)
                if "!important" in value:
                    found["important"].append(at)
                if not is_tokens and not decl.allowed:
                    colours = COLOR.findall(value)
                    if COLOUR_PROPS.match(decl.prop) or decl.prop.startswith("--"):
                        colours += NAMED_BW.findall(value)
                    found["colour_literals"].extend([at] * len(colours))
                    if GRADIENT.search(value):
                        found["gradients"].append(at)
                for _ in VAR_FALLBACK_LITERAL.findall(value):
                    found["var_fallback_literals"].append(at)
                if decl.prop == "border-radius" or (decl.prop.startswith("border-") and decl.prop.endswith("-radius")):
                    lengths = [(float(n), unit) for n, unit in LENGTH.findall(value)]
                    if any(unit == "px" and n >= 10 for n, unit in lengths):
                        found["pill_radius"].append(at)
                    if any(unit == "%" and n == 50 for n, unit in lengths):
                        found["circle_radius"].append(at)
                    if any(unit == "px" and n > 6 for n, unit in lengths):
                        found["radius_over_6"].append(at)
                    normal = value.replace("!important", "").strip()
                    if normal not in {"0", "0px"} and not re.fullmatch(r"var\(--radius[\w-]*\)", normal):
                        radius_values[normal].append(at)
                if decl.prop == "font-size":
                    normal = value.replace("!important", "").strip()
                    if not normal.startswith("var(--text-") and normal != "inherit":
                        found["font_size_literals"].append(at)
                    if "var(" not in normal and normal != "inherit":
                        font_values[normal].append(at)
                if decl.prop == "text-transform" and "uppercase" in value:
                    found["uppercase"].append(at)
                if decl.prop == "box-shadow" and "var(" not in value:
                    for shadow in split_top_level(value, ","):
                        bare = re.sub(r"\w+\([^)]*\)|#[0-9a-fA-F]+", " ", shadow)
                        lengths = [float(t[:-2] if t.endswith("px") else t) for t in bare.split() if SHADOW_LENGTH.fullmatch(t)]
                        if len(lengths) >= 3 and lengths[2] > 0:
                            found["blurred_shadows"].append(at)
                            break

    for name, places in used.items():
        if name not in declared:
            found["undefined_custom_properties"].append(f"{name} (first used {places[0]})")
    for selector, files in selector_files.items():
        if len(files) > 1:
            found["cross_file_duplicate_selectors"].append(f"{selector}  [{', '.join(sorted(files))}]")
    found["distinct_radius_values"] = [f"{v}  ({len(p)}x, e.g. {p[0]})" for v, p in sorted(radius_values.items())]
    found["distinct_font_sizes"] = [f"{v}  ({len(p)}x, e.g. {p[0]})" for v, p in sorted(font_values.items())]
    for name, extra in manifest_imports(manifest_text):
        if "layer(" not in extra:
            found["unlayered_imports"].append(name)
    return found


def compute_metrics(styles_dir: Path = STYLES_DIR, manifest: Path = MANIFEST) -> tuple[dict[str, int], dict[str, list[str]]]:
    sheets = load_stylesheets(styles_dir, manifest)
    manifest_text = manifest.read_text(encoding="utf-8") if manifest.exists() else ""
    occurrences = _occurrences(sheets, manifest_text)
    metrics = {
        "lines_total": sum(s.lines for s in sheets),
        "max_module_lines": max((s.lines for s in sheets), default=0),
        "module_count": len(sheets),
    }
    for name in ENFORCED:
        metrics[name] = len(occurrences.get(name, []))
    return metrics, occurrences


def load_budget(path: Path = BUDGET_PATH) -> dict[str, int]:
    return json.loads(path.read_text(encoding="utf-8"))["enforced"]


def compare(metrics: dict[str, int], budget: dict[str, int]) -> tuple[list[str], list[str]]:
    """Return (regressions, unrecorded improvements)."""
    regressions = [f"{k}: {metrics[k]} > budget {budget[k]}" for k in ENFORCED if metrics[k] > budget.get(k, 0)]
    improvements = [f"{k}: {metrics[k]} < budget {budget[k]}" for k in ENFORCED if k in budget and metrics[k] < budget[k]]
    return regressions, improvements


def write_budget(metrics: dict[str, int], path: Path = BUDGET_PATH) -> None:
    payload = {
        "_note": "Ratchet: enforced values may only go down. Update with: python scripts/css_audit.py --ratchet",
        "enforced": {k: metrics[k] for k in ENFORCED},
        "informational": {k: metrics[k] for k in INFORMATIONAL},
    }
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        handle.write(json.dumps(payload, indent=2) + "\n")


def specificity(selector: str) -> tuple[int, int, int]:
    """Approximate (ids, classes/attributes/pseudo-classes, elements)."""
    body = re.sub(r"::[\w-]+", " e ", selector)
    ids = len(re.findall(r"#[\w-]+", body))
    classes = len(re.findall(r"\.[\w-]+|\[[^\]]*\]|:(?!is\(|where\(|not\(|has\()[\w-]+", body))
    elements = len(re.findall(r"(?:^|[\s>+~(,])([a-zA-Z][\w-]*)", body)) + body.count(" e ")
    return ids, classes, elements


def selector_report(sheets: list[Stylesheet], class_name: str) -> list[str]:
    token = re.compile(rf"\.{re.escape(class_name)}(?![\w-])")
    bare = re.compile(rf"^\.{re.escape(class_name)}(?:[:\[][^\s>+~]*)?$")
    lines = []
    for sheet in sheets:
        for rule in sheet.rules:
            for selector in rule.selectors:
                if token.search(selector):
                    context = " / ".join(rule.context) or "-"
                    kind = "bare" if bare.match(selector) else "compound"
                    props = ", ".join(d.prop for d in rule.declarations)
                    lines.append(
                        f"{sheet.name}:{rule.line}  order={rule.order}  spec={specificity(selector)}  {kind}  "
                        f"ctx={context}  {selector}  {{{props}}}"
                    )
    return lines


def dark_redundancy(sheets: list[Stylesheet]) -> list[str]:
    """Classify each dark-override declaration as REDUNDANT, REMAP, or NO-BASE."""
    base: dict[tuple[str, tuple[str, ...]], dict[str, set[str]]] = defaultdict(lambda: defaultdict(set))
    for sheet in sheets:
        for rule in sheet.rules:
            for selector in rule.selectors:
                if not selector.startswith(DARK_PREFIX):
                    for decl in rule.declarations:
                        base[(selector, rule.context)][decl.prop].add(decl.value.replace("!important", "").strip())
    report = []
    for sheet in sheets:
        if sheet.name in TOKEN_FILES:
            continue
        for rule in sheet.rules:
            for selector in rule.selectors:
                if not selector.startswith(DARK_PREFIX):
                    continue
                rest = selector[len(DARK_PREFIX):].strip()
                inner = re.fullmatch(r":is\((.*)\)", rest)
                targets = [" ".join(t.split()) for t in split_top_level(inner.group(1), ",")] if inner else [rest]
                for target in targets:
                    for decl in rule.declarations:
                        value = decl.value.replace("!important", "").strip()
                        existing = base.get((target, rule.context), {}).get(decl.prop)
                        if not existing:
                            verdict = "NO-BASE"
                        elif existing == {value}:
                            verdict = "REDUNDANT"
                        else:
                            verdict = f"REMAP (base: {' | '.join(sorted(existing))})"
                        report.append(f"{sheet.name}:{decl.line}  {verdict}  {target}  {decl.prop}: {value}")
    return report


def prune_dark_redundant(
    file_name: str, styles_dir: Path = STYLES_DIR, keep: frozenset[str] = frozenset()
) -> tuple[int, int]:
    """Remove dark-override targets whose every declaration repeats the base token.

    Only top-level `html[data-theme="dark"] X` and `html[data-theme="dark"] :is(...)`
    rules are rewritten; a target stays unless all of the rule's declarations
    are REDUNDANT for it. Targets in `keep` always stay: a snapshot showed that
    their override also outranks a different, more specific rule, so removing
    it changes the result even though the base repeats the token.
    Returns (targets removed, rules deleted).
    """
    sheets = load_stylesheets(styles_dir)
    base: dict[str, dict[str, set[str]]] = defaultdict(lambda: defaultdict(set))
    for sheet in sheets:
        for rule in sheet.rules:
            if rule.context:
                continue
            for selector in rule.selectors:
                if not selector.startswith(DARK_PREFIX):
                    for decl in rule.declarations:
                        base[selector][decl.prop].add(decl.value.replace("!important", "").strip())

    path = styles_dir / file_name
    text = path.read_text(encoding="utf-8")
    pattern = re.compile(r'^html\[data-theme="dark"\][^{]*\{[^{}]*\}\n?', re.M)
    removed = deleted = 0

    def rewrite(match: re.Match) -> str:
        nonlocal removed, deleted
        block = match.group(0)
        if "/*" in block:
            return block
        selector_text, body = block.split("{", 1)
        body = body.rsplit("}", 1)[0]
        decls = []
        for chunk in split_top_level(body, ";"):
            if ":" in chunk:
                prop, value = chunk.split(":", 1)
                decls.append((prop.strip().lower(), value.replace("!important", "").strip()))
        rest = " ".join(selector_text.split())[len(DARK_PREFIX):].strip()
        inner = re.fullmatch(r":is\((.*)\)", rest)
        targets = [" ".join(t.split()) for t in split_top_level(inner.group(1), ",")] if inner else [rest]
        if not decls or not all(re.fullmatch(r"var\(--[\w-]+\)", v) for _, v in decls):
            return block

        def redundant(target: str) -> bool:
            return all(base.get(target, {}).get(prop) == {value} for prop, value in decls)

        kept = [t for t in targets if t in keep or not redundant(t)]
        removed += len(targets) - len(kept)
        if not kept:
            deleted += 1
            return ""
        if len(kept) == len(targets):
            return block
        if inner:
            listed = ",\n".join(f"  {t}" for t in kept)
            new_selector = f'{DARK_PREFIX} :is(\n{listed}\n) '
        else:
            new_selector = f"{DARK_PREFIX} {kept[0]} "
        return new_selector + "{" + body + "}\n"

    new_text = re.sub(r"\n{3,}", "\n\n", pattern.sub(rewrite, text))
    with open(path, "w", encoding="utf-8", newline="\n") as handle:
        handle.write(new_text)
    return removed, deleted


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--json", action="store_true", help="print all metrics as JSON")
    mode.add_argument("--check", action="store_true", help="fail if metrics differ from the budget")
    mode.add_argument("--ratchet", action="store_true", help="record improvements in the budget")
    mode.add_argument("--list", metavar="METRIC", help="print every occurrence of a metric")
    mode.add_argument("--selector", metavar="CLASS", help="print every rule mentioning .CLASS in cascade order")
    mode.add_argument("--dark-redundancy", action="store_true", help="classify dark-mode overrides")
    mode.add_argument("--prune-dark", metavar="FILE", help="remove fully redundant dark-override targets from FILE")
    parser.add_argument("--keep", action="append", default=[], help="with --prune-dark: a target selector to preserve")
    args = parser.parse_args(argv)
    if args.prune_dark:
        keep = frozenset(" ".join(item.split()) for item in args.keep)
        removed, deleted = prune_dark_redundant(args.prune_dark, keep=keep)
        print(f"{args.prune_dark}: removed {removed} redundant targets, deleted {deleted} empty rules")
        return 0

    metrics, occurrences = compute_metrics()
    if args.json:
        print(json.dumps(metrics, indent=2))
        return 0
    if args.list:
        if args.list not in metrics:
            parser.error(f"unknown metric {args.list!r}; choose from {', '.join(metrics)}")
        for item in occurrences.get(args.list, []):
            print(item)
        return 0
    if args.selector:
        for line in selector_report(load_stylesheets(), args.selector.lstrip(".")):
            print(line)
        return 0
    if args.dark_redundancy:
        for line in dark_redundancy(load_stylesheets()):
            print(line)
        return 0

    if not BUDGET_PATH.exists():
        if args.ratchet:
            write_budget(metrics)
            print(f"created {BUDGET_PATH.relative_to(REPO_ROOT)}")
            return 0
        print("no budget file; create it with --ratchet", file=sys.stderr)
        return 1
    regressions, improvements = compare(metrics, load_budget())
    if args.ratchet:
        if regressions:
            print("refusing to ratchet; these metrics rose:\n  " + "\n  ".join(regressions), file=sys.stderr)
            return 1
        write_budget(metrics)
        print("budget updated" + (":\n  " + "\n  ".join(improvements) if improvements else " (no changes)"))
        return 0
    for line in regressions:
        print(f"REGRESSION {line}")
    for line in improvements:
        print(f"IMPROVED   {line}  (run --ratchet to record)")
    if not regressions and not improvements:
        print("css budget OK")
    return 1 if regressions or improvements else 0


if __name__ == "__main__":
    sys.exit(main())
