package exec

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// elfHeader writes the smallest file debug/elf will read the machine out of.
//
// Sixty-four bytes of ELF64 header and nothing else. A real binary would do,
// but a test that has to carry one for each architecture it cares about is a
// test that stops being extended.
func elfHeader(t *testing.T, path string, machine elf.Machine) {
	t.Helper()

	b := make([]byte, 64)

	copy(b, []byte{0x7f, 'E', 'L', 'F'})
	b[4] = 2 // ELFCLASS64
	b[5] = 1 // ELFDATA2LSB
	b[6] = 1 // EV_CURRENT

	binary.LittleEndian.PutUint16(b[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(b[18:], uint16(machine))
	binary.LittleEndian.PutUint32(b[20:], uint32(elf.EV_CURRENT))

	err := os.MkdirAll(filepath.Dir(path), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, b, 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

// A cached image is asked what it actually is, not only what its key says.
//
// The image cache is keyed on reference *and* platform, so an entry under the
// arm64 key claims to be an arm64 image. On this machine one is not: the entry
// for `hashicorp/terraform:light` under `linux/arm64` holds an
// `ELF 64-bit ... x86-64` busybox. How it got there is unexplained; that it is
// there is not in doubt.
//
// It matters because the architecture check runs while the configuration is
// fetched, and a cached image is not fetched. So the first build refuses the
// image with a sentence naming both architectures, and every build after it
// gets `fork/exec /bin/sh: exec format error` from the kernel instead - the
// difference being only whether the cache was warm (E28).
//
// The stored configuration cannot answer this: it records Env, Entrypoint and
// Labels, and not the architecture. The tree can.
func TestACachedImageIsAskedWhatItActuallyIs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		machine elf.Machine
		want    string
	}{
		{"an amd64 tree", elf.EM_X86_64, "amd64"},
		{"an arm64 tree", elf.EM_AARCH64, testArch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			elfHeader(t, filepath.Join(root, "bin", "busybox"), tc.machine)

			got, known := archOfTree(root)
			if !known {
				t.Fatal("the tree was not readable")
			}

			if got != tc.want {
				t.Errorf("the tree reads as %q, want %q", got, tc.want)
			}
		})
	}
}

// A tree that says nothing about itself is trusted, which is the rule the
// architecture check already follows for an image that declares nothing.
//
// Refusing what cannot be read would refuse every scratch image, every
// distroless one, and anything whose entrypoint is a script - none of which is
// evidence of anything being wrong.
func TestATreeWithNothingToReadIsNotRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("not an elf"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, known := archOfTree(root)
	if known {
		t.Error("a tree with no binary in it was given an architecture")
	}
}

// A cache entry that contradicts its own key is thrown away and fetched again.
//
// Serving it would be serving an image the key says this machine can run and
// the file says it cannot, which is how `fork/exec /bin/sh: exec format error`
// arrives in place of a sentence naming both architectures. Refusing outright
// would be worse than either: the entry is a *cache*, and a cache that has gone
// wrong is meant to be discarded, not to end builds until someone deletes it by
// hand.
//
// Fetching again also puts the question back where it can be answered properly.
// The pull checks what the registry declares, so an image that really is amd64
// is refused with the message that names both architectures, and one that was
// merely mis-keyed is simply fetched correctly.
func TestACacheEntryThatContradictsItsKeyIsDiscarded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const ref = "example.test/thing:1"

	// A populated entry under the arm64 key, holding an amd64 tree.
	entry := filepath.Join(root, "imagecache", ImageCacheKey(ref, testPlatform))
	elfHeader(t, filepath.Join(entry, "bin", "busybox"), elf.EM_X86_64)

	pulled := 0

	pull := func(_ context.Context, _, dir string) (ocispec.ImageConfig, error) {
		pulled++

		// What a correct pull leaves behind, so the entry that replaces the
		// discarded one is the right architecture.
		elfHeader(t, filepath.Join(dir, "bin", "busybox"), elf.EM_AARCH64)

		return ocispec.ImageConfig{}, nil
	}

	err := fetchImageFrom(context.Background(), root, ref, testPlatform,
		filepath.Join(t.TempDir(), "dest"), pull)
	if err != nil {
		t.Fatal(err)
	}

	if pulled != 1 {
		t.Errorf("the contradictory entry was served instead of refetched (%d pulls)", pulled)
	}

	if got, _ := archOfTree(entry); got != testArch {
		t.Errorf("the entry is still %q after refetching", got)
	}
}

// An entry that agrees with its key is served, and nothing is fetched.
//
// The check has to be free in the ordinary case or it is a tax on every build
// to defend against a state nobody has explained.
func TestAnAgreeingCacheEntryIsNotRefetched(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const ref = "example.test/thing:2"

	entry := filepath.Join(root, "imagecache", ImageCacheKey(ref, testPlatform))
	elfHeader(t, filepath.Join(entry, "bin", "busybox"), elf.EM_AARCH64)

	pulled := 0

	pull := func(_ context.Context, _, _ string) (ocispec.ImageConfig, error) {
		pulled++

		return ocispec.ImageConfig{}, nil
	}

	err := fetchImageFrom(context.Background(), root, ref, testPlatform,
		filepath.Join(t.TempDir(), "dest"), pull)
	if err != nil {
		t.Fatal(err)
	}

	if pulled != 0 {
		t.Errorf("a good entry was fetched again (%d pulls)", pulled)
	}
}
