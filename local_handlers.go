package sftp

import (
	"io"
	"os"
	"sync"
	"syscall"
)

// NewLocalHandlers returns request handlers backed by the local filesystem.
// Their filesystem operations share the implementation used by Server. Returned
// files implement io.ReaderAt, io.WriterAt, io.Closer, FileStater and FileSetstater.
// Wrappers should forward the optional metadata interfaces to retain FSTAT and
// FSETSTAT behavior after rename or unlink.
//
// The handlers are not a sandbox or chroot. RequestServer's path normalization
// still applies. Configure relative paths with WithStartDirectory. This differs
// from Server for paths containing a symlink followed by "..". Relative symlink
// targets retain RequestServer's semantics rather than being made absolute.
func NewLocalHandlers() Handlers {
	h := &localHandlers{}
	return Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h}
}

type localHandlers struct{ osIDLookup }

func (h *localHandlers) Fileread(r *Request) (io.ReaderAt, error)      { return h.open(r) }
func (h *localHandlers) Filewrite(r *Request) (io.WriterAt, error)     { return h.open(r) }
func (h *localHandlers) OpenFile(r *Request) (WriterAtReaderAt, error) { return h.open(r) }

func (h *localHandlers) open(r *Request) (*localFile, error) {
	f, err := openLocalFile(localPath("", false, r.Filepath), r.Flags, r.attributeFlags(), r.Attrs, false)
	if err != nil {
		return nil, err
	}
	return &localFile{file: f}, nil
}

type localFile struct{ file }

func (f *localFile) Fstat() (os.FileInfo, error) { return f.file.Stat() }

func (f *localFile) Fsetstat(r *Request) error {
	attrs, _, err := unmarshalFileStat(r.attributeFlags(), r.Attrs)
	if err != nil {
		return err
	}
	return setLocalFileStat(f.file, r.attributeFlags(), attrs)
}

func (*localHandlers) Filecmd(r *Request) error {
	name := localPath("", false, r.Filepath)
	switch r.Method {
	case "Setstat":
		attrs, _, err := unmarshalFileStat(r.attributeFlags(), r.Attrs)
		if err != nil {
			return err
		}
		return setLocalPathStat(name, r.attributeFlags(), attrs)
	case "Mkdir":
		return os.Mkdir(name, 0755)
	case "Rmdir", "Remove":
		return os.Remove(name)
	case "Rename", "PosixRename":
		return os.Rename(name, localPath("", false, r.Target))
	case "Link":
		return os.Link(name, localPath("", false, r.Target))
	case "Symlink":
		return os.Symlink(name, localPath("", false, r.Target))
	default:
		return ErrSSHFxOpUnsupported
	}
}

func (h *localHandlers) PosixRename(r *Request) error { return h.Filecmd(r) }

func (*localHandlers) StatVFS(r *Request) (*StatVFS, error) {
	return getStatVFSForPath(localPath("", false, r.Filepath))
}

func (*localHandlers) Filelist(r *Request) (ListerAt, error) {
	name := localPath("", false, r.Filepath)
	switch r.Method {
	case "List":
		info, err := statLocalFile(name, false)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, &os.PathError{Op: "opendir", Path: name, Err: syscall.ENOTDIR}
		}
		f, err := openLocalOSFile(name, os.O_RDONLY, 0, false)
		if err != nil {
			return nil, err
		}
		return &localDirectory{localFile: localFile{file: f}}, nil
	case "Stat":
		info, err := statLocalFile(name, false)
		return localFileInfo{info}, err
	default:
		return nil, ErrSSHFxOpUnsupported
	}
}

func (*localHandlers) Lstat(r *Request) (ListerAt, error) {
	info, err := lstatLocalFile(localPath("", false, r.Filepath), false)
	return localFileInfo{info}, err
}

func (*localHandlers) Readlink(name string) (string, error) {
	return os.Readlink(localPath("", false, name))
}

type localFileInfo []os.FileInfo

func (l localFileInfo) ListAt(dst []os.FileInfo, offset int64) (int, error) {
	if offset < 0 {
		return 0, syscall.EINVAL
	}
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[offset:])
	if n < len(dst) {
		return n, io.EOF
	}
	return n, nil
}

type localDirectory struct {
	localFile
	mu     sync.Mutex
	offset int64
}

func (d *localDirectory) ListAt(dst []os.FileInfo, offset int64) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if offset < 0 {
		return 0, syscall.EINVAL
	}
	if len(dst) == 0 {
		return 0, nil
	}
	if offset < d.offset {
		seeker, ok := d.file.(io.Seeker)
		if !ok {
			return 0, ErrSSHFxOpUnsupported
		}
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		d.offset = 0
	}
	// Normal READDIR calls advance sequentially. Skipping or restarting also
	// stays bounded rather than retaining a snapshot of the entire directory.
	for d.offset < offset {
		entries, err := d.Readdir(int(min(offset-d.offset, 128)))
		d.offset += int64(len(entries))
		if err != nil {
			return 0, err
		}
	}
	entries, err := d.Readdir(len(dst))
	d.offset += int64(len(entries))
	return copy(dst, entries), err
}
