package profilename

import "testing"

// Only an unambiguous two-word name is split; everything else is left for
// the user, because forms copy these parts verbatim.
func TestSplit(t *testing.T) {
	for _, tc := range []struct {
		name, given, family string
		ok                  bool
	}{
		{"Ada Lovelace", "Ada", "Lovelace", true},
		{"  Ada   Lovelace  ", "Ada", "Lovelace", true},
		{"Kim Min-jun", "Kim", "Min-jun", true},
		{"Cher", "", "", false},
		{"Mary Ann Smith", "", "", false},
		{"Juan de la Cruz", "", "", false},
		{"", "", "", false},
		{"   ", "", "", false},
	} {
		given, family, ok := Split(tc.name)
		if given != tc.given || family != tc.family || ok != tc.ok {
			t.Errorf("Split(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.name, given, family, ok, tc.given, tc.family, tc.ok)
		}
	}
}
