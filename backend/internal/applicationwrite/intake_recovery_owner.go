package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
)

// RecoverIntakeWrites is an explicit, host-only recovery boundary. resourceRoot
// is trusted sidecar configuration, never a request value. runMode retains the
// process gate, Home marker, guarded SQLite files and IMMEDIATE transaction;
// no HTTP route or installed-app startup invokes this method yet.
func (s *Service) RecoverIntakeWrites(ctx context.Context, resourceRoot string) (*Outcome, *Error) {
	var pending bool
	var recovered intakeRecoveryResult
	var sourceID string
	var reportVerified bool
	out, problem := s.runMode(ctx, func(reads *sourceReads) (mutation, *Error) {
		sealed, present, e := reads.root.ReadSingleLinkFile(datahome.IntakeRecoveryPath, intakeIntentLimit)
		if e != nil {
			pending = true
			return nil, failed("intent", "INTAKE_INTENT_UNAVAILABLE")
		}
		pending = present
		if !present {
			return func(context.Context, *sql.Tx) (*applications.MutationResult, *Error) {
				if found, e := reads.root.Exists(datahome.IntakeRecoveryPath); e != nil || found {
					return nil, failed("intent", "INTAKE_INTENT_CHANGED")
				}
				return intakeRecoveryMutation(false, "none", "")
			}, nil
		}
		p, error := decodeIntakeIntent(sealed, reads.home)
		if error != nil {
			return nil, error
		}
		sourceID = p.SourceID
		resources, e := runtimepaths.Open(resourceRoot)
		if e != nil {
			return nil, failed("resources", e.Code)
		}
		reads.intakeResources = resources
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			live := func(ctx context.Context) ([]byte, bool, *Error) {
				return intakeLiveState(ctx, tx, reads.root, resources, p, s.now(), s.resumeEntropy)
			}
			var e *Error
			recovered, e = recoverFrozenIntake(ctx, reads.root, p, sealed, live, s.checkpoint)
			reportVerified = recovered.Facts.ReportPublished && intakeFileEquals(reads.root, p.ReportPath, p.Report)
			if e != nil {
				return nil, e
			}
			return intakeRecoveryMutation(true, recovered.State, p.SourceID)
		}, nil
	}, "intake")
	if out != nil {
		out.RecoveryRequired = pending && !recovered.Facts.IntentRemoved
		out.IntakeState = recovered.State
		out.IntakeSourceID = sourceID
		out.IntakeReportPublished = reportVerified
	}
	return out, problem
}

func intakeRecoveryMutation(recovered bool, state, sourceID string) (*applications.MutationResult, *Error) {
	raw, e := json.Marshal(struct {
		Recovered bool   `json:"recovered"`
		State     string `json:"state"`
		SourceID  string `json:"source_id,omitempty"`
	}{recovered, state, sourceID})
	if e != nil {
		return nil, failed("result", "INTAKE_RECOVERY_RESPONSE_INVALID")
	}
	return &applications.MutationResult{Result: raw, Changed: false}, nil
}

type intakeVersionReader struct {
	root        *runtimepaths.Root
	readProblem string
}

func (r *intakeVersionReader) ReadFile(name string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if !strings.HasPrefix(name, resumelibrary.LibraryPath+"/") || limit < 1 || limit > resumelibrary.MaxResumeBytes {
		r.readProblem = "INTAKE_SOURCE_PATH_INVALID"
		return nil, false, &runtimepaths.Error{Code: "PATH_INVALID"}
	}
	raw, present, e := r.root.ReadSingleLinkFile(name, limit)
	if e != nil {
		r.readProblem = e.Code
	}
	return raw, present, e
}

type intakeCatalogReader struct{ root *runtimepaths.Root }

func (r intakeCatalogReader) Check(name string) *runtimepaths.Error { return r.root.Check(name) }
func (r intakeCatalogReader) ReadFile(name string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	return r.root.ReadSingleLinkFile(name, limit)
}

func intakeLiveState(ctx context.Context, tx *sql.Tx, data, resources *runtimepaths.Root, p intakeIntent, now string, entropy resumelibrary.Entropy) ([]byte, bool, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, false, failed("context", "INTAKE_CANCELED")
	}
	witness, e := resumelibrary.MutationWitness(ctx, tx, p.Resume.ID)
	if e != nil {
		return nil, false, failed("database", "INTAKE_SOURCE_UNAVAILABLE")
	}
	if witness != p.Resume.Row {
		return nil, false, nil
	}
	reader := &intakeVersionReader{root: data}
	version, e := resumelibrary.ApplyObservedInTransaction(ctx, tx, resumelibrary.Request{
		Operation: "version_file", Fields: map[string]any{"resume_id": p.Resume.ID, "require_active": true},
	}, now, entropy, reader, nil)
	if e != nil {
		if reader.readProblem != "" {
			return nil, false, failed("source", "INTAKE_SOURCE_UNAVAILABLE")
		}
		switch e.SourceClass {
		case "ResumeNotFound", "ResumeVersionArchived", "ResumeVersionFileMissing", "ResumeVersionFileInvalid":
			return nil, false, nil
		}
		return nil, false, failed("source", "INTAKE_SOURCE_UNAVAILABLE")
	}
	var metadata struct {
		ID       string `json:"id"`
		FileName string `json:"file_name"`
		SHA256   string `json:"sha256"`
		Size     int    `json:"size_bytes"`
		Archived bool   `json:"archived"`
	}
	if json.Unmarshal(version.Mutation.Result, &metadata) != nil || !version.IsFile || metadata.ID != p.Resume.ID ||
		metadata.FileName != p.Resume.FileName || !strings.EqualFold(metadata.SHA256, p.Resume.SHA256) || metadata.Size != p.Resume.Size ||
		metadata.Archived || len(version.Bytes) != p.Resume.Size || digestBytes(version.Bytes) != p.Resume.SHA256 {
		return nil, false, nil
	}
	matching, problem := intakeCatalogMatches(ctx, resources, data, p)
	if problem != nil || !matching {
		return nil, false, problem
	}
	return version.Bytes, true, nil
}

func intakeCatalogMatches(ctx context.Context, resources, data *runtimepaths.Root, p intakeIntent) (bool, *Error) {
	for _, w := range p.Catalog {
		if ctx.Err() != nil {
			return false, failed("context", "INTAKE_CANCELED")
		}
		root := data
		if w.Path == catalogstore.CoreGroupsPath || w.Path == catalogstore.CoreSkillsPath {
			root = resources
		}
		raw, present, e := root.ReadSingleLinkFile(w.Path, int64(2<<20))
		if e != nil {
			return false, failed("catalog", "INTAKE_CATALOG_UNAVAILABLE")
		}
		if present != w.Present || present && (len(raw) != w.Size || digestBytes(raw) != w.SHA256) {
			return false, nil
		}
	}
	state, e := catalogstore.ReadState(intakeCatalogReader{resources}, intakeCatalogReader{data})
	if e != nil {
		return false, failed("catalog", "INTAKE_CATALOG_UNAVAILABLE")
	}
	if state.Snapshot.CatalogRevision() != p.RegistryRevision {
		return false, nil
	}
	for _, w := range p.Catalog {
		root := data
		if w.Path == catalogstore.CoreGroupsPath || w.Path == catalogstore.CoreSkillsPath {
			root = resources
		}
		raw, present, e := root.ReadSingleLinkFile(w.Path, int64(2<<20))
		if e != nil {
			return false, failed("catalog", "INTAKE_CATALOG_UNAVAILABLE")
		}
		if present != w.Present || present && (len(raw) != w.Size || digestBytes(raw) != w.SHA256) {
			return false, nil
		}
	}
	return true, nil
}
