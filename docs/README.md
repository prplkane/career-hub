# Documentation Index

Documents are grouped by what the reader needs:

| Folder | Holds |
| --- | --- |
| `guides/` | How to run, use, test and develop the app |
| `reference/` | How the system is built: architecture, contracts, formats, layout |
| `process/` | How we work: rules for people and agents, testing policy, ledger |
| `product/` | What we are building: specification, backlog, roadmaps, plans |
| `history/` | Finished plans, reviews, evidence and the Go migration records |
| `adr/` | Architecture decision records |

Every document is listed below with its status:

- **Current:** describes the shipped Go sidecar, Electron desktop, extension,
  or agent client. Trust it; fix it when it is wrong.
- **Process:** how engineering work is done in this repository.
- **Needs rewrite:** still useful for intent, but its commands or paths are
  from the retired Python runtime. A banner at the top names the current
  source.
- **Historical:** completed plans, evidence, prompts, and migration records.
  Kept for traceability. Do not follow their commands.

Product status comes from two documents only: the
[Capability Acceptance Registry](process/acceptance_registry.md) (what is implemented)
and the [Product Backlog](product/product_backlog.md) (what is approved next).
Engineering tasks live in `.backlog/` (Engineering Work Ledger).

## Start here

| If you are… | Read |
| --- | --- |
| Using the app | [START_HERE](../START_HERE.md), then [README](../README.md) |
| Contributing a change | [CONTRIBUTING](../CONTRIBUTING.md), [CHANGELOG](../CHANGELOG.md) |
| Setting up a development machine | [Local development](guides/local_development.md), [Native packaging](../packaging/README.md) |
| Changing backend code | [Architecture](reference/architecture.md), [Backend README](../backend/README.md), [Sidecar protocol](reference/sidecar_protocol.md) |
| Orienting in the repository | [Repository layout](reference/repository_layout.md), [Architecture atlas](reference/architecture_atlas.md) |

## Current reference

### Architecture and runtime

| Document | Purpose |
| --- | --- |
| [architecture.md](reference/architecture.md) | Runtime boundaries: Electron host, Go sidecar, Data Home, extension, agent client |
| [architecture_atlas.md](reference/architecture_atlas.md) | Diagrams of runtime, data, and request flows |
| [repository_layout.md](reference/repository_layout.md) | What each folder holds and where private data lives |
| [sidecar_protocol.md](reference/sidecar_protocol.md) | Desktop-to-sidecar startup and readiness protocol |
| [technology_selection.md](reference/technology_selection.md) | Why each technology was chosen, including retired options |
| [schema_reference.md](reference/schema_reference.md) | YAML and record schemas |
| [calendar_dates.md](reference/calendar_dates.md) | Date and month-precision rules |

### Data and privacy

| Document | Purpose |
| --- | --- |
| [private_data.md](reference/private_data.md) | What is private and where it is stored |
| [data_home.md](reference/data_home.md) | Data Home contract and recovery (**needs rewrite**: commands are retired) |

### Product workflows

| Document | Purpose |
| --- | --- |
| [agent_workflows.md](guides/agent_workflows.md) | `career-hub-agent` commands and repository skills |
| [application_materials.md](reference/application_materials.md) | Resume and cover-letter material editing and export |
| [application_text_workbench.md](guides/application_text_workbench.md) | Application question drafts |
| [application_session_prompt.md](guides/application_session_prompt.md) | Prompt for an agent-assisted application session |
| [application_packages.md](reference/application_packages.md) | Legacy review contract for application packages |
| [base_resume_versions.md](guides/base_resume_versions.md) | Base resume versions |
| [job_review.md](guides/job_review.md) | Deterministic job review rules |
| [competency_graph_contract.md](reference/competency_graph_contract.md) | Competency Graph contract |
| [email_alert_setup.md](guides/email_alert_setup.md) | Gmail connection and email alert setup |
| [resume_standards.md](reference/resume_standards.md) | Resume writing standards |
| [resume_template_strategy.md](reference/resume_template_strategy.md) | Resume template approach |
| [small_model_playbook.md](process/small_model_playbook.md) | Exact-vocabulary rules for job-description work |

### Desktop, extension, and design

| Document | Purpose |
| --- | --- |
| [local_development.md](guides/local_development.md) | Build and run the desktop from source |
| [desktop_release_gates.md](reference/desktop_release_gates.md) | Release gates (**needs rewrite**: written for the Python sidecar) |
| [extension_development.md](guides/extension_development.md) | Browser extension development |
| [browser_extension_compatibility.md](guides/browser_extension_compatibility.md) | Chromium, Firefox, and Safari compatibility |

## Process

| Document | Purpose |
| --- | --- |
| [engineering_rules.md](process/engineering_rules.md) | Code rules for Go, JavaScript, and tooling |
| [workflow_rules.md](process/workflow_rules.md) | Task workflow |
| [review_checklist.md](process/review_checklist.md) | Review checklist |
| [testing_strategy.md](process/testing_strategy.md) | Testing policy: which checks to run and when |
| [manual_testing.md](guides/manual_testing.md) | Manual test scenarios (**needs rewrite**: many commands are retired) |

## Needs rewrite

These still describe intent correctly but teach retired `python -m src.*`
commands. Each has a banner naming the current source. Rewriting them is
tracked as CA-DOC-4 in the assessment.

| Document | Current source until rewritten |
| --- | --- |
| [manual_testing.md](guides/manual_testing.md) | Desktop workflows in [START_HERE](../START_HERE.md); checks in the [README](../README.md#verification) |
| [data_home.md](reference/data_home.md) | Settings in the desktop; [private_data.md](reference/private_data.md) |
| [system_pipeline.md](reference/system_pipeline.md) | [architecture.md](reference/architecture.md), [architecture_atlas.md](reference/architecture_atlas.md) |
| [job_intake.md](guides/job_intake.md) | Jobs workflow in [START_HERE](../START_HERE.md) |
| [vault_intake.md](guides/vault_intake.md) | Resume entry in [START_HERE](../START_HERE.md) |
| [application_history.md](reference/application_history.md) | Applications workflow in [START_HERE](../START_HERE.md) |
| [intake_hardening.md](history/intake_hardening.md) | Go tests under `backend/internal/jobcard` and `intakeextract` |
| [model_context.md](reference/model_context.md) | [agent_workflows.md](guides/agent_workflows.md) (`career-hub-agent context`) |
| [desktop_release_gates.md](reference/desktop_release_gates.md) | [Native packaging](../packaging/README.md) |

## Reviews and retrospectives

| Document | Purpose |
| --- | --- |

## Historical plans and evidence

| Document | Purpose |
| --- | --- |
| [product_spec.md](product/product_spec.md) | Original product specification and design discussion |
| [phase_plan.md](history/phase_plan.md) | Historical phase plan (replaced by the registry) |
| [mvp_workflow.md](guides/mvp_workflow.md) | Original MVP workflow |
| [user_stories.md](product/user_stories.md) | Early user stories |
| [optimization_backlog.md](product/optimization_backlog.md) | Context-optimization tickets OPT-001–OPT-010 |
| [desktop_delivery_roadmap.md](product/desktop_delivery_roadmap.md) | Desktop delivery roadmap |
| [cv_first_resume_management_plan.md](product/cv_first_resume_management_plan.md) | CV-first resume management delivery plan |
| [job_discovery_capture_plan.md](product/job_discovery_capture_plan.md) | Job discovery capture plan |
| [competency_graph_traceability.md](history/competency_graph_traceability.md) | Competency Graph task traceability |
| [cg900_remediation_2026-08-23.md](history/cg900_remediation_2026-08-23.md) | CG-900 remediation evidence |
| [mail_100_eml_parser_task.md](history/mail_100_eml_parser_task.md) | MAIL-100 `.eml` parser task |

## Architecture decision records

ADR files are not renamed when superseded; this table is the status index.

| ADR | Decision | Status |
| --- | --- | --- |
| [DESK-010a](adr/DESK-010a_pyinstaller_layout.md) | Packaged sidecar layout | Superseded: the Python sidecar is retired; the Go sidecar ships as a native executable ([packaging](../packaging/README.md)) |
| [DESK-010b](adr/DESK-010b_app_data_identity.md) | App data isolated by product and channel | Accepted |
| [DESK-010c](adr/DESK-010c_application_version.md) | One application version source (`VERSION`) | Accepted; the PyInstaller part is retired |
| [DESK-010d](adr/DESK-010d_release_channels.md) | Dev, beta, and stable channels | Accepted |
| [DESK-010e](adr/DESK-010e_supported_platforms.md) | Desktop support matrix | Accepted; target-host acceptance pending |
| [DESK-010f](adr/DESK-010f_data_compatibility.md) | Fail closed across app, Data Home, and SQLite versions | Accepted |
| [DESK-020](adr/DESK-020_offline_connectivity.md) | Explicit offline state | Implemented |
| [DESK-030](adr/DESK-030_desktop_shell.md) | Keep Electron as the desktop shell and keep it current | Accepted (2026-10-13) |
| [ELEC-340](adr/ELEC-340_lifecycle_and_process_ownership.md) | One desktop owner and native close semantics | Accepted and implemented |
| [LINK-000](adr/LINK-000_public_job_link_resolver.md) | Attended public job-link resolver | Implemented |
| [MAIL-200](adr/MAIL-200_dedicated_gmail_connector.md) | Dedicated Gmail connector | Accepted for private dogfood |
| [MIG-100](adr/MIG-100_backend_language_and_migration.md) | Backend language and migration boundary | Completed: Go is the only product backend since PR #40 (2026-10-03) |
| [SEC-240](adr/SEC-240_platform_secret_and_document_protection.md) | Platform key stores and document envelope | Implemented; target-host review pending |
| [SEC-250](adr/SEC-250_local_api_admission.md) | Admit only the owner's own clients to the local API | Accepted; items 1–3 and 5 implemented, extension pairing pending |

## Benchmarks

All four were measured on the Python runtime and are historical baselines.

| Document | Purpose |
| --- | --- |

## Migration records

`docs/history/migration/` is the record of the Python-to-Go port (September–October
2026). It is historical, but the per-slice contracts still describe behavior
the Go code deliberately preserves, so they are useful when changing those
areas. Links to `src/*.py` inside them point at deleted files.

Summaries:

| Document | Purpose |
| --- | --- |

Per-area contracts and designs:

- **Agent client:** `agent_caller_retirement`, `agent_context_contract`,
  `agent_materials_contract`, `agent_package_contract`,
  `agent_prepare_contract`, `agent_submission_contract`,
  `agent_vocabulary_contract`.
- **Applications:** `application_analysis_contract`,
  `application_checklist_contract`, `application_contacts_contract`,
  `application_helper_contract`, `application_http_contract`,
  `application_ledger_contract`, `application_maintenance_contract`,
  `application_reads_contract`, `application_review_contract`,
  `application_route_inventory`, `application_schema_contract`,
  `application_sessions_contract`, `application_texts_contract`.
- **Assessment and jobs:** `assessment_cache_contract`,
  `assessment_context_contract`, `saved_assessment_contract`,
  `saved_jd_reanalysis_sol_next`, `jd_reanalysis_safety_gate`,
  `job_card_contract`, `job_digest_contract`, `job_evaluation_contract`,
  `job_evidence_contract`, `new_jd_intake_contract`,
  `new_job_recovery_maintenance_next`, `native_intake_file_ambiguity_review`,
  `demand_coverage_native`, `public_link_resolution_native`.
- **Catalogs and documents:** `catalog_mutation_contract`,
  `document_ledger_contract`, `document_migration_maintenance_next`.
- **Discovery and extension:** `discovery_http_contract`,
  `discovery_reads_contract`, `discovery_writes_contract`,
  `extension_http_contract`.
- **Email:** `email_application_link_contract`,
  `email_application_updates_read_contract`, `email_connection_http_handoff`,
  `email_connection_snapshot_design`, `email_credential_clone_policy`,
  `email_credential_transaction_design`, `email_cursor_reset_contract`,
  `email_envelope_contract`, `email_header_metadata_contract`,
  `email_html_contract`, `email_import_projection_contract`,
  `email_lease_contract`, `email_message_contract`, `email_mime_contract`,
  `email_outcome_validation_contract`, `email_outcome_write_contract`,
  `email_plain_contract`, `email_receipts_contract`, `email_routing_contract`,
  `email_sender_mutation_contract`, `email_sender_rules_contract`,
  `email_source_contract`, `email_sync_http_contract`,
  `email_worker_contract`, `legacy_email_credential_transition`.
- **Materials:** `materials_contract`, `material_integrity_contract`,
  `material_render_contract`.
- **Resumes:** `resume_digest_write_design`, `resume_digest_writer_gate`,
  `resume_library_writes_contract`, `resume_management_parallel_read_design`,
  `resume_management_read_design`, `resume_plan_contract`.
- **Vault and profile:** `vault_http_contract`, `vault_mutation_contract`,
  `vault_profile_contract`, `vault_read_models_contract`,
  `vault_setup_contract`, `profile_http_contract`.
- **Backup and recovery:** `backup_restore_contract`,
  `shared_recovery_classifier_astra_gate`.
- **Contacts:** `contact_candidate_activation_review`,
  `contact_candidate_sol_next`.
- **Review handoffs:** `astra_review_2026_09_27`,
  `review_readiness_2026_09_27`, `sol_handoff`, `sol_resume_management_next`.
