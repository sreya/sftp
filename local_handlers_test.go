package sftp

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func localTestPath(p string) string {
	p = filepath.ToSlash(p)
	if runtime.GOOS == "windows" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

func newLocalServerTestClient(t *testing.T, dir string) *Client {
	t.Helper()
	a, b := net.Pipe()
	deadline := time.Now().Add(10 * time.Second)
	require.NoError(t, a.SetDeadline(deadline))
	require.NoError(t, b.SetDeadline(deadline))
	s, err := NewServer(a, WithServerWorkingDirectory(dir))
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { defer close(done); _ = s.Serve() }()
	client, err := NewClientPipe(b, b)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		_ = s.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	})
	return client
}

func TestLocalHandlersOpenPermissions(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}
	for _, factory := range []struct {
		name   string
		client func(*testing.T, string) *Client
	}{
		{"Server", newLocalServerTestClient},
		{"RequestServer", func(t *testing.T, dir string) *Client {
			return newLocalRequestTestClient(t, NewLocalHandlers(), WithStartDirectory(localTestPath(dir)))
		}},
	} {
		t.Run(factory.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range []struct {
				name  string
				flags uint32
				attrs *FileStat
				mode  os.FileMode
			}{
				{"Default", 0, nil, 0644},
				{"Zero", sshFileXferAttrPermissions, &FileStat{Mode: 0}, 0},
				{"Private", sshFileXferAttrPermissions, &FileStat{Mode: 0600}, 0600},
				{"Mixed", sshFileXferAttrSize | sshFileXferAttrUIDGID | sshFileXferAttrPermissions | sshFileXferAttrACmodTime, &FileStat{Size: 99, UID: 123, GID: 456, Mode: 0600, Atime: 1, Mtime: 2}, 0600},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					dir := t.TempDir()
					client := factory.client(t, dir)
					expected := filepath.Join(dir, "expected")
					require.NoError(t, os.WriteFile(expected, nil, tc.mode))
					want, err := os.Stat(expected)
					require.NoError(t, err)
					id := client.nextID()
					typ, data, err := client.sendPacket(context.Background(), nil, &sshFxpOpenPacket{
						ID: id, Path: "file", Pflags: sshFxfRead | sshFxfWrite | sshFxfCreat | sshFxfTrunc, Flags: tc.flags, Attrs: tc.attrs,
					})
					require.NoError(t, err)
					require.Equal(t, fxp(sshFxpHandle), typ)
					gotID, data := unmarshalUint32(data)
					require.Equal(t, id, gotID)
					handle, _ := unmarshalString(data)
					file := &File{c: client, path: "file", handle: handle}
					defer file.Close()
					info, err := file.Stat()
					require.NoError(t, err)
					require.Equal(t, want.Mode().Perm(), info.Mode().Perm())
					require.Zero(t, info.Size(), "OPEN ignores initial size like Server")
					_, err = file.WriteAt([]byte("payload"), 0)
					require.NoError(t, err)
					buf := make([]byte, 7)
					_, err = file.ReadAt(buf, 0)
					require.NoError(t, err)
					require.Equal(t, "payload", string(buf))
				})
			}
		})
	}
}

func TestLocalHandlersDescriptorMetadata(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("renaming an open Windows file")
	}
	for _, action := range []string{"rename", "unlink"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			client := newLocalRequestTestClient(t, NewLocalHandlers(), WithStartDirectory(dir))
			file, err := client.OpenFile("file", os.O_RDWR|os.O_CREATE)
			require.NoError(t, err)
			defer file.Close()
			_, err = file.WriteAt([]byte("original"), 0)
			require.NoError(t, err)
			name := filepath.Join(dir, "file")
			if action == "rename" {
				require.NoError(t, os.Rename(name, name+".moved"))
			} else {
				require.NoError(t, os.Remove(name))
			}
			require.NoError(t, os.WriteFile(name, []byte("replacement"), 0600))
			info, err := file.Stat()
			require.NoError(t, err)
			require.EqualValues(t, 8, info.Size())
			require.NoError(t, file.Truncate(3))
			require.NoError(t, file.Chmod(0400))
			info, err = file.Stat()
			require.NoError(t, err)
			require.EqualValues(t, 3, info.Size())
			require.Equal(t, os.FileMode(0400), info.Mode().Perm())
			untouched, err := os.ReadFile(name)
			require.NoError(t, err)
			require.Equal(t, "replacement", string(untouched))
			info, err = os.Stat(name)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		})
	}
}

func TestLocalHandlersOperations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	client := newLocalRequestTestClient(t, NewLocalHandlers(), WithStartDirectory(localTestPath(dir)))
	wd, err := client.Getwd()
	require.NoError(t, err)
	require.Equal(t, localTestPath(dir), wd)
	require.NoError(t, client.Mkdir("nested"))
	file, err := client.Create("nested/file")
	require.NoError(t, err)
	_, err = file.Write([]byte("data"))
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.NoError(t, client.Rename("nested/file", "nested/renamed"))
	if runtime.GOOS != "windows" {
		require.NoError(t, client.Link("nested/renamed", "hardlink"))
		require.NoError(t, client.Symlink("renamed", "nested/symlink"))
		target, err := client.ReadLink("nested/symlink")
		require.NoError(t, err)
		require.Equal(t, "renamed", target)
		info, err := client.Lstat("nested/symlink")
		require.NoError(t, err)
		require.NotZero(t, info.Mode()&os.ModeSymlink)
		info, err = client.Stat("nested/symlink")
		require.NoError(t, err)
		require.EqualValues(t, 4, info.Size())
		require.NoError(t, client.Remove("nested/symlink"))
		require.NoError(t, client.Remove("hardlink"))
	}
	entries, err := client.ReadDir("nested")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.NoError(t, client.PosixRename("nested/renamed", "nested/final"))
	require.NoError(t, client.Remove("nested/final"))
	require.NoError(t, client.RemoveDirectory("nested"))
	_, err = client.Stat("missing")
	require.Error(t, err)
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		stats, err := client.StatVFS(".")
		require.NoError(t, err)
		require.Positive(t, stats.Bsize)
	}
}

type decoratedLocalFile interface {
	io.ReaderAt
	io.WriterAt
	io.Closer
	FileStater
	FileSetstater
}

type observingLocalFile struct {
	decoratedLocalFile
	closed *atomic.Int32
}

func (f *observingLocalFile) Close() error { f.closed.Add(1); return f.decoratedLocalFile.Close() }

type observingLocalWriter struct {
	FileWriter
	closed *atomic.Int32
}

func (w *observingLocalWriter) Filewrite(r *Request) (io.WriterAt, error) {
	f, err := w.FileWriter.Filewrite(r)
	if err != nil {
		return nil, err
	}
	return &observingLocalFile{f.(decoratedLocalFile), w.closed}, nil
}
func (w *observingLocalWriter) OpenFile(r *Request) (WriterAtReaderAt, error) {
	f, err := w.FileWriter.(OpenFileWriter).OpenFile(r)
	if err != nil {
		return nil, err
	}
	return &observingLocalFile{f.(decoratedLocalFile), w.closed}, nil
}

func TestLocalHandlersDecoration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var closed atomic.Int32
	handlers := NewLocalHandlers()
	handlers.FilePut = &observingLocalWriter{handlers.FilePut, &closed}
	client := newLocalRequestTestClient(t, handlers, WithStartDirectory(localTestPath(dir)))
	f, err := client.OpenFile("file", os.O_RDWR|os.O_CREATE)
	require.NoError(t, err)
	_, err = f.WriteAt([]byte("data"), 0)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(2))
	info, err := f.Stat()
	require.NoError(t, err)
	require.EqualValues(t, 2, info.Size())
	require.NoError(t, f.Close())
	require.EqualValues(t, 1, closed.Load())
}

func TestLocalHandlersDirectoryOffsets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0600))
	}
	r := NewRequest("List", localTestPath(dir))
	lister, err := NewLocalHandlers().FileList.Filelist(r)
	require.NoError(t, err)
	defer lister.(io.Closer).Close()
	buf := make([]os.FileInfo, 1)
	n, err := lister.ListAt(buf, 0)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	first := buf[0].Name()
	_, err = lister.ListAt(buf, 2)
	require.NoError(t, err)
	_, err = lister.ListAt(buf, 0)
	require.NoError(t, err)
	require.Equal(t, first, buf[0].Name())
}
