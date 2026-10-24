package privacy

import (
	"strings"
	"testing"
)

type policyCase struct {
	Name      string `json:"name"`
	SDDL      string `json:"sddl"`
	Principal string `json:"principal"`
	Inherited bool   `json:"inherited"`
	Want      bool   `json:"-"`
}

func policyCases() []policyCase {
	const sid = "S-1-5-21-1000"
	const root = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;" + sid + ")"
	cases := []policyCase{
		{"root", root, sid, false, true},
		{"star_principal", root, "*" + sid, false, true},
		{"root_without_AI", strings.Replace(root, "PAI", "P", 1), sid, false, true},
		{"control_order_duplicates", strings.Replace(root, "PAI", "ARAIPP", 1), sid, false, true},
		{"inherited_dir", strings.ReplaceAll(strings.Replace(root, "PAI", "AI", 1), "OICI", "OICIID"), sid, true, true},
		{"inherited_file", strings.ReplaceAll(strings.Replace(root, "PAI", "AI", 1), "OICI", "ID"), sid, true, true},
		{"flag_order", strings.ReplaceAll(root, "OICI", "CIOI"), sid, false, true},
		{"system_numeric", strings.Replace(root, "SY", "S-1-5-18", 1), sid, false, true},
		{"system_duplicate", strings.Replace(root, sid, "S-1-5-18", 1), "S-1-5-18", false, true},
		{"admin_alias", strings.Replace(root, sid, "LA", 1), "S-1-5-21-100-200-300-500", false, true},
		{"admin_wrong_user", strings.Replace(root, sid, "LA", 1), sid, false, false},
		{"root_unprotected", strings.Replace(root, "PAI", "AI", 1), sid, false, false},
		{"child_protected", root, sid, true, false},
		{"child_no_inherited", strings.Replace(root, "PAI", "AI", 1), sid, true, false},
		{"single_system", "D:P(A;OICI;FA;;;SY)", "S-1-5-18", false, false},
		{"duplicate_user", strings.Replace(root, "SY", sid, 1), sid, false, false},
		{"wrong_user", root, "S-1-5-21-2000", false, false},
		{"single_ace", "D:P(A;OICI;FA;;;SY)", sid, false, false},
		{"third_ace", root + "(A;OICI;FA;;;BU)", sid, false, false},
		{"null_dacl", "D:NO_ACCESS_CONTROL", sid, false, false},
		{"empty_dacl", "D:P", sid, false, false},
		{"missing_dacl", "", sid, false, false},
		{"owner_prefix", "O:SY" + root, sid, false, false},
		{"sacl_suffix", root + "S:", sid, false, false},
		{"bad_control", strings.Replace(root, "PAI", "PX", 1), sid, false, false},
		{"trailing_byte", root + "x", sid, false, false},
		{"missing_close", root[:len(root)-1], sid, false, false},
		{"missing_open", strings.Replace(root, "(A", "A", 1), sid, false, false},
		{"bad_principal", root, "candidate", false, false},
		{"double_star", root, "**" + sid, false, false},
		{"principal_newline", root, sid + "\n", false, false},
		{"principal_short", root, "S-1", false, false},
	}
	for _, flags := range []string{"", "OI", "CI", "OIOICI", "OICIIO", "OICIIN", "OICIID", "OICINP", "OICIX", "OICISA"} {
		cases = append(cases, policyCase{"invalid_root_flags_" + flags, strings.Replace(root, "OICI", flags, 1), sid, false, false})
	}
	for _, rights := range []string{"GA", "FR", "FAFR", "0x1f01ff", "", "FA;"} {
		cases = append(cases, policyCase{"invalid_rights_" + rights, strings.Replace(root, ";FA;", ";"+rights+";", 1), sid, false, false})
	}
	for _, kind := range []string{"D", "OA", "XA", "AU", ""} {
		cases = append(cases, policyCase{"invalid_type_" + kind, strings.Replace(root, "(A;", "("+kind+";", 1), sid, false, false})
	}
	for _, field := range []string{";FA;x;;", ";FA;;x;"} {
		cases = append(cases, policyCase{"object_guid_" + field, strings.Replace(root, ";FA;;;", field, 1), sid, false, false})
	}
	return cases
}

func TestPrivateDACL(t *testing.T) {
	for _, tc := range policyCases() {
		t.Run(tc.Name, func(t *testing.T) {
			if got := privateDACL(tc.SDDL, tc.Principal, tc.Inherited); got != tc.Want {
				t.Fatalf("got %v, want %v", got, tc.Want)
			}
		})
	}
}

func TestErrorContainsOnlyCode(t *testing.T) {
	if got := fail("ACL_SETUP_FAILED").Error(); got != "data home privacy: ACL_SETUP_FAILED" {
		t.Fatal("unexpected error contract")
	}
}

func FuzzPrivateDACL(f *testing.F) {
	for _, tc := range policyCases() {
		f.Add(tc.SDDL, tc.Principal, tc.Inherited)
	}
	f.Fuzz(func(t *testing.T, sddl, principal string, inherited bool) {
		if len(sddl) > 4096 || len(principal) > 128 {
			t.Skip()
		}
		if privateDACL(sddl, principal, inherited) && privateDACL(sddl, principal, !inherited) {
			t.Fatal("same descriptor accepted as both root and inherited")
		}
	})
}
