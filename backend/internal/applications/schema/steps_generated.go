// This file is the hand-maintained owner of the application SQLite schema.
// Every step is a fixed, reviewed statement list; nothing here is generated or
// translated at runtime. Append a new numbered step for every schema change and
// raise Version in steps.go: an existing step is never edited, except that an
// early CREATE TABLE may already contain a column that a later step also adds
// idempotently through columnAction (v14 and v17 do this for v20 and v25).
// TestStepwiseUpgradeMatchesFreshSchema keeps those two paths equivalent.

package schema

var migrations = [...][]action{
	{ // Version 1
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS job_applications (
                    job_id TEXT PRIMARY KEY,
                    company TEXT NOT NULL,
                    title TEXT NOT NULL,
                    source_url TEXT NOT NULL,
                    content_fingerprint TEXT NOT NULL,
                    decision TEXT CHECK (
                        decision IS NULL OR decision IN ('apply', 'consider', 'skip')
                    ),
                    current_status TEXT NOT NULL CHECK (
                        current_status IN (
                            'captured', 'reviewed', 'preparing', 'applied',
                            'interview', 'offer', 'rejected', 'withdrawn', 'closed'
                        )
                    ),
                    notes TEXT NOT NULL DEFAULT '',
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );

                CREATE TABLE IF NOT EXISTS application_status_events (
                    event_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    job_id TEXT NOT NULL
                        REFERENCES job_applications(job_id) ON DELETE CASCADE,
                    status TEXT NOT NULL CHECK (
                        status IN (
                            'captured', 'reviewed', 'preparing', 'applied',
                            'interview', 'offer', 'rejected', 'withdrawn', 'closed'
                        )
                    ),
                    occurred_at TEXT NOT NULL,
                    recorded_at TEXT NOT NULL,
                    note TEXT NOT NULL DEFAULT ''
                );

                CREATE INDEX IF NOT EXISTS application_status_events_job_time
                    ON application_status_events(job_id, occurred_at, event_id);

                CREATE INDEX IF NOT EXISTS job_applications_status_updated
                    ON job_applications(current_status, updated_at);

                PRAGMA user_version = 1;`},
	},
	{ // Version 2
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS application_resume_attachments (
                    job_id TEXT PRIMARY KEY
                        REFERENCES job_applications(job_id) ON DELETE CASCADE,
                    relative_path TEXT NOT NULL,
                    file_name TEXT NOT NULL,
                    label TEXT NOT NULL,
                    sha256 TEXT NOT NULL,
                    size_bytes INTEGER NOT NULL CHECK (size_bytes > 0),
                    attached_at TEXT NOT NULL
                );

                PRAGMA user_version = 2;`},
	},
	{ // Version 3
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS application_resume_plans (
                    job_id TEXT PRIMARY KEY
                        REFERENCES job_applications(job_id) ON DELETE CASCADE,
                    plan_json TEXT NOT NULL,
                    status TEXT NOT NULL CHECK (status IN ('draft', 'approved')),
                    revision INTEGER NOT NULL CHECK (revision > 0),
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );

                PRAGMA user_version = 3;`},
	},
	{ // Version 4
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS job_assessments (
                    job_id TEXT PRIMARY KEY
                        REFERENCES job_applications(job_id) ON DELETE CASCADE,
                    job_fingerprint TEXT NOT NULL,
                    job_analyzer_version INTEGER NOT NULL CHECK (job_analyzer_version >= 0),
                    candidate_revision TEXT NOT NULL,
                    assessment_context_revision TEXT NOT NULL,
                    assessment_version INTEGER NOT NULL CHECK (assessment_version > 0),
                    assessment_json TEXT NOT NULL,
                    computed_at TEXT NOT NULL
                );

                PRAGMA user_version = 4;`},
	},
	{ // Version 5
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS resume_library (
                    resume_id TEXT PRIMARY KEY,
                    display_name TEXT NOT NULL,
                    relative_path TEXT NOT NULL UNIQUE,
                    file_name TEXT NOT NULL,
                    sha256 TEXT NOT NULL,
                    size_bytes INTEGER NOT NULL CHECK (size_bytes > 0),
                    added_at TEXT NOT NULL
                );

                CREATE INDEX IF NOT EXISTS resume_library_display_name
                    ON resume_library(display_name COLLATE NOCASE, added_at DESC);`},
		{kind: columnAction, table: `application_resume_attachments`, column: `resume_id`, sql: `ALTER TABLE application_resume_attachments ADD COLUMN resume_id TEXT`},
		{kind: columnAction, table: `job_applications`, column: `applied_resume_id`, sql: `ALTER TABLE job_applications ADD COLUMN applied_resume_id TEXT`},
		{kind: columnAction, table: `job_applications`, column: `applied_resume_recorded`, sql: `ALTER TABLE job_applications ADD COLUMN applied_resume_recorded INTEGER NOT NULL DEFAULT 0`},
		{kind: sqlAction, sql: `INSERT OR IGNORE INTO resume_library (
                    resume_id, display_name, relative_path, file_name, sha256,
                    size_bytes, added_at
                )
                SELECT
                    'legacy_' || job_id,
                    label,
                    relative_path,
                    file_name,
                    sha256,
                    size_bytes,
                    attached_at
                FROM application_resume_attachments;

                UPDATE application_resume_attachments
                SET resume_id = 'legacy_' || job_id
                WHERE resume_id IS NULL;

                UPDATE job_applications
                SET
                    applied_resume_id = (
                        SELECT resume_id
                        FROM application_resume_attachments
                        WHERE application_resume_attachments.job_id = job_applications.job_id
                    ),
                    applied_resume_recorded = 1
                WHERE current_status IN (
                    'applied', 'interview', 'offer', 'rejected', 'withdrawn', 'closed'
                );

                PRAGMA user_version = 5;`},
	},
	{ // Version 6
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS application_applied_resume_events (
                    event_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    job_id TEXT NOT NULL
                        REFERENCES job_applications(job_id) ON DELETE CASCADE,
                    resume_id TEXT REFERENCES resume_library(resume_id),
                    action TEXT NOT NULL CHECK (
                        action IN ('initial_snapshot', 'recorded', 'corrected')
                    ),
                    occurred_at TEXT NOT NULL,
                    recorded_at TEXT NOT NULL,
                    note TEXT NOT NULL DEFAULT ''
                );

                CREATE INDEX IF NOT EXISTS application_applied_resume_events_job_time
                    ON application_applied_resume_events(job_id, event_id);

                INSERT INTO application_applied_resume_events (
                    job_id, resume_id, action, occurred_at, recorded_at, note
                )
                SELECT
                    applications.job_id,
                    applications.applied_resume_id,
                    'initial_snapshot',
                    COALESCE(
                        (
                            SELECT events.occurred_at
                            FROM application_status_events AS events
                            WHERE events.job_id = applications.job_id
                              AND events.status = 'applied'
                            ORDER BY events.event_id ASC
                            LIMIT 1
                        ),
                        applications.updated_at
                    ),
                    applications.updated_at,
                    ''
                FROM job_applications AS applications
                WHERE applications.applied_resume_recorded = 1;

                PRAGMA user_version = 6;`},
	},
	{ // Version 7
		{kind: columnAction, table: `resume_library`, column: `archived_at`, sql: `ALTER TABLE resume_library ADD COLUMN archived_at TEXT`},
		{kind: sqlAction, sql: `CREATE INDEX IF NOT EXISTS resume_library_active_display_name
                    ON resume_library(
                        archived_at,
                        display_name COLLATE NOCASE,
                        added_at DESC,
                        resume_id
                    );

                PRAGMA user_version = 7;`},
	},
	{ // Version 8
		{kind: sqlAction, sql: `CREATE INDEX IF NOT EXISTS application_resume_attachments_resume_id
                    ON application_resume_attachments(resume_id)
                    WHERE resume_id IS NOT NULL;

                CREATE INDEX IF NOT EXISTS job_applications_applied_resume_id
                    ON job_applications(applied_resume_id)
                    WHERE applied_resume_id IS NOT NULL;

                CREATE INDEX IF NOT EXISTS application_applied_resume_events_resume_id_action
                    ON application_applied_resume_events(resume_id, action)
                    WHERE resume_id IS NOT NULL;

                PRAGMA user_version = 8;`},
	},
	{ // Version 9
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS approved_import_resume_versions (
                    source_id TEXT PRIMARY KEY,
                    source_sha256 TEXT NOT NULL,
                    resume_id TEXT NOT NULL UNIQUE
                        REFERENCES resume_library(resume_id) ON DELETE CASCADE,
                    created_at TEXT NOT NULL
                );

                PRAGMA user_version = 9;`},
	},
	{ // Version 10
		{kind: noRowsAction, sql: `SELECT 1
                FROM application_resume_attachments AS attachments
                LEFT JOIN resume_library AS library
                  ON library.resume_id = attachments.resume_id
                WHERE attachments.resume_id IS NOT NULL
                  AND library.resume_id IS NULL
                UNION ALL
                SELECT 1
                FROM job_applications AS applications
                LEFT JOIN resume_library AS library
                  ON library.resume_id = applications.applied_resume_id
                WHERE applications.applied_resume_id IS NOT NULL
                  AND library.resume_id IS NULL
                UNION ALL
                SELECT 1
                FROM application_applied_resume_events AS events
                LEFT JOIN resume_library AS library
                  ON library.resume_id = events.resume_id
                WHERE events.resume_id IS NOT NULL
                  AND library.resume_id IS NULL
                UNION ALL
                SELECT 1
                FROM approved_import_resume_versions AS imports
                LEFT JOIN resume_library AS library
                  ON library.resume_id = imports.resume_id
                WHERE library.resume_id IS NULL
                LIMIT 1`},
		{kind: sqlAction, sql: `CREATE TRIGGER IF NOT EXISTS resume_attachment_insert_guard
                BEFORE INSERT ON application_resume_attachments
                WHEN NEW.resume_id IS NOT NULL
                  AND NOT EXISTS (
                      SELECT 1 FROM resume_library
                      WHERE resume_id = NEW.resume_id
                  )
                BEGIN
                    SELECT RAISE(ABORT, 'selected resume does not exist');
                END;

                CREATE TRIGGER IF NOT EXISTS resume_attachment_update_guard
                BEFORE UPDATE OF resume_id ON application_resume_attachments
                WHEN NEW.resume_id IS NOT NULL
                  AND NOT EXISTS (
                      SELECT 1 FROM resume_library
                      WHERE resume_id = NEW.resume_id
                  )
                BEGIN
                    SELECT RAISE(ABORT, 'selected resume does not exist');
                END;

                CREATE TRIGGER IF NOT EXISTS applied_resume_insert_guard
                BEFORE INSERT ON job_applications
                WHEN NEW.applied_resume_id IS NOT NULL
                  AND NOT EXISTS (
                      SELECT 1 FROM resume_library
                      WHERE resume_id = NEW.applied_resume_id
                  )
                BEGIN
                    SELECT RAISE(ABORT, 'applied resume does not exist');
                END;

                CREATE TRIGGER IF NOT EXISTS applied_resume_update_guard
                BEFORE UPDATE OF applied_resume_id ON job_applications
                WHEN NEW.applied_resume_id IS NOT NULL
                  AND NOT EXISTS (
                      SELECT 1 FROM resume_library
                      WHERE resume_id = NEW.applied_resume_id
                  )
                BEGIN
                    SELECT RAISE(ABORT, 'applied resume does not exist');
                END;

                CREATE TRIGGER IF NOT EXISTS resume_library_delete_reference_guard
                BEFORE DELETE ON resume_library
                WHEN EXISTS (
                    SELECT 1 FROM application_resume_attachments
                    WHERE resume_id = OLD.resume_id
                ) OR EXISTS (
                    SELECT 1 FROM job_applications
                    WHERE applied_resume_id = OLD.resume_id
                ) OR EXISTS (
                    SELECT 1 FROM application_applied_resume_events
                    WHERE resume_id = OLD.resume_id
                )
                BEGIN
                    SELECT RAISE(ABORT, 'resume version in use');
                END;

                PRAGMA user_version = 10;`},
	},
	{ // Version 11
		{kind: columnAction, table: `resume_library`, column: `origin`, sql: `ALTER TABLE resume_library
                    ADD COLUMN origin TEXT NOT NULL DEFAULT 'uploaded'
                    CHECK (origin IN ('uploaded', 'generated'))`},
		{kind: sqlAction, sql: `UPDATE resume_library SET origin = 'uploaded' WHERE origin IS NULL`},
		{kind: sqlAction, sql: `PRAGMA user_version = 11`},
	},
	{ // Version 12
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS job_assessment_resolution_events (
                    event_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    job_id TEXT NOT NULL
                        REFERENCES job_applications(job_id) ON DELETE CASCADE,
                    finding_key TEXT NOT NULL,
                    assessment_revision TEXT NOT NULL,
                    resolution TEXT NOT NULL CHECK (
                        resolution IN (
                            'confirmed', 'resolved', 'not_applicable', 'accepted_risk'
                        )
                    ),
                    note TEXT NOT NULL DEFAULT '',
                    recorded_at TEXT NOT NULL
                );

                CREATE INDEX IF NOT EXISTS job_assessment_resolution_latest
                    ON job_assessment_resolution_events(job_id, finding_key, event_id DESC);

                PRAGMA user_version = 12;`},
	},
	{ // Version 13
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS job_discoveries (
                    discovery_id TEXT PRIMARY KEY,
                    state TEXT NOT NULL CHECK (
                        state IN ('inbox', 'shortlisted', 'dismissed', 'promoted')
                    ),
                    title TEXT NOT NULL DEFAULT '',
                    company TEXT NOT NULL DEFAULT '',
                    location TEXT NOT NULL DEFAULT '',
                    observed_url TEXT NOT NULL DEFAULT '',
                    canonical_url TEXT NOT NULL DEFAULT '',
                    posting_identity TEXT NOT NULL DEFAULT '',
                    content_scope TEXT NOT NULL CHECK (
                        content_scope IN (
                            'link_only', 'selection', 'page_visible', 'confirmed_full'
                        )
                    ),
                    source_relative_path TEXT,
                    content_sha256 TEXT NOT NULL DEFAULT '',
                    content_chars INTEGER NOT NULL DEFAULT 0 CHECK (content_chars >= 0),
                    first_seen_at TEXT NOT NULL,
                    last_seen_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    promoted_job_id TEXT UNIQUE
                        REFERENCES job_applications(job_id),
                    promoted_at TEXT,
                    revision INTEGER NOT NULL CHECK (revision > 0),
                    CHECK (
                        (source_relative_path IS NULL AND content_sha256 = '' AND content_chars = 0)
                        OR
                        (source_relative_path IS NOT NULL AND length(content_sha256) = 64
                         AND content_chars > 0)
                    ),
                    CHECK (
                        (state = 'promoted' AND promoted_job_id IS NOT NULL
                         AND promoted_at IS NOT NULL)
                        OR
                        (state <> 'promoted' AND promoted_job_id IS NULL
                         AND promoted_at IS NULL)
                    )
                );

                CREATE UNIQUE INDEX IF NOT EXISTS job_discoveries_posting_identity
                    ON job_discoveries(posting_identity)
                    WHERE posting_identity <> '';

                CREATE INDEX IF NOT EXISTS job_discoveries_canonical_url
                    ON job_discoveries(canonical_url)
                    WHERE canonical_url <> '';

                CREATE INDEX IF NOT EXISTS job_discoveries_content_identity
                    ON job_discoveries(content_sha256)
                    WHERE posting_identity = '' AND canonical_url = ''
                      AND content_sha256 <> '';

                CREATE INDEX IF NOT EXISTS job_discoveries_state_last_seen
                    ON job_discoveries(state, last_seen_at DESC, discovery_id);

                CREATE TABLE IF NOT EXISTS job_discovery_observations (
                    observation_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    discovery_id TEXT NOT NULL
                        REFERENCES job_discoveries(discovery_id) ON DELETE CASCADE,
                    source_kind TEXT NOT NULL CHECK (
                        source_kind IN (
                            'extension', 'email_file', 'manual', 'watcher',
                            'public_resolver'
                        )
                    ),
                    source_channel TEXT NOT NULL,
                    request_fingerprint TEXT NOT NULL,
                    request_payload_sha256 TEXT NOT NULL,
                    observed_url TEXT NOT NULL DEFAULT '',
                    observed_at TEXT NOT NULL,
                    content_sha256 TEXT NOT NULL DEFAULT '',
                    metadata_json TEXT NOT NULL DEFAULT '{}',
                    recorded_at TEXT NOT NULL,
                    UNIQUE(source_kind, request_fingerprint)
                );

                CREATE INDEX IF NOT EXISTS job_discovery_observations_discovery_time
                    ON job_discovery_observations(
                        discovery_id, recorded_at, observation_id
                    );

                CREATE TABLE IF NOT EXISTS job_discovery_assessments (
                    discovery_id TEXT PRIMARY KEY
                        REFERENCES job_discoveries(discovery_id) ON DELETE CASCADE,
                    content_sha256 TEXT NOT NULL,
                    analyzer_version INTEGER NOT NULL CHECK (analyzer_version >= 0),
                    evaluator_version INTEGER NOT NULL CHECK (evaluator_version >= 0),
                    candidate_revision TEXT NOT NULL,
                    assessment_context_revision TEXT NOT NULL,
                    assessment_version INTEGER NOT NULL CHECK (assessment_version > 0),
                    scope TEXT NOT NULL CHECK (
                        scope IN ('page_visible', 'confirmed_full')
                    ),
                    assessment_json TEXT NOT NULL,
                    computed_at TEXT NOT NULL
                );

                PRAGMA user_version = 13;`},
	},
	{ // Version 14
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS job_email_connections (
                    connection_id TEXT PRIMARY KEY,
                    provider TEXT NOT NULL CHECK (provider = 'gmail_imap'),
                    account_address TEXT NOT NULL,
                    folder TEXT NOT NULL,
                    secret_name TEXT NOT NULL,
                    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
                    lookback_days INTEGER NOT NULL CHECK (
                        lookback_days BETWEEN 1 AND 90
                    ),
                    max_messages_per_sync INTEGER NOT NULL CHECK (
                        max_messages_per_sync BETWEEN 1 AND 1000
                    ),
                    last_tested_at TEXT,
                    last_test_status TEXT NOT NULL DEFAULT 'not_tested' CHECK (
                        last_test_status IN (
                            'not_tested', 'succeeded', 'failed'
                        )
                    ),
                    last_error_code TEXT NOT NULL DEFAULT '',
                    revision INTEGER NOT NULL CHECK (revision > 0),
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                );

                CREATE TABLE IF NOT EXISTS job_email_sender_rules (
                    connection_id TEXT NOT NULL
                        REFERENCES job_email_connections(connection_id)
                        ON DELETE CASCADE,
                    sender_address TEXT NOT NULL,
                    disposition TEXT NOT NULL CHECK (
                        disposition IN ('allowed', 'disabled')
                    ),
                    purpose TEXT NOT NULL DEFAULT 'both' CHECK (
                        purpose IN (
                            'job_alerts', 'application_tracking', 'both'
                        )
                    ),
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    PRIMARY KEY (connection_id, sender_address)
                );

                CREATE INDEX IF NOT EXISTS job_email_sender_rules_disposition
                    ON job_email_sender_rules(connection_id, disposition, sender_address);

                CREATE TABLE IF NOT EXISTS job_email_sync_state (
                    connection_id TEXT NOT NULL
                        REFERENCES job_email_connections(connection_id)
                        ON DELETE CASCADE,
                    folder TEXT NOT NULL,
                    uidvalidity TEXT NOT NULL DEFAULT '',
                    last_uid INTEGER NOT NULL DEFAULT 0 CHECK (last_uid >= 0),
                    last_started_at TEXT,
                    last_completed_at TEXT,
                    last_result TEXT NOT NULL DEFAULT 'never' CHECK (
                        last_result IN (
                            'never', 'running', 'succeeded', 'partial', 'failed'
                        )
                    ),
                    consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (
                        consecutive_failures >= 0
                    ),
                    next_retry_at TEXT,
                    last_error_code TEXT NOT NULL DEFAULT '',
                    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
                    PRIMARY KEY (connection_id, folder)
                );

                CREATE TABLE IF NOT EXISTS job_email_message_receipts (
                    receipt_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    connection_id TEXT NOT NULL
                        REFERENCES job_email_connections(connection_id)
                        ON DELETE CASCADE,
                    folder TEXT NOT NULL,
                    uidvalidity TEXT NOT NULL,
                    message_uid INTEGER NOT NULL CHECK (message_uid > 0),
                    message_id_hash TEXT NOT NULL DEFAULT '',
                    content_sha256 TEXT NOT NULL DEFAULT '',
                    sender_address TEXT NOT NULL DEFAULT '',
                    status TEXT NOT NULL CHECK (
                        status IN (
                            'imported', 'no_candidates', 'duplicate',
                            'unknown_sender', 'too_large',
                            'failed_retryable', 'failed_final'
                        )
                    ),
                    attempt_count INTEGER NOT NULL DEFAULT 1 CHECK (
                        attempt_count > 0
                    ),
                    candidate_count INTEGER NOT NULL DEFAULT 0 CHECK (
                        candidate_count >= 0
                    ),
                    discovery_count INTEGER NOT NULL DEFAULT 0 CHECK (
                        discovery_count >= 0
                    ),
                    error_code TEXT NOT NULL DEFAULT '',
                    first_seen_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    UNIQUE (connection_id, folder, uidvalidity, message_uid)
                );

                CREATE INDEX IF NOT EXISTS job_email_message_receipts_message_id
                    ON job_email_message_receipts(connection_id, message_id_hash)
                    WHERE message_id_hash <> '';

                CREATE INDEX IF NOT EXISTS job_email_message_receipts_content
                    ON job_email_message_receipts(connection_id, content_sha256)
                    WHERE content_sha256 <> '';

                CREATE INDEX IF NOT EXISTS job_email_message_receipts_pending_sender
                    ON job_email_message_receipts(
                        connection_id, status, sender_address, message_uid
                    );

                PRAGMA user_version = 14;`},
	},
	{ // Version 15
		{kind: columnAction, table: `job_email_connections`, column: `schedule_enabled`, sql: `ALTER TABLE job_email_connections
                    ADD COLUMN schedule_enabled INTEGER NOT NULL DEFAULT 0 CHECK (
                        schedule_enabled IN (0, 1)
                    )`},
		{kind: columnAction, table: `job_email_connections`, column: `interval_minutes`, sql: `ALTER TABLE job_email_connections
                    ADD COLUMN interval_minutes INTEGER NOT NULL DEFAULT 30 CHECK (
                        interval_minutes BETWEEN 15 AND 1440
                    )`},
		{kind: columnAction, table: `job_email_sync_state`, column: `lease_owner`, sql: `ALTER TABLE job_email_sync_state
                    ADD COLUMN lease_owner TEXT NOT NULL DEFAULT ''`},
		{kind: columnAction, table: `job_email_sync_state`, column: `lease_expires_at`, sql: `ALTER TABLE job_email_sync_state
                    ADD COLUMN lease_expires_at TEXT`},
		{kind: sqlAction, sql: `PRAGMA user_version = 15`},
	},
	{ // Version 16
		{kind: columnAction, table: `job_email_message_receipts`, column: `message_sent_date`, sql: `ALTER TABLE job_email_message_receipts
                    ADD COLUMN message_sent_date TEXT NOT NULL DEFAULT ''`},
		{kind: columnAction, table: `job_email_message_receipts`, column: `subject_job_signal`, sql: `ALTER TABLE job_email_message_receipts
                    ADD COLUMN subject_job_signal INTEGER NOT NULL DEFAULT 0 CHECK (
                        subject_job_signal IN (0, 1)
                    )`},
		{kind: columnAction, table: `job_email_message_receipts`, column: `subject_service_signal`, sql: `ALTER TABLE job_email_message_receipts
                    ADD COLUMN subject_service_signal INTEGER NOT NULL DEFAULT 0 CHECK (
                        subject_service_signal IN (0, 1)
                    )`},
		{kind: sqlAction, sql: `PRAGMA user_version = 16`},
	},
	{ // Version 17
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS job_email_application_outcomes (
                    outcome_id TEXT PRIMARY KEY,
                    message_fingerprint TEXT NOT NULL,
                    source_provider TEXT NOT NULL,
                    posting_identity TEXT NOT NULL,
                    source_url TEXT NOT NULL,
                    title TEXT NOT NULL DEFAULT '',
                    company TEXT NOT NULL DEFAULT '',
                    location TEXT NOT NULL DEFAULT '',
                    application_status TEXT NOT NULL CHECK (
                        application_status IN (
                            'captured', 'reviewed', 'preparing', 'applied',
                            'interview', 'offer', 'rejected', 'withdrawn', 'closed'
                        )
                    ),
                    evidence_code TEXT NOT NULL,
                    observed_at TEXT NOT NULL,
                    resolution TEXT NOT NULL CHECK (
                        resolution IN (
                            'unmatched', 'discovery_dismissed',
                            'application_updated', 'application_already_current',
                            'status_conflict', 'ambiguous'
                        )
                    ),
                    matched_discovery_id TEXT
                        REFERENCES job_discoveries(discovery_id) ON DELETE SET NULL,
                    matched_job_id TEXT
                        REFERENCES job_applications(job_id) ON DELETE SET NULL,
                    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    UNIQUE (
                        message_fingerprint, posting_identity, application_status
                    )
                );

                CREATE INDEX IF NOT EXISTS job_email_application_outcomes_time
                    ON job_email_application_outcomes(observed_at DESC, outcome_id);

                CREATE INDEX IF NOT EXISTS job_email_application_outcomes_resolution
                    ON job_email_application_outcomes(resolution, updated_at DESC);

                PRAGMA user_version = 17;`},
	},
	{ // Version 18
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS job_discovery_field_overrides (
                    discovery_id TEXT PRIMARY KEY
                        REFERENCES job_discoveries(discovery_id) ON DELETE CASCADE,
                    title TEXT,
                    company TEXT,
                    location TEXT,
                    revision INTEGER NOT NULL CHECK (revision > 0),
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    CHECK (
                        title IS NULL
                        OR (length(trim(title)) > 0 AND length(title) <= 500)
                    ),
                    CHECK (
                        company IS NULL
                        OR (length(trim(company)) > 0 AND length(company) <= 500)
                    ),
                    CHECK (
                        location IS NULL
                        OR (length(trim(location)) > 0 AND length(location) <= 500)
                    )
                );`},
		{kind: postingsAction},
		{kind: sqlAction, sql: `PRAGMA user_version = 18`},
	},
	{ // Version 19
		{kind: columnAction, table: `job_discoveries`, column: `content_verified_at`, sql: `ALTER TABLE job_discoveries
                    ADD COLUMN content_verified_at TEXT NOT NULL DEFAULT ''`},
		{kind: columnAction, table: `job_discoveries`, column: `content_verified_from_scope`, sql: `ALTER TABLE job_discoveries
                    ADD COLUMN content_verified_from_scope TEXT NOT NULL DEFAULT ''
                    CHECK (
                        content_verified_from_scope IN (
                            '', 'selection', 'page_visible', 'confirmed_full'
                        )
                    )`},
		{kind: sqlAction, sql: `UPDATE job_discoveries
                SET content_verified_at = updated_at,
                    content_verified_from_scope = 'confirmed_full'
                WHERE content_scope = 'confirmed_full'
                  AND content_verified_at = ''`},
		{kind: sqlAction, sql: `PRAGMA user_version = 19`},
	},
	{ // Version 20
		{kind: columnAction, table: `job_email_sender_rules`, column: `purpose`, sql: `ALTER TABLE job_email_sender_rules
                    ADD COLUMN purpose TEXT NOT NULL DEFAULT 'both' CHECK (
                        purpose IN (
                            'job_alerts', 'application_tracking', 'both'
                        )
                    )`},
		{kind: columnAction, table: `job_email_message_receipts`, column: `subject_application_signal`, sql: `ALTER TABLE job_email_message_receipts
                    ADD COLUMN subject_application_signal INTEGER NOT NULL DEFAULT 0
                    CHECK (subject_application_signal IN (0, 1))`},
		{kind: sqlAction, sql: `UPDATE job_email_sender_rules
                SET purpose = 'application_tracking'
                WHERE sender_address = 'handshake@notifications.joinhandshake.com'
                  AND purpose = 'both'`},
		{kind: sqlAction, sql: `CREATE INDEX IF NOT EXISTS job_email_sender_rules_purpose
                    ON job_email_sender_rules(
                        connection_id, disposition, purpose, sender_address
                    )`},
		{kind: sqlAction, sql: `PRAGMA user_version = 20`},
	},
	{ // Version 21
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS application_sessions (
                    session_id TEXT PRIMARY KEY,
                    job_id TEXT NOT NULL
                        REFERENCES job_applications(job_id) ON DELETE CASCADE,
                    state TEXT NOT NULL CHECK (
                        state IN (
                            'pending_claim', 'armed', 'submission_detected',
                            'confirmed_applied', 'dismissed', 'interrupted',
                            'expired', 'cancelled'
                        )
                    ),
                    expected_url TEXT NOT NULL,
                    expected_origin TEXT NOT NULL,
                    bound_tab_id INTEGER CHECK (
                        bound_tab_id IS NULL OR bound_tab_id >= 0
                    ),
                    bound_origin TEXT NOT NULL DEFAULT '',
                    session_token_sha256 TEXT NOT NULL DEFAULT '',
                    claim_request_id TEXT NOT NULL DEFAULT '',
                    detected_event_key TEXT NOT NULL DEFAULT '',
                    detection_rule TEXT NOT NULL DEFAULT '',
                    interruption_reason TEXT NOT NULL DEFAULT '',
                    created_at TEXT NOT NULL,
                    expires_at TEXT NOT NULL,
                    claimed_at TEXT,
                    detected_at TEXT,
                    resolved_at TEXT,
                    updated_at TEXT NOT NULL,
                    revision INTEGER NOT NULL CHECK (revision > 0),
                    CHECK (
                        state <> 'pending_claim'
                        OR (
                            bound_tab_id IS NULL
                            AND bound_origin = ''
                            AND session_token_sha256 = ''
                        )
                    ),
                    CHECK (
                        state <> 'armed'
                        OR (
                            bound_tab_id IS NOT NULL
                            AND bound_origin <> ''
                            AND length(session_token_sha256) = 64
                        )
                    ),
                    CHECK (
                        state NOT IN (
                            'submission_detected', 'confirmed_applied',
                            'dismissed', 'interrupted'
                        )
                        OR (
                            bound_tab_id IS NOT NULL
                            AND bound_origin <> ''
                        )
                    )
                );

                CREATE UNIQUE INDEX IF NOT EXISTS application_sessions_active_job
                    ON application_sessions(job_id)
                    WHERE state IN (
                        'pending_claim', 'armed', 'submission_detected'
                    );

                CREATE UNIQUE INDEX IF NOT EXISTS application_sessions_armed_tab
                    ON application_sessions(bound_tab_id)
                    WHERE state = 'armed';

                CREATE INDEX IF NOT EXISTS application_sessions_review_queue
                    ON application_sessions(state, detected_at DESC, session_id);

                CREATE INDEX IF NOT EXISTS application_sessions_expiry
                    ON application_sessions(state, expires_at);

                CREATE TABLE IF NOT EXISTS application_session_events (
                    event_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    session_id TEXT NOT NULL
                        REFERENCES application_sessions(session_id) ON DELETE CASCADE,
                    event_key TEXT NOT NULL UNIQUE,
                    event_type TEXT NOT NULL CHECK (
                        event_type = 'submission_detected'
                    ),
                    rule_id TEXT NOT NULL,
                    submit_observed_at TEXT NOT NULL,
                    success_observed_at TEXT NOT NULL,
                    recorded_at TEXT NOT NULL
                );

                CREATE INDEX IF NOT EXISTS application_session_events_session
                    ON application_session_events(session_id, event_id);

                PRAGMA user_version = 21;`},
	},
	{ // Version 22
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS application_instruction_manual_items (
                    item_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    job_id TEXT NOT NULL
                        REFERENCES job_applications(job_id) ON DELETE CASCADE,
                    instruction_fingerprint TEXT NOT NULL CHECK (
                        length(instruction_fingerprint) = 64
                    ),
                    source_fingerprint TEXT NOT NULL CHECK (
                        length(source_fingerprint) = 64
                    ),
                    category TEXT NOT NULL CHECK (
                        category IN (
                            'email_destination', 'exact_phrase',
                            'required_artifact', 'cover_letter_format',
                            'external_step', 'deadline'
                        )
                    ),
                    confidence TEXT NOT NULL CHECK (
                        confidence IN ('explicit', 'review')
                    ),
                    source_text TEXT NOT NULL,
                    line_start INTEGER NOT NULL CHECK (line_start > 0),
                    line_end INTEGER NOT NULL CHECK (line_end >= line_start),
                    extractor_version INTEGER NOT NULL CHECK (extractor_version >= 0),
                    created_at TEXT NOT NULL,
                    UNIQUE(job_id, instruction_fingerprint, source_fingerprint)
                );

                CREATE INDEX IF NOT EXISTS application_instruction_manual_job_source
                    ON application_instruction_manual_items(
                        job_id, source_fingerprint, item_id
                    );

                CREATE TABLE IF NOT EXISTS application_instruction_action_events (
                    event_id INTEGER PRIMARY KEY AUTOINCREMENT,
                    job_id TEXT NOT NULL
                        REFERENCES job_applications(job_id) ON DELETE CASCADE,
                    instruction_fingerprint TEXT NOT NULL CHECK (
                        length(instruction_fingerprint) = 64
                    ),
                    source_fingerprint TEXT NOT NULL CHECK (
                        length(source_fingerprint) = 64
                    ),
                    action TEXT NOT NULL CHECK (
                        action IN ('pending', 'done', 'not_applicable')
                    ),
                    category TEXT NOT NULL CHECK (
                        category IN (
                            'email_destination', 'exact_phrase',
                            'required_artifact', 'cover_letter_format',
                            'external_step', 'deadline'
                        )
                    ),
                    confidence TEXT NOT NULL CHECK (
                        confidence IN ('explicit', 'review')
                    ),
                    origin TEXT NOT NULL CHECK (origin IN ('detected', 'manual')),
                    source_text TEXT NOT NULL,
                    line_start INTEGER NOT NULL CHECK (line_start > 0),
                    line_end INTEGER NOT NULL CHECK (line_end >= line_start),
                    extractor_version INTEGER NOT NULL CHECK (extractor_version >= 0),
                    recorded_at TEXT NOT NULL
                );

                CREATE INDEX IF NOT EXISTS application_instruction_action_latest
                    ON application_instruction_action_events(
                        job_id, instruction_fingerprint, event_id DESC
                    );

                PRAGMA user_version = 22;`},
	},
	{ // Version 23
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS application_contacts (
                    contact_id TEXT PRIMARY KEY,
                    name TEXT NOT NULL DEFAULT '',
                    role TEXT NOT NULL DEFAULT '',
                    company TEXT NOT NULL DEFAULT '',
                    context TEXT NOT NULL DEFAULT '',
                    identity_channel_type TEXT NOT NULL DEFAULT '' CHECK (
                        identity_channel_type IN (
                            '', 'email', 'phone', 'professional_profile'
                        )
                    ),
                    identity_channel_normalized TEXT NOT NULL DEFAULT '',
                    archived_at TEXT,
                    revision INTEGER NOT NULL CHECK (revision > 0),
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    CHECK (
                        name <> '' OR identity_channel_normalized <> ''
                    ),
                    CHECK (
                        (identity_channel_type = ''
                         AND identity_channel_normalized = '')
                        OR
                        (identity_channel_type <> ''
                         AND identity_channel_normalized <> '')
                    )
                );

                CREATE INDEX IF NOT EXISTS application_contacts_active_name
                    ON application_contacts(archived_at, name, contact_id);

                CREATE TABLE IF NOT EXISTS application_contact_channels (
                    channel_id TEXT PRIMARY KEY,
                    contact_id TEXT NOT NULL
                        REFERENCES application_contacts(contact_id)
                        ON DELETE RESTRICT,
                    channel_type TEXT NOT NULL CHECK (
                        channel_type IN (
                            'email', 'phone', 'professional_profile'
                        )
                    ),
                    exact_value TEXT NOT NULL,
                    normalized_value TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    UNIQUE(contact_id, channel_type, normalized_value)
                );

                CREATE UNIQUE INDEX IF NOT EXISTS application_contact_exact_email
                    ON application_contact_channels(normalized_value)
                    WHERE channel_type = 'email';

                CREATE UNIQUE INDEX IF NOT EXISTS application_contact_exact_profile
                    ON application_contact_channels(normalized_value)
                    WHERE channel_type = 'professional_profile';

                CREATE INDEX IF NOT EXISTS application_contact_channels_contact
                    ON application_contact_channels(
                        contact_id, channel_type, channel_id
                    );

                CREATE TABLE IF NOT EXISTS application_contact_provenance (
                    provenance_id TEXT PRIMARY KEY,
                    contact_id TEXT NOT NULL
                        REFERENCES application_contacts(contact_id)
                        ON DELETE RESTRICT,
                    origin TEXT NOT NULL CHECK (
                        origin IN ('manual', 'jd_candidate')
                    ),
                    source_reference TEXT NOT NULL,
                    discovery_id TEXT NOT NULL DEFAULT '',
                    job_id TEXT NOT NULL DEFAULT '',
                    source_content_sha256 TEXT NOT NULL DEFAULT '' CHECK (
                        source_content_sha256 = ''
                        OR length(source_content_sha256) = 64
                    ),
                    source_text TEXT NOT NULL DEFAULT '',
                    line_start INTEGER NOT NULL DEFAULT 0 CHECK (line_start >= 0),
                    line_end INTEGER NOT NULL DEFAULT 0 CHECK (line_end >= 0),
                    stated_fields_json TEXT NOT NULL,
                    recorded_at TEXT NOT NULL,
                    CHECK (
                        (line_start = 0 AND line_end = 0)
                        OR
                        (line_start > 0 AND line_end >= line_start)
                    ),
                    UNIQUE(
                        contact_id, origin, source_reference,
                        source_content_sha256
                    )
                );

                CREATE INDEX IF NOT EXISTS application_contact_provenance_contact
                    ON application_contact_provenance(
                        contact_id, recorded_at, provenance_id
                    );

                CREATE TRIGGER IF NOT EXISTS application_contact_provenance_no_update
                BEFORE UPDATE ON application_contact_provenance
                BEGIN
                    SELECT RAISE(ABORT, 'contact provenance is append-only');
                END;

                CREATE TRIGGER IF NOT EXISTS application_contact_provenance_no_delete
                BEFORE DELETE ON application_contact_provenance
                BEGIN
                    SELECT RAISE(ABORT, 'contact provenance is append-only');
                END;

                CREATE TABLE IF NOT EXISTS application_contact_job_links (
                    contact_id TEXT NOT NULL
                        REFERENCES application_contacts(contact_id)
                        ON DELETE RESTRICT,
                    job_id TEXT NOT NULL
                        REFERENCES job_applications(job_id)
                        ON DELETE CASCADE,
                    created_at TEXT NOT NULL,
                    PRIMARY KEY(contact_id, job_id)
                );

                CREATE INDEX IF NOT EXISTS application_contact_job_links_job
                    ON application_contact_job_links(job_id, contact_id);

                CREATE TABLE IF NOT EXISTS application_contact_discovery_links (
                    contact_id TEXT NOT NULL
                        REFERENCES application_contacts(contact_id)
                        ON DELETE RESTRICT,
                    discovery_id TEXT NOT NULL
                        REFERENCES job_discoveries(discovery_id)
                        ON DELETE CASCADE,
                    created_at TEXT NOT NULL,
                    PRIMARY KEY(contact_id, discovery_id)
                );

                CREATE INDEX IF NOT EXISTS application_contact_discovery_links_discovery
                    ON application_contact_discovery_links(
                        discovery_id, contact_id
                    );

                PRAGMA user_version = 23;`},
	},
	{ // Version 24
		{kind: sqlAction, sql: `CREATE UNIQUE INDEX IF NOT EXISTS application_contact_candidate_provenance
                    ON application_contact_provenance(
                        origin, source_reference, source_content_sha256
                    )
                    WHERE origin = 'jd_candidate';

                PRAGMA user_version = 24;`},
	},
	{ // Version 25
		{kind: columnAction, table: `job_email_application_outcomes`, column: `revision`, sql: `ALTER TABLE job_email_application_outcomes
                    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (
                        revision > 0
                    )`},
		{kind: sqlAction, sql: `PRAGMA user_version = 25`},
	},
	{ // Version 26
		{kind: sqlAction, sql: `CREATE TABLE IF NOT EXISTS reusable_answer_families (
                    answer_id TEXT PRIMARY KEY,
                    source_kind TEXT NOT NULL CHECK (
                        source_kind IN ('literal', 'canonical_field')
                    ),
                    canonical_question TEXT NOT NULL CHECK (
                        length(canonical_question) BETWEEN 1 AND 500
                    ),
                    value_kind TEXT NOT NULL CHECK (
                        value_kind IN ('text', 'boolean', 'single_choice')
                    ),
                    canonical_field_id TEXT,
                    current_revision_id TEXT,
                    use_policy TEXT NOT NULL DEFAULT 'suggest_only' CHECK (
                        use_policy = 'suggest_only'
                    ),
                    metadata_revision INTEGER NOT NULL DEFAULT 1 CHECK (
                        metadata_revision > 0
                    ),
                    confirmed_at TEXT NOT NULL,
                    review_due_at TEXT NOT NULL,
                    archived_at TEXT,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    created_request_id TEXT NOT NULL UNIQUE,
                    CHECK (
                        (
                            source_kind = 'literal'
                            AND canonical_field_id IS NULL
                            AND current_revision_id IS NOT NULL
                        )
                        OR
                        (
                            source_kind = 'canonical_field'
                            AND canonical_field_id IS NOT NULL
                            AND current_revision_id IS NULL
                        )
                    ),
                    FOREIGN KEY(answer_id, current_revision_id)
                        REFERENCES reusable_answer_revisions(
                            answer_id, answer_revision_id
                        )
                        ON DELETE RESTRICT
                        DEFERRABLE INITIALLY DEFERRED
                );

                CREATE TABLE IF NOT EXISTS reusable_answer_aliases (
                    alias_id TEXT PRIMARY KEY,
                    answer_id TEXT NOT NULL
                        REFERENCES reusable_answer_families(answer_id)
                        ON DELETE CASCADE,
                    display_text TEXT NOT NULL CHECK (
                        length(display_text) BETWEEN 1 AND 500
                    ),
                    normalized_text TEXT NOT NULL UNIQUE CHECK (
                        length(normalized_text) BETWEEN 1 AND 500
                    ),
                    checkbox_checked_value INTEGER CHECK (
                        checkbox_checked_value IS NULL
                        OR checkbox_checked_value IN (0, 1)
                    ),
                    retired_at TEXT,
                    created_at TEXT NOT NULL,
                    UNIQUE(answer_id, alias_id)
                );

                CREATE TABLE IF NOT EXISTS reusable_answer_revisions (
                    answer_revision_id TEXT PRIMARY KEY,
                    answer_id TEXT NOT NULL
                        REFERENCES reusable_answer_families(answer_id)
                        ON DELETE CASCADE,
                    revision_number INTEGER NOT NULL CHECK (
                        revision_number > 0
                    ),
                    value_json TEXT NOT NULL,
                    value_fingerprint TEXT NOT NULL CHECK (
                        length(value_fingerprint) = 64
                    ),
                    created_at TEXT NOT NULL,
                    created_request_id TEXT NOT NULL UNIQUE,
                    UNIQUE(answer_id, revision_number),
                    UNIQUE(answer_id, answer_revision_id)
                );

                CREATE TABLE IF NOT EXISTS reusable_answer_representations (
                    representation_id TEXT PRIMARY KEY,
                    answer_id TEXT NOT NULL
                        REFERENCES reusable_answer_families(answer_id)
                        ON DELETE CASCADE,
                    alias_id TEXT,
                    control_kind TEXT NOT NULL CHECK (
                        control_kind IN (
                            'text_like', 'textarea', 'select_one',
                            'radio_group', 'checkbox_boolean'
                        )
                    ),
                    option_map_json TEXT NOT NULL,
                    mapping_fingerprint TEXT NOT NULL CHECK (
                        length(mapping_fingerprint) = 64
                    ),
                    verification_state TEXT NOT NULL CHECK (
                        verification_state IN ('user_approved', 'fill_verified')
                    ),
                    metadata_revision INTEGER NOT NULL DEFAULT 1 CHECK (
                        metadata_revision > 0
                    ),
                    retired_at TEXT,
                    created_at TEXT NOT NULL,
                    updated_at TEXT NOT NULL,
                    UNIQUE(answer_id, mapping_fingerprint),
                    UNIQUE(answer_id, representation_id),
                    FOREIGN KEY(answer_id, alias_id)
                        REFERENCES reusable_answer_aliases(answer_id, alias_id)
                        ON DELETE RESTRICT
                );

                CREATE TABLE IF NOT EXISTS reusable_answer_use_events (
                    event_id TEXT PRIMARY KEY,
                    answer_id TEXT NOT NULL
                        REFERENCES reusable_answer_families(answer_id)
                        ON DELETE RESTRICT,
                    answer_revision_id TEXT,
                    canonical_source_revision TEXT,
                    alias_id TEXT,
                    representation_id TEXT,
                    job_id TEXT
                        REFERENCES job_applications(job_id)
                        ON DELETE SET NULL,
                    outcome TEXT NOT NULL CHECK (
                        outcome IN (
                            'filled', 'reverted', 'event_failed',
                            'copied', 'used_once'
                        )
                    ),
                    occurred_at TEXT NOT NULL,
                    recorded_at TEXT NOT NULL,
                    CHECK (
                        (answer_revision_id IS NOT NULL AND canonical_source_revision IS NULL)
                        OR
                        (answer_revision_id IS NULL AND canonical_source_revision IS NOT NULL)
                    ),
                    FOREIGN KEY(answer_id, answer_revision_id)
                        REFERENCES reusable_answer_revisions(
                            answer_id, answer_revision_id
                        )
                        ON DELETE RESTRICT,
                    FOREIGN KEY(answer_id, alias_id)
                        REFERENCES reusable_answer_aliases(answer_id, alias_id)
                        ON DELETE RESTRICT,
                    FOREIGN KEY(answer_id, representation_id)
                        REFERENCES reusable_answer_representations(
                            answer_id, representation_id
                        )
                        ON DELETE RESTRICT
                );

                CREATE INDEX IF NOT EXISTS reusable_answer_families_active_due
                    ON reusable_answer_families(archived_at, review_due_at, updated_at);
                CREATE INDEX IF NOT EXISTS reusable_answer_revisions_family_order
                    ON reusable_answer_revisions(answer_id, revision_number DESC);
                CREATE INDEX IF NOT EXISTS reusable_answer_aliases_family
                    ON reusable_answer_aliases(answer_id, retired_at, created_at);
                CREATE INDEX IF NOT EXISTS reusable_answer_representations_family
                    ON reusable_answer_representations(answer_id, retired_at, control_kind);
                CREATE INDEX IF NOT EXISTS reusable_answer_use_events_answer_time
                    ON reusable_answer_use_events(answer_id, occurred_at, event_id);
                CREATE INDEX IF NOT EXISTS reusable_answer_use_events_job_time
                    ON reusable_answer_use_events(job_id, occurred_at, event_id);

                CREATE TRIGGER IF NOT EXISTS reusable_answer_revisions_no_update
                BEFORE UPDATE ON reusable_answer_revisions
                BEGIN
                    SELECT RAISE(ABORT, 'reusable answer revisions are immutable');
                END;

                CREATE TRIGGER IF NOT EXISTS reusable_answer_use_events_no_update
                BEFORE UPDATE ON reusable_answer_use_events
                BEGIN
                    SELECT RAISE(ABORT, 'reusable answer use events are append-only');
                END;

                CREATE TRIGGER IF NOT EXISTS reusable_answer_use_events_no_delete
                BEFORE DELETE ON reusable_answer_use_events
                BEGIN
                    SELECT RAISE(ABORT, 'reusable answer use events are append-only');
                END;

                PRAGMA user_version = 26;`},
	},
	{ // Version 27
		{kind: columnAction, table: `job_email_sender_rules`, column: `subject_policy_json`, sql: `ALTER TABLE job_email_sender_rules
                    ADD COLUMN subject_policy_json TEXT NOT NULL DEFAULT '{}'`},
		{kind: columnAction, table: `job_email_message_receipts`, column: `subject_route`, sql: `ALTER TABLE job_email_message_receipts
                    ADD COLUMN subject_route TEXT NOT NULL DEFAULT '' CHECK (
                        subject_route IN (
                            '', 'job_alerts', 'application_tracking',
                            'both', 'filtered'
                        )
                    )`},
		{kind: sqlAction, sql: `CREATE INDEX IF NOT EXISTS job_email_message_receipts_sender_route
                    ON job_email_message_receipts(
                        connection_id, sender_address, subject_route
                    )`},
		{kind: sqlAction, sql: `PRAGMA user_version = 27`},
	},
	{ // Version 28
		{kind: columnAction, table: `job_applications`, column: `revision`, sql: `ALTER TABLE job_applications
                    ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (
                        revision > 0
                    )`},
		{kind: sqlAction, sql: `CREATE INDEX IF NOT EXISTS job_email_application_outcomes_matched_discovery
                    ON job_email_application_outcomes(matched_discovery_id);

                CREATE INDEX IF NOT EXISTS job_email_application_outcomes_matched_job
                    ON job_email_application_outcomes(matched_job_id);

                CREATE INDEX IF NOT EXISTS application_contact_provenance_job
                    ON application_contact_provenance(job_id);

                CREATE INDEX IF NOT EXISTS application_contact_provenance_discovery
                    ON application_contact_provenance(discovery_id);

                CREATE INDEX IF NOT EXISTS job_discoveries_last_seen
                    ON job_discoveries(last_seen_at DESC, discovery_id);

                PRAGMA user_version = 28;`},
	},
}
