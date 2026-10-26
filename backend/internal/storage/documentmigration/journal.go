package documentmigration

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

const (
	JournalVersion     = 1
	MaxJournalBytes    = 4096
	CodeRecoveryFailed = "DOCUMENT_MIGRATION_RECOVERY_FAILED"
)

type JournalState string

const (
	JournalRecoveryVerified JournalState = "recovery_verified"
	JournalKeyPlanned       JournalState = "key_planned"
	JournalKeyCreated       JournalState = "key_created"
	JournalStagingVerified  JournalState = "staging_verified"
	JournalDocumentsSwapped JournalState = "documents_swapped"
	JournalMarkerActivated  JournalState = "marker_activated"
)

// Journal mirrors the source recovery sentinel. Optional key identities remain
// nullable because recovery_verified precedes key planning. The codec alone
// does not authorize any filesystem or credential operation.
type Journal struct {
	MigrationID string
	State       JournalState
	DataHomeID  *string
	KeyID       *string
}

var journalStates = map[JournalState]bool{
	JournalRecoveryVerified: true,
	JournalKeyPlanned:       true,
	JournalKeyCreated:       true,
	JournalStagingVerified:  true,
	JournalDocumentsSwapped: true,
	JournalMarkerActivated:  true,
}

var journalOrder = map[JournalState]int{
	JournalRecoveryVerified: 0,
	JournalKeyPlanned:       1,
	JournalKeyCreated:       2,
	JournalStagingVerified:  3,
	JournalDocumentsSwapped: 4,
	JournalMarkerActivated:  5,
}

// CanAdvanceTo permits only one forward writer transition. Recovery does not
// use this helper to infer filesystem state or authorize rollback.
func (journal Journal) CanAdvanceTo(next Journal) bool {
	currentOrder, current := journalOrder[journal.State]
	nextOrder, following := journalOrder[next.State]
	if !current || !following || next.MigrationID != journal.MigrationID || nextOrder != currentOrder+1 {
		return false
	}
	if journal.State == JournalRecoveryVerified {
		return journal.DataHomeID == nil && journal.KeyID == nil &&
			validJournalIdentity(next)
	}
	return validJournalIdentity(journal) && validJournalIdentity(next) && sameJournalIdentity(journal, next)
}

func validJournalIdentity(journal Journal) bool {
	return journal.DataHomeID != nil && journal.KeyID != nil &&
		lowerHexID(*journal.DataHomeID, "dh_", 32) && lowerHexID(*journal.KeyID, "key_", 32)
}

func sameJournalIdentity(left, right Journal) bool {
	return left.DataHomeID != nil && right.DataHomeID != nil &&
		left.KeyID != nil && right.KeyID != nil &&
		*left.DataHomeID == *right.DataHomeID && *left.KeyID == *right.KeyID
}

// CanonicalBytes reproduces Python json.dumps(sort_keys=True,
// separators=(",", ":"), ensure_ascii=True) plus one LF.
func (journal Journal) CanonicalBytes() ([]byte, *Error) {
	if !lowerHexID(journal.MigrationID, "", 32) || !journalStates[journal.State] ||
		!optionalScalar(journal.DataHomeID) || !optionalScalar(journal.KeyID) {
		return nil, fail(CodeRecoveryFailed)
	}
	result := []byte(`{"data_home_id":`)
	result = appendOptionalASCII(result, journal.DataHomeID)
	result = append(result, `,"key_id":`...)
	result = appendOptionalASCII(result, journal.KeyID)
	result = append(result, `,"migration_id":`...)
	result = appendASCII(result, journal.MigrationID)
	result = append(result, `,"state":`...)
	result = appendASCII(result, string(journal.State))
	result = append(result, `,"version":1}`...)
	return append(result, '\n'), nil
}

// ParseJournal accepts only the exact bounded canonical bytes. Equality after
// typed decoding rejects duplicate/unknown/missing keys, alternate key order,
// whitespace, escaping variants, trailing data, and wrong scalar types.
func ParseJournal(content []byte) (Journal, *Error) {
	if len(content) == 0 || len(content) > MaxJournalBytes || !utf8.Valid(content) {
		return Journal{}, fail(CodeRecoveryFailed)
	}
	var decoded struct {
		DataHomeID  *string      `json:"data_home_id"`
		KeyID       *string      `json:"key_id"`
		MigrationID string       `json:"migration_id"`
		State       JournalState `json:"state"`
		Version     int          `json:"version"`
	}
	if err := json.Unmarshal(content, &decoded); err != nil || decoded.Version != JournalVersion {
		return Journal{}, fail(CodeRecoveryFailed)
	}
	journal := Journal{
		MigrationID: decoded.MigrationID, State: decoded.State,
		DataHomeID: decoded.DataHomeID, KeyID: decoded.KeyID,
	}
	canonical, problem := journal.CanonicalBytes()
	if problem != nil || !bytes.Equal(content, canonical) {
		return Journal{}, fail(CodeRecoveryFailed)
	}
	return journal, nil
}

func lowerHexID(value, prefix string, digits int) bool {
	if len(value) != len(prefix)+digits || value[:len(prefix)] != prefix {
		return false
	}
	for _, character := range value[len(prefix):] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func optionalScalar(value *string) bool {
	return value == nil || utf8.ValidString(*value)
}

func appendOptionalASCII(target []byte, value *string) []byte {
	if value == nil {
		return append(target, "null"...)
	}
	return appendASCII(target, *value)
}

func appendASCII(target []byte, value string) []byte {
	target = append(target, '"')
	for _, character := range value {
		switch character {
		case '"', '\\':
			target = append(target, '\\', byte(character))
		case '\b':
			target = append(target, `\b`...)
		case '\f':
			target = append(target, `\f`...)
		case '\n':
			target = append(target, `\n`...)
		case '\r':
			target = append(target, `\r`...)
		case '\t':
			target = append(target, `\t`...)
		default:
			if character >= 0x20 && character <= 0x7e {
				target = append(target, byte(character))
			} else if character <= 0xffff {
				target = appendUnicodeEscape(target, uint16(character))
			} else {
				character -= 0x10000
				target = appendUnicodeEscape(target, uint16(0xd800+(character>>10)))
				target = appendUnicodeEscape(target, uint16(0xdc00+(character&0x3ff)))
			}
		}
	}
	return append(target, '"')
}

func appendUnicodeEscape(target []byte, value uint16) []byte {
	const digits = "0123456789abcdef"
	return append(target, '\\', 'u', digits[value>>12], digits[(value>>8)&15], digits[(value>>4)&15], digits[value&15])
}
