package earthfile2llb

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/logbus"
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
func TestFinalizeStatesCallsOnExecutionSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup  func(c *Converter)
		name   string
		export Export
		push   bool
	}{
		{
			// Control: neither predicate fires, so the force-execution path runs.
			name:   "main state is not saved as an image",
			export: ExportAll,
			setup: func(c *Converter) {
				c.mts.Final.MainState = pllb.Image("alpine:3.20")
			},
		},
		{
			// A target whose body is only ARG / BUILD --auto-skip children that
			// were themselves skipped (or any FROM-less target under
			// --no-fake-dep) has a scratch main state. forceExecution already
			// treats scratch as a successful no-op; isStateExported instead
			// reports it as "exported" and the success callback is skipped.
			name:   "main state is scratch",
			export: ExportAll,
			setup:  func(*Converter) {},
		},
		{
			name:   "main state is saved as a locally exported image",
			export: ExportAll,
			setup: func(c *Converter) {
				c.mts.Final.MainState = pllb.Image("alpine:3.20")
				c.mts.Final.SaveImages = append(c.mts.Final.SaveImages, states.SaveImage{
					DockerTag: testDockerTag,
					State:     c.mts.Final.MainState,
				})
			},
		},
		{
			name:   "main state is saved as a pushed image",
			export: ExportNone,
			push:   true,
			setup: func(c *Converter) {
				c.mts.Final.MainState = pllb.Image("alpine:3.20")
				c.mts.Final.SaveImages = append(c.mts.Final.SaveImages, states.SaveImage{
					DockerTag: "registry.example.com/" + testDockerTag,
					State:     c.mts.Final.MainState,
					Push:      true,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			c, eg := newFinalizeTestConverter(t, finalizeTestOpt{
				gw:       &fakeGwClient{},
				export:   tt.export,
				doPushes: tt.push,
				onSuccess: func(context.Context) {
					calls.Add(1)
				},
			})
			tt.setup(c)

			_, err := c.FinalizeStates(t.Context())
			require.NoError(t, err)
			require.NoError(t, eg.Wait())

			require.Equal(t, int32(1), calls.Load(),
				"OnExecutionSuccess must fire exactly once so BUILD --auto-skip can save the target's hash")
		})
	}
}
