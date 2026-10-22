from __future__ import annotations

import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

from scripts.build_browser_extension import (
    BACKGROUND_SCRIPTS,
    FIREFOX_EXTENSION_ID,
    TARGETS,
    build_target,
    manifest_for_target,
)


class BrowserExtensionBuildTests(unittest.TestCase):
    def test_target_manifests_keep_least_privilege_browser_contracts(self) -> None:
        chrome = manifest_for_target("chrome")
        edge = manifest_for_target("edge")
        firefox = manifest_for_target("firefox")
        safari = manifest_for_target("safari")

        for manifest in (chrome, edge, firefox, safari):
            self.assertEqual(manifest["manifest_version"], 3)
            self.assertEqual(manifest["version"], "0.10.0")
            self.assertEqual(manifest["host_permissions"], ["http://127.0.0.1/*"])
            self.assertNotIn("tabs", manifest["permissions"])
            self.assertNotIn("content_scripts", manifest)

        for manifest in (chrome, edge):
            self.assertEqual(
                manifest["background"],
                {"service_worker": "service-worker.js"},
            )
            self.assertIn("sidePanel", manifest["permissions"])
            self.assertEqual(
                manifest["side_panel"],
                {"default_path": "sidepanel.html"},
            )
            self.assertIn("key", manifest)

        self.assertEqual(firefox["background"], {"scripts": BACKGROUND_SCRIPTS})
        self.assertNotIn("sidePanel", firefox["permissions"])
        self.assertNotIn("side_panel", firefox)
        self.assertEqual(
            firefox["sidebar_action"]["default_panel"],
            "sidepanel.html",
        )
        self.assertEqual(firefox["incognito"], "not_allowed")
        firefox_gecko = firefox["browser_specific_settings"]["gecko"]
        self.assertEqual(
            firefox_gecko,
            {
                "id": FIREFOX_EXTENSION_ID,
                "strict_min_version": "140.0",
                "data_collection_permissions": {
                    "required": [
                        "personallyIdentifyingInfo",
                        "locationInfo",
                        "browsingActivity",
                        "websiteContent",
                        "websiteActivity",
                    ]
                },
            },
        )
        self.assertNotIn(
            "none",
            firefox_gecko["data_collection_permissions"]["required"],
        )
        for manifest in (chrome, edge, safari):
            self.assertNotIn("incognito", manifest)

        self.assertEqual(
            safari["background"],
            {"scripts": BACKGROUND_SCRIPTS, "persistent": False},
        )
        self.assertNotIn("sidePanel", safari["permissions"])
        self.assertNotIn("side_panel", safari)
        self.assertNotIn("sidebar_action", safari)
        self.assertNotIn("key", safari)
        self.assertEqual(
            safari["browser_specific_settings"],
            {"safari": {"strict_min_version": "17.0"}},
        )

    def test_builds_all_unpacked_and_store_upload_packages(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            output_root = Path(temp_dir)
            for target in TARGETS:
                target_root, archive_path = build_target(target, output_root)
                self.assertTrue((target_root / "manifest.json").is_file())
                self.assertTrue((target_root / "browser-api.js").is_file())
                self.assertTrue((target_root / "icons" / "icon-128.png").is_file())
                self.assertTrue(archive_path.is_file())
                self.assertEqual(
                    archive_path.suffix,
                    ".xpi" if target == "firefox" else ".zip",
                )
                manifest = json.loads(
                    (target_root / "manifest.json").read_text(encoding="utf-8")
                )
                self.assertEqual(manifest, manifest_for_target(target))
                with zipfile.ZipFile(archive_path) as archive:
                    names = set(archive.namelist())
                self.assertIn("manifest.json", names)
                self.assertIn("browser-api.js", names)
                self.assertIn("sidepanel.html", names)

    def test_archives_are_reproducible_and_overwrite_is_explicit(self) -> None:
        with tempfile.TemporaryDirectory() as first_dir, tempfile.TemporaryDirectory() as second_dir:
            first_root = Path(first_dir)
            second_root = Path(second_dir)
            _, first_archive = build_target("firefox", first_root)
            _, second_archive = build_target("firefox", second_root)
            self.assertEqual(
                hashlib.sha256(first_archive.read_bytes()).hexdigest(),
                hashlib.sha256(second_archive.read_bytes()).hexdigest(),
            )
            with self.assertRaises(FileExistsError):
                build_target("firefox", first_root)
            rebuilt_root, rebuilt_archive = build_target(
                "firefox",
                first_root,
                overwrite=True,
            )
            self.assertTrue(rebuilt_root.is_dir())
            self.assertTrue(rebuilt_archive.is_file())

    def test_unknown_target_fails_closed(self) -> None:
        with self.assertRaises(ValueError):
            manifest_for_target("unknown")


if __name__ == "__main__":
    unittest.main()
