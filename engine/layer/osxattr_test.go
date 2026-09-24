package layer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"

	"golang.org/x/sys/unix"
)

// TestAnAttributeTheOperatingSystemAddsDoesNotReachTheLayerId.
//
// **macOS stamps every file it writes with `com.apple.provenance`**, whose value
// is a per-machine constant - identical across processes, binaries and volumes
// here, and absent on Linux entirely. Hashed, it would make every layer this Mac
// unpacks a different layer from the one Linux unpacks from the same bytes.
//
// `assembledBy` already excludes it and says so. This pins the rule at the level
// a caller sees, because the rule was enforced in one function and stated in a
// comment - and a comment is not a thing that fails when somebody writes a
// second reader (which is exactly what happened next: see
// TestTheArchiveReaderDropsTheSameAttributesTheWalkDoes).
func TestAnAttributeTheOperatingSystemAddsDoesNotReachTheLayerId(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	at := filepath.Join(root, "tool")

	err := os.WriteFile(at, []byte("the tool"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Before: whatever this machine put there of its own accord.
	before, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	// **`user.overlay.impure` rather than `com.apple.provenance`**, though the
	// rule covers both. A name outside the `user.` namespace needs privilege on
	// Linux, so the macOS one skips there - and a test that skips is a test that
	// verified nothing on the machine where overlayfs actually writes these.
	err = unix.Lsetxattr(at, "user.overlay.impure", []byte("y"), 0)
	if err != nil {
		t.Skipf("this filesystem will not take the attribute: %v", err)
	}

	after, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	if layer.ManifestID(before) != layer.ManifestID(after) {
		t.Fatalf("an attribute the operating system adds changed the layer's name:"+
			"\n  %v without it\n  %v with it"+
			"\n  no archive carries this and no other machine reproduces it, so a"+
			"\n  layer unpacked here can never be the layer unpacked anywhere else",
			layer.ManifestID(before), layer.ManifestID(after))
	}
}

// TestAnOrdinaryAttributeStillReachesTheLayerId is the other half. A
// `security.capability` is why xattrs are hashed at all: `setcap` on a binary
// lives there, and a layer that dropped it has a service that cannot bind its
// port.
func TestAnOrdinaryAttributeStillReachesTheLayerId(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	at := filepath.Join(root, "tool")

	err := os.WriteFile(at, []byte("the tool"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	before, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	err = unix.Lsetxattr(at, "user.something", []byte("that matters"), 0)
	if err != nil {
		t.Skipf("this filesystem will not take the attribute: %v", err)
	}

	after, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	if layer.ManifestID(before) == layer.ManifestID(after) {
		t.Fatal("an extended attribute stopped reaching its layer's identity")
	}
}
