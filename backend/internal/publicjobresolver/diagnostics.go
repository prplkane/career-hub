package publicjobresolver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emaildiagnostics"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

// A resolver diagnostic contains only closed phases/counts and publication facts.
// It cannot authorize recovery, retries, networking, or another product write.
type Diagnostic struct {
	SchemaVersion    int    `json:"schema_version"`
	Operation        string `json:"operation"`
	OperationID      string `json:"operation_id"`
	RunID            string `json:"run_id"`
	Sequence         int    `json:"sequence"`
	State            string `json:"state"`
	Stage            string `json:"stage"`
	ErrorCode        string `json:"error_code"`
	Outcome          string `json:"outcome"`
	Strategy         string `json:"strategy"`
	RedirectCount    int    `json:"redirect_count"`
	ResponseBytes    int    `json:"response_bytes"`
	Committed        bool   `json:"committed"`
	CommitUnknown    bool   `json:"commit_unknown"`
	Partial          bool   `json:"partial"`
	RecoveryRequired bool   `json:"recovery_required"`
	CleanupFailed    bool   `json:"cleanup_failed"`
}

var diagnosticID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var ErrDiagnostic = errors.New("PUBLIC_JOB_RESOLUTION_DIAGNOSTIC_FAILED")

func NewDiagnostic() (Diagnostic, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Diagnostic{}, ErrDiagnostic
	}
	text := hex.EncodeToString(id[:])
	return Diagnostic{SchemaVersion: 4, Operation: "public_job_resolution", OperationID: text, RunID: time.Now().UTC().Format("20060102T150405.000000Z") + "_" + text[:8], Sequence: 1, State: "running", Stage: "eligibility"}, nil
}
func ValidStage(stage string) bool {
	return slices.Contains([]string{"eligibility", "url_policy", "dns", "connect", "tls", "timeout", "redirect", "http", "response", "extract", "persist", "review", "complete"}, stage)
}
func PublicCode(code string) bool {
	return slices.Contains([]string{"PUBLIC_JOB_RESOLUTION_URL_REJECTED", "PUBLIC_JOB_RESOLUTION_PROTECTED_SOURCE", "PUBLIC_JOB_RESOLUTION_DNS_REJECTED", "PUBLIC_JOB_RESOLUTION_CONNECT_FAILED", "PUBLIC_JOB_RESOLUTION_TLS_FAILED", "PUBLIC_JOB_RESOLUTION_TIMEOUT", "PUBLIC_JOB_RESOLUTION_REDIRECT_REJECTED", "PUBLIC_JOB_RESOLUTION_TOO_MANY_REDIRECTS", "PUBLIC_JOB_RESOLUTION_HTTP_UNAVAILABLE", "PUBLIC_JOB_RESOLUTION_RATE_LIMITED", "PUBLIC_JOB_RESOLUTION_RESPONSE_TOO_LARGE", "PUBLIC_JOB_RESOLUTION_CONTENT_UNSUPPORTED", "PUBLIC_JOB_RESOLUTION_CONTENT_NOT_FOUND", "PUBLIC_JOB_RESOLUTION_CONTENT_AMBIGUOUS", "PUBLIC_JOB_RESOLUTION_FAILED"}, code)
}
func DiagnosticCode(code string) string {
	if PublicCode(code) || slices.Contains([]string{"PUBLIC_JOB_RESOLUTION_STALE", "PUBLIC_JOB_RESOLUTION_NOT_ELIGIBLE", "PUBLIC_JOB_RESOLUTION_DIAGNOSTIC_FAILED", "DISCOVERY_NOT_FOUND", "LOCAL_OPERATION_CANCELED", "LOCAL_RESPONSE_INVALID", "WORKSPACE_UNAVAILABLE"}, code) {
		return code
	}
	return "PUBLIC_JOB_RESOLUTION_FAILED"
}
func EncodeDiagnostic(r Diagnostic) ([]byte, error) {
	if r.SchemaVersion != 4 || r.Operation != "public_job_resolution" || !diagnosticID.MatchString(r.OperationID) || !emaildiagnostics.ValidRunID(r.RunID) || r.RunID[len(r.RunID)-8:] != r.OperationID[:8] || !ValidStage(r.Stage) || r.RedirectCount < 0 || r.RedirectCount > MaxRedirects || r.ResponseBytes < 0 || r.ResponseBytes > MaxResponseBytes || !slices.Contains([]string{"", "jobposting_jsonld", "semantic_container"}, r.Strategy) || !slices.Contains([]string{"", "updated", "created", "duplicate"}, r.Outcome) {
		return nil, ErrDiagnostic
	}
	if r.Sequence == 1 {
		clean := r
		clean.SchemaVersion = 0
		clean.Operation = ""
		clean.OperationID = ""
		clean.RunID = ""
		clean.Sequence = 0
		clean.State = ""
		clean.Stage = ""
		if r.State != "running" || r.Stage != "eligibility" || clean != (Diagnostic{}) {
			return nil, ErrDiagnostic
		}
	} else if r.Sequence == 2 {
		switch r.State {
		case "success":
			if r.Stage != "complete" || r.ErrorCode != "" || r.Outcome == "" || r.Strategy == "" || r.CommitUnknown || r.Partial || r.RecoveryRequired || r.CleanupFailed {
				return nil, ErrDiagnostic
			}
		case "failed":
			if r.Stage == "complete" || r.ErrorCode == "" || DiagnosticCode(r.ErrorCode) != r.ErrorCode || (r.Committed || r.CommitUnknown || r.RecoveryRequired) && !r.Partial {
				return nil, ErrDiagnostic
			}
		default:
			return nil, ErrDiagnostic
		}
	} else {
		return nil, ErrDiagnostic
	}
	return json.Marshal(r)
}
func DecodeDiagnostic(raw []byte) (Diagnostic, error) {
	var r Diagnostic
	if len(raw) > 4096 || json.Unmarshal(raw, &r) != nil {
		return r, ErrDiagnostic
	}
	canonical, err := EncodeDiagnostic(r)
	if err != nil || !bytes.Equal(raw, canonical) {
		return Diagnostic{}, ErrDiagnostic
	}
	return r, nil
}

// Initial admission uses the normal Home guard. Terminal logging may observe a
// Discovery recovery intent left by this operation; it cannot settle that intent.
func WriteDiagnostic(ctx context.Context, dataRoot string, expected datahome.Info, before *Diagnostic, after Diagnostic) (observed bool, problem error) {
	if ctx == nil || ctx.Err() != nil || !expected.Initialized || expected.DataHomeID == "" || expected.Channel == "" {
		return false, ErrDiagnostic
	}
	raw, err := EncodeDiagnostic(after)
	if err != nil {
		return false, err
	}
	if before == nil {
		if after.Sequence != 1 {
			return false, ErrDiagnostic
		}
	} else {
		if _, err := EncodeDiagnostic(*before); err != nil || before.Sequence != 1 || after.Sequence != 2 || before.OperationID != after.OperationID || before.RunID != after.RunID {
			return false, ErrDiagnostic
		}
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return false, ErrDiagnostic
	}
	defer release()
	root, e := runtimepaths.Open(dataRoot)
	if e != nil {
		return false, ErrDiagnostic
	}
	defer func() {
		if root.Close() != nil {
			problem = ErrDiagnostic
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return false, ErrDiagnostic
	}
	sameHome := func() bool {
		var home *datahome.Info
		var e *datahome.Error
		if before == nil {
			home, e = datahome.Inspect(dataRoot, &expected.Channel)
		} else {
			home, e = datahome.InspectOwnedForDiscoveryRecovery(root, &expected.Channel)
		}
		return e == nil && home != nil && *home == expected
	}
	if !sameHome() {
		return false, ErrDiagnostic
	}
	const directory = "career_vault/logs/native-intake"
	if _, e := root.EnsureDirectory(directory); e != nil {
		return false, ErrDiagnostic
	}
	if before == nil {
		entries, _, e := root.ListDirectoryFileMetadata(directory)
		if e != nil || len(entries) >= runtimepaths.MaxDirectoryEntries {
			return false, ErrDiagnostic
		}
	}
	if ctx.Err() != nil || !sameHome() {
		return false, ErrDiagnostic
	}
	name := directory + "/" + after.OperationID + ".json"
	var changed bool
	var writeErr *runtimepaths.Error
	if before == nil {
		changed, writeErr = root.CreateFile(name, raw)
	} else {
		prior, _ := EncodeDiagnostic(*before)
		changed, writeErr = root.ReplaceFile(name, prior, raw)
	}
	retained, found, readErr := root.ReadSingleLinkFile(name, 4096)
	observed = found && readErr == nil && bytes.Equal(raw, retained)
	if writeErr != nil || !changed || !observed || ctx.Err() != nil || !sameHome() {
		return observed, ErrDiagnostic
	}
	return observed, nil
}
