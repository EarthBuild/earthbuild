package bulk

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// PackTree writes a directory - or a single file - as a tar stream, and says
// how many bytes it wrote.
//
// **A stream and not a filesystem**, which is the whole reason this exists. An
// export leaves a sandbox, and the alternative was a second block device the
// host mounts: that puts a kernel filesystem parser on metadata the sandbox
// authored, which is precisely the surface a VM boundary was added to remove.
// A tar is parsed in userspace, by code that already refuses what it does not
// like.
//
// **Not `image.Pack`**, though it does the same shape of work, because this
// runs in PID 1 of a microVM: importing the image package would put the whole
// store, layer and OCI stack in the initramfs for one function. What that
// package adds - digests, whiteouts, layer identity - an export has no use for.
//
// The order is fixed, so the same tree gives the same bytes. Byte-wise, not
// collation-aware: a locale-dependent order would make the archive depend on
// the language of the machine that wrote it.
func PackTree(root string, w io.Writer) (int64, error) {
	fi, err := os.Lstat(root)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", root, err)
	}

	// **Everything is named under the root's own base name**, a directory as
	// much as a file. The receiver cannot tell the two apart from the stream,
	// so a directory whose contents arrived at the top level and a file that
	// arrived under its name would need different handling on the far side -
	// decided by a question the far side cannot ask.
	//
	// So `SAVE ARTIFACT /out` gives `out/...` and `SAVE ARTIFACT /out.txt`
	// gives `out.txt`, and the receiver joins the base name either way.
	base, self := filepath.Dir(root), filepath.Base(root)
	names := []string{self}

	if fi.IsDir() {
		under, entErr := treeEntries(root)
		if entErr != nil {
			return 0, entErr
		}

		for _, rel := range under {
			names = append(names, path.Join(self, rel))
		}
	}

	counted := &counter{w: w}
	tw := tar.NewWriter(counted)

	for _, rel := range names {
		err = packEntry(tw, base, rel)
		if err != nil {
			return 0, err
		}
	}

	err = tw.Close()
	if err != nil {
		return 0, fmt.Errorf("finish the archive: %w", err)
	}

	return counted.n, nil
}

func treeEntries(root string) ([]string, error) {
	var names []string

	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if p == root {
			return nil
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}

		names = append(names, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}

	sort.Strings(names)

	return names, nil
}

func packEntry(tw *tar.Writer, base, rel string) error {
	at := filepath.Join(base, filepath.FromSlash(rel))

	fi, err := os.Lstat(at)
	if err != nil {
		return fmt.Errorf("read %s: %w", at, err)
	}

	link := ""
	if fi.Mode()&os.ModeSymlink != 0 {
		link, err = os.Readlink(at)
		if err != nil {
			return fmt.Errorf("read the link %s: %w", at, err)
		}
	}

	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return fmt.Errorf("describe %s: %w", at, err)
	}

	// The name in the archive, not on the machine that wrote it. `ModTime` is
	// kept: an export carries the timestamp a published layer was stamped with
	// (I8), and dropping it here would make every exported file "now".
	hdr.Name = rel
	hdr.Uname, hdr.Gname = "", ""

	err = tw.WriteHeader(hdr)
	if err != nil {
		return fmt.Errorf("write the header for %s: %w", rel, err)
	}

	if !fi.Mode().IsRegular() {
		return nil
	}

	f, err := os.Open(at)
	if err != nil {
		return fmt.Errorf("open %s: %w", at, err)
	}

	defer func() { _ = f.Close() }()

	_, err = io.Copy(tw, f)
	if err != nil {
		return fmt.Errorf("copy %s into the archive: %w", at, err)
	}

	return nil
}

// UnpackTree writes a tar stream into a directory.
//
// **Every name is checked**, because the archive was written inside the sandbox
// and its names are the one thing on this path that the untrusted side chose.
// A `../` in an entry is a build writing outside the directory the engine gave
// it, and there is no legitimate export that needs one.
func UnpackTree(r io.Reader, into string) error {
	err := os.MkdirAll(into, 0o750)
	if err != nil {
		return fmt.Errorf("prepare %s: %w", into, err)
	}

	tr := tar.NewReader(r)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}

		if err != nil {
			return fmt.Errorf("read the archive: %w", err)
		}

		at, err := within(into, hdr.Name)
		if err != nil {
			return err
		}

		err = unpackEntry(tr, hdr, at)
		if err != nil {
			return err
		}
	}
}

func unpackEntry(tr *tar.Reader, hdr *tar.Header, at string) error {
	err := os.MkdirAll(filepath.Dir(at), 0o750)
	if err != nil {
		return fmt.Errorf("make room for %s: %w", hdr.Name, err)
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		return mkdirAs(at, hdr)

	case tar.TypeSymlink:
		// Removed first: an export is written into a directory that may hold
		// the previous build's answer, and `Symlink` refuses to replace.
		_ = os.Remove(at)

		err = os.Symlink(hdr.Linkname, at)
		if err != nil {
			return fmt.Errorf("link %s: %w", hdr.Name, err)
		}

		return nil

	case tar.TypeReg:
		return writeReg(tr, hdr, at)

	default:
		return fmt.Errorf("%s is a %q, which an export may not contain"+
			"\n  an artifact is files, directories and symlinks", hdr.Name,
			string(hdr.Typeflag))
	}
}

func mkdirAs(at string, hdr *tar.Header) error {
	err := os.MkdirAll(at, os.FileMode(hdr.Mode).Perm()) //nolint:gosec // a mode from the archive
	if err != nil {
		return fmt.Errorf("make %s: %w", hdr.Name, err)
	}

	// Set explicitly: MkdirAll applies the umask, and an export is meant to
	// come out as it went in.
	err = os.Chmod(at, os.FileMode(hdr.Mode).Perm()) //nolint:gosec // a mode from the archive
	if err != nil {
		return fmt.Errorf("set the mode of %s: %w", hdr.Name, err)
	}

	return nil
}

func writeReg(tr *tar.Reader, hdr *tar.Header, at string) error {
	f, err := os.OpenFile(at, os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
		os.FileMode(hdr.Mode).Perm()) //nolint:gosec // a mode from the archive
	if err != nil {
		return fmt.Errorf("create %s: %w", hdr.Name, err)
	}

	//nolint:gosec // the archive is the guest's own staging, bounded by the device
	_, err = io.Copy(f, tr)
	if err != nil {
		_ = f.Close()

		return fmt.Errorf("write %s: %w", hdr.Name, err)
	}

	err = f.Close()
	if err != nil {
		return fmt.Errorf("finish %s: %w", hdr.Name, err)
	}

	// After the contents, because writing sets it again.
	err = os.Chtimes(at, hdr.ModTime, hdr.ModTime)
	if err != nil {
		return fmt.Errorf("stamp %s: %w", hdr.Name, err)
	}

	return nil
}

// within resolves an entry's name under root, and refuses one that leaves it.
//
// **Refused, not cleaned.** `path.Clean` turns `../escaped` into `escaped`,
// which lands inside the destination and is the usual answer - but it is a
// silent one: the file arrives under a name nobody asked for, and the archive
// that tried to climb out is indistinguishable from one that did not. Nothing
// legitimate produces such a name, so it is a fault to report.
func within(root, name string) (string, error) {
	slashed := strings.ReplaceAll(name, `\`, "/")

	bad := path.IsAbs(slashed)
	for _, seg := range strings.Split(slashed, "/") {
		if seg == ".." {
			bad = true
		}
	}

	if bad {
		return "", fmt.Errorf("the archive names %q, which is outside %s"+
			"\n  an export is written by the sandbox, so its names are the part"+
			" of this a build chooses", name, root)
	}

	return filepath.Join(root, filepath.FromSlash(path.Clean(slashed))), nil
}

// counter counts what passes through it, so a caller learns the archive's size
// without holding it.
type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)

	return n, err //nolint:wrapcheck // the caller's own writer's error
}
