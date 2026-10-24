//go:build windows

package applicationhelper

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
)

func textPointer(value string) *string { return &value }
func configCase(existing bool) oracleCase {
	c := oracleCase{Files: map[string]*string{}, Steps: []oracleStep{{Operation: "configuration.save", Fields: map[string]any{"enabled_fields": []any{"email"}}}}}
	if existing {
		c.Files[ConfigurationPath] = textPointer("# retained\nschema_version: 2\nenabled_fields: []\n")
	}
	return c
}
func noResult(out *Outcome) bool {
	return out != nil && (len(out.Result) == 0 || bytes.Equal(out.Result, []byte("null")))
}
func TestHelperNativeFailuresAndCrashes(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "create"
		if existing {
			name = "replace"
		}
		t.Run(name, func(t *testing.T) {
			for _, mode := range []string{"unowned", "pending", "canceled", "fail:after_read", "fail:before_publish", name + "-failure", "cancel:after_read", "cancel:before_publish", "drift:" + ConfigurationPath, "drift:" + datahome.MarkerPath, "intervening-create", "close-failure", "ambiguous-" + name, "fail:after_publish", "fail:before_return", "third-state"} {
				t.Run(mode, func(t *testing.T) {
					if existing && mode == "intervening-create" {
						t.Skip("separate create-only race")
					}
					c := configCase(existing)
					root := fixture(t, c)
					before := settingsFiles(t, root)
					outputs := native(t, nativeRequest{root, c, mode})
					got := outputs[0]
					committed := mode == "close-failure" || strings.HasPrefix(mode, "ambiguous-") || mode == "fail:after_publish" || mode == "fail:before_return" || mode == "third-state"
					if got.Error == nil || !noResult(got.Outcome) || got.Outcome.Committed != committed || got.Error.Committed != committed {
						t.Fatalf("wrong commit outcome: code=%v outcome=%+v", got.Error, got.Outcome)
					}
					if mode == "close-failure" && !got.Outcome.CleanupFailed {
						t.Fatal("cleanup not recorded")
					}
					if !committed && !strings.HasPrefix(mode, "drift:") && mode != "intervening-create" && !reflect.DeepEqual(before, got.Files) {
						t.Fatal("failed operation changed settings")
					}
					if committed && mode != "third-state" {
						if text := got.Files[ConfigurationPath]; text == nil || *text != string(configurationBytes([]string{"email"})) {
							t.Fatal("committed content missing")
						}
					}
					if mode == "third-state" || mode == "intervening-create" {
						if text := got.Files[ConfigurationPath]; text == nil || !strings.Contains(*text, "[phone]") {
							t.Fatal("overwrote intervening state")
						}
					}
				})
			}
			for _, stage := range []string{"after_read", "before_publish", "after_publish", "before_return"} {
				t.Run("crash-"+stage, func(t *testing.T) {
					c := configCase(existing)
					root := fixture(t, c)
					before := settingsFiles(t, root)
					crashNative(t, nativeRequest{root, c, "crash:" + stage})
					after := settingsFiles(t, root)
					if stage == "after_read" || stage == "before_publish" {
						if !reflect.DeepEqual(before, after) {
							t.Fatal("prepublication crash changed settings")
						}
					} else {
						if after[ConfigurationPath] == nil || *after[ConfigurationPath] != string(configurationBytes([]string{"email"})) {
							t.Fatal("committed file lost")
						}
					}
					read := oracleCase{Steps: []oracleStep{{Operation: "configuration.load"}}}
					outputs := native(t, nativeRequest{Root: root, Case: read})
					if outputs[0].Error != nil {
						t.Fatal("restart cannot read intact setting")
					}
				})
			}
			for _, stage := range []string{"after_publish", "before_return"} {
				t.Run("cancel-"+stage, func(t *testing.T) {
					c := configCase(existing)
					root := fixture(t, c)
					got := native(t, nativeRequest{root, c, "cancel:" + stage})[0]
					if got.Error != nil || !got.Outcome.Committed || noResult(got.Outcome) {
						t.Fatal("late cancellation hid successful commit")
					}
				})
			}
		})
	}
}
func TestHelperNativeReadPrivacyAndBounds(t *testing.T) {
	for _, path := range []string{ConfigurationPath, DemographicsPath, AnswersPath} {
		operation := map[string]string{ConfigurationPath: "configuration.load", DemographicsPath: "demographics.load", AnswersPath: "answers.load"}[path]
		for _, mode := range []string{"oversized", "invalid-utf8", "hardlink", "directory", "alias"} {
			t.Run(operation+"-"+mode, func(t *testing.T) {
				c := oracleCase{Files: map[string]*string{}, Steps: []oracleStep{{Operation: operation}}}
				root := fixture(t, c)
				full := filepath.Join(root, filepath.FromSlash(path))
				switch mode {
				case "oversized":
					writeFixture(t, root, path, bytes.Repeat([]byte("x"), MaxInputBytes+1))
				case "invalid-utf8":
					writeFixture(t, root, path, []byte{0xff})
				case "hardlink":
					target := filepath.Join(root, "synthetic-private")
					if e := os.WriteFile(target, []byte("private"), 0600); e != nil {
						t.Fatal(e)
					}
					if e := os.Link(target, full); e != nil {
						t.Fatal(e)
					}
				case "directory":
					if e := os.Mkdir(full, 0700); e != nil {
						t.Fatal(e)
					}
				case "alias":
					writeFixture(t, root, strings.ToUpper(path), []byte("private"))
				}
				// Bound tests inspect the public service result, not the helper's
				// snapshot serializer (which deliberately refuses directories/UTF8).
				c.Steps[0].Fields = map[string]any{}
				got := native(t, nativeRequest{root, c, "bounds"})[0]
				if got.Error == nil || !noResult(got.Outcome) || got.Outcome.Committed {
					t.Fatal("unsafe read returned payload")
				}
			})
		}
	}
}
