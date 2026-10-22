# Agent Workflows

Career Hub agents use the native client packaged with the Go desktop. In
**Settings > Local agent connection**, copy the verified `executablePath` and the
`connection` file path. Keep that app open; the file is rewritten at each start.
The copied connection contains no access token: the token lives only in that
owner-only file. In PowerShell, assign those values to
`$agent` and `$connection`, then invoke commands explicitly:

```powershell
& $agent context --connection $connection --job-id '<saved-job-id>' --purpose assessment
```

The client sends reads and writes through the running service's owner. It does
not discover a Data Home, start another writer or fall back to Python. It returns
compact JSON and can pass reviewed typed drafts to the deterministic renderer;
it performs no AI calls, scraping, external submission or email sending.
All nine product commands are packaged and qualified; installed discovery and
prepare/context are recorded in the connection contract.
`audit` is a separate repository development tool and is not an agent executable
command. Repo skills now use this native connection; default desktop packaging
and Python product retirement remain separate migration steps.

## Commands

| Command | Purpose | Writes |
| --- | --- | --- |
| `analyze` | Assess a saved or ephemeral JD | None |
| `prepare` | Save/reuse the exact JD and start material preparation | JD when new; ledger to `Preparing` |
| `context` | Return `assessment`, `resume_delta`, or `application_text` context | None |
| `material-context` | Return one bounded resume or cover-letter draft and its allowed evidence | None |
| `save-draft` | Validate and upsert one private application-text draft | Workbench draft only |
| `save-material` | Validate and save one typed resume or cover-letter draft | Job-scoped private material only |
| `render-material` | Render and structurally validate DOCX, PDF, or TXT | Job-scoped local export only |
| `registry-add-exact` | Add one reviewed exact JD technology | Private custom vocabulary; never evidence |
| `record-submission` | Record a submission that happened outside Career Hub | Ledger, only with `--confirm-submitted` |

Use `--pretty` for inspection; omit it for the smaller agent payload. Use
`--timings` only for one lightweight elapsed-time diagnostic on stderr; stdout
remains the same JSON contract. A raw JD
may be supplied with `--source-file` or `--stdin`. `--connection` names the owner-only connection file shown in Settings; the
running sidecar writes the server address and a scoped agent token there, and
the client never receives a token over HTTP (ADR SEC-250). `--connection` is
required; an optional `--server` must match the file. The
client accepts no profile or Data Home argument.
Registry mode defaults to `off`; pass `--registry-mode exact-auto` explicitly
to expose write-ready group context for a persisted JD.
Use `prepare --purpose application_text` or `prepare --purpose resume_delta` to
save a new JD and receive the next task packet in one pass.

For one exact writing or resume-comparison task, pass a strict bounded JSON
request to `prepare` or `context` with `--task-request-json`. The existing
`--task-request-file` form remains supported. Examples:

```json
{
  "schema_version": 1,
  "purpose": "application_text",
  "kind": "short_answer",
  "exact_prompt": "Why are you interested in this role?",
  "limits": {"characters": 500}
}
```

```json
{
  "schema_version": 1,
  "purpose": "resume_delta",
  "resume_id": "resume_<id>"
}
```

Unknown fields, incompatible purpose fields, unsupported writing kinds, and
non-positive limits fail validation. `workbench_item_id` may identify one
existing draft when revising it. The request is task intent, not candidate
evidence, and is echoed under `task` so the caller does not need ambient chat
state to reconstruct it.

An explicitly requested Resume Version stays first in the bounded context even
when it is already the application's selected version. Distinct requested and
selected versions retain that order; duplicate priority IDs consume one rank.

Route directly to the command and repo skill matching the requested outcome.
An application-text or resume request does not require a separate assessment
pass unless the user asks for a fit opinion or the selected workflow reports
that required saved-job context is unavailable.

## Stable Boundaries

- `analyze` does not persist a discussion-only JD.
- Repeating `prepare` reuses the JD and returns `no_change_needed` when the
  application is already active.
- Preparation artifacts stop at `Preparing`; they never imply `Applied`.
- `no_change_needed` is valid for resume tailoring and other reviews. No skill
  has a quota of edits or recommendations.
- Required lines routed to parser review remain in the coverage denominator and
  keep the advisory verdict at `CONSIDER`; they cannot disappear into a falsely
  complete parsed score.
- A content-rich source with no parsed required technical items reports
  incomplete required parsing and remains `CONSIDER` for manual review.
- Exact JD vocabulary is stored in the private overlay so it is recognized
  immediately without mutating product source during a live application. Core
  promotion remains a separate reviewed development change.
- Registry vocabulary, whether core or custom, is never candidate evidence.
- Application text defaults to inline delivery. Google Docs is explicit-only.
  When a material artifact is requested, agents use `render-material` rather
  than manually constructing a Word document.

For an approved Resume Plan, `material-context --kind resume` returns only the
selected evidence plus editable summary and bullet fields. Skills, education,
source metadata, plan fingerprints, and layout are code-owned. For a cover
letter, `material-context --kind cover_letter` returns the reusable structure
and a bounded Career Vault evidence catalog. JD match terms are returned in a
separate `job_vocabulary_metadata` collection, never inside candidate evidence
rows. `no_change_needed` is valid for
both; the contracts never impose a rewrite quota.

Context is purpose-specific:

- `assessment` returns at most eight Resume Version metadata rows and does not
  derive Resume Digest state;
- `application_text` returns only selected Resume Version metadata, plus at
  most three workbench item summaries unless the typed request identifies one
  related item;
- `resume_delta` returns at most eight Resume Versions with digest state and at
  most twenty experience candidates, prioritizing the requested resume.

The adapter emits a `contract_version`, write receipt, no-forcing policy,
reusable `context_snapshot.id`, packet fingerprint, and rough context metrics.
Budgets use the conservative four-characters-per-token estimate: 4,500 tokens
for assessment, 6,500 for application text, 6,500 for resume delta, 5,000 for
cover-letter material, and 3,000 for resume material. `within_budget` is
diagnostic and tests protect the bounded fixtures; it does not silently remove
facts. Requirement, evidence, registry, resume, and workbench projections also
have explicit hard caps.

`save-material` returns a compact mutation receipt instead of echoing the full
editor packet. It includes the new revision, status, validation counts and
codes, source revisions, and `reload_required`. Continue from the caller's
local draft and returned revision when `reload_required` is false; call
`material-context` again only when a fresh editor is actually needed.

## Repo Skills

Discoverable instructions live in `.agents/skills/`:

- `career-hub-assess-job`
- `career-hub-tailor-resume`
- `career-hub-write-application`
- `career-hub-audit-workflows` (explicit invocation only)

Each skill has `agents/openai.yaml` and `references/contract.yaml`. The contract
declares the adapter version, commands, implementation paths, and focused tests
that keep the skill relevant after code changes.

## Maintenance Audit

After changing a mapped service or a repo skill, run:

```powershell
python -m scripts.agent_workflow_audit --project-root . --changed --pretty
```

`review_required` means relevant working-tree paths changed and warrants a
semantic check; it does not force a skill edit. `outdated` means a command,
contract version, or tracked path no longer matches. `broken` means required
metadata or contract structure is invalid. The command exits nonzero only for
`outdated` or `broken`.

When the CLI contract intentionally changes, increment
`ContractVersion` in `backend/internal/agentmaterials/packet.go`, update the
native CLI's advertised commands and affected skill contracts and instructions,
and run their mapped tests plus the skill validator. The audit recognizes
literal declarations without executing the checkout; native executable
qualification separately verifies command behavior. The development audit has
its own `AUDIT_SCHEMA_VERSION`. A later MCP
or plugin may wrap this CLI if another client needs discovery over a protocol;
the local CLI remains the source of behavior rather than duplicating logic.
