// Package jobcard validates the complete stored Job Card schema. Schema validity
// is not verification that every digest quote belongs to the retained JD.
package jobcard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

const MaxInputBytes = 16 << 20
const MaxIssues = 512
const maxNodes = 100000

// ValidationVersion is the composed assessment-cache validation contract, not
// the stored digest schema. Version 2 rejects the source's scalar/path holes.
const ValidationVersion uint32 = 2

type Error struct{ Code string }

func (*Error) Error() string                { return "Job Card validation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }

type Issue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}
type Report struct {
	Issues []Issue `json:"issues"`
}

func (r *Report) SchemaValid() bool { return r != nil && len(r.Issues) == 0 }

// RawFileCheck observes whether the already lexically confined path identifies
// an existing regular file. The owned adapter must reject links/aliases/special
// files and observe cancellation; it must not follow an arbitrary filename URL.
type RawFileCheck func(context.Context, string) (bool, *Error)

type validator struct {
	ctx          context.Context
	issues       []Issue
	failure      *Error
	nodes, bytes int
}

func (v *validator) active() bool {
	if v.failure != nil {
		return false
	}
	if v.ctx == nil || v.ctx.Err() != nil {
		v.failure = &Error{Code: "JOB_CARD_CANCELED"}
		return false
	}
	return true
}
func (v *validator) add(field, message string) {
	if !v.active() {
		return
	}
	if len(v.issues) >= MaxIssues {
		v.failure = &Error{Code: "JOB_CARD_ISSUE_LIMIT"}
		return
	}
	v.issues = append(v.issues, Issue{field, message})
}
func get(value cataloginput.Value, key string) cataloginput.Value {
	item, _ := lookup(value, key)
	return item
}
func lookup(value cataloginput.Value, key string) (cataloginput.Value, bool) {
	if value.Kind == "mapping" {
		for _, entry := range value.Entries {
			if entry.Key.Kind == "string" && entry.Key.Text == key {
				return entry.Value, true
			}
		}
	}
	return cataloginput.Value{Kind: "null"}, false
}
func has(value cataloginput.Value, key string) bool { _, present := lookup(value, key); return present }
func nonempty(value cataloginput.Value) bool {
	return value.Kind == "string" && textcompat.Strip(value.Text) != ""
}
func member(value cataloginput.Value, values ...string) bool {
	if value.Kind == "string" {
		for _, text := range values {
			if value.Text == text {
				return true
			}
		}
	}
	return false
}
func boolean(value cataloginput.Value) bool { return value.Kind == "bool" }
func truthy(value cataloginput.Value) bool {
	result := cataloginput.PrepareCatalog(value, false)
	return result.Kind != "sequence" || len(result.Items) != 0
}
func fieldPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
func itemPath(parent string, index int) string { return fmt.Sprintf("%s[%d]", parent, index) }
func (v *validator) mapping(value cataloginput.Value, path string) bool {
	if value.Kind != "mapping" {
		v.add(path, "expected mapping")
		return false
	}
	return v.active()
}
func (v *validator) list(value cataloginput.Value, path string, nonempty bool) bool {
	message := "expected list"
	if nonempty {
		message = "expected non-empty list"
	}
	if value.Kind != "sequence" || nonempty && len(value.Items) == 0 {
		v.add(path, message)
		return false
	}
	return v.active()
}
func (v *validator) strings(value cataloginput.Value, path string, required bool, fields ...string) {
	for _, key := range fields {
		item := get(value, key)
		if required && !nonempty(item) {
			v.add(fieldPath(path, key), "expected non-empty string")
		} else if !required && item.Kind != "string" {
			v.add(fieldPath(path, key), "expected string")
		}
	}
}
func integer(value cataloginput.Value) (*big.Int, bool) {
	if value.Kind != "int" {
		return nil, false
	}
	result, ok := new(big.Int).SetString(value.Text, 10)
	return result, ok
}
func atLeast(value cataloginput.Value, minimum int64) bool {
	result, ok := integer(value)
	return ok && result.Cmp(big.NewInt(minimum)) >= 0
}
func equalInteger(value cataloginput.Value, want int) bool {
	result, ok := integer(value)
	return ok && result.Cmp(big.NewInt(int64(want))) == 0
}
func compareIntegers(left, right cataloginput.Value, strict bool) bool {
	a, okA := integer(left)
	b, okB := integer(right)
	return okA && okB && (a.Cmp(b) > 0 || !strict && a.Cmp(b) == 0)
}
func numeric(value cataloginput.Value) (*big.Rat, bool) {
	if value.Kind == "int" {
		number, ok := integer(value)
		if ok {
			return new(big.Rat).SetInt(number), true
		}
		return nil, false
	}
	if value.Kind == "float" {
		number, err := strconv.ParseFloat(value.Text, 64)
		if err == nil && !math.IsInf(number, 0) && !math.IsNaN(number) {
			return new(big.Rat).SetFloat64(number), true
		}
	}
	return nil, false
}
func nonnegative(value cataloginput.Value) bool {
	number, ok := numeric(value)
	return ok && number.Sign() >= 0
}
func numericBelow(left, right cataloginput.Value) bool {
	a, okA := numeric(left)
	b, okB := numeric(right)
	return okA && okB && a.Cmp(b) < 0
}
func hexDigest(value cataloginput.Value) bool {
	if value.Kind != "string" || len(value.Text) != 64 {
		return false
	}
	for _, r := range value.Text {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func stableID(value cataloginput.Value) bool {
	if value.Kind != "string" || len(value.Text) == 0 {
		return false
	}
	for i, r := range value.Text {
		if !(r >= 'a' && r <= 'z' || i > 0 && (r >= '0' && r <= '9' || r == '_')) {
			return false
		}
	}
	return true
}

func (v *validator) tree(value cataloginput.Value, depth int) bool {
	if !v.active() {
		return false
	}
	v.nodes++
	v.bytes += len(value.Text) + len(value.Bytes)
	if depth > cataloginput.YAMLMaxDepth || v.nodes > maxNodes || v.bytes > MaxInputBytes {
		v.failure = &Error{Code: "JOB_CARD_INPUT_LIMIT"}
		return false
	}
	if !utf8.ValidString(value.Text) {
		v.failure = &Error{Code: "JOB_CARD_INPUT_UNSUPPORTED"}
		return false
	}
	if value.Kind != "sequence" && len(value.Items) != 0 || value.Kind != "mapping" && len(value.Entries) != 0 || value.Kind != "bytes" && len(value.Bytes) != 0 || value.Kind != "bool" && value.Boolean {
		v.failure = &Error{Code: "JOB_CARD_INPUT_UNSUPPORTED"}
		return false
	}
	if value.Kind == "null" || value.Kind == "bool" || value.Kind == "bytes" || value.Kind == "sequence" || value.Kind == "mapping" {
		if value.Text != "" {
			v.failure = &Error{Code: "JOB_CARD_INPUT_UNSUPPORTED"}
			return false
		}
	}
	switch value.Kind {
	case "null", "string", "bool", "bytes", "date", "datetime":
	case "int":
		text := value.Text
		if len(text) > 0 && text[0] == '-' {
			text = text[1:]
		}
		valid := len(text) > 0 && len(text) <= 1024 && (len(text) == 1 || text[0] != '0') && value.Text != "-0"
		for _, r := range text {
			if r < '0' || r > '9' {
				valid = false
				break
			}
		}
		if !valid {
			v.failure = &Error{Code: "JOB_CARD_INPUT_UNSUPPORTED"}
			return false
		}
	case "float":
		number, err := strconv.ParseFloat(value.Text, 64)
		if len(value.Text) > 1024 || err != nil || !json.Valid([]byte(value.Text)) || strings.TrimSpace(value.Text) != value.Text || math.IsNaN(number) || math.IsInf(number, 0) {
			v.failure = &Error{Code: "JOB_CARD_INPUT_UNSUPPORTED"}
			return false
		}
	case "sequence":
		if len(value.Items) > maxNodes-v.nodes {
			v.failure = &Error{Code: "JOB_CARD_INPUT_LIMIT"}
			return false
		}
		for _, item := range value.Items {
			if !v.tree(item, depth+1) {
				return false
			}
		}
	case "mapping":
		if len(value.Entries) > (maxNodes-v.nodes)/2 {
			v.failure = &Error{Code: "JOB_CARD_INPUT_LIMIT"}
			return false
		}
		seen := map[string]bool{}
		for _, entry := range value.Entries {
			if entry.Key.Kind != "string" || seen[entry.Key.Text] {
				v.failure = &Error{Code: "JOB_CARD_INPUT_UNSUPPORTED"}
				return false
			}
			seen[entry.Key.Text] = true
			if !v.tree(entry.Key, depth+1) || !v.tree(entry.Value, depth+1) {
				return false
			}
		}
	default:
		v.failure = &Error{Code: "JOB_CARD_INPUT_UNSUPPORTED"}
		return false
	}
	return true
}

var requiredFields = []string{"id", "status", "company", "title", "source_url", "location", "remote_policy", "seniority", "required_skills", "required_platforms", "required_capabilities", "preferred_skills", "experience_requirements", "education_requirements", "responsibilities", "visa_signals", "raw_jd_path"}
var stringFields = []string{"id", "status", "company", "title", "source_url", "location", "remote_policy", "seniority", "raw_jd_path"}
var listFields = []string{"required_skills", "required_platforms", "required_capabilities", "preferred_skills", "education_requirements", "responsibilities", "notes", "manual_overrides"}
var visaFields = []string{"citizenship_required", "clearance_required", "sponsorship_mentioned", "no_visa_or_opt"}

// Validate returns every ordered schema issue or a nil report and operational
// error. No partially inspected report is returned on limit/cancellation/I/O
// failure. The caller must keep the decoded input unchanged during this call.
func Validate(ctx context.Context, data cataloginput.Value, rawCheck RawFileCheck) (*Report, *Error) {
	return validate(ctx, data, rawCheck, "incoming")
}

// ValidateDiscoveryPreview checks an in-memory advisory projection against the
// separately confined discovery-capture subtree. It never treats a Feed item
// as a saved Job Card, creates a placeholder file, or permits a caller-selected
// root. The owned capture reader must verify the file bytes/hash and provide
// the exact already-retained path to rawCheck.
func ValidateDiscoveryPreview(ctx context.Context, data cataloginput.Value, rawCheck RawFileCheck) (*Report, *Error) {
	return validate(ctx, data, rawCheck, "discovery/captures")
}

func validate(ctx context.Context, data cataloginput.Value, rawCheck RawFileCheck, rawFolder string) (*Report, *Error) {
	v := &validator{ctx: ctx, issues: []Issue{}}
	if !v.tree(data, 0) {
		return nil, v.failure
	}
	if data.Kind != "mapping" {
		names := map[string]string{"null": "NoneType", "string": "str", "sequence": "list", "bool": "bool", "int": "int", "float": "float", "bytes": "bytes", "date": "date", "datetime": "datetime"}
		v.add("", "expected mapping, got "+names[data.Kind])
	} else {
		missing := []string{}
		for _, key := range requiredFields {
			if !has(data, key) {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			v.add("", "missing required fields: "+strings.Join(missing, ", "))
		}
		for _, key := range stringFields {
			if has(data, key) && get(data, key).Kind != "string" {
				v.add(key, "expected string")
			}
		}
		for _, key := range listFields {
			if !has(data, key) {
				continue
			}
			value := get(data, key)
			if !v.list(value, key, false) {
				continue
			}
			for i, item := range value.Items {
				if !v.active() {
					break
				}
				if !nonempty(item) {
					v.add(itemPath(key, i), "expected non-empty string")
				}
			}
		}
		v.experience(data)
		v.groups(data)
		v.compensation(data)
		status := get(data, "status")
		if status.Kind == "string" && !member(status, "draft", "ready") {
			v.add("status", "expected one of: draft, ready")
		}
		if member(status, "ready") {
			for _, key := range []string{"company", "title"} {
				if !nonempty(get(data, key)) {
					v.add(key, "required when status is ready")
				}
			}
			responsibilities := get(data, "responsibilities")
			if responsibilities.Kind == "sequence" && len(responsibilities.Items) == 0 {
				v.add("responsibilities", "must contain at least one item when status is ready")
			}
			required := false
			for _, key := range []string{"required_skills", "required_platforms", "required_capabilities", "experience_requirements", "education_requirements"} {
				value := get(data, key)
				if value.Kind == "sequence" && len(value.Items) > 0 {
					required = true
				}
			}
			groups, _ := technologyGroups(data)
			if groups.Kind == "sequence" {
				for _, group := range groups.Items {
					if group.Kind == "mapping" && member(get(group, "requirement"), "required") {
						required = true
					}
				}
			}
			if !required {
				v.add("", "ready card must contain at least one explicit requirement or required technology group")
			}
		}
		v.visa(data)
		v.digest(data)
		v.sourceURL(data)
		v.rawPath(data, rawCheck, rawFolder)
		if id := get(data, "id"); id.Kind == "string" && !nonempty(id) {
			v.add("id", "must not be empty")
		}
	}
	if !v.active() {
		return nil, v.failure
	}
	return &Report{Issues: v.issues}, nil
}
