package catalogtransaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
)

func evidenceFixture(project, missing bool) (*memory, intent) {
	m, catalogs := fixture(missing)
	path := vaultPaths[3]
	if project {
		path = vaultPaths[4]
	}
	record := Change{Path: path, RecordID: "record", BeforePresent: true, Before: []byte("record before"), After: []byte("record after")}
	m.files[path] = bytes.Clone(record.Before)
	data := intent{Version: 2, HomeID: testHomeID, Changes: append([]Change{record}, catalogs...)}
	for _, companion := range vaultPaths {
		if companion == path {
			continue
		}
		raw := []byte("unchanged required companion: " + companion)
		m.files[companion] = raw
		data.Guards = append(data.Guards, Guard{companion, fmt.Sprintf("sha256:%x", sha256.Sum256(raw))})
	}
	return m, data
}
func assertEvidenceAfter(t *testing.T, m *memory, data intent) {
	t.Helper()
	for _, change := range data.Changes {
		_, after, e := observe(m, change)
		if e != nil || !after {
			t.Fatal("did not reach complete after-state")
		}
	}
	if _, present := m.files[EvidenceJournalPath]; present {
		t.Fatal("completed journal retained")
	}
	if e := checkGuards(context.Background(), m, data.Guards); e != nil {
		t.Fatal("companion changed")
	}
	if string(m.files["documents/private.bin"]) != "unchanged" {
		t.Fatal("unrelated data touched")
	}
}
func TestEvidenceEveryFailureAndRestartBoundary(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			baseline, data := evidenceFixture(project, missing)
			report, e := beginIntent(context.Background(), baseline, data, accept)
			if e != nil || !report.Completed || len(report.AppliedFiles) != 3 {
				t.Fatal("baseline", e)
			}
			points := baseline.steps
			for point := 1; point <= points; point++ {
				m, data := evidenceFixture(project, missing)
				m.failAt = point
				report, e := beginIntent(context.Background(), m, data, accept)
				if e == nil {
					t.Fatalf("fault %d did not fail", point)
				}
				_, pending := m.files[EvidenceJournalPath]
				if pending && !report.IntentRecorded || len(report.AppliedFiles) > 0 && !report.IntentRecorded {
					t.Fatal("hidden publication facts")
				}
				m.failAt = 0
				if pending {
					recovered, e := resumePolicy(context.Background(), m, testHomeID, 2, accept)
					if e != nil || !recovered.Completed {
						t.Fatalf("recovery at %d: %v", point, e)
					}
					assertEvidenceAfter(t, m, data)
				} else if report.JournalRemoved {
					assertEvidenceAfter(t, m, data)
				} else {
					for _, change := range data.Changes {
						if _, present := m.files[change.Path]; present != change.BeforePresent || !bytes.Equal(m.files[change.Path], change.Before) {
							t.Fatal("unrecorded mutation")
						}
					}
				}
				again, e := resumePolicy(context.Background(), m, testHomeID, 2, accept)
				if e != nil || again.IntentRecorded || len(again.AppliedFiles) != 0 {
					t.Fatal("non-idempotent recovery")
				}
			}
			t.Logf("verified %d failure/restart boundaries (project=%v missing=%v)", points, project, missing)
		}
	}
}

func TestEvidenceGuardsBlockDriftAtEveryPublicationStage(t *testing.T) {
	for _, stage := range []string{"before", "validation", "intent", "record", "groups", "skills", "recovery"} {
		for _, missing := range []bool{false, true} {
			m, data := evidenceFixture(false, false)
			guard := data.Guards[0]
			alter := func() {
				if missing {
					delete(m.files, guard.Path)
				} else {
					m.files[guard.Path] = []byte("external")
				}
			}
			validate := accept
			switch stage {
			case "before":
				alter()
			case "validation":
				validate = func(context.Context, []Change) error { alter(); return nil }
			case "recovery":
				raw, _ := encodeIntent(data)
				m.files[EvidenceJournalPath] = raw
				alter()
			default:
				at := map[string]string{"intent": EvidenceJournalPath, "record": data.Changes[0].Path, "groups": data.Changes[1].Path, "skills": data.Changes[2].Path}[stage]
				m.mutate = func(path string) {
					if path == at {
						alter()
					}
				}
			}
			var report Report
			var e error
			if stage == "recovery" {
				report, e = resumePolicy(context.Background(), m, testHomeID, 2, validate)
			} else {
				report, e = beginIntent(context.Background(), m, data, validate)
			}
			if !errors.Is(e, ErrConflict) || report.Completed || report.JournalRemoved {
				t.Fatal("guard drift accepted", stage, e)
			}
			writes := m.writeCount
			m.mutate = nil
			if report.IntentRecorded {
				if _, e := resumePolicy(context.Background(), m, testHomeID, 2, accept); !errors.Is(e, ErrConflict) || m.writeCount != writes {
					t.Fatal("recovery overwrote changed companion")
				}
			}
			if missing {
				if _, present := m.files[guard.Path]; present {
					t.Fatal("missing companion recreated")
				}
			} else if string(m.files[guard.Path]) != "external" {
				t.Fatal("companion overwritten")
			}
		}
	}
}

func resign(data intent) []byte {
	payload, _ := json.Marshal(data)
	hash := sha256.Sum256(payload)
	raw, _ := json.Marshal(envelope{payload, hex.EncodeToString(hash[:])})
	return raw
}
func TestEvidenceClosedPolicyAndLegacyJournalBytes(t *testing.T) {
	_, legacy := fixture(false)
	oldPayload, _ := json.Marshal(struct {
		Version int      `json:"version"`
		HomeID  string   `json:"data_home_id"`
		Changes []Change `json:"changes"`
	}{1, testHomeID, legacy})
	hash := sha256.Sum256(oldPayload)
	oldJournal, _ := json.Marshal(envelope{oldPayload, hex.EncodeToString(hash[:])})
	current, e := encode(testHomeID, legacy)
	if e != nil || !bytes.Equal(current, oldJournal) {
		t.Fatal("legacy journal bytes changed")
	}
	for _, alter := range []func(*intent){
		func(d *intent) { d.Version = 3 }, func(d *intent) { d.Version = 1 }, func(d *intent) { d.HomeID = "invalid" },
		func(d *intent) { d.Changes = d.Changes[:2] }, func(d *intent) { d.Changes[0].Path = "documents/private.bin" },
		func(d *intent) { d.Changes[0].Path = "career_vault/profile.yaml" }, func(d *intent) { d.Changes[0].BeforePresent = false },
		func(d *intent) { d.Changes[0].CheckOnly = true }, func(d *intent) { d.Changes[1], d.Changes[2] = d.Changes[2], d.Changes[1] },
		func(d *intent) { d.Changes[0].After = make([]byte, cataloginput.YAMLMaxInputBytes+1) },
		func(d *intent) { d.Changes[0].RecordID = "" },
		func(d *intent) { d.Changes[1].RecordID = "wrong-member" },
		func(d *intent) { d.Changes[1].CheckOnly = true },
		func(d *intent) { d.Guards = d.Guards[:6] }, func(d *intent) { d.Guards[0] = d.Guards[1] },
		func(d *intent) { d.Guards[0].Path = "../outside" }, func(d *intent) { d.Guards[0].Revision = "bad" },
		func(d *intent) {
			d.Guards[0].Revision = "sha256:FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"
		},
	} {
		_, data := evidenceFixture(false, false)
		alter(&data)
		if _, e := encodeIntent(data); !errors.Is(e, ErrInput) {
			t.Fatal("invalid policy admitted")
		}
		if _, e := decodeIntent(resign(data), testHomeID, 2); !errors.Is(e, ErrJournal) {
			t.Fatal("signed invalid journal admitted")
		}
	}
	_, data := evidenceFixture(false, false)
	// encoding/json repairs invalid Go strings to U+FFFD. Test the encoder
	// directly; re-signing that repaired string would be a different, valid ID.
	data.Changes[0].RecordID = "\xff"
	if _, e := encodeIntent(data); !errors.Is(e, ErrInput) {
		t.Fatal("invalid Unicode record identity admitted")
	}
	data.Changes[0].RecordID = "record"
	raw, _ := encodeIntent(data)
	if _, e := decode(raw, testHomeID); !errors.Is(e, ErrJournal) {
		t.Fatal("evidence through registry recovery")
	}
	if _, e := decodeIntent(oldJournal, testHomeID, 2); !errors.Is(e, ErrJournal) {
		t.Fatal("registry through evidence recovery")
	}
}

func TestEvidenceCheckOnlyCatalogsAndEntryGuards(t *testing.T) {
	for _, missing := range []bool{false, true} {
		m, data := evidenceFixture(false, missing)
		for i := 1; i < 3; i++ {
			data.Changes[i].CheckOnly = true
			data.Changes[i].After = bytes.Clone(data.Changes[i].Before)
		}
		report, e := beginIntent(context.Background(), m, data, accept)
		if e != nil || !report.Completed || len(report.AppliedFiles) != 1 {
			t.Fatal("unchanged catalogs written")
		}
		assertEvidenceAfter(t, m, data)
		m, data = evidenceFixture(false, missing)
		data.Changes[2].CheckOnly = true
		data.Changes[2].After = bytes.Clone(data.Changes[2].Before)
		raw, _ := encodeIntent(data)
		m.files[EvidenceJournalPath] = raw
		m.files[catalogstore.CustomSkillsPath] = []byte("external catalog")
		if _, e := resumePolicy(context.Background(), m, testHomeID, 2, accept); !errors.Is(e, ErrConflict) || m.writeCount != 0 {
			t.Fatal("catalog guard drift accepted")
		}
	}
	_, data := evidenceFixture(false, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []context.Context{nil, ctx, context.Background()} {
		if _, e := BeginEvidenceOwned(c, nil, testHomeID, data.Changes, data.Guards, accept); e == nil {
			t.Fatal("unowned begin")
		}
		if _, e := RecoverEvidenceOwned(c, nil, testHomeID, accept); e == nil {
			t.Fatal("unowned recovery")
		}
	}
	if _, e := BeginEvidenceOwned(context.Background(), nil, testHomeID, nil, nil, accept); e == nil {
		t.Fatal("invalid intent")
	}
	if _, e := RecoverEvidenceOwned(context.Background(), nil, testHomeID, nil); e == nil {
		t.Fatal("nil validator")
	}
}
