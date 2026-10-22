"""Real disposable Git repositories; never push to a hosted remote."""
import contextlib
import io
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from scripts.install_git_hooks import install
from scripts.pre_push import outgoing, require_clean
from scripts.test_selection import ROOT, changed_paths, git

ZERO = "0" * 40


class PushTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="career-hub-push-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "source"
        self.root.mkdir()
        git(self.root, "init", "-b", "main")
        git(self.root, "config", "user.name", "Synthetic Test")
        git(self.root, "config", "user.email", "synthetic@example.test")
        git(self.root, "config", "commit.gpgsign", "false")
        git(self.root, "config", "core.autocrlf", "false")
        self.base = self.commit("README.md", "Synthetic repository\n")

    def commit(self, name, content):
        target = self.root / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")
        git(self.root, "add", "--", name)
        git(self.root, "commit", "-qm", "Synthetic change")
        return git(self.root, "rev-parse", "HEAD").strip()

    def record(self, old=None):
        head = git(self.root, "rev-parse", "HEAD").strip()
        return f"refs/heads/main {head} refs/heads/main {old or self.base}\n"

    def prepare_hook(self):
        (self.root / ".gitignore").write_text("__pycache__/\n")
        for name in ["test_selection.py", "local_checks.py", "pre_push.py", "check_public_tree.py", "__init__.py"]:
            target = self.root / "scripts" / name
            target.parent.mkdir(exist_ok=True)
            source = ROOT / "scripts" / name
            target.write_bytes(source.read_bytes() if source.exists() else b"")
        (self.root / ".githooks").mkdir()
        shutil.copyfile(ROOT / ".githooks/pre-push", self.root / ".githooks/pre-push")
        # Match the repository's executable hook mode before committing on Unix.
        hook = self.root / ".githooks/pre-push"
        hook.chmod(hook.stat().st_mode | 0o111)
        git(self.root, "add", ".")
        git(self.root, "commit", "-qm", "Synthetic tooling")
        remote = Path(self.temp.name) / "remote.git"
        git(self.root, "init", "--bare", str(remote))
        git(self.root, "remote", "add", "origin", str(remote))
        git(self.root, "push", "-qu", "origin", "main")
        self.base = git(self.root, "rev-parse", "HEAD").strip()
        install(self.root)

    def push(self):
        return subprocess.run(["git", "push", "origin", "main"], cwd=self.root, capture_output=True,
                              text=True, timeout=60)

    def test_transient_deleted_file_remains_in_outgoing_commits_and_selection(self):
        transient = self.commit("backend/transient.go", "package synthetic\n")
        git(self.root, "rm", "backend/transient.go")
        git(self.root, "commit", "-qm", "Remove synthetic file")
        commits, paths = outgoing(self.root, "origin", self.record())
        self.assertIn(transient, commits)
        self.assertIn("backend/transient.go", paths)

    def test_new_branch_does_not_exclude_another_remotes_objects(self):
        git(self.root, "remote", "add", "origin", "unused-origin")
        git(self.root, "remote", "add", "other", "unused-other")
        git(self.root, "update-ref", "refs/remotes/origin/main", self.base)
        head = self.commit("README.md", "Outgoing docs\n")
        git(self.root, "update-ref", "refs/remotes/other/main", head)
        commits, _ = outgoing(self.root, "origin", self.record(ZERO))
        self.assertIn(head, commits)
        self.assertNotIn(self.base, commits)

    def test_renamed_code_is_included_in_changed_paths(self):
        self.commit("backend/source.go", "package synthetic\n")
        base = git(self.root, "rev-parse", "HEAD").strip()
        git(self.root, "mv", "backend/source.go", "README.md.new")
        git(self.root, "commit", "-qm", "Rename")
        self.assertIn("backend/source.go", changed_paths(self.root, base))

    def test_dirty_tree_and_changed_head_are_rejected(self):
        (self.root / "README.md").write_text("Unsaved change")
        with self.assertRaisesRegex(ValueError, "pending changes"):
            require_clean(self.root, self.base)
        self.commit("README.md", "Committed change")
        with self.assertRaisesRegex(ValueError, "HEAD changed"):
            require_clean(self.root, self.base)

    def test_tag_nonhead_and_malformed_pushes_fail_explicitly(self):
        for record in ["malformed", self.record().replace("refs/heads/main", "refs/tags/v1"),
                       f"refs/heads/other {'a'*40} refs/heads/other {ZERO}"]:
            with self.subTest(record=record), self.assertRaises(ValueError):
                outgoing(self.root, "origin", record)

    def test_deletion_sends_no_new_content(self):
        self.assertEqual(outgoing(self.root, "origin", f"(delete) {ZERO} refs/heads/old {self.base}"), ([], []))

    def test_existing_hook_is_never_overwritten(self):
        hook = self.root / ".git/hooks/pre-push"
        hook.write_text("existing hook")
        with self.assertRaisesRegex(ValueError, "Existing pre-push"):
            install(self.root)
        self.assertEqual(hook.read_text(), "existing hook")

    def test_existing_hooks_path_is_preserved(self):
        git(self.root, "config", "core.hooksPath", "custom-hooks")
        with self.assertRaisesRegex(ValueError, "Existing core.hooksPath"):
            install(self.root)
        self.assertEqual(git(self.root, "config", "--get", "core.hooksPath").strip(), "custom-hooks")

    def test_worktree_hook_installation_does_not_enable_other_checkout(self):
        linked = Path(self.temp.name) / "linked"
        git(self.root, "worktree", "add", "-b", "linked", str(linked))
        (linked / ".githooks").mkdir()
        (linked / ".githooks/pre-push").write_text("#!/bin/sh\nexit 0\n")
        install(linked)
        self.assertEqual(git(linked, "config", "--get", "core.hooksPath").strip(), (linked / ".githooks").as_posix())
        result = subprocess.run(["git", "config", "--get", "core.hooksPath"], cwd=self.root, capture_output=True)
        self.assertEqual(result.returncode, 1)

    def test_real_hook_rejects_private_file_deleted_in_later_commit(self):
        self.prepare_hook()
        self.commit("private_documents/synthetic.txt", "Synthetic data only")
        git(self.root, "rm", "private_documents/synthetic.txt")
        git(self.root, "commit", "-qm", "Remove private file")
        result = self.push()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("FAIL  Privacy", result.stdout)
        self.assertEqual(git(self.root, "ls-remote", "origin", "refs/heads/main").split()[0], self.base)

    def test_missing_python_stops_actual_push_before_upload(self):
        self.prepare_hook()
        self.commit("README.md", "Outgoing clean documentation\n")
        # Git itself still needs sh on Windows. Remove Python, not Git's runtime.
        if os.name == "nt":
            git_executable = Path(shutil.which("git"))
            shells = [parent / suffix for parent in git_executable.parents
                      for suffix in ("bin/sh.exe", "usr/bin/sh.exe")]
            shell = next(candidate for candidate in shells if candidate.is_file())
            tool_path = os.pathsep.join([str(shell.parent), git(self.root, "--exec-path").strip()])
        else:
            tool_directory = Path(self.temp.name) / "shell-only"
            tool_directory.mkdir()
            (tool_directory / "sh").symlink_to(shutil.which("sh"))
            tool_path = str(tool_directory)
        self.assertIsNone(shutil.which("python", path=tool_path))
        result = subprocess.run([shutil.which("git"), "push", "origin", "main"], cwd=self.root,
                                env={**os.environ, "PATH": tool_path},
                                capture_output=True, text=True, timeout=30)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("requires Python 3", result.stderr)
        self.assertEqual(git(self.root, "ls-remote", "origin", "refs/heads/main").split()[0], self.base)

    def test_missing_remote_base_fails_instead_of_guessing_a_range(self):
        with self.assertRaises(subprocess.CalledProcessError):
            outgoing(self.root, "origin", self.record("a" * 40))

    @unittest.skipUnless(shutil.which("gitleaks"), "Gitleaks integration requires installed scanner")
    def test_real_hook_accepts_documentation_only_push(self):
        self.prepare_hook()
        head = self.commit("README.md", "Clean synthetic documentation\n")
        result = self.push()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("documentation only", result.stdout)
        self.assertEqual(git(self.root, "ls-remote", "origin", "refs/heads/main").split()[0], head)

    @unittest.skipUnless(shutil.which("gitleaks"), "Gitleaks integration requires installed scanner")
    def test_real_secret_scan_rejects_transient_synthetic_token(self):
        self.prepare_hook()
        token = "ghp_" + "aB3dE6gH9jK2mN5pQ8sT1vW4yZ7cF0iL3oR6"
        self.commit("README.md", "synthetic_token=" + token + "\n")
        self.commit("README.md", "Removed synthetic token\n")
        result = self.push()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("FAIL  Outgoing secret scan", result.stdout)
        self.assertNotIn(token, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
