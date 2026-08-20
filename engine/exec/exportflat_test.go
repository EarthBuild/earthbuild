package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// stackOf makes n distinct layer identities.
func stackOf(n int) []ir.NodeID {
	out := make([]ir.NodeID, 0, n)

	for i := range n {
		out = append(out, (&ir.Node{
			Op: ir.Op{Kind: ir.OpExec, Args: []string{"step", string(rune('a' + i%26)), string(rune(i))}},
		}).ID())
	}

	return out
}

// An artifact is read from a stack the mount can take.
//
// The scheduler flattens what a *step runs on* - `Flatten(base, MaxStack, …)`
// in `schedule.go` - and the exporter mounted whatever it was handed. A step's
// own stack is its base plus its own layer, so a build flattened to exactly the
// limit exports one layer deeper than the limit:
//
//	materialise the filesystem holding /out.txt: a stack of 65 layers needs
//	4137 bytes of mount options and the kernel reads 4095
//
// Seventy-five steps all ran. The build was complete and correct, and failed
// while copying a file out of it - which reads as a defect in the export and is
// a defect in the arithmetic two files away.
//
// It went unseen because 65 layers *fit* under macOS's shorter store paths.
// The limit is a byte budget being approximated by a layer count, so whether
// the off-by-one is fatal depends on where the store happens to live: the
// first Linux run found it immediately.
//
// **The failure class, fourth instance: a rule applied at one call site and not
// at its sibling.** E106 a shared definition with one consumer, E107 a fix
// applied to one of two implementations, E108 a conditional creation with an
// unconditional follow-up, and now a policy the scheduler applies and the
// exporter does not.
func TestAnExportedStackFitsTheMount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		n    int
	}{
		{"a shallow stack is left alone", 4},
		{"exactly the limit is left alone", MountableStackDepth},
		{"one deeper is flattened", MountableStackDepth + 1},
		{"a step's own layer on a flattened base", MountableStackDepth + 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := stackOf(tc.n)

			out, rng := flattenForMount(in)

			if len(out) > MountableStackDepth {
				t.Errorf("a stack of %d layers was handed to the mount as %d,"+
					" and the mount takes %d", tc.n, len(out), MountableStackDepth)
			}

			if tc.n <= MountableStackDepth {
				if rng != nil {
					t.Errorf("a stack that already fits was squashed anyway")
				}

				return
			}

			// The oldest are the ones collapsed: edits land near the top of a
			// stack, so squashing the newest would destroy the cache hits that
			// matter (green paper 4.8).
			if len(rng) == 0 {
				t.Fatal("the stack was shortened without naming what to squash")
			}

			if rng[0] != in[0] {
				t.Error("the squashed range does not start at the oldest layer")
			}

			// Nothing may be lost: the last layer of the input is still the
			// last layer of the output, or the artifact is read from the wrong
			// filesystem.
			if out[len(out)-1] != in[len(in)-1] {
				t.Error("the newest layer did not survive flattening")
			}
		})
	}
}
