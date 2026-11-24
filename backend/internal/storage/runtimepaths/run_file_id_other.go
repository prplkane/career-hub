//go:build !linux

package runtimepaths

import (
	"io/fs"
	"os"
)

type runFileID struct{}
type runFileIdentities struct{}

func (runFileIdentities) stat(child *os.Root, name string) (fs.FileInfo, runFileID, error) {
	info, err := child.Lstat(name)
	return info, runFileID{}, err
}

func (runFileIdentities) object(*os.File, fs.FileInfo) runFileID { return runFileID{} }
func (runFileIdentities) close() error                           { return nil }
