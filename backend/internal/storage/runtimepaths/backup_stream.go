package runtimepaths

import (
	"context"
	"crypto/sha256"
	"hash"
	"io"
	"io/fs"
	"os"
	"sync"
)

type FilePublicationOutcome string

const (
	FileNotPublished       FilePublicationOutcome = "not_published"
	FilePublished          FilePublicationOutcome = "published"
	FilePublicationUnknown FilePublicationOutcome = "unknown"
)

type StreamPublication struct {
	Outcome          FilePublicationOutcome
	CleanupConfirmed bool
}

// CreateBackupStream writes one bounded, verified new file in existing managed
// parents. It never overwrites a destination. Producer/verifier errors are fixed
// stage codes, not potentially private callback/native error text. A published
// or unknown result with an error must not be retried as an uncommitted write.
// Callbacks run without Root's mutex and may read source leases on the same Root.
func (r *Root) CreateBackupStream(ctx context.Context, relative string, limit int64, produce func(io.Writer) error, verify func(io.ReaderAt, int64) error) (StreamPublication, *Error) {
	if !backupReadSupported {
		return StreamPublication{FileNotPublished, true}, failure("BACKUP_STREAM_UNSUPPORTED")
	}
	if r == nil {
		return StreamPublication{FileNotPublished, true}, failure("ROOT_CLOSED")
	}
	if problem := r.RequireProcessOwnership(); problem != nil {
		return StreamPublication{FileNotPublished, true}, problem
	}
	return r.createBackupStream(ctx, relative, limit, produce, verify, backupStreamOperations{})
}

type backupStreamOperations struct {
	write         func(*os.File, []byte) (int, error)
	sync          func(*os.File) error
	close         func(*os.File) error
	rename        func(*os.File, *os.Root, string) error
	beforePublish func(*os.Root, string, string)
}

type backupStage struct {
	path        *checkedPath
	directories []*os.File
	file        *os.File
	name        string
	identity    fs.FileInfo
}

func (r *Root) createBackupStream(ctx context.Context, relative string, limit int64, produce func(io.Writer) error, verify func(io.ReaderAt, int64) error, ops backupStreamOperations) (result StreamPublication, problem *Error) {
	result = StreamPublication{FileNotPublished, true}
	if !backupReadSupported {
		return result, failure("BACKUP_STREAM_UNSUPPORTED")
	}
	if ctx == nil || ctx.Err() != nil {
		return result, failure("BACKUP_STREAM_CANCELED")
	}
	if limit < 0 || limit > MaxBackupReadBytes || produce == nil || verify == nil {
		return result, failure("WRITE_LIMIT_INVALID")
	}
	stage, stageAttempted, problem := r.newBackupStage(relative)
	if problem != nil {
		result.CleanupConfirmed = !stageAttempted
		return result, problem
	}
	result.CleanupConfirmed = false
	if ops.write == nil {
		ops.write = (*os.File).Write
	}
	if ops.sync == nil {
		ops.sync = (*os.File).Sync
	}
	if ops.close == nil {
		ops.close = (*os.File).Close
	}
	if ops.rename == nil {
		ops.rename = renameBackupFile
	}
	var publicationFile *os.File
	defer func() {
		if publicationFile != nil {
			if err := ops.close(publicationFile); err != nil && problem == nil {
				problem = failure("FILE_CLOSE")
			}
		}
		if stage.file != nil {
			if err := ops.close(stage.file); err != nil && problem == nil {
				problem = failure("FILE_CLOSE")
			}
		}
		// An unknown rename must keep any remaining stage/evidence for inspection.
		if result.Outcome != FilePublicationUnknown {
			if e := cleanupCreationStage(stage.path.parent, stage.name, stage.identity); e != nil {
				if problem == nil {
					problem = e
				}
			} else {
				result.CleanupConfirmed = true
			}
		}
		if e := stage.closeParents(); e != nil && problem == nil {
			problem = e
		}
	}()
	writer := &backupStreamWriter{ctx: ctx, file: stage.file, write: ops.write, limit: limit, hash: sha256.New(), active: true}
	defer writer.stop()
	producerError := produce(writer)
	size, digest, writeProblem := writer.stop()
	if writeProblem != nil {
		return result, writeProblem
	}
	if producerError != nil {
		return result, failure("BACKUP_PRODUCER_FAILED")
	}
	if ctx.Err() != nil {
		return result, failure("BACKUP_STREAM_CANCELED")
	}
	if err := ops.sync(stage.file); err != nil {
		return result, failure("FILE_SYNC")
	}
	file := stage.file
	stage.file = nil // Close consumes the handle on failure too.
	if err := ops.close(file); err != nil {
		return result, failure("FILE_CLOSE")
	}
	publicationFile, problem = openBackupPublication(stage.path.parent, stage.name, stage.identity)
	if problem != nil {
		return result, problem
	}
	before, err := publicationFile.Stat()
	if err != nil || before.Size() != size {
		return result, failure("FILE_STAGE")
	}
	reader := &backupStreamReader{ctx: ctx, file: publicationFile, size: size, active: true}
	defer reader.stop()
	h := sha256.New()
	read, err := io.CopyBuffer(h, io.NewSectionReader(reader, 0, size), make([]byte, 64<<10))
	if ctx.Err() != nil {
		return result, failure("BACKUP_STREAM_CANCELED")
	}
	if err != nil || read != size || string(h.Sum(nil)) != string(digest) {
		return result, failure("FILE_STAGE")
	}
	verifyError := verify(reader, size)
	reader.stop()
	if verifyError != nil {
		return result, failure("BACKUP_VERIFIER_FAILED")
	}
	if ctx.Err() != nil {
		return result, failure("BACKUP_STREAM_CANCELED")
	}
	after, err := publicationFile.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return result, failure("PATH_CHANGED")
	}
	if problem = replacementLinkCheck(publicationFile, after); problem != nil {
		return result, problem
	}
	if ops.beforePublish != nil {
		ops.beforePublish(stage.path.parent, stage.name, stage.path.name)
	}
	// No callbacks under this lock. Root.Close cannot split final admission and
	// publication; native no-replace still arbitrates independent destination writes.
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return result, failure("ROOT_CLOSED")
	}
	if problem = checkBindings(appendBindings(r.bindings, stage.path.bindings)); problem != nil {
		return result, problem
	}
	if _, present, e := portableChild(stage.path.parent, stage.path.name); e != nil {
		return result, e
	} else if present {
		return result, failure("FILE_EXISTS")
	}
	if ctx.Err() != nil {
		return result, failure("BACKUP_STREAM_CANCELED")
	}
	renameError := ops.rename(publicationFile, stage.path.parent, stage.path.name)
	if renameError != nil {
		result.Outcome = observeBackupPublication(stage)
		return result, failure("FILE_PUBLISH")
	}
	result.Outcome = FilePublished
	current, err := stage.path.parent.Lstat(stage.path.name)
	if err != nil || isLinkLike(current) || !current.Mode().IsRegular() || !os.SameFile(stage.identity, current) {
		return result, failure("PATH_CHANGED")
	}
	if problem = replacementLinkCheck(publicationFile, current); problem != nil {
		return result, problem
	}
	observed, present, e := portableChild(stage.path.parent, stage.path.name)
	if e != nil {
		return result, e
	}
	if !present || observed != stage.path.name {
		return result, failure("PATH_CHANGED")
	}
	if e := checkBindings(appendBindings(r.bindings, stage.path.bindings)); e != nil {
		return result, e
	}
	if ctx.Err() != nil {
		return result, failure("BACKUP_STREAM_CANCELED")
	}
	return result, nil
}

func observeBackupPublication(stage *backupStage) FilePublicationOutcome {
	if info, err := stage.path.parent.Lstat(stage.path.name); err == nil && !isLinkLike(info) && info.Mode().IsRegular() && os.SameFile(stage.identity, info) {
		return FilePublished
	}
	if info, err := stage.path.parent.Lstat(stage.name); err == nil && !isLinkLike(info) && info.Mode().IsRegular() && os.SameFile(stage.identity, info) {
		return FileNotPublished
	}
	return FilePublicationUnknown
}

func (s *backupStage) closeParents() *Error {
	var problem *Error
	for i := len(s.directories) - 1; i >= 0; i-- {
		if s.directories[i].Close() != nil {
			problem = failure("ROOT_CLOSE")
		}
	}
	if s.path != nil {
		if e := s.path.close(); e != nil {
			problem = e
		}
	}
	return problem
}

type backupStreamWriter struct {
	mu          sync.Mutex
	ctx         context.Context
	file        *os.File
	write       func(*os.File, []byte) (int, error)
	limit, size int64
	hash        hash.Hash
	active      bool
	problem     *Error
}

func (w *backupStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		return 0, failure("BACKUP_STREAM_CLOSED")
	}
	if w.problem != nil {
		return 0, w.problem
	}
	if int64(len(p)) > w.limit-w.size {
		w.problem = failure("FILE_SIZE_LIMIT")
		return 0, w.problem
	}
	written := 0
	for len(p) > 0 {
		if w.ctx.Err() != nil {
			w.problem = failure("BACKUP_STREAM_CANCELED")
			return written, w.problem
		}
		chunk := p
		if len(chunk) > MaxBackupReadChunk {
			chunk = chunk[:MaxBackupReadChunk]
		}
		n, err := w.write(w.file, chunk)
		if n > 0 {
			_, _ = w.hash.Write(chunk[:n])
			w.size += int64(n)
			written += n
			p = p[n:]
		}
		if err != nil || n != len(chunk) {
			w.problem = failure("FILE_WRITE")
			return written, w.problem
		}
	}
	return written, nil
}

func (w *backupStreamWriter) stop() (int64, []byte, *Error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.active = false
	return w.size, w.hash.Sum(nil), w.problem
}

type backupStreamReader struct {
	mu     sync.Mutex
	ctx    context.Context
	file   *os.File
	size   int64
	active bool
}

func (r *backupStreamReader) ReadAt(p []byte, offset int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return 0, failure("BACKUP_STREAM_CLOSED")
	}
	if r.ctx.Err() != nil {
		return 0, failure("BACKUP_STREAM_CANCELED")
	}
	if offset < 0 || len(p) > MaxBackupReadChunk {
		return 0, failure("READ_LIMIT_INVALID")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.size {
		return 0, io.EOF
	}
	n, err := r.file.ReadAt(p, offset)
	if r.ctx.Err() != nil {
		clear(p[:n])
		return 0, failure("BACKUP_STREAM_CANCELED")
	}
	if err != nil && err != io.EOF {
		clear(p[:n])
		return 0, failure("FILE_READ")
	}
	return n, err
}
func (r *backupStreamReader) stop() { r.mu.Lock(); r.active = false; r.mu.Unlock() }
