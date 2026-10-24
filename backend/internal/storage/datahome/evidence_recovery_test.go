package datahome

import (
	"testing"
)

func TestEvidenceRecoveryBypassesOnlyItsOwnSentinel(t *testing.T) {
	for _, skip := range []string{"", RegistryRecoveryPath, EvidenceRecoveryPath, DiscoveryRecoveryPath, ResumeRecoveryPath, IntakeRecoveryPath, NewJobRecoveryPath, EmailCredentialRecoveryPath, ApprovalRecoveryPath} {
		for _, pending := range []string{DocumentRecoveryPath, RestoreRecoveryPath, RegistryRecoveryPath, EvidenceRecoveryPath, DiscoveryRecoveryPath, ResumeRecoveryPath, IntakeRecoveryPath, NewJobRecoveryPath, EmailCredentialRecoveryPath, ApprovalRecoveryPath} {
			source := &fakeReader{marker: []byte(validMarker), present: true, sentinel: pending}
			info, e := inspectSkippingRecovery(source, nil, skip)
			if skip == pending {
				if e != nil || info == nil {
					t.Fatal("own recovery sentinel refused")
				}
			} else if e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" || info != nil {
				t.Fatal("foreign pending work admitted")
			}
			for _, call := range source.calls {
				if call == "read:"+pending {
					t.Fatal("admission parsed a private journal")
				}
			}
		}
	}
	if info, e := InspectOwnedForEvidenceRecovery(nil, nil); info != nil || e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatal("owner required")
	}
	if info, e := InspectOwnedForDiscoveryRecovery(nil, nil); info != nil || e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatal("discovery owner required")
	}
	if info, e := InspectOwnedForResumeRecovery(nil, nil); info != nil || e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatal("resume owner required")
	}
	if info, e := InspectOwnedForIntakeRecovery(nil, nil); info != nil || e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatal("intake owner required")
	}
	if info, e := InspectOwnedForNewJobRecovery(nil, nil); info != nil || e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatal("new-job owner required")
	}
	if info, e := InspectOwnedForApprovalRecovery(nil, nil); info != nil || e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatal("approval owner required")
	}
}
