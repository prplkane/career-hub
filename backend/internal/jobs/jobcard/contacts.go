package jobcard

import (
	"unicode/utf8"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

func (v *validator) contactChannels(value cataloginput.Value, path string) []applicationanalysis.Channel {
	result := []applicationanalysis.Channel{}
	if !v.list(value, path, false) {
		return result
	}
	if len(value.Items) > 8 {
		v.add(path, "exceeds bounded maximum")
		return result
	}
	seen := map[[2]string]bool{}
	for i, item := range value.Items {
		current := itemPath(path, i)
		if !v.mapping(item, current) {
			continue
		}
		kind, text := get(item, "kind"), get(item, "value")
		validKind := member(kind, "email", "phone", "professional_profile")
		if !validKind {
			v.add(current+".kind", "unexpected kind")
		}
		if !nonempty(text) {
			v.add(current+".value", "expected non-empty string")
		} else if utf8.RuneCountInString(text.Text) > 300 {
			v.add(current+".value", "exceeds bounded maximum")
		}
		if validKind && nonempty(text) {
			key := [2]string{kind.Text, textcompat.Fold(textcompat.Strip(text.Text))}
			if seen[key] {
				v.add(path, "duplicate channel")
			}
			seen[key] = true
			result = append(result, applicationanalysis.Channel{Kind: kind.Text, Value: text.Text})
		}
	}
	return result
}

func (v *validator) contacts(value, fingerprint cataloginput.Value) {
	const path = "digest.contact_candidates"
	if value.Kind == "null" || !v.mapping(value, path) {
		return
	}
	v.sourceEnvelope(value, fingerprint, path, true)
	items := get(value, "candidates")
	if !v.list(items, path+".candidates", false) {
		items = cataloginput.Value{Kind: "sequence"}
	} else if len(items.Items) > 24 {
		v.add(path+".candidates", "exceeds bounded maximum")
	}
	v.sourceCounts(value, path, "candidate_count", "candidates", len(items.Items))
	for i, item := range items.Items {
		current := itemPath(path+".candidates", i)
		if !v.mapping(item, current) {
			continue
		}
		id, content := get(item, "candidate_id"), get(item, "content_sha256")
		if !hexDigest(id) {
			v.add(current+".candidate_id", "expected lowercase SHA-256")
		}
		if !hexDigest(content) {
			v.add(current+".content_sha256", "expected lowercase SHA-256")
		} else if hash := get(value, "content_sha256"); hash.Kind != "string" || content.Text != hash.Text {
			v.add(current+".content_sha256", "must match exact source hash")
		}
		for _, key := range []string{"name", "role", "company"} {
			value := get(item, key)
			if value.Kind != "string" {
				v.add(current+"."+key, "expected string")
			} else if utf8.RuneCountInString(value.Text) > 200 {
				v.add(current+"."+key, "exceeds bounded maximum")
			}
		}
		kind, confidence := get(item, "context_kind"), get(item, "confidence")
		validKind := member(kind, "application_channel", "application_recipient", "recruiter", "hiring_manager", "possible_team_contact")
		validConfidence := member(confidence, "explicit", "review")
		if !validKind {
			v.add(current+".context_kind", "unexpected context")
		}
		if !validConfidence {
			v.add(current+".confidence", "unexpected confidence")
		}
		channels := v.contactChannels(get(item, "channels"), current+".channels")
		if member(kind, "application_channel") {
			if truthy(get(item, "name")) || truthy(get(item, "role")) || truthy(get(item, "company")) {
				v.add(current, "application channel must not be represented as a person")
			}
			emails := len(channels) > 0
			for _, channel := range channels {
				if channel.Kind != "email" {
					emails = false
				}
			}
			if !emails {
				v.add(current+".channels", "expected application email")
			}
		} else if !nonempty(get(item, "name")) {
			v.add(current+".name", "required for a person candidate")
		}
		v.sourceLines(item, current)
		allStrings := true
		for _, key := range []string{"name", "role", "company", "source_text"} {
			if get(item, key).Kind != "string" {
				allStrings = false
			}
		}
		if hexDigest(id) && hexDigest(content) && validKind && validConfidence && allStrings && v.active() {
			expected, err := applicationanalysis.CandidateID(applicationanalysis.ContactCandidate{
				ContentSHA256: content.Text, ContextKind: kind.Text, Confidence: confidence.Text, Name: get(item, "name").Text,
				Role: get(item, "role").Text, Company: get(item, "company").Text, SourceText: get(item, "source_text").Text, Channels: channels,
			})
			if err != nil {
				v.failure = &Error{Code: "JOB_CARD_INPUT_LIMIT"}
				return
			}
			if expected != id.Text {
				v.add(current+".candidate_id", "does not match exact fields")
			}
		}
	}
}
