package agentmaterials

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/textcompat"
)

type VocabularyInput struct {
	Name           string `json:"name"`
	PrimaryGroupID string `json:"primary_group_id"`
	SourceLine     *int   `json:"source_line,omitempty"`
	ConfirmedExact bool   `json:"confirmed_exact"`
}

type VocabularyWrite struct {
	IntentRecorded bool `json:"intent_recorded"`
	AppliedFiles   int  `json:"applied_files"`
	FilesComplete  bool `json:"files_complete"`
	JournalRemoved bool `json:"journal_removed"`
	Completed      bool `json:"completed"`
}

type VocabularyStage struct {
	Known                   *bool            `json:"known"`
	Committed               *bool            `json:"committed"`
	Partial                 *bool            `json:"partial"`
	CleanupFailed           *bool            `json:"cleanup_failed"`
	RecoveryRequired        bool             `json:"recovery_required,omitempty"`
	RevisionRefreshRequired bool             `json:"revision_refresh_required,omitempty"`
	RunID                   string           `json:"run_id,omitempty"`
	Write                   *VocabularyWrite `json:"write,omitempty"`
}

type VocabularyStages struct {
	Registry   *VocabularyStage `json:"registry"`
	Reanalysis *VocabularyStage `json:"reanalysis"`
}

var vocabularyRunID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{6}Z_[a-f0-9]{8}$`)

func validVocabularyStage(s *VocabularyStage) bool {
	if s == nil || s.Known == nil || s.Partial == nil || s.CleanupFailed == nil ||
		(*s.Known && s.Committed == nil) || (!*s.Known && (!s.RecoveryRequired || s.Committed != nil && !*s.Committed)) ||
		(s.RunID != "" && !vocabularyRunID.MatchString(s.RunID)) {
		return false
	}
	if w := s.Write; w != nil {
		if w.AppliedFiles < 0 || w.AppliedFiles > 1000 || w.Completed && (!w.IntentRecorded || !w.FilesComplete || !w.JournalRemoved) {
			return false
		}
	}
	return true
}

func validVocabularyStages(s *VocabularyStages) bool {
	return s != nil && validVocabularyStage(s.Registry) && (s.Reanalysis == nil || validVocabularyStage(s.Reanalysis))
}

type vocabularyLookup struct {
	ID            string  `json:"id"`
	CanonicalName string  `json:"canonical_name"`
	Origin        string  `json:"origin"`
	Category      string  `json:"category"`
	Kind          string  `json:"kind"`
	PrimaryDomain *string `json:"primary_domain"`
	PrimaryGroup  struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Origin string `json:"origin"`
	} `json:"primary_group"`
	Archived bool `json:"archived"`
}

type vocabularyCreated struct {
	catalog.Term
	Origin string `json:"origin"`
}

func vocabularyStageOK(s *VocabularyStage) bool {
	return s != nil && *s.Known && !*s.Partial && !*s.CleanupFailed && !s.RecoveryRequired && !s.RevisionRefreshRequired
}

func (c *Client) addExactVocabulary(ctx context.Context, r Request) ([]byte, *Error) {
	v := r.Vocabulary
	if c == nil || c.vocabularyHTTP == nil || v == nil || !jobcard.ValidID(r.JobID) || !v.ConfirmedExact ||
		r.Kind != "" || r.FileFormat != "" || r.Approve || r.ExpectedRevision != 0 || len(r.Payload) != 0 ||
		!utf8.ValidString(v.Name) || !utf8.ValidString(v.PrimaryGroupID) || textcompat.Strip(v.Name) == "" || utf8.RuneCountInString(textcompat.Strip(v.Name)) > 160 || len(v.PrimaryGroupID) > 160 ||
		(v.SourceLine != nil && (*v.SourceLine < 1 || *v.SourceLine > 200000)) {
		return nil, problem("AGENT_VOCABULARY_REQUEST_INVALID")
	}
	body, err := json.Marshal(v)
	if err != nil || len(body) > 4096 {
		return nil, problem("AGENT_VOCABULARY_REQUEST_INVALID")
	}
	// Only this two-stage command uses the longer fixed transport. Keep ordinary
	// material requests unchanged, and do not race by mutating a shared client.
	long := *c
	long.http = c.vocabularyHTTP
	token, problem := long.sessionFor(ctx, "exact_vocabulary")
	if problem != nil {
		return nil, problem
	}
	raw, problem := long.request(ctx, "POST", "/api/jobs/"+url.PathEscape(r.JobID)+"/vocabulary/exact", token, body, 128<<10)
	if problem != nil {
		return nil, problem
	}
	return vocabularyReceipt(raw, r)
}

func vocabularyReceipt(raw []byte, request Request) ([]byte, *Error) {
	var receipt struct {
		Schema           int               `json:"schema_version"`
		Contract         int               `json:"contract_version"`
		Result           string            `json:"result"`
		JobID            string            `json:"job_id"`
		SourceRef        string            `json:"source_ref"`
		Record           json.RawMessage   `json:"record"`
		CandidateChanged *bool             `json:"candidate_evidence_changed"`
		RegistryRevision string            `json:"registry_revision"`
		Reanalyzed       *bool             `json:"job_reanalyzed"`
		Warning          *string           `json:"reanalysis_warning"`
		Stages           *VocabularyStages `json:"stages"`
	}
	e := problem("AGENT_VOCABULARY_RESPONSE_INVALID")
	if json.Unmarshal(raw, &receipt) != nil {
		return nil, e
	}
	if validVocabularyStages(receipt.Stages) {
		e.Stages = receipt.Stages
	}
	prefix := "job:" + request.JobID + "#line:"
	line, lineErr := strconv.Atoi(strings.TrimPrefix(receipt.SourceRef, prefix))
	if receipt.Schema != 1 || receipt.Contract != ContractVersion || receipt.JobID != request.JobID ||
		!strings.HasPrefix(receipt.SourceRef, prefix) || lineErr != nil || line < 1 || line > 200000 || receipt.SourceRef != prefix+strconv.Itoa(line) ||
		(request.Vocabulary.SourceLine != nil && line != *request.Vocabulary.SourceLine) || receipt.CandidateChanged == nil || *receipt.CandidateChanged ||
		e.Stages == nil || !vocabularyStageOK(receipt.Stages.Registry) {
		return nil, e
	}
	var recordFields map[string]json.RawMessage
	if json.Unmarshal(receipt.Record, &recordFields) != nil || !bytes.Equal(bytes.TrimSpace(recordFields["archived"]), []byte("false")) {
		return nil, e // Missing/null archived must not be invented as active.
	}
	out := map[string]any{"schema_version": 1, "contract_version": ContractVersion, "result": receipt.Result, "job_id": receipt.JobID,
		"source_ref": receipt.SourceRef, "candidate_evidence_changed": false, "stages": receipt.Stages}
	switch receipt.Result {
	case "no_change_needed":
		var record vocabularyLookup
		if w := receipt.Stages.Registry.Write; w != nil && (w.IntentRecorded || w.AppliedFiles != 0 || w.FilesComplete || w.JournalRemoved || w.Completed) {
			return nil, e
		}
		if *receipt.Stages.Registry.Committed || receipt.Stages.Reanalysis != nil || receipt.Reanalyzed != nil || receipt.RegistryRevision != "" || receipt.Warning != nil ||
			json.Unmarshal(receipt.Record, &record) != nil || record.ID == "" || record.CanonicalName == "" || record.Archived ||
			(record.Origin != "core" && record.Origin != "custom") || record.PrimaryGroup.ID == "" || record.PrimaryGroup.Name == "" {
			return nil, e
		}
		out["record"] = record
	case "created":
		var record vocabularyCreated
		if !*receipt.Stages.Registry.Committed || receipt.Stages.Registry.Write == nil || !receipt.Stages.Registry.Write.Completed ||
			receipt.Reanalyzed == nil || receipt.RegistryRevision == "" || len(receipt.RegistryRevision) > 128 ||
			json.Unmarshal(receipt.Record, &record) != nil || record.ID == "" || record.Name != textcompat.Strip(request.Vocabulary.Name) || record.Archived || record.Origin != "custom" || record.PrimaryGroupID == nil || *record.PrimaryGroupID != request.Vocabulary.PrimaryGroupID {
			return nil, e
		}
		if *receipt.Reanalyzed {
			if receipt.Warning != nil || !vocabularyStageOK(receipt.Stages.Reanalysis) {
				return nil, e
			}
		} else if receipt.Warning == nil || *receipt.Warning == "" || len(*receipt.Warning) > 512 {
			return nil, e
		}
		out["record"], out["registry_revision"], out["job_reanalyzed"], out["reanalysis_warning"] = record, receipt.RegistryRevision, *receipt.Reanalyzed, receipt.Warning
	default:
		return nil, e
	}
	// Re-encode only the typed projection, never arbitrary server metadata.
	return compactUTF8(out)
}
