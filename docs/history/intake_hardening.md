# JD Intake Hardening and Scale Verification

> **Status: needs rewrite.** The intent below is still accurate, but its `python -m src.*` commands belong to the retired Python runtime and no longer run.
> Until this document is rewritten, use the Go tests in `backend/internal/jobcard` and `backend/internal/intakeextract`. See the
> [documentation index](../README.md); historical comparisons use the
> pinned reference export.

This document defines the `CAP-JD-INTAKE-HARDENING` verification contract for the local JD intake workflow. It keeps the fast path short: a submitted posting becomes one local card, one ledger record, and one stored advisory assessment before the success response is returned. See [Capability Acceptance Registry](../process/acceptance_registry.md) for cross-capability status.

## Acceptance Contract

| Scenario | Required outcome |
| --- | --- |
| Fast intake | A new JD returns `201`, creates exactly one immutable source/card pair, registers `captured`, and returns its assessment. |
| Legacy Reviewed | Existing `reviewed` records and history remain readable and can move to a current stage, but no new action may create `reviewed`. |
| Fast applied path | **Save as applied** creates and registers the JD, then moves the new ledger record to `applied` before returning; a resume is optional and is truthfully stored as not recorded when absent. |
| Identity correction | Company/title and other supported fields are editable, retained through reanalysis, and recorded in telemetry. |
| Duplicate | Retries with the same idempotency key return the existing job (`200`). Posting URL or requisition ID defines an identified posting; content fingerprint is a fallback only when no identity is provided. |
| Assessment freshness | Assessment reuse requires matching job, active assessment-relevant candidate facts, context, evaluator revision, and evaluation month. Unrelated Vault edits and daily date changes do not recompute it. |
| Assessment failure | The response identifies `assessment_compute`; the captured job remains recoverable and a later assessment retry can succeed. |
| Resume selection | Saved resumes are searchable by display name; upload and selection remain available from Applications. The submitted resume is recorded separately so the immutable submission history is never overwritten. A direct Interview/Offer entry creates a truthful unknown-resume snapshot without fabricating an Applied event. |

The assessment is advisory. Neither a recommendation nor a missing selected resume blocks the applicant from recording a real application.

## Reproducible Metrics

Run the local report from the repository root:

```powershell
python -m src.intake_metrics --days 30
```

The report reads the append-only `career_vault/logs/intake.jsonl` and the private local ledger (`applications/career_hub.sqlite3`). It never writes a job, changes application status, or creates a database when one does not exist. A partial final JSONL line is ignored; events outside the requested time window and legacy events without the required terminal outcome are not guessed.

| Metric | Definition | Interpretation |
| --- | --- | --- |
| `add_job_latency_ms` | p50/p95 nearest-rank latency from successful terminal `job_intake_run` events. | End-to-end user wait time for both new and duplicate saves. |
| `assessment_computation_ms` | p50/p95 of successful uncached `assessment_compute` stages. | Cost of a real deterministic assessment, not cache reuse. |
| `assessment_cache_hit_rate` | Cached assessment completions / all completed assessment stages. | Diagnostic for reuse; a sudden drop can indicate a revision or cache issue. |
| `company_title_correction_rate` | Detail updates containing `company` or `title` / all telemetry-eligible detail updates. | Input-extraction quality signal, not a success/failure rate. |
| `duplicate_rate` | `already_captured` / known created-or-duplicate intake outcomes. | Measures repeated submissions; duplicates must not make an extra record. |
| `ledger_creation_failure_rate` | Failed ledger stages / all terminal ledger stages. | Reliability guardrail; target is zero failures. |
| `applied_records_with_selected_resume` | Applied snapshots with a selected resume / all applied snapshots recorded by the Hub. | Tracking completeness. It intentionally distinguishes a truthful `not recorded` from a selected resume. |

Rates include explicit numerator/denominator fields. `null` means there is no eligible observation, not a zero rate.

## Initial Operating Guardrails

These are release and monitoring guardrails, not hard-coded product limits. Establish a baseline after at least 25 distinct local postings on the normal development machine, then review by a fixed 30-day window.

- `add_job_latency_ms.p95_ms`: under 2,000 ms.
- `assessment_computation_ms.p95_ms`: under 1,000 ms for uncached work.
- `ledger_creation_failure_rate.failure_count`: zero.
- Any duplicate must leave the source/card count unchanged.
- Investigate a company/title correction rate above 25% once there are at least 50 eligible corrections; do not optimize against a smaller sample.
- Aim for at least 90% selected-resume coverage among applications submitted through the Hub. Already-applied roles intentionally logged without a known resume are valid exceptions and should remain visible rather than silently excluded.

## Scale Verification

Use isolated temporary data for repeatable verification. The automated suite covers 12 distinct sequential posts plus one duplicate, rejects creation of the legacy Reviewed stage, and verifies Applied transitions; it also checks the resulting report counts. This is a correctness and regression test, not a production throughput benchmark.

Before a release that changes intake, run:

```powershell
python -m unittest tests.test_intake_metrics tests.test_intake_ui -v
python -m src.validate_jobs
python -m src.intake_metrics --days 30
python -m src.application_history integrity
```

For browser smoke testing, use a disposable job. Tab to the saved-JD picker, use ArrowDown/End to move through options, type a search term, and use Escape to close it. In Applications, repeat keyboard search in the Resume picker, then confirm Escape closes the drawer without moving the selected record.

## Failure Handling

`assessment_preflight`, `job_draft_write`, `ledger_registration`, and `assessment_compute` are independently logged stages. A failure response contains a stable error code and, for stage failures, a stage name and run id. Unexpected HTTP failures also create a content-minimized `server_operation` record in the same local Diagnostics log; it records only the operation and exception type, never an exception message or local path. Do not mark a failed request as successfully saved in the UI. When an assessment fails after the immutable job and ledger record have been written, preserve that intake work; retrying the assessment is safe and does not create a duplicate.

Do not use these metrics to infer job suitability, applicant performance, or the quality of the Career Vault. They measure intake mechanics only.
