package applicationwrite

import (
	"encoding/json"
	"path"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/intakeextract"
	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/textcompat"
)

// This is only a pure recovery-intent codec. No writer or recovery command
// currently calls it. Its base64-encoded report fits below CreateFile's 16 MiB
// ceiling and ReadSingleLinkFile's ceiling; the report itself must fit the
// stricter 2 MiB extraction reader before any publication can be attempted.
const intakeIntentLimit = 4 << 20
const intakeDiagnosticLimit = 64 << 10

type intakeResumeIdentity struct {
	ID       string                `json:"id"`
	FileName string                `json:"file_name"`
	SHA256   string                `json:"sha256"`
	Size     int                   `json:"size_bytes"`
	Row      resumelibrary.Witness `json:"row"`
}

type intakeCatalogWitness struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Size    int    `json:"size"`
	SHA256  string `json:"sha256"`
}

type intakeDiagnostic struct {
	SchemaVersion int    `json:"schema_version"`
	OperationID   string `json:"operation_id"`
	SourceID      string `json:"source_id"`
	State         string `json:"state"`
	Stage         string `json:"stage"`
	ErrorCode     string `json:"error_code"`
}

type intakeIntent struct {
	Version               int                    `json:"version"`
	Home                  datahome.Info          `json:"home"`
	OperationID           string                 `json:"operation_id"`
	Resume                intakeResumeIdentity   `json:"resume"`
	Catalog               []intakeCatalogWitness `json:"catalog"`
	RegistryRevision      string                 `json:"registry_revision"`
	RawPath               string                 `json:"raw_path"`
	SourceID              string                 `json:"source_id"`
	ReportPath            string                 `json:"report_path"`
	DiagnosticPath        string                 `json:"diagnostic_path"`
	Report                []byte                 `json:"report"`
	ReportSHA256          string                 `json:"report_sha256"`
	ProvisionalDiagnostic []byte                 `json:"provisional_diagnostic"`
	TerminalDiagnostic    []byte                 `json:"terminal_diagnostic"`
}

func intakeID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, b := range s {
		if !strings.ContainsRune("0123456789abcdef", b) {
			return false
		}
	}
	return true
}

func intakeResumeID(s string) bool {
	// The DB identifier is never a path component: an older imported identifier
	// may contain punctuation or Unicode. Only its digest enters the raw name.
	return s != "" && len(s) <= 4096 && utf8.ValidString(s) && textcompat.Strip(s) == s
}

func intakeFileName(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 255 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 || strings.ContainsRune("/\\:", r) {
			return false
		}
	}
	return true
}

func intakeSlug(stem string) string {
	var b strings.Builder
	separator := false
	for _, c := range stem {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			separator = false
		} else if b.Len() > 0 && !separator {
			b.WriteByte('_')
			separator = true
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}

func validIntakeHome(h datahome.Info) bool {
	if h.FormatVersion != 2 || !h.Initialized || (h.Channel != "dev" && h.Channel != "stable") ||
		len(h.DataHomeID) != 35 || !strings.HasPrefix(h.DataHomeID, "dh_") {
		return false
	}
	return validHomeIDTail(h.DataHomeID[3:])
}

func validHomeIDTail(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, b := range s {
		if !strings.ContainsRune("0123456789abcdef", b) {
			return false
		}
	}
	return true
}

func validIntakeDiagnostic(raw []byte, id, source, state string) bool {
	if len(raw) == 0 || len(raw) > intakeDiagnosticLimit {
		return false
	}
	var d intakeDiagnostic
	if !canonicalIntentJSON(raw, &d) || d.SchemaVersion != 1 || d.OperationID != id || d.SourceID != source || d.State != state {
		return false
	}
	if state == "publishing" {
		return d.Stage == "publication" && d.ErrorCode == ""
	}
	return d.Stage == "completed" && d.ErrorCode == ""
}

func validIntakeCatalog(items []intakeCatalogWitness) bool {
	paths := []string{catalogstore.CoreGroupsPath, catalogstore.CustomGroupsPath, catalogstore.CoreSkillsPath, catalogstore.CustomSkillsPath}
	if len(items) != len(paths) {
		return false
	}
	for i, w := range items {
		if w.Path != paths[i] || w.Size < 0 || w.Size > int(intakeread.MaxExtractionBytes) {
			return false
		}
		if w.Present {
			if !validDigest(w.SHA256) {
				return false
			}
		} else if w.Size != 0 || w.SHA256 != "" {
			return false
		}
	}
	return true
}

func validIntakeIntent(p intakeIntent) bool {
	if p.Version != 1 || !validIntakeHome(p.Home) || !intakeID(p.OperationID) || !intakeResumeID(p.Resume.ID) ||
		!intakeFileName(p.Resume.FileName) || !validDigest(p.Resume.SHA256) || p.Resume.Size < 1 || p.Resume.Size > resumelibrary.MaxResumeBytes ||
		p.Resume.Row.ResumeID != p.Resume.ID || !p.Resume.Row.Present || !validDigest(p.Resume.Row.SHA256) ||
		!validIntakeCatalog(p.Catalog) || !validDigest(p.RegistryRevision) {
		return false
	}
	suffix := strings.ToLower(path.Ext(p.Resume.FileName))
	if suffix != ".txt" && suffix != ".docx" {
		return false
	}
	wantRaw := "career_vault/raw/resume_" + digestBytes([]byte(p.Resume.ID))[:24] + "_digest_" + p.OperationID + suffix
	if p.RawPath != wantRaw || len(p.RawPath) > 255 {
		return false
	}
	wantID := "src_" + intakeSlug(strings.TrimSuffix(path.Base(wantRaw), suffix)) + "_" + p.Resume.SHA256[:10]
	if p.SourceID != wantID || len(p.SourceID) > 255 ||
		p.ReportPath != "career_vault/intake_runs/"+wantID+"/extracted.yaml" ||
		p.DiagnosticPath != "career_vault/logs/native-intake/"+p.OperationID+".json" {
		return false
	}
	if len(p.Report) == 0 || len(p.Report) > int(intakeread.MaxExtractionBytes) || p.ReportSHA256 != digestBytes(p.Report) {
		return false
	}
	report, e := intakeread.ParseExtraction(p.Report)
	if e != nil || report.SchemaVersion != "2" || report.ExtractorVersion != intakeextract.ExtractorVersion ||
		report.RegistryRevision != p.RegistryRevision || report.Source.ID != p.SourceID ||
		report.Source.Path != p.RawPath || report.Source.SHA256 != p.Resume.SHA256 ||
		report.Source.FileType != strings.TrimPrefix(suffix, ".") {
		return false
	}
	return validIntakeDiagnostic(p.ProvisionalDiagnostic, p.OperationID, p.SourceID, "publishing") &&
		validIntakeDiagnostic(p.TerminalDiagnostic, p.OperationID, p.SourceID, "success")
}

func encodeIntakeIntent(p intakeIntent) ([]byte, *Error) {
	if !validIntakeIntent(p) {
		return nil, failed("intent", "INTAKE_INTENT_INVALID")
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return nil, failed("intent", "INTAKE_INTENT_INVALID")
	}
	raw, err := json.Marshal(discoveryEnvelope{Payload: payload, SHA256: digestBytes(payload)})
	if err != nil || len(raw) > intakeIntentLimit {
		return nil, failed("intent", "INTAKE_INTENT_LIMIT")
	}
	return raw, nil
}

func decodeIntakeIntent(raw []byte, home datahome.Info) (intakeIntent, *Error) {
	var envelope discoveryEnvelope
	var p intakeIntent
	if len(raw) == 0 || len(raw) > intakeIntentLimit || !canonicalIntentJSON(raw, &envelope) ||
		envelope.SHA256 != digestBytes(envelope.Payload) || !canonicalIntentJSON(envelope.Payload, &p) || !validIntakeIntent(p) {
		return intakeIntent{}, failed("intent", "INTAKE_INTENT_INVALID")
	}
	if p.Home != home {
		return intakeIntent{}, failed("intent", "INTAKE_INTENT_HOME_CHANGED")
	}
	return p, nil
}
