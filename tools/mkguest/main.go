// Command mkguest builds the guest artefacts a microVM sandbox boots: an
// initramfs holding `earth-vmboot` as /init and `earth-guestd` beside it.
//
// The kernel is not built here. Firecracker boots an uncompressed ELF vmlinux
// and a distribution ships a bzImage, so that artefact comes from elsewhere -
// see docs/native/settings.md.
//
//	go run ./tools/mkguest -o out/vm
package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	out := flag.String("o", "out/vm", "where to write the artefacts")
	arch := flag.String("arch", "amd64", "the guest's GOARCH")
	flag.Parse()

	err := run(*out, *arch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkguest: %v\n", err)
		os.Exit(1)
	}
}

func run(out, arch string) error {
	root, err := os.MkdirTemp("", "mkguest")
	if err != nil {
		return fmt.Errorf("make a directory for the guest root: %w", err)
	}

	defer func() { _ = os.RemoveAll(root) }()

	err = build(root, arch)
	if err != nil {
		return err
	}

	// The mount points PID 1 needs. They have to exist before anything is
	// mounted on them, and an initramfs holds only what was packed into it: a
	// missing /proc is an ENOENT from `mount` that reads as a kernel without
	// procfs, which is where two rounds of this went (E971).
	for _, d := range []string{"proc", "sys", "dev", "tmp", "run", "store"} {
		err = os.Mkdir(filepath.Join(root, d), 0o755)
		if err != nil {
			return fmt.Errorf("make the guest's %s: %w", d, err)
		}
	}

	err = os.MkdirAll(out, 0o755)
	if err != nil {
		return fmt.Errorf("make %s: %w", out, err)
	}

	at := filepath.Join(out, "initrd.cpio.gz")

	err = pack(at, root)
	if err != nil {
		return err
	}

	fmt.Printf("initrd: %s\n"+
		"  set EARTH_VM_INITRD to it, EARTH_VM_KERNEL to an uncompressed vmlinux\n", at)

	return nil
}

// build puts the two binaries in the guest root.
//
// **Static, because the initramfs is the whole filesystem** until /store is
// mounted: there is no dynamic loader in it and nothing to find one in.
// `-trimpath` and an empty build id so two builds of one commit agree - the
// build id is a hash of the linker inputs and includes their paths.
func build(root, arch string) error {
	for at, pkg := range map[string]string{
		"init":         "./cmd/earth-vmboot",
		"earth-guestd": "./cmd/earth-guestd",
	} {
		//nolint:gosec,noctx // the argv is this file's; a build tool needs no deadline
		cmd := osexec.Command("go", "build", "-trimpath",
			"-ldflags", "-s -w -buildid=", "-o", filepath.Join(root, at), pkg)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
		cmd.Stderr = os.Stderr

		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("build %s for linux/%s: %w", pkg, arch, err)
		}
	}

	return nil
}

func pack(at, root string) error {
	f, err := os.Create(at)
	if err != nil {
		return fmt.Errorf("create %s: %w", at, err)
	}

	defer func() { _ = f.Close() }()

	// Level 9 and no name or timestamp in the header, which is the rest of what
	// makes the file reproducible.
	z, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("compress %s: %w", at, err)
	}

	err = writeCPIO(z, root)
	if err != nil {
		return err
	}

	err = z.Close()
	if err != nil {
		return fmt.Errorf("finish %s: %w", at, err)
	}

	return f.Close()
}

// writeCPIO writes root as a newc archive, which is the one format the Linux
// initramfs loader reads.
//
// **Written here rather than shelled out to `cpio`.** BSD cpio puts each file's
// real inode number in the header, so packing one tree from two temporary
// directories gives two different archives; GNU cpio has `--reproducible` and
// macOS does not have GNU cpio. Numbering the entries from 1 in sorted order
// costs a dozen lines and makes the archive a function of its input.
func writeCPIO(w io.Writer, root string) error {
	names, err := entries(root)
	if err != nil {
		return err
	}

	for i, name := range names {
		err = writeEntry(w, root, name, i+1)
		if err != nil {
			return err
		}
	}

	// The trailer, which is how the loader knows it has the whole archive: a
	// kernel that does not find one treats the initramfs as truncated and
	// mounts nothing.
	return writeHeader(w, "TRAILER!!!", 0, 0, 0, 0)
}

// entries is every path under root, sorted, relative and slash-separated.
func entries(root string) ([]string, error) {
	var names []string

	err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." {
			return err
		}

		names = append(names, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk the guest root: %w", err)
	}

	sort.Strings(names)

	return names, nil
}

// newc field widths and the modes the loader needs to tell a directory from a
// file. Symlinks are not written: nothing in a guest root is one, and a format
// that silently drops what it cannot express is worse than one that says so.
const (
	modeDir  = 0o040000
	modeFile = 0o100000
)

func writeEntry(w io.Writer, root, name string, ino int) error {
	at := filepath.Join(root, filepath.FromSlash(name))

	fi, err := os.Lstat(at)
	if err != nil {
		return fmt.Errorf("read %s: %w", at, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, which this does not write"+
			"\n  put the target in the guest root instead", name)
	}

	mode := modeFile | uint32(fi.Mode().Perm())
	size := fi.Size()

	if fi.IsDir() {
		mode, size = modeDir|uint32(fi.Mode().Perm()), 0
	}

	// The kernel wants paths without a leading slash, and each name is
	// NUL-terminated inside its own padded field.
	err = writeHeader(w, name, mode, ino, size, 1)
	if err != nil {
		return err
	}

	if fi.IsDir() {
		return nil
	}

	f, err := os.Open(at)
	if err != nil {
		return fmt.Errorf("open %s: %w", at, err)
	}

	defer func() { _ = f.Close() }()

	n, err := io.Copy(w, f)
	if err != nil {
		return fmt.Errorf("copy %s into the archive: %w", at, err)
	}

	return pad(w, n)
}

// writeHeader writes one 110-byte newc header and its padded name.
//
// **Owner, times and link counts are constants.** They are what a build would
// otherwise inherit from the machine it ran on, and the guest runs everything
// as root regardless: mtime 0 rather than now, uid and gid 0 rather than the
// builder's.
func writeHeader(w io.Writer, name string, mode uint32, ino int, size int64, nlink int) error {
	var b strings.Builder

	b.WriteString("070701")

	for _, v := range []int64{
		int64(ino), int64(mode), 0, 0, int64(nlink), 0, size,
		0, 0, 0, 0, // device and rdev major/minor
		int64(len(name) + 1),
		0, // check, unused by newc
	} {
		fmt.Fprintf(&b, "%08X", uint32(v)) //nolint:gosec // newc fields are 32-bit by definition
	}

	b.WriteString(name)
	b.WriteByte(0)

	_, err := io.WriteString(w, b.String())
	if err != nil {
		return fmt.Errorf("write the header for %s: %w", name, err)
	}

	// The header and name together are padded to four bytes, and the file's
	// contents start on that boundary.
	return pad(w, int64(b.Len()))
}

// pad rounds the stream up to a four-byte boundary, which newc requires after
// every name and every file.
func pad(w io.Writer, n int64) error {
	if n%4 == 0 {
		return nil
	}

	_, err := w.Write(make([]byte, 4-n%4))
	if err != nil {
		return fmt.Errorf("pad the archive: %w", err)
	}

	return nil
}
