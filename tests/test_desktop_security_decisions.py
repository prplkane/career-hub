from __future__ import annotations

from pathlib import Path
import unittest


ROOT = Path(__file__).parents[1]


class DesktopSecurityDecisionTests(unittest.TestCase):
    def test_sec_240_decision_covers_platform_crypto_and_recovery_gates(self) -> None:
        text = (
            ROOT / "docs" / "adr" / "SEC-240_platform_secret_and_document_protection.md"
        ).read_text(encoding="utf-8")
        normalized = " ".join(text.split())

        for section in (
            "**Status:**",
            "## Context",
            "## Decision",
            "## Verification Gates",
            "## Alternatives",
            "## Consequences",
            "## Reversal Conditions",
            "## Primary References",
        ):
            self.assertIn(section, text)
        for requirement in (
            "kSecUseDataProtectionKeychain=true",
            "kSecAttrSynchronizable=false",
            "kSecAttrAccessibleWhenUnlockedThisDeviceOnly",
            "AES-256-GCM",
            "12-byte nonce",
            "data_home_id",
            "fails closed",
            "Windows-only",
            "recovery copy",
            "independent security review",
        ):
            with self.subTest(requirement=requirement):
                self.assertIn(requirement, normalized)

    def test_offline_decision_keeps_local_work_available_and_avoids_false_claims(self) -> None:
        text = (
            ROOT / "docs" / "adr" / "DESK-020_offline_connectivity.md"
        ).read_text(encoding="utf-8")
        normalized = " ".join(text.split())

        for requirement in (
            "Offline — local features remain available",
            "Connection available",
            "does not display a global “internet verified” promise",
            "No periodic external reachability request",
            "never automatically",
            "local application connection failure",
            "manual `.eml` import remain enabled",
            "cutoff during each network action",
            "notarization ticket is stapled",
        ):
            with self.subTest(requirement=requirement):
                self.assertIn(requirement, normalized)

    def test_roadmap_assigns_offline_behavior_to_electron_evidence_tickets(self) -> None:
        text = (ROOT / "docs" / "product" / "desktop_delivery_roadmap.md").read_text(
            encoding="utf-8"
        )

        self.assertIn("Implement `DESK-020`", text)
        self.assertIn("cutoff during Gmail sync and public-JD resolution", text)
        self.assertIn("distinguishes a sidecar failure from internet unavailability", text)


if __name__ == "__main__":
    unittest.main()
