package runtimepaths

import (
	"errors"
	"io/fs"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type runFileID struct {
	kind int32
	data string
}

type runInode struct{ device, inode uint64 }

type runFilePin struct {
	mapping []byte
	file    *os.File
}

type runFileIdentities struct {
	mu   sync.Mutex
	pins map[runInode]runFilePin
}

func inodeOf(info fs.FileInfo) runInode {
	stat := info.Sys().(*syscall.Stat_t)
	return runInode{uint64(stat.Dev), stat.Ino}
}

func (ids *runFileIdentities) object(file *os.File, info fs.FileInfo) runFileID {
	// Keep the identity of a pinned inode even when filesystem permissions change.
	ids.mu.Lock()
	_, pinned := ids.pins[inodeOf(info)]
	ids.mu.Unlock()
	if pinned {
		return runFileID{}
	}
	handle, _, err := unix.NameToHandleAt(int(file.Fd()), "", unix.AT_EMPTY_PATH)
	runtime.KeepAlive(file)
	if err != nil || handle.Size() == 0 {
		return runFileID{}
	}
	return runFileID{kind: handle.Type(), data: string(handle.Bytes())}
}

func (ids *runFileIdentities) stat(child *os.Root, name string) (fs.FileInfo, runFileID, error) {
	file, err := child.OpenFile(name, unix.O_PATH|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, runFileID{}, err
	}
	info, err := file.Stat()
	var id runFileID
	if err == nil && info.Mode().IsRegular() {
		id = ids.object(file, info)
		if id == (runFileID{}) {
			var adopted bool
			adopted, err = ids.retain(info, file)
			if adopted {
				return info, id, nil
			}
		}
	}
	return info, id, errors.Join(err, file.Close())
}

// A mapping retains the file and prevents inode reuse without keeping a file
// descriptor per report. PROT_NONE never reads the file or faults in its data.
func mapRunFile(file *os.File) ([]byte, error) {
	path := "/proc/self/fd/" + strconv.FormatUint(uint64(file.Fd()), 10)
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	runtime.KeepAlive(file)
	if err != nil {
		return nil, nil
	}
	mapping, _ := unix.Mmap(fd, 0, 1, unix.PROT_NONE, unix.MAP_PRIVATE)
	if err := unix.Close(fd); err != nil {
		if mapping != nil {
			err = errors.Join(err, unix.Munmap(mapping))
		}
		return nil, err
	}
	return mapping, nil
}

// retain reports whether the Root adopted the caller's O_PATH descriptor.
// Metadata-only permissions and filesystems without mmap still work via this pin.
func (ids *runFileIdentities) retain(info fs.FileInfo, file *os.File) (bool, error) {
	key := inodeOf(info)
	ids.mu.Lock()
	defer ids.mu.Unlock()
	if _, pinned := ids.pins[key]; pinned {
		return false, nil
	}
	if ids.pins == nil {
		ids.pins = make(map[runInode]runFilePin)
	}
	mapping, err := mapRunFile(file)
	if err != nil {
		return false, err
	}
	if mapping != nil {
		ids.pins[key] = runFilePin{mapping: mapping}
		return false, nil
	}
	ids.pins[key] = runFilePin{file: file}
	return true, nil
}

// The owning Root excludes readers before calling close.
func (ids *runFileIdentities) close() error {
	var err error
	for _, pin := range ids.pins {
		if pin.file != nil {
			err = errors.Join(err, pin.file.Close())
		} else {
			err = errors.Join(err, unix.Munmap(pin.mapping))
		}
	}
	ids.pins = nil
	return err
}
