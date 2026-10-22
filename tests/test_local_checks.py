import contextlib
import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from scripts.local_checks import Runner, run_selected
from scripts.native_integration import native_environment


class NativeSelectionTests(unittest.TestCase):
    def run_selection(self, selected, fail_at=None):
        calls = []
        def run(name, command, **options):
            calls.append((name, command, options))
            return name != fail_at
        runner = SimpleNamespace(root=Path.cwd(), directory=Path.cwd() / "synthetic-report", run=run)
        with contextlib.redirect_stdout(io.StringIO()):
            passed = run_selected(runner, selected)
        return passed, calls

    def test_native_always_builds_then_compiles_fixture_then_runs_integration(self):
        passed, calls = self.run_selection({"native"})
        self.assertTrue(passed)
        self.assertEqual([name for name, _, _ in calls], [
            "Build current native sidecar", "Compile native startup fixture", "Native supervisor integration"])
        fixture = calls[1][1][calls[1][1].index("-o") + 1]
        self.assertEqual(calls[2][1][-1], fixture)
        self.assertIn("./internal/sidecar/startup", calls[1][1])
        self.assertIn("--allow-dirty", calls[0][1])

    def test_build_or_fixture_failure_prevents_integration(self):
        for label, count in [("Build current native sidecar", 1), ("Compile native startup fixture", 2)]:
            with self.subTest(label=label):
                passed, calls = self.run_selection({"native"}, label)
                self.assertFalse(passed)
                self.assertEqual(len(calls), count)

    def test_go_regression_bounds_package_concurrency_without_retries(self):
        passed, calls = self.run_selection({"go"})
        self.assertTrue(passed)
        command = next(command for name, command, _ in calls if name == "Go regression tests")
        self.assertEqual(command[1:], ["test", "-p", "2", "./cmd/...", "./internal/..."])

    def test_quick_desktop_checks_clear_inherited_native_opt_ins(self):
        with patch.dict(os.environ, {"CAREERHUB_TEST_GO_SIDECAR": "1", "CAREERHUB_GO_SIDECAR": "stale"}):
            passed, calls = self.run_selection({"desktop"})
        self.assertTrue(passed)
        for _, _, options in calls:
            for name in ("CAREERHUB_TEST_GO_SIDECAR", "CAREERHUB_GO_SIDECAR",
                         "CAREERHUB_GO_SIDECAR_SHA256", "CAREERHUB_GO_STARTUP_FIXTURE"):
                self.assertEqual(options["env"][name], "")

    def test_native_configuration_fails_closed_and_hashes_the_current_binary(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            fixture = root / "fixture.exe"
            binary = root / "packaging/dist/go-onedir/career-hub-sidecar/career-hub-sidecar.exe"
            with patch("scripts.native_integration.os", SimpleNamespace(name="nt", environ={
                "CAREERHUB_GO_SIDECAR": "stale", "CAREERHUB_GO_SIDECAR_SHA256": "wrong",
            })), patch("scripts.native_integration.git_environment", side_effect=lambda root, env: env):
                with self.assertRaisesRegex(ValueError, "Missing fresh sidecar"):
                    native_environment(root, fixture)
                binary.parent.mkdir(parents=True)
                binary.write_bytes(b"current build")
                with self.assertRaisesRegex(ValueError, "Missing fresh startup fixture"):
                    native_environment(root, fixture)
                fixture.write_bytes(b"current startup fixture")
                env = native_environment(root, fixture)
                self.assertEqual(env["CAREERHUB_TEST_GO_SIDECAR"], "1")
                self.assertEqual(env["CAREERHUB_GO_SIDECAR"], str(binary))
                self.assertEqual(env["CAREERHUB_GO_SIDECAR_SHA256"], hashlib.sha256(binary.read_bytes()).hexdigest())
                self.assertEqual(env["CAREERHUB_GO_STARTUP_FIXTURE"], str(fixture))
            with patch("scripts.native_integration.os", SimpleNamespace(name="posix")):
                with self.assertRaisesRegex(ValueError, "requires Windows"):
                    native_environment(root, fixture)


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.runner = Runner()
        self.addCleanup(shutil.rmtree, self.runner.directory)

    def test_success_records_duration_and_keeps_verbose_output_local(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertTrue(self.runner.run("synthetic pass", [sys.executable, "-c", "print('local-detail')"]))
            self.runner.finish()
        self.assertNotIn("local-detail", output.getvalue())
        record = json.loads((self.runner.directory / "result.json").read_text())["checks"][0]
        self.assertEqual(record["status"], "PASS")
        self.assertGreaterEqual(record["seconds"], 0)
        self.assertIn("local-detail", (self.runner.directory / record["log"]).read_text())

    def test_nonzero_exit_is_failure(self):
        self.assertFalse(self.runner.run("failure", [sys.executable, "-c", "raise SystemExit(7)"]))
        self.assertEqual(self.runner.results[0]["status"], "FAIL")
        self.assertIn("7", self.runner.results[0]["reason"])

    def test_missing_required_tool_is_error_not_skip(self):
        self.assertFalse(self.runner.run("missing tool", ["career-hub-definitely-missing-tool"]))
        self.assertEqual(self.runner.results[0]["status"], "ERROR")

    def test_command_timeout_is_error(self):
        self.assertFalse(self.runner.run("timeout", [sys.executable, "-c", "import time; time.sleep(60)"], timeout=0.1))
        self.assertEqual(self.runner.results[0]["status"], "ERROR")
        self.assertIn("Timed out", self.runner.results[0]["reason"])

    def test_overall_budget_stops_next_command(self):
        self.runner.budget = 0
        self.assertFalse(self.runner.run("budget", [sys.executable, "-c", "print('must not run')"]))
        self.assertIn("budget exhausted", self.runner.results[0]["reason"])

    def test_hook_git_environment_cannot_redirect_nested_fixture_commands(self):
        with tempfile.TemporaryDirectory(prefix="career-hub-git-env-") as temporary:
            root = Path(temporary) / "guard"
            root.mkdir()
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            nested = Path(temporary) / "nested.git"
            self.runner.root = root
            with patch.dict(os.environ, {"GIT_DIR": str(root / ".git"),
                                         "GIT_WORK_TREE": str(root),
                                         "GIT_INDEX_FILE": str(root / ".git/index")}):
                self.assertTrue(self.runner.run("nested repository", [sys.executable, "-c",
                    "import subprocess,sys; subprocess.run(['git','init','--bare','-q',sys.argv[1]],check=True)", str(nested)]))
            self.assertEqual(subprocess.check_output(["git", "config", "--local", "core.bare"], cwd=root, text=True).strip(), "false")
            self.assertTrue((nested / "HEAD").is_file())


if __name__ == "__main__":
    unittest.main()
