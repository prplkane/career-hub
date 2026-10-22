from __future__ import annotations

from contextlib import redirect_stdout
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
from threading import Event
import unittest
from unittest.mock import Mock, patch

from scripts import migration_inventory as inventory


class FakeGitChild:
    def __init__(self, output: bytes, returncode: int = 0, *, running: bool = False):
        self.stdout = io.BytesIO(output)
        self.returncode = None if running else returncode
        self.final_returncode = returncode
        self.kill = Mock(side_effect=self._kill)
        self.wait = Mock(side_effect=self._wait)

    def _kill(self) -> None:
        self.returncode = -9

    def _wait(self, timeout: float) -> int:
        if self.returncode is None:
            self.returncode = self.final_returncode
        return self.returncode

    def poll(self) -> int | None:
        return self.returncode


class MigrationInventoryTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.tracked: list[str] = []

    def source(self, path: str, content: str | bytes, *, tracked: bool = True) -> Path:
        destination = self.root / path
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(content.encode("utf-8") if isinstance(content, str) else content)
        if tracked:
            self.tracked.append(path)
        return destination

    def collect(self) -> dict[str, object]:
        metadata = {
            "status": "available", "commit": "a" * 40, "commit_status": "available",
            "file_list_status": "available", "source_basis": "working_tree_bytes_for_indexed_paths",
        }
        with patch.object(inventory, "_tracked_files", return_value=(metadata, sorted(self.tracked))):
            return inventory.collect_inventory(self.root)

    def test_static_inventory_preserves_tables_branches_options_and_expressions(self) -> None:
        self.source("src/intake_ui_routes/mutations.py", '''
from missing_package import dangerous
_POST_EXACT = {"/api/first": RouteMatch("_handle_first"), DYNAMIC_PATH: make_route()}
_PUT_EXACT = {}
_VAULT_COLLECTIONS = (("/api/vault/projects/", "project"),)
_JOB_POST_SUFFIXES = (("/start", "_handle_start"),)
def _post_route(route):
    if route == "/api/first":
        return _POST_EXACT[route]
    if route.startswith("/api/jobs/"):
        if route.endswith("/start"):
            return make_route(route)
    return None
''')
        self.source("src/intake_ui_routes/reads.py", r'''
STATIC_FILES = {"/": ("index.html", "text/html"), "/favicon.svg": ("favicon.svg", "image/svg+xml"), **dynamic_files}
def _dispatch_collection_get(target, parsed, support):
    if parsed.path == "/api/health":
        return True
    if "\\" not in parsed.path:
        return False
    return False
''')
        self.source("src/intake_ui_routes/contracts.py", '''
CAPABILITY_HEADER = "X-Career-Hub-Token"
EXTENSION_ALLOWED_REQUESTS = frozenset({("POST", "/api/capture")})
''')
        self.source("src/entry.py", '''
import nonexistent
AGENT_WORKFLOW_CONTRACT_VERSION = 4
SCHEMA_VERSION: int = derive_version()
def _add_root_arguments(parser):
    parser.add_argument("--data-root", type=Path)
def build_parser():
    parser = ArgumentParser()
    _add_root_arguments(parser)
    commands = parser.add_subparsers(dest="command", required=True)
    start = commands.add_parser("start")
    start.add_argument("--port", type=int, default=8766)
    start.add_argument("--channel", choices=sorted(CHANNELS))
    group = start.add_mutually_exclusive_group(required=True)
    group.add_argument("--stdin", action="store_true")
    return parser
''')
        result = self.collect()
        self.assertFalse(result["complete"])
        self.assertEqual(result["characterization_status"], "not_run")
        self.assertEqual(result["python"]["src_module_count"], 4)
        tables = {row["symbol"]: row for row in result["http"]["tables"]}
        self.assertEqual(tables["_POST_EXACT"]["entries"][0]["key"]["value"], "/api/first")
        self.assertEqual(tables["_POST_EXACT"]["entries"][1]["key"]["expression"], "DYNAMIC_PATH")
        self.assertEqual(tables["_POST_EXACT"]["entries"][0]["value"]["kind"], "expression")
        self.assertEqual(tables["_JOB_POST_SUFFIXES"]["entries"][0]["value"], ["/start", "_handle_start"])
        self.assertEqual(tables["STATIC_FILES"]["entries"][1]["key"]["value"], "/favicon.svg")
        self.assertTrue(tables["STATIC_FILES"]["entries"][2]["is_expansion"])
        branches = [row for row in result["http"]["branches"] if row["symbol"] == "_post_route"]
        self.assertEqual([row["lexical_order"] for row in branches], [1, 2, 3])
        self.assertEqual(branches[0]["predicate"], "route == '/api/first'")
        self.assertEqual(branches[2]["predicate"], "route.endswith('/start')")
        read_predicates = [row["predicate"] for row in result["http"]["branches"] if row["symbol"] == "_dispatch_collection_get"]
        self.assertEqual(read_predicates[1], "'\\\\' not in parsed.path")
        declarations = result["cli"][0]["declarations"]
        self.assertEqual(declarations[0]["symbol"], "_add_root_arguments")
        self.assertIn("start", [row["arguments"][0].get("value") for row in declarations if row["arguments"]])
        channel = next(row for row in declarations if row["arguments"] and row["arguments"][0].get("value") == "--channel")
        self.assertEqual(channel["keywords"][0]["value"], {"kind": "expression", "expression": "sorted(CHANNELS)"})
        versions = {row["symbol"]: row["value"] for row in result["contract_versions"]}
        self.assertEqual(versions["AGENT_WORKFLOW_CONTRACT_VERSION"], {"kind": "literal", "value": 4})
        self.assertEqual(versions["SCHEMA_VERSION"]["kind"], "expression")

    def test_does_not_import_execute_or_modify_sources(self) -> None:
        self.source("src/__init__.py", 'raise RuntimeError("target package imported")\n')
        self.source("src/side_effect.py", '''
from pathlib import Path
Path(__file__).with_suffix(".executed").write_text("bad")
raise RuntimeError("target module executed")
def build_parser():
    raise RuntimeError("target function executed")
''')
        before = {path: ((self.root / path).read_bytes(), (self.root / path).stat().st_mtime_ns)
                  for path in self.tracked}
        result = self.collect()
        self.assertEqual(len(result["cli"]), 1)
        self.assertFalse((self.root / "src/side_effect.executed").exists())
        self.assertFalse((self.root / "src/__pycache__").exists())
        self.assertEqual(before, {path: ((self.root / path).read_bytes(), (self.root / path).stat().st_mtime_ns)
                                  for path in self.tracked})

    def test_fingerprints_deterministic_and_source_drift_visible(self) -> None:
        path = self.source("src/example.py", "SCHEMA_VERSION = 1\n")
        self.source("src/data/catalog.yaml", "name: synthetic\n")
        self.source("requirements.txt", "example==1.0\n")
        self.source("AGENTS.md", "Synthetic instructions\n")
        self.source("desktop/src/sidecar.js", "const PROTOCOL_VERSION = 1;\n")
        self.source("extension/service-worker.js", "const VERSION = 1;\n")
        self.source("src/intake_ui_static/ui/api.js", "export const api = {};\n")
        first = self.collect()
        self.assertEqual(first, self.collect())
        row = next(row for row in first["files"] if row["path"] == "src/example.py")
        self.assertEqual(row["sha256"], hashlib.sha256(path.read_bytes()).hexdigest())
        self.assertEqual({row["group"] for row in first["consumer_fingerprints"]}, {"desktop", "extension", "frontend"})
        path.write_text("SCHEMA_VERSION = 2\n", encoding="utf-8")
        changed = self.collect()
        self.assertNotEqual(first["source_fingerprint"], changed["source_fingerprint"])
        self.assertEqual(first["git"]["commit"], changed["git"]["commit"])
        stable = {row["path"]: row["sha256"] for row in first["files"] if row["path"] != "src/example.py"}
        self.assertEqual(stable, {row["path"]: row["sha256"] for row in changed["files"] if row["path"] != "src/example.py"})

    def test_malformed_and_undecodable_ast_are_hashed_with_explicit_gaps(self) -> None:
        self.source("src/bad.py", "def broken(:\n")
        self.source("src/undecodable.py", b"value = '\xff'\n")
        self.source("src/valid.py", "# one\n\nVALUE = 1")
        result = self.collect()
        self.assertEqual(len(result["files"]), 3)
        self.assertEqual(result["python"]["src_module_count"], 3)
        self.assertEqual(result["python"]["src_physical_lines"], 5)
        self.assertEqual(len([gap for gap in result["gaps"] if gap["code"] == "python_ast_invalid"]), 2)
        self.assertEqual([row["parse_status"] for row in result["python"]["modules"]], ["invalid", "invalid", "parsed"])

    def test_private_untracked_binary_and_escape_paths_are_excluded(self) -> None:
        self.source("src/safe.py", "VALUE = 1\n")
        self.source("src/untracked.py", "raise Exception('private')", tracked=False)
        for path in ("career_vault/profile.yaml", "tmp/worktree/src/code.py", "src/node_modules/code.js",
                     "src/.env", "src/data/object.bin", "src/private_documents/identity.txt"):
            self.source(path, "PRIVATE CONTENT")
        self.source("src/binary.py", b"BINARY\0CONTENT")
        self.tracked.extend(["../escape.py", "/absolute.py", "src/../../escape.py", "src/C:/escape.py"])
        with patch.object(inventory, "_read_source", wraps=inventory._read_source) as reader:
            result = self.collect()
        read_paths = {call.args[1] for call in reader.call_args_list}
        self.assertEqual(read_paths, {"src/safe.py", "src/binary.py"})
        self.assertEqual([row["path"] for row in result["files"]], ["src/safe.py"])
        rendered = json.dumps(result)
        self.assertNotIn("PRIVATE CONTENT", rendered)
        self.assertNotIn(str(self.root), rendered)
        self.assertNotIn("escape.py", rendered)
        self.assertIn("binary_content_excluded", rendered)

    def test_absolute_source_literals_are_redacted(self) -> None:
        self.source("src/cli.py", '''
SCHEMA_VERSION = Path("C:/Users/example/data")
def build_parser():
    parser.add_argument("--data-root", default="/home/example/data")
''')
        rendered = json.dumps(self.collect())
        self.assertNotIn("C:/Users/example", rendered)
        self.assertNotIn("/home/example", rendered)
        self.assertIn("absolute-path-redacted", rendered)

    def test_git_unavailable_fails_closed_without_directory_fallback(self) -> None:
        self.source("src/should_not_read.py", "raise Exception('private')")
        with patch.object(inventory.subprocess, "Popen", side_effect=FileNotFoundError), \
                patch.object(inventory, "_read_source") as reader:
            result = inventory.collect_inventory(self.root)
        reader.assert_not_called()
        self.assertEqual(result["git"]["status"], "unavailable")
        self.assertEqual(result["git"]["file_list_status"], "git_unavailable")
        self.assertEqual(result["files"], [])
        self.assertFalse(result["complete"])

    def test_git_calls_use_arrays_timeout_and_no_global_mutations(self) -> None:
        self.source("src/example.py", "VALUE = 1\n")
        responses = [FakeGitChild(b"b" * 40 + b"\n"), FakeGitChild(b"src/example.py\0")]
        with patch.object(inventory.subprocess, "Popen", side_effect=responses) as run:
            result = inventory.collect_inventory(self.root)
        self.assertEqual(result["git"]["commit"], "b" * 40)
        for call in run.call_args_list:
            self.assertIsInstance(call.args[0], list)
            self.assertNotIn("shell", call.kwargs)
            self.assertNotIn("config", call.args[0])
            self.assertIn("--no-optional-locks", call.args[0])
        self.assertIn("ls-files", run.call_args_list[1].args[0])
        for child in responses:
            self.assertGreater(child.wait.call_args_list[0].kwargs["timeout"], 0)
            self.assertLessEqual(child.wait.call_args_list[0].kwargs["timeout"], inventory.GIT_TIMEOUT_SECONDS)

    def test_git_errors_and_bounds_are_sanitized(self) -> None:
        for outcome, expected in (
            (OSError("private path"), "git_execution_failed"),
            (FakeGitChild(b"private", 128), "git_query_failed"),
            (FakeGitChild(b"x" * 20), "git_output_limit"),
        ):
            with self.subTest(expected=expected), patch.object(inventory, "MAX_GIT_OUTPUT_BYTES", 10):
                kwargs = {"side_effect": outcome} if isinstance(outcome, Exception) else {"return_value": outcome}
                with patch.object(inventory.subprocess, "Popen", **kwargs):
                    value, error = inventory._git(self.root, ["rev-parse", "HEAD"])
                self.assertIsNone(value)
                self.assertEqual(error, expected)

    def test_git_output_limit_stops_and_reaps_still_running_child(self) -> None:
        child = FakeGitChild(b"x" * 100, running=True)
        child.stdout = Mock(wraps=child.stdout)
        with patch.object(inventory, "MAX_GIT_OUTPUT_BYTES", 10), \
                patch.object(inventory.subprocess, "Popen", return_value=child):
            value, error = inventory._git(self.root, ["ls-files"])
        self.assertIsNone(value)
        self.assertEqual(error, "git_output_limit")
        child.stdout.read.assert_called_once_with(11)
        child.kill.assert_called_once_with()
        child.wait.assert_called_with(timeout=1)

    def test_git_timeout_is_sanitized_and_owned_child_is_reaped(self) -> None:
        child = FakeGitChild(b"", running=True)
        child.wait.side_effect = [subprocess.TimeoutExpired(["git", str(self.root)], 5), -9]
        with patch.object(inventory.subprocess, "Popen", return_value=child):
            value, error = inventory._git(self.root, ["ls-files"])
        self.assertIsNone(value)
        self.assertEqual(error, "git_timeout")
        child.kill.assert_called_once_with()

    def test_stalled_git_pipe_has_bounded_wait_and_cleanup(self) -> None:
        released = Event()
        child = FakeGitChild(b"", running=True)
        child.stdout = Mock()
        child.stdout.read.side_effect = lambda _limit: (released.wait(1), b"")[1]

        def kill_and_release() -> None:
            child._kill()
            released.set()

        child.kill.side_effect = kill_and_release
        with patch.object(inventory, "GIT_TIMEOUT_SECONDS", 0.01), \
                patch.object(inventory.subprocess, "Popen", return_value=child):
            value, error = inventory._git(self.root, ["ls-files"])
        self.assertIsNone(value)
        self.assertEqual(error, "git_timeout")
        child.kill.assert_called_once_with()
        child.stdout.close.assert_called_once_with()

    def test_file_and_total_size_bounds_are_explicit(self) -> None:
        self.source("src/a.py", "VALUE = 1\n")
        self.source("src/b.py", "VALUE = 2\n")
        with patch.object(inventory, "MAX_FILE_BYTES", 4):
            result = self.collect()
        self.assertEqual(result["files"], [])
        self.assertEqual(len([gap for gap in result["gaps"] if gap["code"] == "file_byte_limit"]), 2)
        with patch.object(inventory, "MAX_TOTAL_BYTES", 11):
            result = self.collect()
        self.assertEqual(len(result["files"]), 1)
        self.assertIn("total_byte_limit", {gap["code"] for gap in result["gaps"]})
        with patch.object(inventory, "MAX_FILES", 1):
            result = self.collect()
        self.assertIn("file_count_limit", {gap["code"] for gap in result["gaps"]})

    def test_symlink_sources_are_excluded(self) -> None:
        target = self.source("private.txt", "PRIVATE CONTENT", tracked=False)
        link = self.root / "src/link.py"
        link.parent.mkdir()
        try:
            link.symlink_to(target)
        except OSError:
            self.skipTest("This host does not permit unprivileged symlinks")
        self.tracked.append("src/link.py")
        result = self.collect()
        self.assertEqual(result["files"], [])
        self.assertIn("link_or_reparse_excluded", {gap["code"] for gap in result["gaps"]})

    def test_cli_emits_json_stdout_without_writing_a_report(self) -> None:
        output = io.StringIO()
        with patch.object(inventory, "collect_inventory", return_value={"complete": False}) as collector, redirect_stdout(output):
            status = inventory.main(["--project-root", str(self.root)])
        self.assertEqual(status, 0)
        self.assertEqual(json.loads(output.getvalue()), {"complete": False})
        collector.assert_called_once_with(self.root)
        self.assertEqual(list(self.root.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
