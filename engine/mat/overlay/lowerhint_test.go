package overlay

import (
	"fmt"
	"strings"
	"testing"
)

// A mount that fails with ENOENT says which of the two causes it was.
//
// The kernel says "no such file or directory" and names nothing, and the two
// things that produce it here want opposite responses: a missing layer is a
// build referring to a step whose result was never written, and an over-long
// option string is a stack that has to be flattened. Guessing between them
// costs an afternoon, which is what this is here to stop.
func TestAFailedMountSaysWhichCause(t *testing.T) {
	t.Parallel()

	// The real shape: the guest's store, and identities that are 64 hex
	// characters because that is what a BLAKE3-256 digest is.
	const root = "/var/lib/earthbuild/layers/"

	layers := func(n int) []string {
		out := make([]string, 0, n)
		for i := range n {
			out = append(out, root+strings.Repeat(fmt.Sprintf("%x", i%16), 64))
		}

		return out
	}

	all := func(string) bool { return true }

	t.Run("a stack whose paths do not fit in a page", func(t *testing.T) {
		t.Parallel()

		lower := layers(60)
		opts := mountOptions(lower, "/var/lib/earthbuild/scratch/mounts/h000038/upper",
			"/var/lib/earthbuild/scratch/mounts/h000038/work", false)

		if len(opts) <= maxMountOptions {
			t.Fatalf("the fixture fits after all (%d bytes), so it tests nothing", len(opts))
		}

		hint := lowerHint(opts, lower, all)
		if !strings.Contains(hint, "arrived cut in half") {
			t.Errorf("the length was not diagnosed:\n%s", hint)
		}

		if !strings.Contains(hint, "not overlayfs's own limit") {
			t.Errorf("the hint does not distinguish this from the layer-count wall:\n%s", hint)
		}
	})

	t.Run("a layer that is not in the store", func(t *testing.T) {
		t.Parallel()

		lower := layers(4)
		opts := mountOptions(lower, "/scratch/upper", "/scratch/work", false)

		missing := lower[2]
		hint := lowerHint(opts, lower, func(p string) bool { return p != missing })

		if !strings.Contains(hint, missing) {
			t.Errorf("the missing layer is not named:\n%s", hint)
		}

		if !strings.Contains(hint, "3 of 4") {
			t.Errorf("the hint does not say where in the stack it was:\n%s", hint)
		}
	})

	t.Run("nothing wrong that this can see", func(t *testing.T) {
		t.Parallel()

		lower := layers(4)
		opts := mountOptions(lower, "/scratch/upper", "/scratch/work", false)

		if hint := lowerHint(opts, lower, all); hint != "" {
			t.Errorf("a hint was invented for a mount with nothing detectably wrong:\n%s", hint)
		}
	})
}

// The stack that failed does not fit, and the short names are what make it fit.
//
// `+earthly` in this repository asks for 41 layers, which the engine reported
// as 4140 bytes of options against a limit of 4095 - **41**, against a
// MaxStackDepth of 480. The two limits were an order of magnitude apart and
// only one of them was written down.
//
// The first version of this test guessed the path lengths and concluded that 41
// layers fit. It passed, and it was wrong: the guess was 91 bytes where the
// engine measured 98. So the numbers here are the engine's own, and the test
// exists to keep the fix honest rather than to re-derive the arithmetic.
func TestShortNamesAreWhatMakeARealStackFit(t *testing.T) {
	t.Parallel()

	// Measured, not assumed: the guest's store is at /var/lib/earthbuild and a
	// layer is named by a 64-character digest.
	const (
		layerRoot = "/var/lib/earthbuild/store/layers/"
		farmRoot  = "/var/lib/earthbuild/scratch/l/"
		scratch   = "/var/lib/earthbuild/scratch/mounts/h000038/"
	)

	long := make([]string, 0, 41)
	short := make([]string, 0, 41)

	for i := range 41 {
		id := strings.Repeat(fmt.Sprintf("%x", i%16), 64)
		long = append(long, layerRoot+id)
		short = append(short, farmRoot+id[:shortNameLen])
	}

	was := mountOptions(long, scratch+"upper", scratch+"work", false)
	now := mountOptions(short, scratch+"upper", scratch+"work", false)

	t.Logf("41 layers: %d bytes by full name, %d by short name, limit %d",
		len(was), len(now), maxMountOptions)

	if len(was) <= maxMountOptions {
		t.Errorf("the stack that failed now fits by full name (%d bytes), so this test has"+
			" stopped describing the defect it was written for", len(was))
	}

	if len(now) > maxMountOptions {
		t.Errorf("short names do not make it fit either: %d bytes", len(now))
	}
}

// How deep a stack can go before the paths, rather than overlayfs, stop it.
//
// Recorded as a number because it is the honest limit and it is nowhere near
// MaxStackDepth. Flattening (Φ) is the answer when a build exceeds it, and the
// mount says so rather than reporting ENOENT.
func TestTheDepthTheOptionPageAllows(t *testing.T) {
	t.Parallel()

	const (
		farmRoot = "/var/lib/earthbuild/scratch/l/"
		scratch  = "/var/lib/earthbuild/scratch/mounts/h000038/"
	)

	fits := 0

	for n := 1; n <= 600; n++ {
		lower := make([]string, 0, n)
		for i := range n {
			lower = append(lower, farmRoot+strings.Repeat(fmt.Sprintf("%x", i%16), shortNameLen))
		}

		if len(mountOptions(lower, scratch+"upper", scratch+"work", false)) <= maxMountOptions {
			fits = n
		}
	}

	t.Logf("the option page allows %d layers with short names; overlayfs itself allows 500,"+
		" and the engine flattens at %d", fits, 480)

	// A guard rather than an assertion about the exact number: the point is that
	// the path budget is the binding limit, and that it is not so small that
	// ordinary builds meet it. `+earthly` needs 41.
	if fits < 80 {
		t.Errorf("only %d layers fit in the option page, which ordinary targets will exceed", fits)
	}
}
