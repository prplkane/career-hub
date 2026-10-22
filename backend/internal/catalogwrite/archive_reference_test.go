package catalogwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/runtimepaths"
)

var archivePaths = map[string]string{
	"core_groups": catalogstore.CoreGroupsPath, "core_skills": catalogstore.CoreSkillsPath,
	"custom_groups": catalogstore.CustomGroupsPath, "custom_skills": catalogstore.CustomSkillsPath,
}

type archiveMemory map[string][]byte

func (m archiveMemory) Check(string) *runtimepaths.Error { return nil }
func (m archiveMemory) ReadFile(path string, _ int64) ([]byte, bool, *runtimepaths.Error) {
	raw, present := m[path]
	return bytes.Clone(raw), present, nil
}

type archiveOutput struct{ bytes.Buffer }

func (o *archiveOutput) Write(p []byte) (int, error) {
	if len(p) > (2<<20)-o.Len() {
		return 0, errors.New("synthetic archive output limit")
	}
	return o.Buffer.Write(p)
}

func normalizedJSON(t *testing.T, value any) any {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	var result any
	if json.Unmarshal(raw, &result) != nil {
		t.Fatal("invalid test value")
	}
	return result
}

func TestArchivePlanInvalidAndCancelled(t *testing.T) {
	if p, e := planArchive(nil, nil, "", true, ""); p != nil || e == nil {
		t.Fatal("invalid input accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state, e := catalogstore.ReadState(archiveMemory{}, archiveMemory{})
	if e != nil {
		t.Fatal(e)
	}
	if p, e := planArchive(ctx, state, "", true, ""); p != nil || e == nil || e.Stage != "cancelled" {
		t.Fatal("cancelled plan accepted")
	}
}
