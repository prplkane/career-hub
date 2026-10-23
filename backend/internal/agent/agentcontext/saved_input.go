package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/discovery/discoverypreview"
	"careerhub.local/backend/internal/jobs/evaluationinput"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/jobevaluation"
	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// SavedEvidence is fresh evaluation input, not a cached assessment or a complete
// agent packet. The owner must still read the ledger/workbench/resume state and
// finish all admission, observation and cleanup checks before publication.
type SavedEvidence struct {
	Job, Assessment json.RawMessage
	Sources         ModelSources
	RegistryGroups  []catalog.Group
}

func (*SavedEvidence) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted saved agent evidence]")
}

// ReadSavedEvidence validates the current saved card and entire Vault, then
// evaluates without registering a job or publishing an assessment cache. Data
// and resources must be confined, admitted owner ports; this function does not
// open files or establish process ownership. Its bounded byte/presence rechecks
// detect observed changes but cannot create an atomic filesystem snapshot.
// A host-selected date is required; there is no ambient or agent-selected clock.
func ReadSavedEvidence(ctx context.Context, data, resources evaluationinput.Source, id string, today time.Time) (*SavedEvidence, *Error) {
	if e := contextStopped(ctx); e != nil {
		return nil, e
	}
	if id == "" || id == "." || id == ".." || len(id) > 256 || !utf8.ValidString(id) || strings.ContainsAny(id, "/\\:\x00") {
		return nil, failure("JOB_ID_INVALID")
	}
	if data == nil || resources == nil || today.Year() < 1 || today.Year() > 9999 {
		return nil, failure("SOURCE_REQUIRED")
	}
	remaining := contextJSONLimit
	d := &observedEvidenceSource{ctx: ctx, source: data, files: map[string]evidenceFile{}, remaining: &remaining}
	r := &observedEvidenceSource{ctx: ctx, source: resources, files: map[string]evidenceFile{}, remaining: &remaining}
	result, e := readSavedEvidence(ctx, d, r, id, today)
	if e != nil {
		return nil, e
	}
	if e := d.recheck(); e != nil {
		return nil, e
	}
	if e := r.recheck(); e != nil {
		return nil, e
	}
	return result, nil
}

// The transaction composition shares these observations through packet output.
func readSavedEvidence(ctx context.Context, d, r *observedEvidenceSource, id string, today time.Time) (*SavedEvidence, *Error) {
	path := "jobs/normalized/" + id + ".yaml"
	loaded, je := jobcard.Read(ctx, d, path)
	if je != nil {
		return nil, failure(je.Code)
	}
	if !loaded.Report.SchemaValid() || packetGet(loaded.Card, "id", packetNull()).Text != id {
		return nil, failure("JOB_CARD_INVALID")
	}
	inputs, ie := evaluationinput.Prepare(ctx, d, r, today)
	if ie != nil {
		return nil, failure(ie.Code)
	}
	return evaluateEvidence(ctx, d, loaded.Card, inputs, path)
}

// Both source-only and saved analysis use the same evaluated Vault observations.
// The source-only caller supplies an in-memory card, never a fabricated file.
func evaluateEvidence(ctx context.Context, d *observedEvidenceSource, card packetValue, inputs *evaluationinput.Inputs, path string) (*SavedEvidence, *Error) {
	raw, present, pe := d.ReadFile("career_vault/hard_blocker_policy.yaml", cataloginput.YAMLMaxInputBytes)
	if pe != nil {
		return nil, failure("POLICY_READ_FAILED")
	}
	policy := packetNull()
	if present {
		var ye *cataloginput.YAMLError
		policy, ye = cataloginput.ParseYAML(raw)
		if ye != nil {
			return nil, failure("POLICY_INVALID")
		}
	}
	policy, policyErr := discoverypreview.Policy(ctx, policy, present)
	if policyErr != nil {
		return nil, failure("POLICY_INVALID")
	}
	review, ee := jobevaluation.Evaluate(ctx, jobevaluation.Input{
		Job: card, Profile: inputs.Candidate.Eligibility, Policy: policy,
		Index: inputs.Candidate.Index, Concepts: inputs.Concepts, Expansions: inputs.Expansions, DurationMonths: inputs.Candidate.DurationMonths,
	})
	if ee != nil {
		return nil, failure(ee.Code)
	}
	result := &SavedEvidence{RegistryGroups: inputs.Registry.Groups()}
	var e *Error
	result.Job, e = encodeOrderedContextJSON(ctx, card, false)
	if e != nil {
		return nil, e
	}
	// Preserve the source's reserved advisory ID convention even for a saved card.
	if strings.HasPrefix(packetGet(card, "id", packetNull()).Text, "advisory_") {
		path = "ephemeral:stdin"
	}
	var encodeErr error
	result.Assessment, encodeErr = json.Marshal(struct {
		*jobevaluation.Result
		JobStatus      string `json:"job_status"`
		AssessmentMode string `json:"assessment_mode"`
		JobCardPath    string `json:"job_card_path"`
	}{review, packetGet(card, "status", packetString("draft")).Text, "advisory_only", path})
	if encodeErr != nil {
		return nil, failure("ASSESSMENT_ENCODING")
	}
	size := len(result.Job) + len(result.Assessment)
	for _, item := range []struct {
		path string
		out  *json.RawMessage
	}{
		{"career_vault/experience_blocks.yaml", &result.Sources.Experiences},
		{"career_vault/project_blocks.yaml", &result.Sources.Projects},
		{"career_vault/education.yaml", &result.Sources.Education},
		{"career_vault/profile.yaml", &result.Sources.Profile},
		{"career_vault/visa_profile.yaml", &result.Sources.Visa},
		{"career_vault/skill_blocks.yaml", &result.Sources.Skills},
		{"career_vault/style_rules.yaml", &result.Sources.StyleRules},
	} {
		// All seven documents were already observed and validated by Prepare.
		file, ok := d.files[item.path]
		if !ok || !file.present {
			return nil, failure("SOURCE_REQUIRED")
		}
		value, ye := cataloginput.ParseYAML(file.raw)
		if ye != nil {
			return nil, failure("SOURCE_INVALID")
		}
		*item.out, e = encodeOrderedContextJSON(ctx, value, false)
		if e != nil {
			return nil, e
		}
		size += len(*item.out)
		if size > contextJSONLimit {
			return nil, failure("SOURCE_LIMIT")
		}
	}
	if e := contextStopped(ctx); e != nil {
		return nil, e
	}
	return result, nil
}

type evidenceFile struct {
	raw     []byte
	present bool
	limit   int64
}
type observedEvidenceSource struct {
	ctx       context.Context
	source    evaluationinput.Source
	files     map[string]evidenceFile
	remaining *int
}

func (s *observedEvidenceSource) Check(path string) *runtimepaths.Error {
	if s.ctx.Err() != nil {
		return &runtimepaths.Error{Code: "READ_CANCELED"}
	}
	return s.source.Check(path)
}
func (s *observedEvidenceSource) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if limit <= 0 || limit > 4<<20 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	if e := s.Check(path); e != nil {
		return nil, false, e
	}
	if old, ok := s.files[path]; ok {
		if int64(len(old.raw)) > limit {
			return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
		}
		return bytes.Clone(old.raw), old.present, nil
	}
	if len(s.files) >= 32 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	raw, present, e := s.source.ReadFile(path, limit)
	if e != nil {
		return nil, false, e
	}
	if s.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "READ_CANCELED"}
	}
	if int64(len(raw)) > limit || len(raw) > *s.remaining {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	if !present && len(raw) != 0 {
		return nil, false, &runtimepaths.Error{Code: "FILE_READ_FAILED"}
	}
	*s.remaining -= len(raw)
	s.files[path] = evidenceFile{bytes.Clone(raw), present, limit}
	return bytes.Clone(raw), present, nil
}
func (s *observedEvidenceSource) recheck() *Error {
	paths := make([]string, 0, len(s.files))
	for path := range s.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if e := s.Check(path); e != nil {
			return failure("SOURCE_RECHECK_FAILED")
		}
		old := s.files[path]
		raw, present, e := s.source.ReadFile(path, old.limit)
		if e != nil {
			return failure("SOURCE_RECHECK_FAILED")
		}
		if old.present != present || !bytes.Equal(old.raw, raw) {
			return failure("SOURCE_CHANGED")
		}
	}
	return contextStopped(s.ctx)
}
