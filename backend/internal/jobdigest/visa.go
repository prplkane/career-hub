package jobdigest

import "careerhub.local/backend/internal/eligibilityfacts"

var visaPatterns = [][2]string{
	{"citizenship_required", `\b(?:must\s+be|has\s+to\s+be|requires?)\s+(?:a\s+)?(?:u\.?s\.?\s+)?citizen|\bu\.?s\.?\s+citizenship\s+(?:is\s+)?required`},
	{"clearance_required", `\bactive\s+(?:security\s+)?clearance\b|\b(?:security\s+)?clearance\s+(?:is\s+)?required|\bmust\s+(?:hold|have|possess).{0,30}\bclearance\b|\bmust\s+be\s+able\s+to\s+obtain(?:\s+and\s+maintain)?(?:\s+the)?(?:\s+required)?\s+clearance\b`},
	{"clearable", `\bclearable\b`},
	{"work_authorization_required", `\b(?:u\.?s\.?\s+)?work\s+authori[sz]ation\s+required\b|\blegally\s+authori[sz]ed\s+to\s+work\s+in\s+the\s+united\s+states\b`},
	{"export_control", `\bexport\s+control\b|\bprotected\s+individual\b`},
	{"no_visa_or_opt", `\b(?:no|without)\s+(?:visa\s+)?sponsorship\b|\b(?:will\s+not|cannot|unable\s+to)\s+(?:provide\s+)?sponsor|\bnot\s+eligible\s+for\s+(?:visa\s+)?sponsorship|\bwithout\s+the\s+need\s+for\s+(?:employer\s+)?sponsorship\b|\bOPT\s+(?:candidates?\s+)?(?:are\s+)?not\s+eligible`},
	{"sponsorship_available", `\b(?:visa\s+)?sponsorship\s+(?:is\s+)?(?:available|provided|offered)|\bwe\s+(?:can|will)\s+sponsor\b`},
	{"conditional_sponsorship", `\b(?:limited|conditional)\s+(?:immigration\s+|visa\s+)?sponsorship\s+may\s+be\s+available\b|\b(?:immigration\s+|visa\s+)?sponsorship\s+may\s+be\s+available\b`},
}

func trueValue(value OptionalBool) bool { return value.Value != nil && *value.Value }
func (a *analysis) visa() {
	matched := map[string][]Evidence{}
	for _, pattern := range visaPatterns {
		matched[pattern[0]] = []Evidence{}
	}
	for _, l := range a.lines {
		if !a.check() {
			return
		}
		text := analysisText(l.text)
		for _, pattern := range visaPatterns {
			if pattern[0] == "citizenship_required" && a.has(text, `\b(?:citizen|national)\b.{0,120}\b(?:permanent resident|refugee|asylee|protected individual)\b`, true) {
				continue
			}
			if a.has(text, pattern[1], true) {
				if !a.item() {
					return
				}
				matched[pattern[0]] = append(matched[pattern[0]], evidence(l.number, l.text, "visa_"+pattern[0]))
			}
		}
	}
	mentioned := a.has(a.input.Source, `\bsponsor(?:ship|ed|ing)?\b`, true)
	signals := map[string]*bool{"citizenship_required": nil, "clearance_required": nil, "sponsorship_mentioned": nil, "no_visa_or_opt": nil}
	for name := range signals {
		if len(matched[name]) > 0 || (name == "sponsorship_mentioned" && mentioned) {
			yes := true
			signals[name] = &yes
		}
	}
	gates := map[string]Gate{}
	for _, name := range []string{"citizenship", "clearance"} {
		signal := name + "_required"
		stated := signals[signal] != nil
		profile := a.input.Profile.CitizenshipBlocker
		if name == "clearance" {
			profile = a.input.Profile.ClearanceBlocker
		}
		gate := Gate{"not_stated", []string{}, append([]Evidence{}, matched[signal]...)}
		if stated {
			gate.Status = "unclear"
			gate.Reasons = append(gate.Reasons, "Posting explicitly requires "+name+".")
			if profile.Value != nil {
				gate.Status = "compatible"
				if *profile.Value {
					gate.Status = "incompatible"
				}
			}
		}
		if name == "clearance" && !stated && len(matched["clearable"]) > 0 {
			gate.Status = "unclear"
			gate.Reasons = []string{"Posting requires the candidate to be clearable; manual review is required."}
			gate.Evidence = matched["clearable"]
		}
		gates[name] = gate
	}
	known := a.input.Profile.SponsorshipNow.Present || a.input.Profile.SponsorshipFuture.Present
	needs := trueValue(a.input.Profile.SponsorshipNow) || trueValue(a.input.Profile.SponsorshipFuture)
	status := "not_stated"
	reasons := []string{}
	switch {
	case len(matched["no_visa_or_opt"]) > 0:
		opt, ambiguous := eligibilityfacts.OPTStatus(a.input.Profile.CurrentStatus)
		status = "unclear"
		if needs || opt {
			status = "incompatible"
		} else if known && !ambiguous {
			status = "compatible"
		}
		reasons = []string{"Posting says sponsorship or OPT is unavailable."}
	case len(matched["conditional_sponsorship"]) > 0:
		status = "unclear"
		reasons = []string{"Posting says sponsorship may be available with limitations."}
	case len(matched["sponsorship_available"]) > 0:
		status = "compatible"
		reasons = []string{"Posting explicitly offers sponsorship."}
	case needs:
		if mentioned {
			status = "unclear"
		}
		reasons = []string{"Future sponsorship requirement needs manual review."}
	case known:
		status = "compatible"
	}
	proof := append([]Evidence{}, matched["no_visa_or_opt"]...)
	proof = append(proof, matched["conditional_sponsorship"]...)
	proof = append(proof, matched["sponsorship_available"]...)
	gates["sponsorship"] = Gate{status, reasons, proof}
	proof = append([]Evidence{}, matched["work_authorization_required"]...)
	proof = append(proof, matched["export_control"]...)
	gate := Gate{"not_stated", []string{}, proof}
	if len(proof) > 0 {
		gate.Status = "unclear"
		gate.Reasons = []string{"Posting includes work-authorization or export-control language; verify manually."}
	}
	gates["work_authorization"] = gate
	a.result.Gates, a.result.VisaSignals = gates, signals
}
