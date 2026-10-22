"""Policy checks for the tag-triggered release workflow that feeds the updater."""
import unittest
from pathlib import Path
from urllib.parse import urlsplit

import yaml

ROOT = Path(__file__).resolve().parents[1]
SHA_PIN = "@" + "0123456789abcdef" * 2 + "01234567"  # shape of a 40-hex pin


class ReleaseWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.text = (ROOT / ".github/workflows/release.yml").read_text()
        self.workflow = yaml.load(self.text, Loader=yaml.BaseLoader)
        self.build = self.workflow["jobs"]["build"]
        self.publish = self.workflow["jobs"]["publish"]

    def step(self, job, name):
        return next(i for i, step in enumerate(job["steps"]) if step.get("name") == name)

    def test_only_tags_publish_and_pull_requests_dry_run(self):
        self.assertEqual(self.workflow["on"]["push"], {"tags": ["v*.*.*"]})
        self.assertEqual(self.workflow["on"]["pull_request"], {"paths": [
            ".github/workflows/release.yml", "desktop/assets/**", "desktop/forge.config.js",
            "desktop/src/installer_contract.js"]})
        self.assertEqual(set(self.workflow["on"]), {"push", "pull_request"})
        self.assertEqual(self.workflow["env"]["PUBLISH"], "${{ startsWith(github.ref, 'refs/tags/v') }}")
        # The variable only overrides the default dev channel; no setup is needed to release.
        self.assertEqual(self.workflow["env"]["RELEASE_CHANNEL"], "${{ vars.CAREER_HUB_RELEASE_CHANNEL || 'dev' }}")
        self.assertEqual(self.publish["if"],
                         "startsWith(github.ref, 'refs/tags/v') && !github.event.repository.private")
        self.assertEqual(self.publish["needs"], "build")

    def test_write_access_is_confined_to_the_publish_job(self):
        self.assertEqual(self.workflow["permissions"], {"checks": "read", "contents": "read"})
        self.assertEqual(self.build["permissions"], {"checks": "read", "contents": "read"})
        self.assertEqual(self.publish["permissions"], {"contents": "write"})
        # Only the tag-restricted environment hands out the write-capable token.
        self.assertEqual(self.publish["environment"], "release")
        self.assertNotIn("environment", self.build)
        # No personal token anywhere: the repository's own token publishes to itself.
        self.assertNotIn("secrets.", self.text)
        publish = self.publish["steps"][self.step(self.publish, "Publish the release and update feed")]
        self.assertEqual(publish["env"]["GH_TOKEN"], "${{ github.token }}")
        self.assertIn("--repo $env:GITHUB_REPOSITORY", publish["run"])

    def test_every_action_is_pinned_to_a_commit(self):
        for job in (self.build, self.publish):
            for step in job["steps"]:
                if "uses" in step:
                    action, _, ref = step["uses"].partition("@")
                    self.assertRegex(ref, r"^[0-9a-f]{40}$", action)

    def test_release_notes_link_back_to_the_source(self):
        publish = self.publish["steps"][self.step(self.publish, "Publish the release and update feed")]["run"]
        for rule in ["View source code", "$source/tree/$env:GITHUB_REF_NAME", r"\[#\d+\]",
                     "CHANGELOG.md has no"]:
            self.assertIn(rule, publish)

    def test_inputs_are_checked_before_building(self):
        check = self.build["steps"][self.step(self.build, "Check release inputs")]["run"]
        for rule in ['-ne "v$version"', "merge-base --is-ancestor", "CAREER_HUB_RELEASE_CHANNEL",
                     "check_name=Tests%20and%20public-tree%20gate", '-notcontains "success"']:
            self.assertIn(rule, check)
        self.assertLess(self.step(self.build, "Check release inputs"),
                        self.step(self.build, "Build the Squirrel package with the update feed"))
        build = self.build["steps"][self.step(self.build, "Build the Squirrel package with the update feed")]
        self.assertEqual(build["env"]["CAREER_HUB_UPDATE_FEED_URL"], "${{ env.UPDATE_FEED_URL }}")
        self.assertIn("prepare-package.cjs $env:RELEASE_CHANNEL", build["run"])

    def test_feed_url_meets_the_desktop_validator(self):
        check = self.build["steps"][self.step(self.build, "Check release inputs")]["run"]
        template = check.split('"UPDATE_FEED_URL=', 1)[1].split('"', 1)[0]
        url = urlsplit(template.replace("$env:GITHUB_REPOSITORY", "prplkane/career-hub"))
        # Mirrors validatedUpdateFeedUrl in desktop/src/build_metadata.js.
        self.assertEqual((url.scheme, url.netloc, url.query, url.fragment), ("https", "github.com", "", ""))
        self.assertEqual(url.path, "/prplkane/career-hub/releases/latest/download")

    def test_package_set_is_exact_and_handed_over_intact(self):
        collect = self.build["steps"][self.step(self.build, "Collect and hash release files")]["run"]
        for rule in ["RELEASES", "-full.nupkg", "Setup.exe", "-delta.nupkg", "SHA256SUMS.txt", '-join "`n"']:
            self.assertIn(rule, collect)
        upload = self.build["steps"][self.step(self.build, "Keep the release files for the publish job")]
        self.assertEqual(upload["if"], "env.PUBLISH == 'true'")
        self.assertEqual(upload["with"]["if-no-files-found"], "error")
        download = self.publish["steps"][self.step(self.publish, "Fetch the release files")]
        self.assertEqual(download["with"]["name"], upload["with"]["name"])


if __name__ == "__main__":
    unittest.main()
