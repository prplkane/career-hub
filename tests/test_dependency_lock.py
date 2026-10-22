from __future__ import annotations

import re
import unittest
from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parents[1]
DIRECT_REQUIREMENT = re.compile(r"^([A-Za-z0-9_.-]+)")


def canonicalize(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name).lower()


class DependencyLockTests(unittest.TestCase):
    def test_direct_dependencies_have_exact_hash_pins(self) -> None:
        requirements = (PROJECT_ROOT / "requirements.in").read_text(
            encoding="utf-8"
        )
        lock = (PROJECT_ROOT / "requirements.txt").read_text(encoding="utf-8")

        direct_names = {
            canonicalize(match.group(1))
            for line in requirements.splitlines()
            if (match := DIRECT_REQUIREMENT.match(line.strip()))
        }
        locked_names = {
            canonicalize(match.group(1))
            for line in lock.splitlines()
            if (match := re.match(r"^([A-Za-z0-9_.-]+)==[^\s\\]+", line))
        }

        self.assertTrue(direct_names)
        self.assertTrue(direct_names.issubset(locked_names))
        self.assertNotIn("# WARNING", lock)
        self.assertIn("--hash=sha256:", lock)

    def test_lock_uses_exact_versions_only(self) -> None:
        lock = (PROJECT_ROOT / "requirements.txt").read_text(encoding="utf-8")
        requirement_lines = [
            line
            for line in lock.splitlines()
            if line and not line.startswith(("#", " "))
        ]

        self.assertTrue(requirement_lines)
        self.assertTrue(all("==" in line for line in requirement_lines))


if __name__ == "__main__":
    unittest.main()
