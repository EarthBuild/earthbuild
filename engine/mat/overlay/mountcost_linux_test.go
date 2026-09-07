//go:build linux

package overlay_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
)

// What a step pays to have its filesystem mounted.
//
// E4 measured the *capture* side and found the argument for overlayfs: the upper
// directory is the diff, so writing a layer is a read of what changed rather
// than a walk of everything - 1.5 s against 22 s on a 100k-file tree. Nothing
// measured the other side, and "we use overlayfs" has been an unpriced answer
// ever since.
//
// This prices it. The stack depth is the variable that matters: overlayfs takes
// every lower directory in one mount option, and a real build reaches double
// figures - a `FROM` plus a dozen `RUN`s is a dozen lowers by the last step.
//
// The layers are written outside the timed loop, because writing a layer is not
// what a step pays per mount and including it would price the wrong thing.
func BenchmarkMountCostByStackDepth(b *testing.B) {
	for _, depth := range []int{1, 4, 16, 64} {
		b.Run(fmt.Sprintf("%d-layers", depth), func(b *testing.B) {
			m, err := overlay.New(b.TempDir())
			if err != nil {
				b.Skipf("this machine cannot mount overlayfs: %v", err)
			}

			stack := make([]ir.NodeID, depth)

			for i := range stack {
				stack[i] = ir.NodeID{byte(i + 1)}

				writeErr := m.WriteLayer(stack[i], map[string]string{
					fmt.Sprintf("file-%d", i): "contents",
				})
				if writeErr != nil {
					b.Fatal(writeErr)
				}
			}

			ctx := context.Background()

			// One mount before the timer, to find out whether this machine can
			// mount at all: a benchmark that reports the cost of failing is
			// worse than one that skips.
			h, err := m.Materialise(ctx, stack)
			if err != nil {
				b.Skipf("this machine cannot mount overlayfs: %v", err)
			}

			err = h.Release()
			if err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()

			for b.Loop() {
				h, err := m.Materialise(ctx, stack)
				if err != nil {
					b.Fatal(err)
				}

				err = h.Release()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Which half of that cost is the mount, and which the unmount.
//
// The pair measures what a step pays; a build that could overlap or defer one of
// them needs to know which one to attack, and "9 ms per step" is not an
// actionable number until it is split.
//
// Sixteen layers, because that is the shape a real build reaches by its last
// step and the depth sweep showed the constant dominates anyway.
func BenchmarkMountAndUnmountSeparately(b *testing.B) {
	m, err := overlay.New(b.TempDir())
	if err != nil {
		b.Skipf("this machine cannot mount overlayfs: %v", err)
	}

	const depth = 16

	stack := make([]ir.NodeID, depth)

	for i := range stack {
		stack[i] = ir.NodeID{byte(i + 1)}

		writeErr := m.WriteLayer(stack[i], map[string]string{
			fmt.Sprintf("file-%d", i): "contents",
		})
		if writeErr != nil {
			b.Fatal(writeErr)
		}
	}

	ctx := context.Background()

	h, err := m.Materialise(ctx, stack)
	if err != nil {
		b.Skipf("this machine cannot mount overlayfs: %v", err)
	}

	err = h.Release()
	if err != nil {
		b.Fatal(err)
	}

	b.Run("materialise", func(b *testing.B) {
		// The handles are released after the timer, so this measures mounting
		// and nothing else - at the price of holding b.N mounts at once, which
		// is why the bench is run with a bounded -benchtime.
		held := make([]interface{ Release() error }, 0, b.N)

		b.ResetTimer()

		for b.Loop() {
			h, err := m.Materialise(ctx, stack)
			if err != nil {
				b.Fatal(err)
			}

			held = append(held, h)
		}

		b.StopTimer()

		for _, h := range held {
			_ = h.Release()
		}
	})

	b.Run("release", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()

			h, err := m.Materialise(ctx, stack)
			if err != nil {
				b.Fatal(err)
			}

			b.StartTimer()

			err = h.Release()
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
