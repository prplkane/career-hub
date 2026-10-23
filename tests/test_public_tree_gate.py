from __future__ import annotations

import contextlib
import io
import subprocess
import tempfile
import unittest
from pathlib import Path

from scripts.check_public_tree import (
    content_violations,
    path_violations,
    reviewed_binary_matches,
    PATH_ALLOWED_EMAIL_ADDRESSES,
)


class PublicTreeGateTests(unittest.TestCase):
    def test_private_roots_allow_only_gitkeep(self) -> None:
        self.assertEqual(path_violations("career_vault/raw/.gitkeep"), [])
        self.assertIn(
            "private data path is tracked",
            path_violations("career_vault/raw/resume.docx"),
        )
        self.assertIn(
            "private data path is tracked",
            path_violations("jobs/normalized/example.yaml"),
        )
        self.assertIn(
            "private data path is tracked",
            path_violations("documents/files/example.dpapi"),
        )
        self.assertIn(
            "private data path is tracked",
            path_violations("logs/competency_migrations/example.json"),
        )

    def test_private_artifact_types_are_rejected_anywhere(self) -> None:
        for path in (
            "resume.pdf",
            "fixtures/candidate.docx",
            "data/application.sqlite3",
            "certs/client.pem",
            ".npmrc",
        ):
            with self.subTest(path=path):
                self.assertTrue(path_violations(path))

    def test_example_contacts_are_allowed(self) -> None:
        self.assertEqual(
            content_violations(
                "candidate@example.com | alerts@example.test | test@example.invalid | "
                "digest@mail.jobs.example | (555) 010-2026 | "
                "194863141+example@users.noreply.github.com"
            ),
            [],
        )

    def test_reviewed_extension_icons_require_exact_content(self) -> None:
        project_root = Path(__file__).resolve().parents[1]
        relative_path = "extension/icons/icon-16.png"
        raw = (project_root / relative_path).read_bytes()
        self.assertTrue(reviewed_binary_matches(relative_path, raw))
        self.assertFalse(reviewed_binary_matches(relative_path, raw + b"changed"))
        self.assertFalse(reviewed_binary_matches("assets/icon-16.png", raw))

    def test_known_service_sender_is_narrowly_allowed(self) -> None:
        self.assertEqual(
            content_violations("career-hub-capture@careerhub.local"),
            [],
        )
        self.assertEqual(content_violations("jobs-noreply@linkedin.com"), [])
        self.assertEqual(
            content_violations("handshake@notifications.joinhandshake.com"),
            [],
        )
        self.assertIn(
            "non-example email address",
            content_violations("person" + "@linkedin.com"),
        )
        self.assertIn(
            "non-example email address",
            content_violations("person" + "@notifications.joinhandshake.com"),
        )

    def test_reviewed_fonts_require_exact_path_and_content(self) -> None:
        project_root = Path(__file__).resolve().parents[1]
        for name in ("Vera.ttf", "VeraBd.ttf"):
            relative = "backend/internal/materials/materialrender/assets/" + name
            raw = (project_root / relative).read_bytes()
            self.assertTrue(reviewed_binary_matches(relative, raw))
            self.assertFalse(reviewed_binary_matches(relative, raw + b"changed"))
            self.assertFalse(reviewed_binary_matches("elsewhere/" + name, raw))

    def test_attribution_and_provider_exceptions_remain_path_scoped(self) -> None:
        for relative, addresses in PATH_ALLOWED_EMAIL_ADDRESSES.items():
            for address in addresses:
                with self.subTest(path=relative, address=address):
                    self.assertIn("non-example email address", content_violations(address))
                    self.assertEqual(content_violations(address, allowed_email_addresses=addresses), [])
                    self.assertIn("non-example email address", content_violations(
                        "unreviewed" + "@gmail.com", allowed_email_addresses=addresses))

    def test_realistic_contacts_and_user_paths_are_rejected(self) -> None:
        self.assertIn(
            "non-example email address",
            content_violations("candidate" + "@personal.test"),
        )
        self.assertIn(
            "non-example email address",
            content_violations("candidate" + "@gmail.com"),
        )
        self.assertIn(
            "non-example phone number",
            content_violations("(" + "480" + ") 555-0123"),
        )
        self.assertIn(
            "absolute user-profile path",
            content_violations("C:" + r"\Users\Candidate\resume.docx"),
        )
        self.assertIn(
            "absolute user-profile path",
            content_violations("/" + "home/candidate/resume.docx"),
        )

    def test_explicit_synthetic_profile_names_are_allowed(self) -> None:
        self.assertEqual(
            content_violations("/Users/Test/Library/Application Support"),
            [],
        )
        self.assertEqual(content_violations("/home/example/data"), [])
        self.assertIn(
            "absolute user-profile path",
            content_violations("/" + "home/tester/data"),
        )

    def test_dependency_metadata_allows_only_reviewed_upstream_contact(self) -> None:
        reviewed_contact = "i" + "@izs.me"
        self.assertEqual(
            content_violations(
                f"Upstream support: {reviewed_contact}",
                allowed_email_addresses=frozenset({reviewed_contact}),
            ),
            [],
        )
        self.assertIn(
            "non-example email address",
            content_violations(
                "Private contact: maintainer" + "@vendor.invalid",
                allowed_email_addresses=frozenset({reviewed_contact}),
            ),
        )
        self.assertIn(
            "absolute user-profile path",
            content_violations(
                "/" + "home/private-user/local-package",
                allowed_email_addresses=frozenset({reviewed_contact}),
            ),
        )


if __name__ == "__main__":
    unittest.main()


class RevisionAuditTests(unittest.TestCase):
    """`--rev` audits what a commit records, not what the checkout contains."""

    GIT = (
        "git",
        "-c", "commit.gpgsign=false",
        "-c", "user.name=Test",
        "-c", "user.email=test@example.com",
        "-c", "core.autocrlf=false",
    )

    def _git(self, root: Path, *args: str) -> str:
        return subprocess.run(
            [*self.GIT, *args], cwd=root, check=True, capture_output=True, text=True
        ).stdout.strip()

    def _repo(self, stack: contextlib.ExitStack) -> Path:
        root = Path(stack.enter_context(
            tempfile.TemporaryDirectory(ignore_cleanup_errors=True)
        )).resolve()
        self._git(root, "init", "-q", "-b", "main")
        return root

    def _commit(self, root: Path, message: str) -> str:
        self._git(root, "add", "-A")
        self._git(root, "commit", "-q", "-m", message)
        return self._git(root, "rev-parse", "HEAD")

    def test_secret_added_then_deleted_is_caught_only_in_its_commit(self) -> None:
        from scripts.check_public_tree import audit_public_tree, audit_revision

        with contextlib.ExitStack() as stack:
            root = self._repo(stack)
            (root / "README.md").write_text("clean\n", encoding="utf-8")
            (root / "secrets").mkdir()
            (root / "secrets" / "x.env").write_text("TOKEN=synthetic\n", encoding="utf-8")
            (root / "notes.md").write_text("mail me: somebody@realmail.example.org\n", encoding="utf-8")
            first = self._commit(root, "leak")
            (root / "secrets" / "x.env").unlink()
            (root / "notes.md").write_text("mail me: somebody@example.com\n", encoding="utf-8")
            second = self._commit(root, "cleanup")

            self.assertEqual(audit_revision(root, second), [])
            self.assertEqual(audit_public_tree(root), [])
            first_errors = audit_revision(root, first)
            self.assertIn("secrets/x.env: private data path is tracked", first_errors)
            self.assertIn("secrets/x.env: private or binary artifact type is tracked", first_errors)
            self.assertNotIn("notes.md: non-example email address", first_errors)

    def test_dirty_working_copy_does_not_change_the_commit_verdict(self) -> None:
        from scripts.check_public_tree import audit_public_tree, audit_revision

        with contextlib.ExitStack() as stack:
            root = self._repo(stack)
            (root / "README.md").write_text("contact: team@example.com\n", encoding="utf-8")
            head = self._commit(root, "clean")
            (root / "README.md").write_text("contact: real.person@" + "gmail.com\n", encoding="utf-8")

            self.assertEqual(audit_revision(root, head), [])
            self.assertIn("README.md: non-example email address", audit_public_tree(root))

    def test_entry_kinds_mirror_tree_audit_wording(self) -> None:
        from scripts.check_public_tree import (
            MAX_TRACKED_FILE_BYTES,
            RevisionEntry,
            _entry_errors,
        )

        blob = RevisionEntry("100644", "blob", "a" * 40, 10, "src/x.py")
        self.assertEqual(_entry_errors(blob), [])
        self.assertEqual(_entry_errors(blob._replace(mode="100755")), [])
        self.assertEqual(
            _entry_errors(blob._replace(mode="120000", size=5)),
            ["tracked symbolic link is not allowed"],
        )
        self.assertEqual(
            _entry_errors(RevisionEntry("160000", "commit", "b" * 40, None, "vendor/dep")),
            ["tracked path is not a regular file"],
        )
        self.assertEqual(
            _entry_errors(blob._replace(size=MAX_TRACKED_FILE_BYTES + 1)),
            [f"tracked file exceeds {MAX_TRACKED_FILE_BYTES} bytes"],
        )

    def test_unknown_revision_is_a_run_failure_not_a_pass(self) -> None:
        from scripts.check_public_tree import main

        with contextlib.redirect_stdout(io.StringIO()) as output:
            code = main(["--rev", "0" * 40])
        self.assertEqual(code, 2)
        self.assertIn("failed to run", output.getvalue())
