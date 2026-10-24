package datahome

import (
	"bytes"
	"os"
	"testing"
)

func TestCanonicalBootstrapMarker(t *testing.T) {
	id := "dh_0123456789abcdef0123456789abcdef"
	for _, channel := range []string{"dev", "stable"} {
		raw, e := MarkerBytes(channel, id)
		if e != nil {
			t.Fatal(e)
		}
		info, e := parseMarker(raw)
		if e != nil || info.Channel != channel || info.DataHomeID != id {
			t.Fatal("marker not readable by admission")
		}
		want := "{\"application\":\"CareerHub\",\"channel\":\"" + channel + "\",\"data_home_id\":\"" + id + "\",\"format_version\":2}\n"
		if string(raw) != want {
			t.Fatal("marker bytes not canonical")
		}
	}
	// The beta channel was retired; only dev and stable Homes exist.
	if _, e := MarkerBytes("beta", id); e == nil || e.Code != "INVALID_CHANNEL" {
		t.Fatal("retired beta channel accepted")
	}
	if _, e := MarkerBytes("bad", "bad"); e == nil || e.Code != "INVALID_CHANNEL" {
		t.Fatal("invalid channel precedence lost")
	}
	if _, e := MarkerBytes("stable", "bad"); e == nil || e.Code != "INVALID_DATA_HOME" {
		t.Fatal("invalid identity accepted")
	}
	id, err := newHomeID()
	if err != nil || !validHomeID(id) || id[15] != '4' {
		t.Fatal("new UUIDv4 identity invalid")
	}
}

type bootstrapReferenceOutput struct{ buffer bytes.Buffer }

func (b *bootstrapReferenceOutput) Write(p []byte) (int, error) {
	if len(p) > (64<<10)-b.buffer.Len() {
		return 0, os.ErrInvalid
	}
	return b.buffer.Write(p)
}
