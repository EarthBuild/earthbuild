package earthfile2llb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// stillWaitingFor is how long a Wait is given to show it is still waiting
	// for an export that is held. A Wait that does not wait returns at once, so
	// this only bounds how long a passing test takes.
	stillWaitingFor = 200 * time.Millisecond
	// returnsWithin bounds how long a Wait may take once nothing holds it.
	returnsWithin = 10 * time.Second
)

// pushes reports whether req pushes anything.
func (req exportRequest) pushes() bool {
	for _, img := range req.images {
		if img.push {
			return true
		}
	}

	return false
}

// loads reports whether req loads anything into the local container engine.
func (req exportRequest) loads() bool {
	return len(req.localNames()) != 0
}

// waitAsync runs wb.Wait in the background, and returns where its error is
// sent.
func waitAsync(ctx context.Context, wb *waitBlock, push, localExport bool) <-chan error {
	done := make(chan error, 1)

	go func() {
		done <- wb.Wait(ctx, push, localExport)
	}()

	return done
}

// requireReturns requires done to receive a nil error within returnsWithin.
func requireReturns(t *testing.T, done <-chan error, msg string) {
	t.Helper()

	select {
	case err := <-done:
		require.NoError(t, err, msg)
	case <-time.After(returnsWithin):
		require.FailNow(t, "Wait did not return", msg)
	}
}

// Two parent targets, converted in parallel, each BUILD the same image target
// inside their own WAIT ... END. The target is converted once, in the first
// parent's block; the second BUILD only re-attaches its wait items to the second
// block (AttachTopLevelWaitItems), which gets the SAVE IMAGE but not the
// target's main state. Each END promises that the image is pushed (or loaded)
// by the time it returns: a RUN --push after it may rely on that. So the END
// that does not export the image itself must wait until the export another
// block took on is done.
//
// It must wait for the export it needs: a block that needs the image pushed
// must wait for the push, even when an earlier WAIT already loaded the image
// locally (so the image's first export is long done).
func TestWaitReturnsOnlyOnceSharedImageIsExported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// hold picks the export requests the gateway holds.
		hold   func(exportRequest) bool
		name   string
		export Export
		push   bool
		// loadedFirst is whether an earlier WAIT loaded the image, before the
		// push was set; the push is then the only export left.
		loadedFirst bool
	}{
		{
			name:   "push taken by the other block",
			hold:   exportRequest.pushes,
			export: ExportNone,
			push:   true,
		},
		{
			name:   "local export taken by the other block",
			hold:   exportRequest.loads,
			export: ExportAll,
		},
		{
			name:        "push taken by the other block after an earlier WAIT loaded the image",
			hold:        exportRequest.pushes,
			export:      ExportAll,
			push:        true,
			loadedFirst: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			held := make(chan struct{}, 1)
			release := make(chan struct{})

			gw := &requestRecordingGwClient{hold: tt.hold, held: held, release: release}

			// The first parent's WAIT block, in which the image target is
			// converted.
			first := newWaitBlock()

			c, eg := newFinalizeTestConverter(t, finalizeTestOpt{
				gw:          gw,
				export:      tt.export,
				doPushes:    tt.push && !tt.loadedFirst,
				waitBlock:   first,
				waitBlockOn: true,
			})
			saveTestImage(true)(t, c)

			_, err := c.FinalizeStates(t.Context())
			require.NoError(t, err)
			require.NoError(t, eg.Wait())

			if tt.loadedFirst {
				// An earlier WAIT loads it, and is done.
				require.NoError(t, first.Wait(t.Context(), false, true))

				// Then the image is BUILT under --push.
				c.opt.DoPushes = true
				c.mts.Final.SetDoPushes()

				first = newWaitBlock()
				c.mts.Final.AttachTopLevelWaitItems(t.Context(), first)
			}

			// The second parent's WAIT block BUILDs the converted target.
			second := newWaitBlock()
			c.mts.Final.AttachTopLevelWaitItems(t.Context(), second)

			// The first END takes the export on, and the gateway holds it.
			firstDone := waitAsync(t.Context(), first, tt.push, c.opt.doSaves())

			select {
			case <-held:
			case <-time.After(returnsWithin):
				require.FailNow(t, "the first END never exported the image")
			}

			// The second END must not return while that export is held.
			ctx, cancel := context.WithTimeout(t.Context(), stillWaitingFor)
			defer cancel()

			err = second.Wait(ctx, tt.push, c.opt.doSaves())
			require.ErrorIs(t, err, context.DeadlineExceeded,
				"the second END returned before the export it relies on was done")

			// Once the export is done, a block waiting for it returns.
			third := newWaitBlock()
			c.mts.Final.AttachTopLevelWaitItems(t.Context(), third)

			thirdDone := waitAsync(t.Context(), third, tt.push, c.opt.doSaves())

			close(release)
			requireReturns(t, firstDone, "the first END")
			requireReturns(t, thirdDone, "an END waiting for the first END's export")
		})
	}
}
