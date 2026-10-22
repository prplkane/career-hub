from __future__ import annotations

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]

# Refusal takes well under a second locally; a cold PowerShell start on a
# hosted CI runner has exceeded 15 seconds, so bound the hang generously.
POWERSHELL_TIMEOUT_SECONDS = 60


@unittest.skipUnless(os.name == "nt" and shutil.which("powershell.exe"), "Windows launcher")
class LauncherSecurityTests(unittest.TestCase):
    def test_retired_data_and_server_options_refuse_before_launch(self) -> None:
        with tempfile.TemporaryDirectory(prefix="careerhub-launch-refusal-") as directory:
            fixture = Path(directory)
            sentinel = fixture / "preserve.txt"
            sentinel.write_text("synthetic original", encoding="utf-8")
            for args in (["-DataRoot", str(fixture)], ["-Port", "8766"], ["-LegacyRepositoryData"]):
                with self.subTest(args=args):
                    result = subprocess.run(
                        ["powershell.exe", "-NoProfile", "-NonInteractive", "-File",
                         str(ROOT / "scripts/start_career_hub.ps1"), *args],
                        capture_output=True, text=True, timeout=POWERSHELL_TIMEOUT_SECONDS,
                    )
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn("NamedParameterNotFound", result.stderr)
                    self.assertEqual(list(fixture.iterdir()), [sentinel])
                    self.assertEqual(sentinel.read_text(encoding="utf-8"), "synthetic original")

    def test_conflicting_extension_authorization_refuses_before_launch(self) -> None:
        result = subprocess.run(
            ["powershell.exe", "-NoProfile", "-NonInteractive", "-File",
             str(ROOT / "scripts/start_career_hub.ps1"), "-EnableExtension",
             "-ExtensionOrigin", "chrome-extension://synthetic"],
            capture_output=True, text=True, timeout=POWERSHELL_TIMEOUT_SECONDS,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("cannot be used together", result.stderr)


if __name__ == "__main__":
    unittest.main()
