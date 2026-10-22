//go:build windows

package runtimepaths

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

const backupRemovalSupported = true

func (r *Root) removeBackupEntry(ctx context.Context, relative string, expected *backupRemovalFile, ops backupRemovalOperations) (outcome BackupRemovalOutcome, problem *Error) {
	outcome = BackupNotRemoved
	if ctx == nil || ctx.Err() != nil {
		return outcome, failure("BACKUP_REMOVE_CANCELED")
	}
	if r == nil {
		return outcome, failure("ROOT_CLOSED")
	}
	if p := r.RequireProcessOwnership(); p != nil {
		return outcome, p
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return outcome, failure("ROOT_CLOSED")
	}
	checked, p := r.walkEntry(relative, expected == nil)
	if checked != nil {
		defer func() {
			if e := checked.close(); e != nil {
				problem = e
			}
		}()
	}
	if p != nil {
		return outcome, p
	}
	if !checked.present {
		return outcome, failure("FILE_MISSING")
	}
	if expected == nil && !checked.info.IsDir() || expected != nil && !checked.info.Mode().IsRegular() {
		return outcome, failure("BACKUP_REMOVE_TYPE_INVALID")
	}
	if expected != nil && checked.info.Size() != expected.size {
		return outcome, failure("BACKUP_REMOVE_CONTENT_CHANGED")
	}
	var pins []*os.File
	defer func() {
		for i := len(pins) - 1; i >= 0; i-- {
			if pins[i].Close() != nil {
				problem = failure("ROOT_CLOSE")
			}
		}
	}()
	for _, parent := range append(append([]*os.Root{}, r.handles...), checked.handles...) {
		pin, p := pinOwnershipRoot(parent)
		if p != nil {
			return outcome, p
		}
		pins = append(pins, pin)
		before, err := parent.Stat(".")
		after, pinErr := pin.Stat()
		if err != nil || pinErr != nil || isLinkLike(after) || !after.IsDir() || !os.SameFile(before, after) {
			return outcome, failure("PATH_CHANGED")
		}
	}
	if p := checkLocalVolume(pins[len(pins)-1]); p != nil {
		return outcome, p
	}
	var owned *os.File
	if expected == nil {
		owned, p = openOwnedMoveDirectory(checked.parent, checked.name, checked.info)
	} else {
		owned, p = openOwnedCreation(checked.parent, checked.name, checked.info)
	}
	if p != nil {
		return outcome, p
	}
	defer func() {
		if owned != nil && owned.Close() != nil {
			problem = failure("FILE_CLOSE")
		}
	}()
	if expected == nil {
		names, err := owned.Readdirnames(1)
		if len(names) != 0 || err != io.EOF {
			return outcome, failure("BACKUP_REMOVE_DIRECTORY_NOT_EMPTY")
		}
	} else {
		hash := sha256.New()
		buffer := make([]byte, 64<<10)
		for offset := int64(0); offset < expected.size; {
			if ctx.Err() != nil {
				return outcome, failure("BACKUP_REMOVE_CANCELED")
			}
			chunk := buffer[:min(int64(len(buffer)), expected.size-offset)]
			n, err := owned.ReadAt(chunk, offset)
			if n != len(chunk) || err != nil && err != io.EOF {
				return outcome, failure("FILE_READ")
			}
			hash.Write(chunk)
			offset += int64(n)
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected.digest {
			return outcome, failure("BACKUP_REMOVE_CONTENT_CHANGED")
		}
	}
	if ops.beforeDelete != nil {
		if p := ops.beforeDelete(); p != nil {
			return outcome, p
		}
	}
	// The opened DELETE handle forbids replacing this leaf. Parent pins and
	// bindings are independent: no absolute-path removal or re-open after hashing.
	info, err := owned.Stat()
	if err != nil || isLinkLike(info) || !os.SameFile(checked.info, info) || info.IsDir() != (expected == nil) ||
		(expected != nil && (info.Size() != expected.size || !info.ModTime().Equal(checked.info.ModTime()))) {
		return outcome, failure("PATH_CHANGED")
	}
	if expected != nil {
		if p := replacementLinkCheck(owned, info); p != nil {
			return outcome, p
		}
	}
	bindings := appendBindings(r.bindings, checked.bindings)
	if p := checkBindings(bindings); p != nil {
		return outcome, p
	}
	if ctx.Err() != nil {
		return outcome, failure("BACKUP_REMOVE_CANCELED")
	}
	if ops.remove == nil {
		ops.remove = markBackupEntryForRemoval
	}
	if ops.close == nil {
		ops.close = (*os.File).Close
	}
	outcome = BackupRemovalUnknown
	removeErr := ops.remove(owned)
	// After disposition, only close this handle; do not Stat/Read it again.
	closeErr := ops.close(owned)
	owned = nil
	_, observed := checked.parent.Lstat(checked.name)
	if errors.Is(observed, fs.ErrNotExist) {
		outcome = BackupRemoved
	}
	if removeErr != nil || closeErr != nil || outcome != BackupRemoved {
		return outcome, failure("BACKUP_REMOVE_UNCONFIRMED")
	}
	if p := checkBindings(bindings); p != nil {
		return outcome, p
	}
	if ctx.Err() != nil {
		return outcome, failure("BACKUP_REMOVE_CANCELED")
	}
	return outcome, nil
}

func markBackupEntryForRemoval(file *os.File) error {
	var status windows.IO_STATUS_BLOCK
	remove := byte(1)
	err := windows.NtSetInformationFile(windows.Handle(file.Fd()), &status, &remove, 1, windows.FileDispositionInformation)
	runtime.KeepAlive(file)
	return err
}
