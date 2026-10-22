package emailconnection

import (
	"strings"
	"testing"

	"careerhub.local/backend/internal/emailrouting"
)

func validNewInput() ConfigureNewInput {
	return ConfigureNewInput{
		AccountAddress:     "  Alerts@Example.Test ",
		Password:           "abcd efgh ijkl mnop",
		JobSenders:         []string{" JOBS@Example.Test ", "both@example.test", "jobs@example.test"},
		ApplicationSenders: []string{"both@example.test", "TRACK@Example.Test"},
		ExpectedRevision:   0,
	}
}

func TestNormalizeNewConnectionSourceShape(t *testing.T) {
	values, problem := normalizeNewConnection(validNewInput())
	if problem != nil {
		t.Fatal(problem)
	}
	if values.account != "alerts@example.test" || values.password != "abcdefghijklmnop" || values.interval != 30 || values.scheduled {
		t.Fatalf("incorrect normalized defaults: %+v", values)
	}
	want := []normalizedSender{
		{"both@example.test", emailrouting.SenderPurposeBoth, emailrouting.EmptyPolicy().CanonicalJSON()},
		{"jobs@example.test", emailrouting.SenderPurposeJobAlerts, emailrouting.EmptyPolicy().CanonicalJSON()},
		{"track@example.test", emailrouting.SenderPurposeApplicationTracking, emailrouting.EmptyPolicy().CanonicalJSON()},
	}
	if len(values.senders) != len(want) {
		t.Fatalf("sender count: %d", len(values.senders))
	}
	for i := range want {
		if values.senders[i] != want[i] {
			t.Fatalf("sender %d: %+v", i, values.senders[i])
		}
	}
}

func TestNormalizeNewConnectionCustomPolicy(t *testing.T) {
	input := validNewInput()
	raw := `{"application_tracking":{"include_any":[],"exclude_any":[]},"job_alerts":{"include_any":["Software Engineer"],"exclude_any":[]}}`
	input.SubjectPolicies = map[string]string{
		"jobs@example.test":  raw,
		"both@example.test":  "{}",
		"track@example.test": "{}",
	}
	values, problem := normalizeNewConnection(input)
	if problem != nil {
		t.Fatal(problem)
	}
	policy, err := emailrouting.ParsePolicyJSON(raw)
	if err != nil || values.senders[1].policyJSON != policy.CanonicalJSON() {
		t.Fatalf("custom sender policy not canonicalized: %v %+v", err, values.senders[1])
	}
}

func TestNormalizeNewConnectionCountsDistinctSenders(t *testing.T) {
	input := validNewInput()
	input.JobSenders = make([]string, 101)
	for i := range input.JobSenders {
		input.JobSenders[i] = "jobs@example.test"
	}
	values, problem := normalizeNewConnection(input)
	if problem != nil || len(values.senders) != 3 {
		t.Fatalf("duplicate source senders rejected: %v %+v", problem, values.senders)
	}
}

func TestNormalizeEditSettingsRevisionAndNoPassword(t *testing.T) {
	input := EditSettingsInput{
		AccountAddress: "ALERTS@example.test", JobSenders: []string{"jobs@example.test"},
		ExpectedRevision: 7,
	}
	values, problem := normalizeEditSettings(input)
	if problem != nil || values.account != "alerts@example.test" || values.password != "" || values.interval != 30 {
		t.Fatalf("settings normalization: %+v %v", values, problem)
	}
	input.ExpectedRevision = 0
	if _, problem := normalizeEditSettings(input); problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
		t.Fatalf("zero edit revision admitted: %v", problem)
	}
}

func TestNormalizeRotationRequiresNewPasswordAndPositiveRevision(t *testing.T) {
	input := RotateInput{
		AccountAddress: " Alerts@Example.Test ", Password: "abcd efgh ijkl mnop",
		JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 7,
	}
	values, problem := normalizeRotation(input)
	if problem != nil || values.account != "alerts@example.test" || values.password != "abcdefghijklmnop" || len(values.senders) != 1 {
		t.Fatalf("rotation normalization: %+v %v", values, problem)
	}
	input.Password = ""
	if _, problem := normalizeRotation(input); problem == nil || problem.Code != "EMAIL_CREDENTIAL_MISSING" {
		t.Fatalf("rotation without password admitted: %v", problem)
	}
	input.Password, input.ExpectedRevision = "abcdefghijklmnop", 0
	if _, problem := normalizeRotation(input); problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
		t.Fatalf("zero rotation revision admitted: %v", problem)
	}
}

func TestNormalizeNewConnectionRejectsUnsafeInput(t *testing.T) {
	cases := map[string]func(*ConfigureNewInput){
		"missing sender":      func(v *ConfigureNewInput) { v.JobSenders = nil; v.ApplicationSenders = nil },
		"display address":     func(v *ConfigureNewInput) { v.AccountAddress = "Jobs <alerts@example.test>" },
		"invalid sender":      func(v *ConfigureNewInput) { v.JobSenders = []string{"jobs@example.test, other@example.test"} },
		"invalid interval":    func(v *ConfigureNewInput) { n := 14; v.IntervalMinutes = &n },
		"negative revision":   func(v *ConfigureNewInput) { v.ExpectedRevision = -1 },
		"oversized secret":    func(v *ConfigureNewInput) { v.Password = strings.Repeat("a", 513) },
		"incomplete policies": func(v *ConfigureNewInput) { v.SubjectPolicies = map[string]string{"jobs@example.test": "{}"} },
		"unrecognized policy": func(v *ConfigureNewInput) {
			v.SubjectPolicies = map[string]string{"nobody@example.test": "{}", "both@example.test": "{}", "track@example.test": "{}"}
		},
	}
	for label, alter := range cases {
		t.Run(label, func(t *testing.T) {
			input := validNewInput()
			alter(&input)
			_, problem := normalizeNewConnection(input)
			if problem == nil || problem.Code != "EMAIL_CONNECTION_INVALID" {
				t.Fatalf("unsafe input admitted: %v", problem)
			}
		})
	}
	input := validNewInput()
	input.ExpectedRevision = 1
	if _, problem := normalizeNewConnection(input); problem == nil || problem.Code != "EMAIL_CONNECTION_REVISION_CONFLICT" {
		t.Fatalf("nonzero initial revision admitted: %v", problem)
	}
}
