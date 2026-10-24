//go:build windows

package startup

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
)

func TestRestoreRecoveryInspection(t *testing.T) {
	for _, mode := range []string{"restore", "mixed", "none", "channel"} {
		t.Run(mode, func(t *testing.T) {
			start := fixture(t)
			marker, p := datahome.MarkerBytes("dev", "dh_0123456789abcdef0123456789abcdef")
			if p != nil {
				t.Fatal(p)
			}
			writeFixture(t, start.DataRoot, datahome.MarkerPath, string(marker))
			if mode != "none" {
				writeFixture(t, start.DataRoot, datahome.RestoreRecoveryPath, "opaque classification is not replay authority")
			}
			if mode == "mixed" {
				writeFixture(t, start.DataRoot, datahome.NewJobRecoveryPath, "other intent")
			}
			before := snapshot(t, start.DataRoot)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRestoreRecoveryInspectionHelper$")
			cmd.Env = append(os.Environ(), "CH_RESTORE_INSPECT_HOME="+start.DataRoot, "CH_RESTORE_INSPECT_RES="+start.ResourceRoot, "CH_RESTORE_INSPECT_MODE="+mode)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, output)
			}
			if !reflect.DeepEqual(before, snapshot(t, start.DataRoot)) {
				t.Fatal("inspection changed Home bytes")
			}
		})
	}
}

func TestRestoreRecoveryInspectionHelper(t *testing.T) {
	mode := os.Getenv("CH_RESTORE_INSPECT_MODE")
	if mode == "" {
		t.Skip("subprocess only")
	}
	start := protocol.StartRecord{DataRoot: os.Getenv("CH_RESTORE_INSPECT_HOME"), ResourceRoot: os.Getenv("CH_RESTORE_INSPECT_RES"), Channel: "dev"}
	if mode == "channel" {
		start.Channel = "stable"
	}
	kind, err := InspectRecoveryForProcess(context.Background(), start)
	if mode == "restore" {
		if err != nil || kind != datahome.RecoveryRestore {
			t.Fatal(kind, err)
		}
	} else if err == nil {
		t.Fatal("ambiguous or unadmitted classification accepted", kind)
	}
}
