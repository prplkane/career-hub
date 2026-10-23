package materialintegrity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// FactClaim is a single typed editable field, not concatenated document prose.
// sourceOnly is set only by the typed projection for source-controlled metadata.
type FactClaim struct {
	Path, Text string
	ProofIDs   []string
	sourceOnly bool
}

func (FactClaim) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted material claim]") }

type FactProof struct{ ID, Text string }

func (FactProof) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted material proof]") }

// ManualFactCheck is an already explicitly confirmed, local review record.
// The owner must load it from the review store, never trust a client flag.
// Legacy free-form manual_checks strings cannot be converted to these records.
type ManualFactCheck struct {
	ID           string `json:"id"`
	DraftPath    string `json:"draft_path"`
	ClaimSHA256  string `json:"claim_sha256"`
	TokenSHA256  string `json:"token_sha256"`
	ReviewSHA256 string `json:"review_sha256"`
	Confirmed    bool   `json:"confirmed"`
}

type FactResult struct {
	Validation                Validation `json:"validation"`
	ProofIDs                  []string   `json:"proof_ids"`
	ManualCheckIDs            []string   `json:"manual_check_ids"`
	ObservedDemandIDs         []string   `json:"observed_demand_ids"`
	CitedDemandIDs            []string   `json:"cited_demand_ids"`
	SourceControlledDemandIDs []string   `json:"source_controlled_demand_ids"`
}

// ReviewIdentity binds overrides to the job, exact draft and all current source
// revisions/policy. Used receipt IDs are excluded to avoid a circular identity.
func ReviewIdentity(s Sources) (string, *Error) {
	if !validSources(s) {
		return "", failure("MATERIAL_FACT_SOURCES_INVALID")
	}
	s.ProofIDs, s.ManualCheckIDs = []string{}, []string{}
	raw, _ := json.Marshal(s)
	return digest(append([]byte("material-review-v1\x00"), raw...)), nil
}

func factText(text string) bool {
	return len(text) <= MaxTextBytes && utf8.ValidString(text) && !strings.ContainsRune(text, 0)
}
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func containsNamed(proof, key string) bool {
	_, term, _ := strings.Cut(key, "\x00")
	for start := 0; start <= len(proof)-len(term); {
		relative := strings.Index(proof[start:], term)
		if relative < 0 {
			return false
		}
		at, end := start+relative, start+relative+len(term)
		left, right := true, true
		first, _ := utf8.DecodeRuneInString(term)
		last, _ := utf8.DecodeLastRuneInString(term)
		if at > 0 && textcompat.IsWord(first) {
			r, _ := utf8.DecodeLastRuneInString(proof[:at])
			left = !textcompat.IsWord(r)
		}
		if end < len(proof) && textcompat.IsWord(last) {
			r, _ := utf8.DecodeRuneInString(proof[end:])
			right = !textcompat.IsWord(r)
		}
		if left && right {
			return true
		}
		_, size := utf8.DecodeRuneInString(proof[at:])
		start = at + size
	}
	return false
}

// CheckFacts is the pure claim-to-cited-proof gate, NOT the export owner. Callers
// must derive the complete typed claim list and validate source/proof ownership
// before this call. No JD or unrelated Vault proof may be inserted as evidence.
func CheckFacts(ctx context.Context, p *FactPolicy, sources Sources, claims []FactClaim, proofs []FactProof, checks []ManualFactCheck) (*FactResult, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("MATERIAL_INTEGRITY_CANCELED")
	}
	if p == nil || p.matcher == nil || p.digest != sources.PolicySHA256 {
		return nil, failure("MATERIAL_FACT_POLICY_STALE")
	}
	review, e := ReviewIdentity(sources)
	if e != nil {
		return nil, e
	}
	if len(claims) > 1000 || len(proofs) > 10000 || len(checks) > 1000 {
		return nil, failure("MATERIAL_FACT_INPUT_LIMIT")
	}
	total, references := 0, 0
	claimByPath := map[string]FactClaim{}
	claimHashes := map[string]string{}
	usedProofs := map[string]bool{}
	for _, claim := range claims {
		total += len(claim.Text)
		if total > MaxFactBytes || !factText(claim.Text) || !draftPath.MatchString(claim.Path) || claim.Path == "artifact" {
			return nil, failure("MATERIAL_FACT_CLAIM_INVALID")
		}
		if _, duplicate := claimByPath[claim.Path]; duplicate {
			return nil, failure("MATERIAL_FACT_CLAIM_INVALID")
		}
		claimByPath[claim.Path] = claim
		claimHashes[claim.Path] = digest([]byte(claim.Text))
		seen := map[string]bool{}
		for _, id := range claim.ProofIDs {
			references++
			if !identifier(id) || seen[id] || references > 10000 {
				return nil, failure("MATERIAL_FACT_REFERENCE_INVALID")
			}
			seen[id], usedProofs[id] = true, true
		}
	}
	proofByID := map[string]string{}
	for _, proof := range proofs {
		total += len(proof.Text)
		if total > MaxFactBytes || !identifier(proof.ID) || !factText(proof.Text) {
			return nil, failure("MATERIAL_FACT_PROOF_INVALID")
		}
		if _, duplicate := proofByID[proof.ID]; duplicate {
			return nil, failure("MATERIAL_FACT_PROOF_INVALID")
		}
		proofByID[proof.ID] = proof.Text
	}
	for id := range usedProofs {
		if _, ok := proofByID[id]; !ok {
			return nil, failure("MATERIAL_FACT_PROOF_MISSING")
		}
	}
	if !equalStrings(sortedKeys(usedProofs), sources.ProofIDs) {
		return nil, failure("MATERIAL_FACT_REFERENCES_STALE")
	}
	manual := map[string]ManualFactCheck{}
	checkIDs := map[string]bool{}
	for _, check := range checks {
		_, exists := claimByPath[check.DraftPath]
		if !exists || !identifier(check.ID) || !hash(check.ClaimSHA256, "") || !hash(check.TokenSHA256, "") || !hash(check.ReviewSHA256, "") || checkIDs[check.ID] {
			return nil, failure("MATERIAL_MANUAL_CHECK_INVALID")
		}
		checkIDs[check.ID] = true
		if !check.Confirmed || check.ReviewSHA256 != review || check.ClaimSHA256 != claimHashes[check.DraftPath] {
			return nil, failure("MATERIAL_MANUAL_CHECK_STALE")
		}
		key := check.DraftPath + "\x00" + check.TokenSHA256
		if _, duplicate := manual[key]; duplicate {
			return nil, failure("MATERIAL_MANUAL_CHECK_AMBIGUOUS")
		}
		manual[key] = check
	}
	tokens, e := p.tokens(ctx, claims)
	if e != nil {
		return nil, e
	}
	result := &FactResult{Validation: Validation{Status: "pass", Findings: []Finding{}}, ProofIDs: sortedKeys(usedProofs)}
	usedChecks, observed, cited, selectedSkills := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	type proofIndex struct {
		normalized string
		tokens     map[string]bool
	}
	indexed := map[string]proofIndex{}
	work := 0
	for i, claim := range claims {
		for _, token := range tokens[i] {
			if ctx.Err() != nil {
				return nil, failure("MATERIAL_INTEGRITY_CANCELED")
			}
			if claim.sourceOnly && token.kind != "placeholder" {
				// A selected Vault keyword is an exact source-controlled field,
				// unlike incidental vocabulary in contact links or employers.
				if token.demand != "" && strings.HasPrefix(claim.Path, "skills[") && strings.Contains(claim.Path, ".keywords[") {
					observed[token.demand], selectedSkills[token.demand] = true, true
				}
				continue
			}
			if token.demand != "" {
				observed[token.demand] = true
			}
			supported := false
			if token.kind != "placeholder" {
				for _, id := range claim.ProofIDs {
					index, ok := indexed[id]
					if !ok {
						parsed, e := staticTokens(proofByID[id])
						if e != nil {
							return nil, e
						}
						index = proofIndex{textcompat.Normalize(proofByID[id]), map[string]bool{}}
						for _, t := range compactTokens(parsed) {
							index.tokens[t.key] = true
						}
						indexed[id] = index
					}
					work += len(index.normalized) + len(token.key)
					if work > MaxFactWork {
						return nil, failure("MATERIAL_FACT_WORK_LIMIT")
					}
					if token.kind == "number" {
						supported = index.tokens[token.key]
					} else {
						supported = index.tokens[token.key] || containsNamed(index.normalized, token.key)
					}
					if supported {
						break
					}
				}
			}
			if supported {
				if token.demand != "" {
					cited[token.demand] = true
				}
				continue
			}
			finding := Finding{Severity: "block", Code: "UNSUPPORTED_FACT_TOKEN", DraftPath: claim.Path, DraftStart: token.start, DraftEnd: token.end, ArtifactStart: -1, ArtifactEnd: -1, TokenSHA256: token.sha256()}
			if token.kind == "placeholder" {
				finding.Code = "UNRESOLVED_PLACEHOLDER"
			} else if check, ok := manual[claim.Path+"\x00"+token.sha256()]; ok {
				finding.Severity, finding.Code, finding.ManualCheckID = "pass", "MANUAL_FACT_CHECK", check.ID
				usedChecks[check.ID] = true
			}
			if len(result.Validation.Findings) == 1000 {
				return nil, failure("MATERIAL_FACT_FINDING_LIMIT")
			}
			result.Validation.Findings = append(result.Validation.Findings, finding)
			if finding.Severity == "block" {
				result.Validation.Status = "block"
			}
		}
	}
	result.ManualCheckIDs, result.ObservedDemandIDs, result.CitedDemandIDs = sortedKeys(usedChecks), sortedKeys(observed), sortedKeys(cited)
	result.SourceControlledDemandIDs = sortedKeys(selectedSkills)
	if !validValidation(result.Validation, result.ManualCheckIDs) {
		return nil, failure("MATERIAL_FACT_RESULT_INVALID")
	}
	return result, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
