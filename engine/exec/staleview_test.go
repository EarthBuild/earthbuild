//go:build darwin

package exec_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// TestASandboxWhoseStoreWasReplacedIsNotReused.
//
// **The worst outcome this engine can produce is a hang, and this produced
// one.** Delete the store directory while a sandbox has it bind-mounted and the
// virtiofs mount goes on pointing at the deleted inode. Recreate the path - which
// anything that opens the layer store does, including the engine itself - and
// the VM is still looking at the old one.
//
// The symptom is not reliably the `mkdir ... no such file or directory` it was
// first filed as. Measured while benchmarking: two builds in six went wrong, one
// producing nothing and one taking **477 seconds** for a single
// `RUN echo N > /fN`, and an earlier run reached step 34 of 45 in ten minutes.
//
// Existence is not the test - the path is back, which is why `reapStranded`'s
// rule does not fire. Identity is: the directory a VM was started against has an
// inode, and a directory of the same name with a different one is a different
// directory.
func TestASandboxWhoseStoreWasReplacedIsNotReused(t *testing.T) {
	t.Parallel()

	const at = "/store"

	for _, c := range []struct {
		name   string
		labels map[string]string
		now    uint64
		reuse  bool
	}{
		{
			name:   "the same directory it started against",
			labels: map[string]string{exec.LabelStoreInode: "42"},
			now:    42,
			reuse:  true,
		},
		{
			name:   "a directory of the same name, replaced underneath it",
			labels: map[string]string{exec.LabelStoreInode: "42"},
			now:    99,
			reuse:  false,
		},
		{
			// A VM started before this engine labelled anything. Reused, because
			// refusing every unlabelled sandbox would throw away every machine
			// running at the moment of an upgrade - and the old failure is rare
			// where this one would be certain.
			name:   "a sandbox from before the label existed",
			labels: map[string]string{},
			now:    42,
			reuse:  true,
		},
		{
			name:   "a label this engine cannot read",
			labels: map[string]string{exec.LabelStoreInode: "not a number"},
			now:    42,
			reuse:  true,
		},
	} {
		if got := exec.SandboxSeesStore(c.labels, c.now); got != c.reuse {
			t.Errorf("%s at %s: reuse=%v, want %v", c.name, at, got, c.reuse)
		}
	}
}
