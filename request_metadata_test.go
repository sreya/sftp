package sftp

import (
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type metadataFile struct {
	*os.File
	statErr, setstatErr error
	setstatCalls        int
	attrs               *FileStat
}

func (f *metadataFile) Stat() (os.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return f.File.Stat()
}

func (f *metadataFile) Setstat(r *Request) error {
	f.setstatCalls++
	f.attrs = r.Attributes()
	return f.setstatErr
}

type metadataIOOnly struct {
	io.ReaderAt
	io.WriterAt
	io.Closer
}

type metadataHandlers struct {
	file                    *metadataFile
	descriptor              bool
	statCalls, setstatCalls int
	fallbackAttributes      *FileStat
}

func (h *metadataHandlers) open() interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
} {
	if h.descriptor {
		return h.file
	}
	return &metadataIOOnly{h.file, h.file, h.file}
}

func (h *metadataHandlers) Fileread(*Request) (io.ReaderAt, error)      { return h.open(), nil }
func (h *metadataHandlers) Filewrite(*Request) (io.WriterAt, error)     { return h.open(), nil }
func (h *metadataHandlers) OpenFile(*Request) (WriterAtReaderAt, error) { return h.open(), nil }
func (h *metadataHandlers) Filecmd(r *Request) error {
	h.setstatCalls++
	h.fallbackAttributes = r.Attributes()
	return nil
}
func (h *metadataHandlers) Filelist(*Request) (ListerAt, error) {
	h.statCalls++
	info, err := h.file.File.Stat()
	return metadataInfo{info}, err
}

type metadataInfo []os.FileInfo

func (l metadataInfo) ListAt(dst []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[off:])
	if n < len(dst) {
		return n, io.EOF
	}
	return n, nil
}

func TestRequestDescriptorMetadata(t *testing.T) {
	t.Parallel()
	for _, access := range []struct {
		name  string
		flags int
	}{
		{"Read", os.O_RDONLY}, {"Write", os.O_WRONLY}, {"ReadWrite", os.O_RDWR},
	} {
		t.Run(access.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name       string
				descriptor bool
				err        error
			}{
				{name: "Descriptor", descriptor: true},
				{name: "DescriptorError", descriptor: true, err: os.ErrPermission},
				{name: "LegacyFallback"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					file, err := os.CreateTemp(t.TempDir(), "file")
					require.NoError(t, err)
					t.Cleanup(func() { _ = file.Close() })
					h := &metadataHandlers{file: &metadataFile{File: file, statErr: tc.err, setstatErr: tc.err}, descriptor: tc.descriptor}
					client := newLocalRequestTestClient(t, Handlers{h, h, h, h})
					remote, err := client.OpenFile("file", access.flags)
					require.NoError(t, err)
					defer remote.Close()
					_, err = remote.Stat()
					if tc.err != nil {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
					err = remote.Chmod(0600)
					if tc.err != nil {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
					if tc.descriptor {
						require.Zero(t, h.statCalls)
						require.Zero(t, h.setstatCalls)
						require.Equal(t, 1, h.file.setstatCalls)
						require.Equal(t, os.FileMode(0600), h.file.attrs.FileMode().Perm())
					} else {
						require.Equal(t, 1, h.statCalls)
						require.Equal(t, 1, h.setstatCalls)
						require.Zero(t, h.file.setstatCalls)
						require.Equal(t, os.FileMode(0600), h.fallbackAttributes.FileMode().Perm())
					}
				})
			}
		})
	}
}

func newLocalRequestTestClient(t *testing.T, handlers Handlers, options ...RequestServerOption) *Client {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	deadline := time.Now().Add(10 * time.Second)
	require.NoError(t, serverConn.SetDeadline(deadline))
	require.NoError(t, clientConn.SetDeadline(deadline))
	server := NewRequestServer(serverConn, handlers, options...)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve()
	}()
	client, err := NewClientPipe(clientConn, clientConn)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("request server did not stop")
		}
	})
	return client
}
