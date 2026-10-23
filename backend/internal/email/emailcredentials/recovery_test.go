package emailcredentials

import (
	"errors"
	"strings"
	"testing"
)

func observed(found bool) *bool { return &found }

func TestRecoveryTruthTable(t *testing.T) {
	for _, test := range []struct {
		name     string
		intent   Intent
		after    bool
		presence CredentialObservation
		want     RecoveryPlan
		wantErr  error
	}{
		{"configure-before-no-new", sampleIntent(InitialConfigure), false, CredentialObservation{NewFound: observed(false)}, RecoveryPlan{DatabaseSide: BeforeCommit}, nil},
		{"configure-before-orphan", sampleIntent(InitialConfigure), false, CredentialObservation{NewFound: observed(true)}, RecoveryPlan{DatabaseSide: BeforeCommit, Cleanup: CleanupNew}, nil},
		{"configure-after-new", sampleIntent(InitialConfigure), true, CredentialObservation{NewFound: observed(true)}, RecoveryPlan{DatabaseSide: AfterCommit}, nil},
		{"configure-after-missing", sampleIntent(InitialConfigure), true, CredentialObservation{NewFound: observed(false)}, RecoveryPlan{}, ErrRecoveryCredentialMissing},
		{"rotate-before-orphan", sampleIntent(Rotate), false, CredentialObservation{OldFound: observed(true), NewFound: observed(true)}, RecoveryPlan{DatabaseSide: BeforeCommit, Cleanup: CleanupNew}, nil},
		{"rotate-before-disabled-old-missing", sampleIntent(Rotate), false, CredentialObservation{OldFound: observed(false), NewFound: observed(true)}, RecoveryPlan{DatabaseSide: BeforeCommit, Cleanup: CleanupNew}, nil},
		{"rotate-after-old-present", sampleIntent(Rotate), true, CredentialObservation{OldFound: observed(true), NewFound: observed(true)}, RecoveryPlan{DatabaseSide: AfterCommit, Cleanup: CleanupOld, LegacyPreflightRequired: true}, nil},
		{"rotate-after-old-retired", sampleIntent(Rotate), true, CredentialObservation{OldFound: observed(false), NewFound: observed(true)}, RecoveryPlan{DatabaseSide: AfterCommit}, nil},
		{"rotate-after-new-missing", sampleIntent(Rotate), true, CredentialObservation{OldFound: observed(true), NewFound: observed(false)}, RecoveryPlan{}, ErrRecoveryCredentialMissing},
		{"promote-before-no-new", sampleIntent(PromoteLegacy), false, CredentialObservation{NewFound: observed(false)}, RecoveryPlan{DatabaseSide: BeforeCommit}, nil},
		{"promote-before-orphan", sampleIntent(PromoteLegacy), false, CredentialObservation{NewFound: observed(true)}, RecoveryPlan{DatabaseSide: BeforeCommit, Cleanup: CleanupNew}, nil},
		{"promote-after-new", sampleIntent(PromoteLegacy), true, CredentialObservation{NewFound: observed(true)}, RecoveryPlan{DatabaseSide: AfterCommit}, nil},
		{"promote-after-missing", sampleIntent(PromoteLegacy), true, CredentialObservation{NewFound: observed(false)}, RecoveryPlan{}, ErrRecoveryCredentialMissing},
		{"disconnect-before", sampleIntent(Disconnect), false, CredentialObservation{OldFound: observed(true)}, RecoveryPlan{DatabaseSide: BeforeCommit}, nil},
		{"disconnect-before-old-missing", sampleIntent(Disconnect), false, CredentialObservation{OldFound: observed(false)}, RecoveryPlan{}, ErrRecoveryCredentialMissing},
		{"disconnect-after-old-present", sampleIntent(Disconnect), true, CredentialObservation{OldFound: observed(true)}, RecoveryPlan{DatabaseSide: AfterCommit, Cleanup: CleanupOld, LegacyPreflightRequired: true}, nil},
		{"disconnect-after-old-retired", sampleIntent(Disconnect), true, CredentialObservation{OldFound: observed(false)}, RecoveryPlan{DatabaseSide: AfterCommit}, nil},
		{"forget-before-disabled-old-missing", sampleIntent(Forget), false, CredentialObservation{OldFound: observed(false)}, RecoveryPlan{DatabaseSide: BeforeCommit}, nil},
		{"forget-after-old-present", sampleIntent(Forget), true, CredentialObservation{OldFound: observed(true)}, RecoveryPlan{DatabaseSide: AfterCommit, Cleanup: CleanupOld, LegacyPreflightRequired: true}, nil},
		{"forget-after-old-retired", sampleIntent(Forget), true, CredentialObservation{OldFound: observed(false)}, RecoveryPlan{DatabaseSide: AfterCommit}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := test.intent.Before
			if test.after {
				current = test.intent.After
			}
			plan, err := PlanRecovery(test.intent, current, test.presence)
			if plan != test.want || !errors.Is(err, test.wantErr) || (test.wantErr == nil && err != nil) {
				t.Fatalf("plan=%+v err=%v; want=%+v %v", plan, err, test.want, test.wantErr)
			}
		})
	}
}

func TestRecoveryV2CleanupNeedsNoLegacyPreflight(t *testing.T) {
	v2Old := strings.Replace(intentV2, strings.Repeat("ab", 16), strings.Repeat("cd", 16), 1)
	for _, operation := range []string{Rotate, Disconnect, Forget} {
		intent := sampleIntent(operation)
		intent.Before.SecretName = v2Old
		if operation == Disconnect {
			intent.After.SecretName = v2Old
		}
		observation := CredentialObservation{OldFound: observed(true)}
		if operation == Rotate {
			observation.NewFound = observed(true)
		}
		plan, err := PlanRecovery(intent, intent.After, observation)
		if err != nil || plan.Cleanup != CleanupOld || plan.LegacyPreflightRequired {
			t.Fatalf("v2 %s cleanup: %+v %v", operation, plan, err)
		}
	}
}

func TestRecoveryRefusesAmbiguousAndUnreadState(t *testing.T) {
	for _, test := range []struct {
		name     string
		intent   Intent
		current  RowWitness
		presence CredentialObservation
		wantErr  error
	}{
		{"third-database-state", sampleIntent(Rotate), RowWitness{Present: true, Revision: 99, Enabled: true, SecretName: LegacyName}, CredentialObservation{OldFound: observed(true), NewFound: observed(true)}, ErrRecoveryStateChanged},
		{"unread-new", sampleIntent(InitialConfigure), sampleIntent(InitialConfigure).After, CredentialObservation{}, ErrRecoveryObservationInvalid},
		{"unexpected-old", sampleIntent(InitialConfigure), sampleIntent(InitialConfigure).After, CredentialObservation{OldFound: observed(false), NewFound: observed(true)}, ErrRecoveryObservationInvalid},
		{"unread-old", sampleIntent(Rotate), sampleIntent(Rotate).After, CredentialObservation{NewFound: observed(true)}, ErrRecoveryObservationInvalid},
		{"unexpected-new", sampleIntent(Disconnect), sampleIntent(Disconnect).After, CredentialObservation{OldFound: observed(true), NewFound: observed(false)}, ErrRecoveryObservationInvalid},
		{"promote-unexpected-old", sampleIntent(PromoteLegacy), sampleIntent(PromoteLegacy).After, CredentialObservation{OldFound: observed(true), NewFound: observed(true)}, ErrRecoveryObservationInvalid},
		{"promote-unread-new", sampleIntent(PromoteLegacy), sampleIntent(PromoteLegacy).After, CredentialObservation{}, ErrRecoveryObservationInvalid},
		{"promote-third-state", sampleIntent(PromoteLegacy), RowWitness{Present: true, Revision: 99, Enabled: true, SecretName: LegacyName}, CredentialObservation{NewFound: observed(true)}, ErrRecoveryStateChanged},
		{"invalid-intent", func() Intent { p := sampleIntent(Forget); p.Version = 2; return p }(), sampleIntent(Forget).After, CredentialObservation{OldFound: observed(true)}, ErrIntentInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := PlanRecovery(test.intent, test.current, test.presence)
			if plan != (RecoveryPlan{}) || !errors.Is(err, test.wantErr) {
				t.Fatalf("accepted unsafe recovery: %+v %v", plan, err)
			}
		})
	}
	forgetEnabled := sampleIntent(Forget)
	forgetEnabled.Before.Enabled = true
	if plan, err := PlanRecovery(forgetEnabled, forgetEnabled.Before, CredentialObservation{OldFound: observed(false)}); plan != (RecoveryPlan{}) || !errors.Is(err, ErrRecoveryCredentialMissing) {
		t.Fatalf("forgot active missing secret: %+v %v", plan, err)
	}
	rotateEnabled := sampleIntent(Rotate)
	rotateEnabled.Before.Enabled = true
	if plan, err := PlanRecovery(rotateEnabled, rotateEnabled.Before, CredentialObservation{OldFound: observed(false), NewFound: observed(true)}); plan != (RecoveryPlan{}) || !errors.Is(err, ErrRecoveryCredentialMissing) {
		t.Fatalf("rotated active missing old secret: %+v %v", plan, err)
	}
}
