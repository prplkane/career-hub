"""Read-only, content-minimized migration inventory; never import target code.

Run ``python -m scripts.migration_inventory --project-root .`` for JSON stdout.
Only Git-indexed files in reviewed textual source/resource roots are read. Missing
Git, invalid source, bounds, and dynamic expressions remain explicit gaps, not a
fallback scan of potentially private untracked files or a claim of completeness.
Hashes describe working-tree bytes, not necessarily bytes in the reported commit.
"""

from __future__ import annotations

import argparse
import ast
from copy import deepcopy
import hashlib
import json
import math
from pathlib import Path, PurePosixPath
import platform
import re
import stat
import subprocess
from threading import Thread
from time import monotonic
from typing import Any


INVENTORY_FORMAT_VERSION = 1
MAX_FILE_BYTES = 4 * 1024 * 1024
MAX_TOTAL_BYTES = 64 * 1024 * 1024
MAX_FILES = 5_000
MAX_GIT_OUTPUT_BYTES = 2 * 1024 * 1024
MAX_EXPRESSION_CHARS = 2_048
GIT_TIMEOUT_SECONDS = 5

REVIEWED_ROOTS = (
    ".agents/skills", "backend", "desktop/src", "desktop/tests",
    "desktop/scripts", "docs", "extension", "packaging/pyinstaller",
    "packaging/scripts", "scripts", "src", "tests",
)
REVIEWED_FILES = (
    "AGENTS.md", "CLAUDE.md", "GEMINI.md", "LICENSE", "README.md",
    "SECURITY.md", "START_HERE.md", "VERSION", ".gitignore", ".gitattributes",
    "requirements.in", "requirements.txt", "package.json", "package-lock.json",
    "desktop/package.json", "desktop/package-lock.json",
    "packaging/requirements-build.in", "packaging/requirements-build.txt",
)
TEXT_SUFFIXES = frozenset({
    ".py", ".go", ".mod", ".sum", ".js", ".mjs", ".cjs", ".json",
    ".yaml", ".yml", ".md", ".txt", ".in", ".ps1", ".sh", ".html",
    ".css", ".svg", ".toml", ".spec", ".lock", ".sql", ".eml",
})
EXCLUDED_PARTS = frozenset({
    ".git", ".venv", "venv", "__pycache__", "node_modules", "tmp",
    "worktrees", "build", "dist", "outputs", "career_vault", "documents",
    "private_documents", "resume_versions", "resume_templates", "secrets",
    "backups", "security-reports", ".career_hub_staging", "logs",
})
ROUTE_FILES = {
    "src/intake_ui_routes/mutations.py", "src/intake_ui_routes/reads.py",
    "src/intake_ui_routes/contracts.py",
}
ROUTE_TABLES = {
    "_POST_EXACT", "_PUT_EXACT", "_VAULT_COLLECTIONS", "_JOB_POST_SUFFIXES",
    "STATIC_FILES", "STYLE_MODULES",
}
PARSER_OPERATIONS = {
    "add_argument", "add_parser", "add_subparsers", "add_mutually_exclusive_group",
}
VERSION_NAME = re.compile(r"(?:^|_)(?:VERSION|SCHEMA_VERSION)$")


def _canonical(value: object) -> bytes:
    return json.dumps(value, ensure_ascii=True, sort_keys=True, separators=(",", ":"),
                      allow_nan=False).encode("utf-8")


def _safe_text(value: str, *, route_context: bool = False) -> str:
    # Source literals may themselves contain a developer's private absolute path.
    # API URLs are public route syntax, not filesystem paths.
    if re.search(r"(?:[A-Za-z]:[\\/]|\\\\)", value) or (
        not route_context and value.startswith("/") and value != "/" and not value.startswith("/api/")
    ):
        return "<absolute-path-redacted>"
    if len(value) > MAX_EXPRESSION_CHARS:
        return value[:MAX_EXPRESSION_CHARS] + "<truncated>"
    return value


class _RedactLiterals(ast.NodeTransformer):
    def __init__(self, *, route_context: bool = False) -> None:
        self.route_context = route_context

    def visit_Constant(self, node: ast.Constant) -> ast.AST:
        if isinstance(node.value, str):
            return ast.copy_location(ast.Constant(_safe_text(node.value, route_context=self.route_context)), node)
        return node


def _expression(node: ast.AST, *, route_context: bool = False) -> str:
    try:
        redacted = _RedactLiterals(route_context=route_context).visit(deepcopy(node))
        expression = ast.unparse(redacted)
        # Literal values were checked above. Rechecking their escaped source
        # representation would mistake a backslash guard for a UNC path.
        if len(expression) > MAX_EXPRESSION_CHARS:
            return expression[:MAX_EXPRESSION_CHARS] + "<truncated>"
        return expression
    except (RecursionError, ValueError):
        return "<expression-unavailable>"


def _json_literal(value: Any, *, route_context: bool = False) -> Any:
    if value is None or isinstance(value, (bool, int)):
        return value
    if isinstance(value, float) and math.isfinite(value):
        return value
    if isinstance(value, str):
        return _safe_text(value, route_context=route_context)
    if isinstance(value, (list, tuple)):
        return [_json_literal(item, route_context=route_context) for item in value]
    if isinstance(value, (set, frozenset)):
        return sorted((_json_literal(item, route_context=route_context) for item in value), key=_canonical)
    if isinstance(value, dict) and all(isinstance(key, str) for key in value):
        return {_safe_text(key, route_context=route_context): _json_literal(item, route_context=route_context)
                for key, item in value.items()}
    raise ValueError("Not a JSON literal")


def _ast_value(node: ast.AST, *, route_context: bool = False) -> dict[str, object]:
    try:
        return {"kind": "literal", "value": _json_literal(ast.literal_eval(node), route_context=route_context)}
    except (ValueError, TypeError, SyntaxError, RecursionError):
        return {"kind": "expression", "expression": _expression(node, route_context=route_context)}


def _git(root: Path, arguments: list[str]) -> tuple[bytes | None, str | None]:
    """Use argument arrays and local trust only; do not alter global Git config."""
    try:
        child = subprocess.Popen(
            ["git", "--no-optional-locks", "-c", f"safe.directory={root.as_posix()}",
             "-c", "core.fsmonitor=false", "-C", str(root), *arguments],
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        )
    except FileNotFoundError:
        return None, "git_unavailable"
    except OSError:
        return None, "git_execution_failed"
    output: list[bytes] = []
    read_errors: list[str] = []

    def read_bounded() -> None:
        try:
            assert child.stdout is not None
            output.append(child.stdout.read(MAX_GIT_OUTPUT_BYTES + 1))
        except (OSError, ValueError):
            read_errors.append("git_read_failed")

    # Reading in a daemon permits timeout even if a stalled child keeps its pipe
    # open. The single read never allocates an unbounded communicate() buffer.
    reader = Thread(target=read_bounded, daemon=True)
    deadline = monotonic() + GIT_TIMEOUT_SECONDS
    error = None
    reader.start()
    try:
        reader.join(GIT_TIMEOUT_SECONDS)
        if reader.is_alive():
            error = "git_timeout"
        elif read_errors:
            error = read_errors[0]
        elif len(output[0]) > MAX_GIT_OUTPUT_BYTES:
            error = "git_output_limit"
        elif child.wait(timeout=max(0.001, deadline - monotonic())):
            error = "git_query_failed"
    except subprocess.TimeoutExpired:
        error = "git_timeout"
    except OSError:
        error = "git_execution_failed"
    finally:
        try:
            if child.poll() is None:
                child.kill()
            child.wait(timeout=1)
        except (OSError, subprocess.TimeoutExpired):
            error = "git_cleanup_failed"
        reader.join(1)
        if not reader.is_alive() and child.stdout is not None:
            child.stdout.close()
    return (None, error) if error else (output[0], None)


def _tracked_files(root: Path) -> tuple[dict[str, object], list[str]]:
    commit_raw, commit_error = _git(root, ["rev-parse", "--verify", "HEAD"])
    commit = commit_raw.decode("ascii", errors="replace").strip() if commit_raw else ""
    if not re.fullmatch(r"[a-f0-9]{40}|[a-f0-9]{64}", commit):
        commit = ""
        commit_error = commit_error or "git_commit_invalid"
    raw, error = _git(root, ["ls-files", "--cached", "--full-name", "-z", "--",
                             *REVIEWED_FILES, *REVIEWED_ROOTS])
    metadata = {
        "status": "available" if error is None else "unavailable",
        "commit": commit or None, "commit_status": commit_error or "available",
        "file_list_status": error or "available",
        "source_basis": "working_tree_bytes_for_indexed_paths",
    }
    if raw is None:
        return metadata, []
    try:
        return metadata, sorted(set(part for part in raw.decode("utf-8").split("\0") if part))
    except UnicodeError:
        metadata.update(status="unavailable", file_list_status="git_paths_invalid_utf8")
        return metadata, []


def _reviewed_path(raw: str) -> bool:
    path = PurePosixPath(raw)
    if (not raw or "\\" in raw or path.is_absolute() or path.as_posix() != raw
            or any(part in {"..", ".", ""} or ":" in part for part in path.parts)
            or any(ord(char) < 32 for char in raw)):
        return False
    if any(part.casefold() in EXCLUDED_PARTS or part.casefold().startswith(".env")
           for part in path.parts):
        return False
    if raw in REVIEWED_FILES:
        return True
    return path.suffix.casefold() in TEXT_SUFFIXES and any(
        raw.startswith(prefix + "/") for prefix in REVIEWED_ROOTS
    )


def _read_source(root: Path, relative: str, remaining: int) -> tuple[bytes | None, str | None]:
    path = root
    try:
        for part in PurePosixPath(relative).parts:
            path = path / part
            info = path.lstat()
            if stat.S_ISLNK(info.st_mode) or (
                getattr(info, "st_file_attributes", 0) & stat.FILE_ATTRIBUTE_REPARSE_POINT
            ):
                return None, "link_or_reparse_excluded"
        if not stat.S_ISREG(info.st_mode):
            return None, "non_regular_file_excluded"
        if info.st_size > MAX_FILE_BYTES:
            return None, "file_byte_limit"
        if info.st_size > remaining:
            return None, "total_byte_limit"
        if not path.resolve(strict=True).is_relative_to(root):
            return None, "source_outside_root"
        with path.open("rb") as stream:
            data = stream.read(min(MAX_FILE_BYTES, remaining) + 1)
        if len(data) > min(MAX_FILE_BYTES, remaining):
            return None, "source_grew_beyond_limit"
        if b"\0" in data:
            return None, "binary_content_excluded"
        return data, None
    except OSError:
        return None, "source_unreadable"


def _category(path: str) -> str:
    if path.startswith(".agents/") or PurePosixPath(path).name in {
        "AGENTS.md", "CLAUDE.md", "GEMINI.md", "SECURITY.md", "START_HERE.md",
    }:
        return "instructions"
    if ("lock" in PurePosixPath(path).name or PurePosixPath(path).name.startswith("requirements")
            or PurePosixPath(path).suffix in {".mod", ".sum"}):
        return "dependency"
    if path.startswith(("src/data/", "src/intake_ui_static/")) or path == "VERSION":
        return "resource"
    if path.startswith("docs/"):
        return "documentation"
    return "source_or_test"


def _consumer(path: str) -> str | None:
    for prefix, group in (
        ("src/intake_ui_static/", "frontend"), ("extension/", "extension"),
        ("desktop/src/", "desktop"), (".agents/skills/", "agent_instructions"),
    ):
        if path.startswith(prefix):
            return group
    return None


def _assignments(tree: ast.Module):
    for node in tree.body:
        if isinstance(node, ast.Assign):
            for target in node.targets:
                if isinstance(target, ast.Name):
                    yield target.id, node.value, node.lineno
        elif isinstance(node, ast.AnnAssign) and isinstance(node.target, ast.Name) and node.value:
            yield node.target.id, node.value, node.lineno


def _route_inventory(path: str, tree: ast.Module) -> dict[str, list[dict[str, object]]]:
    tables, branches, constants = [], [], []
    for name, value, line in _assignments(tree):
        reference = {"path": path, "symbol": name, "line": line}
        if name in ROUTE_TABLES:
            entries = []
            if isinstance(value, ast.Dict):
                entries = [{"key": _ast_value(key, route_context=True) if key is not None else None,
                            "value": _ast_value(item, route_context=True), "is_expansion": key is None}
                           for key, item in zip(value.keys, value.values)]
            elif isinstance(value, (ast.Tuple, ast.List)):
                entries = [_ast_value(item, route_context=True) for item in value.elts]
            tables.append({**reference, "entries": entries,
                           "expression": _expression(value, route_context=True) if not entries else None})
        elif path.endswith("/contracts.py"):
            constants.append({**reference, "value": _ast_value(value)})
    for function in tree.body:
        if not isinstance(function, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        if not (function.name.startswith("_dispatch_") or function.name in {
            "_post_route", "_put_route", "_delete_route", "match_mutation_route", "dispatch_get",
        }):
            continue
        for order, node in enumerate(sorted(
            (node for node in ast.walk(function) if isinstance(node, ast.If)),
            key=lambda node: node.lineno,
        ), 1):
            branches.append({"path": path, "symbol": function.name, "line": node.lineno,
                             "lexical_order": order, "predicate": _expression(node.test, route_context=True)})
    return {"tables": tables, "branches": branches, "constants": constants}


def _cli_inventory(path: str, tree: ast.Module) -> dict[str, object] | None:
    functions = [node for node in tree.body if isinstance(node, ast.FunctionDef)]
    parser = next((node for node in functions if node.name == "build_parser"), None)
    if parser is None:
        return None
    declarations = []
    for function in functions:
        for call in sorted((node for node in ast.walk(function) if isinstance(node, ast.Call)),
                           key=lambda node: (node.lineno, node.col_offset)):
            if not isinstance(call.func, ast.Attribute) or call.func.attr not in PARSER_OPERATIONS:
                continue
            declarations.append({
                "symbol": function.name, "line": call.lineno,
                "operation": call.func.attr, "receiver": _expression(call.func.value),
                "arguments": [_ast_value(value) for value in call.args],
                "keywords": [{"name": value.arg, "value": _ast_value(value.value)}
                             for value in call.keywords],
            })
    return {"path": path, "symbol": "build_parser", "line": parser.lineno,
            "declarations": declarations}


def collect_inventory(project_root: Path | str) -> dict[str, object]:
    """Collect deterministic evidence from tracked reviewed paths, without writes."""
    try:
        root = Path(project_root).resolve(strict=True)
        if not root.is_dir():
            raise ValueError("Inventory project root must be a directory")
    except OSError:
        raise ValueError("Inventory project root is unavailable") from None

    git, tracked = _tracked_files(root)
    gaps: list[dict[str, object]] = [
        {"code": "static_inventory_only", "detail": "No behavioral characterization or parity is established."},
        {"code": "dynamic_expressions_unresolved", "detail": "Expressions are recorded, not evaluated; helper calls and loops are not expanded."},
        {"code": "route_scope_partial", "detail": "Ordered predicates are lexical evidence, not a complete router, request, response, or authorization schema."},
        {"code": "native_and_persistence_uncharacterized", "detail": "Native behavior, persisted formats, and consumer semantics require separate fixtures."},
    ]
    if git["status"] != "available":
        gaps.append({"code": git["file_list_status"], "detail": "Source enumeration was skipped; no untracked filesystem fallback."})
    files, modules, cli, versions, consumers = [], [], [], [], []
    routes: dict[str, list[dict[str, object]]] = {"tables": [], "branches": [], "constants": []}
    excluded = 0
    total_bytes = 0
    candidates = [path for path in tracked if _reviewed_path(path)]
    excluded = len(tracked) - len(candidates)
    if len(candidates) > MAX_FILES:
        gaps.append({"code": "file_count_limit", "omitted_count": len(candidates) - MAX_FILES})
    for relative in candidates[:MAX_FILES]:
        data, error = _read_source(root, relative, MAX_TOTAL_BYTES - total_bytes)
        if data is None:
            gaps.append({"code": error, "path": relative})
            continue
        total_bytes += len(data)
        fingerprint = hashlib.sha256(data).hexdigest()
        files.append({"path": relative, "category": _category(relative),
                      "sha256": fingerprint, "bytes": len(data)})
        group = _consumer(relative)
        if group:
            consumers.append({"path": relative, "group": group, "sha256": fingerprint})
        if not relative.endswith(".py"):
            continue
        module = {"path": relative, "physical_lines": len(data.splitlines()), "parse_status": "parsed"}
        if relative.startswith("src/"):
            modules.append(module)
        try:
            tree = ast.parse(data, filename=relative)
        except (SyntaxError, ValueError, UnicodeError, RecursionError) as exc:
            module["parse_status"] = "invalid"
            gaps.append({"code": "python_ast_invalid", "path": relative,
                         "line": getattr(exc, "lineno", None)})
            continue
        parser = _cli_inventory(relative, tree)
        if parser:
            cli.append(parser)
        for name, value, line in _assignments(tree):
            if VERSION_NAME.search(name):
                versions.append({"path": relative, "symbol": name, "line": line,
                                 "value": _ast_value(value)})
        if relative in ROUTE_FILES:
            for key, values in _route_inventory(relative, tree).items():
                routes[key].extend(values)
    present = {row["path"] for row in files}
    for relative in sorted(ROUTE_FILES - present):
        gaps.append({"code": "route_source_unavailable", "path": relative})
    return {
        "format_version": INVENTORY_FORMAT_VERSION,
        "complete": False, "characterization_status": "not_run", "git": git,
        "scope": {"tracked_only": True, "reviewed_roots": list(REVIEWED_ROOTS),
                  "reviewed_files": list(REVIEWED_FILES), "excluded_entry_count": excluded,
                  "max_files": MAX_FILES, "max_file_bytes": MAX_FILE_BYTES,
                  "max_total_bytes": MAX_TOTAL_BYTES},
        "files": files, "source_fingerprint": hashlib.sha256(_canonical(files)).hexdigest(),
        "python": {"src_module_count": len(modules), "src_physical_lines": sum(
            row["physical_lines"] for row in modules), "modules": modules},
        "http": routes, "cli": cli, "contract_versions": versions,
        "consumer_fingerprints": consumers, "gaps": gaps,
        "runtime_metadata": {"python_implementation": platform.python_implementation(),
                             "python_version": platform.python_version()},
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project-root", type=Path, default=Path.cwd())
    args = parser.parse_args(argv)
    try:
        report = collect_inventory(args.project_root)
    except ValueError as exc:
        parser.exit(2, f"{exc}\n")
    print(json.dumps(report, ensure_ascii=True, sort_keys=True, indent=2, allow_nan=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
