import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import yaml

from scripts.test_selection import ALL, select


class SelectionTests(unittest.TestCase):
    def test_docs_and_ledger_need_no_application_checks(self):
        self.assertEqual(select(["README.md", "docs/testing.md", ".backlog/tasks/ch-1.md"]),
                         {"checks": [], "code": False, "go": False, "app": False})

    def test_documentation_directory_does_not_hide_executable_changes(self):
        self.assertEqual(set(select(["docs/helper.py"])["checks"]), ALL)

    def test_ui_change_selects_real_workflows_without_full_go_suite(self):
        result = select(["src/intake_ui_static/ui/api.js"])
        self.assertTrue(result["app"])
        self.assertIn("workflows", result["checks"])
        self.assertNotIn("go", result["checks"])
        self.assertFalse(result["go"])

    def test_backend_includes_consumers_and_windows_checks(self):
        result = select(["backend/internal/sidecar/routes.go"])
        self.assertTrue({"go", "desktop", "native", "startup", "workflows"}.issubset(result["checks"]))
        self.assertTrue(result["go"])

    def test_desktop_changes_include_native_but_ui_only_changes_do_not(self):
        self.assertIn("native", select(["desktop/src/sidecar.js"])["checks"])
        self.assertNotIn("native", select(["src/intake_ui_static/ui/api.js"])["checks"])

    def test_shared_dependencies_ci_and_unknown_paths_are_broad(self):
        for path in ["backend/go.mod", "package-lock.json", "desktop/package.json",
                     ".github/workflows/ci.yml", "new-component/rules.yaml"]:
            with self.subTest(path=path):
                self.assertEqual(set(select([path])["checks"]), ALL)

    def test_mixed_change_never_becomes_docs_only(self):
        self.assertEqual(select(["README.md", "extension/worker.js"]), select(["extension/worker.js"]))

    def test_tooling_checks_do_not_build_application(self):
        result = select(["scripts/pre_push.py", "tests/test_pre_push.py"])
        self.assertTrue(result["code"])
        self.assertFalse(result["app"])


class CIPolicyTests(unittest.TestCase):
    def test_windows_jobs_export_canonical_temp_before_native_checks(self):
        root = Path(__file__).resolve().parents[1]
        for workflow, job in [("ci.yml", "desktop"), ("qualification.yml", "qualification")]:
            with self.subTest(workflow=workflow), tempfile.TemporaryDirectory() as temporary:
                document = yaml.load((root / ".github/workflows" / workflow).read_text(), Loader=yaml.BaseLoader)
                steps = document["jobs"][job]["steps"]
                setup = [step for step in steps if step.get("name") == "Use canonical runner-owned temporary fixtures"]
                self.assertEqual(len(setup), 1, "Windows checks must retain canonical temporary fixtures")
                setup = setup[0]
                native = next(step for step in steps if "npm run smoke:native-desktop" in step.get("run", ""))
                self.assertLess(steps.index(setup), steps.index(native))
                runner = Path(temporary).resolve() / "runner"
                runner.mkdir()
                environment_file = runner.parent / "environment"
                environment_file.write_text("KEPT=1\n", encoding="utf-8")
                result = subprocess.run([sys.executable, "-c", setup["run"]], capture_output=True,
                    text=True, timeout=10, env={**os.environ,
                        "RUNNER_TEMP": str(runner / ".." / "runner"),
                        "TEMP": "unused-inherited-temp", "TMP": "unused-inherited-temp",
                        "GITHUB_ENV": str(environment_file)})
                self.assertEqual(result.returncode, 0, result.stderr)
                fixture = runner / "career-hub-test-temp"
                self.assertTrue(fixture.is_dir())
                self.assertEqual(environment_file.read_text(encoding="utf-8"),
                    f"KEPT=1\nTEMP={fixture}\nTMP={fixture}\n")

    def test_expensive_qualification_requires_explicit_dispatch(self):
        root = Path(__file__).resolve().parents[1]
        manual = yaml.load((root / ".github/workflows/qualification.yml").read_text(), Loader=yaml.BaseLoader)
        self.assertEqual(set(manual["on"]), {"workflow_dispatch"})
        commands = "\n".join(step.get("run", "") for step in manual["jobs"]["qualification"]["steps"])
        for command in ["go test -p 2 ./cmd/... ./internal/...", "go vet ./...", "govulncheck@",
                        "python -m scripts.local_checks --profile native",
                        "npm run smoke:competency", "npm run smoke:desktop-recovery",
                        "npm run smoke:diagnostics-history", "npm run test:workflows"]:
            self.assertIn(command, commands)

    def test_privacy_and_security_do_not_skip_documentation_changes(self):
        root = Path(__file__).resolve().parents[1]
        ci = yaml.load((root / ".github/workflows/ci.yml").read_text(), Loader=yaml.BaseLoader)
        for name in ["privacy", "gitleaks", "trivy"]:
            self.assertNotIn("if", ci["jobs"][name])
        self.assertEqual(ci["jobs"]["test"]["name"], "Tests and public-tree gate")
        self.assertNotIn("--profile native", (root / ".github/workflows/ci.yml").read_text())

    def test_portable_go_runs_on_linux_when_selected(self):
        root = Path(__file__).resolve().parents[1]
        ci = yaml.load((root / ".github/workflows/ci.yml").read_text(), Loader=yaml.BaseLoader)
        job = ci["jobs"]["go"]
        self.assertEqual(job["runs-on"], "ubuntu-latest")
        self.assertEqual(job["if"], "needs.privacy.outputs.go == 'true'")
        commands = "\n".join(step.get("run", "") for step in job["steps"])
        for command in ["gofmt -l .", "go vet ./...", "GOOS=windows go vet ./...", "govulncheck@",
                        "go test ./cmd/... ./internal/..."]:
            self.assertIn(command, commands)
        self.assertIn("go", ci["jobs"]["test"]["needs"])
        go_test_jobs = [
            name for name, entry in ci["jobs"].items()
            if any("go test " in step.get("run", "") for step in entry["steps"])
        ]
        self.assertEqual(go_test_jobs, ["go"], "Routine Go tests must stay in the Linux job")


if __name__ == "__main__":
    unittest.main()
