package applicationwrite

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func testDiscoveryIntent() discoveryIntent {
	return discoveryIntent{Version: 1, Home: datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_0123456789abcdef0123456789abcdef"}, Operation: "capture",
		Files: []discoveryFileIntent{{Change: discovery.SourceChange{RelativePath: "jobs/discovery/captures/discovery_one/source.txt", AfterPresent: true, After: []byte("Synthetic Ω source")},
			Before: discovery.RowWitness{DiscoveryID: "discovery_one"}, After: discovery.RowWitness{DiscoveryID: "discovery_one", Present: true, SHA256: digestBytes([]byte("row"))}}}, Guards: []discoveryReadGuard{}}
}
func TestDiscoveryIntentStrictEnvelope(t *testing.T) {
	p := testDiscoveryIntent()
	raw, e := encodeDiscoveryIntent(p)
	if e != nil {
		t.Fatal(e.Code)
	}
	if discoveryIntentLimit > runtimepaths.MaxReadBytes {
		t.Fatal("journal exceeds native file bounds")
	}
	decoded, e := decodeDiscoveryIntent(raw, p.Home)
	if e != nil || !bytes.Equal(decoded.Files[0].Change.After, p.Files[0].Change.After) {
		t.Fatal("round trip", e)
	}
	for name, change := range map[string]func(*discoveryIntent){
		"version":     func(p *discoveryIntent) { p.Version++ },
		"wrong-id":    func(p *discoveryIntent) { p.Files[0].After.DiscoveryID = "another" },
		"path-escape": func(p *discoveryIntent) { p.Files[0].Change.RelativePath = "jobs/discovery/captures/../source.txt" },
		"drive": func(p *discoveryIntent) {
			p.Files[0].Change.RelativePath = "jobs/discovery/captures/id:stream/source.txt"
		},
		"aliased-path": func(p *discoveryIntent) {
			p.Guards = append(p.Guards, discoveryReadGuard{Path: strings.ToUpper(p.Files[0].Change.RelativePath)})
		},
		"absent-with-bytes": func(p *discoveryIntent) { p.Files[0].Change.Before = []byte("oops") },
		"unchanged-row":     func(p *discoveryIntent) { p.Files[0].Before = p.Files[0].After },
		"hash":              func(p *discoveryIntent) { p.Files[0].After.SHA256 = "xx" },
		"invalid-text":      func(p *discoveryIntent) { p.Files[0].Change.After = []byte{0xff} },
		"null-text":         func(p *discoveryIntent) { p.Files[0].Change.After = []byte("x\x00y") },
		"invalid-guard": func(p *discoveryIntent) {
			p.Guards = []discoveryReadGuard{{Path: "jobs/discovery/captures/other/source.txt", Size: 1}}
		},
		"missing-before-row": func(p *discoveryIntent) { p.Files[0].Change.BeforePresent = true },
		"delete-with-after":  func(p *discoveryIntent) { p.Operation = "delete" },
	} {
		t.Run(name, func(t *testing.T) {
			p := testDiscoveryIntent()
			change(&p)
			if _, e := encodeDiscoveryIntent(p); e == nil {
				t.Fatal("invalid intent encoded")
			}
		})
	}
	for name, bad := range map[string][]byte{
		"trailing":  append(append([]byte(nil), raw...), ' '),
		"duplicate": bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"unknown":   bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"token":"private"`), 1),
		"checksum":  bytes.Replace(raw, []byte(`"operation":"capture"`), []byte(`"operation":"delete"`), 1),
		"alias":     bytes.Replace(raw, []byte(`"payload"`), []byte(`"Payload"`), 1),
		"escape":    bytes.Replace(raw, []byte(`"version"`), []byte(`"\u0076ersion"`), 1),
		"deep":      []byte(strings.Repeat("[", 17) + "0" + strings.Repeat("]", 17)),
		"oversized": bytes.Repeat([]byte(" "), discoveryIntentLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := decodeDiscoveryIntent(bad, p.Home); e == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
	other := p.Home
	other.Channel = "stable"
	if _, e := decodeDiscoveryIntent(raw, other); e == nil || e.Code != "DISCOVERY_INTENT_HOME_CHANGED" {
		t.Fatal("wrong home accepted")
	}
	// Re-signing does not excuse unknown/duplicate inner fields.
	var env discoveryEnvelope
	_ = json.Unmarshal(raw, &env)
	env.Payload = bytes.Replace(env.Payload, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
	env.SHA256 = digestBytes(env.Payload)
	bad, _ := json.Marshal(env)
	if _, e := decodeDiscoveryIntent(bad, p.Home); e == nil {
		t.Fatal("re-signed duplicate accepted")
	}
}
