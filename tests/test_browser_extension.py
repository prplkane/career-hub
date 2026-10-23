from __future__ import annotations

import base64
import hashlib
import json
import re
from pathlib import Path
import shutil
import struct
import subprocess
import unittest


PROJECT_ROOT = Path(__file__).resolve().parents[1]
EXTENSION_ROOT = PROJECT_ROOT / "extension"
DEV_EXTENSION_ID = "jhgaecfochimmafoopcpodncoeobecfg"

# These harnesses take about 50 ms locally (Node start-up ~25 ms, script
# ~17 ms). The first node.exe launch on a fresh hosted Windows runner has
# exceeded 10 seconds (qualification run 39583743627), and the first Node test
# here absorbs that cold start, so bound only a hang, generously.
NODE_TIMEOUT_SECONDS = 60


class BrowserExtensionTests(unittest.TestCase):
    def test_manifest_is_mv3_and_least_privilege(self) -> None:
        manifest = json.loads(
            (EXTENSION_ROOT / "manifest.json").read_text(encoding="utf-8")
        )

        self.assertEqual(manifest["manifest_version"], 3)
        self.assertEqual(manifest["version"], "0.10.0")
        self.assertEqual(manifest["minimum_chrome_version"], "116")
        public_key = base64.b64decode(manifest["key"], validate=True)
        digest = hashlib.sha256(public_key).digest()[:16]
        calculated_id = "".join(
            chr(ord("a") + nibble)
            for byte in digest
            for nibble in (byte >> 4, byte & 0x0F)
        )
        self.assertEqual(calculated_id, DEV_EXTENSION_ID)
        self.assertEqual(
            manifest["permissions"],
            ["activeTab", "scripting", "storage", "sidePanel", "clipboardWrite"],
        )
        self.assertEqual(
            manifest["host_permissions"],
            ["http://127.0.0.1/*"],
        )
        self.assertEqual(
            manifest["background"],
            {"service_worker": "service-worker.js"},
        )
        self.assertEqual(manifest["action"]["default_popup"], "popup.html")
        self.assertEqual(manifest["side_panel"], {"default_path": "sidepanel.html"})
        expected_icons = {
            "16": "icons/icon-16.png",
            "32": "icons/icon-32.png",
            "48": "icons/icon-48.png",
            "128": "icons/icon-128.png",
        }
        self.assertEqual(manifest["icons"], expected_icons)
        self.assertEqual(
            manifest["action"]["default_icon"],
            {key: expected_icons[key] for key in ("16", "32")},
        )
        self.assertTrue((EXTENSION_ROOT / "icons" / "icon-master.svg").is_file())
        for size, relative_path in expected_icons.items():
            icon = (EXTENSION_ROOT / relative_path).read_bytes()
            self.assertEqual(icon[:8], b"\x89PNG\r\n\x1a\n")
            self.assertEqual(
                struct.unpack(">II", icon[16:24]),
                (int(size), int(size)),
            )
        for prohibited in (
            "content_scripts",
            "content_security_policy",
            "externally_connectable",
            "optional_host_permissions",
            "optional_permissions",
        ):
            self.assertNotIn(prohibited, manifest)
        for permission in ("cookies", "tabs", "webRequest"):
            self.assertNotIn(permission, manifest["permissions"])
        self.assertTrue((EXTENSION_ROOT / "page-extractor.js").is_file())
        self.assertTrue((EXTENSION_ROOT / "browser-api.js").is_file())
        self.assertTrue(
            (EXTENSION_ROOT / "application-session-observer.js").is_file()
        )
        self.assertTrue((EXTENSION_ROOT / "application-form-helper.js").is_file())
        self.assertTrue((EXTENSION_ROOT / "application-answer-helper.js").is_file())

    def test_worker_owns_active_tab_and_uses_only_fixed_loopback_routes(self) -> None:
        worker = (EXTENSION_ROOT / "service-worker.js").read_text(encoding="utf-8")

        self.assertIn('const APP_ORIGIN = "http://127.0.0.1:8766";', worker)
        self.assertIn('const SESSION_PATH = "/api/session";', worker)
        self.assertIn('const CAPTURE_PATH = "/api/discoveries/captures";', worker)
        self.assertIn(
            'const APPLICATION_HELPER_SESSION_PATH = "/api/application-helper/session";',
            worker,
        )
        self.assertIn(
            'const APPLICATION_HELPER_FIELDS_PATH = "/api/application-helper/fields";',
            worker,
        )
        self.assertIn('const INSPECT_COMMAND = "career-hub.inspect-active-tab.v1";', worker)
        self.assertIn('const CAPTURE_COMMAND = "career-hub.capture-active-tab.v2";', worker)
        self.assertIn(
            'const OPEN_REVIEW_COMMAND = "career-hub.open-discovery-review.v1";',
            worker,
        )
        self.assertIn(
            'const PREPARE_APPLICATION_COMMAND = "career-hub.prepare-application.v1";',
            worker,
        )
        # The review target and one-shot reviews live in memory-backed
        # storage.session so they outlive the worker's idle termination.
        self.assertIn('DISCOVERY_REVIEW_STORAGE_PREFIX = "discovery_review_by_tab_v1:"', worker)
        self.assertIn("await saveDiscoveryReviewTarget(tab.id, result.review.discovery_id)", worker)
        self.assertIn('HELPER_REVIEW_STORAGE_PREFIX = "application_helper_review_v1:"', worker)
        self.assertIn('await saveHelperReview("prefill", {', worker)
        self.assertIn('await takeHelperReview("prefill", message.review_token)', worker)
        self.assertIn("value_digest: await reusableValueDigest(", worker)
        self.assertNotIn("new Map()", worker.replace("const submissionSignalByTab = new Map();", ""))
        self.assertIn('"helper-catalog.js"', worker)
        self.assertIn("const HELPER_FIELD_CATALOG = CareerHubHelperCatalog.catalog;", worker)
        self.assertIn("extensionApi.tabs.create({", worker)
        self.assertIn('const intentPath = intent === "prepare" ? "/prepare" : "";', worker)
        self.assertIn('url: `${APP_ORIGIN}/#job-feed/${encodeURIComponent(discoveryId)}${intentPath}`', worker)
        self.assertIn('"page-extractor.js"', worker)
        self.assertIn('"application-session-observer.js"', worker)
        self.assertIn('"application-form-helper.js"', worker)
        self.assertIn('"application-answer-helper.js"', worker)
        self.assertIn('new Set(["link", "selection", "page"])', worker)
        self.assertIn("extensionApi.tabs.query({ active: true, currentWindow: true })", worker)
        self.assertIn("extensionApi.tabs.get(targetTabId)", worker)
        self.assertIn('runtime.getURL("sidepanel.html")', worker)
        self.assertIn("extensionApi.scripting.executeScript", worker)
        self.assertIn('"browser-api.js"', worker)
        self.assertIn("const extensionApi = CareerHubBrowser.api;", worker)
        self.assertIn("validatedHelperProjection", worker)
        self.assertIn("validatedHelperFormInspection", worker)
        self.assertIn("runCareerHubApplicationForm", worker)
        self.assertIn('["inspect", enabledFieldIds, MAX_HELPER_FORM_CONTROLS]', worker)
        self.assertIn('["fill", instructions, MAX_HELPER_FORM_CONTROLS]', worker)
        self.assertIn("APPLICATION_HELPER_REVIEW_EXPIRED", worker)
        self.assertIn("HELPER_REVISION_PATTERN", worker)
        self.assertIn("target: { tabId, allFrames: false }", worker)
        self.assertIn("crypto.randomUUID()", worker)
        self.assertIn("capture_kind: mode", worker)
        self.assertIn('destination: "feed"', worker)
        self.assertIn('source_text: ""', worker)
        self.assertIn('strategy: "link"', worker)
        self.assertIn('credentials: "omit"', worker)
        self.assertIn('cache: "no-store"', worker)
        self.assertIn("AbortController", worker)
        self.assertIn("validReceipt", worker)
        self.assertIn("MAX_RECEIPT_FINDINGS = 3", worker)
        self.assertIn("candidate_fact", worker)
        self.assertNotIn("message.url", worker)
        self.assertNotIn("request.url", worker)
        self.assertNotIn("message.discovery_id", worker)
        self.assertNotIn("message.route", worker)
        self.assertIn("extensionApi.storage.session", worker)
        self.assertIn('APPLICATION_SESSION_STORAGE_KEY_PREFIX = "application_session_by_tab_v2:"', worker)
        self.assertIn('url: `${APP_ORIGIN}/#profile/application-helper`', worker)
        self.assertIn('"final_action_click"', worker)
        # ADR SEC-250 item 4: local storage holds only the pairing credential.
        self.assertIn('const EXTENSION_PAIR_PATH = "/api/extension/pair";', worker)
        self.assertIn('const PAIR_EXTENSION_COMMAND = "career-hub.extension.pair.v1";', worker)
        self.assertIn('"X-Career-Hub-Extension-Credential"', worker)
        local_calls = re.findall(r"extensionApi\.storage\.local\.(\w+)\(([^)]*)\)", worker)
        self.assertEqual({name for name, _ in local_calls}, {"get", "set", "remove"})
        for _, argument in local_calls:
            self.assertIn("CREDENTIAL_STORAGE_KEY", argument)
        self.assertIn("isPairExtensionCommand(message) && fromExtensionPage(sender)", worker)
        self.assertNotIn("extensionApi.storage.sync", worker)
        self.assertNotIn("localStorage", worker)
        self.assertNotIn("document.cookie", worker)
        self.assertNotIn("source_kind", worker)
        self.assertNotIn("raw_html", worker)
        self.assertNotIn("screenshot", worker)

    def test_popup_has_explicit_capture_modes_and_is_network_free(self) -> None:
        popup = (EXTENSION_ROOT / "popup.html").read_text(encoding="utf-8")
        client = (EXTENSION_ROOT / "popup.js").read_text(encoding="utf-8")
        styles = (EXTENSION_ROOT / "popup.css").read_text(encoding="utf-8")

        self.assertIn('id="page-button"', popup)
        self.assertIn('id="selection-button"', popup)
        self.assertIn('id="link-button"', popup)
        self.assertIn('id="retry-button"', popup)
        self.assertIn('id="review-button"', popup)
        self.assertIn('id="prepare-application-button"', popup)
        self.assertIn('id="arm-application-button"', popup)
        self.assertIn('id="open-applications-button"', popup)
        self.assertIn('id="helper-button"', popup)
        self.assertIn("Prepare application", popup)
        self.assertIn("Track submission", popup)
        self.assertIn("Review in Career Hub", popup)
        self.assertIn('id="assessment-receipt"', popup)
        self.assertIn('id="receipt-findings"', popup)
        self.assertIn('id="receipt-extra-step-count"', popup)
        self.assertIn('id="receipt-contact-candidate-count"', popup)
        self.assertIn('id="tab-title"', popup)
        self.assertIn('id="tab-host"', popup)
        self.assertIn('role="status"', popup)
        self.assertIn('aria-live="polite"', popup)
        self.assertIn("Save for later", popup)
        self.assertIn("Selection", popup)
        self.assertIn("Link only", popup)
        self.assertIn("Career Hub must be running", popup)
        self.assertIn('<script src="browser-api.js"></script>', popup)
        self.assertIn('<script src="popup.js"></script>', popup)
        self.assertIn('<link rel="stylesheet" href="popup.css">', popup)
        self.assertNotIn("<input", popup)
        self.assertNotIn("<select", popup)
        self.assertNotIn("<textarea", popup)
        self.assertNotIn("<script>", popup)
        self.assertNotIn("style=", popup)

        self.assertIn("extensionApi.tabs.query", client)
        self.assertIn("receipt.contact_candidate_count", client)
        self.assertIn("extensionApi.runtime.sendMessage", client)
        self.assertIn(
            'const CAPTURE_COMMAND = "career-hub.capture-active-tab.v2";',
            client,
        )
        self.assertIn(
            'const INSPECT_COMMAND = "career-hub.inspect-active-tab.v1";',
            client,
        )
        self.assertIn(
            'const OPEN_REVIEW_COMMAND = "career-hub.open-discovery-review.v1";',
            client,
        )
        self.assertIn(
            'const PREPARE_APPLICATION_COMMAND = "career-hub.prepare-application.v1";',
            client,
        )
        self.assertIn(
            'const ARM_APPLICATION_COMMAND = "career-hub.arm-application-session.v1";',
            client,
        )
        self.assertIn(
            'const OPEN_APPLICATIONS_COMMAND = "career-hub.open-applications.v1";',
            client,
        )
        self.assertIn(
            "CareerHubBrowser.openApplicationHelperSurface(activeWindowId, activeTabId)",
            client,
        )
        self.assertIn("BUNDLED_EXTENSION_ORIGIN", client)
        self.assertIn('id="extension-origin-panel"', popup)
        self.assertIn("CareerHubBrowser.extensionOrigin()", client)
        self.assertIn("command: CAPTURE_COMMAND", client)
        self.assertIn("command: OPEN_REVIEW_COMMAND", client)
        self.assertIn("command: PREPARE_APPLICATION_COMMAND", client)
        self.assertIn("mode,", client)
        self.assertIn("textContent", client)
        self.assertIn('successIcon.toggleAttribute("hidden"', client)
        self.assertIn('errorIcon.toggleAttribute("hidden"', client)
        self.assertIn("focus({ preventScroll: true })", client)
        self.assertIn("Extension isn’t allowed", client)
        self.assertIn("Pair this extension first", client)
        self.assertIn("Reload the unpacked extension", client)
        self.assertIn("-EnableExtension", client)
        self.assertIn("Career Hub needs a restart", client)
        self.assertIn("No application status was changed.", client)
        self.assertIn("extra application", client)
        self.assertIn("No vocabulary was added.", client)
        self.assertIn("No gaps were inferred from this partial capture.", client)
        self.assertNotIn("innerHTML", client)
        self.assertNotIn("fetch(", client)
        self.assertNotIn("localStorage", client)
        self.assertNotIn("chrome.storage", client)
        self.assertNotIn("observed_url", client)
        self.assertNotIn("page_title", client)
        self.assertNotIn("discovery_id", client)
        self.assertNotIn("#job-feed/", client)

        self.assertIn("--accent: #087f6b", styles)
        self.assertIn("prefers-reduced-motion", styles)
        self.assertNotIn("gradient", styles)

    def test_side_panel_is_attended_application_assistant_and_storage_free(self) -> None:
        panel = (EXTENSION_ROOT / "sidepanel.html").read_text(encoding="utf-8")
        client = (EXTENSION_ROOT / "sidepanel.js").read_text(encoding="utf-8")
        styles = (EXTENSION_ROOT / "sidepanel.css").read_text(encoding="utf-8")

        self.assertIn("Application assistant", panel)
        self.assertIn("Profile autofill", panel)
        self.assertIn("Resume shelf", panel)
        self.assertIn("Reusable answers", panel)
        self.assertIn('id="field-list"', panel)
        self.assertIn('id="refresh-button"', panel)
        self.assertIn('id="settings-button"', panel)
        self.assertIn('id="review-fields-button"', panel)
        self.assertIn('id="mapping-list"', panel)
        self.assertIn('id="fill-selected-button"', panel)
        self.assertIn('id="resume-list"', panel)
        self.assertIn('id="answer-list"', panel)
        self.assertIn('id="review-answers-button"', panel)
        self.assertIn('id="find-answers-button"', panel)
        self.assertIn('id="answer-mapping-list"', panel)
        self.assertIn('id="fill-answers-button"', panel)
        self.assertIn('id="answer-candidate-list"', panel)
        self.assertIn('id="pairing-section"', panel)
        self.assertIn('id="pairing-code-input"', panel)
        self.assertIn('autocomplete="off"', panel)
        self.assertIn('const PAIR_EXTENSION_COMMAND = "career-hub.extension.pair.v1";', client)
        self.assertIn('pairingSection.hidden = code !== "EXTENSION_PAIRING_REQUIRED"', client)
        for code in ("REQUIRED", "CODE_INVALID", "CODE_EXPIRED", "UNAVAILABLE"):
            self.assertIn(f'code === "EXTENSION_PAIRING_{code}"', client)
        self.assertNotIn("attempts left", client)
        self.assertIn('aria-live="polite"', panel)
        self.assertIn("The page is read only after a Review or Find action", panel)
        self.assertIn("Never clicks Continue, Next, or Submit", panel)
        self.assertIn("Exact-question matching only", panel)
        self.assertIn("No navigation or submission action is clicked for you", panel)
        self.assertIn('<script src="browser-api.js"></script>', panel)
        self.assertIn('<script src="helper-catalog.js"></script>', panel)
        self.assertIn('<script src="sidepanel.js"></script>', panel)
        self.assertIn("const PREFILL_FIELD_IDS = CareerHubHelperCatalog.prefillFieldIds;", client)
        self.assertIn("extensionApi.runtime.sendMessage", client)
        self.assertIn('<link rel="stylesheet" href="sidepanel.css">', panel)
        self.assertNotIn("Copy all", panel)
        self.assertNotIn("<script>", panel)
        self.assertNotIn("style=", panel)

        self.assertIn(
            'const LOAD_HELPER_FIELDS_COMMAND = "career-hub.application-helper.load.v1";',
            client,
        )
        self.assertIn(
            'const REVIEW_HELPER_FORM_COMMAND = "career-hub.application-helper.review-form.v1";',
            client,
        )
        self.assertIn(
            'const FILL_HELPER_FIELDS_COMMAND = "career-hub.application-helper.fill-reviewed.v1";',
            client,
        )
        self.assertIn(
            'const OPEN_HELPER_RESUME_COMMAND = "career-hub.application-helper.open-resume.v1";',
            client,
        )
        self.assertIn(
            'const REVIEW_SAVED_ANSWERS_COMMAND = "career-hub.application-helper.review-answers.v1";',
            client,
        )
        self.assertIn(
            'const FILL_SAVED_ANSWERS_COMMAND = "career-hub.application-helper.fill-reviewed-answers.v1";',
            client,
        )
        self.assertIn(
            'const FIND_REUSABLE_ANSWERS_COMMAND = "career-hub.application-helper.find-reusable-answers.v1";',
            client,
        )
        self.assertIn(
            'const SAVE_REUSABLE_ANSWER_COMMAND = "career-hub.application-helper.save-reviewed-answer.v1";',
            client,
        )
        self.assertIn(
            'const DELETE_REUSABLE_ANSWER_COMMAND = "career-hub.application-helper.delete-answer.v1";',
            client,
        )
        self.assertIn("navigator.clipboard.writeText", client)
        self.assertIn('window.addEventListener("pagehide", clearSensitiveState)', client)
        self.assertIn("Career Hub data changed", client)
        self.assertIn("Nothing was copied", client)
        self.assertIn("previewProfileValue", client)
        self.assertIn("answerValueText", client)
        self.assertIn("document.createElement", client)
        self.assertIn("textContent", client)
        self.assertNotIn("innerHTML", client)
        self.assertNotIn("fetch(", client)
        self.assertNotIn("chrome.storage", client)
        self.assertNotIn("localStorage", client)
        self.assertNotIn("sessionStorage", client)
        self.assertNotIn("chrome.scripting", client)
        self.assertNotIn("chrome.tabs.query", client)
        self.assertNotIn("copyAll", client)
        self.assertIn("prefers-reduced-motion", styles)
        self.assertNotIn("gradient", styles)

    def test_reusable_answer_helper_is_bounded_exact_match_and_never_submits(self) -> None:
        helper = (EXTENSION_ROOT / "application-answer-helper.js").read_text(
            encoding="utf-8"
        )

        self.assertIn("function runCareerHubReusableAnswers", helper)
        self.assertIn('document.querySelectorAll("input, select")', helper)
        self.assertIn("MAX_RAW_CONTROLS = 2000", helper)
        self.assertIn("MAX_CANDIDATES = 30", helper)
        self.assertIn("normalizedQuestion", helper)
        self.assertIn("entry.question.canonical ===", helper)
        self.assertIn("strongestOwner", helper)
        self.assertIn('mode === "inspect_saved"', helper)
        self.assertIn('mode === "find_completed"', helper)
        self.assertIn('mode === "read_completed"', helper)
        self.assertIn('mode === "fill_saved"', helper)
        self.assertIn('new Event("input", { bubbles: true })', helper)
        self.assertIn('new Event("change", { bubbles: true })', helper)
        self.assertIn("forbiddenQuestion", helper)
        self.assertNotIn(".click(", helper)
        self.assertNotIn("requestSubmit", helper)
        self.assertNotIn("submit()", helper)
        self.assertNotIn("chrome.storage", helper)
        self.assertNotIn("fetch(", helper)

    def test_form_helper_is_main_frame_empty_control_only_and_never_submits(self) -> None:
        helper = (EXTENSION_ROOT / "application-form-helper.js").read_text(
            encoding="utf-8"
        )

        self.assertIn("function runCareerHubApplicationForm", helper)
        self.assertNotIn("function inspectCareerHubApplicationForm", helper)
        self.assertNotIn("function fillCareerHubApplicationForm", helper)
        self.assertIn('document.querySelectorAll("input, select, textarea")', helper)
        self.assertIn('descriptor.type === "radio"', helper)
        self.assertIn('descriptor.type === "select"', helper)
        self.assertIn("canRepresentKind", helper)
        self.assertIn("rawLimit", helper)
        self.assertIn("form_key", helper)
        self.assertIn("candidatesByField", helper)
        self.assertIn("autocompleteToken", helper)
        self.assertIn("textWithoutControls", helper)
        self.assertIn('clone.querySelectorAll("input, select, textarea, button, script, style")', helper)
        self.assertIn("getClientRects", helper)
        self.assertIn("String(control.element.value", helper)
        self.assertIn('new Event("input", { bubbles: true })', helper)
        self.assertIn('new Event("change", { bubbles: true })', helper)
        self.assertIn("race|ethnicity|gender|sex|pronoun|disability|veteran", helper)
        self.assertNotIn("combobox", helper)
        self.assertNotIn(".click(", helper)
        self.assertNotIn("requestSubmit", helper)
        self.assertNotIn("submit()", helper)
        self.assertNotIn("chrome.storage", helper)
        self.assertNotIn("fetch(", helper)

    def test_application_observer_accepts_only_bounded_final_actions(self) -> None:
        observer = (EXTENSION_ROOT / "application-session-observer.js").read_text(
            encoding="utf-8"
        )

        self.assertIn("FINAL_ACTION_PATTERN", observer)
        self.assertIn('recordSubmitCandidate("native_submit")', observer)
        self.assertIn('recordSubmitCandidate("final_action_click")', observer)
        self.assertIn('document.addEventListener("click", onClick, true)', observer)
        self.assertIn("characterData: true", observer)
        self.assertNotIn("form value", observer.lower())

    def test_extension_javascript_has_valid_syntax_when_node_is_available(self) -> None:
        node = shutil.which("node")
        if node is None:
            self.skipTest("Node.js is unavailable for JavaScript syntax checks")
        for script_name in (
            "page-extractor.js",
            "application-session-observer.js",
            "browser-api.js",
            "helper-catalog.js",
            "application-form-helper.js",
            "application-answer-helper.js",
            "service-worker.js",
            "popup.js",
            "sidepanel.js",
        ):
            with self.subTest(script_name=script_name):
                completed = subprocess.run(
                    [node, "--check", str(EXTENSION_ROOT / script_name)],
                    cwd=PROJECT_ROOT,
                    capture_output=True,
                    text=True,
                    encoding="utf-8",
                    errors="replace",
                    timeout=NODE_TIMEOUT_SECONDS,
                    check=False,
                )
                self.assertEqual(
                    completed.returncode,
                    0,
                    completed.stdout + completed.stderr,
                )

    def test_worker_protocol_harness_rejects_arbitrary_urls_and_captures_fixed_contract(self) -> None:
        node = shutil.which("node")
        if node is None:
            self.skipTest("Node.js is unavailable for the worker protocol harness")
        completed = subprocess.run(
            [node, str(PROJECT_ROOT / "tests" / "browser_extension_worker_harness.cjs")],
            cwd=PROJECT_ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=NODE_TIMEOUT_SECONDS,
            check=False,
        )
        self.assertEqual(
            completed.returncode,
            0,
            completed.stdout + completed.stderr,
        )
        self.assertIn("worker protocol harness: OK", completed.stdout)

    def test_browser_compatibility_harness_uses_each_supported_helper_surface(self) -> None:
        node = shutil.which("node")
        if node is None:
            self.skipTest("Node.js is unavailable for the compatibility harness")
        completed = subprocess.run(
            [node, str(PROJECT_ROOT / "tests" / "browser_extension_compatibility.cjs")],
            cwd=PROJECT_ROOT,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=NODE_TIMEOUT_SECONDS,
            check=False,
        )
        self.assertEqual(
            completed.returncode,
            0,
            completed.stdout + completed.stderr,
        )
        self.assertIn("browser extension compatibility harness: OK", completed.stdout)

    def test_developer_guide_keeps_install_and_origin_setup_explicit(self) -> None:
        guide = (PROJECT_ROOT / "docs" / "guides" / "extension_development.md").read_text(
            encoding="utf-8"
        )
        normalized = " ".join(guide.split())

        self.assertIn("Load unpacked", guide)
        self.assertIn("chrome://extensions", guide)
        self.assertIn("-ExtensionOrigin", guide)
        self.assertIn("-EnableExtension", guide)
        self.assertIn(DEV_EXTENSION_ID, guide)
        self.assertIn("chrome-extension://", guide)
        self.assertIn("Prepare application", guide)
        self.assertIn("Save for later", guide)
        self.assertIn("Selection", guide)
        self.assertIn("Link only", guide)
        self.assertIn("Track submission", guide)

        compatibility = (
            PROJECT_ROOT / "docs" / "guides" / "browser_extension_compatibility.md"
        ).read_text(encoding="utf-8")
        for browser in ("Chrome", "Edge", "Firefox", "Safari"):
            self.assertIn(browser, compatibility)
        self.assertIn("scripts.build_browser_extension", compatibility)
        self.assertIn("moz-extension://", compatibility)
        self.assertIn("safari-web-extension://", compatibility)
        self.assertIn("xcrun safari-web-extension-packager", compatibility)
        self.assertIn("Needs JD", guide)
        self.assertIn("activeTab", guide)
        self.assertIn("bounded visible-page text", normalized)
        self.assertIn("selected text", normalized)
        self.assertIn("never persists a capability token", normalized)
        self.assertIn("never writes to disk", normalized)
        self.assertIn("`storage.session`", guide)
        self.assertIn("Mark this application as applied?", guide)
        self.assertIn("`scripting`", guide)
        self.assertIn("`sidePanel`", guide)
        self.assertIn("`clipboardWrite`", guide)
        self.assertIn("There is no Copy all", guide)
        self.assertIn(
            "Profile/answer Copy and Resume opening do not invoke `scripting`",
            guide,
        )
        self.assertIn(
            "Profile Review/Fill and reusable-answer Find/Review", guide
        )
        self.assertIn("always with `allFrames: false`", guide)
        self.assertIn("lazy answer bank lives only in the private Data Home", guide)
        self.assertIn("active Resume Version ids", guide)
        self.assertIn("no persistent site access", normalized.lower())


if __name__ == "__main__":
    unittest.main()
