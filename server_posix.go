//go:build !windows
// +build !windows

package sftp

import (
	"io/fs"
	"os"
)

func (lfs localFS) openLocal(path string, flag int, mode fs.FileMode) (file, error) {
	return os.OpenFile(path, flag, mode)
}

func (lfs localFS) lstatLocal(name string) (os.FileInfo, error) {
	return os.Lstat(name)
}

func (lfs localFS) statLocal(name string) (os.FileInfo, error) {
	return os.Stat(name)
}
