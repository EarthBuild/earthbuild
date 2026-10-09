package earthfile2llb

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logstream"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/gatewaycrafter"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/util/syncutil/semutil"
	"github.com/EarthBuild/earthbuild/util/syncutil/serrgroup"
	"github.com/EarthBuild/earthbuild/variables"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// fakeGwClient is a gateway client whose Solve succeeds immediately with an
// empty result, which forceExecution treats as "executed". Every other method
// panics via the nil embedded interface, so a test fails loudly if FinalizeStates
// starts relying on more of the gateway.
type fakeGwClient struct {
	gwclient.Client

	solves atomic.Int32
}

func (f *fakeGwClient) Solve(context.Context, gwclient.SolveRequest) (*gwclient.Result, error) {
	f.solves.Add(1)

	return &gwclient.Result{}, nil
}

type finalizeTestOpt struct {
	gw          gwclient.Client
	parallelism semutil.Semaphore
	onSuccess   func(context.Context)
	waitBlock   *waitBlock
	export      Export
	doPushes    bool
	waitBlockOn bool
}

// newFinalizeTestConverter builds the smallest Converter that FinalizeStates can
// run against: a fresh target whose main state is scratch, with
// ExecAfterParallel enabled so that the async force-execution path is taken.
func newFinalizeTestConverter(t *testing.T, o finalizeTestOpt) (*Converter, *serrgroup.Group) {
	t.Helper()

	target := domain.Target{LocalPath: ".", Target: "child"}
	platr := platutil.NewResolver(specs.Platform{OS: "linux", Architecture: "amd64"})

	sts, _, err := states.NewVisitedUpfrontHashCollection().
		Add(t.Context(), target, platr, false, variables.NewScope(), nil)
	require.NoError(t, err)

	bus := logbus.New()

	logbusTarget, err := bus.Run().NewTarget(sts.ID, target, nil, "", "")
	require.NoError(t, err)

	ftrs := &features.Features{ExecAfterParallel: true, WaitBlock: o.waitBlockOn}

	eg := &serrgroup.Group{}

	parallelism := o.parallelism
	if parallelism == nil {
		parallelism = semutil.NewWeighted(1)
	}

	wb := o.waitBlock
	if wb == nil {
		wb = newWaitBlock()
	}

	c := &Converter{
		target: target,
		platr:  platr,
		opt: ConvertOpt{
			GwClient:           o.gw,
			Parallelism:        parallelism,
			ErrorGroup:         eg,
			CacheImports:       states.NewCacheImports(nil),
			OnExecutionSuccess: o.onSuccess,
			Export:             o.export,
			ImagePlan:          ImagePlanOpt{Export: o.export, Push: o.doPushes},
			SaveReferenced:     true,
			DoPushes:           o.doPushes,
			Logbus:             bus,
			ExportCoordinator:  gatewaycrafter.NewExportCoordinator(),
		},
		mts: &states.MultiTarget{Final: sts},
		varCollection: variables.NewCollection(variables.NewCollectionOpt{
			Target:           target,
			PlatformResolver: platr,
			Features:         ftrs,
		}),
		ftrs:           ftrs,
		waitBlockStack: []*waitBlock{wb},
		logbusTarget:   logbusTarget,
	}

	return c, eg
}

// OnExecutionSuccess is the only thing that saves the hash of a
// `BUILD --auto-skip` child (see newOnExecutionSuccess in interpreter.go). It must
// be called once the child target has executed successfully, regardless of
// whether its main state also happens to be exported as an image, and
// regardless of whether the main state is scratch. Otherwise auto-skip silently
// stops skipping: the hash is never written, and every build re-runs the
// target.
//
// When an image export is what solves the main state, the target has not
// executed until that export is done. So neither OnExecutionSuccess nor the
// target's SUCCESS status may come before it.
func TestFinalizeStatesCallsOnExecutionSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup       func(t *testing.T, c *Converter)
		name        string
		export      Export
		push        bool
		waitBlockOn bool
		// exportSolves is whether an image export, not force execution, solves
		// the main state.
		exportSolves bool
	}{
		{
			// Control: nothing exports the main state, so it is force executed.
			name:   "main state is not saved as an image",
			export: ExportAll,
			setup: func(_ *testing.T, c *Converter) {
				c.mts.Final.MainState = pllb.Image("alpine:3.20")
			},
		},
		{
			// A target whose body is only ARG / BUILD --auto-skip children that
			// were themselves skipped (or any FROM-less target under
			// --no-fake-dep) has a scratch main state. forceExecution treats
			// scratch as a successful no-op.
			name:   "main state is scratch",
			export: ExportAll,
			setup:  func(*testing.T, *Converter) {},
		},
		{
			name:         "main state is saved as an image builder.go exports",
			export:       ExportAll,
			exportSolves: true,
			setup:        saveTestImage(false),
		},
		{
			name:         "main state is saved as an image a wait block exports",
			export:       ExportAll,
			waitBlockOn:  true,
			exportSolves: true,
			setup:        saveTestImage(false),
		},
		{
			name:         "main state is saved as an image a wait block pushes",
			export:       ExportNone,
			push:         true,
			waitBlockOn:  true,
			exportSolves: true,
			setup:        saveTestImage(true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			c, eg := newFinalizeTestConverter(t, finalizeTestOpt{
				gw:          &exportRecordingGwClient{},
				export:      tt.export,
				doPushes:    tt.push,
				waitBlockOn: tt.waitBlockOn,
				onSuccess: func(context.Context) {
					calls.Add(1)
				},
			})
			tt.setup(t, c)

			_, err := c.FinalizeStates(t.Context())
			require.NoError(t, err)
			require.NoError(t, eg.Wait())

			if tt.exportSolves {
				require.Zero(t, calls.Load(), "OnExecutionSuccess must wait for the export that solves the main state")
				require.NotEqual(t, logstream.RunStatus_RUN_STATUS_SUCCESS, lastTargetStatus(c),
					"the target must not succeed before the export that solves its main state")
			}

			// The exporters run later: a wait block when it is waited on,
			// builder.go once the whole build has been converted.
			err = c.waitBlock().Wait(t.Context(), tt.push, c.opt.doSaves())
			require.NoError(t, err)
			settleBuilderExports(t.Context(), c, nil)

			require.Equal(t, int32(1), calls.Load(),
				"OnExecutionSuccess must fire exactly once so BUILD --auto-skip can save the target's hash")
			require.Equal(t, logstream.RunStatus_RUN_STATUS_SUCCESS, lastTargetStatus(c))
		})
	}
}

// A failed export that was going to solve the main state is a failed target:
// no OnExecutionSuccess (which would save a BUILD --auto-skip hash for a target
// that never ran), and a FAILURE status rather than SUCCESS.
func TestFinalizeStatesReportsFailedExport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		waitBlockOn bool
	}{
		{name: "exported by builder.go"},
		{name: "exported by a wait block", waitBlockOn: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			exportErr := errors.New("registry unavailable")

			c, eg := newFinalizeTestConverter(t, finalizeTestOpt{
				gw:          &exportRecordingGwClient{exportErr: exportErr},
				export:      ExportAll,
				waitBlockOn: tt.waitBlockOn,
				onSuccess: func(context.Context) {
					calls.Add(1)
				},
			})
			saveTestImage(false)(t, c)

			_, err := c.FinalizeStates(t.Context())
			require.NoError(t, err)
			require.NoError(t, eg.Wait())

			err = c.waitBlock().Wait(t.Context(), false, c.opt.doSaves())
			if tt.waitBlockOn {
				require.ErrorIs(t, err, exportErr)
			} else {
				require.NoError(t, err)
				settleBuilderExports(t.Context(), c, exportErr)
			}

			require.Zero(t, calls.Load())
			require.Equal(t, logstream.RunStatus_RUN_STATUS_FAILURE, lastTargetStatus(c))
		})
	}
}

// settleBuilderExports does what builder.go does once it has exported c's
// images, with err as the outcome: it settles every export it took on.
func settleBuilderExports(ctx context.Context, c *Converter, err error) {
	for _, si := range c.mts.Final.SaveImages {
		plan := PlanImage(c.opt.ImagePlan, c.mts.Final, c.opt.rootTarget, si)
		if plan.SolvedByBuilder(si, false) && si.Export.TakeForBuilder() && si.Export != nil {
			si.Export.Outcome.Settle(ctx, err)
		}
	}
}

// saveTestImage returns a setup that runs SAVE IMAGE (--push, if push) on an
// alpine main state.
func saveTestImage(push bool) func(t *testing.T, c *Converter) {
	return func(t *testing.T, c *Converter) {
		t.Helper()

		c.mts.Final.RanFromLike = true
		c.mts.Final.MainState = pllb.Image("alpine:3.20")

		err := c.SaveImage(t.Context(), []string{"registry.example.com/" + testDockerTag}, push, false, false, nil, false)
		require.NoError(t, err)
	}
}

// lastTargetStatus returns the last status c's target reported on the log bus.
func lastTargetStatus(c *Converter) logstream.RunStatus {
	rec := &targetStatusRecorder{targetID: c.mts.Final.ID}

	c.opt.Logbus.AddRawSubscriber(rec)
	defer c.opt.Logbus.RemoveRawSubscriber(rec)

	rec.mu.Lock()
	defer rec.mu.Unlock()

	return rec.last
}

// targetStatusRecorder is a log bus subscriber that records the last status
// reported for one target.
type targetStatusRecorder struct {
	targetID string
	last     logstream.RunStatus
	mu       sync.Mutex
}

func (r *targetStatusRecorder) Write(delta *logstream.Delta) {
	dtm, ok := delta.GetDeltaManifest().GetFields().GetTargets()[r.targetID]
	if !ok || dtm.GetStatus() == logstream.RunStatus_RUN_STATUS_UNKNOWN {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.last = dtm.GetStatus()
}
