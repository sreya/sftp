# Request Based SFTP API

The request based API allows for custom backends in a way similar to the http
package. In order to create a backend you need to implement 4 handler
interfaces; one for reading, one for writing, one for misc commands and one for
listing files. Each has 1 required method and in each case those methods take
the Request as the only parameter and they each return something different.
These 4 interfaces are enough to handle all the SFTP traffic in a simplified
manner.

The Request structure has the following public fields:

- Method (string) - string name of incoming call
- Filepath (string) - POSIX path of file to act on
- Flags (uint32) - file open/create flags for OPEN, attribute flags for SETSTAT
- Attrs ([]byte) - byte string of file attribute data
- Target (string) - target path for renames and sym-links

Below are the methods and a brief description of what they need to do.

### Fileread(*Request) (io.Reader, error)

Handler for "Get" method and returns an io.Reader for the file which the server
then sends to the client.

### Filewrite(*Request) (io.Writer, error)

Handler for "Put" method and returns an io.Writer for the file which the server
then writes the uploaded file to. The file opening "pflags" are currently
preserved in the Request.Flags field as a 32bit bitmask value. See the [SFTP
spec](https://filezilla-project.org/specs/draft-ietf-secsh-filexfer-02.txt#section-6.3) for
details.

###    Filecmd(*Request) error

Handles "SetStat", "Rename", "Rmdir", "Mkdir"  and "Symlink" methods. Makes the
appropriate changes and returns nil for success or an filesystem like error
(eg. os.ErrNotExist). The attributes are currently propagated in their raw form
([]byte) and will need to be unmarshalled to be useful. See the respond method
on sshFxpSetstatPacket for example of you might want to do this.

### Fileinfo(*Request) ([]os.FileInfo, error)

Handles "List", "Stat", "Readlink" methods. Gathers/creates FileInfo structs
with the data on the files and returns in a list (list of 1 for Stat and
Readlink).


## Local filesystem handlers

`NewLocalHandlers()` provides the filesystem operations used by `NewServer`
through the request-handler interfaces. Configure relative paths with
`WithStartDirectory` rather than maintaining another working-directory setting:

```go
handlers := sftp.NewLocalHandlers()
server := sftp.NewRequestServer(conn, handlers, sftp.WithStartDirectory("/home/user"))
```

A caller can replace `FileGet` or `FilePut` with a wrapper that delegates to the
original handler and observes the returned file. The other operations continue
using the package's implementation. See `ExampleNewLocalHandlers` for wrappers
that log close and interruption without implementing filesystem operations.

Returned files implement `io.ReaderAt`, `io.WriterAt`, `io.Closer`, `FileStater`,
and `FileSetstater`. A wrapper must forward the optional metadata interfaces to
keep FSTAT/FSETSTAT on the open descriptor. The explicit `Fstat` and `Fsetstat` methods take priority
when present, and an error does not fall back to the pathname. Existing custom
handlers without these interfaces retain their path-based behavior, including
handlers that return a plain `*os.File` with its existing `Stat` method.

`Request.AttrFlags()` and `Request.Attributes()` now expose OPEN attributes
separately from the access flags returned by `Pflags()`. This includes explicit
creation mode `0000` and the absence of attributes. Existing manually constructed
SETSTAT requests continue using `Flags` as their attribute mask.

### Remaining differences

These handlers reuse the existing filesystem operations, not the entire server
lifecycle. `RequestServer` still normalizes paths before dispatch, which can differ
from `NewServer` for a symlink followed by `..`. Relative symlink targets retain
`RequestServer` semantics. The handlers expose the host filesystem and do not
provide a chroot or sandbox.

The default request-server working directory remains `/`, while an unconfigured
`NewServer` uses the process working directory. Set `WithStartDirectory` explicitly
when comparing the two. Windows virtual-root drive enumeration is not enabled by
this factory.

FSETSTAT size, mode, and ownership operations use the descriptor. File timestamps
retain `NewServer`'s path fallback on platforms where the file does not implement
`Chtimes`. Abrupt-disconnect error and cleanup behavior also remain those of
`RequestServer`.

## TODO

- Add support for API users to see trace/debugging info of what is going on
inside SFTP server.
- Unmarshal the file attributes into a structure on the Request object.
