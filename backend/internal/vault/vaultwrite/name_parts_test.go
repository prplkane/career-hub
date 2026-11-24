package vaultwrite

import (
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/vault/vaultread"
)

func text(value string) cataloginput.Value {
	return cataloginput.Value{Kind: "string", Text: value}
}

func profileWith(fields map[string]string) cataloginput.Value {
	record := cataloginput.Value{Kind: "mapping"}
	for key, value := range fields {
		set(&record, key, text(value))
	}
	return record
}

func TestDeriveNamePartsFillsBothPartsOnlyWhileBothAreEmpty(t *testing.T) {
	record := profileWith(map[string]string{"name": "Ada Lovelace"})
	deriveNameParts(&record)
	if got := intakeField(record, "given_name").Text; got != "Ada" {
		t.Fatalf("given_name = %q, want Ada", got)
	}
	if got := intakeField(record, "family_name").Text; got != "Lovelace" {
		t.Fatalf("family_name = %q, want Lovelace", got)
	}
	if marker := intakeField(record, NamePartsMarker); marker.Kind != "bool" || !marker.Boolean {
		t.Fatalf("%s = %+v, want true", NamePartsMarker, marker)
	}
}

func TestDeriveNamePartsNeverReplacesATypedPart(t *testing.T) {
	for _, typed := range []string{"given_name", "family_name"} {
		record := profileWith(map[string]string{"name": "Ada Lovelace", typed: "Augusta"})
		deriveNameParts(&record)
		if got := intakeField(record, typed).Text; got != "Augusta" {
			t.Fatalf("%s = %q, want the typed Augusta", typed, got)
		}
		if _, present := get(record, NamePartsMarker); present {
			t.Fatalf("a typed %s must not be marked as derived", typed)
		}
		other := map[string]string{"given_name": "family_name", "family_name": "given_name"}[typed]
		if _, present := get(record, other); present {
			t.Fatalf("%s was filled although %s was typed", other, typed)
		}
	}
}

func TestDeriveNamePartsLeavesAnUnsplittableNameAlone(t *testing.T) {
	record := profileWith(map[string]string{"name": "   "})
	deriveNameParts(&record)
	for _, key := range []string{"given_name", "family_name", NamePartsMarker} {
		if _, present := get(record, key); present {
			t.Fatalf("%s was set for a blank name", key)
		}
	}
}

func TestProfileSaveClearsTheNamePartsMarker(t *testing.T) {
	original := profileWith(map[string]string{"name": "Ada Lovelace", "given_name": "Ada", "family_name": "Lovelace"})
	set(&original, NamePartsMarker, cataloginput.Value{Kind: "bool", Boolean: true})
	submitted := profileWith(map[string]string{"name": "Ada Lovelace", "given_name": "Ada", "family_name": "King"})
	set(&submitted, "contact", cataloginput.Value{Kind: "mapping"})
	merged, problem := merge(vaultread.ProfileTarget, original, submitted)
	if problem != nil {
		t.Fatalf("merge: %v", problem)
	}
	if _, present := get(merged, NamePartsMarker); present {
		t.Fatal("saving the Profile must clear the derived-name marker")
	}
	if got := intakeField(merged, "family_name").Text; got != "King" {
		t.Fatalf("family_name = %q, want the saved King", got)
	}
}
