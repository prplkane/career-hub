package jobevaluation

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobevidence"
	"careerhub.local/backend/internal/textcompat"
)

// Input borrows the caller's snapshot for the duration of Evaluate. Callers must
// not mutate it concurrently. Only consumed shapes are projected here: this is
// not a replacement for the complete Job Card, digest, or Vault validator.
type Input struct {
	Job, Profile, Policy cataloginput.Value
	Index                jobevidence.Index
	Concepts             *jobevidence.Concepts
	Expansions           map[string][]string
	DurationMonths       map[string]float64
}

type technologyGroup struct {
	level        string
	alternatives []string
}
type experience struct {
	minimum     json.Number
	threshold   float64
	maximum     json.RawMessage
	label, area string
	evidence    []any
}
type classified struct {
	category, name, level, text string
	evidence                    []any
}
type job struct {
	id                                      string
	requiredSkills, platforms, capabilities []string
	preferred, education                    []string
	groups                                  []technologyGroup
	experience                              []experience
	classified                              []classified
	signals                                 cataloginput.Value
	gateEvidence                            map[string][]any
	noRequirementSections                   bool
}

func field(value cataloginput.Value, key string) (cataloginput.Value, bool) {
	if value.Kind == "mapping" {
		for _, entry := range value.Entries {
			if entry.Key.Kind == "string" && entry.Key.Text == key {
				return entry.Value, true
			}
		}
	}
	return cataloginput.Value{Kind: "null"}, false
}
func get(value cataloginput.Value, key string) cataloginput.Value {
	result, _ := field(value, key)
	return result
}
func isTrue(value cataloginput.Value) bool { return value.Kind == "bool" && value.Boolean }
func truthy(value cataloginput.Value) bool {
	prepared := cataloginput.PrepareCatalog(value, false)
	return prepared.Kind != "sequence" || len(prepared.Items) != 0
}

type inputBudget struct {
	ctx                        context.Context
	bytes, nodes, requirements int
}

// Validate bounded decoded shape before lookups, including ignored metadata.
// Arbitrary Python objects/non-finite numbers are an explicit unsupported
// boundary; they must not produce a successful partial evaluation.
func (b *inputBudget) tree(value cataloginput.Value, depth int) *Error {
	if err := stopped(b.ctx); err != nil {
		return err
	}
	b.nodes++
	b.bytes += len(value.Text) + len(value.Bytes)
	if depth > cataloginput.YAMLMaxDepth || b.nodes > maxNodes || b.bytes > MaxInputBytes {
		return &Error{Code: "EVALUATION_INPUT_LIMIT"}
	}
	if !utf8.ValidString(value.Text) {
		return &Error{Code: "EVALUATION_INPUT_UNSUPPORTED"}
	}
	switch value.Kind {
	case "null", "bool", "string", "date", "datetime", "bytes":
	case "int", "float":
		if !validNumber(value) {
			return &Error{Code: "EVALUATION_INPUT_UNSUPPORTED"}
		}
	case "sequence":
		if len(value.Items) > maxNodes-b.nodes {
			return &Error{Code: "EVALUATION_INPUT_LIMIT"}
		}
		for _, item := range value.Items {
			if err := b.tree(item, depth+1); err != nil {
				return err
			}
		}
	case "mapping":
		if len(value.Entries) > (maxNodes-b.nodes)/2 {
			return &Error{Code: "EVALUATION_INPUT_LIMIT"}
		}
		keys := map[string]bool{}
		for _, entry := range value.Entries {
			if entry.Key.Kind != "string" || keys[entry.Key.Text] {
				return &Error{Code: "EVALUATION_INPUT_UNSUPPORTED"}
			}
			keys[entry.Key.Text] = true
			if err := b.tree(entry.Key, depth+1); err != nil {
				return err
			}
			if err := b.tree(entry.Value, depth+1); err != nil {
				return err
			}
		}
	default:
		return &Error{Code: "EVALUATION_INPUT_UNSUPPORTED"}
	}
	return nil
}

func validNumber(value cataloginput.Value) bool {
	if len(value.Text) == 0 || len(value.Text) > 1024 || !json.Valid([]byte(value.Text)) {
		return false
	}
	text := value.Text
	if text[0] == '-' {
		text = text[1:]
	}
	if len(text) == 0 || text[0] < '0' || text[0] > '9' {
		return false
	}
	if value.Kind == "int" {
		return !strings.ContainsAny(text, ".eE") && (len(text) == 1 || text[0] != '0') && value.Text != "-0"
	}
	number, err := strconv.ParseFloat(value.Text, 64)
	return err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func label(value cataloginput.Value) (string, *Error) {
	if value.Kind != "string" {
		return "", &Error{Code: "EVALUATION_JOB_SHAPE"}
	}
	if len(value.Text) > maxLabelBytes {
		return "", &Error{Code: "EVALUATION_INPUT_LIMIT"}
	}
	return value.Text, nil
}
func (b *inputBudget) strings(value cataloginput.Value) ([]string, *Error) {
	if value.Kind != "sequence" {
		return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
	}
	b.requirements += len(value.Items)
	if b.requirements > MaxRequirements {
		return nil, &Error{Code: "EVALUATION_REQUIREMENT_LIMIT"}
	}
	out := make([]string, 0, len(value.Items))
	for _, item := range value.Items {
		text, err := label(item)
		if err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	return out, nil
}

func numericYears(value cataloginput.Value) (json.Number, float64, *Error) {
	if value.Kind != "int" && value.Kind != "float" {
		return "", 0, &Error{Code: "EVALUATION_JOB_SHAPE"}
	}
	number, err := strconv.ParseFloat(value.Text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 10000 {
		return "", 0, &Error{Code: "EVALUATION_NUMBER_UNSUPPORTED"}
	}
	return json.Number(value.Text), number, nil
}

// Retained provenance is copied, JSON-compatible and bounded independently. It
// remains untrusted quoted data, never an instruction or a new candidate fact.
func evidence(ctx context.Context, value cataloginput.Value) ([]any, *Error) {
	if value.Kind != "sequence" {
		return []any{}, nil
	}
	out := make([]any, 0, len(value.Items))
	budget := &resultBudget{ctx: ctx}
	for _, item := range value.Items {
		converted, err := jsonValue(ctx, item)
		if err != nil {
			return nil, err
		}
		if err := stopped(ctx); err != nil {
			return nil, err
		}
		if failure := budget.reserve(converted); failure != nil {
			return nil, failure
		}
		if budget.bytes > 256<<10 {
			return nil, &Error{Code: "EVALUATION_EVIDENCE_LIMIT"}
		}
		out = append(out, converted)
	}
	return out, nil
}

func jsonValue(ctx context.Context, value cataloginput.Value) (any, *Error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	switch value.Kind {
	case "null":
		return nil, nil
	case "string":
		return value.Text, nil
	case "bool":
		return value.Boolean, nil
	case "int", "float":
		return json.Number(value.Text), nil
	case "sequence":
		out := make([]any, 0, len(value.Items))
		for _, item := range value.Items {
			converted, err := jsonValue(ctx, item)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		return out, nil
	case "mapping":
		out := make(map[string]any, len(value.Entries))
		for _, entry := range value.Entries {
			converted, err := jsonValue(ctx, entry.Value)
			if err != nil {
				return nil, err
			}
			out[entry.Key.Text] = converted
		}
		return out, nil
	default:
		return nil, &Error{Code: "EVALUATION_EVIDENCE_UNSUPPORTED"}
	}
}

func project(ctx context.Context, input Input) (*job, *Error) {
	budget := &inputBudget{ctx: ctx}
	for _, value := range []cataloginput.Value{input.Job, input.Profile, input.Policy} {
		if err := budget.tree(value, 0); err != nil {
			return nil, err
		}
	}
	if input.Job.Kind != "mapping" || input.Profile.Kind != "mapping" || (input.Policy.Kind != "mapping" && input.Policy.Kind != "null") {
		return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
	}
	if status := get(input.Profile, "current_status"); len(status.Text) > maxLabelBytes {
		return nil, &Error{Code: "EVALUATION_INPUT_LIMIT"}
	}
	out := &job{gateEvidence: map[string][]any{}}
	var err *Error
	if out.id, err = label(get(input.Job, "id")); err != nil {
		return nil, err
	}
	if textcompat.Strip(out.id) == "" || len(out.id) > 1024 {
		return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
	}
	for _, entry := range []struct {
		key string
		to  *[]string
	}{
		{"required_skills", &out.requiredSkills}, {"required_platforms", &out.platforms},
		{"required_capabilities", &out.capabilities}, {"preferred_skills", &out.preferred},
		{"education_requirements", &out.education},
	} {
		if *entry.to, err = budget.strings(get(input.Job, entry.key)); err != nil {
			return nil, err
		}
	}
	out.signals = get(input.Job, "visa_signals")
	for _, key := range []string{"citizenship_required", "clearance_required", "no_visa_or_opt", "sponsorship_mentioned"} {
		value, present := field(out.signals, key)
		if !present || (value.Kind != "bool" && value.Kind != "null") {
			return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
		}
	}
	digest := get(input.Job, "digest")
	gates := get(digest, "eligibility_gates")
	for _, key := range []string{"citizenship", "clearance", "sponsorship"} {
		out.gateEvidence[key], err = evidence(ctx, get(get(gates, key), "evidence"))
		if err != nil {
			return nil, err
		}
	}
	// Digests before analyzer version 13 omit the signal; absence means the
	// requirement families must be read as recognized, never as missing.
	switch flag := get(get(digest, "analysis_coverage"), "no_requirement_sections"); flag.Kind {
	case "null":
	case "bool":
		out.noRequirementSections = flag.Boolean
	default:
		return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
	}
	groups, present := field(input.Job, "technology_groups")
	if !present {
		groups, present = field(digest, "technology_groups")
	}
	if present && groups.Kind != "sequence" {
		return nil, &Error{Code: "EVALUATION_GROUPS_UNSUPPORTED"}
	}
	for _, item := range groups.Items {
		level := get(item, "requirement")
		if level.Kind != "string" || (level.Text != "required" && level.Text != "preferred" && level.Text != "mentioned") {
			return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
		}
		operator := get(item, "operator")
		if operator.Kind != "string" || operator.Text != "any_of" {
			return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
		}
		alternatives, err := budget.strings(get(item, "technologies"))
		if err != nil {
			return nil, err
		}
		if len(alternatives) < 2 {
			return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
		}
		out.groups = append(out.groups, technologyGroup{level.Text, alternatives})
	}
	values := get(input.Job, "experience_requirements")
	if values.Kind != "sequence" {
		return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
	}
	provenance := get(get(digest, "provenance"), "experience_requirements")
	for i, item := range values.Items {
		budget.requirements++
		req := experience{evidence: []any{}}
		req.minimum, req.threshold, err = numericYears(get(item, "minimum_years"))
		if err != nil {
			return nil, err
		}
		req.label = req.minimum.String() + "+"
		if maximum, present := field(item, "maximum_years"); present {
			if maximum.Kind == "null" {
				req.maximum = json.RawMessage("null")
				req.label = req.minimum.String() + "-None"
			} else {
				number, threshold, err := numericYears(maximum)
				if err != nil {
					return nil, err
				}
				if threshold < req.threshold {
					return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
				}
				req.maximum = json.RawMessage(number.String())
				req.label = req.minimum.String() + "-" + number.String()
			}
		}
		if req.area, err = label(get(item, "area")); err != nil {
			return nil, err
		}
		if provenance.Kind == "sequence" && i < len(provenance.Items) && provenance.Items[i].Kind == "mapping" {
			req.evidence, err = evidence(ctx, cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{provenance.Items[i]}})
			if err != nil {
				return nil, err
			}
		}
		out.experience = append(out.experience, req)
	}
	values = get(digest, "classified_requirements")
	if values.Kind == "sequence" {
		for _, item := range values.Items {
			budget.requirements++
			req := classified{}
			for _, part := range []struct {
				key string
				to  *string
			}{
				{"category", &req.category}, {"name", &req.name}, {"requirement", &req.level}, {"text", &req.text},
			} {
				if *part.to, err = label(get(item, part.key)); err != nil {
					return nil, err
				}
			}
			evidenceValue := get(item, "evidence")
			if evidenceValue.Kind != "sequence" {
				return nil, &Error{Code: "EVALUATION_JOB_SHAPE"}
			}
			if req.evidence, err = evidence(ctx, evidenceValue); err != nil {
				return nil, err
			}
			out.classified = append(out.classified, req)
		}
	}
	if budget.requirements > MaxRequirements {
		return nil, &Error{Code: "EVALUATION_REQUIREMENT_LIMIT"}
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
