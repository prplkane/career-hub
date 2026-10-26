package applicationanalysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestWholeResultsOrRedactedRefusal(t *testing.T) {
	for _, source := range []string{string([]byte{0xff}), "\xed\xa0\x80", strings.Repeat("x", MaxSourceBytes+1)} {
		got, err := Analyze(source)
		if err == nil || !reflect.DeepEqual(got, Result{}) {
			t.Fatal("partial/malformed analysis accepted")
		}
		if !errors.Is(err, ErrInput) && !errors.Is(err, ErrLimit) {
			t.Fatal("untyped input refusal")
		}
		instructions, err := ExtractInstructions(source)
		if err == nil || !reflect.DeepEqual(instructions, InstructionResult{}) {
			t.Fatal("partial instruction result")
		}
		contacts, err := ExtractContacts(source)
		if err == nil || !reflect.DeepEqual(contacts, ContactResult{}) {
			t.Fatal("partial contact result")
		}
	}
	var addresses []string
	for i := 0; i <= maxChannelsPerLine; i++ {
		addresses = append(addresses, fmt.Sprintf("person%d@example.com", i))
	}
	source := "How to apply\nEmail careers@example.com\nContact Jane Doe — " + strings.Join(addresses, " ")
	got, err := Analyze(source)
	if !errors.Is(err, ErrLimit) || !reflect.DeepEqual(got, Result{}) {
		t.Fatal("channel budget did not fail the whole operation")
	}
	if strings.Contains(err.Error(), "Jane") || strings.Contains(err.Error(), "example") {
		t.Fatal("source leaked into error")
	}
	contact, err := ExtractContacts(source)
	if !errors.Is(err, ErrLimit) || !reflect.DeepEqual(contact, ContactResult{}) {
		t.Fatal("partial contact return")
	}
	// The instruction-only operation does not depend on contact-candidate limits.
	if _, err := ExtractInstructions(source); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentOperationsAndCallerOwnedResults(t *testing.T) {
	source := "How to apply\nEmail your resume to careers@example.com.\nContact Jane Doe — jane@example.com"
	full, err := Analyze(source)
	if err != nil {
		t.Fatal(err)
	}
	instructions, err := ExtractInstructions(source)
	if err != nil {
		t.Fatal(err)
	}
	contacts, err := ExtractContacts(source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full.Instructions, instructions) || !reflect.DeepEqual(full.Contacts, contacts) {
		t.Fatal("operation composition differs")
	}
	want, _ := json.Marshal(full)
	full.Contacts.Candidates[0].Name = "modified"
	full.Contacts.Candidates[0].Channels[0].Value = "modified"
	full.Instructions.Items[0].SourceText = "modified"
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			got, err := Analyze(source)
			encoded, _ := json.Marshal(got)
			if err != nil || string(encoded) != string(want) {
				t.Error("analysis shares mutable caller state")
			}
		}()
	}
	wait.Wait()
}

func TestMarkerCollisionDoesNotReserveSourceCharacters(t *testing.T) {
	source := "How to apply\n\ue000\ue001\ue002\ue003 Email careers@example.com"
	engine := newMatcher(source)
	for _, marker := range engine.marks {
		if strings.ContainsRune(source, marker) {
			t.Fatal("marker collision")
		}
	}
	text := engine.text("Email careers@example.com")
	match, ok := text.match("i._EMAIL_ADDRESS", "")
	if !ok || match.value() != "careers@example.com" || match.group("absent") != "" {
		t.Fatal("collision fallback changes capture")
	}
	if _, ok := text.match("c._PERSON_NAME", "full"); ok {
		t.Fatal("fullmatch accepted partial name")
	}
}

func TestContactIDUsesSourceAndOrderedExactFields(t *testing.T) {
	base := ContactCandidate{ContentSHA256: digest("raw source"), ContextKind: "recruiter", Confidence: "explicit",
		Name: "Jane Doe", Role: "Recruiter", Company: "Example", SourceText: " Exact evidence ",
		Channels: []Channel{{"email", "jane@example.com"}, {"phone", "555-010-0199"}}}
	id := candidateID(base)
	base.SourceText = "EXACT\tEVIDENCE"
	if candidateID(base) != id {
		t.Fatal("collapsed folded evidence must retain identity")
	}
	base.Channels[0], base.Channels[1] = base.Channels[1], base.Channels[0]
	if candidateID(base) == id {
		t.Fatal("channel order lost from identity")
	}
	base.ContentSHA256 = digest("other source")
	if candidateID(base) == id {
		t.Fatal("whole source lost from candidate identity")
	}
}
