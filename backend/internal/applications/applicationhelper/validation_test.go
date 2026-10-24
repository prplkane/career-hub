package applicationhelper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func TestQuestionAndAnswerIdentity(t *testing.T) {
	cases := [][3]string{
		{"  Ａｒｅ you willing to relocate? (REQUIRED)  ", "Are you willing to relocate?", "are you willing to relocate"},
		{"Do\x1cyou\u0085prefer\u3000Straße?!", "Do you prefer Straße?!", "do you prefer strasse"},
		{"您喜欢旅行吗？", "您喜欢旅行吗?", "您喜欢旅行吗"},
		{"Are you willing to relocate? * *", "Are you willing to relocate? *", "are you willing to relocate? *"},
	}
	for _, c := range cases {
		display, key, e := question(c[0])
		if e != nil || display != c[1] || key != revision([]byte(c[2])) {
			t.Fatalf("identity: %#v %q %q", e, display, key)
		}
	}
	for _, input := range []any{nil, 1, true, []any{}, "a\x00b", "abc", "12345", "What is your SALARY?", "Do you need work authoriZation?", "Your vİsa?", "Do you need ſponsorship?", strings.Repeat("x", 301), string([]byte{0xff}), "\u1c89\u1c89\u1c89\u1c89"} {
		if _, _, e := question(input); e == nil {
			t.Fatalf("question accepted: %T", input)
		}
	}
	for _, input := range []string{"Do you like banking?", "Are you willing to relocate?", "Travel preferences?"} {
		if _, _, e := question(input); e != nil {
			t.Fatalf("safe question rejected %q", input)
		}
	}
	kind, v, e := answerValue("choice", "  Ｙｅｓ\x1cplease ")
	if e != nil || kind != "choice" || v != "Yes please" {
		t.Fatal("value normalization")
	}
	kind, v, e = answerValue("boolean", false)
	if e != nil || kind != "boolean" || v != false {
		t.Fatal("explicit false lost")
	}
}
func TestAnswerBankBoundsRoundtripAndIDs(t *testing.T) {
	bank := []answer{}
	for i := 0; i < MaxAnswers; i++ {
		q := fmt.Sprintf("Travel preference %03d?", i)
		display, key, e := question(q)
		if e != nil {
			t.Fatal(e)
		}
		bank = append(bank, answer{fmt.Sprintf("answer_%032x", i+1), display, key, "text", "Yes: [no] \"maybe\"\n\u0085", "2026-09-10T12:00:00Z", "2026-09-10T12:00:00Z"})
	}
	// Stored canonical answers contain collapsed whitespace; encoding must not
	// silently normalize a malformed internal value tree.
	if _, e := answerBytes(context.Background(), bank); e == nil {
		t.Fatal("encoder accepted noncanonical values")
	}
	for i := range bank {
		bank[i].Value = "Yes: [no] \"maybe\""
	}
	raw, e := answerBytes(context.Background(), bank)
	if e != nil {
		t.Fatal(e)
	}
	got, _, e := answerBank(raw, true)
	if e != nil || !reflect.DeepEqual(got, bank) {
		t.Fatal("bounded roundtrip")
	}
	payload := map[string]any{"question": "Another travel preference?", "value_kind": "boolean", "value": true}
	if _, e := saveAnswer(bank, payload, "2026-09-10T12:00:00Z", func() (string, error) { t.Fatal("allocated full bank"); return "", nil }); e == nil {
		t.Fatal("full bank accepted")
	}
	if _, e := saveAnswer(nil, payload, "2026-09-10T12:00:00Z", func() (string, error) { return "private", errors.New("do not print") }); e == nil || e.Code != "APPLICATION_HELPER_ID_UNAVAILABLE" {
		t.Fatal("ID failure")
	}
	if _, e := saveAnswer(bank[:1], payload, "2026-09-10T12:00:00Z", func() (string, error) { return bank[0].ID, nil }); e == nil {
		t.Fatal("duplicate ID")
	}
	if _, _, e := answerBank(bytes.Repeat([]byte("x"), MaxInputBytes+1), true); e == nil || e.Code != "APPLICATION_HELPER_INPUT_LIMIT" {
		t.Fatal("byte bound")
	}
	if _, _, e := answerBank([]byte("schema_version: 1\nanswers: ["+strings.Repeat("null,", 201)+"]"), true); e == nil {
		t.Fatal("record bound")
	}
}
func TestSettingsStrictPrivacyAndLegacy(t *testing.T) {
	enabled, raw, e := configuration([]byte("# old\nschema_version: 1\nenabled_fields: [phone, email]\n"), true)
	if e != nil || !reflect.DeepEqual(enabled, []string{"email", "phone"}) || string(raw) != "schema_version: 2\nenabled_fields:\n- email\n- phone\n" {
		t.Fatal("legacy intent/revision")
	}
	for _, value := range []any{nil, "email", []any{true}, []any{"email", "email"}, []any{"password"}, []any{"us_unsupported"}} {
		if _, e := normalizeFields(value); e == nil {
			t.Fatal("unsupported permission")
		}
	}
	for _, version := range []string{"true", "1.0", "null", "3"} {
		if _, _, e := configuration([]byte("schema_version: "+version+"\nenabled_fields: []\n"), true); e == nil {
			t.Fatal("noninteger version")
		}
	}
	profile := cataloginput.Value{Kind: "mapping", Entries: []cataloginput.Entry{{Key: cataloginput.Value{Kind: "string", Text: "name"}, Value: cataloginput.Value{Kind: "string", Text: "\x1c Person\u0085"}}}}
	if canonicalValue(profile, fields[0]) != "Person" {
		t.Fatal("profile value")
	}
	if canonicalValue(profile, fields[4]) != nil {
		t.Fatal("inferred missing contact")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := response(cancelled, map[string]any{"private": "hidden"}); e == nil {
		t.Fatal("late response")
	}
	err := sourceUnavailable()
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if text := fmt.Sprintf(format, err); text != "Application helper operation could not be completed." {
			t.Fatal("unsafe error formatting")
		}
	}
}
func TestInvalidServiceAndRequestNoIO(t *testing.T) {
	for _, op := range []string{"", "../profile", "configuration.destroy"} {
		if out, e := New("invalid").Run(context.Background(), Request{Operation: op}); e == nil || e.Code != "APPLICATION_HELPER_OPERATION_INVALID" || out.Committed {
			t.Fatal("operation")
		}
	}
	if _, e := New("invalid").Run(context.Background(), Request{"configuration.load", map[string]any{"path": "private"}}); e == nil || e.Code != "APPLICATION_HELPER_REQUEST_INVALID" {
		t.Fatal("unknown input")
	}
	if _, e := (*Service)(nil).Run(context.Background(), Request{Operation: "projection"}); e == nil {
		t.Fatal("nil service")
	}
	if _, e := New("invalid").Run(nil, Request{Operation: "projection"}); e == nil || e.Code != "APPLICATION_HELPER_CANCELED" {
		t.Fatal("nil context")
	}
}

func TestResumeNumberSpelling(t *testing.T) {
	for _, c := range [][2]string{{"12", "12"}, {"12.0", "12.0"}, {"0.00001", "1e-05"}, {"10000000000000000.0", "1e+16"}, {"1e-7", "1e-07"}, {"-0.0", "-0.0"}} {
		got, e := resumeNumber(c[0])
		if e != nil || got != c[1] {
			t.Fatalf("numeric label %s -> %s", c[0], got)
		}
	}
}

func TestGeneratedIDsAreUUIDv4(t *testing.T) {
	s := New("not accessed")
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		id, e := s.id()
		if e != nil || !answerID.MatchString(id) || seen[id] {
			t.Fatal("generated identity")
		}
		seen[id] = true
		raw, e := hex.DecodeString(strings.TrimPrefix(id, "answer_"))
		if e != nil || raw[6]>>4 != 4 || raw[8]>>6 != 2 {
			t.Fatal("UUID variant/version")
		}
	}
}
