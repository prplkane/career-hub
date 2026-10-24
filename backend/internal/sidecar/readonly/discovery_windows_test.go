//go:build windows

package readonly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
)

type discoveryServiceCase struct {
	Name, Fixture, Operation, Error string
	Arguments                       struct {
		discovery.PageOptions
		DiscoveryID string `json:"discovery_id"`
	}
	Expected json.RawMessage
}
type discoveryServiceRequest struct {
	Start          protocol.StartRecord
	Mode, WantCode string
	Cases          []discoveryServiceCase
}

func runDiscoveryService(t *testing.T, request discoveryServiceRequest) {
	t.Helper()
	raw, e := json.Marshal(request)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDiscoveryServiceHelper$")
	cmd.Env = append(os.Environ(), "CAREERHUB_DISCOVERY_SERVICE_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(raw)
	out, e := cmd.CombinedOutput()
	if e != nil || ctx.Err() != nil || cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		t.Fatalf("native discovery %s failed: %s", request.Mode, out[:min(len(out), 2200)])
	}
}

func TestNativeDiscoveryFailures(t *testing.T) {
	for _, test := range []struct{ mode, code string }{
		{"missing", "DISCOVERY_SCHEMA_PREPARATION_REQUIRED"}, {"schema0", "DISCOVERY_SCHEMA_PREPARATION_REQUIRED"},
		{"schema" + strconv.Itoa(schema.Version-1), "DISCOVERY_SCHEMA_PREPARATION_REQUIRED"}, {"schema" + strconv.Itoa(schema.Version+1), "DISCOVERY_SCHEMA_TOO_NEW"},
		{"malformed", "DISCOVERY_READ_FAILED"}, {"corrupt", "DISCOVERY_READ_FAILED"},
		{"unowned", "DATA_HOME_OWNERSHIP_REQUIRED"}, {"directory", "DISCOVERY_PATH_REJECTED"},
		{"hardlink", "DISCOVERY_PATH_REJECTED"}, {"aux-wal", "DISCOVERY_PATH_REJECTED"},
		{"aux-shm", "DISCOVERY_PATH_REJECTED"}, {"aux-journal", "DISCOVERY_PATH_REJECTED"},
		{"identity", "DATA_HOME_IDENTITY_CHANGED"}, {"channel", "DATA_HOME_CHANNEL_MISMATCH"},
		{"recovery", "DATA_HOME_RECOVERY_REQUIRED"}, {"resource", "SIDECAR_RESOURCE_INVALID"},
		{"canceled", "DISCOVERY_READ_CANCELED"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			base, start := fixture(t)
			path := filepath.Join(start.DataRoot, filepath.FromSlash(applicationDatabasePath))
			switch test.mode {
			case "missing":
			case "directory":
				if e := os.MkdirAll(path, 0700); e != nil {
					t.Fatal(e)
				}
			case "corrupt":
				writeFixture(t, start.DataRoot, applicationDatabasePath, "PRIVATE synthetic corrupt database")
			default:
				version := strconv.Itoa(schema.Version)
				if strings.HasPrefix(test.mode, "schema") {
					version = strings.TrimPrefix(test.mode, "schema")
				}
				writeApplicationSQL(t, start.DataRoot, "PRAGMA user_version="+version)
			}
			if test.mode == "hardlink" {
				if e := os.Link(path, filepath.Join(base, "synthetic-alias")); e != nil {
					t.Fatal(e)
				}
			}
			if strings.HasPrefix(test.mode, "aux-") {
				foreign := filepath.Join(base, "synthetic-auxiliary")
				if e := os.WriteFile(foreign, []byte("synthetic"), 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Link(foreign, path+"-"+strings.TrimPrefix(test.mode, "aux-")); e != nil {
					t.Fatal(e)
				}
			}
			before := tree(t, base)
			runDiscoveryService(t, discoveryServiceRequest{Start: start, Mode: test.mode, WantCode: test.code})
			if !slices.Contains([]string{"identity", "channel", "recovery", "resource"}, test.mode) && !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("failed service mutated files")
			}
		})
	}
}

func TestDiscoveryServiceHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_DISCOVERY_SERVICE_HELPER") != "1" {
		return
	}
	var request discoveryServiceRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 32<<20)).Decode(&request) != nil {
		t.Fatal("invalid synthetic request")
	}
	var w *Workspace
	var e error
	if request.Mode == "unowned" {
		w, e = Open(request.Start)
	} else {
		w, e = OpenForProcess(request.Start)
	}
	if e != nil {
		t.Fatal("fixture admission failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	switch request.Mode {
	case "identity":
		writeFixture(t, request.Start.DataRoot, datahome.MarkerPath, marker("dev", "dh_22222222222222222222222222222222"))
	case "channel":
		writeFixture(t, request.Start.DataRoot, datahome.MarkerPath, marker("stable", fixtureID))
	case "recovery":
		writeFixture(t, request.Start.DataRoot, datahome.RestoreRecoveryPath, "synthetic recovery")
	case "resource":
		writeFixture(t, request.Start.ResourceRoot, "VERSION", "invalid")
	case "canceled":
		cancel()
	}
	if request.Mode != "reference" {
		for _, op := range []string{"list", "count", "detail", "assessment"} {
			c := discoveryServiceCase{Operation: op}
			c.Arguments.DiscoveryID = "synthetic"
			request.Cases = append(request.Cases, c)
		}
	}
	for _, c := range request.Cases {
		before := tree(t, filepath.Dir(request.Start.DataRoot))
		var result any
		var e error
		switch c.Operation {
		case "list":
			options := c.Arguments.PageOptions
			if options.Limit == 0 && c.Name != "invalid-limit-0" {
				options.Limit = 50
			}
			if options.Sort == "" {
				options.Sort = "last_desc"
			}
			result, e = w.Discoveries(ctx, options)
		case "count":
			result, e = w.DiscoveryCount(ctx, c.Arguments.Filters)
		case "detail":
			result, e = w.Discovery(ctx, c.Arguments.DiscoveryID)
		case "assessment":
			result, e = w.DiscoveryAssessment(ctx, c.Arguments.DiscoveryID)
		default:
			t.Fatal("invalid synthetic operation")
		}
		if !reflect.DeepEqual(before, tree(t, filepath.Dir(request.Start.DataRoot))) {
			t.Fatal("native read modified files")
		}
		if e != nil && errors.Unwrap(e) != nil {
			t.Fatal("private underlying cause retained")
		}
		want := request.WantCode
		if c.Error != "" {
			want = "DISCOVERY_" + c.Error
		}
		raw, encodingError := json.Marshal(result)
		if encodingError != nil {
			t.Fatal(encodingError)
		}
		if want != "" {
			requireCode(t, e, want)
			if string(raw) != "null" && !(c.Operation == "count" && string(raw) == "0") {
				t.Fatal("failed read retained content")
			}
		} else {
			if e != nil {
				var problem *Error
				errors.As(e, &problem)
				t.Fatalf("native source case %s failed: %v", c.Name, problem.Code)
			}
			if !reflect.DeepEqual(nativeReadJSON(t, raw), nativeReadJSON(t, c.Expected)) {
				t.Fatalf("native source payload changed: %s", c.Name)
			}
		}
	}
}
