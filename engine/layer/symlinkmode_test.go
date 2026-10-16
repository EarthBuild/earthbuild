package layer

import (
	"bytes"
	"io/fs"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// encodeOne is one entry through the digest encoding, so a test can ask what
// reaches the hash rather than what the struct happens to hold.
func encodeOne(e entry) []byte {
	var buf bytes.Buffer

	enc := ir.NewEncoder(&buf)
	e.hash(enc, withTimes)

	return buf.Bytes()
}

// TestASymlinksPermissionBitsDoNotReachTheLayerId.
//
// **A symlink has no meaningful mode, and the platforms disagree about what to
// invent.** Linux reports every symlink as 0777 and never consults the bits;
// macOS reports 0755 and has `lchmod`. Measured on both:
//
//	linux   777  lrwxrwxrwx
//	darwin  755  lrwxr-xr-x
//
// So the same layer had two names depending on which machine unpacked it - and
// every base image contains symlinks. A developer on a Mac and CI on Linux could
// never share a cache entry for any of them, which is most of what this engine
// exists to do.
//
// `kindOf` above already normalises the *type* byte "even where a platform
// reports mode bits differently". This is the rest of that sentence.
func TestASymlinksPermissionBitsDoNotReachTheLayerId(t *testing.T) {
	t.Parallel()

	linux := entry{path: "usr/link", mode: uint32(fs.ModeSymlink | 0o777), link: "bin/tool"}
	darwin := entry{path: "usr/link", mode: uint32(fs.ModeSymlink | 0o755), link: "bin/tool"}

	if !bytes.Equal(encodeOne(linux), encodeOne(darwin)) {
		t.Fatal("a symlink unpacked on Linux and on macOS hashes differently,\n" +
			"  so the same image has two layer ids and neither machine can use\n" +
			"  what the other built")
	}
}

// TestARegularFilesPermissionBitsStillReachTheLayerId is the other half, and the
// reason the fix cannot simply drop the mode: an executable bit is a real
// property of a real file, and §3.3 counts it.
func TestARegularFilesPermissionBitsStillReachTheLayerId(t *testing.T) {
	t.Parallel()

	plain := entry{path: "usr/bin/tool", mode: 0o644}
	exec := entry{path: "usr/bin/tool", mode: 0o755}

	if bytes.Equal(encodeOne(plain), encodeOne(exec)) {
		t.Fatal("a file's mode stopped reaching its layer's identity")
	}
}

// TestADirectorysPermissionBitsStillReachTheLayerId: same argument, and the
// case E655 turned on - an undescribed directory's mode is now stated, which is
// only worth stating if it is hashed.
func TestADirectorysPermissionBitsStillReachTheLayerId(t *testing.T) {
	t.Parallel()

	open := entry{path: "usr", mode: uint32(fs.ModeDir | 0o755)}
	shut := entry{path: "usr", mode: uint32(fs.ModeDir | 0o700)}

	if bytes.Equal(encodeOne(open), encodeOne(shut)) {
		t.Fatal("a directory's mode stopped reaching its layer's identity")
	}
}
