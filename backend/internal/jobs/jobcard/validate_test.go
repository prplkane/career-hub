package jobcard

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/joburls"
	"careerhub.local/backend/internal/skills/cataloginput"
)

const syntheticCard = `id: synthetic
status: ready
company: Synthetic
title: Engineer
source_url: https://example.test/jobs/1
location: ''
remote_policy: ''
seniority: ''
required_skills: [Python]
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: [Build services.]
visa_signals:
  citizenship_required: null
  clearance_required: null
  sponsorship_mentioned: null
  no_visa_or_opt: null
raw_jd_path: jobs/incoming/synthetic.txt
`

func cardFixture(t *testing.T) cataloginput.Value {
	t.Helper()
	value, err := cataloginput.ParseYAML([]byte(syntheticCard))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func put(value *cataloginput.Value, key string, item cataloginput.Value) {
	for i := range value.Entries {
		if value.Entries[i].Key.Text == key {
			value.Entries[i].Value = item
			return
		}
	}
	value.Entries = append(value.Entries, cataloginput.Entry{Key: cataloginput.Value{Kind: "string", Text: key}, Value: item})
}
func requireFailure(t *testing.T, report *Report, err *Error, code string) {
	t.Helper()
	if report != nil || err == nil || err.Code != code {
		t.Fatalf("expected no report and %s, got=%v err=%#v", code, report, err)
	}
}

func TestInputSafetyAndPrivacy(t *testing.T) {
	for _, kind := range []string{"complex", "object", "unknown"} {
		report, err := Validate(context.Background(), cataloginput.Value{Kind: kind}, syntheticCheck)
		requireFailure(t, report, err, "JOB_CARD_INPUT_UNSUPPORTED")
	}
	for _, test := range []cataloginput.Value{
		{Kind: "string", Text: string([]byte{255})}, {Kind: "int", Text: "01"}, {Kind: "int", Text: "-0"}, {Kind: "int", Text: "1e3"}, {Kind: "int", Text: strings.Repeat("1", 1025)},
		{Kind: "float", Text: "NaN"}, {Kind: "float", Text: "Inf"}, {Kind: "float", Text: "1e999"}, {Kind: "float", Text: " 1.0 "}, {Kind: "float", Text: "0x1p0"}, {Kind: "float", Text: "1_000.0"},
		{Kind: "mapping", Entries: []cataloginput.Entry{{Key: cataloginput.Value{Kind: "bool"}}}},
		{Kind: "string", Items: []cataloginput.Value{{Kind: "null"}}}, {Kind: "string", Entries: []cataloginput.Entry{{}}},
		{Kind: "string", Bytes: []byte{1}}, {Kind: "string", Boolean: true}, {Kind: "mapping", Text: "hidden"},
	} {
		report, err := Validate(context.Background(), test, syntheticCheck)
		requireFailure(t, report, err, "JOB_CARD_INPUT_UNSUPPORTED")
	}
	value := cardFixture(t)
	value.Entries = append(value.Entries, value.Entries[0])
	report, err := Validate(context.Background(), value, syntheticCheck)
	requireFailure(t, report, err, "JOB_CARD_INPUT_UNSUPPORTED")
	for name, item := range map[string]cataloginput.Value{
		"bytes":   {Kind: "bytes", Bytes: make([]byte, MaxInputBytes+1)},
		"text":    {Kind: "string", Text: strings.Repeat("a", MaxInputBytes+1)},
		"nodes":   {Kind: "sequence", Items: make([]cataloginput.Value, maxNodes)},
		"entries": {Kind: "mapping", Entries: make([]cataloginput.Entry, maxNodes)},
	} {
		t.Run(name, func(t *testing.T) {
			report, err := Validate(context.Background(), item, syntheticCheck)
			requireFailure(t, report, err, "JOB_CARD_INPUT_LIMIT")
		})
	}
	deep := cataloginput.Value{Kind: "null"}
	for range cataloginput.YAMLMaxDepth + 1 {
		deep = cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{deep}}
	}
	report, err = Validate(context.Background(), deep, syntheticCheck)
	requireFailure(t, report, err, "JOB_CARD_INPUT_LIMIT")
	value = cardFixture(t)
	items := make([]cataloginput.Value, MaxIssues+1)
	for i := range items {
		items[i] = cataloginput.Value{Kind: "null"}
	}
	put(&value, "required_skills", cataloginput.Value{Kind: "sequence", Items: items})
	report, err = Validate(context.Background(), value, func(context.Context, string) (bool, *Error) { t.Fatal("I/O after issue limit"); return false, nil })
	requireFailure(t, report, err, "JOB_CARD_ISSUE_LIMIT")
	report, err = Validate(context.Background(), cardFixture(t), nil)
	requireFailure(t, report, err, "JOB_CARD_SOURCE_CHECK_REQUIRED")
	report, err = Validate(context.Background(), cardFixture(t), func(context.Context, string) (bool, *Error) {
		return false, &Error{Code: "PRIVATE path password token"}
	})
	requireFailure(t, report, err, "JOB_CARD_SOURCE_UNAVAILABLE")
	if strings.Contains(fmt.Sprintf("%v %#v %+v %s", err, err, err, err), "PRIVATE") {
		t.Fatal("provider data leaked")
	}
	urlCard := cardFixture(t)
	put(&urlCard, "source_url", cataloginput.Value{Kind: "string", Text: "https://example.test/" + strings.Repeat("x", joburls.MaxURLBytes)})
	report, err = Validate(context.Background(), urlCard, syntheticCheck)
	requireFailure(t, report, err, "JOB_CARD_URL_LIMIT")
}

type cancelAfter struct {
	context.Context
	calls, at int
}

func (c *cancelAfter) Err() error {
	c.calls++
	if c.at > 0 && c.calls >= c.at {
		return context.Canceled
	}
	return nil
}
func TestEveryValidationCancellationBoundary(t *testing.T) {
	card := cardFixture(t)
	baseline := &cancelAfter{Context: context.Background()}
	report, err := Validate(baseline, card, syntheticCheck)
	if err != nil || !report.SchemaValid() {
		t.Fatal("baseline failed")
	}
	for at := 1; at <= baseline.calls; at++ {
		ctx := &cancelAfter{Context: context.Background(), at: at}
		report, err := Validate(ctx, card, syntheticCheck)
		requireFailure(t, report, err, "JOB_CARD_CANCELED")
	}
	report, err = Validate(nil, card, syntheticCheck)
	requireFailure(t, report, err, "JOB_CARD_CANCELED")
	ctx, cancel := context.WithCancel(context.Background())
	report, err = Validate(ctx, card, func(context.Context, string) (bool, *Error) { cancel(); return true, nil })
	requireFailure(t, report, err, "JOB_CARD_CANCELED")
	t.Logf("validated cancellation boundaries=%d", baseline.calls)
}

func TestSharedIdentityAndURLBounds(t *testing.T) {
	for _, item := range []applicationanalysis.ContactCandidate{
		{SourceText: strings.Repeat("a", applicationanalysis.MaxSourceBytes+1)},
		{Name: string([]byte{255})},
		{Channels: make([]applicationanalysis.Channel, 129)},
		{Channels: []applicationanalysis.Channel{{Kind: "email", Value: string([]byte{255})}}},
		{Channels: []applicationanalysis.Channel{{Kind: string([]byte{255})}}},
	} {
		if id, err := applicationanalysis.CandidateID(item); err == nil || id != "" {
			t.Fatal("unbounded candidate identity")
		}
	}
	for _, url := range []string{"https://example.test:65536/a", "https://example.test:/a", "https://user@example.test/a", "https://example.test\uff0fpath", "https://[::1", "https://[v1.z]/a", "https://example.test/ a", "https://example.test:1:2/a", "https://::1/a"} {
		if ok, err := joburls.ValidSource(url); ok || err != nil {
			t.Fatalf("bad syntax accepted: %q", url)
		}
	}
	for _, url := range []string{"https://example.test:443/a?x=1#b", "http://localhost:0/a", "https://[::1]/job", "https://example.test/path%20space"} {
		if ok, err := joburls.ValidSource(url); !ok || err != nil {
			t.Fatalf("safe syntax rejected: %q", url)
		}
	}
	for _, url := range []string{string([]byte{255}), strings.Repeat("x", joburls.MaxURLBytes+1)} {
		if ok, err := joburls.ValidSource(url); ok || err == nil {
			t.Fatal("URL bounds ignored")
		}
	}
}
