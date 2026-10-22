package agentcontext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications"
)

func bindPreparationFixture(t *testing.T, db *sql.DB, data *savedSourceFixture, req *SavedPacketRequest) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	summaries, problem := applications.SummariesInTransaction(context.Background(), tx, []string{req.JobID})
	if problem != nil {
		t.Fatal(problem)
	}
	raw, err := json.Marshal(summaries[req.JobID])
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	cardHash := sha256.Sum256([]byte(data.files["jobs/normalized/"+req.JobID+".yaml"]))
	summaryHash := sha256.Sum256(raw)
	req.Operation = "prepare"
	req.WriteReceipt = json.RawMessage(`{"result":"no_change_needed","changed":false,"job_created":false,"job_reused":true,"status_before":"preparing","status_after":"preparing","applied_recorded":false}`)
	req.Preparation = &PreparationBinding{hex.EncodeToString(cardHash[:]), hex.EncodeToString(summaryHash[:])}
}

func TestPreparationBinding(t *testing.T) {
	for _, mode := range []string{"assessment", "application_text", "resume_delta", "card-drift", "status-drift", "metadata-drift", "resume-drift", "absent", "bad-hash", "uppercase-hash", "wrong-operation"} {
		t.Run(mode, func(t *testing.T) {
			db := savedPacketDB(t)
			d, r := savedInputFixture()
			req := savedPacketRequest("assessment")
			if ValidPurpose(mode) {
				req.Purpose = mode
			}
			bindPreparationFixture(t, db, d, &req)
			query := ""
			switch mode {
			case "card-drift":
				d.files["jobs/normalized/synthetic.yaml"] += "# changed bytes\n"
			case "status-drift":
				query = "UPDATE job_applications SET current_status='applied'"
			case "metadata-drift":
				query = "UPDATE job_applications SET title='Another title'"
			case "resume-drift":
				query = "DELETE FROM application_resume_attachments"
			case "absent":
				req.Preparation = nil
			case "bad-hash":
				req.Preparation.ApplicationSHA256 = strings.Repeat("z", 64)
			case "uppercase-hash":
				req.Preparation.CardSHA256 = strings.ToUpper(req.Preparation.CardSHA256)
			case "wrong-operation":
				req.Operation = "context"
			}
			if query != "" {
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			packet, e := preparePacketFixture(t, db, d, r, req, func(context.Context, json.RawMessage) (json.RawMessage, *string, *Error) {
				return json.RawMessage(`[{"id":"r1","display_name":"Alpha","digest_status":"not_digested","digest_supported":true,"digest_source_id":null,"digest_processed_at":null}]`), nil, nil
			})
			if ValidPurpose(mode) {
				if e != nil || !bytes.Contains(packet, []byte(`"operation":"prepare"`)) || !bytes.Contains(packet, req.WriteReceipt) {
					t.Fatal("bound packet", e)
				}
			} else if e == nil || packet != nil {
				t.Fatal("stale/invalid binding accepted")
			}
		})
	}
}
