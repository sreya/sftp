//go:build !windows
// +build !windows

package sftp

import (
	"io/fs"
	"os"
)

func (s *Server) openfile(path string, flag int, mode fs.FileMode) (file, error) {
	return openLocalOSFile(path, flag, mode, s.winRoot)
}

func openLocalOSFile(path string, flag int, mode fs.FileMode, winRoot bool) (file, error) {
	return os.OpenFile(path, flag, mode)
}

func (s *Server) lstat(name string) (os.FileInfo, error) {
	return lstatLocalFile(name, s.winRoot)
}

func lstatLocalFile(name string, winRoot bool) (os.FileInfo, error) {
	return os.Lstat(name)
}

func (s *Server) stat(name string) (os.FileInfo, error) {
	return statLocalFile(name, s.winRoot)
}

func statLocalFile(name string, winRoot bool) (os.FileInfo, error) {
	return os.Stat(name)
}
