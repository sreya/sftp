//go:build !windows && !plan9
// +build !windows,!plan9

package sftp

import (
	"path"
)

func (s *Server) toLocalPath(p string) string {
	return localPath(s.workDir, s.winRoot, p)
}

func localPath(workDir string, winRoot bool, p string) string {
	if workDir != "" && !path.IsAbs(p) {
		p = path.Join(workDir, p)
	}

	return p
}
