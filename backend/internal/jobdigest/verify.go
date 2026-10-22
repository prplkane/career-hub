package jobdigest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/textcompat"
)

var ErrSourceMismatch = errors.New("deterministic JD digest does not match retained source and inputs")

// CheckReferences checks the producer's exact-source/registry postconditions
// without running extraction again. It is not a completeness/replay proof like
// Verify: use it only in a pipeline that separately checks cache identity,
// checksum, full schema and current context. A checksum does not authenticate a
// local editor, and valid retained references cannot prove extraction completeness.
func CheckReferences(ctx context.Context, input Input, candidate *Result) error {
	if ctx == nil || candidate == nil {
		return ErrInput
	}
	if err := outputBound(ctx, reflect.ValueOf(candidate)); err != nil {
		return err
	}
	a, err := prepareAnalysis(ctx, input)
	if err != nil {
		return err
	}
	a.result = candidate
	return a.verifyReferences()
}

// Verify independently reruns the complete analyzer against the supplied exact
// source, effective catalog and eligibility projection. Every field, quote,
// coordinate, identity, count and ordered result must reproduce exactly. This
// is deliberately not a cheap hash-only cache check. Callers still own schema,
// analyzer-version, captured-JD completeness and filesystem admission checks.
// It proves deterministic reproduction, not semantic/legal truth or candidate
// skill possession. It never overwrites a stale artifact to make it pass.
func Verify(ctx context.Context, input Input, candidate *Result) error {
	if ctx == nil || candidate == nil {
		return ErrInput
	}
	if err := outputBound(ctx, reflect.ValueOf(candidate)); err != nil {
		return err
	}
	fresh, err := Analyze(ctx, input)
	if err != nil {
		return err
	}
	equal := reflect.DeepEqual(fresh, candidate)
	if err := ctx.Err(); err != nil {
		return err
	}
	if !equal {
		return ErrSourceMismatch
	}
	return nil
}

// The producer also checks its own exact-reference postconditions without a
// second extraction pass. Cleaned display lines are never source coordinates.
func (a *analysis) verifyReferences() error {
	lines := map[int]line{}
	for _, line := range a.lines {
		lines[line.number] = line
	}
	proof := func(values []Evidence) bool {
		for _, item := range values {
			if !a.check() {
				return false
			}
			original, ok := lines[item.Line]
			if !ok || item.Rule == "" {
				return false
			}
			text := original.text
			if item.Rule == "compensation_range" || item.Rule == "sole_compensation_location" {
				text = original.raw
			}
			if item.Text != prefix(text, 500) {
				return false
			}
		}
		return true
	}
	for _, field := range a.result.Fields {
		if !proof(field.Evidence) {
			return a.referenceFailure()
		}
	}
	for _, item := range a.result.Provenance.Technologies {
		if !proof(item.Evidence) {
			return a.referenceFailure()
		}
	}
	for _, item := range a.result.TechnologyGroups {
		if !proof(item.Evidence) {
			return a.referenceFailure()
		}
	}
	for _, values := range [][]Evidence{a.result.Provenance.Experience, a.result.Provenance.Education, a.result.Provenance.Responsibilities} {
		if !proof(values) {
			return a.referenceFailure()
		}
	}
	for _, item := range a.result.Compensation.Ranges {
		if !proof(item.Evidence) {
			return a.referenceFailure()
		}
	}
	for _, gate := range a.result.Gates {
		if !proof(gate.Evidence) {
			return a.referenceFailure()
		}
	}
	for _, item := range a.result.Classified {
		if !proof(item.Evidence) {
			return a.referenceFailure()
		}
	}
	for _, item := range a.result.Coverage.ReviewItems {
		if !a.check() {
			return a.err
		}
		original, ok := lines[item.Line]
		if !ok || item.Text != prefix(original.clean, 500) {
			return ErrSourceMismatch
		}
	}
	skills := map[string]catalog.Term{}
	for _, term := range a.input.Registry.Skills() {
		skills[term.ID] = term
	}
	points := map[int][]rune{}
	for _, item := range a.result.Occurrences {
		if !a.check() {
			return a.err
		}
		original, ok := lines[item.Line]
		if !ok {
			return ErrSourceMismatch
		}
		chars, present := points[item.Line]
		if !present {
			chars = []rune(original.text)
			points[item.Line] = chars
		}
		if item.Start < 0 || item.End <= item.Start || item.End > len(chars) || string(chars[item.Start:item.End]) != item.SourceText {
			return ErrSourceMismatch
		}
		term, ok := skills[item.SkillID]
		if !ok || term.Archived || term.Name != item.CanonicalName || catalog.EffectiveGroupID(term) != item.PrimaryGroupID || a.input.Registry.SkillOrigin(term.ID) != item.Origin {
			return ErrSourceMismatch
		}
	}
	hash := sha256.Sum256([]byte(a.input.Source))
	contentHash := hex.EncodeToString(hash[:])
	if a.result.Contacts.ContentSHA256 != contentHash || a.result.Instructions.SourceFingerprint != a.result.ContentFingerprint || a.result.Contacts.SourceFingerprint != a.result.ContentFingerprint {
		return ErrSourceMismatch
	}
	sourceLine := func(number, end int, quote string, truncated bool) bool {
		original, ok := lines[number]
		if !ok || number != end {
			return false
		}
		expected := prefix(original.text, 1200)
		return quote == expected && truncated == (len(expected) < len(original.text))
	}
	for _, item := range a.result.Instructions.Items {
		if !a.check() {
			return a.err
		}
		if !sourceLine(item.LineStart, item.LineEnd, item.SourceText, item.EvidenceTruncated) {
			return ErrSourceMismatch
		}
	}
	for _, item := range a.result.Contacts.Candidates {
		if !a.check() {
			return a.err
		}
		if !sourceLine(item.LineStart, item.LineEnd, item.SourceText, item.EvidenceTruncated) || item.ContentSHA256 != contentHash {
			return ErrSourceMismatch
		}
	}
	// Trim semantics are explicit: occurrence offsets refer to each original
	// Python-stripped line, not a repaired line, raw file byte or UTF-16 offset.
	for _, original := range lines {
		if textcompat.Strip(original.raw) != original.text {
			return ErrSourceMismatch
		}
	}
	return a.ctx.Err()
}
func (a *analysis) referenceFailure() error {
	if a.err != nil {
		return a.err
	}
	return ErrSourceMismatch
}
