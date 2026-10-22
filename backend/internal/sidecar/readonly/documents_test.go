package readonly

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/documents/envelope"
	"careerhub.local/backend/internal/documents/keys"
	"careerhub.local/backend/internal/documents/ledger"
	"careerhub.local/backend/internal/secretstore"
)

func TestDocumentReadRejectsBeforeStorage(t *testing.T) {
	var missing *Workspace
	if value, e := missing.Documents(nil, true); value != nil {
		t.Fatal("partial value")
	} else {
		requireCode(t, e, "DOCUMENT_READ_CANCELED")
	}
	if value, e := missing.DocumentTitles(context.Background()); value != nil {
		t.Fatal("partial titles")
	} else {
		requireCode(t, e, "DATA_HOME_UNAVAILABLE")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if value, e := (&Workspace{}).Documents(canceled, false); value != nil {
		t.Fatal("canceled metadata")
	} else {
		requireCode(t, e, "DOCUMENT_READ_CANCELED")
	}
}

func TestDocumentAdmissionBeforeAndAfterRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		info  *datahome.Info
		issue *datahome.Error
		code  string
	}{
		{"missing", nil, nil, "DATA_HOME_UNAVAILABLE"},
		{"uninitialized", &datahome.Info{}, nil, "DATA_HOME_UNAVAILABLE"},
		{"different_id", &datahome.Info{Initialized: true, DataHomeID: "different", Channel: "dev", FormatVersion: 2}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"recovery", nil, &datahome.Error{Code: "DATA_HOME_RECOVERY_REQUIRED"}, "DATA_HOME_RECOVERY_REQUIRED"},
		{"unknown_error", nil, &datahome.Error{Code: "PRIVATE"}, "DATA_HOME_UNAVAILABLE"},
	} {
		for _, after := range []bool{false, true} {
			for _, loadFails := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/after=%t/loadFails=%t", tc.name, after, loadFails), func(t *testing.T) {
					observations, loads := 0, 0
					result, e := admittedDocuments(context.Background(), fixtureID, func() (*datahome.Info, *datahome.Error) {
						observations++
						if after && observations == 1 {
							return profileInfo(), nil
						}
						return tc.info, tc.issue
					}, func(info datahome.Info) (*documentRead, error) {
						loads++
						if info.DataHomeID != fixtureID {
							t.Fatal("wrong trusted identity")
						}
						if loadFails {
							return nil, fail("DOCUMENT_KEY_UNAVAILABLE")
						}
						return &documentRead{records: []ledger.Document{{Title: "private synthetic"}}}, nil
					})
					requireCode(t, e, tc.code)
					if result != nil || (!after && loads != 0) || (after && loads != 1) {
						t.Fatal("admission leaked result or loaded early")
					}
				})
			}
		}
	}
	for _, field := range []string{"channel", "format"} {
		i := 0
		result, e := admittedDocuments(context.Background(), fixtureID, func() (*datahome.Info, *datahome.Error) {
			i++
			info := profileInfo()
			if i > 1 {
				if field == "channel" {
					info.Channel = "stable"
				} else {
					info.FormatVersion++
				}
			}
			return info, nil
		}, func(datahome.Info) (*documentRead, error) { return &documentRead{records: []ledger.Document{}}, nil })
		if result != nil {
			t.Fatal("identity drift leaked empty result")
		}
		requireCode(t, e, "DATA_HOME_IDENTITY_CHANGED")
	}
	for _, result := range []*documentRead{nil, {}, {records: []ledger.Document{}}} {
		got, e := admittedDocuments(context.Background(), fixtureID, func() (*datahome.Info, *datahome.Error) { return profileInfo(), nil }, func(datahome.Info) (*documentRead, error) { return result, nil })
		if result == nil || result.records == nil {
			if got != nil {
				t.Fatal("invalid result escaped")
			}
			requireCode(t, e, "DOCUMENT_LEDGER_INVALID")
		} else if e != nil || got == nil {
			t.Fatal("empty valid read rejected")
		}
	}
}

func TestDocumentCancellationAndSafeCryptoErrors(t *testing.T) {
	for _, stage := range []string{"before", "inspect", "load", "after"} {
		ctx, cancel := context.WithCancel(context.Background())
		if stage == "before" {
			cancel()
		}
		count := 0
		value, e := admittedDocuments(ctx, fixtureID, func() (*datahome.Info, *datahome.Error) {
			count++
			if stage == "inspect" || (stage == "after" && count == 2) {
				cancel()
			}
			return profileInfo(), nil
		}, func(datahome.Info) (*documentRead, error) {
			if stage == "load" {
				cancel()
			}
			return &documentRead{records: []ledger.Document{}}, nil
		})
		cancel()
		if value != nil {
			t.Fatal("canceled read returned data")
		}
		requireCode(t, e, "DOCUMENT_READ_CANCELED")
	}
	for _, tc := range []struct {
		err  error
		code string
	}{
		{&keys.Error{Code: keys.CodeUnavailable}, "DOCUMENT_KEY_UNAVAILABLE"},
		{&keys.Error{Code: keys.CodeKeyInvalid}, "DOCUMENT_ENCRYPTION_INVALID"},
		{&keys.Error{Code: keys.CodeBackendRequired}, "DOCUMENT_ENCRYPTION_UNAVAILABLE"},
		{&envelope.Error{Code: envelope.CodeAuthenticationFailed}, "DOCUMENT_ENCRYPTION_INVALID"},
		{&secretstore.BackendError{Code: "PRIVATE", Operation: "private", Win32Code: 123}, "DOCUMENT_ENCRYPTION_UNAVAILABLE"},
		{fmt.Errorf("PRIVATE WRAPPER: %w", &secretstore.BackendError{Code: secretstore.CodeWindowsFailure}), "DOCUMENT_ENCRYPTION_UNAVAILABLE"},
		{errors.New("PRIVATE KEY OR PATH"), "DOCUMENT_ENCRYPTION_FAILED"},
	} {
		e := documentCryptoError(tc.err)
		requireCode(t, e, tc.code)
		if errors.Unwrap(e) != nil {
			t.Fatal("native cause retained")
		}
	}
}
