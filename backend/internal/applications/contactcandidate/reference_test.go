package contactcandidate

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type boundedOracleOutput struct{ bytes.Buffer }

func (out *boundedOracleOutput) Write(value []byte) (int, error) {
	if out.Len()+len(value) > 8<<20 {
		return 0, errors.New("oracle output budget exceeded")
	}
	return out.Buffer.Write(value)
}

func TestContactCandidateDecoderIsBoundedAndClosed(t *testing.T) {
	for _, raw := range [][]byte{
		nil,
		[]byte("[]"),
		[]byte("{} {}"),
		append([]byte(`{"contract_version":1,"source_kind":"job","source_id":"x","candidate_id":"`), 0xff),
		bytes.Repeat([]byte("x"), MaxRequestBytes+1),
	} {
		if _, p := DecodePreview(raw); p == nil {
			t.Fatal("invalid envelope was accepted")
		}
	}
}

func TestContactCandidateTypedRequestValidation(t *testing.T) {
	valid := SourceRequest{
		SourceKind: "job", SourceID: "synthetic",
		CandidateID: strings.Repeat("a", 64), ExpectedContentSHA256: strings.Repeat("b", 64),
	}
	if p := ValidateSourceRequest(valid); p != nil {
		t.Fatal(p)
	}
	for _, change := range []func(*SourceRequest){
		func(request *SourceRequest) { request.SourceKind = "saved_job" },
		func(request *SourceRequest) { request.SourceID = " synthetic " },
		func(request *SourceRequest) { request.CandidateID = strings.Repeat("A", 64) },
		func(request *SourceRequest) { request.ExpectedContentSHA256 = "bad" },
	} {
		request := valid
		change(&request)
		if p := ValidateSourceRequest(request); p == nil || p.Code != requestInvalid {
			t.Fatal("invalid typed request was accepted")
		}
	}
}

func TestContactCandidateTypedConfirmationValidation(t *testing.T) {
	base := SourceRequest{
		SourceKind: "job", SourceID: "synthetic",
		CandidateID: strings.Repeat("a", 64), ExpectedContentSHA256: strings.Repeat("b", 64),
	}
	create := ConfirmationRequest{SourceRequest: base, Action: "create", Contact: map[string]any{
		"name": "Jane", "role": "Recruiter", "company": "Example", "context": "Recruiter", "channels": []any{},
	}}
	link := ConfirmationRequest{SourceRequest: base, Action: "link_existing", ContactID: "contact_synthetic", ExpectedRevision: 1}
	for _, request := range []ConfirmationRequest{create, link} {
		if p := ValidateConfirmationRequest(request); p != nil {
			t.Fatal(p)
		}
	}
	for _, request := range []ConfirmationRequest{
		{SourceRequest: base, Action: "create", Contact: map[string]any{"name": "Jane"}},
		{SourceRequest: base, Action: "create", Contact: create.Contact, ContactID: "contact_synthetic"},
		{SourceRequest: base, Action: "link_existing", ContactID: " contact_synthetic ", ExpectedRevision: 1},
		{SourceRequest: base, Action: "link_existing", ContactID: "contact_synthetic"},
		{SourceRequest: base, Action: "other"},
	} {
		if p := ValidateConfirmationRequest(request); p == nil || p.Code != requestInvalid {
			t.Fatal("invalid typed confirmation request was accepted")
		}
	}
}
