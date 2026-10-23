package jobdigest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func TestEligibilityProjectionPresenceAndStrictTypes(t *testing.T) {
	raw := []byte("current_status: '  STEM OPT  '\ncitizenship_required_blocker: true\nclearance_required_blocker: false\nrequires_sponsorship_now: null\nignored_private_document: never_copy_this\n42: ignored\n")
	value, err := cataloginput.ParseYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	before := value
	got, failure := ProfileProjection(context.Background(), value)
	if failure != nil {
		t.Fatal(failure)
	}
	if got.CurrentStatus != "  STEM OPT  " || !got.CitizenshipBlocker.Present || got.CitizenshipBlocker.Value == nil || !*got.CitizenshipBlocker.Value || got.ClearanceBlocker.Value == nil || *got.ClearanceBlocker.Value || !got.SponsorshipNow.Present || got.SponsorshipNow.Value != nil || got.SponsorshipFuture.Present {
		t.Fatal("presence/coercion contract")
	}
	*got.CitizenshipBlocker.Value = false
	if !reflect.DeepEqual(before, value) {
		t.Fatal("projection aliases source")
	}
	for _, raw := range []string{"null", "{}", "current_status: null"} {
		v, _ := cataloginput.ParseYAML([]byte(raw))
		got, err := ProfileProjection(context.Background(), v)
		if err != nil || !reflect.DeepEqual(got, EligibilityProfile{}) {
			t.Fatal("empty/null profile")
		}
	}
	for _, raw := range []string{"[]", "current_status: 1", "current_status: false", "requires_sponsorship_now: 'false'", "requires_future_sponsorship: []", "citizenship_required_blocker: 1"} {
		v, err := cataloginput.ParseYAML([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		got, failure := ProfileProjection(context.Background(), v)
		if !errors.Is(failure, ErrInput) || !reflect.DeepEqual(got, EligibilityProfile{}) {
			t.Fatalf("invalid projection %q: %v", raw, failure)
		}
	}
	for _, bad := range []cataloginput.Value{
		{Kind: "mapping", Entries: make([]cataloginput.Entry, MaxItems+1)},
		{Kind: "mapping", Entries: []cataloginput.Entry{{Key: cataloginput.Value{Kind: "string", Text: "current_status"}, Value: cataloginput.Value{Kind: "string", Text: strings.Repeat("x", 4097)}}}},
	} {
		if _, err := ProfileProjection(context.Background(), bad); !errors.Is(err, ErrLimit) {
			t.Fatal("profile limit")
		}
	}
	v, _ := cataloginput.ParseYAML([]byte("current_status: OPT"))
	duplicated := v
	duplicated.Entries = append(append([]cataloginput.Entry{}, v.Entries...), v.Entries[0])
	if _, err := ProfileProjection(context.Background(), duplicated); !errors.Is(err, ErrInput) {
		t.Fatal("noncanonical duplicate input")
	}
	v.Entries[0].Value.Text = string([]byte{255})
	if _, err := ProfileProjection(context.Background(), v); !errors.Is(err, ErrInput) {
		t.Fatal("invalid Unicode projection")
	}
	if _, err := ProfileProjection(nil, value); !errors.Is(err, ErrInput) {
		t.Fatal("nil context")
	}
	baseline := &cancelAfter{Context: context.Background()}
	if _, err := ProfileProjection(baseline, value); err != nil {
		t.Fatal(err)
	}
	for at := 1; at <= baseline.calls; at++ {
		got, err := ProfileProjection(&cancelAfter{Context: context.Background(), at: at}, value)
		if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, EligibilityProfile{}) {
			t.Fatal("partial canceled profile")
		}
	}
}
