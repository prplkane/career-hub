"""Audit repo-scoped agent skills against the Career Hub tool contract."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

import yaml

AUDIT_SCHEMA_VERSION = 1
SKILLS_RELATIVE_PATH = Path(".agents") / "skills"
MAX_ADAPTER_SOURCE_BYTES = 1024 * 1024


class AgentWorkflowAuditError(ValueError):
    """Raised when the workflow audit cannot safely inspect the repository."""


def _native_literal(source: str, name: str, kind: str) -> str:
    """Recognize one simple top-level const; refuse other declaration forms.

    Comments and quoted contents are lexical units, never declaration text.
    This deliberately accepts less than the full Go grammar.
    """
    tokens = re.compile(
        r"(?P<space>[^\S\n]+)|(?P<newline>\n)|"
        r"(?P<comment>//[^\n]*|/\*[\s\S]*?\*/)|"
        r"(?P<raw>`[^`]*`)|"
        r'''(?P<quoted>"(?:\\[^\r\n]|[^"\\\r\n])*"|'(?:\\[^\r\n]|[^'\\\r\n])*')|'''
        r"(?P<identifier>[^\W\d]\w*)|(?P<number>[0-9][\w.]*)|"
        r"(?P<punctuation>[^\w\s])"
    )
    statements: list[list[tuple[str, str]]] = []
    statement: list[tuple[str, str]] = []
    depth = 0
    offset = 0
    while offset < len(source):
        # Unterminated comments/literals must not fall through as punctuation.
        match = tokens.match(source, offset)
        if (match is None or
                (source.startswith("/*", offset) and match.lastgroup != "comment") or
                (source[offset] in "`\"'" and match.lastgroup not in {"raw", "quoted"})):
            raise AgentWorkflowAuditError("adapter contract declarations need review")
        token_kind = match.lastgroup
        value = match.group()
        offset = match.end()
        if token_kind == "space":
            continue
        if token_kind == "comment":
            if "\n" not in value:
                continue
            token_kind = "newline"
        if token_kind == "punctuation" and value == "{":
            if depth == 0:
                statements.append(statement)
                statement = []
            depth += 1
        elif token_kind == "punctuation" and value == "}":
            depth -= 1
            if depth < 0:
                raise AgentWorkflowAuditError("adapter contract declarations need review")
        elif depth == 0:
            if token_kind == "newline" or (token_kind == "punctuation" and value == ";"):
                statements.append(statement)
                statement = []
            else:
                statement.append((token_kind, value))
    if depth:
        raise AgentWorkflowAuditError("adapter contract declarations need review")
    statements.append(statement)
    candidates = [parts for parts in statements if ("identifier", name) in parts]
    if (len(candidates) != 1 or len(candidates[0]) != 4 or
            candidates[0][:3] != [("identifier", "const"), ("identifier", name), ("punctuation", "=")] or
            candidates[0][3][0] != kind):
        raise AgentWorkflowAuditError("adapter contract declarations need review")
    return candidates[0][3][1]


def _read_adapter_contract(project_root: Path) -> tuple[int, frozenset[str]]:
    """Read native declarations, never compile or execute target code.

    This validates the advertised contract, not command implementation;
    native executable qualification supplies that separate evidence.
    """
    def read(relative: str) -> str:
        try:
            with (project_root / relative).open("rb") as source:
                raw = source.read(MAX_ADAPTER_SOURCE_BYTES + 1)
        except OSError as exc:
            raise AgentWorkflowAuditError("adapter contract source is unavailable") from exc
        if len(raw) > MAX_ADAPTER_SOURCE_BYTES:
            raise AgentWorkflowAuditError("adapter contract source exceeds the size limit")
        try:
            return raw.decode("utf-8-sig")
        except UnicodeError as exc:
            raise AgentWorkflowAuditError("adapter contract declarations need review") from exc

    packet = read("backend/internal/agent/agentmaterials/packet.go")
    client = read("backend/cmd/career-hub-agent/main.go")
    version = _native_literal(packet, "ContractVersion", "number")
    help_text = _native_literal(client, "help", "raw")[1:-1]
    if re.fullmatch(r"[1-9][0-9]{0,8}", version) is None:
        raise AgentWorkflowAuditError("adapter contract declarations need review")
    lines = re.findall(r"^Commands: (.+)\r?$", help_text, re.MULTILINE)
    if len(lines) != 1:
        raise AgentWorkflowAuditError("adapter contract declarations need review")
    commands = lines[0].rstrip("\r").split(", ")
    if (len(set(commands)) != len(commands)
            or any(re.fullmatch(r"[a-z][a-z0-9-]{0,63}", name) is None for name in commands)):
        raise AgentWorkflowAuditError("adapter contract declarations need review")
    return int(version), frozenset(commands)


def _relative_posix(project_root: Path, path: Path) -> str:
    return path.relative_to(project_root).as_posix()


def _load_mapping(path: Path, label: str) -> dict[str, Any]:
    try:
        payload = yaml.safe_load(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, yaml.YAMLError) as exc:
        raise AgentWorkflowAuditError(f"{label} is unreadable: {path}") from exc
    if not isinstance(payload, dict):
        raise AgentWorkflowAuditError(f"{label} must be a YAML mapping: {path}")
    return payload


def _skill_frontmatter(path: Path) -> dict[str, Any]:
    try:
        text = path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise AgentWorkflowAuditError(f"SKILL.md is unreadable: {path}") from exc
    lines = text.splitlines()
    if len(lines) < 3 or lines[0].strip() != "---":
        raise AgentWorkflowAuditError(f"SKILL.md frontmatter is missing: {path}")
    try:
        end = lines[1:].index("---") + 1
    except ValueError as exc:
        raise AgentWorkflowAuditError(f"SKILL.md frontmatter is not closed: {path}") from exc
    try:
        payload = yaml.safe_load("\n".join(lines[1:end]))
    except yaml.YAMLError as exc:
        raise AgentWorkflowAuditError(f"SKILL.md frontmatter is invalid: {path}") from exc
    if not isinstance(payload, dict):
        raise AgentWorkflowAuditError(f"SKILL.md frontmatter must be a mapping: {path}")
    return payload


def _git_changed_paths(project_root: Path) -> tuple[set[str], list[str]]:
    commands = (
        ("diff", "--name-only", "--relative"),
        ("diff", "--cached", "--name-only", "--relative"),
        ("ls-files", "--others", "--exclude-standard"),
    )
    changed: set[str] = set()
    warnings: list[str] = []
    for command in commands:
        try:
            result = subprocess.run(
                [
                    "git",
                    "-c",
                    f"safe.directory={project_root.as_posix()}",
                    *command,
                ],
                cwd=project_root,
                check=False,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="replace",
            )
        except OSError as exc:
            warnings.append(f"Git changed-path detection is unavailable: {exc}")
            break
        if result.returncode != 0:
            detail = result.stderr.strip() or "unknown Git error"
            warnings.append(f"Git changed-path detection is unavailable: {detail}")
            break
        changed.update(
            line.strip().replace("\\", "/")
            for line in result.stdout.splitlines()
            if line.strip()
        )
    return changed, warnings


def _clean_path_list(value: object, field: str, errors: list[str]) -> list[str]:
    if not isinstance(value, list):
        errors.append(f"{field} must be a list")
        return []
    cleaned: list[str] = []
    for index, item in enumerate(value):
        if not isinstance(item, str) or not item.strip():
            errors.append(f"{field}[{index}] must be a non-empty string")
            continue
        normalized = item.strip().replace("\\", "/")
        if normalized.startswith("./"):
            normalized = normalized[2:]
        if normalized.startswith("../") or Path(normalized).is_absolute():
            errors.append(f"{field}[{index}] must stay inside the repository")
            continue
        cleaned.append(normalized)
    return cleaned


def _path_matches(changed_path: str, tracked_path: str) -> bool:
    tracked = tracked_path.rstrip("/")
    return changed_path == tracked or changed_path.startswith(tracked + "/")


def _audit_skill(
    project_root: Path,
    skill_dir: Path,
    *,
    changed_paths: set[str],
    changed_enabled: bool,
    contract_version: int,
    command_names: frozenset[str],
) -> dict[str, object]:
    errors: list[str] = []
    outdated_reasons: list[str] = []
    skill_path = skill_dir / "SKILL.md"
    contract_path = skill_dir / "references" / "contract.yaml"
    interface_path = skill_dir / "agents" / "openai.yaml"

    frontmatter: dict[str, Any] = {}
    contract: dict[str, Any] = {}
    if not skill_path.is_file():
        errors.append("SKILL.md is missing")
    else:
        try:
            frontmatter = _skill_frontmatter(skill_path)
        except AgentWorkflowAuditError as exc:
            errors.append(str(exc))
    if not contract_path.is_file():
        errors.append("references/contract.yaml is missing")
    else:
        try:
            contract = _load_mapping(contract_path, "skill contract")
        except AgentWorkflowAuditError as exc:
            errors.append(str(exc))
    if not interface_path.is_file():
        errors.append("agents/openai.yaml is missing")
    else:
        try:
            interface = _load_mapping(interface_path, "skill interface")
        except AgentWorkflowAuditError as exc:
            errors.append(str(exc))
        else:
            if not isinstance(interface.get("interface"), dict):
                errors.append("agents/openai.yaml interface must be a mapping")

    folder_name = skill_dir.name
    if frontmatter:
        if frontmatter.get("name") != folder_name:
            errors.append("frontmatter name must match the skill folder")
        description = frontmatter.get("description")
        if not isinstance(description, str) or not description.strip():
            errors.append("frontmatter description is missing")

    tracked_paths: list[str] = []
    tests: list[str] = []
    commands: list[str] = []
    if contract:
        if contract.get("schema_version") != 1:
            errors.append("contract schema_version must be 1")
        if contract.get("skill_name") != folder_name:
            errors.append("contract skill_name must match the skill folder")
        tool_contract = contract.get("tool_contract")
        if not isinstance(tool_contract, dict):
            errors.append("tool_contract must be a mapping")
        else:
            module = tool_contract.get("module")
            expected_version, available_commands = contract_version, command_names
            if module == "scripts.agent_workflow_audit":
                expected_version, available_commands = AUDIT_SCHEMA_VERSION, frozenset({"audit"})
            elif module != "career-hub-agent":
                outdated_reasons.append("tool module must name career-hub-agent or scripts.agent_workflow_audit")
            if tool_contract.get("version") != expected_version:
                outdated_reasons.append(
                    "tool contract version does not match the current adapter"
                )
            commands = _clean_path_list(
                tool_contract.get("commands"), "commands", errors
            )
            unknown_commands = sorted(set(commands) - available_commands)
            if unknown_commands:
                outdated_reasons.append(
                    "commands are no longer available: " + ", ".join(unknown_commands)
                )
        tracked_paths = _clean_path_list(
            contract.get("tracked_paths"), "tracked_paths", errors
        )
        tests = _clean_path_list(contract.get("tests"), "tests", errors)

    for relative in tracked_paths + tests:
        if not (project_root / relative).exists():
            outdated_reasons.append(f"tracked path no longer exists: {relative}")

    changed_relevant = sorted(
        path
        for path in changed_paths
        if any(_path_matches(path, tracked) for tracked in tracked_paths)
        or _path_matches(path, _relative_posix(project_root, skill_dir))
    )
    if errors:
        status = "broken"
    elif outdated_reasons:
        status = "outdated"
    elif changed_enabled and changed_relevant:
        status = "review_required"
    else:
        status = "current"
    return {
        "name": folder_name,
        "status": status,
        "commands": commands,
        "changed_relevant_paths": changed_relevant,
        "errors": errors,
        "outdated_reasons": outdated_reasons,
    }


def audit_agent_workflows(project_root: Path, *, changed: bool = False) -> dict[str, object]:
    """Return a machine-readable compatibility report for repo-scoped skills."""
    root = Path(project_root).resolve()
    skills_root = root / SKILLS_RELATIVE_PATH
    if not skills_root.is_dir():
        raise AgentWorkflowAuditError(f"repo skill directory is missing: {skills_root}")
    contract_version, command_names = _read_adapter_contract(root)
    changed_paths: set[str] = set()
    warnings: list[str] = []
    if changed:
        changed_paths, warnings = _git_changed_paths(root)
    skill_dirs = sorted(
        path
        for path in skills_root.iterdir()
        if path.is_dir()
    )
    skills = [
        _audit_skill(
            root,
            skill_dir,
            changed_paths=changed_paths,
            changed_enabled=changed,
            contract_version=contract_version,
            command_names=command_names,
        )
        for skill_dir in skill_dirs
    ]
    counts = {
        status: sum(item["status"] == status for item in skills)
        for status in ("current", "review_required", "outdated", "broken")
    }
    return {
        "schema_version": AUDIT_SCHEMA_VERSION,
        "contract_version": contract_version,
        "changed_mode": changed,
        "changed_paths": sorted(changed_paths),
        "skills": skills,
        "summary": {"total": len(skills), **counts},
        "warnings": warnings,
    }


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Audit Career Hub repo-scoped skills against their tool contracts."
    )
    parser.add_argument("--project-root", type=Path, default=Path.cwd())
    parser.add_argument("--changed", action="store_true")
    parser.add_argument("--pretty", action="store_true")
    return parser


def main() -> int:
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    args = build_parser().parse_args()
    try:
        report = audit_agent_workflows(args.project_root, changed=args.changed)
    except AgentWorkflowAuditError as exc:
        print(f"Agent workflow audit failed: {exc}", file=sys.stderr)
        return 1
    json.dump(
        report,
        sys.stdout,
        ensure_ascii=False,
        indent=2 if args.pretty else None,
        separators=None if args.pretty else (",", ":"),
        sort_keys=args.pretty,
    )
    print()
    return 1 if report["summary"]["broken"] or report["summary"]["outdated"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
