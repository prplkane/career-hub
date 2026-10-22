package main

import (
	"context"
	"encoding/json"
	"net/http"

	"careerhub.local/backend/internal/applicationhelper"
	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/assessmentpolicy"
	"careerhub.local/backend/internal/diagnosticslog"
	"careerhub.local/backend/internal/emailconnection"
	"careerhub.local/backend/internal/emailhost"
	"careerhub.local/backend/internal/emailimport"
	"careerhub.local/backend/internal/emailworker"
	"careerhub.local/backend/internal/publicjobresolver"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	sidecar "careerhub.local/backend/internal/sidecar/runtime"
	"careerhub.local/backend/internal/vaultwrite"
)

func preparedApplication(w *readonly.Workspace, start protocol.StartRecord, origins ...string) sidecar.Prepared {
	return preparedApplicationWithEmailBackend(w, start, secretstore.NewWindowsBackend, origins...)
}

// The backend factory is invoked only by the email read route; unrelated
// pages never require Credential Manager to initialize successfully.
func preparedApplicationWithEmailBackend(w *readonly.Workspace, start protocol.StartRecord, emailBackend func() (secretstore.Backend, error), origins ...string) sidecar.Prepared {
	return preparedApplicationWithEmailDependencies(w, start, emailBackend, emailhost.Run, origins...)
}

type emailSyncRunner func(context.Context, *applicationwrite.Service, secretstore.Backend, string) (emailworker.Result, error)

func preparedApplicationWithEmailDependencies(w *readonly.Workspace, start protocol.StartRecord, emailBackend func() (secretstore.Backend, error), syncRunner emailSyncRunner, origins ...string) sidecar.Prepared {
	return preparedApplicationWithPublicFetcher(w, start, emailBackend, syncRunner, publicjobresolver.Fetch, origins...)
}

// The fetch seam is internal and used only by synthetic transport/owner tests.
// No request, environment variable or persisted value selects a dialer/fetcher.
func preparedApplicationWithPublicFetcher(w *readonly.Workspace, start protocol.StartRecord, emailBackend func() (secretstore.Backend, error), syncRunner emailSyncRunner, fetch publicFetcher, origins ...string) sidecar.Prepared {
	profile, policy, helper := vaultwrite.New(start.DataRoot), assessmentpolicy.New(start.DataRoot), applicationhelper.New(start.DataRoot)
	editVault := vaultEdits(start)
	feed := feedActions(start)
	reanalysis := newReanalysisCoordinator(w, start)
	jobs := jobActions(w, start, reanalysis)
	vocabulary := newExactVocabularyCoordinator(w, start, reanalysis)
	jobIntake := jobIntakeAction(w, start)
	promoteDiscovery := discoveryPromotionAction(w, start)
	resolvePublic := discoveryPublicResolutionAction(w, start, fetch)
	intake := intakeActions(w, start)
	invoke := func(ctx context.Context, call func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError)) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		if ctx == nil || ctx.Err() != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "LOCAL_OPERATION_CANCELED"}
		}
		if w.CheckAdmission() != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE"}
		}
		out, e := call()
		if w.CheckAdmission() != nil {
			if out != nil {
				out.Result = nil
				out.Content = nil
			}
			return out, &httpapi.ApplicationError{Code: "WORKSPACE_UNAVAILABLE"}
		}
		return out, e
	}
	invokeEmail := func(ctx context.Context, category string, call func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError)) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		out, problem := invoke(ctx, call)
		if problem != nil && problem.Category == "" {
			problem.Category = category
		}
		return out, problem
	}
	invokeJobIntake := func(ctx context.Context, request httpapi.JobIntakeRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
		if ctx == nil || ctx.Err() != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "LOCAL_OPERATION_CANCELED", Category: "job_intake"}
		}
		if w.CheckAdmission() != nil {
			return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "JOB_INTAKE_UNAVAILABLE", Category: "job_intake"}
		}
		out, problem := jobIntake(ctx, request)
		if w.CheckAdmission() != nil {
			if out == nil {
				out = &httpapi.ApplicationOutcome{}
			}
			out.Result = nil
			out.Content = nil
			out.RecoveryRequired = true
			if problem == nil {
				problem = &httpapi.ApplicationError{Code: "JOB_INTAKE_RECOVERY_REQUIRED", Category: "job_intake"}
			}
		}
		return out, problem
	}
	actions := httpapi.ApplicationActions{Static: w.StaticAsset, Runtime: w.RuntimeInfo,
		AgentContext: agentContextAction(w, start),
		AgentAnalyze: agentContextAction(w, start),
		AgentPrepare: agentPrepareAction(w, start),
		RecordSubmission: func(ctx context.Context, request httpapi.RecordSubmissionRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				// Bypass jobActions: it may register a missing application. This
				// command requires an existing ledger record and explicit confirmation.
				return jobWriteResult(applicationwrite.New(start.DataRoot).RecordSubmission(ctx, request))
			})
		},
		ExactVocabulary: func(ctx context.Context, request httpapi.ExactVocabularyRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				return vocabulary.Application(ctx, request)
			})
		},
		VaultSetup: func(ctx context.Context, request httpapi.VaultSetupRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				return importVaultSetup(ctx, w, start.DataRoot, request)
			})
		},
		EmailImport: func(ctx context.Context, batch *emailimport.Batch) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "email_import", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				return importEmailFiles(ctx, w, start.DataRoot, batch)
			})
		},
		Logs: func(ctx context.Context, after string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "diagnostic_read", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				home, err := w.StartupHome()
				if err != nil {
					return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "DIAGNOSTIC_HOME_CHANGED"}
				}
				page, problem := diagnosticslog.ReadPage(ctx, start.DataRoot, home, after)
				if problem != nil {
					return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: problem.Code}
				}
				raw, err := json.Marshal(page)
				if err != nil {
					return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "LOCAL_RESPONSE_INVALID"}
				}
				return &httpapi.ApplicationOutcome{Result: raw}, nil
			})
		},
		Jobs: func(ctx context.Context, request httpapi.JobRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			if request.Operation == "resume.digest" {
				if ctx == nil || ctx.Err() != nil {
					return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "RESUME_DIGEST_CANCELED", Category: "resume_digest"}
				}
				if w.CheckAdmission() != nil {
					return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "RESUME_DIGEST_UNAVAILABLE", Category: "resume_digest"}
				}
				out, problem := jobs(ctx, request)
				if w.CheckAdmission() != nil {
					if out != nil {
						out.Result = nil
						out.Content = nil
					}
					// A digest may intentionally leave its exact recovery sentinel.
					// Preserve that operation-specific failure instead of replacing it
					// with a generic workspace error after the owner returns.
					if problem != nil && out != nil && out.RecoveryRequired {
						return out, problem
					}
					return out, &httpapi.ApplicationError{Code: "RESUME_DIGEST_UNAVAILABLE", Category: "resume_digest"}
				}
				return out, problem
			}
			return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) { return jobs(ctx, request) })
		},
		Intake: func(ctx context.Context, request httpapi.IntakeRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			if request.Operation != "review.build" && request.Operation != "review.suggest" {
				return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) { return intake(ctx, request) })
			}
			canceledCode, unavailableCode, committedCode := "INTAKE_REVIEW_BUILD_CANCELED", "INTAKE_REVIEW_BUILD_UNAVAILABLE", "INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED"
			if request.Operation == "review.suggest" {
				canceledCode, unavailableCode, committedCode = "INTAKE_REVIEW_SUGGEST_CANCELED", "INTAKE_REVIEW_SUGGEST_UNAVAILABLE", "INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED"
			}
			if ctx == nil || ctx.Err() != nil {
				return &httpapi.ApplicationOutcome{}, intakeError(canceledCode)
			}
			if w.CheckAdmission() != nil {
				return &httpapi.ApplicationOutcome{}, intakeError(unavailableCode)
			}
			out, problem := intake(ctx, request)
			if w.CheckAdmission() != nil {
				if out == nil {
					out = &httpapi.ApplicationOutcome{}
				}
				out.Result = nil
				if problem != nil && (out.Committed || out.CommitUnknown || out.RecoveryRequired) {
					return out, problem
				}
				if out.Committed {
					out.Partial = true
					return out, intakeError(committedCode)
				}
				return out, intakeError(unavailableCode)
			}
			return out, problem
		},
		JobIntake: func(ctx context.Context, request httpapi.JobIntakeRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeJobIntake(ctx, request)
		},
		EmailConnection: func(ctx context.Context) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "email_connection_read", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				return readEmailConnection(ctx, w, start.DataRoot, emailBackend)
			})
		},
		EmailConfigure: func(ctx context.Context, request httpapi.EmailConfigureRequest, runID string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "email_connection_mutation", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				return configureEmailConnection(ctx, w, start.DataRoot, emailBackend, request, runID)
			})
		},
		EmailRetire: func(ctx context.Context, operation string, revision int64) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "email_connection_mutation", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				return retireEmailConnection(ctx, w, start.DataRoot, emailBackend, operation, revision)
			})
		},
		EmailSync: func(ctx context.Context, trigger, requestedRunID string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "email_sync", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				return syncEmailConnection(ctx, w, start.DataRoot, emailBackend, syncRunner, trigger, requestedRunID)
			})
		},
		EmailUpdates: func(ctx context.Context, offset int64, limit int) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "email_application_updates_read", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				return readEmailUpdates(ctx, w, offset, limit)
			})
		},
		EmailUpdateLink: func(ctx context.Context, request httpapi.EmailUpdateLinkRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "email_application_update_link", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				home, err := w.StartupHome()
				if err != nil {
					return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "EMAIL_APPLICATION_UPDATE_LINK_UNAVAILABLE"}
				}
				return linkEmailUpdate(ctx, start.DataRoot, home, request)
			})
		},
		EmailCursorReset: func(ctx context.Context, revision int64) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "email_connection_mutation", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				home, err := w.StartupHome()
				if err != nil {
					return &httpapi.ApplicationOutcome{}, emailMutationFailure("DATA_HOME_UNAVAILABLE")
				}
				return runEmailMutation(ctx, w, start.DataRoot, emailBackend, func() (emailconnection.ConnectionMutationResult, *emailconnection.Error) {
					return emailconnection.ResetCursor(ctx, start.DataRoot, home, revision)
				})
			})
		},
		EmailSenderRule: func(ctx context.Context, request httpapi.EmailSenderRuleRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invokeEmail(ctx, "email_connection_mutation", func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				home, err := w.StartupHome()
				if err != nil {
					return &httpapi.ApplicationOutcome{}, emailMutationFailure("DATA_HOME_UNAVAILABLE")
				}
				return runEmailMutation(ctx, w, start.DataRoot, emailBackend, func() (emailconnection.ConnectionMutationResult, *emailconnection.Error) {
					return emailconnection.SetSenderDisposition(ctx, start.DataRoot, home, emailconnection.SenderDispositionInput{
						SenderAddress: request.SenderAddress, Disposition: request.Disposition,
						Purpose: &request.Purpose, ExpectedRevision: request.ExpectedRevision,
					})
				})
			})
		},
		Feed: func(ctx context.Context, request httpapi.FeedRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			if request.Operation == "resolve_public" {
				return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) { return resolvePublic(ctx, request) })
			}
			if request.Operation == "promote" {
				return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) { return promoteDiscovery(ctx, request) })
			}
			return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) { return feed(ctx, request) })
		},
		Competencies: w.CompetencyExplorer,
		Demand:       w.DemandGaps,
		VaultEdit: func(ctx context.Context, request httpapi.VaultEditRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) { return editVault(ctx, request) })
		},
		Profile: func(ctx context.Context, kind string, fields map[string]any, expected string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				var out *vaultwrite.Outcome
				var e *vaultwrite.Error
				if kind == "profile" {
					out, e = profile.UpdateProfile(ctx, "profile", fields, expected)
				} else if kind == "eligibility" {
					out, e = profile.UpdateEligibility(ctx, "eligibility", fields, expected)
				} else {
					return &httpapi.ApplicationOutcome{}, &httpapi.ApplicationError{Code: "VAULT_RECORD_NOT_FOUND"}
				}
				var result *httpapi.ApplicationOutcome
				if out != nil {
					result = &httpapi.ApplicationOutcome{Result: out.Result, Committed: out.Committed, CleanupFailed: out.CleanupFailed}
				}
				if e != nil {
					code := "VAULT_OPERATION_FAILED"
					switch e.SourceClass {
					case "VaultEditorRevisionConflict":
						code = "VAULT_EDITOR_REVISION_CONFLICT"
					case "VaultRecordNotFound":
						code = "VAULT_RECORD_NOT_FOUND"
					case "VaultEditorError":
						code = "VAULT_RECORD_VALIDATION_FAILED"
					}
					if e.Stage == "validation" || e.Stage == "load" {
						code = "VAULT_RECORD_VALIDATION_FAILED"
					}
					if e.Stage == "context" {
						code = "LOCAL_OPERATION_CANCELED"
					}
					return result, &httpapi.ApplicationError{Code: code}
				}
				return result, nil
			})
		},
		Policy: func(ctx context.Context, save bool, value any, expected string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				out, e := policy.Run(ctx, save, value, expected)
				var result *httpapi.ApplicationOutcome
				if out != nil {
					result = &httpapi.ApplicationOutcome{Result: out.Result, Committed: out.Committed, CleanupFailed: out.CleanupFailed}
				}
				if e != nil {
					return result, &httpapi.ApplicationError{Code: e.Code}
				}
				return result, nil
			})
		},
		Helper: func(ctx context.Context, operation string, fields map[string]any) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
			return invoke(ctx, func() (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
				out, e := helper.Run(ctx, applicationhelper.Request{Operation: operation, Fields: fields})
				var result *httpapi.ApplicationOutcome
				if out != nil {
					result = &httpapi.ApplicationOutcome{Result: out.Result, Committed: out.Committed, CleanupFailed: out.CleanupFailed}
				}
				if e != nil {
					code := "APPLICATION_HELPER_OPERATION_FAILED"
					switch e.Code {
					case "APPLICATION_HELPER_REVISION_CONFLICT", "APPLICATION_DEMOGRAPHICS_REVISION_CONFLICT", "APPLICATION_HELPER_CONFIGURATION_INVALID", "APPLICATION_DEMOGRAPHICS_INVALID":
						code = e.Code
					case "APPLICATION_HELPER_CANCELED":
						code = "LOCAL_OPERATION_CANCELED"
					}
					return result, &httpapi.ApplicationError{Code: code}
				}
				return result, nil
			})
		},
	}
	materials := materialActions(w, start)
	session := &agentSession{dataRoot: start.DataRoot}
	return sidecar.Prepared{AppVersion: w.AppVersion(), Handler: func(port int) (http.Handler, error) {
		var handler http.Handler
		var err error
		if len(origins) > 0 {
			handler, err = httpapi.NewApplicationWithExtensions(port, w.AppVersion(), w.Registry, w.Vault, w.Documents, materials, actions, origins, extensionActions(w, start))
			if err == nil {
				err = httpapi.ConfigureExtensionPairing(handler, dataHomePairingStore{dataRoot: start.DataRoot})
			}
		} else {
			handler, err = httpapi.NewApplication(port, w.AppVersion(), w.Registry, w.Vault, w.Documents, materials, actions)
		}
		if err != nil {
			return nil, err
		}
		// The window token comes from the desktop host over stdin, never HTTP.
		if err := httpapi.UseSessionToken(handler, start.SessionToken); err != nil {
			return nil, err
		}
		token, ok := httpapi.AgentToken(handler)
		if !ok {
			return nil, errAgentConnection
		}
		if err := session.publish(port, token); err != nil {
			return nil, err
		}
		return handler, nil
	}, Release: session.release}
}
