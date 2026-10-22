package readonly

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/intakeextract"
	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/runtimepaths"
)

// agentDigestObservation borrows the packet owner's admitted roots and current
// SQL transaction. It retains its additional native run handle and witnesses
// until the owner has checked the whole packet. Close must run on failure too,
// before releasing the operation gate or publishing any response.
type agentDigestObservation struct {
	dataRoot                         string
	data, resources                  *runtimepaths.Root
	tx                               *sql.Tx
	catalogData, catalogResources    *catalogWitness
	runs                             *runtimepaths.Root
	observed                         *runtimepaths.ObservedRuns
	called, closed, indexed, present bool
}

func agentDigestFailure(code string) *agentcontext.Error {
	return &agentcontext.Error{Code: "AGENT_CONTEXT_DIGEST_" + code}
}

func (o *agentDigestObservation) Read(ctx context.Context, raw json.RawMessage) (json.RawMessage, *string, *agentcontext.Error) {
	if o == nil || o.called || o.closed || o.data == nil || o.resources == nil || o.tx == nil {
		return nil, nil, agentDigestFailure("LIFETIME_INVALID")
	}
	o.called = true
	if ctx == nil || ctx.Err() != nil {
		return nil, nil, agentDigestFailure("CANCELED")
	}
	// The upstream selector passes only these two public fields, at most eight
	// versions. Never accept paths, source hashes or externally chosen run IDs.
	var rows []map[string]json.RawMessage
	if len(raw) > applications.MaxResultBytes || json.Unmarshal(raw, &rows) != nil || len(rows) == 0 || len(rows) > 8 {
		return nil, nil, agentDigestFailure("SELECTION_INVALID")
	}
	ids := make([]string, len(rows))
	seen := map[string]bool{}
	for i, row := range rows {
		_, namePresent := row["display_name"]
		if len(row) != 2 || json.Unmarshal(row["id"], &ids[i]) != nil || ids[i] == "" || seen[ids[i]] || !namePresent {
			return nil, nil, agentDigestFailure("SELECTION_INVALID")
		}
		seen[ids[i]] = true
	}
	identities, e := applications.ResumeDigestIdentitiesInTransaction(ctx, o.tx)
	if e != nil {
		return nil, nil, agentDigestFailure("IDENTITIES_FAILED")
	}
	requested := map[string]bool{}
	for _, id := range ids {
		if hash := identities[id].SHA256; hash != "" {
			requested[hash] = true
		}
	}
	selected := map[string]selectedRun{}
	if len(requested) != 0 {
		o.indexed = true
		var pe *runtimepaths.Error
		o.present, pe = o.data.Exists(resumeRunsPath)
		if pe != nil {
			return nil, nil, agentDigestFailure("RUNS_REJECTED")
		}
		if o.present {
			o.runs, pe = runtimepaths.Open(filepath.Join(o.dataRoot, filepath.FromSlash(resumeRunsPath)))
			if pe != nil {
				return nil, nil, agentDigestFailure("RUNS_REJECTED")
			}
			var err error
			o.observed, err = scanManagementRuns(ctx, o.runs, requested, selected)
			if err != nil {
				return nil, nil, agentDigestFailure("RUNS_REJECTED")
			}
		}
	}
	o.catalogData = &catalogWitness{o.data, map[string]catalogFile{}}
	o.catalogResources = &catalogWitness{o.resources, map[string]catalogFile{}}
	state, ce := catalogstore.ReadState(o.catalogResources, o.catalogData)
	if ce != nil || state == nil {
		return nil, nil, agentDigestFailure("CATALOG_FAILED")
	}
	result := make([]map[string]any, len(rows))
	for i, id := range ids {
		identity := identities[id]
		run, found := selected[identity.SHA256]
		entry := map[string]any{"id": rows[i]["id"], "display_name": rows[i]["display_name"],
			"digest_status":    intakeread.DigestStatus(found, run.workflow, run.extraction.RegistryRevision, state.Snapshot.CatalogRevision(), run.extraction.ExtractorVersion, intakeextract.ExtractorVersion),
			"digest_supported": strings.EqualFold(filepath.Ext(identity.FileName), ".txt") || strings.EqualFold(filepath.Ext(identity.FileName), ".docx"),
			"digest_source_id": nil, "digest_processed_at": nil}
		if found {
			entry["digest_source_id"], entry["digest_processed_at"] = run.id, pythonUTC(run.modified)
		}
		result[i] = entry
	}
	if e := o.Check(ctx); e != nil {
		return nil, nil, e
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > applications.MaxResultBytes {
		return nil, nil, agentDigestFailure("RESULT_LIMIT")
	}
	return encoded, nil, nil
}

func (o *agentDigestObservation) Check(ctx context.Context) *agentcontext.Error {
	if o == nil || o.closed {
		return agentDigestFailure("LIFETIME_INVALID")
	}
	if ctx == nil || ctx.Err() != nil {
		return agentDigestFailure("CANCELED")
	}
	if !o.called {
		return nil
	} // No digest demand, no catalog or run access.
	if o.catalogData == nil || o.catalogResources == nil || !o.catalogData.recheck() || !o.catalogResources.recheck() {
		return agentDigestFailure("CATALOG_CHANGED")
	}
	if o.indexed {
		present, e := o.data.Exists(resumeRunsPath)
		if e != nil || present != o.present {
			return agentDigestFailure("RUNS_CHANGED")
		}
		if present && (o.observed == nil || o.observed.Recheck() != nil) {
			return agentDigestFailure("RUNS_CHANGED")
		}
	}
	if ctx.Err() != nil {
		return agentDigestFailure("CANCELED")
	}
	return nil
}

func (o *agentDigestObservation) Close() *agentcontext.Error {
	if o == nil || o.closed {
		return nil
	}
	o.closed = true
	if o.runs != nil && o.runs.Close() != nil {
		return agentDigestFailure("CLEANUP_FAILED")
	}
	return nil
}
