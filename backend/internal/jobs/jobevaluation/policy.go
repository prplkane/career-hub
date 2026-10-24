package jobevaluation

import (
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vault/eligibilityfacts"
)

const (
	workAuthorization = "citizenship_work_authorization"
	activeClearance   = "active_clearance"
	minimumExperience = "minimum_experience_years"
)

func (s *evaluation) policyFinding(class, code, message, source string, fact CandidateFact, evidence []any) *Error {
	enabled := true
	if value := get(s.input.Policy, class); value.Kind == "bool" {
		enabled = value.Boolean
	}
	hasSource := false
	for _, item := range evidence {
		if fields, ok := item.(map[string]any); ok {
			if text, ok := fields["text"].(string); ok && textcompat.Strip(text) != "" {
				hasSource = true
			}
		}
	}
	hasFact := fact.Value != nil && textcompat.Strip(fact.Statement) != ""
	finding := Finding{PolicyClass: class, Code: code, Message: message, Source: source, SourceEvidence: evidence, CandidateFact: fact}
	if enabled && hasSource && hasFact {
		finding.Status = "likely_conflict"
		return retain(s, &s.result.EligibilityBlockers, finding)
	}
	finding.Status = "review"
	reviewMessage := message
	switch {
	case !enabled:
		finding.ReviewReason = "policy_set_to_review"
		reviewMessage += " This conflict class is set to Review."
	case !hasSource:
		finding.ReviewReason = "source_evidence_unavailable"
		reviewMessage += " Exact JD evidence is unavailable; review manually."
	default:
		finding.ReviewReason = "candidate_fact_unconfirmed"
		reviewMessage += " The Career profile fact is not confirmed; review manually."
	}
	if err := retain(s, &s.result.PolicyReviews, finding); err != nil {
		return err
	}
	return retain(s, &s.result.ManualReview, reviewMessage)
}

// Only a whole reviewed status label confirms OPT, not a substring or prose
// about past/future/negated status. Other text containing the token OPT is
// unknown and stays Review unless an explicit sponsorship boolean confirms the
// conflict independently. No legal eligibility or location inference is made.
func optStatus(status string) (confirmed, ambiguous bool) {
	return eligibilityfacts.OPTStatus(status)
}

func (s *evaluation) eligibility() *Error {
	for _, gate := range []struct {
		signal, fact, class, name, message, unknown, statement, unknownStatement string
	}{
		{"citizenship_required", "citizenship_required_blocker", workAuthorization, "citizenship",
			"Posting requires U.S. citizenship; verify eligibility or an exception path.",
			"Posting requires U.S. citizenship; Career profile compatibility is unknown.",
			"Career profile marks citizenship-only roles as incompatible.",
			"Career profile does not confirm citizenship-role compatibility."},
		{"clearance_required", "clearance_required_blocker", activeClearance, "clearance",
			"Posting requires active security clearance; verify eligibility or an exception path.",
			"Posting requires active security clearance; Career profile compatibility is unknown.",
			"Career profile marks active-clearance requirements as incompatible.",
			"Career profile does not confirm active-clearance compatibility."},
	} {
		if !isTrue(get(s.job.signals, gate.signal)) {
			continue
		}
		value := get(s.input.Profile, gate.fact)
		if value.Kind == "bool" && !value.Boolean {
			continue
		}
		fact := CandidateFact{Field: "visa_profile." + gate.fact, Statement: gate.statement, Value: true}
		message := gate.message
		if value.Kind != "bool" {
			fact.Value, fact.Statement, message = nil, gate.unknownStatement, gate.unknown
		}
		if err := s.policyFinding(gate.class, gate.name+"_requirement", message, "visa_signals."+gate.signal, fact, s.job.gateEvidence[gate.name]); err != nil {
			return err
		}
	}
	if isTrue(get(s.job.signals, "no_visa_or_opt")) {
		now := get(s.input.Profile, "requires_sponsorship_now")
		future := get(s.input.Profile, "requires_future_sponsorship")
		status := ""
		if value := get(s.input.Profile, "current_status"); value.Kind == "string" {
			status = textcompat.Strip(value.Text)
		}
		opt, ambiguous := optStatus(status)
		needsSponsorship := isTrue(now) || isTrue(future)
		var fact *CandidateFact
		message := "Posting explicitly excludes visa or OPT candidates; verify whether an exception applies."
		if opt {
			fact = &CandidateFact{Field: "visa_profile.current_status", Value: status, Statement: "Career profile records OPT work authorization."}
		} else if needsSponsorship {
			fact = &CandidateFact{Field: "visa_profile.requires_sponsorship", Value: true, Statement: "Career profile says employer sponsorship is required now or in the future."}
		} else if ambiguous || now.Kind != "bool" || future.Kind != "bool" || status == "" {
			fact = &CandidateFact{Field: "visa_profile.sponsorship_compatibility", Statement: "Career profile does not confirm current and future sponsorship compatibility."}
			message = "Posting explicitly excludes visa or OPT candidates; Career profile compatibility is unknown."
		}
		if fact != nil {
			if err := s.policyFinding(workAuthorization, "visa_or_opt_exclusion", message, "visa_signals.no_visa_or_opt", *fact, s.job.gateEvidence["sponsorship"]); err != nil {
				return err
			}
		}
	}
	if truthy(get(s.input.Profile, "requires_future_sponsorship")) && get(s.job.signals, "sponsorship_mentioned").Kind == "null" {
		return retain(s, &s.result.ManualReview, "Sponsorship language is not stated; verify before applying.")
	}
	return nil
}
