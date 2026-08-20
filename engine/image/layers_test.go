package image_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// layerTar builds a tar of the entries given, as a layer would carry them.
func layerTar(t *testing.T, entries map[string]string) *bytes.Reader {
	t.Helper()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for name, body := range entries {
		err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = tw.Write([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	return bytes.NewReader(buf.Bytes())
}

// A later layer replaces a file an earlier one wrote.
//
// This is what layering *is*, and the engine could not do it: unpacking refused
// with "create \\"etc/apk/world\\": file exists". Only single-layer images worked,
// which is why alpine was fine and `node:20-alpine` was not - and almost every
// real base image is the second kind.
func TestALaterLayerReplacesAnEarlierFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := image.Unpack(layerTar(t, map[string]string{testConfigPath: "first\n"}), dir)
	if err != nil {
		t.Fatalf("the first layer: %v", err)
	}

	err = image.Unpack(layerTar(t, map[string]string{testConfigPath: "second\n"}), dir)
	if err != nil {
		t.Fatalf("the second layer: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "etc", testConfigField)) //nolint:gosec // a fixture this test wrote
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "second\n" {
		t.Errorf("the file holds %q, want the later layer's content", b)
	}
}

// Within one layer, a repeated entry is still a malformed archive.
//
// The distinction is the whole of this: across layers an overwrite is the
// format working, and within one it is an archive that cannot be trusted to
// mean anything - last-writer-wins would be a guess about which entry was
// intended.
func TestARepeatedEntryInOneLayerIsStillRefused(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for _, body := range []string{"first\n", "second\n"} {
		err := tw.WriteHeader(&tar.Header{
			Name: testConfigPath, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = tw.Write([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = image.Unpack(bytes.NewReader(buf.Bytes()), t.TempDir())
	if err == nil {
		t.Error("a layer naming one path twice was accepted")
	}
}

// A file may replace a directory, which docker's own images do.
func TestAFileMayReplaceADirectoryFromAnEarlierLayer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := image.Unpack(layerTar(t, map[string]string{"opt/thing/inner": "x\n"}), dir)
	if err != nil {
		t.Fatal(err)
	}

	err = image.Unpack(layerTar(t, map[string]string{"opt/thing": "now a file\n"}), dir)
	if err != nil {
		t.Fatalf("replacing a directory with a file: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, "opt", "thing")) //nolint:gosec // a fixture this test wrote
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "now a file\n" {
		t.Errorf("the path holds %q", b)
	}
}

// A later layer cannot write through a symlink an earlier one planted.
//
// The escape itself was already refused - safePath resolves an entry's parent
// and rejects anything landing outside the layer - but that covers ancestors,
// not the last component. A leaf symlink is what a malicious image would use:
// layer one writes `config -> /etc/passwd`, layer two writes a regular file
// called `config`, and an unpacker that opened it without care would write
// the archive's contents to /etc/passwd with the build's privileges.
//
// Two things stop it, deliberately: the entry being replaced is removed rather
// than opened, and os.Remove does not follow a symlink; and the create itself
// is O_NOFOLLOW, so the guarantee is a property of the open rather than of the
// removal having happened first.
func TestALayerCannotWriteThroughAPlantedSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	outside := filepath.Join(t.TempDir(), "victim")
	err := os.WriteFile(outside, []byte("original\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Layer one: a symlink pointing out of the layer.
	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)
	err = tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink, Name: testConfigField, Linkname: outside, Mode: 0o777,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = image.Unpack(bytes.NewReader(buf.Bytes()), dir)
	if err != nil {
		t.Fatalf("the symlink layer: %v", err)
	}

	// Layer two: a regular file of the same name.
	err = image.Unpack(layerTar(t, map[string]string{testConfigField: "attacker\n"}), dir)
	// Either outcome is acceptable - refuse the entry, or replace the symlink
	// with a real file. What is not acceptable is the write landing outside.
	if err != nil {
		t.Logf("the entry was refused: %v", err)
	}

	b, readErr := os.ReadFile(outside) //nolint:gosec // a fixture this test wrote
	if readErr != nil {
		t.Fatal(readErr)
	}

	if string(b) != "original\n" {
		t.Errorf("a layer wrote through a symlink to a file outside it: %q", b)
	}
}

// A layer may contain a directory nothing may write to, and still unpack.
//
// `maven:3.8.5-openjdk-17` ships `usr/bin` with a mode that denies writing, and
// the files inside it come *after* it in the archive - so applying the mode when
// the directory is created made every one of them fail with "permission denied".
// Docker's own images do this and the format allows it: a directory's mode
// describes the image, not the unpacking of it.
//
// The modes are applied at the end instead, deepest first, so a restrictive
// parent is never in the way of its own contents.
func TestALayerMayHaveAnUnwritableDirectory(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir, Name: "usr/bin/", Mode: 0o555,
	})
	if err != nil {
		t.Fatal(err)
	}

	const body = "#!/bin/sh\n"

	err = tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "usr/bin/tool", Mode: 0o755, Size: int64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Not t.TempDir: its cleanup is os.RemoveAll, which cannot delete a file
	// inside a directory that denies writing - which is the whole point of this
	// layer. image.RemoveAll exists for the same reason.
	//nolint:usetesting // t.TempDir cannot clean this up - see above
	dir, err := os.MkdirTemp("", "earthbuild-readonly-*")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = image.RemoveAll(dir) })

	err = image.Unpack(bytes.NewReader(buf.Bytes()), dir)
	if err != nil {
		t.Fatalf("a layer with a read-only directory did not unpack: %v", err)
	}

	_, err = os.Stat(filepath.Join(dir, "usr", "bin", "tool"))
	if err != nil {
		t.Fatalf("the file inside it is missing: %v", err)
	}

	// And the mode the image declared is what the directory ends up with.
	fi, err := os.Stat(filepath.Join(dir, "usr", "bin"))
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode().Perm() != 0o555 {
		t.Errorf("the directory ended up %o, want 555", fi.Mode().Perm())
	}
}

// A later layer writes into a directory an earlier one left read-only.
//
// This is the same problem as within one layer and needs its own answer: the
// mode was applied at the end of the earlier layer, so by the time the next one
// arrives the directory really is read-only on disk. `maven:3.8.5-openjdk-17`
// does exactly this - `usr/bin` at 0555 in layer 0, and more binaries added in
// layer 1.
//
// The directory is made writable for the write and put back afterwards, so the
// image ends up with the mode it declared and the unpacking is not blocked by
// it.
func TestALaterLayerWritesIntoAReadOnlyDirectory(t *testing.T) {
	t.Parallel()

	//nolint:usetesting // t.TempDir cannot clean this up - see above
	dir, err := os.MkdirTemp("", "earthbuild-readonly-*")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = image.RemoveAll(dir) })

	var first bytes.Buffer

	tw := tar.NewWriter(&first)
	err = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "usr/bin/", Mode: 0o555})
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = image.Unpack(bytes.NewReader(first.Bytes()), dir)
	if err != nil {
		t.Fatalf("the first layer: %v", err)
	}

	err = image.Unpack(layerTar(t, map[string]string{"usr/bin/tool": "x\n"}), dir)
	if err != nil {
		t.Fatalf("the second layer: %v", err)
	}

	_, err = os.Stat(filepath.Join(dir, "usr", "bin", "tool"))
	if err != nil {
		t.Fatalf("the file the later layer added is missing: %v", err)
	}

	fi, err := os.Stat(filepath.Join(dir, "usr", "bin"))
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode().Perm() != 0o555 {
		t.Errorf("the directory was left %o, want the 555 the image declared", fi.Mode().Perm())
	}
}

// A layer containing an unreadable file is still packed.
//
// Debian ships `/etc/gshadow` with mode 0000: not readable by anyone, root
// included, because root ignores modes and nobody else has any business with
// it. On Linux the engine runs as root and never notices; on a developer's
// machine it is an ordinary user, and `SAVE IMAGE` failed with "permission
// denied" on a file the image legitimately contains.
//
// The file is made readable, read, and put back - the same relax-and-restore
// the unpacker does, and safe for the same reason: this process owns the tree.
func TestALayerWithAnUnreadableFileIsPacked(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.MkdirAll(filepath.Join(dir, "etc"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	secret := filepath.Join(dir, "etc", "gshadow")
	err = os.WriteFile(secret, []byte("root:*::\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Chmod(secret, 0o000)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(secret, 0o600) })

	var buf bytes.Buffer

	_, _, err = image.Pack(dir, &buf)
	if err != nil {
		t.Fatalf("a layer with an unreadable file was not packed: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("the archive is empty")
	}

	// The mode is the image's and must survive being read.
	fi, err := os.Stat(secret)
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode().Perm() != 0 {
		t.Errorf("the file was left at %o, want the 000 it had", fi.Mode().Perm())
	}
}

// A layer may name its own root, and busybox's does.
//
// A tar built with `tar -C rootfs .` begins with an entry called `./`, and
// resolving it gives the unpack root itself - whose *parent* is outside the
// layer, which is what the escape check looks at. So the check refused the one
// entry that cannot possibly escape: the root.
//
// `busybox:1.38.0` could not be pulled at all, and the diagnosis said the layer
// wrote through a symlink out of itself.
func TestALayerMayNameItsOwnRoot(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for _, h := range []*tar.Header{
		{Typeflag: tar.TypeDir, Name: "./", Mode: 0o755},
		{Typeflag: tar.TypeDir, Name: "./bin/", Mode: 0o755},
	} {
		err := tw.WriteHeader(h)
		if err != nil {
			t.Fatal(err)
		}
	}

	const body = "x\n"

	err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "./bin/sh", Mode: 0o755, Size: int64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tw.Write([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	err = tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	err = image.Unpack(bytes.NewReader(buf.Bytes()), dir)
	if err != nil {
		t.Fatalf("a layer naming its own root did not unpack: %v", err)
	}

	_, err = os.Stat(filepath.Join(dir, "bin", "sh"))
	if err != nil {
		t.Errorf("the layer's contents are missing: %v", err)
	}
}

// Two paths differing only in case are refused, naming both.
//
// The layer store on a developer's Mac is case-insensitive by default, so an
// image containing `Foo` and `foo` loses one - and the survivor has the other's
// contents under its own name, which is a wrong image produced silently. Node
// and TypeScript packages collide this way often enough that it is not a
// curiosity.
//
// Refused rather than warned: this filesystem cannot represent the image, and
// building on a wrong one is worse than not building.
func TestPathsDifferingOnlyInCaseAreRefused(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for _, name := range []string{"usr/Foo", testLibPath} {
		const body = "x\n"

		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(body)),
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = tw.Write([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	// Asked of the filesystem rather than inferred from the outcome: an
	// earlier version of this test skipped when Unpack returned no error, which
	// it did on *both* kinds - because the detection under test did not exist
	// yet. A skip that fires when the feature is missing tests nothing.
	if caseSensitive(t, dir) {
		t.Skip("this filesystem is case-sensitive, so the image unpacks correctly here")
	}

	err = image.Unpack(bytes.NewReader(buf.Bytes()), dir)
	if err == nil {
		t.Fatal("two paths differing only in case were accepted on a case-insensitive filesystem")
	}

	for _, want := range []string{"usr/Foo", testLibPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q:\n%s", want, err)
		}
	}

	// No path in this message, on purpose. Unpack is given a directory to
	// write into, which during a pull is a staging directory that is deleted
	// before anyone reads the error - naming it named nothing, and naming the
	// deepest directory inside it pointed at `.pulling-3744278413/usr/lib/
	// xtables`: true, precise, and impossible to act on. Where the unpack was
	// really happening is the caller's knowledge, and the caller says it.
	if strings.Contains(err.Error(), filepath.Join(dir, "usr")) {
		t.Errorf("the refusal names a path inside the unpack:\n%s", err)
	}
}

// One path written twice in the same case is still the malformed-archive case,
// and says so rather than blaming the filesystem.
func TestARepeatedPathIsNotBlamedOnTheFilesystem(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for range 2 {
		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: testLibPath, Mode: 0o644, Size: 0,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = image.Unpack(bytes.NewReader(buf.Bytes()), t.TempDir())
	if err == nil {
		t.Fatal("a layer naming one path twice was accepted")
	}

	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("the refusal reads as a case collision rather than a malformed archive:\n%s", err)
	}
}

// caseSensitive reports whether a directory distinguishes Foo from foo.
func caseSensitive(t *testing.T, dir string) bool {
	t.Helper()

	lower := filepath.Join(dir, ".case-probe")
	err := os.WriteFile(lower, []byte("l"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = os.Remove(lower) }()

	upper := filepath.Join(dir, ".CASE-PROBE")
	err = os.WriteFile(upper, []byte("u"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = os.Remove(upper) }()

	b, err := os.ReadFile(lower) //nolint:gosec // a fixture this test wrote
	if err != nil {
		t.Fatal(err)
	}

	return string(b) == "l"
}
