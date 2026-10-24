// Package agentcontext builds deterministic agent-facing context. Pure packet
// builders are separate from saved-input composition through caller-owned read
// ports. It never opens paths, mutates a Vault, invokes a model or submits jobs.
package agentcontext

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxTaskJSONCharacters = 16000

type Error struct{ Code string }

func (*Error) Error() string                { return "The agent context could not be prepared." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func failure(code string) *Error            { return &Error{Code: "AGENT_CONTEXT_" + code} }

type taskLimits struct {
	Characters *json.Number `json:"characters,omitempty"`
	Words      *json.Number `json:"words,omitempty"`
}
type taskFields struct {
	Schema      int        `json:"schema_version"`
	Purpose     string     `json:"purpose"`
	Kind        *string    `json:"kind,omitempty"`
	ExactPrompt *string    `json:"exact_prompt,omitempty"`
	Limits      taskLimits `json:"limits"`
	WorkbenchID *string    `json:"workbench_item_id,omitempty"`
	ResumeID    *string    `json:"resume_id,omitempty"`
}

// Values can only be built through validation. Callers cannot mutate task intent
// after it has been bound to a context request.
type TaskRequest struct{ fields taskFields }

func ValidPurpose(s string) bool {
	return slices.Contains([]string{"assessment", "application_text", "resume_delta"}, s)
}

var taskID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`)
var resumeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)
var integer = regexp.MustCompile(`^[1-9][0-9]*$`)

// NormalizeTask consumes decoded JSON using json.Decoder.UseNumber. The
// transport must enforce the raw 16,000-character limit and scalar escapes.
// Literal schema_version follows Pydantic equality (true and 1.0 normalize to
// 1); strict positive integer limits deliberately do not accept either type.
func NormalizeTask(payload map[string]any, purpose string) (*TaskRequest, *Error) {
	bad := func() (*TaskRequest, *Error) { return nil, failure("TASK_INVALID") }
	if payload == nil || !ValidPurpose(purpose) {
		return bad()
	}
	for key := range payload {
		if !slices.Contains([]string{"schema_version", "purpose", "kind", "exact_prompt", "limits", "workbench_item_id", "resume_id"}, key) {
			return bad()
		}
	}
	p, ok := payload["purpose"].(string)
	if !ok || p != purpose {
		return bad()
	}
	if v, exists := payload["schema_version"]; exists {
		valid := v == true
		if n, ok := v.(json.Number); ok && len(n) <= MaxTaskJSONCharacters {
			valid = n == "1"
			if strings.ContainsAny(string(n), ".eE") {
				f, err := strconv.ParseFloat(string(n), 64)
				valid = err == nil && f == 1
			}
		}
		if n, ok := v.(int); ok {
			valid = n == 1
		}
		if !valid {
			return bad()
		}
	}
	f := taskFields{Schema: 1, Purpose: purpose}
	for _, item := range []struct {
		key     string
		target  **string
		max     int
		pattern *regexp.Regexp
	}{
		{"kind", &f.Kind, 40, nil}, {"exact_prompt", &f.ExactPrompt, 4000, nil}, {"workbench_item_id", &f.WorkbenchID, 80, taskID}, {"resume_id", &f.ResumeID, 100, resumeID},
	} {
		v := payload[item.key]
		if v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok || !utf8.ValidString(s) || utf8.RuneCountInString(s) < 1 || utf8.RuneCountInString(s) > item.max || item.pattern != nil && !item.pattern.MatchString(s) {
			return bad()
		}
		*item.target = &s
	}
	if v, exists := payload["limits"]; exists {
		m, ok := v.(map[string]any)
		if !ok || m == nil {
			return bad()
		}
		for key := range m {
			if key != "characters" && key != "words" {
				return bad()
			}
		}
		for _, item := range []struct {
			key    string
			target **json.Number
		}{{"characters", &f.Limits.Characters}, {"words", &f.Limits.Words}} {
			if v := m[item.key]; v != nil {
				n, ok := v.(json.Number)
				if !ok || len(n) > 4300 || !integer.MatchString(string(n)) {
					return bad()
				}
				*item.target = &n
			}
		}
	}
	if purpose == "application_text" {
		if f.Kind == nil || !slices.Contains([]string{"cover_letter", "short_answer", "long_answer", "recruiter_message", "follow_up", "other"}, *f.Kind) || f.ExactPrompt == nil || f.ResumeID != nil {
			return bad()
		}
	} else if f.Kind != nil || f.ExactPrompt != nil || f.WorkbenchID != nil || f.Limits.Characters != nil || f.Limits.Words != nil || purpose == "assessment" && f.ResumeID != nil {
		return bad()
	}
	return &TaskRequest{fields: f}, nil
}

func TaskPayload(purpose string, task *TaskRequest) (json.RawMessage, *Error) {
	if !ValidPurpose(purpose) || task != nil && task.fields.Purpose != purpose {
		return nil, failure("TASK_PURPOSE_MISMATCH")
	}
	var out []byte
	var err error
	if task == nil {
		out, err = json.Marshal(struct {
			Provided bool   `json:"provided"`
			Purpose  string `json:"purpose"`
		}{false, purpose})
	} else {
		out, err = json.Marshal(struct {
			Provided bool `json:"provided"`
			taskFields
		}{true, task.fields})
	}
	if err != nil {
		return nil, failure("TASK_INVALID")
	}
	return out, nil
}
