package sftp_test

import (
	"io"
	"log"

	"github.com/pkg/sftp"
)

// Keep the metadata methods when decorating a local file. Embedding only
// io.WriterAt would hide these methods from RequestServer.
type observedLocalFile interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
	sftp.FileStater
	sftp.FileSetstater
}

type loggingLocalFile struct {
	observedLocalFile
	path string
}

func (f *loggingLocalFile) Close() error {
	err := f.observedLocalFile.Close()
	log.Printf("closed file %q: %v", f.path, err)
	return err
}

func (f *loggingLocalFile) TransferError(err error) {
	log.Printf("interrupted file %q: %v", f.path, err)
}

type loggingLocalReader struct{ sftp.FileReader }

func (h loggingLocalReader) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	f, err := h.FileReader.Fileread(r)
	if err != nil {
		log.Printf("open file %q: %v", r.Filepath, err)
		return nil, err
	}
	return &loggingLocalFile{observedLocalFile: f.(observedLocalFile), path: r.Filepath}, nil
}

type loggingLocalWriter struct{ sftp.FileWriter }

func (h loggingLocalWriter) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	f, err := h.FileWriter.Filewrite(r)
	if err != nil {
		log.Printf("open file %q: %v", r.Filepath, err)
		return nil, err
	}
	return &loggingLocalFile{observedLocalFile: f.(observedLocalFile), path: r.Filepath}, nil
}

func (h loggingLocalWriter) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	f, err := h.FileWriter.(sftp.OpenFileWriter).OpenFile(r)
	if err != nil {
		log.Printf("open file %q: %v", r.Filepath, err)
		return nil, err
	}
	return &loggingLocalFile{observedLocalFile: f.(observedLocalFile), path: r.Filepath}, nil
}

func ExampleNewLocalHandlers() {
	handlers := sftp.NewLocalHandlers()
	handlers.FileGet = loggingLocalReader{FileReader: handlers.FileGet}
	handlers.FilePut = loggingLocalWriter{FileWriter: handlers.FilePut}

	// Pass handlers to NewRequestServer with an established SSH channel:
	// server := sftp.NewRequestServer(channel, handlers, sftp.WithStartDirectory("/home/user"))
	// err := server.Serve()
	_ = handlers
}
