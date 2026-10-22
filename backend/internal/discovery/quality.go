package discovery

import (
	"regexp"
	"slices"
	"strings"

	"careerhub.local/backend/internal/joburls"
	"careerhub.local/backend/internal/textcompat"
)

type QualityInput struct {
	Title, Company, Location string
	SourceURL                string `json:"source_url"`
}
type Quality struct {
	Version     int      `json:"version"`
	Status      string   `json:"status"`
	Label       string   `json:"label"`
	Summary     string   `json:"summary"`
	ReasonCodes []string `json:"reason_codes"`
}

var reviewLabel = regexp.MustCompile(`^\d[\d,]*\s+reviews?$`)
var combinedFields = regexp.MustCompile(`\s+[•·]\s+`)
var serviceLabels = []string{
	"account", "complete additional step", "contact indeed", "email preferences", "help",
	"learn how to avoid scams", "login", "manage email preferences", "manage preferences",
	"manage your job alerts", "preferences", "privacy", "privacy policy", "sign in", "terms",
	"unsubscribe", "update your career interests", "view application", "view in browser",
	"view more jobs", "your privacy choices",
}

// ClassifyEmailQuality reports extraction quality only. It does not score fit,
// change capture state, suppress jobs or add UI warnings by itself.
func ClassifyEmailQuality(input QualityInput) (Quality, error) {
	if !validTexts(input.Title, input.Company, input.Location, input.SourceURL) {
		return Quality{}, ErrUnsupported
	}
	title, company, place, source := normalize(input.Title), normalize(input.Company), normalize(input.Location), normalize(input.SourceURL)
	for _, candidate := range []string{title, source} {
		noise, err := joburls.KnownNonJobDestination(candidate)
		if err != nil {
			return Quality{}, err
		}
		if noise {
			return Quality{2, "suspected_noise", "Likely not a job", "This link points to an event or career-resource page, not a job posting.", []string{"non_job_destination"}}, nil
		}
	}
	if slices.Contains(serviceLabels, textcompat.Fold(title)) || reviewLabel.MatchString(matching(title).folded) {
		return Quality{2, "suspected_noise", "Likely not a job", "This title matches a known email service or navigation action.", []string{"service_link_label"}}, nil
	}
	missing := []string{}
	if title == "" {
		missing = append(missing, "missing_title")
	}
	if company == "" {
		missing = append(missing, "missing_company")
	}
	if place == "" {
		missing = append(missing, "missing_location")
	}
	if title == "" {
		return Quality{2, "needs_review", "Check extraction", "The email candidate did not produce a usable job title.", missing}, nil
	}
	if company == "" && combinedFields.MatchString(title) {
		return Quality{2, "needs_review", "Check extracted fields", "The role, company, or location may have merged into the title after the sender changed its email layout.", append([]string{"combined_job_fields"}, missing...)}, nil
	}
	if len(missing) > 0 {
		labels := []string{}
		if company == "" {
			labels = append(labels, "company")
		}
		if place == "" {
			labels = append(labels, "location")
		}
		return Quality{2, "limited", "Limited email details", "The alert did not state " + strings.Join(labels, " and ") + ". The full posting may still be valid.", missing}, nil
	}
	return Quality{2, "ok", "Card fields available", "The email supplied separate role, company, and location fields.", []string{}}, nil
}
