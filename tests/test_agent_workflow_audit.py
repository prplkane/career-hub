from __future__ import annotations

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import yaml

from scripts.agent_workflow_audit import (
    AgentWorkflowAuditError, _read_adapter_contract, audit_agent_workflows,
)

FIXTURE_CONTRACT_VERSION = 4


def write_yaml(path: Path, payload: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(yaml.safe_dump(payload, sort_keys=False), encoding="utf-8")


PACKET = Path("backend/internal/agent/agentmaterials/packet.go")
CLIENT = Path("backend/cmd/career-hub-agent/main.go")


def write_native_contract(root: Path, version: int, commands: str) -> None:
    (root / PACKET).parent.mkdir(parents=True, exist_ok=True)
    (root / CLIENT).parent.mkdir(parents=True, exist_ok=True)
    (root / PACKET).write_text(f"package agentmaterials\nconst ContractVersion = {version}\n", encoding="utf-8")
    (root / CLIENT).write_text(f"package main\nconst help = `Career Hub\nCommands: {commands}\n`\n", encoding="utf-8")


def prepare_skill(
    root: Path,
    *,
    contract_version: int = FIXTURE_CONTRACT_VERSION,
) -> Path:
    skill = root / ".agents" / "skills" / "example-workflow"
    skill.mkdir(parents=True)
    (skill / "SKILL.md").write_text(
        "---\nname: example-workflow\ndescription: Example workflow used for audit tests.\n---\n\n# Example\n",
        encoding="utf-8",
    )
    write_yaml(
        skill / "agents" / "openai.yaml",
        {"interface": {"display_name": "Example Workflow"}},
    )
    write_yaml(
        skill / "references" / "contract.yaml",
        {
            "schema_version": 1,
            "skill_name": "example-workflow",
            "tool_contract": {
                "module": "career-hub-agent",
                "version": contract_version,
                "commands": ["analyze"],
            },
            "tracked_paths": ["src/service.py"],
            "tests": ["tests/test_service.py"],
        },
    )
    (root / "src").mkdir()
    write_native_contract(root, 4, "analyze")
    (root / "src" / "service.py").write_text("VALUE = 1\n", encoding="utf-8")
    (root / "tests").mkdir()
    (root / "tests" / "test_service.py").write_text("", encoding="utf-8")
    return skill


class AgentWorkflowAuditTests(unittest.TestCase):
    def test_contract_comes_from_target_without_executing_it(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            prepare_skill(root, contract_version=7)
            write_native_contract(root, 7, "context")
            with (root / CLIENT).open("a", encoding="utf-8") as source:
                source.write('func init() { panic("must not execute") }\n')
            report = audit_agent_workflows(root)
        self.assertEqual(report["contract_version"], 7)
        self.assertEqual(report["skills"][0]["outdated_reasons"],
                         ["commands are no longer available: analyze"])

    def test_unavailable_or_large_contract_is_explicit(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            with self.assertRaisesRegex(AgentWorkflowAuditError, "unavailable"):
                _read_adapter_contract(root)
            prepare_skill(root)
            with patch("scripts.agent_workflow_audit.MAX_ADAPTER_SOURCE_BYTES", 10):
                with self.assertRaisesRegex(AgentWorkflowAuditError, "size limit"):
                    _read_adapter_contract(root)

    def test_invalid_or_dynamic_contract_never_falls_back(self) -> None:
        invalid = [
            b"not a Go declaration", b"\xff", b"",
            b'const help = dynamicHelp()\n',
            b'const help = `Commands: `\n',
            b'const help = `Commands: --bad`\n',
            b'const help = `Commands: analyze, analyze`\n',
            b'const help = `Commands: analyze\nCommands: context`\n',
            b'const help = `Commands: analyze`\nconst help = `Commands: context`\n',
        ]
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            prepare_skill(root)
            for source in invalid:
                with self.subTest(source=source):
                    (root / CLIENT).write_bytes(source)
                    with self.assertRaisesRegex(AgentWorkflowAuditError, "need review"):
                        audit_agent_workflows(root)
            write_native_contract(root, 4, "analyze")
            for source in ("const ContractVersion = value()", "const ContractVersion = 0", "const ContractVersion = " + "9" * 5000,
                           "const ContractVersion = 4\nconst ContractVersion = 5"):
                (root / PACKET).write_text(source, encoding="utf-8")
                with self.assertRaisesRegex(AgentWorkflowAuditError, "need review"):
                    audit_agent_workflows(root)

    def test_developer_audit_contract_is_separate(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            skill = prepare_skill(root)
            contract_path = skill / "references/contract.yaml"
            contract = yaml.safe_load(contract_path.read_text(encoding="utf-8"))
            contract["tool_contract"] = {"module": "scripts.agent_workflow_audit", "version": 1, "commands": ["audit"]}
            write_yaml(contract_path, contract)
            self.assertEqual(audit_agent_workflows(root)["skills"][0]["status"], "current")
            contract["tool_contract"]["commands"] = ["analyze"]
            write_yaml(contract_path, contract)
            self.assertEqual(audit_agent_workflows(root)["skills"][0]["status"], "outdated")

    def test_comments_strings_and_nested_declarations_are_not_contracts(self) -> None:
        stale = 'const ContractVersion = 4\n'
        invalid = [
            '/*\n' + stale + '*/\n',
            '// ' + stale,
            '/*\n' + stale + '*/\nconst ContractVersion = 2 + 3\n',
            'const unrelated = `\n' + stale + '`\n',
            'func example() {\n' + stale + '}\n',
            'const (\nContractVersion = 4\n)\n',
            stale + '/* unterminated',
            stale + 'const unrelated = "unterminated\n',
            stale + 'const unrelated = `unterminated\n',
        ]
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            prepare_skill(root)
            for source in invalid:
                with self.subTest(source=source):
                    (root / PACKET).write_text(source, encoding="utf-8")
                    with self.assertRaisesRegex(AgentWorkflowAuditError, "need review"):
                        _read_adapter_contract(root)
            write_native_contract(root, 4, "analyze")
            for source in (
                '/*\nconst help = `Commands: analyze`\n*/\n',
                '/*\nconst help = `Commands: analyze`\n*/\nconst help = "Commands: context"\n',
                'const unrelated = "const help = `Commands: analyze`"\n',
                'func example() { const help = `Commands: analyze` }\n',
                'const help = `Commands: analyze` + extra\n',
            ):
                with self.subTest(source=source):
                    (root / CLIENT).write_text(source, encoding="utf-8")
                    with self.assertRaisesRegex(AgentWorkflowAuditError, "need review"):
                        _read_adapter_contract(root)

    def test_actual_literals_allow_comments_and_ignore_decoys(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            prepare_skill(root)
            (root / PACKET).write_text(
                'package agentmaterials\n/* const ContractVersion = 99 */\n'
                'const unrelated = `\nconst ContractVersion = 88\n`\n'
                'const /* note */ ContractVersion = 7 // current\n'
                'func example() { const ContractVersion = 66 }\n', encoding="utf-8")
            (root / CLIENT).write_text(
                'package main\n// const help = `Commands: obsolete`\n'
                'const unrelated = "escaped \\" quote and // /* }"\n'
                'const help /* note */ = `Career Hub\nCommands: context\n`;\n', encoding="utf-8")
            self.assertEqual(_read_adapter_contract(root), (7, frozenset({"context"})))

    def test_standalone_audit_does_not_import_product_modules(self) -> None:
        root = Path(__file__).resolve().parents[1]
        code = '''
import importlib.abc, json, sys
from pathlib import Path
class NoProductImports(importlib.abc.MetaPathFinder):
    def find_spec(self, fullname, path=None, target=None):
        if fullname == "src" or fullname.startswith("src."):
            raise RuntimeError("unexpected product import: " + fullname)
sys.meta_path.insert(0, NoProductImports())
from scripts.agent_workflow_audit import audit_agent_workflows
print(json.dumps(audit_agent_workflows(Path.cwd())))
'''
        result = subprocess.run([sys.executable, "-B", "-c", code], cwd=root,
                                capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr)
        report = json.loads(result.stdout)
        self.assertEqual(report["summary"]["broken"], 0)
        self.assertEqual(report["summary"]["outdated"], 0)

    def test_standalone_and_legacy_cli_return_the_same_report(self) -> None:
        root = Path(__file__).resolve().parents[1]
        reports = []
        for command in (["scripts.agent_workflow_audit"], ["src.agent_workflow", "audit"]):
            result = subprocess.run(
                [sys.executable, "-B", "-m", *command, "--project-root", str(root)],
                cwd=root, capture_output=True, text=True, timeout=20,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            reports.append(json.loads(result.stdout))
        self.assertEqual(reports[0], reports[1])

    def test_cli_contract_error_is_reported_without_traceback(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            prepare_skill(root)
            (root / CLIENT).write_text("invalid!", encoding="utf-8")
            result = subprocess.run(
                [sys.executable, "-B", "-m", "scripts.agent_workflow_audit",
                 "--project-root", str(root)],
                cwd=Path(__file__).resolve().parents[1],
                capture_output=True, text=True, timeout=20,
            )
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn("adapter contract declarations need review", result.stderr)
        self.assertNotIn("Traceback", result.stderr)

    def test_current_and_review_required_are_distinct(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            prepare_skill(root)
            current = audit_agent_workflows(root)
            with patch(
                "scripts.agent_workflow_audit._git_changed_paths",
                return_value=({"src/service.py"}, []),
            ):
                changed = audit_agent_workflows(root, changed=True)

        self.assertEqual(current["skills"][0]["status"], "current")
        self.assertEqual(changed["skills"][0]["status"], "review_required")
        self.assertEqual(
            changed["skills"][0]["changed_relevant_paths"],
            ["src/service.py"],
        )

    def test_contract_version_mismatch_is_outdated(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            prepare_skill(root, contract_version=999)

            report = audit_agent_workflows(root)

        self.assertEqual(report["skills"][0]["status"], "outdated")
        self.assertIn("contract version", report["skills"][0]["outdated_reasons"][0])

    def test_missing_skill_metadata_is_broken(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            skill = prepare_skill(root)
            (skill / "SKILL.md").unlink()

            report = audit_agent_workflows(root)

        self.assertEqual(report["skills"][0]["status"], "broken")
        self.assertIn("SKILL.md is missing", report["skills"][0]["errors"])


if __name__ == "__main__":
    unittest.main()
