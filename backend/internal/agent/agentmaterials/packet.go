// Package agentmaterials adapts the existing material workflow for local agents.
// It does not own data, generate prose or expose human fact-confirmation actions.
package agentmaterials

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"unicode/utf8"

	"careerhub.local/backend/internal/materials"
)

const ContractVersion = 4

type Error struct {
	Code              string             `json:"error_code"`
	State             OperationState     `json:"operation_state"`
	Stages            *VocabularyStages  `json:"stages,omitempty"`
	PreparationStages *PreparationStages `json:"preparation_stages,omitempty"`
	RunID             string             `json:"run_id,omitempty"`
}

func (*Error) Error() string {
	return "The local material-agent operation did not return a verified result."
}
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func problem(code string) *Error            { return &Error{Code: code} }

type evidence struct {
	ID          string `json:"id"`
	SourceType  string `json:"source_type"`
	SourceID    string `json:"source_id"`
	SourceLabel string `json:"source_label"`
	Claim       string `json:"claim"`
	Selected    *bool  `json:"selected_in_plan,omitempty"`
}
type evidenceRow struct {
	evidence
	Matches []string `json:"matched_job_skills"`
}
type editor struct {
	Draft     json.RawMessage `json:"draft"`
	Revision  int64           `json:"revision"`
	Persisted bool            `json:"persisted"`
	Catalog   struct {
		Evidence []evidenceRow `json:"evidence"`
	} `json:"catalog"`
	Validation materials.Validation    `json:"validation"`
	Preview    materials.Preview       `json:"preview"`
	Exports    materials.ExportOptions `json:"exports"`
}

func readEditor(ctx context.Context, job, kind string, raw []byte) (*editor, map[string]json.RawMessage, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, nil, problem("AGENT_MATERIAL_CANCELED")
	}
	if materials.ValidateRequest(materials.Request{JobID: job, Kind: kind, Operation: "load"}) != nil {
		return nil, nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
	}
	if len(raw) > materials.MaxResponseBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	var top map[string]json.RawMessage
	var e editor
	if json.Unmarshal(raw, &top) != nil || json.Unmarshal(raw, &e) != nil || top == nil || e.Revision < 0 {
		return nil, nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	for _, key := range []string{"draft", "revision", "persisted", "catalog", "validation", "preview", "exports"} {
		if len(top[key]) == 0 || bytes.Equal(top[key], []byte("null")) {
			return nil, nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
		}
	}
	if kind == "resume" {
		d, p := materials.ParseResume(e.Draft)
		if p != nil || d.JobID != job {
			return nil, nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
		}
	} else {
		d, p := materials.ParseCover(e.Draft)
		if p != nil || d.JobID != job {
			return nil, nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
		}
	}
	if e.Catalog.Evidence == nil || len(e.Catalog.Evidence) > 10000 || e.Validation.Issues == nil || len(e.Validation.Issues) > 1000 {
		return nil, nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	seen := map[string]bool{}
	for _, row := range e.Catalog.Evidence {
		if row.ID == "" || seen[row.ID] || row.SourceType == "" || row.Claim == "" || kind == "resume" && (row.Selected == nil || !*row.Selected) {
			return nil, nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
		}
		seen[row.ID] = true
	}
	var draft map[string]json.RawMessage
	if json.Unmarshal(e.Draft, &draft) != nil {
		return nil, nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	return &e, draft, nil
}

type editingContract struct {
	CodeOwned     []string `json:"code_owned"`
	AgentFocus    []string `json:"agent_focus"`
	EvidenceRule  string   `json:"evidence_rule"`
	NoForcing     string   `json:"no_forcing"`
	SaveCommand   string   `json:"save_command"`
	ExportCommand string   `json:"export_command"`
}

func contract(kind string) editingContract {
	c := editingContract{CodeOwned: []string{"schema_version", "id", "kind", "template_id", "job_id", "job_fingerprint", "profile_revision", "evidence_revision"}, SaveCommand: "save-material", ExportCommand: "render-material"}
	if kind == "resume" {
		c.CodeOwned = append(c.CodeOwned, "plan_revision", "plan_fingerprint", "company", "role_title", "target_pages", "sections", "experiences[*].block_id/company/role/dates/location", "projects[*].block_id/name/descriptor", "skills", "education")
		c.AgentFocus = []string{"summary", "summary_evidence_refs", "experiences[*].bullets[*].text/evidence_refs", "projects[*].bullets[*].text/evidence_refs"}
		c.EvidenceRule = "Every summary or bullet claim must cite proof IDs already selected in the approved Resume Plan; do not add JD vocabulary as candidate evidence."
		c.NoForcing = "If imported wording is already strong and relevant, preserve it."
	} else {
		c.AgentFocus = []string{"content.opening", "content.evidence_lead_in", "content.evidence_items", "content.closing"}
		c.EvidenceRule = "Every factual claim must cite catalog evidence_refs or include a specific manual_checks item; do not infer candidate skills from the JD."
		c.NoForcing = "If the reusable wording already fits, change only job-specific fields."
	}
	return c
}

func selectEvidence(rows []evidenceRow, kind string) []evidenceRow {
	limit := 12
	if kind == "resume" {
		limit = 30
		rows = append([]evidenceRow{}, rows...)
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			if (len(a.Matches) > 0) != (len(b.Matches) > 0) {
				return len(a.Matches) > 0
			}
			if a.SourceType != b.SourceType {
				return a.SourceType < b.SourceType
			}
			if a.SourceLabel != b.SourceLabel {
				return a.SourceLabel < b.SourceLabel
			}
			return a.ID < b.ID
		})
		return rows[:min(limit, len(rows))]
	}
	selected, fallback := []evidenceRow{}, []evidenceRow{}
	for _, row := range rows {
		if len(row.Matches) > 0 {
			selected = append(selected, row)
		} else {
			fallback = append(fallback, row)
		}
	}
	if len(selected) >= limit {
		return selected[:limit]
	}
	buckets := map[string][]evidenceRow{}
	types := []string{}
	for _, name := range []string{"experience", "project"} {
		for _, row := range fallback {
			if row.SourceType == name {
				types = append(types, name)
				break
			}
		}
	}
	for _, row := range fallback {
		if _, exists := buckets[row.SourceType]; !exists && row.SourceType != "experience" && row.SourceType != "project" {
			types = append(types, row.SourceType)
		}
		buckets[row.SourceType] = append(buckets[row.SourceType], row)
	}
	for len(selected) < limit {
		before := len(selected)
		for _, name := range types {
			if len(buckets[name]) == 0 {
				continue
			}
			selected = append(selected, buckets[name][0])
			buckets[name] = buckets[name][1:]
			if len(selected) == limit {
				break
			}
		}
		if len(selected) == before {
			break
		}
	}
	return selected
}

// compactUTF8 retains field order and Python ensure_ascii=False quoting. Only
// strings are rewritten; literal backslash-u text is never mistaken for Unicode.
func compactUTF8(value any) ([]byte, *Error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > materials.MaxResponseBytes {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		if raw[i] != '"' {
			out = append(out, raw[i])
			i++
			continue
		}
		start := i
		i++
		for raw[i] != '"' {
			if raw[i] == '\\' {
				i++
			}
			i++
		}
		i++
		var s string
		if json.Unmarshal(raw[start:i], &s) != nil {
			return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
		}
		out = append(out, '"')
		for _, r := range s {
			switch r {
			case '"', '\\':
				out = append(out, '\\', byte(r))
			case '\b':
				out = append(out, '\\', 'b')
			case '\f':
				out = append(out, '\\', 'f')
			case '\n':
				out = append(out, '\\', 'n')
			case '\r':
				out = append(out, '\\', 'r')
			case '\t':
				out = append(out, '\\', 't')
			default:
				if r < 0x20 {
					out = append(out, []byte(fmt.Sprintf("\\u%04x", r))...)
				} else {
					out = utf8.AppendRune(out, r)
				}
			}
		}
		out = append(out, '"')
	}
	return out, nil
}

type vocabularyMatch struct {
	ID    string   `json:"evidence_id"`
	Terms []string `json:"terms"`
}
type packet struct {
	Schema   int    `json:"schema_version"`
	Contract int    `json:"contract_version"`
	Kind     string `json:"kind"`
	Job      struct {
		ID      json.RawMessage `json:"id"`
		Company json.RawMessage `json:"company"`
		Title   json.RawMessage `json:"title"`
	} `json:"job"`
	Revision   int64                `json:"revision"`
	Persisted  bool                 `json:"persisted"`
	Draft      json.RawMessage      `json:"draft"`
	Validation materials.Validation `json:"validation"`
	Preview    materials.Preview    `json:"preview"`
	Catalog    struct {
		Evidence  []evidence `json:"evidence"`
		Total     int        `json:"total"`
		Truncated bool       `json:"truncated"`
	} `json:"catalog"`
	Vocabulary struct {
		Matches []vocabularyMatch `json:"evidence_matches"`
		Policy  string            `json:"policy"`
	} `json:"job_vocabulary_metadata"`
	Exports materials.ExportOptions `json:"exports"`
	Editing editingContract         `json:"editing_contract"`
}

// Context projects only an owner-returned editor. No broad Vault read or new
// source lookup occurs here; selected Resume Plan evidence stays selected.
func Context(ctx context.Context, job, kind string, raw []byte) ([]byte, *Error) {
	e, d, err := readEditor(ctx, job, kind, raw)
	if err != nil {
		return nil, err
	}
	p := packet{Schema: 1, Contract: ContractVersion, Kind: kind, Revision: e.Revision, Persisted: e.Persisted, Draft: e.Draft, Validation: e.Validation, Preview: e.Preview, Exports: e.Exports, Editing: contract(kind)}
	p.Job.ID, p.Job.Company, p.Job.Title = d["job_id"], d["company"], d["role_title"]
	p.Catalog.Evidence = []evidence{}
	p.Vocabulary.Matches, p.Vocabulary.Policy = []vocabularyMatch{}, "JD vocabulary metadata is not candidate evidence."
	for _, row := range selectEvidence(e.Catalog.Evidence, kind) {
		p.Catalog.Evidence = append(p.Catalog.Evidence, row.evidence)
		if len(row.Matches) > 0 {
			p.Vocabulary.Matches = append(p.Vocabulary.Matches, vocabularyMatch{row.ID, row.Matches})
		}
	}
	p.Catalog.Total, p.Catalog.Truncated = len(e.Catalog.Evidence), len(e.Catalog.Evidence) > len(p.Catalog.Evidence)
	before, err := compactUTF8(p)
	if err != nil {
		return nil, err
	}
	characters := utf8.RuneCount(before)
	budget := 5000
	if kind == "resume" {
		budget = 3000
	}
	digest := sha256.Sum256(before)
	metrics := struct {
		Characters  int    `json:"characters_before_metrics"`
		Tokens      int    `json:"approximate_tokens_at_four_characters"`
		Included    int    `json:"included_evidence_items"`
		Budget      int    `json:"token_budget_at_four_characters"`
		Within      bool   `json:"within_budget"`
		Fingerprint string `json:"packet_fingerprint"`
	}{characters, (characters + 3) / 4, len(p.Catalog.Evidence), budget, (characters+3)/4 <= budget, "sha256:" + hex.EncodeToString(digest[:])}
	m, err := compactUTF8(metrics)
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, problem("AGENT_MATERIAL_CANCELED")
	}
	result := append(append(append(before[:len(before)-1], []byte(",\"context_metrics\":")...), m...), '}')
	if len(result) > materials.MaxResponseBytes {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	return result, nil
}

func SaveReceipt(ctx context.Context, job, kind string, expected int64, raw []byte) ([]byte, *Error) {
	e, draft, err := readEditor(ctx, job, kind, raw)
	if err != nil {
		return nil, err
	}
	if expected < 0 || e.Revision < expected || !e.Persisted {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	result := "updated"
	if e.Revision == expected {
		result = "no_change_needed"
	}
	codes := []string{}
	for _, issue := range e.Validation.Issues {
		if issue.Code != "" {
			codes = append(codes, issue.Code)
		}
	}
	sources := map[string]json.RawMessage{}
	for _, key := range []string{"job_fingerprint", "profile_revision", "evidence_revision", "plan_revision", "plan_fingerprint"} {
		if raw := draft[key]; len(raw) > 0 && string(raw) != "null" {
			sources[key] = raw
		}
	}
	receipt := map[string]any{"schema_version": 1, "contract_version": ContractVersion, "result": result, "job_id": job, "kind": kind, "revision": e.Revision, "persisted": e.Persisted, "status": draft["status"], "validation": map[string]any{"ready_to_export": e.Validation.Ready, "issue_count": e.Validation.IssueCount, "blocking_save_count": e.Validation.BlockingSaveCount, "blocking_export_count": e.Validation.BlockingExportCount}, "issue_codes": codes[:min(12, len(codes))], "issue_codes_truncated": len(codes) > 12, "source_revisions": sources, "reload_required": false}
	return compactUTF8(receipt)
}

func Revision(raw string) (int64, bool) {
	n, e := strconv.ParseInt(raw, 10, 64)
	return n, e == nil && n >= 0
}
