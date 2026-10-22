package agentmaterials

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func vocabularyFixture(created bool) (Request, map[string]any) {
	r := Request{Command: "registry-add-exact", JobID: "job-one", Vocabulary: &VocabularyInput{Name: "NebulaDB", PrimaryGroupID: "databases_storage", ConfirmedExact: true}}
	stage := func(committed bool) map[string]any {
		return map[string]any{"known": true, "committed": committed, "partial": false, "cleanup_failed": false}
	}
	stages := map[string]any{"registry": stage(false), "reanalysis": nil}
	p := map[string]any{"schema_version": 1, "contract_version": 4, "result": "no_change_needed", "job_id": r.JobID, "source_ref": "job:job-one#line:3", "candidate_evidence_changed": false, "stages": stages,
		"record": map[string]any{"id": "user_nebuladb", "canonical_name": "NebulaDB", "origin": "custom", "category": "platform", "kind": "technology", "primary_domain": nil, "primary_group": map[string]any{"id": "databases_storage", "name": "Databases", "origin": "core"}, "archived": false}}
	if created {
		registry := stage(true)
		registry["write"] = map[string]any{"intent_recorded": true, "applied_files": 1, "files_complete": true, "journal_removed": true, "completed": true}
		stages["registry"], stages["reanalysis"] = registry, stage(true)
		p["result"], p["registry_revision"], p["job_reanalyzed"], p["reanalysis_warning"] = "created", "synthetic-revision", true, nil
		p["record"] = map[string]any{"id": "user_nebuladb", "name": "NebulaDB", "origin": "custom", "primary_group_id": "databases_storage", "archived": false, "aliases": []any{}, "relations": []any{}}
	}
	return r, p
}

func TestVocabularyReceiptStagesAndClosedProjection(t *testing.T) {
	for _, scenario := range []string{"created", "existing", "followup-failed", "followup-not-attempted", "bad-job", "bad-line", "candidate-claim", "false-success", "missing-state", "forged-noop", "wrong-group", "wrong-name", "bad-stage-run", "registry-unknown", "null-archived", "missing-archived"} {
		t.Run(scenario, func(t *testing.T) {
			r, p := vocabularyFixture(scenario != "existing" && scenario != "forged-noop")
			stages := p["stages"].(map[string]any)
			switch scenario {
			case "followup-failed":
				p["job_reanalyzed"], p["reanalysis_warning"] = false, "skill added; reanalysis unconfirmed"
				stages["reanalysis"] = map[string]any{"known": false, "committed": nil, "partial": false, "cleanup_failed": false, "recovery_required": true, "run_id": "20260926T120000.000000Z_1234abcd"}
			case "followup-not-attempted":
				p["job_reanalyzed"], p["reanalysis_warning"], stages["reanalysis"] = false, "non-draft job", nil
			case "bad-job":
				p["job_id"] = "other"
			case "bad-line":
				n := 9
				r.Vocabulary.SourceLine = &n
			case "candidate-claim":
				p["candidate_evidence_changed"] = true
			case "false-success":
				stages["reanalysis"] = nil
			case "missing-state":
				delete(stages["registry"].(map[string]any), "known")
			case "forged-noop":
				stages["registry"].(map[string]any)["committed"] = true
			case "wrong-group":
				p["record"].(map[string]any)["primary_group_id"] = "other"
			case "wrong-name":
				p["record"].(map[string]any)["name"] = "Other"
			case "bad-stage-run":
				stages["reanalysis"].(map[string]any)["run_id"] = "SECRET"
			case "registry-unknown":
				stages["registry"].(map[string]any)["known"] = false
			case "null-archived":
				p["record"].(map[string]any)["archived"] = nil
			case "missing-archived":
				delete(p["record"].(map[string]any), "archived")
			}
			p["private_metadata"] = "SECRET"
			p["record"].(map[string]any)["private_metadata"] = "SECRET"
			raw, _ := json.Marshal(p)
			got, e := vocabularyReceipt(raw, r)
			valid := scenario == "created" || scenario == "existing" || strings.HasPrefix(scenario, "followup-")
			if valid {
				if e != nil || !json.Valid(got) || strings.Contains(string(got), "SECRET") {
					t.Fatal("projection refused or leaked")
				}
				var receipt map[string]any
				_ = json.Unmarshal(got, &receipt)
				if receipt["result"] != p["result"] || receipt["job_reanalyzed"] != p["job_reanalyzed"] {
					t.Fatal("source semantics changed")
				}
			} else if e == nil || len(got) != 0 {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
}

func TestVocabularyClientAuthorityAndBudgets(t *testing.T) {
	for _, mode := range []string{"created", "capability-missing", "failure", "uncertain-failure"} {
		t.Run(mode, func(t *testing.T) {
			r, p := vocabularyFixture(true)
			posts := 0
			c, _ := clientAt(t, func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if req.URL.Path == "/api/session" {
					body := sessionBody()
					if mode != "capability-missing" {
						body = strings.Replace(body, `"material_integrity":1`, `"exact_vocabulary":1`, 1)
					}
					_, _ = io.WriteString(w, body)
					return
				}
				posts++
				if req.Method != "POST" || req.URL.Path != "/api/jobs/job-one/vocabulary/exact" || req.Header.Get("X-Career-Hub-Token") != syntheticToken {
					t.Fatal("wrong authority or endpoint")
				}
				var input VocabularyInput
				if json.NewDecoder(req.Body).Decode(&input) != nil || input.Name != r.Vocabulary.Name || !input.ConfirmedExact {
					t.Fatal("lost exact confirmation")
				}
				if mode == "failure" || mode == "uncertain-failure" {
					p = map[string]any{"error_code": "AGENT_VOCABULARY_REGISTRY_FAILED", "operation_state": map[string]any{"known": false, "committed": true, "partial": false, "cleanup_failed": false}, "stages": p["stages"]}
					if mode == "uncertain-failure" {
						p["stages"].(map[string]any)["reanalysis"] = map[string]any{"known": false, "committed": nil, "partial": false, "cleanup_failed": false, "recovery_required": true}
					}
					w.WriteHeader(500)
				}
				_ = json.NewEncoder(w).Encode(p)
			})
			transport := c.vocabularyHTTP.Transport.(*http.Transport)
			if c.http.Timeout != 30*time.Second || c.vocabularyHTTP.Timeout <= 35*time.Second || transport.ResponseHeaderTimeout <= 35*time.Second || transport.Proxy != nil || !transport.DisableKeepAlives {
				t.Fatal("command budget/security changed")
			}
			got, e := c.Run(context.Background(), r)
			if mode == "created" {
				if e != nil || len(got) == 0 || posts != 1 {
					t.Fatal("valid command failed")
				}
				return
			}
			if e == nil {
				t.Fatal("failure hidden")
			}
			if mode == "capability-missing" {
				if posts != 0 {
					t.Fatal("unadvertised mutation")
				}
				return
			}
			if posts != 1 || e.Stages == nil || !*e.Stages.Registry.Committed {
				t.Fatal("retried or lost stage facts")
			}
			if mode == "uncertain-failure" && (*e.Stages.Reanalysis.Known || e.Stages.Reanalysis.Committed != nil || !e.Stages.Reanalysis.RecoveryRequired) {
				t.Fatal("unknown JD write implied rollback")
			}
		})
	}
}

func TestVocabularyRequestRefusesBeforeNetwork(t *testing.T) {
	c, _ := clientAt(t, func(http.ResponseWriter, *http.Request) { t.Fatal("invalid command reached network") })
	for _, mode := range []string{"confirmation", "job", "name", "group", "line", "kind", "payload", "approve", "foreign-command"} {
		r, _ := vocabularyFixture(true)
		switch mode {
		case "confirmation":
			r.Vocabulary.ConfirmedExact = false
		case "job":
			r.JobID = "../other"
		case "name":
			r.Vocabulary.Name = string([]byte{0xff})
		case "group":
			r.Vocabulary.PrimaryGroupID = strings.Repeat("x", 161)
		case "line":
			n := 0
			r.Vocabulary.SourceLine = &n
		case "kind":
			r.Kind = "cover_letter"
		case "payload":
			r.Payload = []byte(`{}`)
		case "approve":
			r.Approve = true
		case "foreign-command":
			r.Command = "material-context"
		}
		if _, e := c.Run(context.Background(), r); e == nil {
			t.Fatal("invalid request accepted", mode)
		}
	}
}
