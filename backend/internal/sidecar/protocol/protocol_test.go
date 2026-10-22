package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var protocolError *Error
	if !errors.As(err, &protocolError) || protocolError.Code != code || err.Error() != code {
		t.Fatalf("got %v, want code %s", err, code)
	}
}

func requireUnsupported(t *testing.T, err error, reason string) {
	t.Helper()
	var unsupported *CompatibilityError
	if !errors.As(err, &unsupported) || unsupported.Reason != reason {
		t.Fatalf("got %v, want compatibility reason %s", err, reason)
	}
}

func TestReadFraming(t *testing.T) {
	padding := strings.Repeat(" ", MaxRecordBytes-2)
	cases := []struct{ name, raw, code string }{
		{"object", "{}\n", ""},
		{"crlf", "{}\r\n", ""},
		{"exact_lf", "{}" + padding + "\n", ""},
		{"exact_crlf", "{}" + padding + "\r\n", ""},
		{"over_lf", "{}" + padding + " \n", "SIDECAR_RECORD_TOO_LARGE"},
		{"over_crlf", "{}" + padding + " \r\n", "SIDECAR_RECORD_TOO_LARGE"},
		{"empty_pipe", "", "SIDECAR_PIPE_CLOSED"},
		{"empty_record", "\n", "SIDECAR_RECORD_INVALID_JSON"},
		{"empty_crlf", "\r\n", "SIDECAR_RECORD_INVALID_JSON"},
		{"whitespace", " \n", "SIDECAR_RECORD_INVALID_JSON"},
		{"no_lf", "{}", "SIDECAR_RECORD_TRUNCATED"},
		{"only_cr", "{}\r", "SIDECAR_RECORD_TRUNCATED"},
		{"exact_no_lf", "{}" + padding, "SIDECAR_RECORD_TRUNCATED"},
		{"over_no_lf", "{}" + padding + " ", "SIDECAR_RECORD_TOO_LARGE"},
		{"invalid_utf8", "{\"x\":\"\xff\"}\n", "SIDECAR_RECORD_INVALID_UTF8"},
		{"invalid_utf8_no_lf_order", "\xff", "SIDECAR_RECORD_TRUNCATED"},
		{"invalid_utf8_too_large_order", strings.Repeat("\xff", MaxRecordBytes+1) + "\n", "SIDECAR_RECORD_TOO_LARGE"},
		{"bom", "\xef\xbb\xbf{}\n", "SIDECAR_RECORD_INVALID_JSON"},
		{"malformed", "{not-json}\n", "SIDECAR_RECORD_INVALID_JSON"},
		{"trailing_object", "{} {}\n", "SIDECAR_RECORD_INVALID_JSON"},
		{"array", "[]\n", "SIDECAR_RECORD_INVALID_SHAPE"},
		{"null", "null\n", "SIDECAR_RECORD_INVALID_SHAPE"},
		{"string", "\"s\"\n", "SIDECAR_RECORD_INVALID_SHAPE"},
		{"number", "123\n", "SIDECAR_RECORD_INVALID_SHAPE"},
		{"nan_scalar", "NaN\n", "SIDECAR_RECORD_INVALID_SHAPE"},
		{"infinity_scalar", "-Infinity\n", "SIDECAR_RECORD_INVALID_SHAPE"},
		{"nan_key", "{NaN:0}\n", "SIDECAR_RECORD_INVALID_JSON"},
		{"bad_constant", "{\"n\":+Infinity}\n", "SIDECAR_RECORD_INVALID_JSON"},
		{"unescaped_control", "{\"s\":\"\t\"}\n", "SIDECAR_RECORD_INVALID_JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { _, err := ReadRecord(strings.NewReader(tc.raw)); requireCode(t, err, tc.code) })
	}
	reader := strings.NewReader("{}\n{\"next\":true}\n")
	_, err := ReadRecord(reader)
	requireCode(t, err, "")
	second, err := ReadRecord(reader)
	requireCode(t, err, "")
	if second["next"] != true {
		t.Fatal("consumed next record")
	}
	// The byte cap includes UTF-8 bytes, not code points.
	raw := `{"x":"` + strings.Repeat("é", (MaxRecordBytes-8)/2) + `"}`
	if len(raw) != MaxRecordBytes {
		t.Fatalf("bad fixture: %d", len(raw))
	}
	_, err = ReadRecord(strings.NewReader(raw + "\r\n"))
	requireCode(t, err, "")
	_, err = ReadRecord(strings.NewReader(strings.Replace(raw, "é", "€", 1) + "\n"))
	requireCode(t, err, "SIDECAR_RECORD_TOO_LARGE")
	reader = strings.NewReader(strings.Repeat("x", 20000))
	_, err = ReadRecord(reader)
	requireCode(t, err, "SIDECAR_RECORD_TOO_LARGE")
	if reader.Len() != 20000-(MaxRecordBytes+2) {
		t.Fatal("read beyond framing bound")
	}
}

func TestJSONPythonSemantics(t *testing.T) {
	raw := `{"x":1,"x":2,"obj":{"a":1},"obj":{"b":2},"nan":NaN,"inf":Infinity,"negative":-Infinity,"overflow":1e400,"marker":9e99990,"str":"NaN Infinity -Infinity","pair":"\ud83d\ude00","slash":"\/","integer":-0,"float":0.0}` + "\n"
	payload, err := ReadRecord(strings.NewReader(raw))
	requireCode(t, err, "")
	if payload["x"] != json.Number("2") || !reflect.DeepEqual(payload["obj"], map[string]any{"b": json.Number("2")}) {
		t.Fatal("duplicate keys must replace, not merge")
	}
	if !math.IsNaN(payload["nan"].(float64)) || !math.IsInf(payload["inf"].(float64), 1) || !math.IsInf(payload["negative"].(float64), -1) || !math.IsInf(payload["overflow"].(float64), 1) || !math.IsInf(payload["marker"].(float64), 1) {
		t.Fatal("nonfinite numbers lost")
	}
	if payload["str"] != "NaN Infinity -Infinity" || payload["pair"] != "😀" || payload["slash"] != "/" {
		t.Fatal("string decoding changed")
	}
	if payload["integer"] != json.Number("-0") || payload["float"] != float64(0) {
		t.Fatal("integer/float distinction lost")
	}
	for _, input := range []string{`{"x":"\ud800"}`, `{"x":"\udc00"}`, `{"\ud800":1}`, `{"x":"\ud800\u0041"}`} {
		_, err = ReadRecord(strings.NewReader(input + "\n"))
		requireUnsupported(t, err, "escaped lone surrogate")
	}
	_, err = ReadRecord(strings.NewReader(`{"x":"\ud800"` + "\n"))
	requireCode(t, err, "SIDECAR_RECORD_INVALID_JSON")
	_, err = ReadRecord(strings.NewReader(`{"x":` + strings.Repeat("9", 4301) + "}\n"))
	requireUnsupported(t, err, "Python integer digit limit")
	_, err = ReadRecord(strings.NewReader(`{"x":` + strings.Repeat("9", 4300) + "}\n"))
	requireCode(t, err, "")
	_, err = ReadRecord(strings.NewReader(`{"x":` + strings.Repeat("9", 4301) + ",\"x\":1}\n"))
	requireUnsupported(t, err, "Python integer digit limit")
	_, err = ReadRecord(strings.NewReader(`{"x":` + strings.Repeat("[", 900) + "0" + strings.Repeat("]", 900) + "}\n"))
	requireUnsupported(t, err, "deeply nested input")
	_, err = ReadRecord(strings.NewReader(`{"x":` + strings.Repeat("[", 899) + "0" + strings.Repeat("]", 899) + "}\n"))
	requireCode(t, err, "")
}

func startPayload() map[string]any {
	return map[string]any{"command": "start", "protocol_version": 1, "port": 0, "resource_root": `C:\resources`, "data_root": `C:\synthetic`, "channel": "stable"}
}

func TestStartValidation(t *testing.T) {
	cases := []struct {
		name, field string
		value       any
		code        string
	}{
		{"missing", "missing", nil, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"extra", "extra", true, "SIDECAR_RECORD_INVALID_FIELDS"},
		{"version_bool", "protocol_version", true, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"version_float", "protocol_version", 1.0, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"version_null", "protocol_version", nil, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"version_number_float", "protocol_version", json.Number("1.0"), "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"version_next", "protocol_version", 2, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"version_nan", "protocol_version", math.NaN(), "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{"command", "command", "shutdown", "SIDECAR_COMMAND_UNSUPPORTED"},
		{"command_array", "command", []any{}, "SIDECAR_COMMAND_UNSUPPORTED"},
		{"port_bool", "port", false, "SIDECAR_START_INVALID_PORT"},
		{"port_float", "port", 0.0, "SIDECAR_START_INVALID_PORT"},
		{"port_nonzero", "port", 8766, "SIDECAR_START_INVALID_PORT"},
		{"port_inf", "port", math.Inf(1), "SIDECAR_START_INVALID_PORT"},
		{"channel_case", "channel", "Stable", "SIDECAR_START_INVALID_CHANNEL"},
		{"channel_type", "channel", 1, "SIDECAR_START_INVALID_CHANNEL"},
		{"channel_unknown", "channel", "nightly", "SIDECAR_START_INVALID_CHANNEL"},
		{"path_type", "data_root", nil, "SIDECAR_START_INVALID_PATH"},
		{"path_empty", "data_root", "", "SIDECAR_START_INVALID_PATH"},
		{"path_nul", "data_root", "C:\\secret\x00path", "SIDECAR_START_INVALID_PATH"},
		{"path_relative", "data_root", "relative", "SIDECAR_START_INVALID_PATH"},
		{"path_over", "data_root", `C:\` + strings.Repeat("😀", MaxPathChars-2), "SIDECAR_START_INVALID_PATH"},
		{"path_exact_unicode", "data_root", `C:\` + strings.Repeat("😀", MaxPathChars-3), ""},
		{"retired_beta", "channel", "beta", "SIDECAR_START_INVALID_CHANNEL"}, {"dev", "channel", "dev", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := startPayload()
			if tc.field == "missing" {
				delete(p, "command")
			} else {
				p[tc.field] = tc.value
			}
			_, err := ParseStart(p, PathOptions{Style: Windows})
			requireCode(t, err, tc.code)
		})
	}
	// Remove the earlier invalid field one at a time to prove source order.
	p := map[string]any{"command": "bad", "protocol_version": false, "port": false, "channel": "bad", "resource_root": "bad", "data_root": "bad", "extra": true}
	for _, step := range []struct {
		code, field string
		value       any
	}{
		{"SIDECAR_RECORD_INVALID_FIELDS", "extra", nil},
		{"SIDECAR_PROTOCOL_UNSUPPORTED", "protocol_version", 1},
		{"SIDECAR_COMMAND_UNSUPPORTED", "command", "start"},
		{"SIDECAR_START_INVALID_PORT", "port", 0},
		{"SIDECAR_START_INVALID_CHANNEL", "channel", "stable"},
		{"SIDECAR_START_INVALID_PATH", "resource_root", `C:\resources`},
		{"SIDECAR_START_INVALID_PATH", "data_root", `C:\synthetic`},
	} {
		_, err := ParseStart(p, PathOptions{Style: Windows})
		requireCode(t, err, step.code)
		if step.field == "extra" {
			delete(p, step.field)
		} else {
			p[step.field] = step.value
		}
	}
	result, err := ParseStart(p, PathOptions{Style: Windows})
	requireCode(t, err, "")
	if result.Port != 0 || result.Channel != "stable" || result.ResourceRoot != `C:\resources` || result.DataRoot != `C:\synthetic` {
		t.Fatal("wrong result")
	}
	// Framing-to-validation bridge covers actual JSON numbers, duplicate keys,
	// and nonfinite constants instead of only direct Go value construction.
	for _, port := range []struct{ raw, code string }{{"0", ""}, {"-0", ""}, {"0.0", "SIDECAR_START_INVALID_PORT"}, {"0e0", "SIDECAR_START_INVALID_PORT"}, {"false", "SIDECAR_START_INVALID_PORT"}, {"NaN", "SIDECAR_START_INVALID_PORT"}, {"Infinity", "SIDECAR_START_INVALID_PORT"}, {"1e400", "SIDECAR_START_INVALID_PORT"}} {
		raw := `{"command":"start","protocol_version":2,"protocol_version":1,"port":` + port.raw + `,"resource_root":"C:/r","data_root":"C:/d","channel":"dev"}` + "\n"
		payload, err := ReadRecord(strings.NewReader(raw))
		requireCode(t, err, "")
		_, err = ParseStart(payload, PathOptions{Style: Windows})
		requireCode(t, err, port.code)
	}
}

func TestShutdownValidation(t *testing.T) {
	for _, tc := range []struct {
		payload map[string]any
		code    string
	}{
		{map[string]any{"command": "shutdown", "protocol_version": 1}, ""},
		{map[string]any{"command": "shutdown", "protocol_version": json.Number("1")}, ""},
		{nil, "SIDECAR_RECORD_INVALID_FIELDS"},
		{map[string]any{"command": "bad", "protocol_version": false, "extra": 1}, "SIDECAR_RECORD_INVALID_FIELDS"},
		{map[string]any{"command": "bad", "protocol_version": false}, "SIDECAR_PROTOCOL_UNSUPPORTED"},
		{map[string]any{"command": "bad", "protocol_version": 1}, "SIDECAR_COMMAND_UNSUPPORTED"},
		{map[string]any{"command": "shutdown", "protocol_version": 1.0}, "SIDECAR_PROTOCOL_UNSUPPORTED"},
	} {
		requireCode(t, ParseShutdown(tc.payload), tc.code)
	}
}

func TestPathStyles(t *testing.T) {
	for _, tc := range []struct {
		style         PathStyle
		raw, expected string
	}{
		{Windows, "C:/a//./b/../", `C:\a\b\..`},
		{Windows, "//host/share", `\\host\share\`},
		{Windows, "//host/share/a", `\\host\share\a`},
		{Windows, "//?/C:/a", `\\?\C:\a`},
		{Windows, "//?/UNC/host/share", `\\?\UNC\host\share\`},
		{Windows, "//./pipe/name", `\\.\pipe\name`},
		{Windows, "///foo/bar", `\\\foo\bar`},
		{Windows, "é:/data", `é:\data`},
		{Windows, "/data", ""}, {Windows, "C:data", ""}, {Windows, "C:", ""}, {Windows, "//host", ""},
		{POSIX, "/a//./b/../", "/a/b/.."},
		{POSIX, "//host/share", "//host/share"},
		{POSIX, "///a", "/a"}, {POSIX, "C:/data", ""},
		{POSIX, "/a\\b", "/a\\b"}, {POSIX, "/", "/"},
	} {
		t.Run(string(tc.style)+tc.raw, func(t *testing.T) {
			result, err := absolutePath(tc.raw, PathOptions{Style: tc.style})
			if tc.expected == "" {
				requireCode(t, err, "SIDECAR_START_INVALID_PATH")
			} else {
				requireCode(t, err, "")
				if result != tc.expected {
					t.Fatalf("got %q, want %q", result, tc.expected)
				}
			}
		})
	}
	raw, expected := "/a/./b/../c", "/a/b/../c"
	if runtime.GOOS == "windows" {
		raw, expected = "C:/a/./b/../c", `C:\a\b\..\c`
	}
	result, err := absolutePath(raw, PathOptions{})
	requireCode(t, err, "")
	if result != expected {
		t.Fatalf("native path: got %q", result)
	}
	_, err = absolutePath("~/x", PathOptions{Style: Windows})
	requireUnsupported(t, err, "home expansion requires injected resolver")
	var calls []string
	resolver := func(user string) (string, error) { calls = append(calls, user); return "C:/fixture-home", nil }
	result, err = absolutePath("./~test/a/../b", PathOptions{Style: Windows, ExpandUser: resolver})
	requireCode(t, err, "")
	if result != `C:\fixture-home\a\..\b` || !reflect.DeepEqual(calls, []string{"~test"}) {
		t.Fatalf("home callback %q %v", result, calls)
	}
	_, err = absolutePath("C:/~test/x", PathOptions{Style: Windows, ExpandUser: resolver})
	requireCode(t, err, "")
	if len(calls) != 1 {
		t.Fatal("expanded a nonleading home marker")
	}
	_, err = absolutePath("~/x", PathOptions{Style: POSIX, ExpandUser: func(string) (string, error) { return "~", nil }})
	requireUnsupported(t, err, "home expansion unavailable")
	// Resource path validation precedes attempted data-root expansion.
	p := startPayload()
	p["resource_root"] = "relative"
	p["data_root"] = "~/data"
	_, err = ParseStart(p, PathOptions{Style: Windows})
	requireCode(t, err, "SIDECAR_START_INVALID_PATH")
}

func TestExactOutput(t *testing.T) {
	for _, tc := range []struct {
		name     string
		payload  map[string]any
		expected string
	}{
		{"ready", ReadyRecord("0.1.2", 49152, "stable"), `{"app_version":"0.1.2","channel":"stable","event":"ready","port":49152,"protocol_version":1}`},
		{"health", HealthRecord("0.1.2"), `{"app_version":"0.1.2","protocol_version":1,"status":"ok"}`},
		{"error", ErrorRecord("SIDECAR_START_INVALID_PATH"), `{"error_code":"SIDECAR_START_INVALID_PATH","event":"error","protocol_version":1}`},
		{"unicode", map[string]any{"é": "<>& / 😀\u007f\n\t\b\f\r\"\\", "a": true}, `{"a":true,"\u00e9":"<>& / \ud83d\ude00\u007f\n\t\b\f\r\"\\"}`},
		{"codepoint_sort", map[string]any{"😀": 1, "\ue000": 2}, `{"\ue000":2,"\ud83d\ude00":1}`},
		{"nested", map[string]any{"b": []any{nil, false, json.Number("-0"), 1.0, 1e6, 1e16, 1e-5, math.Copysign(0, -1), math.NaN(), math.Inf(1), math.Inf(-1)}, "a": map[string]any{"z": 1, "b": 2}}, `{"a":{"b":2,"z":1},"b":[null,false,0,1.0,1000000.0,1e+16,1e-05,-0.0,NaN,Infinity,-Infinity]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := EncodeRecord(tc.payload)
			requireCode(t, err, "")
			if string(raw) != tc.expected+"\n" {
				t.Fatalf("got %s, want %s", raw, tc.expected)
			}
		})
	}
	for _, tc := range []struct {
		chars int
		code  string
	}{{MaxRecordBytes - 8, ""}, {MaxRecordBytes - 7, "SIDECAR_OUTPUT_TOO_LARGE"}} {
		raw, err := EncodeRecord(map[string]any{"x": strings.Repeat("x", tc.chars)})
		requireCode(t, err, tc.code)
		if tc.code == "" && len(raw) != MaxRecordBytes+1 {
			t.Fatal("bad exact output length")
		}
	}
	_, err := EncodeRecord(map[string]any{"x": strings.Repeat("é", 1365)})
	requireCode(t, err, "SIDECAR_OUTPUT_TOO_LARGE")
	_, err = EncodeRecord(map[string]any{"x": func() {}})
	requireCode(t, err, "SIDECAR_OUTPUT_INVALID")
	_, err = EncodeRecord(map[string]any{"x": json.Number("false")})
	requireCode(t, err, "SIDECAR_OUTPUT_INVALID")
	_, err = EncodeRecord(map[string]any{"x": json.Number("1 ")})
	requireCode(t, err, "SIDECAR_OUTPUT_INVALID")
	_, err = EncodeRecord(map[string]any{"x": "\xff"})
	requireUnsupported(t, err, "non-scalar Unicode output")
}

type flushWriter struct {
	bytes.Buffer
	flushed  bool
	flushErr error
}

func (w *flushWriter) Flush() error { w.flushed = true; return w.flushErr }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

type brokenPipe struct{ err error }

func (p brokenPipe) Read([]byte) (int, error)  { return 0, p.err }
func (p brokenPipe) Write([]byte) (int, error) { return 0, p.err }

func TestWriteFlushAndIOErrors(t *testing.T) {
	w := &flushWriter{}
	requireCode(t, WriteRecord(w, HealthRecord("fixture")), "")
	if !w.flushed || !strings.HasSuffix(w.String(), "\n") {
		t.Fatal("not written and flushed")
	}
	w = &flushWriter{}
	requireCode(t, WriteRecord(w, map[string]any{"x": func() {}}), "SIDECAR_OUTPUT_INVALID")
	if w.Len() != 0 || w.flushed {
		t.Fatal("invalid output touched writer")
	}
	if err := WriteRecord(shortWriter{}, HealthRecord("fixture")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	sentinel := errors.New("synthetic adapter failure")
	_, err := ReadRecord(brokenPipe{sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err = WriteRecord(brokenPipe{sentinel}, HealthRecord("fixture")); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	w = &flushWriter{flushErr: sentinel}
	if err = WriteRecord(w, HealthRecord("fixture")); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}
