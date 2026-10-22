package httpapi

import (
	"testing"
	"time"
)

func TestPublicResolutionDoesNotHoldUIGate(t *testing.T) {
	h, token, _ := applicationFixture(t)
	owner := h.(*handler)
	owner.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		response(t, h, vaultRequest("POST", "/api/discoveries/exact/resolve-public-link", token, `{"contract_version":1,"expected_revision":1}`), 200)
	}()
	select {
	case <-done:
		owner.mu.Unlock()
	case <-time.After(time.Second):
		owner.mu.Unlock()
		<-done
		t.Fatal("public fetch waited on the UI gate")
	}
}

func TestPublicResolutionClosedEnvelope(t *testing.T) {
	h, token, spy := applicationFixture(t)
	for _, body := range []string{`{}`, `{"contract_version":1}`, `{"contract_version":true,"expected_revision":1}`, `{"contract_version":1,"expected_revision":null}`, `{"contract_version":1,"expected_revision":0}`, `{"contract_version":1,"expected_revision":1.0}`, `{"contract_version":1,"expected_revision":1,"url":"https://injected.example"}`, `{"contract_version":1,"expected_revision":1,"expected_revision":2}`} {
		response(t, h, vaultRequest("POST", "/api/discoveries/exact/resolve-public-link", token, body), 400)
	}
	response(t, h, vaultRequest("POST", "/api/discoveries/%65xact/resolve-public-link", token, `{}`), 404)
	response(t, h, vaultRequest("POST", "/api/discoveries/exact/resolve-public-link?x=1", token, `{}`), 400)
	response(t, h, vaultRequest("POST", "/api/discoveries/exact/resolve-public-link", "wrong", `{}`), 403)
	if len(spy.calls) != 0 {
		t.Fatal("invalid request reached resolver")
	}
	spy.out = &ApplicationOutcome{RunID: "20260927T123000.123456Z_1234abcd", PublicResolutionStage: "dns"}
	spy.err = &ApplicationError{Code: "PUBLIC_JOB_RESOLUTION_DNS_REJECTED", Category: "public_job_resolution"}
	out := response(t, h, vaultRequest("POST", "/api/discoveries/exact/resolve-public-link", token, `{"contract_version":1,"expected_revision":1}`), 422)
	if out["stage"] != "dns" || out["run_id"] != spy.out.RunID {
		t.Fatal(out)
	}
	spy.out.Committed = true
	spy.out.Partial = true
	spy.out.PublicResolutionStage = "persist"
	spy.err.Code = "PUBLIC_JOB_RESOLUTION_DIAGNOSTIC_FAILED"
	out = response(t, h, vaultRequest("POST", "/api/discoveries/exact/resolve-public-link", token, `{"contract_version":1,"expected_revision":1}`), 503)
	if out["operation_state"].(map[string]any)["committed"] != true || out["retryable"] != false {
		t.Fatal("lost persisted facts", out)
	}
}
