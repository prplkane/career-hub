package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/publicjobresolver"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

type publicFetcher func(context.Context, string) (*publicjobresolver.HTMLResponse, *publicjobresolver.Error)
type publicResolution struct {
	ResolvedURL   string `json:"resolved_url"`
	Strategy      string `json:"strategy"`
	RedirectCount int    `json:"redirect_count"`
	ResponseBytes int    `json:"response_bytes"`
}
type publicResolutionOwners struct {
	read    func(context.Context, string) (*discovery.Detail, error)
	capture func(context.Context, discovery.CaptureRequest) (*applicationwrite.Outcome, *applicationwrite.Error)
	review  func(context.Context, httpapi.FeedRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError)
	fetch   publicFetcher
}

func discoveryPublicResolutionAction(w *readonly.Workspace, start protocol.StartRecord, fetch publicFetcher) func(context.Context, httpapi.FeedRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	gate := make(chan struct{}, 1)
	return func(ctx context.Context, request httpapi.FeedRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		// Serialize public resolutions, not the UI or storage gate. Other edits
		// remain possible during fetch; CaptureDiscovery rechecks the revision.
		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		case <-ctx.Done():
			return publicResolutionFailure(nil, "LOCAL_OPERATION_CANCELED", "eligibility")
		}
		home, err := w.StartupHome()
		if err != nil {
			return publicResolutionFailure(nil, "WORKSPACE_UNAVAILABLE", "eligibility")
		}
		owner := applicationwrite.New(start.DataRoot).PinnedToHome(home).PinnedToResources(start.ResourceRoot)
		owners := publicResolutionOwners{read: w.Discovery, capture: owner.CaptureDiscovery, review: feedActions(start), fetch: fetch}
		write := func(ctx context.Context, before *publicjobresolver.Diagnostic, after publicjobresolver.Diagnostic) (bool, error) {
			return publicjobresolver.WriteDiagnostic(ctx, start.DataRoot, home, before, after)
		}
		return runDiagnosedPublicResolution(ctx, write, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) { return owners.resolve(ctx, request) })
	}
}

func publicResolutionFailure(out *httpapi.ApplicationOutcome, code, stage string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	if out == nil {
		out = &httpapi.ApplicationOutcome{}
	}
	out.Result = nil
	out.PublicResolutionStage = stage
	out.Partial = out.Partial || out.Committed || out.CommitUnknown || out.RecoveryRequired
	return out, &httpapi.ApplicationError{Code: code, Category: "public_job_resolution"}
}

func (o publicResolutionOwners) resolve(ctx context.Context, r httpapi.FeedRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	detail, err := o.read(ctx, r.ID)
	if err != nil {
		code := "PUBLIC_JOB_RESOLUTION_FAILED"
		if p := feedReadError(err); p != nil && (p.Code == "DISCOVERY_NOT_FOUND" || p.Code == "LOCAL_OPERATION_CANCELED") {
			code = p.Code
		}
		return publicResolutionFailure(nil, code, "eligibility")
	}
	if detail == nil || detail.DiscoveryID != r.ID {
		return publicResolutionFailure(nil, "LOCAL_RESPONSE_INVALID", "eligibility")
	}
	out := &httpapi.ApplicationOutcome{}
	resolution := publicResolution{}
	outcome := "duplicate"
	if slices.Contains(detail.SourceKinds, "public_resolver") {
		var observation *discovery.Observation
		for i := len(detail.Observations) - 1; i >= 0; i-- {
			if detail.Observations[i].SourceKind == "public_resolver" {
				observation = &detail.Observations[i]
				break
			}
		}
		if observation == nil {
			return publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_FAILED", "persist")
		}
		raw, _ := json.Marshal(observation.Metadata)
		if json.Unmarshal(raw, &resolution) != nil {
			return publicResolutionFailure(out, "LOCAL_RESPONSE_INVALID", "persist")
		}
		resolution.ResolvedURL = detail.ResolvedURL
	} else {
		var revision int64
		raw, e := json.Marshal(r.Revision)
		if e != nil || json.Unmarshal(raw, &revision) != nil || revision <= 0 {
			return publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_NOT_ELIGIBLE", "eligibility")
		}
		if detail.Revision != revision {
			return publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_STALE", "eligibility")
		}
		sourceURL := detail.ObservedURL
		if sourceURL == "" {
			sourceURL = detail.CanonicalURL
		}
		if !slices.Contains([]string{"inbox", "shortlisted"}, detail.State) || detail.ContentScope != "link_only" || sourceURL == "" {
			return publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_NOT_ELIGIBLE", "eligibility")
		}
		response, p := o.fetch(ctx, sourceURL)
		if p != nil {
			return publicResolutionFailure(out, p.Code, p.Stage)
		}
		if response == nil {
			return publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_FAILED", "response")
		}
		finalURL, p := publicjobresolver.NormalizeURL(response.FinalURL, "")
		if p != nil {
			return publicResolutionFailure(out, p.Code, p.Stage)
		}
		extraction, p := publicjobresolver.Extract(response, publicjobresolver.Expected{Title: detail.Title, Company: detail.Company, Location: detail.Location})
		if p != nil {
			return publicResolutionFailure(out, p.Code, p.Stage)
		}
		if ctx.Err() != nil {
			return publicResolutionFailure(out, "LOCAL_OPERATION_CANCELED", "persist")
		}
		fingerprint := sha256.Sum256([]byte("public-job-link/v1\x00" + r.ID + "\x00" + sourceURL))
		captured, problem := o.capture(ctx, discovery.CaptureRequest{
			"source_kind": "public_resolver", "source_channel": "public_job_link", "request_fingerprint": "public-job-link-v1:" + hex.EncodeToString(fingerprint[:]),
			"observed_url": finalURL, "posting_identity": extraction.PostingID, "title": extraction.Title, "company": extraction.Company, "location": extraction.Location,
			"content_scope": "page_visible", "source_text": extraction.SourceText, "target_discovery_id": r.ID, "expected_target_revision": r.Revision,
			"metadata": map[string]any{"resolver_version": 1, "extractor_version": 1, "strategy": extraction.Strategy, "redirect_count": response.RedirectCount, "response_bytes": response.BytesRead},
		})
		out = mergePromotionFacts(out, feedWriteOutcome(captured))
		if problem != nil {
			code := "PUBLIC_JOB_RESOLUTION_FAILED"
			switch problem.SourceClass {
			case "DiscoveryRevisionConflict":
				code = "PUBLIC_JOB_RESOLUTION_STALE"
			case "DiscoveryPublicResolutionNotReady":
				code = "PUBLIC_JOB_RESOLUTION_NOT_ELIGIBLE"
			case "DiscoveryNotFound":
				code = "DISCOVERY_NOT_FOUND"
			}
			if problem.Stage == "context" {
				code = "LOCAL_OPERATION_CANCELED"
			}
			return publicResolutionFailure(out, code, "persist")
		}
		var result struct {
			Outcome   string
			Discovery discovery.Detail
		}
		if captured == nil || json.Unmarshal(captured.Result, &result) != nil || result.Discovery.DiscoveryID != r.ID {
			return publicResolutionFailure(out, "LOCAL_RESPONSE_INVALID", "persist")
		}
		outcome = result.Outcome
		resolution = publicResolution{ResolvedURL: finalURL, Strategy: extraction.Strategy, RedirectCount: response.RedirectCount, ResponseBytes: response.BytesRead}
	}
	reviewed, p := o.review(ctx, httpapi.FeedRequest{Operation: "detail", ID: r.ID})
	out = mergePromotionFacts(out, reviewed)
	if p != nil {
		return publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_FAILED", "review")
	}
	if reviewed == nil || len(reviewed.Result) == 0 {
		return publicResolutionFailure(out, "LOCAL_RESPONSE_INVALID", "review")
	}
	// Only the complete existing review projection crosses HTTP. Capture metadata
	// is retained in the ledger, not substituted for current review information.
	out.Result, err = json.Marshal(map[string]any{"outcome": outcome, "discovery": json.RawMessage(reviewed.Result), "resolution": resolution})
	if err != nil {
		return publicResolutionFailure(out, "LOCAL_RESPONSE_INVALID", "review")
	}
	return out, nil
}

type resolverDiagnosticWriter func(context.Context, *publicjobresolver.Diagnostic, publicjobresolver.Diagnostic) (bool, error)

func runDiagnosedPublicResolution(ctx context.Context, write resolverDiagnosticWriter, run func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError)) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	record, err := publicjobresolver.NewDiagnostic()
	if err != nil {
		return publicResolutionFailure(nil, "PUBLIC_JOB_RESOLUTION_DIAGNOSTIC_FAILED", "eligibility")
	}
	observed, err := write(ctx, nil, record)
	if err != nil || !observed {
		out := &httpapi.ApplicationOutcome{}
		if observed {
			out.RunID = record.RunID
		}
		return publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_DIAGNOSTIC_FAILED", "eligibility")
	}
	out, problem := run()
	out = mergePromotionFacts(out, nil)
	out.RunID = record.RunID
	if problem == nil && (out.Partial || out.CommitUnknown || out.RecoveryRequired || out.CleanupFailed) {
		out, problem = publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_FAILED", "persist")
	}
	terminal := record
	terminal.Sequence = 2
	terminal.State = "success"
	terminal.Stage = "complete"
	if problem == nil {
		var body map[string]json.RawMessage
		var resolution publicResolution
		if json.Unmarshal(out.Result, &body) != nil || json.Unmarshal(body["resolution"], &resolution) != nil || json.Unmarshal(body["outcome"], &terminal.Outcome) != nil {
			out, problem = publicResolutionFailure(out, "LOCAL_RESPONSE_INVALID", "review")
		} else {
			terminal.Strategy = resolution.Strategy
			terminal.RedirectCount = resolution.RedirectCount
			terminal.ResponseBytes = resolution.ResponseBytes
			body["run_id"], _ = json.Marshal(record.RunID)
			out.Result, err = json.Marshal(body)
			if err != nil {
				out, problem = publicResolutionFailure(out, "LOCAL_RESPONSE_INVALID", "review")
			}
		}
	}
	terminal.Committed = out.Committed
	terminal.CommitUnknown = out.CommitUnknown
	terminal.Partial = out.Partial
	terminal.RecoveryRequired = out.RecoveryRequired
	terminal.CleanupFailed = out.CleanupFailed
	if problem != nil {
		terminal.State = "failed"
		terminal.Stage = out.PublicResolutionStage
		terminal.ErrorCode = publicjobresolver.DiagnosticCode(problem.Code)
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	observed, err = write(finish, &record, terminal)
	if err != nil || !observed {
		return publicResolutionFailure(out, "PUBLIC_JOB_RESOLUTION_DIAGNOSTIC_FAILED", "persist")
	}
	return out, problem
}
