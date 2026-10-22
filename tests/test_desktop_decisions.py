from __future__ import annotations

from pathlib import Path
import unittest


ADR_FILES = (
    "DESK-010a_pyinstaller_layout.md",
    "DESK-010b_app_data_identity.md",
    "DESK-010c_application_version.md",
    "DESK-010d_release_channels.md",
    "DESK-010e_supported_platforms.md",
    "DESK-010f_data_compatibility.md",
)


class DesktopDecisionTests(unittest.TestCase):
    def test_required_desktop_decisions_have_complete_adr_contracts(self) -> None:
        root = Path(__file__).parents[1] / "docs" / "adr"
        required_sections = (
            "**Status:**",
            "## Context",
            "## Alternatives",
            "## Consequences",
            "## Reversal Conditions",
            "**Evidence ticket",
        )

        for filename in ADR_FILES:
            with self.subTest(filename=filename):
                text = (root / filename).read_text(encoding="utf-8")
                for section in required_sections:
                    self.assertIn(section, text)
                self.assertRegex(text, r"## Decision(?: Method)?\n")

    def test_decisions_cover_every_desk_010_gate(self) -> None:
        root = Path(__file__).parents[1] / "docs" / "adr"
        combined = "\n".join(
            (root / filename).read_text(encoding="utf-8") for filename in ADR_FILES
        )

        for required in (
            "onedir",
            "onefile",
            "dev",
            "beta",
            "stable",
            "VERSION",
            "Windows x64",
            "macOS arm64",
            "macOS x64",
            "minimum OS",
            "data-home format",
            "SQLite",
            "pre-migration backup",
        ):
            with self.subTest(required=required):
                self.assertIn(required, combined)


if __name__ == "__main__":
    unittest.main()
