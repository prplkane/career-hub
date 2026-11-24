package vaultbundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationhelper"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/vault/vaultread"
)

func syntheticFiles() map[string][]byte {
	files := map[string][]byte{}
	for index, path := range Paths() {
		files[path] = []byte("synthetic: " + strings.Repeat("x", index) + "\n")
	}
	return files
}

func TestPathsMatchOwners(t *testing.T) {
	paths := Paths()
	want := append(vaultread.SetupPaths(), catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath, applicationhelper.AnswersPath)
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatal("bundle paths drifted from their owners", paths)
	}
	for index, path := range paths {
		if Optional(path) != (index == len(paths)-1) {
			t.Fatal("only saved short answers are optional", path)
		}
	}
	if Optional("career_vault/unknown.yaml") {
		t.Fatal("unknown path reported optional")
	}
	paths[0] = "changed"
	if Paths()[0] == "changed" {
		t.Fatal("caller changed the closed list")
	}
}

func TestRoundTripAndDeterminism(t *testing.T) {
	for _, withAnswers := range []bool{true, false} {
		files := syntheticFiles()
		if !withAnswers {
			delete(files, applicationhelper.AnswersPath)
		}
		first, problem := Encode(files)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		second, _ := Encode(files)
		if !bytes.Equal(first, second) {
			t.Fatal("encoding is not deterministic")
		}
		if !json.Valid(first) || !bytes.HasPrefix(first, []byte(`{"format":"career-hub-vault-bundle","format_version":1,"protection":"none","files":{"profile.yaml":`)) {
			t.Fatal("unexpected shape", string(first[:80]))
		}
		decoded, problem := Decode(first)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		if len(decoded) != len(files) {
			t.Fatal("file count changed", len(decoded))
		}
		for path, raw := range files {
			if !bytes.Equal(decoded[path], raw) {
				t.Fatal("content changed", path)
			}
		}
		_, hasAnswers := decoded[applicationhelper.AnswersPath]
		if hasAnswers != withAnswers {
			t.Fatal("optional answers presence changed")
		}
	}
}

func TestEncodeRefusals(t *testing.T) {
	files := syntheticFiles()
	files["career_vault/secrets.yaml"] = []byte("x")
	if _, problem := Encode(files); problem == nil || problem.Code != CodeInvalid {
		t.Fatal("unknown path encoded", problem)
	}
	files = syntheticFiles()
	delete(files, vaultread.SetupPaths()[0])
	if _, problem := Encode(files); problem == nil || problem.Code != CodeInvalid {
		t.Fatal("missing required file encoded", problem)
	}
	files = syntheticFiles()
	files[vaultread.SetupPaths()[1]] = make([]byte, cataloginput.YAMLMaxInputBytes+1)
	if _, problem := Encode(files); problem == nil || problem.Code != CodeLimit {
		t.Fatal("oversized file encoded", problem)
	}
	// Eleven files at the per-file limit exceed the whole-bundle limit; export
	// must refuse rather than write a file that import would refuse.
	files = map[string][]byte{}
	for _, path := range Paths() {
		files[path] = bytes.Repeat([]byte("a"), cataloginput.YAMLMaxInputBytes)
	}
	if _, problem := Encode(files); problem == nil || problem.Code != CodeLimit {
		t.Fatal("oversized bundle encoded", problem)
	}
}

func fileEntry(raw []byte) map[string]any {
	sum := sha256.Sum256(raw)
	return map[string]any{"size": len(raw), "sha256": hex.EncodeToString(sum[:]), "content": base64.StdEncoding.EncodeToString(raw)}
}

func validDocument() map[string]any {
	listed := map[string]any{}
	for _, item := range entries {
		listed[item.name] = fileEntry([]byte("synthetic\n"))
	}
	return map[string]any{"format": Format, "format_version": 1, "protection": "none", "files": listed}
}

func encodeDocument(t *testing.T, document map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecodeRejections(t *testing.T) {
	good := encodeDocument(t, validDocument())
	if _, problem := Decode(good); problem != nil {
		t.Fatal("valid document refused", problem.Code)
	}
	files := func(mutate func(map[string]any)) []byte {
		document := validDocument()
		mutate(document["files"].(map[string]any))
		return encodeDocument(t, document)
	}
	top := func(mutate func(map[string]any)) []byte {
		document := validDocument()
		mutate(document)
		return encodeDocument(t, document)
	}
	profile := func(mutate func(map[string]any)) []byte {
		return files(func(listed map[string]any) { mutate(listed["profile.yaml"].(map[string]any)) })
	}
	cases := []struct {
		name string
		raw  []byte
		code string
	}{
		{"empty", nil, CodeInvalid},
		{"array", []byte(`[]`), CodeInvalid},
		{"not json", []byte(`{"format":`), CodeInvalid},
		{"trailing value", append(bytes.Clone(good), []byte(` {}`)...), CodeInvalid},
		{"invalid utf8", append([]byte(`{"format":"`), 0xff, '"', '}'), CodeInvalid},
		{"duplicate top key", []byte(strings.Replace(string(good), `"protection":"none"`, `"protection":"none","protection":"none"`, 1)), CodeInvalid},
		{"case-folded key", []byte(strings.Replace(string(good), `"protection"`, `"Protection"`, 1)), CodeInvalid},
		{"duplicate file", []byte(strings.Replace(string(good), `"education.yaml":`, `"profile.yaml":{"size":0,"sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","content":""},"education.yaml":`, 1)), CodeInvalid},
		{"unknown top key", top(func(d map[string]any) { d["extra"] = true }), CodeInvalid},
		{"missing files", top(func(d map[string]any) { delete(d, "files") }), CodeInvalid},
		{"files not object", top(func(d map[string]any) { d["files"] = []any{} }), CodeInvalid},
		{"wrong format", top(func(d map[string]any) { d["format"] = "zip" }), CodeInvalid},
		{"missing format", top(func(d map[string]any) { delete(d, "format") }), CodeInvalid},
		{"format version string", top(func(d map[string]any) { d["format_version"] = "1" }), CodeInvalid},
		{"format version fraction", top(func(d map[string]any) { d["format_version"] = 1.5 }), CodeInvalid},
		{"newer version", top(func(d map[string]any) { d["format_version"] = 2 }), CodeVersionUnsupported},
		{"protected", top(func(d map[string]any) { d["protection"] = "password-v1"; d["payload"] = "opaque"; delete(d, "files") }), CodeProtectionUnsupported},
		{"protection null", top(func(d map[string]any) { d["protection"] = nil }), CodeInvalid},
		{"protection number", top(func(d map[string]any) { d["protection"] = 0 }), CodeInvalid},
		{"missing protection", top(func(d map[string]any) { delete(d, "protection") }), CodeInvalid},
		{"unknown file name", files(func(l map[string]any) { l["../secrets.yaml"] = fileEntry([]byte("x")) }), CodeInvalid},
		{"path as name", files(func(l map[string]any) { l["career_vault/profile.yaml"] = l["profile.yaml"]; delete(l, "profile.yaml") }), CodeInvalid},
		{"missing required file", files(func(l map[string]any) { delete(l, "taxonomy/technologies.yaml") }), CodeInvalid},
		{"entry extra key", profile(func(e map[string]any) { e["path"] = "x" }), CodeInvalid},
		{"entry missing key", profile(func(e map[string]any) { delete(e, "sha256") }), CodeInvalid},
		{"size string", profile(func(e map[string]any) { e["size"] = "10" }), CodeInvalid},
		{"size negative", profile(func(e map[string]any) { e["size"] = -1 }), CodeInvalid},
		{"size fraction", profile(func(e map[string]any) { e["size"] = 10.0001 }), CodeInvalid},
		{"digest uppercase", profile(func(e map[string]any) { e["sha256"] = strings.ToUpper(e["sha256"].(string)) }), CodeInvalid},
		{"content null", profile(func(e map[string]any) { e["content"] = nil }), CodeInvalid},
		{"content not base64", profile(func(e map[string]any) { e["content"] = "!!!!" }), CodeInvalid},
		{"content with newline", profile(func(e map[string]any) { e["content"] = "c3lu\ndGhldGljCg==" }), CodeInvalid},
		{"size mismatch", profile(func(e map[string]any) { e["size"] = 3 }), CodeDamaged},
		{"hash mismatch", profile(func(e map[string]any) { e["sha256"] = strings.Repeat("0", 64) }), CodeDamaged},
		{"declared size over limit", profile(func(e map[string]any) { e["size"] = cataloginput.YAMLMaxInputBytes + 1 }), CodeLimit},
		{"content over limit", profile(func(e map[string]any) {
			e["content"] = base64.StdEncoding.EncodeToString(make([]byte, cataloginput.YAMLMaxInputBytes+3))
		}), CodeLimit},
		{"bundle over limit", append(bytes.Clone(good), bytes.Repeat([]byte(" "), MaxBundleBytes)...), CodeLimit},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out, problem := Decode(test.raw)
			if problem == nil || problem.Code != test.code || out != nil {
				t.Fatalf("got %v, want %s", problem, test.code)
			}
		})
	}
}

func TestDecodeAcceptsLargestImportableBundle(t *testing.T) {
	// The largest bundle Encode can produce must decode; export never writes a
	// file that import refuses.
	files := map[string][]byte{}
	budget := MaxBundleBytes / 4 * 3
	for _, path := range Paths() {
		size := budget / len(Paths()) / 2
		files[path] = bytes.Repeat([]byte("b"), size)
	}
	raw, problem := Encode(files)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	if _, problem := Decode(raw); problem != nil {
		t.Fatal(problem.Code)
	}
}

func TestFileName(t *testing.T) {
	when := time.Date(2026, 11, 18, 23, 59, 0, 0, time.UTC)
	if got := FileName(when); got != "career-vault-export-2026-11-18.chvault" {
		t.Fatal(got)
	}
}

func TestErrorTextIsCodeOnly(t *testing.T) {
	_, problem := Decode([]byte(`{"format":"Synthetic Person"}`))
	if problem == nil || problem.Error() != CodeInvalid || strings.Contains(problem.Error(), "Synthetic") {
		t.Fatal("error leaked input", problem)
	}
}
