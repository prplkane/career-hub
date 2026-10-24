// Package applicationhelper owns the existing attended helper's privacy
// settings, short-answer bank and minimized projection. It never fills a form,
// submits an application, reads credentials or infers candidate facts.
package applicationhelper

import (
	"crypto/sha256"
	"fmt"
	"io"
)

const (
	ConfigurationPath = "career_vault/application_helper.yaml"
	DemographicsPath  = "career_vault/application_demographics.yaml"
	AnswersPath       = "career_vault/application_answers.yaml"
	MaxAnswers        = 200
	MaxInputBytes     = 2 << 20
	MaxResponseBytes  = 4 << 20
	maxTextBytes      = 16 << 10
)

type Error struct {
	Stage, Code, SourceClass string
	Committed                bool
}

func (*Error) Error() string                { return "Application helper operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func failure(stage, code string) *Error     { return &Error{Stage: stage, Code: code} }
func invalid(class string) *Error {
	code := "APPLICATION_HELPER_CONFIGURATION_INVALID"
	if class == "ApplicationDemographicsError" {
		code = "APPLICATION_DEMOGRAPHICS_INVALID"
	}
	return &Error{Stage: "validation", Code: code, SourceClass: class}
}
func conflict(class string) *Error {
	code := "APPLICATION_HELPER_REVISION_CONFLICT"
	if class == "ApplicationAnswerRevisionConflict" {
		code = "APPLICATION_ANSWER_REVISION_CONFLICT"
	}
	if class == "ApplicationDemographicsRevisionConflict" {
		code = "APPLICATION_DEMOGRAPHICS_REVISION_CONFLICT"
	}
	return &Error{Stage: "revision", Code: code, SourceClass: class}
}
func sourceUnavailable() *Error {
	return &Error{Stage: "source", Code: "APPLICATION_HELPER_SOURCE_UNAVAILABLE", SourceClass: "ApplicationHelperSourceUnavailable"}
}
func canceled() *Error           { return failure("context", "APPLICATION_HELPER_CANCELED") }
func revision(raw []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) }

type field struct {
	id, label, source, document, kind string
	path                              []string
}

var fields = []field{
	{"full_name", "Full name", "Profile · Name", "profile", "text", []string{"name"}},
	{"given_name", "Given name", "Profile · Given name", "profile", "text", []string{"given_name"}},
	{"family_name", "Family name", "Profile · Family name", "profile", "text", []string{"family_name"}},
	{"preferred_name", "Preferred name", "Profile · Preferred name", "profile", "text", []string{"preferred_name"}},
	{"email", "Email", "Profile · Contact · Email", "profile", "text", []string{"contact", "email"}},
	{"phone", "Phone", "Profile · Contact · Phone", "profile", "text", []string{"contact", "phone"}},
	{"address", "Full address", "Profile · Contact · Full address", "profile", "text", []string{"contact", "address"}},
	{"address_line1", "Address line 1", "Profile · Contact · Address line 1", "profile", "text", []string{"contact", "address_line1"}},
	{"address_line2", "Address line 2", "Profile · Contact · Address line 2", "profile", "text", []string{"contact", "address_line2"}},
	{"city", "City", "Profile · Contact · City", "profile", "text", []string{"contact", "city"}},
	{"region", "State / province / region", "Profile · Contact · Region", "profile", "text", []string{"contact", "region"}},
	{"postal_code", "Postal code", "Profile · Contact · Postal code", "profile", "text", []string{"contact", "postal_code"}},
	{"country", "Country", "Profile · Contact · Country", "profile", "text", []string{"contact", "country"}},
	{"current_location", "Current location", "Profile · Current location", "profile", "text", []string{"current_location"}},
	{"linkedin", "LinkedIn", "Profile · Contact · LinkedIn", "profile", "text", []string{"contact", "linkedin"}},
	{"github", "GitHub", "Profile · Contact · GitHub", "profile", "text", []string{"contact", "github"}},
	{"website", "Website / portfolio", "Profile · Contact · Website", "profile", "text", []string{"contact", "website"}},
	{"major", "Major", "Profile · Major", "profile", "text", []string{"major"}},
	{"current_status", "Current status", "Eligibility · Current status", "eligibility", "text", []string{"current_status"}},
	{"work_authorization", "Work authorization statement", "Eligibility · Authorization statement", "eligibility", "text", []string{"work_authorization"}},
	{"requires_sponsorship_now", "Sponsorship needed now", "Eligibility · Sponsorship needed now", "eligibility", "boolean", []string{"requires_sponsorship_now"}},
	{"requires_future_sponsorship", "Future sponsorship needed", "Eligibility · Future sponsorship needed", "eligibility", "boolean", []string{"requires_future_sponsorship"}},
	{"stem_opt_eligible", "STEM OPT eligible", "Eligibility · STEM OPT eligible", "eligibility", "boolean", []string{"stem_opt_eligible"}},
	{"us_gender_identity", "US gender identity", "US · Gender", "demographics", "text", []string{"US", "gender"}},
	{"us_eeo_gender", "US EEO gender", "US · Gender", "demographics", "text", []string{"US", "gender"}},
	{"us_hispanic_latino", "US Hispanic or Latino", "US · Hispanic or Latino", "demographics", "text", []string{"US", "hispanic_latino"}},
	{"us_veteran_status", "US veteran status", "US · Veteran status", "demographics", "text", []string{"US", "veteran_status"}},
	{"us_disability_status", "US disability status", "US · Disability status", "demographics", "text", []string{"US", "disability_status"}},
}

type demographic struct {
	id, label string
	options   []string
}

var demographics = []demographic{
	{"gender", "Gender", []string{"Male", "Female", "Prefer not to answer"}},
	{"hispanic_latino", "Hispanic or Latino", []string{"Yes", "No", "Prefer not to answer"}},
	{"veteran_status", "Veteran status", []string{"Not a protected veteran", "Protected veteran", "Prefer not to answer"}},
	{"disability_status", "Disability status", []string{"Yes", "No", "Prefer not to answer"}},
}
