package jobcard

import (
	"context"
	"crypto/sha256"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/runtimepaths"
)

// Source must retain a confined filesystem root and reject links, aliases,
// directories and special files. runtimepaths.Root provides that boundary.
// The interface permits deterministic I/O faults without touching private data.
type Source interface {
	Check(string) *runtimepaths.Error
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}

// Loaded is an internal fresh read, not a persisted cache hit or a public DTO.
// RawText follows Python text-mode universal-newline decoding. FileSHA256 keeps
// exact bytes separately for race detection; neither hash proves digest quotes.
type Loaded struct {
	Card                          cataloginput.Value
	Report                        *Report
	RawText                       string
	RawPresent                    bool
	CardFileSHA256, RawFileSHA256 [32]byte
}

type observation struct {
	path    string
	limit   int64
	present bool
	hash    [32]byte
}
type reader struct {
	ctx          context.Context
	source       Source
	observations []observation
}

func stopped(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return &Error{Code: "JOB_CARD_CANCELED"}
	}
	return nil
}
func (r *reader) read(path string, limit int64) ([]byte, bool, *Error) {
	if err := stopped(r.ctx); err != nil {
		return nil, false, err
	}
	if r.source.Check(path) != nil {
		return nil, false, &Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
	}
	if err := stopped(r.ctx); err != nil {
		return nil, false, err
	}
	raw, present, err := r.source.ReadFile(path, limit)
	if canceled := stopped(r.ctx); canceled != nil {
		return nil, false, canceled
	}
	if err != nil {
		if err.Code == "FILE_SIZE_LIMIT" {
			return nil, false, &Error{Code: "JOB_CARD_INPUT_LIMIT"}
		}
		return nil, false, &Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
	}
	if int64(len(raw)) > limit {
		return nil, false, &Error{Code: "JOB_CARD_INPUT_LIMIT"}
	}
	if !present && len(raw) != 0 {
		return nil, false, &Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
	}
	return raw, present, nil
}
func (r *reader) observe(path string, limit int64) ([]byte, bool, *Error) {
	raw, present, err := r.read(path, limit)
	if err != nil {
		return nil, false, err
	}
	r.observations = append(r.observations, observation{path, limit, present, sha256.Sum256(raw)})
	return raw, present, nil
}
func (r *reader) recheck() *Error {
	for _, old := range r.observations {
		raw, present, err := r.read(old.path, old.limit)
		if err != nil {
			return err
		}
		if present != old.present || sha256.Sum256(raw) != old.hash {
			return &Error{Code: "JOB_CARD_INPUT_CHANGED"}
		}
	}
	return stopped(r.ctx)
}

// Read inspects one normalized card and only its canonical incoming-JD file.
// Initial/final presence and byte hashes detect observed changes, not a coherent
// multi-file filesystem transaction. A writer still needs ownership/revisions.
// All limits, cancellation, decoding and I/O failures discard the whole result.
func Read(ctx context.Context, source Source, relative string) (*Loaded, *Error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	path, valid := managedPath(relative, "normalized")
	if !valid || !(strings.HasSuffix(strings.ToLower(path), ".yaml") || strings.HasSuffix(strings.ToLower(path), ".yml")) {
		return nil, &Error{Code: "JOB_CARD_PATH_INVALID"}
	}
	if source == nil {
		return nil, &Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
	}
	r := reader{ctx: ctx, source: source}
	raw, present, err := r.observe(path, cataloginput.YAMLMaxInputBytes)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, &Error{Code: "JOB_CARD_NOT_FOUND"}
	}
	card, parseErr := cataloginput.ParseYAML(raw)
	if canceled := stopped(ctx); canceled != nil {
		return nil, canceled
	}
	if parseErr != nil {
		return nil, &Error{Code: "JOB_CARD_DECODE_FAILED"}
	}
	loaded := &Loaded{Card: card, CardFileSHA256: sha256.Sum256(raw)}
	report, failure := Validate(ctx, card, func(ctx context.Context, path string) (bool, *Error) {
		raw, present, err := r.observe(path, applicationanalysis.MaxSourceBytes)
		if err != nil {
			return false, err
		}
		if !utf8.Valid(raw) {
			return false, &Error{Code: "JOB_CARD_SOURCE_ENCODING"}
		}
		loaded.RawFileSHA256 = sha256.Sum256(raw)
		loaded.RawPresent = present
		loaded.RawText = strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
		return present, nil
	})
	if failure != nil {
		return nil, failure
	}
	if err := r.recheck(); err != nil {
		return nil, err
	}
	loaded.Report = report
	return loaded, nil
}
