// Package overlay is the Linux core.Materialiser: overlayfs over layer
// directories.
//
// It is the second implementation of the port, and the reason the conformance
// suite exists. Writing it found a hole in the suite: "reversed stacks produce
// different roots" is trivially true of any implementation that gives each
// handle its own merged directory, so it never checked that layer order was
// honoured at all.
package overlay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/timing"
	"golang.org/x/sys/unix"
)

// Materialiser mounts layer stacks with overlayfs.
//
// Requires CAP_SYS_ADMIN: mounting fails with EPERM unprivileged, which
// experiment E13 established and which is why rootless is a named deferred item
// rather than an oversight.
type Materialiser struct {
	root string
	// scratch holds per-step upper and work directories. Separate from root
	// because layers arrive over a shared mount and overlayfs cannot use one as
	// an upper layer - it falls back to a read-only mount, and the step's first
	// write fails somewhere that looks nothing like the cause.
	scratch string

	// tmpfs is the mount options the scratch was given, or empty. Kept only so a
	// step that runs out of space can be told why the space was small (E406).
	tmpfs string

	// tr turns a layer's portable deletion markers into what overlayfs reads,
	// and remembers which layers it has already done.
	trMu sync.Mutex
	tr   *translator
}

// New prepares a materialiser with layers and scratch under one directory,
// which is correct whenever both are on the same local filesystem.
func New(dir string) (*Materialiser, error) { return NewSplit(dir, dir) }

// NewSplit prepares a materialiser whose layers and scratch are separate.
//
// Use it whenever layers arrive over a shared mount: the layer store may then be
// read-only to the step, which is both a correctness property - a step cannot
// corrupt the cache it is reading - and a practical necessity, since overlayfs
// refuses such a filesystem as an upper layer.
func NewSplit(layerDir, scratchDir string) (*Materialiser, error) {
	err := os.MkdirAll(filepath.Join(layerDir, "layers"), 0o755)
	if err != nil {
		return nil, fmt.Errorf("prepare the layer store: %w", err)
	}

	err = os.MkdirAll(scratchDir, 0o755)
	if err != nil {
		return nil, fmt.Errorf("prepare the scratch directory: %w", err)
	}

	// A tmpfs over the scratch, if the operator asked for one. Mounted before
	// anything is created under it, because a mount hides what is already there
	// and a half-populated scratch would be half-invisible.
	//
	// Worth a quarter of a build's wall clock and costing memory, so it is an
	// opt-in with a size (E406). Nothing unmounts it: this process is the guest,
	// it dies with its mount namespace, and the mount dies with it.
	opts, err := scratchTmpfsOptions(os.Getenv(EnvScratchTmpfs))
	if err != nil {
		return nil, err
	}

	if opts != "" {
		err = unix.Mount("tmpfs", scratchDir, "tmpfs", 0, opts)
		if err != nil {
			return nil, fmt.Errorf("%s asked for a tmpfs scratch at %s: %w",
				EnvScratchTmpfs, scratchDir, err)
		}
	}

	err = os.MkdirAll(filepath.Join(scratchDir, "mounts"), 0o755)
	if err != nil {
		return nil, fmt.Errorf("prepare the scratch directory: %w", err)
	}

	return &Materialiser{root: layerDir, scratch: scratchDir, tmpfs: opts}, nil
}

// ErrUnavailable reports that this machine cannot mount overlayfs at all.
//
// Distinct from ErrUnsupported, which is the wrong *platform*: this is the
// right platform, unable. No CAP_SYS_ADMIN (E13), or a working directory
// already on overlayfs, which overlayfs refuses to stack on and which is the
// state of nearly every container's root.
var ErrUnavailable = errors.New("overlayfs cannot be mounted here")

// Available reports whether stacks can be mounted under dir, by mounting one.
//
// A trial rather than a set of checks: the conditions are the kernel's, and
// asking it is the only way to be right about them. Returns nil when a mount
// works, an error wrapping ErrUnavailable when the machine is the reason, and
// anything else when it is not - so a caller can tell "not here" from "broken",
// which is the distinction that stops every failure being laundered into a skip.
func Available(dir string) error {
	m, err := New(dir)
	if err != nil {
		return err
	}

	h, err := m.Materialise(context.Background(), []ir.NodeID{{}})
	if err != nil {
		return err
	}

	return h.Release()
}

func (m *Materialiser) layerDir(id ir.NodeID) string {
	return filepath.Join(m.root, "layers", id.String())
}

// farmDir is where the short names live. Under scratch, because the layer store
// is read-only to the guest whenever it arrives over a shared mount.
func (m *Materialiser) farmDir() string { return filepath.Join(m.scratch, "l") }

// WriteLayer implements coretest.LayerBuilder, so the content-level conformance
// tests apply to this implementation. It is test and import support - a real
// layer arrives from Δ (green paper §4.6), not from a map of strings.
func (m *Materialiser) WriteLayer(id ir.NodeID, files map[string]string) error {
	dir := m.layerDir(id)
	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		return fmt.Errorf("create layer dir: %w", err)
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err != nil {
			return fmt.Errorf("create layer subdir: %w", err)
		}

		err = os.WriteFile(path, []byte(content), 0o644)
		if err != nil {
			return fmt.Errorf("write layer file: %w", err)
		}
	}

	return nil
}

// Materialise mounts the stack and returns a handle to the merged view.
//
// The stack arrives oldest-first, as green paper §3.2 defines it. overlayfs
// reads lowerdir the other way round - leftmost is the *highest* layer - so the
// list is reversed on the way in. Getting this backwards produces a filesystem
// that looks correct until two layers touch the same path, which is exactly
// what the conformance suite's upperLayerWins now catches.
func (m *Materialiser) Materialise(ctx context.Context, stack []ir.NodeID) (core.Handle, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}

	// Asked for, not derived. See mountName.
	base, err := os.MkdirTemp(filepath.Join(m.scratch, "mounts"), mountPrefix)
	if err != nil {
		return nil, fmt.Errorf("create a mount directory: %w", err)
	}

	merged := filepath.Join(base, "merged")
	upper := filepath.Join(base, "upper")
	work := filepath.Join(base, "work")

	for _, d := range []string{merged, upper, work} {
		err := os.MkdirAll(d, 0o755)
		if err != nil {
			return nil, fmt.Errorf("create mount dirs: %w", err)
		}
	}

	// Scratch: no lower layers, so there is nothing to overlay. A plain
	// directory is the correct answer, and overlayfs would refuse anyway.
	if len(stack) == 0 {
		return &handle{root: upper, upper: upper, base: base}, nil
	}

	endStack := timing.Phase("mat:stack", fmt.Sprintf("%d layers", len(stack)))

	lower := make([]string, 0, len(stack))
	for i := len(stack) - 1; i >= 0; i-- {
		id := stack[i]

		dir := m.layerDir(id)

		err := os.MkdirAll(dir, 0o755)
		if err != nil {
			return nil, fmt.Errorf("prepare layer: %w", err)
		}

		// A layer that records a deletion is translated onto storage this VM
		// owns, because a `.wh.` marker is what the shared store can hold and a
		// character device is what overlayfs reads (E94). Layers without one -
		// nearly all of them - are stacked from the store directly.
		dir, err = m.translator().use(dir, id.String())
		if err != nil {
			return nil, err
		}

		// Named short, because every byte of this path is charged against the
		// one page of options the kernel will read. See link().
		lower = append(lower, link(m.farmDir(), dir, id.String()))
	}

	endStack()

	// Named through /proc/self/fd where that is available, which makes the
	// option budget independent of how deep the store is. See byDescriptor.
	named, closeNamed := lower, func() {}
	shortened := procIsMounted()

	if shortened {
		named, closeNamed, shortened = byDescriptor(lower)
	}

	defer closeNamed()

	opts := mountOptions(named, upper, work, needsUserXattr(filepath.Dir(upper)))

	// Refused here rather than diagnosed after the fact: the kernel's answer to
	// an over-long option string is to truncate it and report ENOENT for the
	// half-a-path at the end, which names neither the length nor the stack.
	err = tooLong(opts, len(stack), shortened)
	if err != nil {
		_ = os.RemoveAll(base)

		return nil, fmt.Errorf("materialise %d layers at %s: %w", len(stack), merged, err)
	}

	err = unix.Mount("overlay", merged, "overlay", 0, opts)
	if err != nil {
		// Diagnose before cleaning up: the hint inspects the filesystem under
		// base, and RemoveAll takes that evidence away. Getting this backwards
		// produced a hint that was computed, empty, and silent.
		//
		// Two hints, for the kernel's two unhelpful answers: EINVAL, which is
		// overwhelmingly overlay-on-overlay, and ENOENT, which names neither
		// the path it wanted nor the fact that it may never have seen the whole
		// list. Both are appended - each is empty when it has nothing to say.
		hint := mountHint(err, base) + lowerHint(opts, lower, dirExists)

		// A machine that cannot mount at all is a different answer from a
		// build that cannot be mounted, and callers act on the difference:
		// I11 degrades or refuses on cause, and a test skips rather than
		// reporting a defect it did not find.
		if unavailable(err, base) {
			err = fmt.Errorf("%w: %w", ErrUnavailable, err)
		}

		_ = os.RemoveAll(base)

		return nil, fmt.Errorf("mount overlay (%d layers) at %s: %w%s",
			len(stack), merged, err, hint)
	}

	return &handle{root: merged, upper: upper, base: base, mounted: true}, nil
}

type handle struct {
	root    string
	upper   string
	base    string
	mounted bool

	mu       sync.Mutex
	released bool
}

func (h *handle) Root() string { return h.root }

// Delta is the overlay upper directory: exactly what the step wrote.
func (h *handle) Delta() string { return h.upper }

func (h *handle) Observations() core.Observation {
	// Empty, never nil. Populated at S5, when the fault path is instrumented.
	return core.Observation{
		Reads:    map[string]ir.NodeID{},
		Listings: map[string]ir.NodeID{},
	}
}

// Release unmounts and removes the handle's directories.
//
// Idempotent, because cleanup paths run more than once and a second release
// must not report a failure that would mask the first error.
func (h *handle) Release() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.released {
		return nil
	}

	h.released = true

	if h.mounted {
		err := unix.Unmount(h.root, 0)
		if err != nil {
			return fmt.Errorf("unmount %s: %w", h.root, err)
		}
	}

	err := os.RemoveAll(h.base)
	if err != nil {
		return fmt.Errorf("remove mount dirs: %w", err)
	}

	return nil
}

// translator prepares deletions for overlayfs, once per materialiser.
//
// Under scratch rather than under the layer store: a translated layer is this
// VM's business and must not be written back into a directory the host shares
// and other builds read.
func (m *Materialiser) translator() *translator {
	m.trMu.Lock()
	defer m.trMu.Unlock()

	if m.tr == nil {
		m.tr = newTranslator(filepath.Join(m.scratch, "translated"))
	}

	return m.tr
}
