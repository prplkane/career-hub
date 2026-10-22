# Career Hub Architecture Atlas

This atlas is the shared map of the implemented Career Hub system for users,
maintainers, and coding agents. It explains where data lives, which records are
authoritative, how the major workflows cross trust boundaries, and how the
stored models relate. It describes current behavior; it does not authorize a
future cloud service, scraper, AI provider, or automatic submission feature.

Use this document for orientation, then follow the linked contract or source
file for field-level behavior:

- [Architecture decisions](architecture.md)
- [Schema reference](schema_reference.md)
- [Current system pipeline](system_pipeline.md)
- [SQLite application history](application_history.md)
- [Data Home and recovery](data_home.md)
- [Native sidecar protocol](sidecar_protocol.md)
- [Job Feed, extension, and email plan](../product/job_discovery_capture_plan.md)
- [Agent workflow adapter](../guides/agent_workflows.md)
- [Manual acceptance paths](../guides/manual_testing.md)

The implementation is the final authority. The main model sources are
[`backend/internal/vaultread/validate.go`](../../backend/internal/vaultread/validate.go),
[`backend/internal/jobcard/validate.go`](../../backend/internal/jobcard/validate.go),
[`backend/internal/applications/schema/steps_generated.go`](../../backend/internal/applications/schema/steps_generated.go), and the typed
models linked below.

## How to read the atlas

Career Hub has four materially different forms of state. Keeping them separate
prevents the most common architecture mistakes.

| State kind | Meaning | Examples |
| --- | --- | --- |
| Canonical | Human-reviewed facts or configuration that future work may reuse | Career Vault YAML, private Skills Registry overlays |
| Transactional | Mutable workflow history with revisions and append-only events | SQLite application, discovery, email, contact, and session records |
| Retained source or audit | Exact input or a review trail; not automatically canonical | raw resumes, retained JDs, extraction/review/approval artifacts, discovery captures |
| Derived or ephemeral | Rebuildable output or in-memory/browser-only state | assessments, evidence/demand projections, rendered exports, capability tokens, UI filters |

In ER diagrams, `||` means exactly one, `o|` means zero or one, `|{` means one
or more, and `o{` means zero or more. The conceptual diagrams cross file and
database boundaries. The physical ER diagrams represent the SQLite v27 schema;
their entity labels use the exact table names.

## 1. System context and trust boundaries

```mermaid
flowchart LR
  USER["User"]
  AGENT["Coding agent"]
  MAIL["Dedicated Gmail or explicit EML files"]
  PAGE["Attended job or application page"]

  subgraph CLIENTS["Local clients"]
    UI["Browser UI"]
    EXT["Browser extension"]
    CLI["Agent workflow and local CLIs"]
    DESKTOP["Electron desktop host"]
  end

  subgraph PYTHON["Go application boundary"]
    API["Origin-checked loopback API"]
    SIDECAR["Owned Go sidecar"]
    DOMAIN["Deterministic domain services"]
    MAIL_ADAPTER["Read-only email adapter"]
  end

  subgraph DATA["Private Career Hub Data Home"]
    VAULT["Career Vault and taxonomy YAML"]
    SOURCES["Resume, JD, and discovery sources"]
    SQLITE["SQLite workflow ledger"]
    MATERIALS["Revisioned drafts and local exports"]
    DOCUMENTS["Encrypted document ledger and blobs"]
  end

  RESOURCES["Immutable packaged resources and catalogs"]
  SECRET["Credential Manager or Keychain"]

  USER --> UI
  USER --> EXT
  USER --> CLI
  USER --> DESKTOP
  PAGE --> EXT
  AGENT --> CLI
  MAIL --> MAIL_ADAPTER
  UI --> API
  EXT -->|"separate least-privilege capabilities"| API
  DESKTOP -->|"versioned stdin/stdout protocol"| SIDECAR
  SIDECAR --> API
  CLI --> DOMAIN
  API --> DOMAIN
  MAIL_ADAPTER --> DOMAIN
  DOMAIN --> VAULT
  DOMAIN --> SOURCES
  DOMAIN --> SQLITE
  DOMAIN --> MATERIALS
  DOMAIN --> DOCUMENTS
  RESOURCES --> DOMAIN
  MAIL_ADAPTER -->|"credential lookup only"| SECRET
  DOCUMENTS -->|"master-key lookup only"| SECRET
```

The entire product boundary is local. The browser and extension never own
business rules, and Electron never owns candidate data. The extension receives
different purpose-specific capabilities for capture, application assistance,
and attended submission tracking. It cannot turn one capability into a general
Career Vault or application reader. The email connector is receive-only; there
is no SMTP path. No repository module calls an AI API.

## 2. Component responsibility UML

```mermaid
classDiagram
  class BrowserUI {
    <<adapter>>
  }
  class BrowserExtension {
    <<least_privilege_adapter>>
  }
  class AgentWorkflowCLI {
    <<adapter>>
  }
  class ElectronHost {
    <<process_owner>>
  }
  class LoopbackAPI {
    <<authorization_boundary>>
  }
  class RouteDispatchers {
    <<thin_adapter>>
  }
  class VaultAndIntakeServices {
    <<domain>>
  }
  class DiscoveryAndEmailServices {
    <<domain>>
  }
  class ApplicationLedgerServices {
    <<domain>>
  }
  class AssessmentAndEvidenceServices {
    <<derived_domain>>
  }
  class ContactAndHelperServices {
    <<domain>>
  }
  class MaterialServices {
    <<domain>>
  }
  class FileRepositories {
    <<persistence>>
  }
  class SQLiteRepository {
    <<persistence>>
  }
  class OSSecretStore {
    <<platform_boundary>>
  }

  BrowserUI --> LoopbackAPI
  BrowserExtension --> LoopbackAPI
  AgentWorkflowCLI --> VaultAndIntakeServices
  AgentWorkflowCLI --> ApplicationLedgerServices
  AgentWorkflowCLI --> AssessmentAndEvidenceServices
  AgentWorkflowCLI --> MaterialServices
  ElectronHost --> LoopbackAPI : owns sidecar
  LoopbackAPI --> RouteDispatchers
  RouteDispatchers --> VaultAndIntakeServices
  RouteDispatchers --> DiscoveryAndEmailServices
  RouteDispatchers --> ApplicationLedgerServices
  RouteDispatchers --> AssessmentAndEvidenceServices
  RouteDispatchers --> ContactAndHelperServices
  RouteDispatchers --> MaterialServices
  VaultAndIntakeServices --> FileRepositories
  DiscoveryAndEmailServices --> FileRepositories
  DiscoveryAndEmailServices --> SQLiteRepository
  ApplicationLedgerServices --> SQLiteRepository
  AssessmentAndEvidenceServices --> FileRepositories
  AssessmentAndEvidenceServices --> SQLiteRepository
  ContactAndHelperServices --> FileRepositories
  ContactAndHelperServices --> SQLiteRepository
  MaterialServices --> FileRepositories
  MaterialServices --> SQLiteRepository
  DiscoveryAndEmailServices --> OSSecretStore : Gmail credential
  FileRepositories --> OSSecretStore : document key only
```

The dependency direction is intentional: adapters delegate to domain services;
domain services own validation, identity, state transitions, and idempotency;
persistence modules store already-validated state. Route handlers do not own
evidence, matching, document, or promotion logic.

## 3. Storage topology and ownership

```mermaid
flowchart TB
  ROOT["Selected Data Home"]
  ROOT --> CV["career_vault"]
  ROOT --> JOBS["jobs"]
  ROOT --> APPS["applications"]
  ROOT --> OUT["outputs"]
  ROOT --> DOCS["documents"]
  ROOT --> OPS["logs, backups, security-reports, secrets"]

  CV --> CV_FACTS["Canonical facts and policies in YAML"]
  CV --> CV_TAX["Private vocabulary overlays"]
  CV --> CV_RAW["Raw resume sources and intake audit runs"]

  JOBS --> JD_RAW["Immutable incoming JDs"]
  JOBS --> JD_CARD["Validated normalized job cards"]
  JOBS --> DISC_SRC["Retained discovery source snapshots"]

  APPS --> DB["career_hub.sqlite3"]
  APPS --> RESUME_BYTES["Immutable Resume Version bytes"]

  OUT --> DRAFTS["Revisioned resume and cover-letter YAML"]
  OUT --> EXPORTS["Derived DOCX, PDF, and TXT"]

  DOCS --> DOC_LEDGER["AES-GCM encrypted ledger"]
  DOCS --> DOC_BLOBS["AES-GCM encrypted PDF and image blobs"]

  RES["Packaged resource root"] --> CATALOGS["Core catalogs, UI assets, templates, fonts"]
  MEMORY["Process and browser memory"] --> TOKENS["Capabilities, review tokens, unsaved forms"]
  BROWSER_STORAGE["Browser local or session storage"] --> PREFS["Presentation preferences and bounded session metadata"]
```

| Owner and path | Stored model | Authority and mutation rule |
| --- | --- | --- |
| `career_vault/profile.yaml` | singleton candidate profile | Canonical after validated save; optimistic file revision |
| `career_vault/visa_profile.yaml` | confirmed eligibility facts | Canonical private facts; unknown remains unknown |
| `career_vault/education.yaml` | schools and certifications | One aggregate and one shared revision |
| `career_vault/experience_blocks.yaml` | experience records | Canonical evidence; archive is reversible |
| `career_vault/project_blocks.yaml` | project records | Canonical evidence with optional Experience links |
| `career_vault/skill_blocks.yaml` | skill-family records | Canonical organization and evidence links; distinct from the registry taxonomy |
| `career_vault/proof_blocks.yaml` | traceable reusable claims | Canonical claim index; every item names one source block |
| `career_vault/style_rules.yaml` | writing, ATS, selection, and quality rules | Canonical configuration, not generated prose |
| `career_vault/taxonomy/*.yaml` | private skill groups and skills | Canonical vocabulary overlay; vocabulary is never candidate evidence by itself |
| `career_vault/hard_blocker_policy.yaml` | enabled deterministic conflict classes | Private revisioned policy; advisory only |
| `career_vault/application_helper.yaml` | enabled helper field ids | Private allowlist; contains no projected values |
| `career_vault/application_answers.yaml` | explicitly saved safe short answers | Lazy, private, revisioned; excluded categories are never stored |
| `career_vault/raw/` and `career_vault/intake_runs/` | raw sources, extraction, review, approval result | Source/audit trail; no canonical write before approval apply |
| `jobs/incoming/` | exact retained JD text | Immutable source used by normalized cards |
| `jobs/normalized/` | validated normalized job YAML | Analyzed source record; assessment and application stage remain separate |
| `jobs/discovery/` | exact retained discovery text | Private source snapshot referenced by SQLite discovery rows |
| `applications/career_hub.sqlite3` | all high-churn workflow records | Transactional source of truth; schema v27, revisions, append-only events |
| `applications/resumes/library/` | immutable Resume Version bytes | Addressed by private SQLite metadata and integrity hash |
| `outputs/application_materials/<job-id>/` | typed resume and cover-letter drafts plus exports | YAML draft is editable truth; exports are rebuildable derivatives |
| `documents/` | encrypted metadata ledger and encrypted blobs | Narrow AES-GCM at-rest boundary; key is stored by the OS, not in the Data Home |
| `logs/`, `backups/`, `security-reports/` | operational evidence and recovery artifacts | Private local operations; never candidate evidence |
| `src/data/` and packaged assets | core catalogs and immutable resources | Product-owned read-only input; not copied into private facts |
| process/browser memory | capability tokens, one-shot review tokens, unsaved state | Ephemeral; never a durable source of truth |

## 4. Conceptual Career Vault model

This is a cross-file model. It is not a SQL schema. `REGISTRY_SKILL` represents
the effective merged core plus private Skills Registry; active Experience,
Project, and Certification records cite it through `competency_ids`.
`SKILL_FAMILY_RECORD` represents the separate legacy-compatible records in
`skill_blocks.yaml`.

```mermaid
erDiagram
  CAREER_VAULT ||--|| PROFILE : contains
  CAREER_VAULT ||--|| ELIGIBILITY_PROFILE : contains
  CAREER_VAULT ||--|| EDUCATION_AGGREGATE : contains
  CAREER_VAULT ||--o{ EXPERIENCE : contains
  CAREER_VAULT ||--o{ PROJECT : contains
  CAREER_VAULT ||--o{ SKILL_FAMILY_RECORD : contains
  CAREER_VAULT ||--o{ PROOF_BLOCK : contains
  CAREER_VAULT ||--|| STYLE_RULES : contains

  EDUCATION_AGGREGATE ||--o{ SCHOOL : owns
  EDUCATION_AGGREGATE ||--o{ CERTIFICATION : owns

  PROJECT o{--o{ EXPERIENCE : related_experience
  REGISTRY_GROUP ||--o{ REGISTRY_SKILL : groups
  EXPERIENCE o{--o{ REGISTRY_SKILL : competency_ids
  PROJECT o{--o{ REGISTRY_SKILL : competency_ids
  CERTIFICATION o{--o{ REGISTRY_SKILL : competency_ids

  SKILL_FAMILY_RECORD o{--o{ EXPERIENCE : related_experience
  SKILL_FAMILY_RECORD o{--o{ PROJECT : related_projects
  SKILL_FAMILY_RECORD o{--o{ CERTIFICATION : related_certifications

  EXPERIENCE o|--o{ PROOF_BLOCK : may_source
  PROJECT o|--o{ PROOF_BLOCK : may_source
  CERTIFICATION o|--o{ PROOF_BLOCK : may_source

  CAREER_VAULT {
    string vault_revision
  }
  PROFILE {
    string name
    string work_authorization
    string target_location
  }
  ELIGIBILITY_PROFILE {
    string current_status
    boolean future_sponsorship
    boolean stem_opt_eligible
  }
  EXPERIENCE {
    string id
    string company
    string role
    string dates
    boolean archived
  }
  PROJECT {
    string id
    string name
    string summary
    boolean archived
  }
  SCHOOL {
    string id
    string institution
    string degree
    boolean archived
  }
  CERTIFICATION {
    string id
    string name
    string credential
    boolean archived
  }
  REGISTRY_GROUP {
    string id
    string name
    string ownership
  }
  REGISTRY_SKILL {
    string id
    string name
    string primary_group_id
    boolean archived
  }
  SKILL_FAMILY_RECORD {
    string id
    string skill_family
    string_array keywords
  }
  PROOF_BLOCK {
    string id
    string claim
    string evidence_type
    string source_block
  }
```

Each `PROOF_BLOCK` resolves to exactly one Experience, Project, or Certification
source according to `evidence_type`; the three optional diagram edges express
that polymorphic rule. Archived evidence remains valid for history but is
excluded from new active projections. A registry name, alias, or job
requirement never becomes proof merely by matching text.

## 5. Conceptual job-to-application model

```mermaid
erDiagram
  EMAIL_MESSAGE o{--o{ DISCOVERY : may_produce
  EMAIL_MESSAGE o{--o{ EMAIL_APPLICATION_OUTCOME : may_produce
  EXTENSION_CAPTURE o{--|| DISCOVERY : creates_or_enriches
  PUBLIC_RESOLUTION o{--|| DISCOVERY : may_enrich

  DISCOVERY ||--o{ DISCOVERY_OBSERVATION : records
  DISCOVERY ||--o| DISCOVERY_PREVIEW : may_have
  DISCOVERY o|--o| NORMALIZED_JOB : explicitly_promotes_to

  RETAINED_JD ||--|| NORMALIZED_JOB : analyzed_as
  NORMALIZED_JOB ||--|| APPLICATION : registered_as
  NORMALIZED_JOB ||--o{ SOURCE_INSTRUCTION : derives
  NORMALIZED_JOB ||--o{ CONTACT_CANDIDATE : derives

  APPLICATION ||--o{ STATUS_EVENT : appends
  APPLICATION ||--o| CACHED_ASSESSMENT : caches
  APPLICATION ||--o| RESUME_PLAN : owns
  APPLICATION ||--o{ MATERIAL_DRAFT : owns
  APPLICATION o{--o| RESUME_VERSION : currently_selects
  APPLICATION ||--o{ SUBMITTED_RESUME_EVENT : records
  APPLICATION o{--o{ CONTACT : explicitly_links
  APPLICATION ||--o{ APPLICATION_SESSION : may_track
  APPLICATION ||--o{ INSTRUCTION_ACTION : records

  RESUME_PLAN o{--o{ VAULT_EVIDENCE : selects
  MATERIAL_DRAFT o{--o{ VAULT_EVIDENCE : cites
  MATERIAL_DRAFT ||--o{ RENDERED_EXPORT : renders

  DISCOVERY {
    string discovery_id
    string lifecycle_state
    string content_scope
    int revision
  }
  NORMALIZED_JOB {
    string job_id
    string analysis_status
    string content_fingerprint
  }
  APPLICATION {
    string job_id
    string current_stage
  }
  RESUME_VERSION {
    string resume_id
    string sha256
    string archive_state
  }
  RESUME_PLAN {
    string status
    int revision
    string vault_revision
    string job_fingerprint
  }
  MATERIAL_DRAFT {
    string kind
    string status
    int revision
  }
  APPLICATION_SESSION {
    string state
    string expected_url
    int revision
  }
  CONTACT {
    string contact_id
    int revision
    string archive_state
  }
```

Three states that look similar in the UI are deliberately independent:

- normalized job `draft` / `ready` describes source-analysis review;
- Discovery `inbox` / `shortlisted` / `dismissed` / `promoted` describes an
  opportunity candidate before ledger promotion;
- application stage describes the user's saved/application history.

An assessment is cached derived data, not a fourth workflow state.

## 6. Physical SQLite ERD

The application database is owned only by
[`backend/internal/applications/schema/steps_generated.go`](../../backend/internal/applications/schema/steps_generated.go). It currently has
32 tables at schema version 27. The diagrams show every table and every
declared parent relationship, split into three bounded contexts so each remains
readable. Attribute boxes show the key, state, revision, and integrity anchors;
the schema owner remains the exact source for every scalar column and check
constraint.

### 6.1 Applications, assessments, and Resume Versions

```mermaid
erDiagram
  job_applications ||--o{ application_status_events : appends
  job_applications ||--o| application_resume_attachments : selects
  job_applications ||--o| application_resume_plans : owns
  job_applications ||--o| job_assessments : caches
  job_applications ||--o{ job_assessment_resolution_events : resolves
  job_applications ||--o{ application_applied_resume_events : records
  resume_library o|--o{ application_resume_attachments : selected_by
  resume_library o|--o{ job_applications : snapshotted_by
  resume_library o|--o{ application_applied_resume_events : recorded_in
  resume_library ||--o| approved_import_resume_versions : mapped_from

  job_applications {
    TEXT job_id PK
    TEXT current_status
    TEXT content_fingerprint
    TEXT applied_resume_id
    INTEGER applied_resume_recorded
  }
  application_status_events {
    INTEGER event_id PK
    TEXT job_id FK
    TEXT status
    TEXT occurred_at
    TEXT recorded_at
  }
  application_resume_attachments {
    TEXT job_id PK
    TEXT resume_id
    TEXT relative_path
    TEXT sha256
  }
  application_resume_plans {
    TEXT job_id PK
    TEXT plan_json
    TEXT status
    INTEGER revision
  }
  job_assessments {
    TEXT job_id PK
    TEXT job_fingerprint
    TEXT candidate_revision
    TEXT assessment_context_revision
    INTEGER assessment_version
  }
  job_assessment_resolution_events {
    INTEGER event_id PK
    TEXT job_id FK
    TEXT finding_key
    TEXT assessment_revision
    TEXT resolution
  }
  resume_library {
    TEXT resume_id PK
    TEXT relative_path UK
    TEXT sha256
    TEXT archived_at
    TEXT origin
  }
  application_applied_resume_events {
    INTEGER event_id PK
    TEXT job_id FK
    TEXT resume_id FK
    TEXT action
    TEXT occurred_at
  }
  approved_import_resume_versions {
    TEXT source_id PK
    TEXT source_sha256
    TEXT resume_id FK
  }
```

`application_resume_attachments.resume_id` and
`job_applications.applied_resume_id` were added to migrated tables and are
protected by database triggers rather than declared foreign-key clauses.
Deletion guards prevent a referenced Resume Version from disappearing.

### 6.2 Discovery and email intake

```mermaid
erDiagram
  job_discoveries ||--o{ job_discovery_observations : records
  job_discoveries ||--o| job_discovery_assessments : previews
  job_discoveries ||--o| job_discovery_field_overrides : corrects
  job_discoveries o|--o| job_applications : promotes_to
  job_email_connections ||--o{ job_email_sender_rules : authorizes
  job_email_connections ||--o{ job_email_sync_state : checkpoints
  job_email_connections ||--o{ job_email_message_receipts : receives
  job_discoveries o|--o{ job_email_application_outcomes : may_match
  job_applications o|--o{ job_email_application_outcomes : may_update

  job_discoveries {
    TEXT discovery_id PK
    TEXT state
    TEXT content_scope
    TEXT source_relative_path
    TEXT content_sha256
    TEXT promoted_job_id FK
    INTEGER revision
  }
  job_discovery_observations {
    INTEGER observation_id PK
    TEXT discovery_id FK
    TEXT source_kind
    TEXT source_channel
    TEXT request_fingerprint UK
    TEXT content_sha256
  }
  job_discovery_assessments {
    TEXT discovery_id PK
    TEXT content_sha256
    TEXT candidate_revision
    TEXT assessment_context_revision
    TEXT scope
  }
  job_discovery_field_overrides {
    TEXT discovery_id PK
    TEXT title
    TEXT company
    TEXT location
    INTEGER revision
  }
  job_email_connections {
    TEXT connection_id PK
    TEXT provider
    TEXT account_address
    TEXT secret_name
    INTEGER enabled
    INTEGER revision
  }
  job_email_sender_rules {
    TEXT connection_id PK
    TEXT sender_address PK
    TEXT disposition
    TEXT purpose
  }
  job_email_sync_state {
    TEXT connection_id PK
    TEXT folder PK
    TEXT uidvalidity
    INTEGER last_uid
    TEXT last_result
    INTEGER revision
  }
  job_email_message_receipts {
    INTEGER receipt_id PK
    TEXT connection_id FK
    INTEGER message_uid
    TEXT message_id_hash
    TEXT content_sha256
    TEXT status
  }
  job_email_application_outcomes {
    TEXT outcome_id PK
    TEXT message_fingerprint
    TEXT posting_identity
    TEXT application_status
    TEXT resolution
    TEXT matched_discovery_id FK
    TEXT matched_job_id FK
    INTEGER revision
  }
  job_applications {
    TEXT job_id PK
    TEXT current_status
  }
```

An email message receipt and an email application outcome share minimized
message identity but intentionally have no foreign key. One transport message
may yield independent opportunity discoveries and application outcomes. The
database never stores the Gmail App Password or the original message body.

### 6.3 Attended sessions, instructions, contacts, and reusable answers

```mermaid
erDiagram
  job_applications ||--o{ application_sessions : tracks
  application_sessions ||--o{ application_session_events : observes
  job_applications ||--o{ application_instruction_manual_items : owns
  job_applications ||--o{ application_instruction_action_events : appends
  application_contacts ||--o{ application_contact_channels : has
  application_contacts ||--o{ application_contact_provenance : proves
  application_contacts ||--o{ application_contact_job_links : links
  application_contacts ||--o{ application_contact_discovery_links : links
  job_applications ||--o{ application_contact_job_links : relates
  job_discoveries ||--o{ application_contact_discovery_links : relates
  reusable_answer_families ||--o{ reusable_answer_aliases : recognizes
  reusable_answer_families ||--o{ reusable_answer_revisions : versions
  reusable_answer_families ||--o{ reusable_answer_representations : maps
  reusable_answer_families ||--o{ reusable_answer_use_events : records
  reusable_answer_aliases o|--o{ reusable_answer_representations : contextualizes
  reusable_answer_revisions o|--o{ reusable_answer_use_events : supplies
  reusable_answer_aliases o|--o{ reusable_answer_use_events : matches
  reusable_answer_representations o|--o{ reusable_answer_use_events : fills
  job_applications o|--o{ reusable_answer_use_events : contextualizes

  job_applications {
    TEXT job_id PK
    TEXT current_status
  }
  application_sessions {
    TEXT session_id PK
    TEXT job_id FK
    TEXT state
    TEXT expected_url
    TEXT expected_origin
    TEXT session_token_sha256
    INTEGER revision
  }
  application_session_events {
    INTEGER event_id PK
    TEXT session_id FK
    TEXT event_key UK
    TEXT event_type
    TEXT rule_id
  }
  application_instruction_manual_items {
    INTEGER item_id PK
    TEXT job_id FK
    TEXT instruction_fingerprint
    TEXT source_fingerprint
    TEXT category
    TEXT source_text
  }
  application_instruction_action_events {
    INTEGER event_id PK
    TEXT job_id FK
    TEXT instruction_fingerprint
    TEXT source_fingerprint
    TEXT action
    TEXT origin
  }
  application_contacts {
    TEXT contact_id PK
    TEXT name
    TEXT identity_channel_type
    TEXT identity_channel_normalized
    TEXT archived_at
    INTEGER revision
  }
  application_contact_channels {
    TEXT channel_id PK
    TEXT contact_id FK
    TEXT channel_type
    TEXT exact_value
    TEXT normalized_value
  }
  application_contact_provenance {
    TEXT provenance_id PK
    TEXT contact_id FK
    TEXT origin
    TEXT source_reference
    TEXT source_content_sha256
    TEXT stated_fields_json
  }
  application_contact_job_links {
    TEXT contact_id PK
    TEXT job_id PK
    TEXT created_at
  }
  application_contact_discovery_links {
    TEXT contact_id PK
    TEXT discovery_id PK
    TEXT created_at
  }
  reusable_answer_families {
    TEXT answer_id PK
    TEXT source_kind
    TEXT canonical_question
    TEXT value_kind
    TEXT current_revision_id FK
    INTEGER metadata_revision
  }
  reusable_answer_aliases {
    TEXT alias_id PK
    TEXT answer_id FK
    TEXT normalized_text UK
    TEXT retired_at
  }
  reusable_answer_revisions {
    TEXT answer_revision_id PK
    TEXT answer_id FK
    INTEGER revision_number
    TEXT value_fingerprint
  }
  reusable_answer_representations {
    TEXT representation_id PK
    TEXT answer_id FK
    TEXT alias_id FK
    TEXT control_kind
    TEXT verification_state
  }
  reusable_answer_use_events {
    TEXT event_id PK
    TEXT answer_id FK
    TEXT answer_revision_id FK
    TEXT job_id FK
    TEXT outcome
  }
  job_discoveries {
    TEXT discovery_id PK
    TEXT state
  }
```

Contact provenance is append-only. Its `job_id` and `discovery_id` snapshot
fields are historical text, not foreign keys; deleting or unlinking a source
therefore cannot erase provenance. The two link tables are the live relational
edges. Instruction action events likewise refer to a stable instruction and
source fingerprint rather than mutating the detected or user-selected evidence.
Reusable answer revisions and use events are immutable audit records. A family
may instead resolve a current canonical profile field, so its use event stores
either an answer revision or a canonical source revision, never both.

### 6.4 Complete table index

| Bounded context | Exact table | Purpose | Change model |
| --- | --- | --- | --- |
| Application | `job_applications` | one current ledger row and JD snapshot per job | mutable current state plus guarded snapshot fields |
| Application | `application_status_events` | dated stage and note history | append-only |
| Application | `job_assessments` | current deterministic assessment cache | replace when source/context revision changes |
| Application | `job_assessment_resolution_events` | user handling of one assessment finding | append-only |
| Resume | `resume_library` | immutable Resume Version metadata | add; reversible archive; guarded delete when unused |
| Resume | `application_resume_attachments` | current Resume Version selection per application | replace or clear |
| Resume | `application_applied_resume_events` | initial and corrected submitted-resume history | append-only |
| Resume | `application_resume_plans` | one revisioned selection plan per application | optimistic replace |
| Resume | `approved_import_resume_versions` | idempotent approved-source-to-version mapping | one-to-one mapping |
| Discovery | `job_discoveries` | effective opportunity record, content identity, lifecycle, promotion | optimistic revision |
| Discovery | `job_discovery_observations` | producer provenance and idempotent request receipt | append-only |
| Discovery | `job_discovery_assessments` | current complete-content preview | replace when assessment inputs change |
| Discovery | `job_discovery_field_overrides` | explicit title/company/location corrections | optimistic replace; all-null row may remain |
| Email | `job_email_connections` | receive-only Gmail configuration without the secret value | optimistic revision |
| Email | `job_email_sender_rules` | exact sender allow/disable and stream purpose | composite-key upsert |
| Email | `job_email_sync_state` | UID cursor, lease, retry, and result per folder | optimistic checkpoint |
| Email | `job_email_message_receipts` | content-minimized idempotency and retry receipt | update one mailbox message identity |
| Email | `job_email_application_outcomes` | explicit status evidence and optional application/discovery resolution | idempotent record plus optimistic link resolution |
| Session | `application_sessions` | exact URL, tab, Origin, token-hash, and review state | constrained state transitions with revision |
| Session | `application_session_events` | minimal paired submit/success signal | append-only and event-key idempotent |
| Instruction | `application_instruction_manual_items` | exact user-selected missed instruction evidence | immutable unique evidence |
| Instruction | `application_instruction_action_events` | pending/done/not-applicable history | append-only |
| Contact | `application_contacts` | canonical stated identity and archive state | optimistic revision |
| Contact | `application_contact_channels` | exact email, phone, or professional-profile channels | transactional child records |
| Contact | `application_contact_provenance` | immutable source and stated-field snapshot | append-only, trigger protected |
| Contact | `application_contact_job_links` | optional many-to-many contact/application link | explicit link or unlink |
| Contact | `application_contact_discovery_links` | optional many-to-many contact/discovery link | explicit link or unlink |
| Reusable answer | `reusable_answer_families` | reviewed answer identity, source mode, and current revision pointer | optimistic metadata revision; reversible archive |
| Reusable answer | `reusable_answer_aliases` | exact normalized question variants | add or retire |
| Reusable answer | `reusable_answer_revisions` | immutable literal answer values | append-only, trigger protected |
| Reusable answer | `reusable_answer_representations` | reviewed control- and option-specific mappings | add, revise metadata, or retire |
| Reusable answer | `reusable_answer_use_events` | fill, copy, revert, and failure history | append-only, trigger protected |

## 7. Stored non-SQL model UML

### 7.1 Resume source intake artifacts

The intake models are defined by
[`backend/internal/vaultread/intake_validation.go`](../../backend/internal/vaultread/intake_validation.go) and
[`backend/internal/vaultread/setup_import.go`](../../backend/internal/vaultread/setup_import.go).
Each new run keeps its source-specific artifacts under
`career_vault/intake_runs/<source-id>/`.

```mermaid
classDiagram
  class SourceMetadata {
    +id
    +path
    +file_type
    +sha256
  }
  class ExtractionReport {
    +schema_version
    +status extracted
    +extractor_version
    +registry_revision
  }
  class DocumentBlock {
    +id
    +order
    +kind
    +text
    +is_bullet
  }
  class SectionHint
  class TechnologyMention
  class TechnologyResolutionIssue
  class DateRangeCandidate
  class BulletCandidate
  class IntakeReview {
    +schema_version
    +status review
    +extraction_schema_version
  }
  class ReviewEntity {
    +id
    +entity_type
    +review_state
    +pending_skills
  }
  class BlockReview {
    +category
    +disposition
    +owner_id
  }
  class ReviewApprovalDecision {
    +subject_kind
    +action
    +target_id
    +field_resolutions
  }
  class ApprovalResult {
    +source_id
    +review_revision
    +vault_revisions
    +changed_files
  }
  class CanonicalVaultFiles {
    <<aggregate_set>>
  }

  ExtractionReport *-- SourceMetadata
  ExtractionReport *-- DocumentBlock
  ExtractionReport *-- SectionHint
  ExtractionReport *-- TechnologyMention
  ExtractionReport *-- TechnologyResolutionIssue
  ExtractionReport *-- DateRangeCandidate
  ExtractionReport *-- BulletCandidate
  IntakeReview *-- SourceMetadata
  IntakeReview *-- ReviewEntity
  IntakeReview *-- BlockReview
  IntakeReview *-- ReviewApprovalDecision
  ApprovalResult --> IntakeReview : audits
  ApprovalResult --> CanonicalVaultFiles : records applied changes
```

`extracted.yaml` is deterministic source analysis. `review.yaml` is an editable
draft. `approval_result.yaml` is an audit receipt. Only the rollback-capable
approval transaction may update canonical Vault files and private taxonomy.

### 7.2 Normalized job card

```mermaid
classDiagram
  class RetainedJD {
    <<immutable_source>>
    +raw_jd_path
    +content_sha256
  }
  class NormalizedJobCard {
    +id
    +status draft_or_ready
    +company
    +title
    +source_url
    +content_fingerprint
    +manual_overrides
  }
  class JobDigest {
    +analyzer_version
    +analysis_coverage
  }
  class ClassifiedRequirement {
    +kind
    +level
    +source_text
    +source_line
  }
  class SkillOccurrence {
    +competency_id
    +canonical_name
    +source_text
    +context
    +span
  }
  class ApplicationInstructionEnvelope {
    +schema_version
    +source_fingerprint
    +status analyzed
  }
  class ContactCandidateEnvelope {
    +schema_version
    +source_fingerprint
    +content_sha256
    +status analyzed
  }
  class CachedAssessment {
    <<derived_sqlite_record>>
    +job_fingerprint
    +candidate_revision
    +context_revision
  }

  RetainedJD --> NormalizedJobCard : source_for
  NormalizedJobCard *-- JobDigest
  JobDigest *-- ClassifiedRequirement
  JobDigest *-- SkillOccurrence
  JobDigest *-- ApplicationInstructionEnvelope
  JobDigest *-- ContactCandidateEnvelope
  NormalizedJobCard --> CachedAssessment : input_to
```

The card preserves source-backed analysis and explicit corrections. Mutable
instruction completion, contact identities, application stage, and assessment
resolution do not live in the card. A `ready` card requires enough reviewed
identity and requirement data; it does not mean “good fit” or “applied.”

### 7.3 Resume plan and application materials

The selection plan is JSON inside SQLite. The typed drafts are revisioned YAML
under `outputs/application_materials/<job-id>/`. Rendered files are derived.

```mermaid
classDiagram
  class ResumePlanRecord {
    <<sqlite>>
    +job_id
    +status
    +revision
    +plan_json
  }
  class ResumePlan {
    +job_id
    +status draft_or_approved
    +vault_revision
    +job_fingerprint
    +target_pages
  }
  class ResumeSectionPlan
  class ResumeExperiencePlan {
    +decision
    +presentation
    +target_bullets
    +proof_ids
  }
  class ResumeProjectPlan
  class ResumeSkillPlan
  class ResumeEducationPlan
  class ResumeMaterialStore {
    <<yaml>>
    +revision
    +updated_at
  }
  class ResumeDraft {
    +status draft_or_approved
    +job_fingerprint
    +plan_fingerprint
    +summary
  }
  class ResumeExperience
  class ResumeBullet {
    +text
    +evidence_refs
    +origin
  }
  class ApplicationMaterialStore {
    <<yaml>>
    +revision
    +updated_at
  }
  class CoverLetterDraft {
    +status draft_or_approved
    +job_fingerprint
    +profile_revision
    +evidence_revision
  }
  class CoverLetterContent
  class CoverLetterEvidenceItem {
    +claim
    +evidence_refs
    +manual_checks
  }
  class RenderedExport {
    <<derived>>
    +format docx_pdf_txt
  }
  class VaultEvidence {
    <<canonical_reference>>
  }

  ResumePlanRecord *-- ResumePlan
  ResumePlan *-- ResumeSectionPlan
  ResumePlan *-- ResumeExperiencePlan
  ResumePlan *-- ResumeProjectPlan
  ResumePlan *-- ResumeSkillPlan
  ResumePlan *-- ResumeEducationPlan
  ResumeMaterialStore *-- ResumeDraft
  ResumeDraft *-- ResumeExperience
  ResumeExperience *-- ResumeBullet
  ApplicationMaterialStore *-- CoverLetterDraft
  CoverLetterDraft *-- CoverLetterContent
  CoverLetterContent *-- CoverLetterEvidenceItem
  ResumePlan --> VaultEvidence : selects
  ResumeBullet --> VaultEvidence : cites
  CoverLetterEvidenceItem --> VaultEvidence : cites
  ResumeDraft --> RenderedExport : renders
  CoverLetterDraft --> RenderedExport : renders
```

An approved plan is a source selection, not a generated resume. A material
draft is application-only wording, not Career Vault evidence and not an
immutable Resume Version. An exported file becomes a Resume Version only
through a separate explicit save/select action.

## 8. State contracts

### 8.1 Discovery lifecycle

```mermaid
stateDiagram-v2
  [*] --> inbox
  inbox --> shortlisted : user shortlists
  shortlisted --> inbox : user returns to inbox
  inbox --> dismissed : user dismisses
  shortlisted --> dismissed : user dismisses
  dismissed --> inbox : user restores
  dismissed --> shortlisted : user restores as shortlisted
  inbox --> promoted : verified source and explicit promotion
  shortlisted --> promoted : verified source and explicit promotion
  promoted --> [*]
```

`promoted` is terminal and stores one unique `promoted_job_id`. Content
readiness is derived from retained source scope and verification; it is not a
second persisted lifecycle controlled by the user.

### 8.2 Application stages

```mermaid
flowchart TB
  ACTION["User records the truthful current stage"] --> VALIDATE["Validate one allowed stage"]
  VALIDATE --> SAVED["Saved / captured"]
  VALIDATE --> PREPARING["Preparing"]
  VALIDATE --> APPLIED["Applied"]
  VALIDATE --> INTERVIEW["Interview"]
  VALIDATE --> OFFER["Offer"]
  VALIDATE --> REJECTED["Rejected"]
  VALIDATE --> WITHDRAWN["Withdrawn"]
  VALIDATE --> CLOSED["Closed"]
  SAVED --> WRITE["Update current row and append dated status event"]
  PREPARING --> WRITE
  APPLIED --> WRITE
  INTERVIEW --> WRITE
  OFFER --> WRITE
  REJECTED --> WRITE
  WITHDRAWN --> WRITE
  CLOSED --> WRITE
```

The repository intentionally does not force a linear stage sequence. `reviewed`
is retained only for legacy compatibility and is not a normal browser write.
The first Applied/Interview/Offer recording also preserves the submitted-resume
snapshot without inventing a missing Applied event.

### 8.3 Attended application session

```mermaid
stateDiagram-v2
  [*] --> pending_claim
  pending_claim --> armed : exact URL claimed by one tab and Origin
  pending_claim --> expired : timeout
  pending_claim --> cancelled : replaced or stopped
  armed --> submission_detected : trusted submit plus supported success signal
  armed --> interrupted : tab or instrumentation boundary lost
  armed --> expired : timeout
  armed --> cancelled : stopped
  interrupted --> cancelled : user clears ended session
  submission_detected --> confirmed_applied : user chooses Mark Applied
  submission_detected --> dismissed : user chooses Not submitted
  confirmed_applied --> [*]
  dismissed --> [*]
  expired --> [*]
  cancelled --> [*]
```

The signal is a review request, not proof by itself. Only
`confirmed_applied` delegates to the normal application-stage transition.

### 8.4 Resume intake approval

These are workflow milestones derived from files and validation, not a single
database status column.

```mermaid
stateDiagram-v2
  [*] --> raw_source
  raw_source --> extracted : deterministic extraction
  extracted --> review_incomplete : create review inventory
  review_incomplete --> review_incomplete : save card and block decisions
  review_incomplete --> approval_ready : all blocks resolved and valid
  approval_ready --> decisions_saved : save field-level approval decisions
  decisions_saved --> review_incomplete : revise review or decisions
  decisions_saved --> canonical_applied : explicit confirmed apply
  canonical_applied --> read_only_audit : write approval result
  read_only_audit --> [*]
```

Repeated analysis creates a new run; it never overwrites an older review or
approved Career Vault state.

## 9. Workflow UML

### 9.1 End-to-end human workflow

```mermaid
flowchart LR
  CV["Maintain reviewed Career Vault"] --> SOURCE["Obtain one job source"]
  SOURCE --> DIRECT["Direct Add job with complete JD"]
  SOURCE --> DISC["Job Feed discovery from email, extension, or link"]
  DISC --> NEEDS["Needs JD"]
  NEEDS --> ENRICH["Explicit public resolve or attended extension capture"]
  ENRICH --> VERIFY["User verifies complete retained JD"]
  VERIFY --> PROMOTE["Explicit Save job or Record as applied"]
  DIRECT --> INTAKE["Retain JD and build normalized job card"]
  PROMOTE --> INTAKE
  INTAKE --> LEDGER["Register one application ledger row"]
  LEDGER --> ASSESS["Compute advisory deterministic assessment"]
  ASSESS --> PREP["Review and prepare"]
  PREP --> PLAN["Optional approved Resume Plan"]
  PLAN --> DRAFT["Optional typed resume or cover-letter draft"]
  DRAFT --> EXPORT["Local DOCX, PDF, or TXT export"]
  PREP --> EXTERNAL["User completes external application"]
  EXPORT --> EXTERNAL
  EXTERNAL --> RECORD["User records Applied or reviews attended signal"]
  RECORD --> HISTORY["Append status and submitted-resume history"]
```

Every match and blocker conclusion is advisory. The user can continue, save,
or record an application regardless of the assessment. Final attachment,
submission, and visual document review remain human actions.

### 9.2 Resume source to canonical Career Vault

```mermaid
sequenceDiagram
  actor User
  participant UI as Browser UI
  participant Source as Private source store
  participant Extractor as Deterministic extractor
  participant Review as Review service
  participant Approval as Approval transaction
  participant Vault as Canonical Vault YAML
  participant Registry as Private taxonomy

  User->>UI: Choose Update Career Vault and one TXT or DOCX
  UI->>Source: Store unique raw source
  UI->>Extractor: Extract exact blocks and source-backed signals
  Extractor->>Source: Write extracted.yaml
  UI->>Review: Build or reopen review.yaml
  Review-->>UI: Cards, block ownership, unresolved items, revision
  loop Until review and field decisions are complete
    User->>UI: Edit cards, links, and approval choices
    UI->>Review: Validate and save revisioned review.yaml
  end
  User->>UI: Confirm Apply to Career Vault
  UI->>Approval: Apply exact saved decisions
  Approval->>Vault: Validate complete in-memory Vault
  Approval->>Registry: Validate any explicitly selected new skills
  Approval->>Vault: Atomically replace changed YAML
  Approval->>Registry: Commit private vocabulary in same rollback boundary
  Approval->>Source: Write approval_result.yaml
  Approval-->>UI: Changed-file and revision receipt
  Note over Extractor,Review: Extraction and review never mutate canonical facts
```

### 9.3 Job Feed capture, verification, and promotion

```mermaid
sequenceDiagram
  actor User
  participant Producer as Extension, email import, or email sync
  participant API as Bounded local adapter
  participant Discovery as Discovery domain
  participant Files as Discovery source store
  participant DB as SQLite
  participant Preview as Deterministic preview
  participant Intake as Existing JD intake
  participant Ledger as Application ledger

  User->>Producer: Explicit capture, import, or sync action
  Producer->>API: Bounded source metadata and optional content
  API->>Discovery: Upsert by posting id, URL, or exact content
  Discovery->>DB: Append observation and revise effective discovery
  opt Retained text is present
    Discovery->>Files: Atomically retain exact source snapshot
  end
  alt Link-only or partial content
    Discovery-->>Producer: Needs JD - assessment unavailable
  else Complete-enough retained content
    Discovery->>Preview: Analyze retained source
    Preview->>DB: Store current preview keyed by revisions
    Discovery-->>Producer: Bounded readiness and blocker counts
  end
  Producer-->>User: Open exact local Discovery review
  User->>API: Correct identity or confirm complete JD
  API->>Discovery: Revision-guarded update
  User->>API: Explicit Save job or Record as applied
  API->>Discovery: Revalidate state, source, and content verification
  Discovery->>Intake: Reuse immutable JD intake with idempotency key
  Intake->>Ledger: Register job and current assessment
  Ledger-->>Discovery: Stable job id
  Discovery->>DB: Mark promoted with unique job id
  API-->>User: Open exact Job Workspace record
```

Capturing, opening, correcting, shortlisting, dismissing, or previewing never
creates an application. Promotion happens only after complete-source
verification and a separate user confirmation.

### 9.4 Email: one transport, two typed outputs

```mermaid
sequenceDiagram
  actor User
  participant Sync as Manual or opt-in local sync
  participant Mailbox as Dedicated Gmail over IMAP TLS
  participant Policy as Exact sender policy
  participant Parser as Bounded MIME parser
  participant Discovery as Discovery service
  participant Outcomes as Email outcome service
  participant Ledger as Application ledger
  participant UI as Career Hub UI

  User->>Sync: Import EML or choose Sync now
  Sync->>Mailbox: Read UID and selected headers without mutation
  Sync->>Policy: Check exact sender and stream purpose
  alt Sender is unknown or disabled
    Policy-->>Sync: Retain minimized header receipt only
    Sync-->>UI: New sender review - no body-derived record
  else Sender is allowed
    Sync->>Mailbox: Fetch MIME body read-only
    Sync->>Parser: Parse within message, link, and candidate bounds
    Parser-->>Sync: Opportunity candidates and application outcomes
    loop Each opportunity candidate allowed by sender purpose
      Sync->>Discovery: Idempotent discovery upsert
    end
    loop Each supported application outcome
      Sync->>Outcomes: Store minimized idempotent outcome
      alt Exact provider posting id matches one safe saved application
        Outcomes->>Ledger: Apply conservative normal status transition
        Ledger-->>Outcomes: Updated, already current, or conflict
      else No exact safe match
        Outcomes-->>UI: Keep unlinked for attended review
      end
    end
    Sync-->>UI: Sanitized counts and stable result codes
  end
  UI-->>User: Feed shows opportunities
  UI-->>User: Applications / Email tracking shows outcomes
```

This split is the key Job Feed/email rule. A LinkedIn rejection message may
produce one rejection outcome and separate recommended-job discoveries. A
Handshake confirmation may produce one unlinked Applied outcome and separate
recommendations. Company-only confirmation never creates or guesses a role,
job, application, or contact. The connector does not mark mail read, move it,
delete it, send it, or retain the App Password in SQLite.

### 9.5 Assessment, preparation, materials, and application history

```mermaid
sequenceDiagram
  actor User
  participant Workspace as Job Workspace
  participant Ledger as Application ledger
  participant Assessment as Review and assessment service
  participant Vault as Career Vault
  participant Plan as Resume Plan service
  participant Materials as Typed material service
  participant Renderer as Local renderer
  participant Employer as External application site

  User->>Workspace: Open one saved job
  Workspace->>Ledger: Load application and assessment key
  alt Assessment inputs are unchanged
    Ledger-->>Workspace: Return cached advisory assessment
  else JD, Vault, policy, catalog, or evaluation context changed
    Workspace->>Assessment: Recompute exact deterministic review
    Assessment->>Vault: Read active evidence and confirmed facts
    Assessment->>Ledger: Replace current assessment cache
    Assessment-->>Workspace: Evidence, gaps, manual review, advisory verdict
  end
  opt Tailored resume is needed
    User->>Plan: Select source-backed sections and evidence
    Plan->>Ledger: Save draft or approved revisioned plan
    User->>Materials: Edit only allowed application wording
    Materials->>Vault: Validate cited current evidence
    Materials->>Renderer: Render approved current draft
    Renderer-->>User: Local DOCX, PDF, or TXT
  end
  User->>Employer: Review, attach, and submit manually
  User->>Workspace: Record truthful stage and submitted Resume Version
  Workspace->>Ledger: Append status and resume history
```

The Resume Version picker, Resume Plan, material draft, rendered export, and
submitted-resume event are separate records. Selecting or rendering a file does
not imply it was submitted.

### 9.6 Application helper and attended submission tracking

```mermaid
sequenceDiagram
  actor User
  participant Extension as Browser extension
  participant API as Purpose-specific local API
  participant Helper as Helper domain
  participant Page as Current application page
  participant Sessions as Application session domain
  participant Ledger as Application ledger

  User->>Extension: Open Application helper
  Extension->>API: Bootstrap helper capability for exact Origin
  API->>Helper: Build enabled, present value projection
  Helper-->>Extension: Masked shelf metadata in memory
  User->>Extension: Review profile fields on page
  Extension->>Page: Inspect supported visible empty controls
  Page-->>Extension: Labels, kinds, and opaque targets only
  Extension-->>User: Review proposals without values
  User->>Extension: Fill selected fields
  Extension->>API: Redeem one-shot review token and selected mapping ids
  API->>Helper: Re-fetch current projection and revision
  Extension->>Page: Revalidate all targets, then fill atomically
  Note over Extension,Page: No navigation, upload, consent, or submit click

  User->>Extension: Track submission on exact saved-job URL
  Extension->>Sessions: Claim one URL, tab, and Origin-bound session
  Sessions-->>Extension: Ephemeral per-session capability
  Extension->>Sessions: Send paired native-submit and visible-success signal
  Sessions-->>User: Queue submission confirmation
  alt User chooses Mark Applied
    User->>Sessions: Confirm current revision
    Sessions->>Ledger: Reuse normal Applied transition
  else User dismisses or tracking is interrupted
    Sessions-->>Ledger: No stage change
  end
```

Helper values remain outside extension storage. Passwords, one-time codes,
financial data, uploads, consent, open-ended answers, demographic controls, and
non-empty controls are outside reviewed prefill. Explicitly enabled voluntary
US demographic answers remain available only through one-field Copy. Submission tracking stores
only minimal rule and timestamp evidence, never DOM, form values, cookies,
credentials, or screenshots.

### 9.7 Desktop sidecar lifecycle

```mermaid
sequenceDiagram
  actor User
  participant Host as Electron host
  participant Sidecar as Packaged Go sidecar
  participant DataHome as Selected Data Home
  participant API as Loopback server
  participant Window as Electron renderer

  User->>Host: Launch Career Hub
  Host->>Sidecar: Spawn exactly one owned child with only sidecar flag
  Host->>Sidecar: Send bounded versioned start record over stdin
  Sidecar->>DataHome: Validate resource/data separation and initialize schemas
  Sidecar->>API: Bind OS-assigned 127.0.0.1 port
  Sidecar-->>Host: Emit one path-free ready record with port
  Host->>API: Verify minimal health contract
  Host->>Window: Load only the constructed owned loopback origin
  Window->>API: Obtain separate memory-only browser session capability
  User->>Host: Quit
  Host->>Sidecar: Send versioned shutdown record
  Sidecar->>API: Stop listener
  Sidecar-->>Host: Exit cleanly
  Note over Host,Sidecar: Parent-pipe closure also stops the child
```

Resource paths, data paths, tokens, and ports do not travel in process
arguments or inherited environment variables. The desktop wrapper is a host,
not another backend or synchronization layer.

### 9.8 Agent-assisted application workflow

```mermaid
sequenceDiagram
  actor User
  participant Skill as Career Hub repo skill
  participant CLI as agent_workflow CLI
  participant Domain as Existing deterministic services
  participant Agent as Language-model reasoning
  participant Materials as Typed material service
  participant Renderer as Local renderer
  participant Ledger as Application ledger

  User->>Skill: Ask for assessment, resume delta, or application text
  Skill->>CLI: Invoke the matching bounded command and typed task request
  CLI->>Domain: Resolve one Data Home and current source revisions
  Domain-->>CLI: Purpose-specific evidence and context
  CLI-->>Agent: Capped packet, fingerprints, limits, and no-forcing policy
  Agent-->>User: Evidence-backed judgment or copy-ready text
  opt User requested a durable resume or cover-letter artifact
    Agent->>CLI: Save typed material draft
    CLI->>Materials: Validate allowed fields and evidence references
    Agent->>CLI: Render requested format
    CLI->>Renderer: Render and structurally validate current draft
    Renderer-->>User: Local artifact
  end
  opt User confirms an external submission already happened
    Agent->>CLI: record-submission with explicit confirmation
    CLI->>Ledger: Append truthful application history
  end
```

The adapter performs no model call itself. Ephemeral `analyze` writes nothing;
`prepare` stops at Preparing; saving a draft does not attach, submit, or mark
Applied. Agents should use the bounded packet and open a source artifact only
for a specific unresolved fact.

## 10. Read-only evidence, demand, and coverage projections

```mermaid
flowchart LR
  VAULT["Active Career Vault records with explicit competency_ids"]
  REGISTRY["Effective core plus private Skills Registry"]
  JOBS["Validated normalized job cards"]
  LEDGER["Saved-job ledger rows and captured dates"]

  VAULT --> GRAPH["Candidate Evidence Graph"]
  REGISTRY --> GRAPH
  GRAPH --> ANALYTICS["Evidence Analytics"]

  JOBS --> DEMAND["JD Demand Index"]
  LEDGER --> DEMAND
  REGISTRY --> DEMAND

  ANALYTICS --> COVERAGE["Coverage Matrix"]
  DEMAND --> COVERAGE
  COVERAGE --> GAPS["Demand and coverage browser projection"]
  GRAPH --> EXPLORER["Competency Explorer projection"]
  REGISTRY --> EXPLORER

  GAPS --> UI1["Jobs / Demand and coverage"]
  EXPLORER --> UI2["Career Vault / Skills / All skills"]
```

These JSON models are calculated on demand and carry deterministic revisions
plus `read_only: true`. They are not caches, graph-database records, scores, or
Career Vault writers. The demand index counts only validated normalized cards
already present in the application ledger and deduplicates non-empty JD
fingerprints. Coverage uses only `supported`, `thin_evidence`,
`evidence_not_found`, and `not_requested`; absence means only **Evidence not
found in Career Vault.**

## 11. Human and system responsibility matrix

| Workflow | User-owned decision | Deterministic system responsibility | Must never be inferred |
| --- | --- | --- | --- |
| Career Vault edit | whether a fact is true and should be saved | validate complete affected aggregate, preserve metadata, reject stale writes | a missing career fact or credential |
| Resume source intake | card ownership and exact field-level approval | preserve source, extract bounded signals, apply only saved decisions | automatic canonical promotion |
| Job discovery | shortlist/dismiss, source verification, promotion intent | dedupe conservatively, retain provenance, block incomplete promotion | that a link/snippet is a complete JD |
| Email intake | sender allowlist, stream purpose, optional outcome link | receive-only parse, split opportunities/outcomes, keep idempotent receipts | role/application identity from company alone |
| Job assessment | whether to proceed and how to resolve findings | exact-source deterministic analysis with current candidate facts | a workflow prohibition or overall fit score |
| Resume Plan | source evidence and section selection | enforce current references and approval completeness | generated prose or submitted file |
| Material draft | final factual wording and visual approval | constrain fields, validate evidence, render locally | Career Vault mutation or external attachment |
| Application helper | each Copy, Review, Fill, or answer-bank action | purpose-limit values, revalidate targets, keep tokens one-shot | consent, credentials, sensitive answers, navigation, or submit |
| Submission tracking | whether the observed result means Applied | bind exact URL/tab/Origin and retain minimal evidence | an automatic stage change |
| Contact ledger | create/link choice and stated-field corrections | revalidate exact JD candidate, preserve provenance, prevent silent merge | identity enrichment or relationship strength |
| Agent workflow | requested outcome, artifact intent, and submission confirmation | return bounded source-backed context and validate writes | edits for their own sake, invented evidence, or ambient submission |

## 12. Security and privacy invariants

1. Mutable candidate data belongs only in the selected private Data Home;
   packaged resources and the Git checkout are a separate boundary.
2. The server binds to `127.0.0.1` and validates exact Host and Origin rules.
   Browser, capture, helper, and session capabilities have distinct scopes.
3. Capability tokens, extension origins, helper values, and review tokens do
   not enter logs, URLs, build artifacts, or persistent extension storage.
4. Gmail access is read-only IMAP TLS for an explicitly configured dedicated
   account. The revocable App Password is stored only in Credential Manager or
   Keychain; SMTP and whole-inbox processing do not exist.
5. General YAML and SQLite are protected by the Data Home's OS-account access
   boundary. The Documents ledger and blobs additionally use authenticated
   AES-256-GCM envelopes whose random master key is held by the OS secret store.
6. Recovery ZIPs are local, unencrypted artifacts protected by Data Home access
   controls. They exclude secrets, earlier backups, scanner reports, and
   staging directories. Cross-device portable document recovery is not
   implemented.
7. Operational logs retain bounded counts, timings, ids, and stable error
   codes, not resume blocks, JD bodies, form values, message subjects, secrets,
   or environment dumps.
8. External network actions are explicit and narrow: Gmail sync, public JD
   resolution, and opening a posting. Local workflows remain usable offline,
   and reconnect never silently replays a network action.
9. No application module calls an AI API. No workflow scrapes protected pages,
   automatically attaches a file, submits a form, or silently sets Applied.

## 13. Current capability and release boundary

| Area | Current implemented boundary | Not yet a supported release claim |
| --- | --- | --- |
| Local browser application | primary development and dogfood product, backed by the selected Data Home | hosted web or multi-device product |
| Browser extension | one source-compatible WebExtensions codebase; Chrome, Edge, and Firefox desktop packages have automated compatibility coverage | mobile browsers; best-effort Chromium variants without their own matrix |
| Safari extension | generated source-compatible Safari package and dedicated helper-tab fallback | signed/released Safari extension before live macOS/Xcode validation |
| Electron desktop | locked host, measured packaged sidecar, lifecycle and security foundation | signed Windows/macOS installers until native release gates pass |
| Job Feed | extension, explicit EML import, read-only Gmail sync, and public-link enrichment | recurring website watchers or protected-page crawling |
| Application assistance | explicit quick copy, reviewed safe prefill, reusable safe answers, attended submission signal | unattended filling, sensitive answers, uploads, or submission automation |
| Agent assistance | local deterministic adapter and repo skills around bounded context | an in-product AI provider or autonomous application agent |

## 14. Change-impact and maintenance map

Update this atlas in the same change when any of these contracts changes.

| Change trigger | Atlas sections to review | Focused authority |
| --- | --- | --- |
| `SCHEMA_VERSION`, a SQLite table, foreign key, trigger guard, or persisted state | Physical ERD, table index, state diagrams | [`backend/internal/applications/schema/steps_generated.go`](../../backend/internal/applications/schema/steps_generated.go) and owning domain module |
| Career Vault file, required field, evidence reference, or archive rule | Storage topology and conceptual Vault ERD | [`backend/internal/vaultread/validate.go`](../../backend/internal/vaultread/validate.go), [`schema_reference.md`](schema_reference.md) |
| normalized-job field or derived envelope | conceptual job model and job-card UML | [`backend/internal/jobcard/validate.go`](../../backend/internal/jobcard/validate.go) |
| intake review or approval contract | intake class and workflow diagrams | typed intake models and approval modules |
| Resume Plan or material draft contract | plan/material class diagram and preparation sequence | `src/resume_plan.py`, `src/resume_materials.py`, `src/application_materials.py` |
| Discovery, email, session, or application transition | corresponding state and sequence diagrams | owning domain module plus focused tests |
| route, capability, sidecar, secret, or process boundary | system context, component UML, privacy invariants | route contracts, sidecar protocol, desktop tests |
| agent command, task packet, or repo-skill mapping | agent workflow sequence and responsibility matrix | [`docs/guides/agent_workflows.md`](../guides/agent_workflows.md) and skill contracts |
| release support statement | capability/release boundary | compatibility matrix and native release gates |

The architecture contract test checks that the atlas covers the current SQLite
table set and schema version, includes all diagram families, and remains linked
from the two primary entry documents. It cannot prove that prose is
semantically current; reviewers must still compare behavior with code and
focused tests.

Run the focused documentation checks with:

```powershell
python -m unittest tests.test_architecture_atlas -v
python scripts/check_public_tree.py
```

When an agent adapter, mapped domain service, or repo skill changes, also run:

```powershell
python -m scripts.agent_workflow_audit --project-root . --changed --pretty
```

## 15. Fast reading paths

- A user trying to understand what happens to their data should read
  [System context and trust boundaries](#1-system-context-and-trust-boundaries),
  [Storage topology and ownership](#3-storage-topology-and-ownership), and the
  [end-to-end workflow](#91-end-to-end-human-workflow).
- An agent starting repository work should read the component UML, both
  conceptual models, the workflow relevant to its task, and the
  [responsibility matrix](#11-human-and-system-responsibility-matrix).
- A maintainer changing persistence should begin with the physical ERD and
  complete table index, then use the change-impact map before editing.
- A release reviewer should read the trust boundaries, security invariants,
  current release boundary, and the relevant manual acceptance section.
