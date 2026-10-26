package materialintegrity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"
)

const MaxReviewBytes = 4 << 20
const MaxReviewRecords = 4096

// Confirmation bindings and original timestamps survive revocation. Confirming
// again appends a new record/ID; it never rewrites the previous approval history.
type reviewRecord struct {
	Check             ManualFactCheck `json:"check"`
	ConfirmedRevision int64           `json:"confirmed_revision"`
	ConfirmedAt       string          `json:"confirmed_at"`
	RevokedRevision   int64           `json:"revoked_revision"`
	RevokedAt         string          `json:"revoked_at"`
}

type reviewStore struct {
	SchemaVersion int            `json:"schema_version"`
	JobID         string         `json:"job_id"`
	Kind          string         `json:"kind"`
	Revision      int64          `json:"revision"`
	Records       []reviewRecord `json:"records"`
}

func (reviewStore) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted material review store]")
}

// Target identity groups repeated instances of the same normalized token in one
// exact field. The review UI must show all spans in that field before confirming.
func reviewTarget(c ManualFactCheck) string {
	raw, _ := json.Marshal([4]string{c.ReviewSHA256, c.DraftPath, c.ClaimSHA256, c.TokenSHA256})
	return "fact_" + digest(raw)
}

func checkID(target string, revision int64) string {
	return "manual_" + digest([]byte(target+":"+strconv.FormatInt(revision, 10)))
}

func reviewClock(clock string) bool {
	t, e := time.Parse("2006-01-02T15:04:05Z", clock)
	return e == nil && t.Year() >= 1 && t.Year() <= 9999 && t.Format("2006-01-02T15:04:05Z") == clock
}

func validReviewStore(s *reviewStore) bool {
	if s == nil || s.SchemaVersion != 1 || !identifier(s.JobID) ||
		(s.Kind != "resume" && s.Kind != "cover_letter") || s.Revision < 1 ||
		s.Records == nil || len(s.Records) == 0 || len(s.Records) > MaxReviewRecords {
		return false
	}
	active, lastID := map[string]bool{}, ""
	for _, r := range s.Records {
		c, target := r.Check, reviewTarget(r.Check)
		if !draftPath.MatchString(c.DraftPath) || c.DraftPath == "artifact" ||
			!hash(c.ClaimSHA256, "") || !hash(c.TokenSHA256, "") || !hash(c.ReviewSHA256, "") || !c.Confirmed ||
			r.ConfirmedRevision < 1 || r.ConfirmedRevision > s.Revision || !reviewClock(r.ConfirmedAt) ||
			c.ID != checkID(target, r.ConfirmedRevision) || c.ID <= lastID {
			return false
		}
		lastID = c.ID
		if r.RevokedRevision == 0 {
			if r.RevokedAt != "" || active[target] {
				return false
			}
			active[target] = true
		} else if r.RevokedRevision <= r.ConfirmedRevision || r.RevokedRevision > s.Revision ||
			!reviewClock(r.RevokedAt) || r.RevokedAt < r.ConfirmedAt {
			return false
		}
	}
	return true
}

func encodeReviewStore(s *reviewStore) ([]byte, *Error) {
	if !validReviewStore(s) {
		return nil, failure("MATERIAL_REVIEW_STORE_INVALID")
	}
	raw, e := json.Marshal(s)
	if e != nil || len(raw)+1 > MaxReviewBytes {
		return nil, failure("MATERIAL_REVIEW_STORE_LIMIT")
	}
	return append(raw, '\n'), nil
}

func decodeReviewStore(raw []byte, job, kind string) (*reviewStore, *Error) {
	if len(raw) == 0 || len(raw) > MaxReviewBytes {
		return nil, failure("MATERIAL_REVIEW_STORE_LIMIT")
	}
	var s reviewStore
	if json.Unmarshal(raw, &s) != nil || s.JobID != job || s.Kind != kind {
		return nil, failure("MATERIAL_REVIEW_STORE_INVALID")
	}
	canonical, e := encodeReviewStore(&s)
	if e != nil || !bytes.Equal(raw, canonical) {
		return nil, failure("MATERIAL_REVIEW_STORE_INVALID")
	}
	return &s, nil
}

// Only records already read from the local canonical store can reach assessment.
// Old source revisions remain history but cannot approve the current export.
func currentChecks(s *reviewStore, identity string) ([]ManualFactCheck, int) {
	checks, stale := []ManualFactCheck{}, 0
	for _, r := range s.Records {
		if r.RevokedRevision != 0 {
			continue
		}
		if r.Check.ReviewSHA256 != identity {
			stale++
			continue
		}
		checks = append(checks, r.Check)
	}
	return checks, stale
}
