package homebackup

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func legacyRestoreFixture() []byte {
	return []byte(`{"marker_sha256":"` + strings.Repeat("b", 64) + `","restore_id":"` + strings.Repeat("a", 32) + `","roots":[{"had_active":true,"name":"jobs"}],"version":1}` + "\n")
}

func TestLegacyRestoreJournalRefusal(t *testing.T) {
	raw := legacyRestoreFixture()
	for _, value := range [][]byte{raw, bytes.Replace(raw, []byte(`[{"had_active":true,"name":"jobs"}]`), []byte(`[]`), 1)} {
		for _, scope := range []string{"", strings.Repeat("c", 64)} {
			j, p := decodeRestoreJournal(value, scope)
			if p == nil || p.Code != "BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED" || !reflect.DeepEqual(j, restoreJournal{}) {
				t.Fatal("legacy journal became native authority", j, p)
			}
		}
	}
	for _, invalid := range [][]byte{
		bytes.TrimSpace(raw), append(bytes.Clone(raw), '\n'),
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1.0`), 1),
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"extra":false`), 1),
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		bytes.Replace(raw, []byte(`"had_active":true`), []byte(`"had_active":null`), 1),
		bytes.Replace(raw, []byte(`"name":"jobs"`), []byte(`"name":"../jobs"`), 1),
		bytes.Replace(raw, []byte(`[{"had_active":true,"name":"jobs"}]`), []byte(`null`), 1),
		bytes.Replace(raw, []byte(`[{"had_active":true,"name":"jobs"}]`), []byte(`[{"had_active":true,"name":"jobs"},{"had_active":false,"name":"jobs"}]`), 1),
	} {
		if isLegacyRestoreJournal(invalid) {
			t.Fatal("malformed journal classified as canonical legacy")
		}
		if _, p := decodeRestoreJournal(invalid, strings.Repeat("c", 64)); p == nil || p.Code != "BACKUP_RESTORE_JOURNAL_INVALID" {
			t.Fatal("malformed journal refusal changed", p)
		}
	}
}
