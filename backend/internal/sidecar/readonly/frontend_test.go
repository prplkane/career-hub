package readonly

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/datahome"
)

func TestFrontendFixedReadRuntimeAndIdentity(t *testing.T) {
	base, start := fixture(t)
	writeFixture(t, start.ResourceRoot, "src/intake_ui_static/index.html", "<!doctype html><title>Career Hub</title>")
	w, e := Open(start)
	if e != nil {
		t.Fatal(e)
	}
	before := tree(t, base)
	raw, e := w.StaticAsset(context.Background(), "index.html")
	if e != nil || string(raw) != "<!doctype html><title>Career Hub</title>" {
		t.Fatal("static read", e)
	}
	for _, name := range []string{"../VERSION", "../../VERSION", "/VERSION", "missing.js"} {
		if raw, e := w.StaticAsset(context.Background(), name); e == nil || len(raw) != 0 {
			t.Fatal("unsafe/missing resource accepted")
		}
	}
	raw, e = w.RuntimeInfo(context.Background())
	var info map[string]any
	if e != nil || json.Unmarshal(raw, &info) != nil || info["data_root"] != start.DataRoot || info["storage_mode"] != "external_data_home" || info["data_profile"] != nil {
		t.Fatal("runtime identity")
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("frontend reads changed storage")
	}
	writeFixture(t, start.DataRoot, datahome.MarkerPath, marker("dev", "dh_abcdef0123456789abcdef0123456789"))
	if raw, e := w.StaticAsset(context.Background(), "index.html"); e == nil || len(raw) != 0 {
		t.Fatal("changed home static read")
	}
	if raw, e := w.RuntimeInfo(context.Background()); e == nil || len(raw) != 0 {
		t.Fatal("changed home runtime")
	}
}
