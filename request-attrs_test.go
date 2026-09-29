package sftp

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestPflags(t *testing.T) {
	pflags := newFileOpenFlags(sshFxfRead | sshFxfWrite | sshFxfAppend)
	assert.True(t, pflags.Read)
	assert.True(t, pflags.Write)
	assert.True(t, pflags.Append)
	assert.False(t, pflags.Creat)
	assert.False(t, pflags.Trunc)
	assert.False(t, pflags.Excl)
}

func TestRequestAflags(t *testing.T) {
	aflags := newFileAttrFlags(
		sshFileXferAttrSize | sshFileXferAttrUIDGID)
	assert.True(t, aflags.Size)
	assert.True(t, aflags.UidGid)
	assert.False(t, aflags.Acmodtime)
	assert.False(t, aflags.Permissions)
}

func TestRequestAttributes(t *testing.T) {
	// UID/GID
	fa := FileStat{UID: 1, GID: 2}
	fl := uint32(sshFileXferAttrUIDGID)
	at := []byte{}
	at = marshalUint32(at, 1)
	at = marshalUint32(at, 2)
	testFs, _, err := unmarshalFileStat(fl, at)
	require.NoError(t, err)
	assert.Equal(t, fa, *testFs)
	// Size and Mode
	fa = FileStat{Mode: 0700, Size: 99}
	fl = uint32(sshFileXferAttrSize | sshFileXferAttrPermissions)
	at = []byte{}
	at = marshalUint64(at, 99)
	at = marshalUint32(at, 0700)
	testFs, _, err = unmarshalFileStat(fl, at)
	require.NoError(t, err)
	assert.Equal(t, fa, *testFs)
	// FileMode
	assert.True(t, testFs.FileMode().IsRegular())
	assert.False(t, testFs.FileMode().IsDir())
	assert.Equal(t, testFs.FileMode().Perm(), os.FileMode(0700).Perm())
}

func TestRequestAttributesEmpty(t *testing.T) {
	fs, b, err := unmarshalFileStat(sshFileXferAttrAll, []byte{
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // size
		0x00, 0x00, 0x00, 0x00, // mode
		0x00, 0x00, 0x00, 0x00, // mtime
		0x00, 0x00, 0x00, 0x00, // atime
		0x00, 0x00, 0x00, 0x00, // uid
		0x00, 0x00, 0x00, 0x00, // gid
		0x00, 0x00, 0x00, 0x00, // extended_count
	})
	require.NoError(t, err)
	assert.Equal(t, &FileStat{
		Extended: []StatExtended{},
	}, fs)
	assert.Empty(t, b)
}

func TestRequestOpenAttributes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags uint32
		attrs FileStat
	}{
		{name: "Absent"},
		{name: "ModeZero", flags: sshFileXferAttrPermissions},
		{name: "Mode0600", flags: sshFileXferAttrPermissions, attrs: FileStat{Mode: 0600}},
		{name: "Mixed", flags: sshFileXferAttrSize | sshFileXferAttrUIDGID | sshFileXferAttrPermissions | sshFileXferAttrACmodTime,
			attrs: FileStat{Size: 123, UID: 4, GID: 5, Mode: 0640, Atime: 10, Mtime: 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access := uint32(sshFxfRead | sshFxfWrite | sshFxfAppend | sshFxfCreat)
			r := requestFromPacket(context.Background(), &sshFxpOpenPacket{
				Path: "file", Pflags: access, Flags: tc.flags,
				Attrs: marshalFileStat(nil, tc.flags, &tc.attrs),
			}, "/")
			t.Cleanup(r.cancelCtx)
			for _, request := range []*Request{r, r.WithContext(context.Background())} {
				require.Equal(t, access, request.Flags)
				require.Equal(t, newFileOpenFlags(access), request.Pflags())
				require.Equal(t, newFileAttrFlags(tc.flags), request.AttrFlags())
				require.Equal(t, &tc.attrs, request.Attributes())
			}
		})
	}
}

func TestRequestManualAttributesCompatibility(t *testing.T) {
	attrs := FileStat{Mode: 0600}
	r := &Request{Method: "Setstat", Flags: sshFileXferAttrPermissions,
		Attrs: marshalFileStat(nil, sshFileXferAttrPermissions, &attrs)}
	require.True(t, r.AttrFlags().Permissions)
	require.Equal(t, &attrs, r.Attributes())
}
