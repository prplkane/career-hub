package catalogwrite

import (
	"testing"

	"careerhub.local/backend/internal/runtimepaths"
)

type exactFakeSource struct {
	raw    []byte
	limits []int64
}

func (*exactFakeSource) Check(string) *runtimepaths.Error { return nil }
func (s *exactFakeSource) ReadFile(_ string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	s.limits = append(s.limits, limit)
	return s.raw, true, nil
}
func TestExactWitnessKeepsFirstReadAndLimits(t *testing.T) {
	source := &exactFakeSource{raw: []byte("first")}
	r := &exactSourceReads{source, map[string]exactObservation{}}
	if _, _, e := r.ReadFile("jobs/incoming/one.txt", 4194304); e != nil {
		t.Fatal(e)
	}
	source.raw[0] = 'x'
	if _, _, e := r.ReadFile("jobs/incoming/one.txt", 4194304); e == nil {
		t.Fatal("repeat read replaced original witness")
	}
	if r.unchanged() {
		t.Fatal("changed source accepted")
	}
	if string(r.reads["jobs/incoming/one.txt"].raw) != "first" || source.limits[len(source.limits)-1] != 4194304 {
		t.Fatal("witness bytes or read bound lost")
	}
}
