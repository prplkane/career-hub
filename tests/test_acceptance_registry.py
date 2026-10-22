from __future__ import annotations

import json
import re
import unittest
from pathlib import Path


class AcceptanceRegistryTests(unittest.TestCase):
    def setUp(self) -> None:
        self.project_root = Path(__file__).resolve().parents[1]
        self.registry = (self.project_root / "docs" / "process" / "acceptance_registry.md").read_text(
            encoding="utf-8"
        )

    def test_registry_covers_current_and_future_resume_capabilities(self) -> None:
        for capability in (
            "CAP-JD-INTAKE",
            "CAP-JD-ASSESSMENT",
            "CAP-APPLICATION-LEDGER",
            "CAP-RESUME-PLAN",
            "CAP-RESUME-REUSE",
            "CAP-RESUME-COMPOSE",
            "CAP-RESUME-RENDER",
            "CAP-COVER-LETTER-COMPOSE",
            "CAP-JD-INTAKE-HARDENING",
        ):
            self.assertIn(capability, self.registry)

        normalized_registry = " ".join(self.registry.split())
        self.assertIn("former intake-hardening Phase 6 label", normalized_registry)
        self.assertIn("the same roadmap stage as the product-spec composer", normalized_registry)
        for capability in (
            "CAP-RESUME-COMPOSE",
            "CAP-RESUME-RENDER",
            "CAP-COVER-LETTER-COMPOSE",
        ):
            row = next(
                line
                for line in self.registry.splitlines()
                if line.startswith(f"| `{capability}`")
            )
            self.assertIn("| Implemented |", row)

    def test_scope_documents_identify_the_registry_as_authoritative(self) -> None:
        readme = (self.project_root / "README.md").read_text(encoding="utf-8")
        product_spec = (self.project_root / "docs" / "product" / "product_spec.md").read_text(
            encoding="utf-8"
        )
        phase_plan = (self.project_root / "docs" / "history" / "phase_plan.md").read_text(
            encoding="utf-8"
        )
        intake_hardening = (self.project_root / "docs" / "history" / "intake_hardening.md").read_text(
            encoding="utf-8"
        )

        for document in (readme, product_spec, phase_plan):
            self.assertIn("Capability Acceptance Registry", document)
        self.assertIn("CAP-JD-INTAKE-HARDENING", intake_hardening)
        self.assertNotIn("Phase 6 verification contract", intake_hardening)

    def test_desktop_capabilities_distinguish_foundation_from_delivery(self) -> None:
        expected_statuses = {
            "CAP-DESKTOP-FOUNDATION": "Foundation",
            "CAP-DESKTOP-DIAGNOSTICS": "Planned",
            "CAP-DESKTOP-DISTRIBUTION": "Planned",
        }
        for capability, status in expected_statuses.items():
            rows = [
                line
                for line in self.registry.splitlines()
                if line.startswith(f"| `{capability}`")
            ]
            self.assertEqual(len(rows), 1)
            self.assertIn(f"| {status} |", rows[0])

        foundation = next(
            line
            for line in self.registry.splitlines()
            if line.startswith("| `CAP-DESKTOP-FOUNDATION`")
        )
        self.assertIn("parent-pipe closure", foundation)
        self.assertIn("locked Electron 44 host/fuses", foundation)
        self.assertIn("Go `onedir` sidecar and native agent client", foundation)
        self.assertIn("isolated installed lifecycle/restore", foundation)
        self.assertIn("macOS arm64 native build", foundation)
        self.assertIn("unsigned Windows development installers are not a production release", foundation)
        self.assertIn("minimal sidecar health endpoint is foundation only", self.registry)

        roadmap = (
            self.project_root / "docs" / "product" / "desktop_delivery_roadmap.md"
        ).read_text(encoding="utf-8")
        self.assertIn("Historical ticket ids `TAURI-300` through `TAURI-350` are superseded", roadmap)
        self.assertNotRegex(roadmap, r"^### `TAURI-\d{3}`", msg="Active Tauri tickets must stay superseded")
        for ticket in ("ELEC-300", "ELEC-310", "ELEC-320", "ELEC-330", "ELEC-340", "ELEC-350"):
            self.assertEqual(roadmap.count(f"### `{ticket}`"), 1)
        self.assertIn("Current result: **not exited**", roadmap)
        self.assertIn("desktop_release_gates.md", roadmap)

        architecture = (self.project_root / "docs" / "reference" / "architecture.md").read_text(
            encoding="utf-8"
        )
        self.assertIn("selected desktop wrapper is Electron", architecture)
        self.assertIn("sidecar_protocol.md", architecture)

    def test_vault_expansions_have_named_capability_boundaries(self) -> None:
        for capability in (
            "CAP-VAULT-SKILL-EDITOR",
            "CAP-COMPETENCY-REFERENCES",
            "CAP-CANDIDATE-EVIDENCE-GRAPH",
            "CAP-EVIDENCE-ANALYTICS",
            "CAP-JD-DEMAND-INDEX",
            "CAP-COVERAGE-MATRIX",
            "CAP-DEMAND-GAPS-UI",
            "CAP-COMPETENCY-EXPLORER",
            "CAP-DOCUMENT-LEDGER",
        ):
            row = next(
                line
                for line in self.registry.splitlines()
                if line.startswith(f"| `{capability}`")
            )
            self.assertIn("| Implemented |", row)

        role_row = next(line for line in self.registry.splitlines() if line.startswith("| `CAP-ROLE-CATALOG`"))
        self.assertIn("| Planned |", role_row)
        obsidian_row = next(line for line in self.registry.splitlines() if line.startswith("| `CAP-OBSIDIAN-EXPORT`"))
        self.assertIn("| Planned |", obsidian_row)

        normalized_registry = " ".join(self.registry.split())
        self.assertIn("A role never implies candidate skills", normalized_registry)
        self.assertIn("Windows DPAPI binds recovery", normalized_registry)
        self.assertIn(
            "checksum-verifies every stored recovery-ZIP payload before archive installation",
            normalized_registry,
        )

    def test_future_agent_repository_and_watcher_work_preserves_attended_boundaries(self) -> None:
        expected_statuses = {
            "CAP-AGENT-WORKFLOW-RUNS": "Foundation",
            "CAP-REPOSITORY-IMPACT-INTAKE": "Planned",
            "CAP-JOB-WATCHING": "Planned",
        }
        for capability, status in expected_statuses.items():
            row = next(
                line
                for line in self.registry.splitlines()
                if line.startswith(f"| `{capability}`")
            )
            self.assertIn(f"| {status} |", row)

        normalized_registry = " ".join(self.registry.split())
        for boundary in (
            "Repository activity is evidence to review, not proof of ownership, scope, or business impact",
            "future adapters may not bypass authentication, CAPTCHA, access controls, or provider restrictions",
            "do not weaken the no-model, no-scraper, attended-helper, or no-submit boundaries",
            "unattended browser filling, submission, and batch application runs are not accepted product scope",
            "Career Hub is a longitudinal career record, not a disposable application campaign",
            "model reasoning may rank or draft, but it does not replace the source of truth",
        ):
            self.assertIn(boundary, normalized_registry)

        self.assertNotIn("CAP-AUTONOMOUS-APPLICATION-RUNS", self.registry)
        product_backlog = (
            self.project_root / "docs" / "product" / "product_backlog.md"
        ).read_text(encoding="utf-8")
        normalized_backlog = " ".join(product_backlog.split())
        self.assertIn(
            "Unattended browser filling, submission, or batch applying",
            normalized_backlog,
        )
        self.assertIn(
            "The product remains AI-provider-neutral; this ticket does not add an AI API.",
            normalized_backlog,
        )

        competency_contract = (
            self.project_root / "docs" / "reference" / "competency_graph_contract.md"
        ).read_text(encoding="utf-8")
        traceability = (
            self.project_root / "docs" / "history" / "competency_graph_traceability.md"
        ).read_text(encoding="utf-8")
        self.assertIn("CAP-REPOSITORY-IMPACT-INTAKE", competency_contract)
        self.assertIn("CAP-REPOSITORY-IMPACT-INTAKE", traceability)

    def test_competency_graph_task_ids_have_one_versioned_definition(self) -> None:
        traceability = (
            self.project_root / "docs" / "history" / "competency_graph_traceability.md"
        ).read_text(encoding="utf-8")
        expected_ids = (
            "CG-000",
            "CG-001",
            "CG-101",
            "CG-102",
            "CG-103",
            "CG-201",
            "CG-202",
            "CG-203",
            "CG-204",
            "CG-301",
            "CG-302",
            "CG-401",
            "CG-402",
            "CG-501",
            "CG-502",
            "CG-601",
            "CG-900",
        )
        headings = re.findall(r"^## (CG-\d{3}) — ", traceability, flags=re.MULTILINE)

        self.assertEqual(tuple(headings), expected_ids)
        self.assertIn("Traceability schema: **1**", traceability)
        self.assertIn("Roadmap version: **CG-1.0**", traceability)
        self.assertIn("Independent verdict: **PASS WITH GAPS**", traceability)
        smoke = self.project_root / "tests" / "competency_surfaces_smoke.cjs"
        self.assertTrue(smoke.is_file())
        remediation = (
            self.project_root / "docs" / "history" / "cg900_remediation_2026-08-23.md"
        ).read_text(encoding="utf-8")
        self.assertIn(
            "717 tests run; OK with 3 expected platform skips", remediation
        )
        self.assertIn("29 passed, 3 expected platform skips", remediation)
        self.assertIn("npm run smoke:competency", remediation)
        package = json.loads(
            (self.project_root / "package.json").read_text(encoding="utf-8")
        )
        self.assertTrue(package["private"])
        self.assertEqual(
            package["scripts"]["smoke:competency"],
            "node tests/competency_surfaces_smoke.cjs",
        )
        self.assertIn("playwright", package["devDependencies"])
        workflow = (
            self.project_root / ".github" / "workflows" / "qualification.yml"
        ).read_text(encoding="utf-8")
        self.assertIn("npm ci --ignore-scripts", workflow)
        self.assertIn("npm run smoke:competency", workflow)
        self.assertIn(
            "[Competency Graph Task Traceability](../history/competency_graph_traceability.md)",
            self.registry,
        )

    def test_discovery_capture_capabilities_have_named_acceptance_boundaries(self) -> None:
        expected_statuses = {
            "CAP-JOB-DISCOVERY-INBOX": "Implemented",
            "CAP-JOB-FEED-UI": "Implemented",
            "CAP-EXTENSION": "Implemented",
            "CAP-DISCOVERY-FIELD-CORRECTIONS": "Implemented",
            "CAP-APPLICATION-SUBMISSION-TRACKING": "Implemented",
            "CAP-HARD-BLOCKER-POLICY": "Implemented",
            "CAP-APPLICATION-INSTRUCTIONS": "Implemented",
            "CAP-JOB-CONTACTS": "Implemented",
            "CAP-APPLICATION-HELPER": "Implemented",
            "CAP-EMAIL-ALERT-INGEST": "Implemented",
            "CAP-JOB-LINK-RESOLUTION": "Implemented",
            "CAP-JOB-WATCHING": "Planned",
        }
        for capability, status in expected_statuses.items():
            row = next(
                line
                for line in self.registry.splitlines()
                if line.startswith(f"| `{capability}`")
            )
            self.assertIn(f"| {status} |", row)

        normalized_registry = " ".join(self.registry.split())
        self.assertIn(
            "Discoveries do not enter the application ledger, Demand & coverage, or saved-job analytics before promotion",
            normalized_registry,
        )
        self.assertIn("No persistent site access", normalized_registry)
        self.assertIn("main-frame bounded extraction", normalized_registry)
        self.assertIn("exact Discovery deep links", normalized_registry)
        self.assertIn("no-write **Review later**", normalized_registry)
        self.assertIn("explicit complete-JD verification", normalized_registry)
        self.assertIn(
            "exactly one startable saved application matches the current canonical posting URL",
            normalized_registry,
        )
        self.assertIn("never requires or force-populates a contact", normalized_registry)
        self.assertIn("submission-detected event", normalized_registry)
        self.assertIn("capture never implies Applied", normalized_registry)
        self.assertIn("one-shot memory-only review token", normalized_registry)
        self.assertIn("No Copy all", normalized_registry)
        self.assertIn("exact-sender header-first filtering", normalized_registry)
        self.assertIn("no attachment ingestion", normalized_registry)
        self.assertIn("No login automation", normalized_registry)
        self.assertIn("No scraper or watcher is present", normalized_registry)

        plan = (
            self.project_root / "docs" / "product" / "job_discovery_capture_plan.md"
        ).read_text(encoding="utf-8")
        setup = (
            self.project_root / "docs" / "guides" / "email_alert_setup.md"
        ).read_text(encoding="utf-8")
        mail_task = (
            self.project_root / "docs" / "history" / "mail_100_eml_parser_task.md"
        ).read_text(encoding="utf-8")
        normalized_docs = " ".join((plan + setup + mail_task).split())
        self.assertIn("Recommended by source", normalized_docs)
        self.assertIn("Feed · Email · Watchers", normalized_docs)
        self.assertIn("Extension and public resolver are enrichment provenance", normalized_docs)
        self.assertIn("Location, on-site, hybrid, and relocation", normalized_docs)
        self.assertIn("never hard blockers", normalized_docs)
        self.assertIn("Whole-inbox forwarding is disabled", normalized_docs)
        self.assertIn("MAIL-100", normalized_docs)
        self.assertIn("guarded local resolver route", normalized_docs)
        self.assertIn("not offered as a Job Feed control", normalized_docs)
        self.assertIn(
            "Public link resolution subsequently completed through `LINK-130`",
            normalized_docs,
        )
        self.assertNotIn(
            "Public link resolver: **Coming soon**",
            normalized_docs,
        )
        self.assertNotIn(
            "public link resolution and Watchers remain separate planned capabilities",
            normalized_docs,
        )

    def test_product_backlog_has_named_capabilities_and_boundaries(self) -> None:
        expected_statuses = {
            "CAP-MATERIAL-INTEGRITY": "Foundation",
            "CAP-AGENT-WORKFLOW-RUNS": "Foundation",
            "CAP-APPLICATION-NARRATIVE-BRIEF": "Planned",
            "CAP-JOB-SOURCE-TRUST": "Foundation",
            "CAP-JOB-WATCHING": "Planned",
            "CAP-RESUME-REUSE": "Planned",
            "CAP-INTERVIEW-PREP": "Planned",
            "CAP-OUTCOME-INSIGHTS": "Foundation",
        }
        for capability, status in expected_statuses.items():
            rows = [
                line
                for line in self.registry.splitlines()
                if line.startswith(f"| `{capability}`")
            ]
            self.assertEqual(len(rows), 1)
            self.assertIn(f"| {status} |", rows[0])

        backlog = (
            self.project_root / "docs" / "product" / "product_backlog.md"
        ).read_text(encoding="utf-8")
        readme = (self.project_root / "README.md").read_text(encoding="utf-8")
        for capability in expected_statuses:
            self.assertIn(capability, backlog)

        for ticket in (
            "PB-MAT-100",
            "PB-AGT-100",
            "PB-SRC-100",
            "PB-WATCH-100",
            "PB-WATCH-110",
            "PB-WATCH-120",
            "PB-NAR-100",
            "PB-RES-100",
            "PB-INT-100",
            "PB-OUT-100",
        ):
            self.assertEqual(backlog.count(f"### `{ticket}`"), 1)

        self.assertIn("Greenhouse Job Board API", backlog)
        self.assertIn("Ashby Public Job Postings API", backlog)
        self.assertIn("No combined mega-skill", backlog)
        self.assertIn("[Product Backlog](docs/product/product_backlog.md)", readme)
        self.assertIn("[Product Backlog](../product/product_backlog.md)", self.registry)

    def test_cv_first_resume_management_contract_reflects_current_and_planned_slices(self) -> None:
        paths = {
            "readme": self.project_root / "README.md",
            "start_here": self.project_root / "START_HERE.md",
            "mvp": self.project_root / "docs" / "guides" / "mvp_workflow.md",
            "vault_intake": self.project_root / "docs" / "guides" / "vault_intake.md",
            "application_history": self.project_root
            / "docs"
            / "reference"
            / "application_history.md",
        }
        documents = {
            name: path.read_text(encoding="utf-8") for name, path in paths.items()
        }
        normalized_registry = " ".join(self.registry.split())
        normalized_documents = {
            name: " ".join(document.split())
            for name, document in documents.items()
        }

        for phrase in (
            "**Implemented resume-entry slices.**",
            "Career Vault becomes canonical only after the user explicitly applies an approved intake review.",
            "**Add resume** offers exactly two intents: **Update Career Vault** and **Save for applications**.",
            "Assessment is not a third Add resume intent.",
            "**Keep for applications** may create one immutable Resume Version",
            "generated resume may be imported again",
            "Duplicate suggestions are advisory and non-blocking.",
            "**Resume Library** means reusable application materials only.",
            "**Implemented lifecycle slices.**",
        ):
            self.assertIn(phrase, normalized_registry)

        vault_row = next(
            line
            for line in self.registry.splitlines()
            if line.startswith("| `CAP-VAULT`")
        )
        ledger_row = next(
            line
            for line in self.registry.splitlines()
            if line.startswith("| `CAP-APPLICATION-LEDGER`")
        )
        self.assertIn("| Implemented |", vault_row)
        self.assertIn("contextual review recovery", vault_row)
        self.assertIn("approved-import file cleanup", vault_row)
        self.assertIn("| Implemented |", ledger_row)
        self.assertIn("standalone Save for applications", ledger_row)
        self.assertIn("Resume Library management", ledger_row)

        for document in normalized_documents.values():
            self.assertIn("Capability Acceptance Registry", document)

        self.assertIn(
            "Processed imports are workflow artifacts, not application Resume Library items",
            normalized_documents["readme"],
        )
        self.assertIn(
            "bounded list of unfinished imports as contextual continuations",
            normalized_documents["mvp"],
        )
        self.assertIn(
            "**Resume Library** means durable application materials only",
            normalized_documents["application_history"],
        )
        self.assertIn(
            "user may explicitly remove the original input file",
            normalized_documents["vault_intake"],
        )
        for document in normalized_documents.values():
            self.assertIn("Save for applications", document)
        self.assertIn(
            "Add resume** offers exactly two implemented intents",
            normalized_documents["start_here"],
        )

    def test_runtime_docs_match_current_storage_and_editor_contracts(self) -> None:
        application_history = (
            self.project_root / "docs" / "reference" / "application_history.md"
        ).read_text(encoding="utf-8")
        system_pipeline = (self.project_root / "docs" / "reference" / "system_pipeline.md").read_text(
            encoding="utf-8"
        )
        mvp_workflow = (self.project_root / "docs" / "guides" / "mvp_workflow.md").read_text(
            encoding="utf-8"
        )
        manual_testing = (self.project_root / "docs" / "guides" / "manual_testing.md").read_text(
            encoding="utf-8"
        )
        launch_config = (self.project_root / ".vscode" / "launch.json").read_text(
            encoding="utf-8"
        )

        self.assertIn("Schema version 6 adds append-only Applied-resume events", application_history)
        self.assertIn("schema version 5 adds", system_pipeline)
        self.assertIn("version 6 adds append-only Applied-resume history", system_pipeline)
        self.assertIn("archive, and restore records", mvp_workflow)
        self.assertNotIn("archive, and delete records", mvp_workflow)
        self.assertIn("Career Hub: Native Desktop", manual_testing)
        self.assertIn("Career Hub: Native Desktop", launch_config)
        self.assertNotIn("Phase 1: Browser UI", manual_testing)


if __name__ == "__main__":
    unittest.main()
