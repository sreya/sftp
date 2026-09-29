package sftp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	_ OpenFileWriter       = localHandlers{}
	_ StatVFSFileCmder     = localHandlers{}
	_ LstatFileLister      = localHandlers{}
	_ ReadlinkFileLister   = localHandlers{}
	_ NameLookupFileLister = localHandlers{}

	_ localHandle = localFile{}
	_ ListerAt    = &localDirectory{}
)

// testLocalServers runs fn against Server and against RequestServer with
// NewLocalHandlers, which should behave the same.
func testLocalServers(t *testing.T, fn func(t *testing.T, client *Client)) {
	t.Run("Server", func(t *testing.T) {
		client, server := clientServerPair(t)
		defer client.Close()
		defer server.Close()
		fn(t, client)
		checkServerAllocator(t, server)
	})
	t.Run("RequestServer", func(t *testing.T) {
		p := clientRequestServerPairWithHandlers(t, NewLocalHandlers())
		defer p.Close()
		fn(t, p.cli)
		checkRequestServerAllocator(t, p)
	})
}

func TestLocalHandlersOpenWithPermissions(t *testing.T) {
	skipIfWindows(t)

	tests := []struct {
		name  string
		flags uint32
		attrs *FileStat
		mode  os.FileMode
	}{
		{"default", 0, nil, 0o644},
		{"zero", sshFileXferAttrPermissions, &FileStat{Mode: 0}, 0},
		{"private", sshFileXferAttrPermissions, &FileStat{Mode: 0o600}, 0o600},
		{"all attributes", sshFileXferAttrSize | sshFileXferAttrUIDGID | sshFileXferAttrPermissions | sshFileXferAttrACmodTime,
			&FileStat{Size: 99, UID: 123, GID: 456, Mode: 0o600, Atime: 1, Mtime: 2}, 0o600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testLocalServers(t, func(t *testing.T, client *Client) {
				dir := t.TempDir()
				expected := filepath.Join(dir, "expected")
				require.NoError(t, os.WriteFile(expected, nil, tt.mode))
				want, err := os.Stat(expected)
				require.NoError(t, err)

				id := client.nextID()
				typ, data, err := client.sendPacket(context.Background(), nil, &sshFxpOpenPacket{
					ID:     id,
					Path:   path.Join(dir, "file"),
					Pflags: toPflags(os.O_RDWR | os.O_CREATE | os.O_TRUNC),
					Flags:  tt.flags,
					Attrs:  tt.attrs,
				})
				require.NoError(t, err)
				require.Equal(t, fxp(sshFxpHandle), typ)
				gotID, data := unmarshalUint32(data)
				require.Equal(t, id, gotID)
				handle, _ := unmarshalString(data)
				f := &File{c: client, path: path.Join(dir, "file"), handle: handle}

				info, err := f.Stat()
				require.NoError(t, err)
				assert.Equal(t, want.Mode().Perm(), info.Mode().Perm())
				assert.Zero(t, info.Size(), "OPEN ignores the size attribute")
				require.NoError(t, f.Close())
			})
		})
	}
}

func TestLocalHandlersOperations(t *testing.T) {
	skipIfWindows(t)

	testLocalServers(t, func(t *testing.T, client *Client) {
		dir := t.TempDir()
		nested := path.Join(dir, "nested")
		require.NoError(t, client.Mkdir(nested))

		f, err := client.Create(path.Join(nested, "file"))
		require.NoError(t, err)
		_, err = f.Write([]byte("data"))
		require.NoError(t, err)
		require.NoError(t, f.Close())
		require.NoError(t, client.Rename(path.Join(nested, "file"), path.Join(nested, "renamed")))

		require.NoError(t, client.Link(path.Join(nested, "renamed"), path.Join(dir, "hardlink")))
		require.NoError(t, client.Symlink("renamed", path.Join(nested, "symlink")))
		target, err := client.ReadLink(path.Join(nested, "symlink"))
		require.NoError(t, err)
		assert.Equal(t, "renamed", target)
		info, err := client.Lstat(path.Join(nested, "symlink"))
		require.NoError(t, err)
		assert.True(t, info.Mode()&os.ModeSymlink != 0)
		info, err = client.Stat(path.Join(nested, "symlink"))
		require.NoError(t, err)
		assert.Equal(t, int64(4), info.Size())
		require.NoError(t, client.Remove(path.Join(nested, "symlink")))
		require.NoError(t, client.Remove(path.Join(dir, "hardlink")))

		require.NoError(t, client.Chmod(path.Join(nested, "renamed"), 0o600))
		info, err = client.Stat(path.Join(nested, "renamed"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

		require.NoError(t, client.PosixRename(path.Join(nested, "renamed"), path.Join(nested, "final")))
		require.NoError(t, client.Remove(path.Join(nested, "final")))
		require.NoError(t, client.RemoveDirectory(nested))
		_, err = client.Stat(nested)
		assert.True(t, os.IsNotExist(err))

		if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
			stats, err := client.StatVFS(dir)
			require.NoError(t, err)
			assert.NotZero(t, stats.Bsize)
		}
	})
}

func TestLocalHandlersReadDir(t *testing.T) {
	skipIfWindows(t)

	testLocalServers(t, func(t *testing.T, client *Client) {
		dir := t.TempDir()
		// More entries than either server returns for one READDIR.
		for i := range 300 {
			require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), nil, 0o600))
		}
		entries, err := client.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 300)

		_, err = client.ReadDir(path.Join(dir, "0"))
		assert.Error(t, err)
	})
}

// FSTAT and FSETSTAT use the open file, not the path it was opened with.
func TestLocalHandlersDescriptorMetadata(t *testing.T) {
	skipIfWindows(t)

	for _, action := range []string{"rename", "unlink"} {
		t.Run(action, func(t *testing.T) {
			testLocalServers(t, func(t *testing.T, client *Client) {
				name := filepath.Join(t.TempDir(), "file")
				f, err := client.OpenFile(name, os.O_RDWR|os.O_CREATE)
				require.NoError(t, err)
				_, err = f.WriteAt([]byte("original"), 0)
				require.NoError(t, err)
				if action == "rename" {
					require.NoError(t, os.Rename(name, name+".moved"))
				} else {
					require.NoError(t, os.Remove(name))
				}
				require.NoError(t, os.WriteFile(name, []byte("replacement"), 0o600))

				info, err := f.Stat()
				require.NoError(t, err)
				assert.Equal(t, int64(8), info.Size())
				require.NoError(t, f.Truncate(3))
				require.NoError(t, f.Chmod(0o400))
				info, err = f.Stat()
				require.NoError(t, err)
				assert.Equal(t, int64(3), info.Size())
				assert.Equal(t, os.FileMode(0o400), info.Mode().Perm())
				require.NoError(t, f.Close())

				replacement, err := os.ReadFile(name)
				require.NoError(t, err)
				assert.Equal(t, "replacement", string(replacement))
				info, err = os.Stat(name)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			})
		})
	}
}

// RequestServer cleans paths before calling the handlers, so a symlink
// followed by ".." resolves lexically instead of through the symlink.
func TestLocalHandlersPathCleaning(t *testing.T) {
	skipIfWindows(t)

	dir := t.TempDir()
	left, right := filepath.Join(dir, "left"), filepath.Join(dir, "right")
	require.NoError(t, os.MkdirAll(filepath.Join(right, "nested"), 0o700))
	require.NoError(t, os.Mkdir(left, 0o700))
	require.NoError(t, os.Symlink(filepath.Join(right, "nested"), filepath.Join(left, "link")))
	require.NoError(t, os.WriteFile(filepath.Join(left, "file"), []byte("lexical"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(right, "file"), []byte("symlink"), 0o600))
	name := left + "/link/../file"

	read := func(t *testing.T, client *Client) string {
		f, err := client.Open(name)
		require.NoError(t, err)
		defer f.Close()
		data, err := io.ReadAll(f)
		require.NoError(t, err)
		return string(data)
	}

	client, server := clientServerPair(t)
	defer client.Close()
	defer server.Close()
	assert.Equal(t, "symlink", read(t, client))

	p := clientRequestServerPairWithHandlers(t, NewLocalHandlers())
	defer p.Close()
	assert.Equal(t, "lexical", read(t, p.cli))
}

// localHandle is the method set of files returned by NewLocalHandlers.
type localHandle interface {
	WriterAtReaderAt
	io.Closer
	Fstater
	Fsetstater
}

type closeCountingFile struct {
	localHandle
	closed atomic.Int32
}

func (f *closeCountingFile) Close() error {
	f.closed.Add(1)
	return f.localHandle.Close()
}

type closeCountingWriter struct {
	OpenFileWriter
	file *closeCountingFile
}

func (w *closeCountingWriter) OpenFile(r *Request) (WriterAtReaderAt, error) {
	f, err := w.OpenFileWriter.OpenFile(r)
	if err != nil {
		return nil, err
	}
	w.file.localHandle = f.(localHandle)
	return w.file, nil
}

// A wrapper that forwards Fstater and Fsetstater keeps FSTAT and FSETSTAT on
// the open file.
func TestLocalHandlersWrappedFile(t *testing.T) {
	skipIfWindows(t)

	name := filepath.Join(t.TempDir(), "file")
	handlers := NewLocalHandlers()
	w := &closeCountingWriter{OpenFileWriter: handlers.FilePut.(OpenFileWriter), file: &closeCountingFile{}}
	handlers.FilePut = w
	p := clientRequestServerPairWithHandlers(t, handlers)
	defer p.Close()

	f, err := p.cli.OpenFile(name, os.O_RDWR|os.O_CREATE)
	require.NoError(t, err)
	_, err = f.WriteAt([]byte("data"), 0)
	require.NoError(t, err)
	require.NoError(t, os.Remove(name))
	require.NoError(t, f.Truncate(2))
	info, err := f.Stat()
	require.NoError(t, err)
	assert.Equal(t, int64(2), info.Size())
	require.NoError(t, f.Close())
	assert.Equal(t, int32(1), w.file.closed.Load())
	checkRequestServerAllocator(t, p)
}
