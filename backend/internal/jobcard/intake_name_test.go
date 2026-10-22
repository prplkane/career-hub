package jobcard

import (
	"strconv"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobdigest"
)

func nameDigest() *jobdigest.Result {
	return &jobdigest.Result{ContentFingerprint: strings.Repeat("a", 64), Fields: map[string]jobdigest.Field{
		"company": {Value: "Example Co"}, "title": {Value: "Platform Engineer"},
	}}
}

func TestSelectIntakeDestinationUsesReadableNameAndBoundedCollisions(t *testing.T) {
	fresh := nameDigest()
	first, err := SelectIntakeDestination("Role.TXT", fresh, nil, nil)
	if err != nil || first.ID != "role" || first.RawPath != "jobs/incoming/role.txt" || first.CardPath != "jobs/normalized/role.yaml" {
		t.Fatal("first destination", first, err)
	}
	suffix := fresh.ContentFingerprint[:12]
	second, err := SelectIntakeDestination("Role.TXT", fresh, []string{"ROLE.txt"}, []string{"role_" + suffix + ".yaml"})
	if err != nil || second.ID != "role_"+suffix+"_2" || reanalysisSourceID(second.RawPath) != second.ID {
		t.Fatal("collision destination", second, err)
	}
	alternate, err := SelectIntakeDestination("Role.TXT", fresh, []string{"ROLE.md"}, []string{"role_" + suffix + ".yml"})
	if err != nil || alternate.ID != "role_"+suffix+"_2" {
		t.Fatal("alternate suffix reused an occupied job ID", alternate, err)
	}
	derived, err := SelectIntakeDestination("", fresh, nil, nil)
	if err != nil || derived.ID != "example_co_platform_engineer" || !strings.HasSuffix(derived.RawPath, ".md") {
		t.Fatal("derived destination", derived, err)
	}
}

func TestSelectIntakeDestinationHandlesUnicodeLengthAndDevices(t *testing.T) {
	fresh := nameDigest()
	unicodeName, err := SelectIntakeDestination("Работа Аналитик.md", fresh, nil, nil)
	if err != nil || unicodeName.ID != "работа_аналитик" {
		t.Fatal("Unicode name", unicodeName, err)
	}
	device, err := SelectIntakeDestination("CON.md", fresh, nil, nil)
	if err != nil || device.ID != "job_con" {
		t.Fatal("Windows device name", device, err)
	}
	longName, err := SelectIntakeDestination(strings.Repeat("é", 120)+".md", fresh, nil, nil)
	if err != nil || len(longName.ID) > intakeNameBaseBytes || !strings.HasPrefix(longName.ID, "é") || reanalysisSourceID(longName.RawPath) != longName.ID {
		t.Fatal("bounded Unicode name", longName, err)
	}
}

func TestSelectIntakeDestinationFailsClosedOnInvalidOrExhaustedNames(t *testing.T) {
	for _, name := range []string{"../role.md", "role/other.md", "role.pdf", "role.", "CON\\x.md", strings.Repeat("x", 513)} {
		if dest, err := SelectIntakeDestination(name, nameDigest(), nil, nil); err == nil || dest != (IntakeDestination{}) {
			t.Fatal("invalid name accepted", name, dest, err)
		}
	}
	fresh := nameDigest()
	fresh.ContentFingerprint = "not-a-fingerprint"
	if dest, err := SelectIntakeDestination("role.md", fresh, nil, nil); err == nil || dest != (IntakeDestination{}) {
		t.Fatal("invalid fingerprint accepted", dest, err)
	}
	fresh = nameDigest()
	names := []string{"role.yaml", "role_" + fresh.ContentFingerprint[:12] + ".yaml"}
	for attempt := 2; attempt < intakeNameAttempts; attempt++ {
		names = append(names, "role_"+fresh.ContentFingerprint[:12]+"_"+strconv.Itoa(attempt)+".yaml")
	}
	if dest, err := SelectIntakeDestination("role.md", fresh, nil, names); err == nil || err.Code != "JOB_INTAKE_NAME_EXHAUSTED" || dest != (IntakeDestination{}) {
		t.Fatal("exhausted namespace accepted", dest, err)
	}
}
