package bulk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Package bulk carries blob bytes from the host into a guest that has no
// filesystem in common with it.
//
// **A channel of its own, because the agent's frames cannot hold a layer.** The
// protocol in proto.go is length-prefixed JSON with a size limit, so a 45 MB
// layer would have to be base64-encoded and cut into pieces - a third more
// bytes and a reassembly bug waiting to be written. Here the length is a number
// and the body is the bytes.
//
// **And the host sends rather than the guest fetching**, because the credential
// is the reason: `engine/image` reads the machine's credential store, so a
// registry password has never been inside a sandbox. A guest that fetched for
// itself would move it into the blast radius of the untrusted code the sandbox
// exists to contain.
//
// Where a filesystem *is* shared - a namespace guest, or a VM with virtio-fs -
// none of this is used: the host writes the blob where the guest can see it and
// says the path, which is fewer copies and the reason that path stays.
const (
	// magic starts every blob, so a desynchronised stream is caught at the
	// next header rather than read as a length of several gigabytes.
	magic = 0xE4B10B01

	// maxName bounds the name field. A blob is named by its digest, which
	// is 71 bytes; the rest is room for a prefix.
	maxName = 256
)

var errDesync = errors.New("the bulk channel lost its framing")

// SendBlob writes one blob to the bulk channel.
//
// The length comes from the caller rather than from the reader, because the
// receiver has to know how much to expect before it starts: a stream that ends
// early must be a failure and not a short file. A reader that gives fewer bytes
// than promised fails here, before the receiver can accept a partial layer.
func SendBlob(w io.Writer, name string, body io.Reader, size int64) error {
	if name == "" || len(name) > maxName {
		return fmt.Errorf("a blob's name is %d bytes and the limit is %d",
			len(name), maxName)
	}

	var hdr [16]byte

	binary.BigEndian.PutUint32(hdr[0:4], magic)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(name))) //nolint:gosec // bounded above
	binary.BigEndian.PutUint64(hdr[8:16], uint64(size))     //nolint:gosec // a file's size

	_, err := w.Write(hdr[:])
	if err != nil {
		return fmt.Errorf("send the header for %s: %w", name, err)
	}

	_, err = io.WriteString(w, name)
	if err != nil {
		return fmt.Errorf("send the name of %s: %w", name, err)
	}

	n, err := io.Copy(w, body)
	if err != nil {
		return fmt.Errorf("send %s: %w", name, err)
	}

	// **Checked here, where the blob is still identifiable.** The receiver
	// would find out too - it reads exactly `size` bytes and the next header
	// would not match the magic - but by then the failure is "the channel lost
	// its framing" and names nothing.
	if n != size {
		return fmt.Errorf("%s was announced as %d bytes and is %d"+
			"\n  a blob that arrives short unpacks into a layer missing its tail,"+
			" which is a wrong build that reports success", name, size, n)
	}

	return nil
}

// ReceiveBlobs writes every blob on the channel into dir, until the channel
// ends.
//
// Returns nil at a clean end. A stream that stops mid-blob is an error: the
// sender went away, and what has been written is a fraction of a layer.
func ReceiveBlobs(r io.Reader, dir string) error {
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return fmt.Errorf("prepare %s for blobs: %w", dir, err)
	}

	for {
		err := receiveBlob(r, dir)
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}
	}
}

func receiveBlob(r io.Reader, dir string) error {
	var hdr [16]byte

	_, err := io.ReadFull(r, hdr[:])
	if err != nil {
		// A channel that ends *between* blobs has ended cleanly, and one that
		// ends inside a header has not.
		if errors.Is(err, io.EOF) {
			return io.EOF
		}

		return fmt.Errorf("read a blob header: %w", err)
	}

	if binary.BigEndian.Uint32(hdr[0:4]) != magic {
		return fmt.Errorf("%w: the header does not start with the magic,"+
			" so what follows is not a length", errDesync)
	}

	nameLen := binary.BigEndian.Uint32(hdr[4:8])
	if nameLen == 0 || nameLen > maxName {
		return fmt.Errorf("%w: a name of %d bytes", errDesync, nameLen)
	}

	name := make([]byte, nameLen)

	_, err = io.ReadFull(r, name)
	if err != nil {
		return fmt.Errorf("read a blob's name: %w", err)
	}

	at, err := pathFor(dir, string(name))
	if err != nil {
		return err
	}

	//nolint:gosec // the size is the sender's, and the sender is the host
	return writeBlob(at, io.LimitReader(r, int64(binary.BigEndian.Uint64(hdr[8:16]))),
		string(name))
}

// pathFor is where a named blob lands, and refuses a name that would land
// somewhere else.
//
// **The sender is the host and the receiver is confined.** A confined process
// that writes wherever it is told is not confined, and this is the one place a
// name from outside becomes a path.
func pathFor(dir, name string) (string, error) {
	if strings.ContainsRune(name, '/') || name == "." || name == ".." {
		return "", fmt.Errorf("a blob named %q would not land in %s"+
			"\n  a blob is named by its digest, which has no path in it", name, dir)
	}

	return filepath.Join(dir, name), nil
}

// writeBlob puts the bytes at their final name, through a temporary one.
//
// **Renamed into place**, so a reader of the directory never sees a partial
// blob under a name that means a whole one: the store looks blobs up by digest
// and a half-written file under the right digest is the worst thing here.
func writeBlob(at string, body io.Reader, name string) error {
	tmp, err := os.CreateTemp(filepath.Dir(at), ".blob-")
	if err != nil {
		return fmt.Errorf("make room for %s: %w", name, err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = io.Copy(tmp, body)
	if err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write %s: %w", name, err)
	}

	err = tmp.Close()
	if err != nil {
		return fmt.Errorf("finish %s: %w", name, err)
	}

	err = os.Rename(tmp.Name(), at)
	if err != nil {
		return fmt.Errorf("put %s in place: %w", name, err)
	}

	return nil
}
