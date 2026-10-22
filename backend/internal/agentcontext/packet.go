package agentcontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobevaluation"
	"careerhub.local/backend/internal/skills/catalog"
)

// PacketInput consists of values observed by ONE admitted read owner, not paths
// for this builder to open. Assessment is the complete corresponding source
// evaluation including job_status, assessment_mode and job_card_path. Application
// is the existing ledger summary, not an arbitrary row dump. The owner must
// retain source JSON/YAML mapping order when encoding all raw values.
//
// Task/purpose control projections here; callers cannot request unrelated draft
// responses or force a historical digest scan through the resume flags.
type PacketInput struct {
	Job, Assessment                      json.RawMessage
	Sources                              ModelSources
	Profile, Channel                     string
	Purpose, Operation, RegistryMode     string
	Persisted                            bool
	Task                                 *TaskRequest
	Application, Workbench, WriteReceipt json.RawMessage
	ResumeInitialized                    bool
	ResumeLibrary                        json.RawMessage
	ResumeLibraryFailure                 *string
	RegistryGroups                       []catalog.Group
	RegistryFailure                      *string
}

// BuildPacket builds a complete contract-v4 purpose packet without filesystem,
// database, credential, network or write access. Digest enrichment is the sole
// injected read callback and is invoked only for the bounded resume selection.
// It does not establish snapshot coherence or make the native commands available.
func BuildPacket(ctx context.Context, input PacketInput, digestReader ResumeDigestReader) ([]byte, *Error) {
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	if !ValidPurpose(input.Purpose) || (input.Operation != "context" && input.Operation != "analyze" && input.Operation != "prepare") {
		return nil, failure("PACKET_INTENT")
	}
	size := len(input.Profile) + len(input.Channel)
	for _, raw := range []json.RawMessage{input.Job, input.Assessment, input.Sources.Experiences, input.Sources.Projects, input.Sources.Education, input.Sources.Profile, input.Sources.Visa, input.Sources.Skills, input.Sources.StyleRules, input.Application, input.Workbench, input.WriteReceipt, input.ResumeLibrary} {
		if len(raw) > contextJSONLimit-size {
			return nil, failure("PACKET_LIMIT")
		}
		size += len(raw)
	}
	if !utf8.ValidString(input.Profile) || !utf8.ValidString(input.Channel) {
		return nil, failure("PACKET_INPUT")
	}
	b := packetBuilder{ctx: ctx}
	card := b.read(input.Job, packetNull())
	assessment := b.read(input.Assessment, packetNull())
	sources := b.sources(input.Sources)
	application := packetNull()
	if input.Persisted {
		application = b.read(input.Application, packetNull())
	}
	receipt := b.read(input.WriteReceipt, packetObject())
	if receipt.Kind == "null" {
		receipt = packetObject()
	}
	if b.err != nil {
		return nil, b.err
	}
	if card.Kind != "mapping" || assessment.Kind != "mapping" || receipt.Kind != "mapping" || (application.Kind != "null" && application.Kind != "mapping") {
		return nil, failure("PACKET_INPUT")
	}
	if digest, exists := packetLookup(card, "digest"); exists && digest.Kind != "mapping" {
		return nil, failure("PACKET_INPUT")
	}
	for _, key := range []string{"matched_required", "matched_preferred", "taxonomy_hints", "experience_review", "relevant_evidence_ids", "eligibility_blockers", "requirement_gaps", "missing_preferred", "manual_review"} {
		if packetGet(assessment, key, packetNull()).Kind != "sequence" {
			return nil, failure("PACKET_INPUT")
		}
	}
	if packetGet(assessment, "application_allowed", packetNull()).Kind != "bool" || packetGet(assessment, "lane", packetNull()).Kind != "string" || packetGet(assessment, "biggest_gap", packetNull()).Kind != "string" {
		return nil, failure("PACKET_INPUT")
	}
	jobID := packetGet(card, "id", packetNull())
	assessmentID := packetGet(assessment, "job_id", packetNull())
	if jobID.Kind != "string" || jobID.Text == "" || assessmentID.Kind != "string" || assessmentID.Text != jobID.Text {
		return nil, failure("PACKET_IDENTITY")
	}
	if id, exists := packetLookup(application, "job_id"); exists && (id.Kind != "string" || id.Text != jobID.Text) {
		return nil, failure("PACKET_IDENTITY")
	}
	jobPath := packetGet(assessment, "job_card_path", packetNull())
	if jobPath.Kind != "string" {
		return nil, failure("PACKET_INPUT")
	}
	var review jobevaluation.Result
	if decodeNumbers(input.Assessment, &review) != nil {
		return nil, failure("PACKET_INPUT")
	}
	taskRaw, err := TaskPayload(input.Purpose, input.Task)
	if err != nil {
		return nil, err
	}
	bundle, err := BuildEvidenceContext(ctx, input.Job, review, input.Sources, jobPath.Text)
	if err != nil {
		return nil, err
	}
	matrix, model, worksheet := b.typed(bundle.Matrix), b.read(bundle.ModelContext, packetNull()), b.typed(bundle.Worksheet)
	if b.err != nil {
		return nil, b.err
	}
	matrix, model, worksheet, err = orderEvidencePacket(matrix, model, worksheet, card, assessment, sources)
	if err != nil {
		return nil, err
	}
	compact, err := BuildCompactAssessment(ctx, *bundle.Matrix, input.Job, review, bundle.ModelContext, input.Purpose)
	if err != nil {
		return nil, err
	}
	registry, err := BuildRegistryContext(ctx, input.Job, input.RegistryMode, input.Persisted, input.RegistryGroups, input.RegistryFailure)
	if err != nil {
		return nil, err
	}
	resumeInput := ResumeContextInput{Initialized: input.ResumeInitialized, Library: input.ResumeLibrary, LibraryFailure: input.ResumeLibraryFailure, IncludeLibrary: input.Purpose != "application_text", IncludeDigest: input.Purpose == "resume_delta"}
	if input.Persisted {
		resumeInput.Application = input.Application
	}
	if input.Task != nil {
		resumeInput.TargetID = input.Task.fields.ResumeID
	}
	// The scalar escape check must precede encoding/json's replacement behavior,
	// but unavailable libraries must retain their source early-return semantics.
	if input.ResumeInitialized && input.ResumeLibraryFailure == nil {
		b.read(input.ResumeLibrary, packetNull())
		if b.err != nil {
			return nil, b.err
		}
	}
	enriched := packetList()
	var ownedDigest ResumeDigestReader
	if digestReader != nil {
		ownedDigest = func(ctx context.Context, selected json.RawMessage) (json.RawMessage, *string, *Error) {
			result, warning, err := digestReader(ctx, selected)
			if err == nil && warning == nil {
				enriched = b.read(result, packetList())
				if b.err != nil {
					return nil, nil, b.err
				}
			}
			return result, warning, err
		}
	}
	resumes, err := BuildResumeContext(ctx, resumeInput, ownedDigest)
	if err != nil {
		return nil, err
	}
	resumeValue := b.typed(resumes)
	resumeItems := packetGet(resumeValue, "items", packetList())
	for i, item := range resumeItems.Items {
		item = packetPick(item, "id", "display_name", "digest_status", "digest_supported", "digest_processed_at")
		if i < len(enriched.Items) {
			for j, field := range item.Entries {
				item.Entries[j].Value = retainPacketOrder(field.Value, packetGet(enriched.Items[i], field.Key.Text, packetNull()))
			}
		}
		resumeItems.Items[i] = item
	}
	packetSet(&resumeValue, "items", resumeItems)
	registryValue := b.typed(registry)
	digest := packetGet(card, "digest", packetObject())
	registrySources := map[string][]packetValue{
		"recognized":         packetRecords(packetPrefix(packetGet(digest, "skill_occurrences", packetList()), 40), false),
		"needs_review_lines": packetRecords(packetPrefix(packetGet(packetGet(digest, "analysis_coverage", packetObject()), "review_items", packetList()), 12), false),
	}
	for _, list := range []struct {
		name string
		keys []string
	}{
		{"recognized", []string{"skill_id", "canonical_name", "source_text", "primary_group_id", "origin", "requirement", "line"}},
		{"needs_review_lines", []string{"line", "section", "text"}},
	} {
		items := packetGet(registryValue, list.name, packetList())
		if len(items.Items) != len(registrySources[list.name]) {
			return nil, failure("PACKET_PROJECTION_IDENTITY")
		}
		for i, item := range items.Items {
			item = packetPick(item, list.keys...)
			for j, field := range item.Entries {
				item.Entries[j].Value = retainPacketOrder(field.Value, packetGet(registrySources[list.name][i], field.Key.Text, packetNull()))
			}
			items.Items[i] = item
		}
		packetSet(&registryValue, list.name, items)
	}
	evidence := b.typed(compact.TopEvidence)
	direct := packetGet(model, "candidate_direct_evidence", packetList())
	if len(evidence.Items) > len(direct.Items) {
		return nil, failure("PACKET_PROJECTION_IDENTITY")
	}
	for i, item := range evidence.Items {
		item = packetPick(item, "id", "evidence_type", "company", "role", "dates", "name", "category", "summary", "credential", "completed", "technologies", "proof_points")
		for j, field := range item.Entries {
			item.Entries[j].Value = retainPacketOrder(field.Value, packetGet(direct.Items[i], field.Key.Text, packetNull()))
		}
		evidence.Items[i] = item
	}
	if b.err != nil {
		return nil, b.err
	}
	snapshot := packetObject(packetField("contract_version", packetInt(4)), packetField("job", card), packetField("assessment", assessment), packetField("matrix", matrix), packetField("model_context", model), packetField("worksheet", worksheet))
	snapshotJSON, err := encodeOrderedContextJSON(ctx, snapshot, true)
	if err != nil {
		return nil, err
	}
	requirements := packetGet(matrix, "requirements", packetList())
	packet := packetObject(
		packetField("schema_version", packetInt(1)), packetField("contract_version", packetInt(4)),
		packetField("operation", packetString(input.Operation)), packetField("purpose", packetString(input.Purpose)),
		packetField("task", b.read(taskRaw, packetNull())),
		packetField("context_snapshot", packetObject(packetField("id", packetString(packetHash(snapshotJSON))), packetField("reusable_across_purposes", packetBool(true)), packetField("job_fingerprint", packetGet(card, "content_fingerprint", packetString(""))))),
		packetField("data_home", packetObject(packetField("profile", packetString(input.Profile)), packetField("channel", packetString(input.Channel)))),
		packetField("job", compactPacketJob(card, input.Persisted)),
		packetField("recommendation", b.typed(compact.Recommendation)), packetField("coverage", b.typed(compact.Coverage)),
		packetField("eligibility", packetObject(packetField("visa_signals", packetGet(card, "visa_signals", packetObject())), packetField("blockers", packetGet(assessment, "eligibility_blockers", packetList())))),
		packetField("requirements", packetPrefix(requirements, 40)), packetField("requirements_truncated", packetBool(len(requirements.Items) > 40)),
		packetField("top_evidence", evidence), packetField("resume_versions", resumeValue), packetField("registry", registryValue),
		packetField("application", application), packetField("manual_review", packetPrefix(packetGet(assessment, "manual_review", packetList()), 12)),
		packetField("write_receipt", receipt),
		packetField("no_forcing", packetObject(packetField("no_change_needed_is_success", packetBool(true)), packetField("resume_change_quota", packetInt(0)), packetField("draft_is_not_submission", packetBool(true)))),
	)
	if input.Purpose == "application_text" {
		workbench, err := orderedWorkbenchContext(ctx, input.Workbench, input.Persisted, input.Task)
		if err != nil {
			return nil, err
		}
		packetSet(&packet, "writing", packetObject(
			packetField("candidate", packetGet(model, "candidate", packetObject())), packetField("style_rules", packetGet(model, "applicable_style_rules", packetObject())),
			packetField("workbench", workbench), packetField("default_delivery", packetString("inline")), packetField("document_delivery", packetString("only_when_explicitly_requested")),
			packetField("draft_contract", packetObject(
				packetField("required", b.typed([]string{"id", "kind", "source", "exact_prompt", "response"})),
				packetField("optional", b.typed([]string{"limits", "evidence_refs", "manual_checks"})),
				packetField("allowed_kinds", b.typed([]string{"cover_letter", "follow_up", "long_answer", "other", "recruiter_message", "short_answer"})),
				packetField("state", packetString("draft_only")),
			)),
		))
	} else if input.Purpose == "resume_delta" {
		experiences := packetGet(worksheet, "experience_candidates", packetList())
		packetSet(&packet, "resume_delta", packetObject(
			packetField("selection_worksheet", packetObject(
				packetField("scoring", packetGet(worksheet, "scoring", packetObject())), packetField("shortlist", packetGet(worksheet, "shortlist", packetList())),
				packetField("experience_candidates", packetPrefix(experiences, 20)), packetField("experience_candidates_total", packetInt(len(experiences.Items))), packetField("experience_candidates_truncated", packetBool(len(experiences.Items) > 20)),
			)), packetField("style_rules", packetGet(model, "applicable_style_rules", packetObject())), packetField("success_states", b.typed([]string{"no_change_needed", "changes_proposed"})),
		))
	}
	if b.err != nil {
		return nil, b.err
	}
	before, err := encodeOrderedContextJSON(ctx, packet, false)
	if err != nil {
		return nil, err
	}
	characters, budget := utf8.RuneCount(before), 6500
	if input.Purpose == "assessment" {
		budget = 4500
	}
	tokens := (characters + 3) / 4
	packetSet(&packet, "context_metrics", packetObject(
		packetField("characters_before_metrics", packetInt(characters)), packetField("approximate_tokens_at_four_characters", packetInt(tokens)),
		packetField("included_evidence_blocks", packetInt(len(evidence.Items))), packetField("token_budget_at_four_characters", packetInt(budget)),
		packetField("within_budget", packetBool(tokens <= budget)), packetField("packet_fingerprint", packetString(packetHash(before))),
	))
	return encodeOrderedContextJSON(ctx, packet, false)
}

func packetHash(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func compactPacketJob(card packetValue, persisted bool) packetValue {
	out := packetObject(packetField("id", packetGet(card, "id", packetNull())), packetField("persisted", packetBool(persisted)), packetField("status", packetGet(card, "status", packetNull())))
	for _, key := range []string{"company", "title", "source_url", "content_fingerprint", "location", "remote_policy", "seniority"} {
		packetSet(&out, key, packetGet(card, key, packetString("")))
	}
	packetSet(&out, "compensation", packetGet(card, "compensation", packetObject()))
	packetSet(&out, "responsibilities", packetPrefix(packetGet(card, "responsibilities", packetList()), 10))
	packetSet(&out, "unresolved_fields", packetGet(packetGet(card, "digest", packetObject()), "unresolved_fields", packetList()))
	return out
}

func orderedWorkbenchContext(ctx context.Context, raw []byte, persisted bool, task *TaskRequest) (packetValue, *Error) {
	b := packetBuilder{ctx: ctx}
	original := packetNull()
	var store map[string]any
	if persisted {
		original = b.read(raw, packetNull())
		if b.err != nil {
			return packetValue{}, b.err
		}
		if original.Kind != "null" && (original.Kind != "mapping" || decodeNumbers(raw, &store) != nil) {
			return packetValue{}, failure("WORKBENCH_UNSUPPORTED")
		}
	}
	indexes := []int{}
	sourceItems := packetRecords(packetGet(original, "items", packetList()), false)
	projected, err := workbenchContext(store, task, &indexes, func(index int, key string) (string, *Error) {
		if index < 0 || index >= len(sourceItems) {
			return "", failure("PACKET_PROJECTION_IDENTITY")
		}
		return workbenchStoredText(ctx, packetGet(sourceItems[index], key, packetNull()))
	})
	if err != nil {
		return packetValue{}, err
	}
	result := b.read(projected, packetNull())
	items := packetGet(result, "items", packetList())
	if len(items.Items) != len(indexes) {
		return packetValue{}, failure("PACKET_PROJECTION_IDENTITY")
	}
	for i, row := range items.Items {
		if indexes[i] < 0 || indexes[i] >= len(sourceItems) {
			return packetValue{}, failure("PACKET_PROJECTION_IDENTITY")
		}
		row = packetPick(row, "id", "kind", "exact_prompt", "exact_prompt_truncated", "limits", "evidence_refs", "manual_checks", "state", "response_available", "response", "response_truncated")
		for j, field := range row.Entries {
			row.Entries[j].Value = retainPacketOrder(field.Value, packetGet(sourceItems[indexes[i]], field.Key.Text, packetNull()))
		}
		items.Items[i] = row
	}
	packetSet(&result, "items", items)
	return result, b.err
}
