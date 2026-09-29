package sftp

import (
	"fmt"
	"io"
	"os"
)

// NewLocalHandlers returns request handlers that serve the local filesystem
// using the same filesystem operations as Server.
//
// Returned files implement io.ReaderAt, io.WriterAt, io.Closer, Fstater and
// Fsetstater. A wrapper around a returned file should forward Fstater and
// Fsetstater, so FSTAT and FSETSTAT keep using the open file as Server does.
//
// The handlers do not provide a chroot or sandbox. RequestServer cleans paths
// before calling the handlers, which differs from Server for a path containing
// a symlink followed by "..". Use WithStartDirectory to set the directory that
// relative paths are resolved against.
func NewLocalHandlers() Handlers {
	h := localHandlers{}
	return Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h}
}

// localHandlers adapts requests to the localFS operations used by Server.
type localHandlers struct {
	localFS
	osIDLookup
}

func (h localHandlers) Fileread(r *Request) (io.ReaderAt, error) {
	return h.OpenFile(r)
}

func (h localHandlers) Filewrite(r *Request) (io.WriterAt, error) {
	return h.OpenFile(r)
}

func (h localHandlers) OpenFile(r *Request) (WriterAtReaderAt, error) {
	f, err := h.open(r.Filepath, r.Flags, r.attrFlags, r.Attrs)
	if err != nil {
		return nil, err
	}
	return localFile{f}, nil
}

// Filecmd does not handle PosixRename. RequestServer handles it as Rename,
// which uses the same operation as Server's posix-rename@openssh.com.
func (h localHandlers) Filecmd(r *Request) error {
	switch r.Method {
	case "Setstat":
		return h.setstat(r.Filepath, r.Flags, r.Attrs)
	case "Rename":
		return h.rename(r.Filepath, r.Target)
	case "Rmdir", "Remove":
		return h.remove(r.Filepath)
	case "Mkdir":
		return h.mkdir(r.Filepath)
	case "Link":
		return h.link(r.Filepath, r.Target)
	case "Symlink":
		return h.symlink(r.Filepath, r.Target)
	}
	return ErrSSHFxOpUnsupported
}

func (h localHandlers) StatVFS(r *Request) (*StatVFS, error) {
	// Server also passes the statvfs@openssh.com path through unchanged.
	return getStatVFSForPath(r.Filepath)
}

func (h localHandlers) Filelist(r *Request) (ListerAt, error) {
	switch r.Method {
	case "List":
		f, err := h.opendir(r.Filepath)
		if err != nil {
			return nil, err
		}
		return &localDirectory{localFile: localFile{f}}, nil
	case "Stat":
		info, err := h.stat(r.Filepath)
		if err != nil {
			return nil, err
		}
		return listerat{info}, nil
	}
	return nil, ErrSSHFxOpUnsupported
}

func (h localHandlers) Lstat(r *Request) (ListerAt, error) {
	info, err := h.lstat(r.Filepath)
	if err != nil {
		return nil, err
	}
	return listerat{info}, nil
}

func (h localHandlers) Readlink(p string) (string, error) {
	return h.readlink(p)
}

// localFile is a file opened by the local handlers.
type localFile struct {
	file
}

func (f localFile) Fstat() (os.FileInfo, error) {
	return f.Stat()
}

func (f localFile) Fsetstat(r *Request) error {
	return fsetstat(f.file, r.Flags, r.Attrs)
}

// localDirectory is a directory opened by the local handlers.
type localDirectory struct {
	localFile
	offset int64
}

// ListAt continues reading from the previous call. RequestServer handles
// READDIR sequentially and advances the offset by the entries returned.
func (d *localDirectory) ListAt(ls []os.FileInfo, offset int64) (int, error) {
	if offset != d.offset {
		return 0, fmt.Errorf("unexpected directory offset %d, expected %d", offset, d.offset)
	}
	if len(ls) == 0 {
		return 0, nil
	}

	entries, err := d.Readdir(len(ls))
	d.offset += int64(len(entries))
	return copy(ls, entries), err
}
