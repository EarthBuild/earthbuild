package image

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// epoch is the timestamp every entry gets.
//
// Not the Unix epoch itself: some tools treat a zero time as "unset" and
// substitute the current one, which would put the clock back into the archive
// by the very mechanism meant to keep it out.
var epoch = time.Unix(1, 0).UTC()

// Pack writes a directory as a tar, and reports the digest and size of what it
// wrote.
//
// The inverse of Unpack, and deliberately its mirror: a tar this produces has
// to be one that reader accepts, because that reader is what every pulled image
// already goes through.
//
// **Byte-reproducible.** An image's identity is the digest of its layers, so a
// tar that varies between runs is an image that varies between runs - two
// builds of one input producing two different images, and a registry storing
// both. Three things make that happen and all three are normalised here:
// directory order, modification times, and ownership.
//
// SHA-256 rather than the BLAKE3 used everywhere else in this engine, because
// this digest is written into an OCI manifest and read by registries. It is the
// one place the format dictates the hash.
func Pack(dir string, w io.Writer) (digest string, size int64, err error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", 0, fmt.Errorf("resolve %s: %w", dir, err)
	}

	names, err := sortedEntries(root)
	if err != nil {
		return "", 0, err
	}

	h := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(w, h)}
	tw := tar.NewWriter(counter)

	// The first name each inode was written under, so a second name for it
	// becomes a link entry rather than a second copy of the bytes.
	links := map[linkID]string{}

	for _, rel := range names {
		packErr := packOne(tw, root, rel, links)
		if packErr != nil {
			return "", 0, packErr
		}
	}

	err = tw.Close()
	if err != nil {
		return "", 0, fmt.Errorf("finish the archive: %w", err)
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), counter.n, nil
}

// DigestOf names a blob the way an OCI manifest does.
func DigestOf(b []byte) string {
	sum := sha256.Sum256(b)

	return "sha256:" + hex.EncodeToString(sum[:])
}

// sortedEntries lists the tree in a fixed order.
//
// Sorted by path rather than taken from the filesystem, because a directory
// listing has no order to promise and two machines will not agree on one.
// Byte-wise, not collation-aware: a locale-dependent ordering would make a
// layer's identity depend on the language of the machine that built it.
func sortedEntries(root string) ([]string, error) {
	var names []string

	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if p == root {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}

		names = append(names, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	sort.Strings(names)

	return names, nil
}

// packOne writes one entry, with everything variable removed.
// linkID identifies a file for spotting hard links: device and inode, because
// an inode number is only unique within a filesystem.
type linkID struct{ dev, ino uint64 }

func packOne(tw *tar.Writer, root, rel string, links map[linkID]string) error {
	p := filepath.Join(root, filepath.FromSlash(rel))

	info, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}

	link := ""
	if info.Mode()&os.ModeSymlink != 0 {
		link, err = os.Readlink(p)
		if err != nil {
			return fmt.Errorf("read the link %s: %w", rel, err)
		}
	}

	h, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return fmt.Errorf("describe %s: %w", rel, err)
	}

	h.Name = rel
	if info.IsDir() {
		h.Name += "/"
	}

	// Everything that is about the machine rather than the content. A timestamp
	// and an owner are properties of the checkout, not of what was built, and
	// two clones of one commit disagree on both.
	h.ModTime = epoch
	h.AccessTime = epoch
	h.ChangeTime = epoch
	h.Uid, h.Gid = 0, 0
	h.Uname, h.Gname = "", ""
	h.Format = tar.FormatPAX

	// Extended attributes, which FileInfoHeader knows nothing about. A layer's
	// `security.capability` - what `setcap` writes - was dropped here, so a
	// binary that could bind a privileged port during the build could not in
	// the image built from it (E93).
	xs, err := readXattrs(p)
	if err != nil {
		return fmt.Errorf("read the attributes of %s: %w", rel, err)
	}

	if len(xs) > 0 {
		h.PAXRecords = xs
	}

	// A second name for a file already written is a link, not a second copy.
	// `layer.Take` records that two paths share an inode and the guest's own
	// copy preserves it (E89); an archive that wrote the bytes twice turned
	// `alpine`'s several-hundred-name busybox into several hundred binaries and
	// changed what the layer says.
	if id, ok := hardLinkID(info); ok {
		if first, seen := links[id]; seen {
			h.Typeflag = tar.TypeLink
			h.Linkname = first
			h.Size = 0
		} else {
			links[id] = h.Name
		}
	}

	err = tw.WriteHeader(h)
	if err != nil {
		return fmt.Errorf("write the header for %s: %w", rel, err)
	}

	// A link entry carries no bytes: they are already in the archive under the
	// name it points at. `IsRegular` is still true of a hard link, so the type
	// the header ended up with is what decides, not the file's mode.
	if !info.Mode().IsRegular() || h.Typeflag == tar.TypeLink {
		return nil
	}

	f, err := open(p, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("open %s: %w", rel, err)
	}

	defer func() { _ = f.Close() }()

	_, err = io.Copy(tw, f)
	if err != nil {
		return fmt.Errorf("copy %s: %w", rel, err)
	}

	return nil
}

// countingWriter reports how many bytes went past, which is the size an OCI
// descriptor has to state.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)

	if err != nil {
		return n, fmt.Errorf("write: %w", err)
	}

	return n, nil
}

// open reads a file the image may not have made readable.
//
// Debian ships `/etc/gshadow` with mode 0000 - not readable by anyone, root
// included, because root ignores modes and nobody else has any business with
// it. On Linux this engine runs as root and never notices; on a developer's
// machine it is an ordinary user, and `SAVE IMAGE` failed with "permission
// denied" on a file the image legitimately contains.
//
// Relaxed, read, and put back, which is safe for the reason the unpacker's
// version is: this process owns the tree it is packing. The mode in the archive
// comes from the header, which was read before any of this, so what the image
// declares is unaffected.
func open(p string, perm os.FileMode) (*os.File, error) {
	f, err := os.Open(p) //nolint:gosec // a path inside the directory being packed
	if err == nil || !errors.Is(err, os.ErrPermission) {
		return f, err
	}

	err = os.Chmod(p, perm|0o400)
	if err != nil {
		return nil, err
	}

	f, err = os.Open(p) //nolint:gosec // likewise

	// Put back whatever it was, whether or not the second attempt worked: a
	// file left readable would be a mode this engine invented.
	cerr := os.Chmod(p, perm)
	if cerr != nil && err == nil {
		_ = f.Close()

		return nil, cerr
	}

	return f, err
}
