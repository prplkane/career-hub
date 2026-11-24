import hashlib
import json
import subprocess
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch

from scripts import release_feed as feed

REPO = "example/career-hub"


class ReleaseFeedTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.made = self.root / "squirrel"
        self.made.mkdir()
        self.out = self.root / "release"

    def package(self, channel="stable", identity=None, url=None):
        pkg_id, title = ("CHDesktop", "Career Hub") if channel == "stable" else ("CHDesktopDev", "Career Hub Dev")
        name = f"{pkg_id}-0.3.4-full.nupkg"
        metadata = {"channel": channel, "app_version": "0.3.4", "update_feed_url": url}
        package = self.made / name
        with zipfile.ZipFile(package, "w") as z:
            z.writestr(pkg_id + ".nuspec", f"<package><metadata><id>{identity or pkg_id}</id>"
                       "<version>0.3.4</version></metadata></package>")
            z.writestr("lib/net45/resources/career-hub/build-metadata.json", json.dumps(metadata))
        (self.made / f"{title}-0.3.4 Setup.exe").write_bytes(b"test installer")
        (self.made / "RELEASES").write_text(feed.entry(feed.digest(package, "sha1"), name, package.stat().st_size))
        return package

    def prepare(self, channel):
        feed.prepare(REPO, channel, "0.3.4", self.made, self.out)

    def test_stable_assets_pin_legacy_and_reference_versioned_stable_package(self):
        package = self.package(url=feed.feed_url(REPO))
        self.prepare("stable")
        self.assertEqual((self.out / "RELEASES").read_text(),
                         "BD25B5D4E7031B5B4C04A2102906A686911C1BCC "
                         "https://github.com/example/career-hub/releases/download/v0.3.2/"
                         "CHDesktopDev-0.3.2-full.nupkg 177937795\n")
        manifest = (self.out / "RELEASES-stable").read_text()
        self.assertEqual(feed.stable_version(REPO, manifest), "0.3.4")
        checksum, location, size = manifest.split()
        self.assertEqual(checksum, hashlib.sha1(package.read_bytes()).hexdigest().upper())
        self.assertTrue(location.endswith("/v0.3.4/" + package.name))
        self.assertEqual(int(size), package.stat().st_size)
        for line in (self.out / "SHA256SUMS.txt").read_text().splitlines():
            checksum, name = line.split("  ")
            self.assertEqual(checksum, feed.digest(self.out / name, "sha256"))

    def test_dev_dry_run_requires_updates_disabled(self):
        self.package("dev")
        self.prepare("dev")
        self.assertFalse((self.out / "RELEASES-stable").exists())
        self.assertIn("CHDesktopDev-0.3.4-full.nupkg", (self.out / "RELEASES").read_text())

    def test_rejects_wrong_nuspec_embedded_feed_manifest_and_extra_package(self):
        self.package(identity="CHDesktopDev", url=feed.feed_url(REPO))
        with self.assertRaisesRegex(ValueError, "NuGet"):
            self.prepare("stable")
        self.package(url="https://github.com/example/career-hub/releases/latest/download")
        with self.assertRaisesRegex(ValueError, "Embedded"):
            self.prepare("stable")
        self.package(url=feed.feed_url(REPO))
        (self.made / "RELEASES").write_text("wrong digest")
        with self.assertRaisesRegex(ValueError, "RELEASES"):
            self.prepare("stable")
        (self.made / "CHDesktopDev-0.3.4-full.nupkg").touch()
        with self.assertRaisesRegex(ValueError, "exactly"):
            self.prepare("stable")
        self.assertFalse(self.out.exists())

    def test_manifest_cannot_point_to_another_channel_tag_or_repository(self):
        for location in ("CHDesktopDev-0.3.4-full.nupkg",
                         feed.package_url(REPO, "0.3.3", "CHDesktop-0.3.4-full.nupkg"),
                         feed.package_url("other/repo", "0.3.4", "CHDesktop-0.3.4-full.nupkg")):
            with self.subTest(location=location), self.assertRaises(ValueError):
                feed.stable_version(REPO, feed.entry("A" * 40, location, 100))

    def test_published_receipt_uses_github_digests_and_rejects_mismatch(self):
        self.package(url=feed.feed_url(REPO))
        self.prepare("stable")
        assets = [{"name": p.name, "size": p.stat().st_size, "digest": "sha256:" + feed.digest(p, "sha256")}
                  for p in self.out.iterdir()]
        def cli(*args):
            return json.dumps({"assets": assets, "isDraft": False, "isPrerelease": False})
        with patch.object(feed, "command", side_effect=cli):
            self.assertEqual(feed.published_manifest(REPO, "0.3.4", self.out), (self.out / "RELEASES-stable").read_text())
            assets[0]["digest"] = "sha256:" + "0" * 64
            with self.assertRaises(ValueError):
                feed.published_manifest(REPO, "0.3.4", self.out)

    def test_git_feed_first_publish_retry_upgrade_rollback_and_race(self):
        bare = self.root / "remote.git"
        feed.command("git", "init", "-q", "--bare", str(bare))
        remote = bare.as_uri()
        def checkout(name):
            path = self.root / name
            path.mkdir()
            return path, feed.checkout_feed(remote, path)
        def manifest(version):
            name = f"CHDesktop-{version}-full.nupkg"
            return feed.entry("A" * 40, feed.package_url(REPO, version, name), 10)

        first, previous = checkout("first")
        self.assertIsNone(previous)
        feed.advance_feed(REPO, first, previous, manifest("0.3.4"))
        second, previous = checkout("second")
        rival, old = checkout("rival")
        self.assertEqual(previous, manifest("0.3.4"))
        head = feed.command("git", "rev-parse", "HEAD", cwd=second)
        feed.advance_feed(REPO, second, previous, previous)
        self.assertEqual(feed.command("git", "rev-parse", "HEAD", cwd=second), head)
        for invalid in (manifest("0.3.3"), previous.replace("A" * 40, "B" * 40)):
            with self.assertRaisesRegex(ValueError, "rewind"):
                feed.advance_feed(REPO, second, previous, invalid)
        feed.advance_feed(REPO, second, previous, manifest("0.3.5"))
        with self.assertRaises(subprocess.CalledProcessError):
            feed.advance_feed(REPO, rival, old, manifest("0.3.6"))
        _, actual = checkout("readback")
        self.assertEqual(actual, manifest("0.3.5"))
