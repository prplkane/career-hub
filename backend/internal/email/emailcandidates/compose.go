package emailcandidates

import (
	"errors"
	"strings"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/email/emailmime"
)

const MaxCandidatesPerMessage = 100

var ErrCandidateLimit = errors.New("EMAIL_CANDIDATE_LIMIT_EXCEEDED")

// EnrichedCandidate adds only the source's deterministic capture projection.
// It does not imply that the URL was opened or a full JD was verified.
type EnrichedCandidate struct {
	Candidate
	CaptureFacts discovery.Facts `json:"capture_facts"`
}

type ComposedMessage struct {
	Candidates             []EnrichedCandidate
	ApplicationOutcomes    []ApplicationOutcome
	IgnoredLinkCount       int
	HTMLLinkCount          int
	HTMLRelationReferences int
	VisibleText            string
}

// ComposeSelected is the offline candidate/outcome stage for an already
// decoded and selected MIME message with validated sender and subject. It
// performs no header parsing, mailbox access, URL fetch or persistent write.
func ComposeSelected(decoded emailmime.Result, senderAddress, subject string) (ComposedMessage, error) {
	composed := ComposedMessage{}
	var candidates []Candidate
	seenURLs := make(map[string]bool)
	var visibleParts []string
	for _, part := range decoded.Selected {
		var extracted []Candidate
		var ignored int
		var visible string
		if part.ContentType == "text/html" {
			result, err := ExtractHTMLSource(
				part.Text, MaxHTMLLinks-composed.HTMLLinkCount,
				MaxHTMLRelationReferences-composed.HTMLRelationReferences,
				senderAddress, subject,
			)
			if err != nil {
				return ComposedMessage{}, err
			}
			extracted, ignored, visible = result.Candidates, result.IgnoredLinks, result.VisibleText
			composed.HTMLLinkCount += result.AnchorCount
			composed.HTMLRelationReferences += result.RelationReferenceCount
		} else {
			extracted, ignored = ExtractPlain(part.Text)
			visible = normalizeVisible(part.Text)
		}
		composed.IgnoredLinkCount += ignored
		if visible != "" {
			visibleParts = append(visibleParts, visible)
		}
		for _, candidate := range extracted {
			if seenURLs[candidate.SourceURL] {
				continue
			}
			seenURLs[candidate.SourceURL] = true
			if len(candidates) >= MaxCandidatesPerMessage {
				return ComposedMessage{}, ErrCandidateLimit
			}
			candidates = append(candidates, candidate)
		}
	}
	composed.VisibleText = strings.Join(visibleParts, " ")
	candidates, composed.ApplicationOutcomes = SeparateApplicationOutcomes(
		subject, senderAddress, composed.VisibleText, candidates,
	)
	for i, candidate := range candidates {
		facts, err := discovery.ProjectEmailFacts(discovery.FactInput{
			Provider: candidate.SourceProvider, Title: candidate.Title,
			Company: candidate.Company, Location: candidate.Location,
			SourceText: candidate.SourceText,
		})
		if err != nil {
			return ComposedMessage{}, ErrHTMLParse
		}
		if facts.Location != "" {
			candidate.Location = facts.Location
		}
		candidate.Ordinal = i + 1
		composed.Candidates = append(composed.Candidates, EnrichedCandidate{Candidate: candidate, CaptureFacts: facts})
	}
	return composed, nil
}
