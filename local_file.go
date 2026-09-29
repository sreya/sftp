package sftp

import (
	"os"
	"syscall"
	"time"
)

// openLocalFile is shared by Server and the local request handlers so OPEN
// flag translation and creation permissions have a single implementation.
func openLocalFile(name string, pflags, attrFlags uint32, attrs any, winRoot bool) (file, error) {
	var osFlags int
	if pflags&(sshFxfRead|sshFxfWrite) == sshFxfRead|sshFxfWrite {
		osFlags |= os.O_RDWR
	} else if pflags&sshFxfWrite != 0 {
		osFlags |= os.O_WRONLY
	} else if pflags&sshFxfRead != 0 {
		osFlags |= os.O_RDONLY
	} else {
		// how are they opening?
		return nil, syscall.EINVAL
	}

	// Don't use O_APPEND flag as it conflicts with WriteAt.
	// The sshFxfAppend flag is a no-op here as the client sends the offsets.

	if pflags&sshFxfCreat != 0 {
		osFlags |= os.O_CREATE
	}
	if pflags&sshFxfTrunc != 0 {
		osFlags |= os.O_TRUNC
	}
	if pflags&sshFxfExcl != 0 {
		osFlags |= os.O_EXCL
	}

	mode := os.FileMode(0o644)
	// Like OpenSSH, we only handle permissions here, and only when the file is being created.
	// Otherwise, the permissions are ignored.
	if attrFlags&sshFileXferAttrPermissions != 0 {
		fs, err := openFileAttributes(attrFlags, attrs)
		if err != nil {
			return nil, err
		}
		mode = fs.FileMode() & os.ModePerm
	}

	f, err := openLocalOSFile(name, osFlags, mode, winRoot)
	if err != nil {
		return nil, err
	}

	return f, nil
}

func setLocalPathStat(name string, flags uint32, fs *FileStat) (err error) {
	if err == nil && (flags&sshFileXferAttrSize) != 0 {
		err = os.Truncate(name, int64(fs.Size))
	}
	if err == nil && (flags&sshFileXferAttrPermissions) != 0 {
		err = os.Chmod(name, fs.FileMode())
	}
	if err == nil && (flags&sshFileXferAttrUIDGID) != 0 {
		err = os.Chown(name, int(fs.UID), int(fs.GID))
	}
	if err == nil && (flags&sshFileXferAttrACmodTime) != 0 {
		err = os.Chtimes(name, fs.AccessTime(), fs.ModTime())
	}

	return err
}

func setLocalFileStat(f file, flags uint32, fs *FileStat) (err error) {
	name := f.Name()
	if err == nil && (flags&sshFileXferAttrSize) != 0 {
		err = f.Truncate(int64(fs.Size))
	}
	if err == nil && (flags&sshFileXferAttrPermissions) != 0 {
		err = f.Chmod(fs.FileMode())
	}
	if err == nil && (flags&sshFileXferAttrUIDGID) != 0 {
		err = f.Chown(int(fs.UID), int(fs.GID))
	}
	if err == nil && (flags&sshFileXferAttrACmodTime) != 0 {
		type chtimer interface {
			Chtimes(atime, mtime time.Time) error
		}

		switch f := any(f).(type) {
		case chtimer:
			// future-compatible, for when/if *os.File supports Chtimes.
			err = f.Chtimes(fs.AccessTime(), fs.ModTime())
		default:
			err = os.Chtimes(name, fs.AccessTime(), fs.ModTime())
		}
	}

	return err
}
