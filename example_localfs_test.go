package sftp_test

import (
	"io"
	"log"
	"os"
	"sync/atomic"

	"github.com/pkg/sftp"
)

// localFile is the method set of files returned by sftp.NewLocalHandlers.
// Embedding all of it, not only io.ReaderAt or io.WriterAt, keeps FSTAT and
// FSETSTAT on the open file, as sftp.Server does.
type localFile interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
	sftp.Fstater
	sftp.Fsetstater
}

// loggedFile counts the bytes transferred through an open file and logs them
// when the file is closed.
type loggedFile struct {
	localFile
	method, path  string
	read, written atomic.Int64
}

func (f *loggedFile) ReadAt(b []byte, off int64) (int, error) {
	n, err := f.localFile.ReadAt(b, off)
	f.read.Add(int64(n))
	return n, err
}

func (f *loggedFile) WriteAt(b []byte, off int64) (int, error) {
	n, err := f.localFile.WriteAt(b, off)
	f.written.Add(int64(n))
	return n, err
}

func (f *loggedFile) Close() error {
	err := f.localFile.Close()
	log.Printf("sftp %s %q: read %d bytes, wrote %d bytes, close error: %v",
		f.method, f.path, f.read.Load(), f.written.Load(), err)
	return err
}

// TransferError is called before Close if the session ends while the file is
// still open.
func (f *loggedFile) TransferError(err error) {
	log.Printf("sftp %s %q: session ended: %v", f.method, f.path, err)
}

// logOpen logs a failed open, or wraps the opened file.
func logOpen[F any](r *sftp.Request, open func(*sftp.Request) (F, error)) (localFile, error) {
	f, err := open(r)
	if err != nil {
		log.Printf("sftp %s %q: %v", r.Method, r.Filepath, err)
		return nil, err
	}
	return &loggedFile{localFile: any(f).(localFile), method: r.Method, path: r.Filepath}, nil
}

// loggedOpener wraps the FileGet and FilePut handlers.
type loggedOpener struct {
	get sftp.FileReader
	put sftp.OpenFileWriter
}

func (h loggedOpener) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	return logOpen(r, h.get.Fileread)
}

func (h loggedOpener) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	return logOpen(r, h.put.Filewrite)
}

func (h loggedOpener) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	return logOpen(r, h.put.OpenFile)
}

// loggedCmder wraps the FileCmd handler. Embedding sftp.StatVFSFileCmder,
// not only sftp.FileCmder, keeps statvfs@openssh.com supported.
type loggedCmder struct {
	sftp.StatVFSFileCmder
}

func (h loggedCmder) Filecmd(r *sftp.Request) error {
	err := h.StatVFSFileCmder.Filecmd(r)
	log.Printf("sftp %s %q %q: %v", r.Method, r.Filepath, r.Target, err)
	return err
}

func ExampleNewLocalHandlers() {
	var channel io.ReadWriteCloser // an accepted "sftp" subsystem channel

	handlers := sftp.NewLocalHandlers()
	opener := loggedOpener{get: handlers.FileGet, put: handlers.FilePut.(sftp.OpenFileWriter)}
	handlers.FileGet = opener
	handlers.FilePut = opener
	handlers.FileCmd = loggedCmder{handlers.FileCmd.(sftp.StatVFSFileCmder)}

	// Resolve relative paths against the process working directory, as
	// sftp.NewServer does by default. RequestServer otherwise uses "/".
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	server := sftp.NewRequestServer(channel, handlers, sftp.WithStartDirectory(wd))
	if err := server.Serve(); err != nil && err != io.EOF {
		log.Print("sftp server completed with error:", err)
	}
	server.Close()
}
