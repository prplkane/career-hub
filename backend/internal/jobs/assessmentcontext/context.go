// Package assessmentcontext preserves the complete deterministic candidate and
// taxonomy revision contract. These are cache inputs, not an assessment result.
package assessmentcontext

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type Error struct{ Stage, Source, Code string }

func (*Error) Error() string                    { return "Assessment inputs could not be read." }
func (e *Error) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, e.Error()) }

// Reader is supplied by the host's managed roots. Paths below are fixed; no
// arbitrary filename or resource-root discovery is accepted at this boundary.
type Reader interface {
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}
type Month struct{ Year, Number int }

func (month Month) Valid() bool {
	return month.Year >= 1 && month.Year <= 9999 && month.Number >= 1 && month.Number <= 12
}

type Revisions struct {
	Candidate string `json:"candidate_revision"`
	Context   string `json:"assessment_context_revision"`
}

type sourceSpec struct {
	path, label string
	resource    bool
}

var candidateFiles = [...]sourceSpec{
	{"career_vault/education.yaml", "education", false},
	{"career_vault/visa_profile.yaml", "eligibility", false},
	{"career_vault/experience_blocks.yaml", "experiences", false},
	{"career_vault/project_blocks.yaml", "projects", false},
	{"career_vault/skill_blocks.yaml", "legacy_skills", false},
}
var contextFiles = [...]sourceSpec{
	{"src/data/job_technologies.yaml", "core_technologies", true},
	{"career_vault/taxonomy/technologies.yaml", "custom_technologies", false},
	{"career_vault/hard_blocker_policy.yaml", "blocker_policy", false},
	{"src/data/requirement_concepts.yaml", "requirement_concepts", true},
}

type observation struct {
	spec    sourceSpec
	present bool
	hash    [32]byte
}
type readSet struct {
	ctx                context.Context
	data, resources    Reader
	observations       []observation
	eligibilityPresent [5]bool
}

var eligibilityFields = [...]string{"current_status", "citizenship_required_blocker", "clearance_required_blocker", "requires_sponsorship_now", "requires_future_sponsorship"}

func canceled(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return &Error{Stage: "context", Code: "ASSESSMENT_INPUT_CANCELED"}
	}
	return nil
}
func (set *readSet) read(spec sourceSpec) ([]byte, bool, *Error) {
	if err := canceled(set.ctx); err != nil {
		return nil, false, err
	}
	reader := set.data
	if spec.resource {
		reader = set.resources
	}
	if reader == nil {
		return nil, false, &Error{Stage: "configuration", Source: spec.label, Code: "ASSESSMENT_ROOT_UNAVAILABLE"}
	}
	raw, present, failure := reader.ReadFile(spec.path, cataloginput.YAMLMaxInputBytes)
	if err := canceled(set.ctx); err != nil {
		return nil, false, err
	}
	if failure != nil {
		code := "ASSESSMENT_INPUT_READ_FAILED"
		if failure.Code == "FILE_SIZE_LIMIT" {
			code = "ASSESSMENT_INPUT_LIMIT"
		}
		return nil, false, &Error{Stage: "read", Source: spec.label, Code: code}
	}
	if len(raw) > cataloginput.YAMLMaxInputBytes {
		return nil, false, &Error{Stage: "read", Source: spec.label, Code: "ASSESSMENT_INPUT_LIMIT"}
	}
	return raw, present, nil
}
func (set *readSet) observe(spec sourceSpec) ([]byte, bool, *Error) {
	raw, present, err := set.read(spec)
	if err == nil {
		set.observations = append(set.observations, observation{spec, present, sha256.Sum256(raw)})
	}
	return raw, present, err
}

// Verify detects observed content/presence drift. It is not a filesystem
// transaction: the owning host must still exclude concurrent writers.
func (set *readSet) verify() *Error {
	for _, before := range set.observations {
		raw, present, err := set.read(before.spec)
		if err != nil {
			return err
		}
		if present != before.present || sha256.Sum256(raw) != before.hash {
			return &Error{Stage: "recheck", Source: before.spec.label, Code: "ASSESSMENT_INPUT_CHANGED"}
		}
	}
	return canceled(set.ctx)
}
func (set *readSet) yaml(spec sourceSpec) (cataloginput.Value, *Error) {
	raw, present, err := set.observe(spec)
	if err != nil {
		return cataloginput.Value{}, err
	}
	if !present {
		return invalid("missing"), nil
	}
	value, yamlErr := cataloginput.ParseYAML(raw)
	if yamlErr != nil {
		code := "ASSESSMENT_YAML_INVALID"
		if yamlErr.Kind == "limit" {
			code = "ASSESSMENT_INPUT_LIMIT"
		} else if yamlErr.Kind == "unsupported" {
			code = "ASSESSMENT_INPUT_UNSUPPORTED"
		}
		return cataloginput.Value{}, &Error{Stage: "input", Source: spec.label, Code: code}
	}
	return value, nil
}

func str(value string) cataloginput.Value { return cataloginput.Value{Kind: "string", Text: value} }
func mapping(entries ...cataloginput.Entry) cataloginput.Value {
	return cataloginput.Value{Kind: "mapping", Entries: entries}
}
func entry(key string, value cataloginput.Value) cataloginput.Entry {
	return cataloginput.Entry{Key: str(key), Value: value}
}
func invalid(reason string) cataloginput.Value { return mapping(entry("invalid", str(reason))) }
func get(value cataloginput.Value, key string) (cataloginput.Value, bool) {
	for _, entry := range value.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key {
			return entry.Value, true
		}
	}
	return cataloginput.Value{Kind: "null"}, false
}
func fields(value cataloginput.Value, keys ...string) cataloginput.Value {
	result := mapping()
	for _, key := range keys {
		field, _ := get(value, key)
		result.Entries = append(result.Entries, entry(key, field))
	}
	return result
}
func activeRecords(value cataloginput.Value, keys ...string) cataloginput.Value {
	if value.Kind != "sequence" {
		return invalid("expected a list")
	}
	result := cataloginput.Value{Kind: "sequence", Items: make([]cataloginput.Value, 0)}
	for _, item := range value.Items {
		if item.Kind != "mapping" {
			return invalid("expected record mappings")
		}
		archived, present := get(item, "archived")
		if present && archived.Kind != "bool" {
			return invalid("archived must be a boolean")
		}
		if present && archived.Boolean {
			continue
		}
		result.Items = append(result.Items, fields(item, keys...))
	}
	return result
}
func (set *readSet) projection() (cataloginput.Value, *Error) {
	var values [5]cataloginput.Value
	for i, spec := range candidateFiles {
		value, err := set.yaml(spec)
		if err != nil {
			return cataloginput.Value{}, err
		}
		values[i] = value
	}
	certifications := invalid("expected a mapping")
	if values[0].Kind == "mapping" {
		var present bool
		certifications, present = get(values[0], "certifications")
		if !present {
			certifications = cataloginput.Value{Kind: "sequence", Items: make([]cataloginput.Value, 0)}
		}
	}
	visa := invalid("expected a mapping")
	if values[1].Kind == "mapping" {
		for i, key := range eligibilityFields {
			_, set.eligibilityPresent[i] = get(values[1], key)
		}
		visa = fields(values[1], eligibilityFields[:]...)
	}
	return mapping(
		entry("experience", activeRecords(values[2], "id", "company", "role", "category", "technologies", "proof_points", "dates", "date_range")),
		entry("projects", activeRecords(values[3], "id", "name", "category", "summary", "technologies", "proof_points")),
		entry("certifications", activeRecords(certifications, "id", "name", "credential", "proof_points")),
		entry("skills", activeRecords(values[4], "id", "skill_family", "keywords")),
		entry("visa", visa),
	), nil
}

func labeledRevision(label string, raw []byte) string {
	digest := sha256.New()
	digest.Write([]byte(label))
	digest.Write([]byte{0})
	digest.Write(raw)
	digest.Write([]byte{0})
	return fmt.Sprintf("sha256:%x", digest.Sum(nil))
}
func (set *readSet) candidateRevision() (string, *Error) {
	projection, err := set.projection()
	if err != nil {
		return "", err
	}
	serialized, err := canonicalJSON(set.ctx, projection)
	if err != nil {
		return "", err
	}
	return labeledRevision("candidate_projection", serialized), nil
}
func (set *readSet) contextRevision(month Month) (string, *Error) {
	if !month.Valid() {
		return "", &Error{Stage: "validation", Code: "ASSESSMENT_MONTH_INVALID"}
	}
	digest := sha256.New()
	for _, spec := range contextFiles {
		raw, present, err := set.observe(spec)
		if err != nil {
			return "", err
		}
		digest.Write([]byte(spec.path))
		digest.Write([]byte{0})
		if present {
			digest.Write(raw)
		} else {
			digest.Write([]byte("<missing>"))
		}
		digest.Write([]byte{0})
	}
	digest.Write([]byte("evaluation_month\x00"))
	digest.Write([]byte(fmt.Sprintf("%04d-%02d", month.Year, month.Number)))
	digest.Write([]byte{0})
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

// ReadCandidateProjection preserves source invalid markers; it is intentionally
// not the stricter full Vault validator. Such changes still invalidate a cache.
func ReadCandidateProjection(ctx context.Context, data Reader) (cataloginput.Value, *Error) {
	set := readSet{ctx: ctx, data: data}
	result, err := set.projection()
	if err != nil {
		return cataloginput.Value{}, err
	}
	if err := set.verify(); err != nil {
		return cataloginput.Value{}, err
	}
	return result, nil
}
func ReadCandidateRevision(ctx context.Context, data Reader) (string, *Error) {
	set := readSet{ctx: ctx, data: data}
	result, err := set.candidateRevision()
	if err != nil {
		return "", err
	}
	if err := set.verify(); err != nil {
		return "", err
	}
	return result, nil
}

// ReadContextRevision is the legacy source-comparison identity, including its
// missing-marker collision. CH-46 must replace it before Go cache activation.
func ReadContextRevision(ctx context.Context, data, resources Reader, month Month) (string, *Error) {
	set := readSet{ctx: ctx, data: data, resources: resources}
	result, err := set.contextRevision(month)
	if err != nil {
		return "", err
	}
	if err := set.verify(); err != nil {
		return "", err
	}
	return result, nil
}

// ReadRevisions verifies the union of both input sets before returning either
// revision. The host supplies the evaluation month explicitly; no ambient clock.
// These legacy identities are not an approved production cache key (see CH-46).
func ReadRevisions(ctx context.Context, data, resources Reader, month Month) (Revisions, *Error) {
	if !month.Valid() {
		return Revisions{}, &Error{Stage: "validation", Code: "ASSESSMENT_MONTH_INVALID"}
	}
	set := readSet{ctx: ctx, data: data, resources: resources}
	candidate, err := set.candidateRevision()
	if err != nil {
		return Revisions{}, err
	}
	contextRevision, err := set.contextRevision(month)
	if err != nil {
		return Revisions{}, err
	}
	if err := set.verify(); err != nil {
		return Revisions{}, err
	}
	return Revisions{candidate, contextRevision}, nil
}
