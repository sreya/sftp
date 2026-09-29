package sftp

import (
	"path"
	"path/filepath"
)

func (lfs localFS) toLocalPath(p string) string {
	if lfs.workDir != "" && !path.IsAbs(p) {
		p = path.Join(lfs.workDir, p)
	}

	lp := filepath.FromSlash(p)

	if path.IsAbs(p) {
		tmp := lp[1:]

		if filepath.IsAbs(tmp) {
			// If the FromSlash without any starting slashes is absolute,
			// then we have a filepath encoded with a prefix '/'.
			// e.g. "/#s/boot" to "#s/boot"
			return tmp
		}
	}

	return lp
}
