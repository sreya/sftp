//go:build !windows && !plan9
// +build !windows,!plan9

package sftp

import (
	"path"
)

func (lfs localFS) toLocalPath(p string) string {
	if lfs.workDir != "" && !path.IsAbs(p) {
		p = path.Join(lfs.workDir, p)
	}

	return p
}
