package applicationhelper

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

// Request selects one existing first-party operation; never a file, SQL query or
// source document. HTTP/extension authorization remains a separate adapter gate.
type Request struct {
	Operation string
	Fields    map[string]any
}
type Outcome struct {
	Result                   json.RawMessage
	Committed, CleanupFailed bool
}
type Service struct {
	root       string
	gate       chan struct{}
	read       func(*runtimepaths.Root, string, int64) ([]byte, bool, *runtimepaths.Error)
	create     func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
	replace    func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error)
	close      func(*runtimepaths.Root) error
	now        func() time.Time
	id         func() (string, error)
	checkpoint func(string) *Error // private, per-instance fault seam
}

func New(dataRoot string) *Service {
	return &Service{root: dataRoot, gate: make(chan struct{}, 1), read: (*runtimepaths.Root).ReadSingleLinkFile,
		create: (*runtimepaths.Root).CreateFile, replace: (*runtimepaths.Root).ReplaceFile, close: (*runtimepaths.Root).Close,
		now: time.Now, id: func() (string, error) {
			var id [16]byte
			_, e := rand.Read(id[:])
			id[6] = (id[6] & 0x0f) | 0x40
			id[8] = (id[8] & 0x3f) | 0x80
			return fmt.Sprintf("answer_%x", id), e
		}}
}

type observed struct {
	raw     []byte
	present bool
}
type reads struct {
	service *Service
	root    *runtimepaths.Root
	files   map[string]observed
	bytes   int
}

func (r *reads) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	raw, present, e := r.service.read(r.root, path, limit)
	if e != nil {
		return nil, false, e
	}
	if before, ok := r.files[path]; ok {
		if before.present != present || !bytes.Equal(before.raw, raw) {
			return nil, false, &runtimepaths.Error{Code: "PATH_CHANGED"}
		}
	} else {
		if len(r.files) >= 16 || len(raw) > (32<<20)-r.bytes {
			return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
		}
		r.files[path] = observed{bytes.Clone(raw), present}
		r.bytes += len(raw)
	}
	return raw, present, nil
}
func (r *reads) unchanged() bool {
	paths := make([]string, 0, len(r.files))
	for path := range r.files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		before := r.files[path]
		raw, present, e := r.service.read(r.root, path, MaxInputBytes)
		if e != nil || before.present != present || !bytes.Equal(before.raw, raw) {
			return false
		}
	}
	return true
}
func ready(root *runtimepaths.Root, expected *datahome.Info) (*datahome.Info, *Error) {
	info, e := datahome.InspectOwnedForRegistryRecovery(root, nil)
	if e != nil {
		return nil, failure("admission", e.Code)
	}
	pending, pathErr := root.Exists(datahome.RegistryRecoveryPath)
	if pathErr != nil || pending {
		return nil, failure("admission", "APPLICATION_HELPER_RECOVERY_REQUIRED")
	}
	if expected != nil && *info != *expected {
		return nil, failure("admission", "APPLICATION_HELPER_HOME_CHANGED")
	}
	return info, nil
}
func validateRequest(r Request) *Error {
	var allowed []string
	switch r.Operation {
	case "configuration.load", "demographics.load", "answers.load", "projection":
	case "configuration.save":
		allowed = []string{"expected_revision", "enabled_fields"}
	case "demographics.save":
		allowed = []string{"expected_revision", "answers"}
	case "answers.save":
		allowed = []string{"expected_revision", "question", "value_kind", "value"}
	case "answers.delete":
		allowed = []string{"expected_revision", "answer_id"}
	default:
		return failure("request", "APPLICATION_HELPER_OPERATION_INVALID")
	}
	if len(r.Fields) > len(allowed) {
		return failure("request", "APPLICATION_HELPER_REQUEST_INVALID")
	}
	for k := range r.Fields {
		if !slices.Contains(allowed, k) {
			return failure("request", "APPLICATION_HELPER_REQUEST_INVALID")
		}
	}
	return nil
}
func (s *Service) point(stage string) *Error {
	if s.checkpoint != nil {
		return s.checkpoint(stage)
	}
	return nil
}

// Run requires a previously initialized, exclusively owned home. It does not
// claim/bootstrap one, migrate a schema or run a background worker. A bounded
// synchronous operation owns the process gate through verification and cleanup.
func (s *Service) Run(ctx context.Context, request Request) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if e := validateRequest(request); e != nil {
		return out, e
	}
	if s == nil || s.gate == nil || s.read == nil || s.create == nil || s.replace == nil || s.close == nil || s.now == nil || s.id == nil {
		return out, failure("configuration", "APPLICATION_HELPER_UNAVAILABLE")
	}
	if ctx == nil {
		return out, canceled()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return out, canceled()
	}
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, canceled()
	}
	defer release()
	if ctx.Err() != nil {
		return out, canceled()
	}
	root, pathErr := runtimepaths.Open(s.root)
	if pathErr != nil {
		return out, failure("path", pathErr.Code)
	}
	defer func() {
		if s.close(root) != nil {
			out.CleanupFailed = true
			if problem == nil {
				problem = failure("close", "APPLICATION_HELPER_CLOSE_FAILED")
			}
		}
		if problem != nil {
			if problem.Code == "APPLICATION_HELPER_CONFIGURATION_INVALID" &&
				(request.Operation == "answers.load" || request.Operation == "answers.save" || request.Operation == "answers.delete") {
				problem.Code = "APPLICATION_ANSWER_INVALID"
			}
			problem.Committed = out.Committed
			out.Result = nil
		}
	}()
	home, problem := ready(root, nil)
	if problem != nil {
		return out, problem
	}
	reader := &reads{s, root, map[string]observed{}, 0}
	proposal, problem := s.prepare(ctx, reader, request)
	if problem != nil {
		return out, problem
	}
	if problem = s.point("after_read"); problem != nil {
		return out, problem
	}
	if !reader.unchanged() {
		return out, failure("revision", "APPLICATION_HELPER_SOURCE_CHANGED")
	}
	if _, problem = ready(root, home); problem != nil {
		return out, problem
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	if proposal.path != "" {
		if problem = s.point("before_publish"); problem != nil {
			return out, problem
		}
		if ctx.Err() != nil {
			return out, canceled()
		}
		before := reader.files[proposal.path]
		if before.present {
			out.Committed, pathErr = s.replace(root, proposal.path, before.raw, proposal.raw)
		} else {
			out.Committed, pathErr = s.create(root, proposal.path, proposal.raw)
		}
		if pathErr != nil {
			out.CleanupFailed = pathErr.Code == "FILE_CLOSE" || pathErr.Code == "FILE_CLEANUP"
			return out, failure("save", pathErr.Code)
		}
		if !out.Committed {
			return out, failure("save", "APPLICATION_HELPER_SAVE_UNCONFIRMED")
		}
		reader.files[proposal.path] = observed{bytes.Clone(proposal.raw), true}
		if problem = s.point("after_publish"); problem != nil {
			return out, problem
		}
	}
	// Finish current-state verification even after a committed write is canceled.
	// A late read/cleanup failure never masquerades as an uncommitted operation.
	if !reader.unchanged() {
		return out, failure("readback", "APPLICATION_HELPER_SOURCE_CHANGED")
	}
	if _, problem = ready(root, home); problem != nil {
		return out, problem
	}
	if problem = s.point("before_return"); problem != nil {
		return out, problem
	}
	if !out.Committed && ctx.Err() != nil {
		return out, canceled()
	}
	out.Result = bytes.Clone(proposal.response)
	return out, nil
}

type prepared struct {
	path          string
	raw, response []byte
}

func readSetting(r *reads, path string) ([]byte, bool, *Error) {
	raw, present, e := r.ReadFile(path, MaxInputBytes)
	if e != nil {
		return nil, false, failure("read", e.Code)
	}
	return raw, present, nil
}
func (s *Service) prepare(ctx context.Context, r *reads, request Request) (*prepared, *Error) {
	if request.Operation == "projection" {
		payload, e := projection(ctx, r)
		if e != nil {
			return nil, e
		}
		raw, e := response(ctx, payload)
		return &prepared{response: raw}, e
	}
	path, class, conflictClass := ConfigurationPath, "ApplicationHelperError", "ApplicationHelperRevisionConflict"
	if request.Operation == "demographics.load" || request.Operation == "demographics.save" {
		path, class, conflictClass = DemographicsPath, "ApplicationDemographicsError", "ApplicationDemographicsRevisionConflict"
	}
	if request.Operation == "answers.load" || request.Operation == "answers.save" || request.Operation == "answers.delete" {
		path, conflictClass = AnswersPath, "ApplicationAnswerRevisionConflict"
	}
	mutating := request.Operation == "configuration.save" || request.Operation == "demographics.save" || request.Operation == "answers.save" || request.Operation == "answers.delete"
	expected := ""
	if mutating {
		var ok bool
		expected, ok = plain(request.Fields["expected_revision"], 256)
		if !ok || expected == "" {
			return nil, invalid(class)
		}
	}
	if request.Operation == "answers.delete" {
		id, ok := plain(request.Fields["answer_id"], 64)
		if !ok || !answerID.MatchString(id) {
			return nil, invalid(class)
		}
	}
	raw, present, e := readSetting(r, path)
	if e != nil {
		return nil, e
	}
	var content []byte
	var values any
	var enabled []string
	var demo map[string]string
	var answers []answer
	switch path {
	case ConfigurationPath:
		enabled, content, e = configuration(raw, present)
	case DemographicsPath:
		demo, content, e = demographicSettings(raw, present)
	case AnswersPath:
		answers, content, e = answerBank(raw, present)
	}
	if e != nil {
		return nil, e
	}
	if mutating && expected != revision(content) {
		return nil, conflict(conflictClass)
	}
	if mutating {
		switch path {
		case ConfigurationPath:
			enabled, e = normalizeFields(request.Fields["enabled_fields"])
			if e == nil {
				content = configurationBytes(enabled)
			}
		case DemographicsPath:
			input, ok := request.Fields["answers"].(map[string]any)
			if !ok {
				return nil, invalid(class)
			}
			demo, e = demographicAnswers(input)
			if e == nil {
				content = demographicsBytes(demo)
			}
		case AnswersPath:
			if request.Operation == "answers.delete" {
				id := request.Fields["answer_id"].(string)
				index := slices.IndexFunc(answers, func(a answer) bool { return a.ID == id })
				if index < 0 {
					return nil, invalid(class)
				}
				answers = slices.Delete(answers, index, index+1)
			} else {
				now := s.now().UTC()
				if now.Year() < 1 || now.Year() > 9999 {
					return nil, failure("clock", "APPLICATION_HELPER_CLOCK_INVALID")
				}
				answers, e = saveAnswer(answers, request.Fields, now.Format("2006-01-02T15:04:05Z"), s.id)
			}
			if e == nil {
				content, e = answerBytes(ctx, answers)
			}
		}
		if e != nil {
			return nil, e
		}
	}
	switch path {
	case ConfigurationPath:
		values = configurationPayload(enabled, content)
	case DemographicsPath:
		values = demographicsPayload(demo, content)
	case AnswersPath:
		values = answerPayload(answers, content)
	}
	result, e := response(ctx, values)
	if e != nil {
		return nil, e
	}
	proposal := &prepared{response: result}
	if mutating {
		proposal.path, proposal.raw = path, content
	}
	return proposal, nil
}
