package earthfile2llb

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

const inlineCacheTestTag = "registry.example.com/myimg:latest"

// exportRecordingGwClient is a gateway client whose Solve succeeds at once with
// an empty result, and whose Export records, per image entry, whether it is
// loaded into the local container engine and whether it is pushed. Every other
// method panics via the nil embedded interface.
type exportRecordingGwClient struct {
	gwclient.Client

	loads  int
	pushes int
	mu     sync.Mutex
}

func (*exportRecordingGwClient) BuildOpts() gwclient.BuildOpts {
	return gwclient.BuildOpts{SessionID: "test-session"}
}

func (*exportRecordingGwClient) Solve(context.Context, gwclient.SolveRequest) (*gwclient.Result, error) {
	return &gwclient.Result{}, nil
}

func (f *exportRecordingGwClient) Export(_ context.Context, req gwclient.ExportRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for key := range req.Metadata {
		refPrefix, ok := strings.CutSuffix(key, "/image.name")
		if !ok {
			continue
		}

		if string(req.Metadata[refPrefix+"/export-image-push"]) == "true" {
			f.pushes++
		}

		_, toLocalRegistry := req.Metadata[refPrefix+"/export-image-local-registry"]
		if string(req.Metadata[refPrefix+"/export-image"]) == "true" || toLocalRegistry {
			f.loads++
		}
	}

	return nil
}

func (f *exportRecordingGwClient) counts() (loads, pushes int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.loads, f.pushes
}

// exportTestBuild is the build as the CLI starts it: the part of
// builder.BuildOpt and builder.Opt that decides who exports an image.
type exportTestBuild struct {
	export        Export
	push          bool
	inlineCache   bool
	globalWaitEnd bool
	// imageMode is --image (OnlyFinalTargetImages).
	imageMode bool
	// artifactMode is --artifact (builder.BuildOpt.OnlyArtifact != nil).
	artifactMode bool
}

// exportTestHarness converts a small build by driving Converters directly, the
// way the interpreter would, and then tallies what the wait blocks export
// against what builder.go would export afterwards.
type exportTestHarness struct {
	t        *testing.T
	gw       *exportRecordingGwClient
	ec       *gatewaycrafter.ExportCoordinator
	bus      *logbus.Bus
	eg       *serrgroup.Group
	visited  states.VisitedCollection
	topLevel *waitBlock
	root     *Converter
	all      []*Converter
	build    exportTestBuild
}

func newExportTestHarness(t *testing.T, build exportTestBuild) *exportTestHarness {
	t.Helper()

	return &exportTestHarness{
		t:        t,
		build:    build,
		gw:       &exportRecordingGwClient{},
		ec:       gatewaycrafter.NewExportCoordinator(),
		bus:      logbus.New(),
		eg:       &serrgroup.Group{},
		visited:  states.NewVisitedUpfrontHashCollection(),
		topLevel: newWaitBlock(),
	}
}

func localTestTarget(name string) domain.Target {
	return domain.Target{LocalPath: ".", Target: name}
}

func remoteTestTarget(name string) domain.Target {
	return domain.Target{GitURL: "github.com/example/repo", Target: name}
}

var (
	amd64 = specs.Platform{OS: "linux", Architecture: "amd64"}
	arm64 = specs.Platform{OS: "linux", Architecture: "arm64"}
)

// rootConverter starts the target the build was invoked on, in the top-level
// wait block, as Earthfile2LLB does for the initial call.
func (h *exportTestHarness) rootConverter(target domain.Target) *Converter {
	h.t.Helper()

	opt := ConvertOpt{
		Export:                h.build.export,
		SaveReferenced:        true,
		DoPushes:              h.build.push,
		UseInlineCache:        h.build.inlineCache,
		GlobalWaitBlockFtr:    h.build.globalWaitEnd,
		OnlyFinalTargetImages: h.build.imageMode,
	}

	h.root = h.newConverter(target, platutil.NewResolver(amd64), opt, h.topLevel)

	return h.root
}

// buildChild starts a target reached through BUILD from parent, as
// prepBuildTarget does: it shares the parent's current wait block.
func (h *exportTestHarness) buildChild(parent *Converter, target domain.Target, platform *specs.Platform) *Converter {
	h.t.Helper()

	opt := parent.opt
	opt.parentTargetID = parent.mts.Final.ID
	opt.SaveReferenced = parent.childSaveReferenced(buildCmd, target.IsRemote())
	opt.DoPushes = parent.opt.DoPushes
	opt.ForceSaveImage = false

	platr := parent.platr
	if platform != nil {
		platr = platr.SubResolver(platutil.FromLLBPlatform(*platform))
	}

	return h.newConverter(target, platr, opt, parent.waitBlock())
}

func (h *exportTestHarness) newConverter(
	target domain.Target, platr *platutil.Resolver, opt ConvertOpt, wb *waitBlock,
) *Converter {
	h.t.Helper()

	sts, _, err := h.visited.Add(h.t.Context(), target, platr, false, variables.NewScope(), nil)
	require.NoError(h.t, err)

	logbusTarget, err := h.bus.Run().NewTarget(sts.ID, target, nil, "", "")
	require.NoError(h.t, err)

	ftrs := &features.Features{ReferencedSaveOnly: true, WaitBlock: true}

	opt.GwClient = h.gw
	opt.Parallelism = semutil.NewWeighted(4)
	opt.ErrorGroup = h.eg
	opt.CacheImports = states.NewCacheImports(nil)
	opt.Logbus = h.bus
	opt.ExportCoordinator = h.ec
	opt.Features = ftrs

	c := &Converter{
		target: target,
		platr:  platr,
		opt:    opt,
		mts:    &states.MultiTarget{Final: sts, Visited: h.visited},
		varCollection: variables.NewCollection(variables.NewCollectionOpt{
			Target:           target,
			PlatformResolver: platr,
			Features:         ftrs,
		}),
		ftrs:           ftrs,
		waitBlockStack: []*waitBlock{wb},
		logbusTarget:   logbusTarget,
	}
	c.mts.Final.RanFromLike = true
	c.mts.Final.MainState = pllb.Image("alpine:3.20")

	h.all = append(h.all, c)

	return c
}

// saveImage runs SAVE IMAGE [--push] <inlineCacheTestTag> on c.
func (h *exportTestHarness) saveImage(c *Converter, push bool) {
	h.t.Helper()

	require.NoError(h.t, c.SaveImage(h.t.Context(), []string{inlineCacheTestTag}, push, false, false, nil, false))
}

// finalize ends c's conversion, as Earthfile2LLB does once its interpreter has
// run, and drains the async force-execution it starts.
func (h *exportTestHarness) finalize(c *Converter) {
	h.t.Helper()

	_, err := c.FinalizeStates(h.t.Context())
	require.NoError(h.t, err)
	require.NoError(h.t, h.eg.Wait())
}

// endConversion waits on the top-level wait block, as Earthfile2LLB does at the
// very end of the initial call, right before builder.go runs.
func (h *exportTestHarness) endConversion() {
	h.t.Helper()

	require.NoError(h.t, h.topLevel.Wait(h.t.Context(), h.root.opt.DoPushes, h.root.opt.doSaves()))
}

// exportTally counts what happens to the images of a build: loads into the
// local container engine, pushes, and the lines of the end-of-build summary.
type exportTally struct {
	loads          int
	pushes         int
	localSummaries int
	pushSummaries  int
}

func (e exportTally) String() string {
	return fmt.Sprintf("loads=%d pushes=%d local-summary-lines=%d push-summary-lines=%d",
		e.loads, e.pushes, e.localSummaries, e.pushSummaries)
}

func (e exportTally) add(o exportTally) exportTally {
	return exportTally{
		loads:          e.loads + o.loads,
		pushes:         e.pushes + o.pushes,
		localSummaries: e.localSummaries + o.localSummaries,
		pushSummaries:  e.pushSummaries + o.pushSummaries,
	}
}

// waitBlocks is what the wait blocks have exported, and summarized, so far.
func (h *exportTestHarness) waitBlocks() exportTally {
	loads, pushes := h.gw.counts()

	return exportTally{
		loads:          loads,
		pushes:         pushes,
		localSummaries: len(h.ec.GetLocalOutputSummary()),
		pushSummaries:  len(h.ec.GetPushedImageSummary()),
	}
}

// builderGo is what builder.go would export, and summarize, after conversion.
//
// It mirrors builder.planImage, the SkipBuilder short-circuit in front of it,
// and the end-of-build summary in convertAndBuild. builder imports this package,
// so a test here cannot call them; keep this in step with builder.go.
func (h *exportTestHarness) builderGo() exportTally {
	var tally exportTally

	b := h.build
	if b.globalWaitEnd && !b.artifactMode && !b.imageMode {
		// builder.go returns before its image exports.
		return tally
	}

	for _, c := range h.all {
		sts := c.mts.Final
		isFinal := c == h.root

		for _, si := range sts.SaveImages {
			tagged := si.DockerTag != ""
			export := tagged &&
				(sts.GetDoSaves() || si.ForceSave) &&
				b.export.Images() &&
				!b.artifactMode &&
				(!b.imageMode || isFinal)
			push := tagged && b.push && si.Push && !sts.Target.IsRemote() && sts.GetDoPushes()

			if si.SkipBuilder || !export && !push {
				continue
			}

			if export {
				tally.loads++
			}

			if push {
				tally.pushes++
			}

			// The --artifact summary reports the artifact only, and the --image
			// summary only the final target's images.
			if b.artifactMode || b.imageMode && !isFinal {
				continue
			}

			if export {
				tally.localSummaries++
			}

			if push || si.Push && !b.push && !sts.Target.IsRemote() {
				tally.pushSummaries++
			}
		}
	}

	return tally
}

// Under --use-inline-cache, an image saved in the top-level implicit wait block
// keeps SkipBuilder == false, so that builder.go exports it with inline cache
// (the #2178 workaround). The top-level block must then leave it to builder.go:
// exporting it at the end of conversion as well loads or pushes the same tag
// twice, and prints it twice in the summary.
func TestTopLevelInlineCacheImageIsExportedOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// run converts the build; it must leave h.root set.
		run   func(h *exportTestHarness)
		build exportTestBuild
		want  exportTally
	}{
		{
			// Control: without inline cache the wait block alone exports it.
			name:  "SAVE IMAGE --push with --push, no inline cache",
			build: exportTestBuild{export: ExportAll, push: true},
			run: func(h *exportTestHarness) {
				root := h.rootConverter(localTestTarget("img"))
				h.saveImage(root, true)
				h.finalize(root)
			},
			want: exportTally{loads: 1, pushes: 1, localSummaries: 1, pushSummaries: 1},
		},
		{
			name:  "SAVE IMAGE",
			build: exportTestBuild{export: ExportAll, inlineCache: true},
			run: func(h *exportTestHarness) {
				root := h.rootConverter(localTestTarget("img"))
				h.saveImage(root, false)
				h.finalize(root)
			},
			want: exportTally{loads: 1, localSummaries: 1},
		},
		{
			name:  "SAVE IMAGE --push without --push",
			build: exportTestBuild{export: ExportAll, inlineCache: true},
			run: func(h *exportTestHarness) {
				root := h.rootConverter(localTestTarget("img"))
				h.saveImage(root, true)
				h.finalize(root)
			},
			want: exportTally{loads: 1, localSummaries: 1, pushSummaries: 1},
		},
		{
			name:  "SAVE IMAGE --push with --push",
			build: exportTestBuild{export: ExportAll, push: true, inlineCache: true},
			run: func(h *exportTestHarness) {
				root := h.rootConverter(localTestTarget("img"))
				h.saveImage(root, true)
				h.finalize(root)
			},
			want: exportTally{loads: 1, pushes: 1, localSummaries: 1, pushSummaries: 1},
		},
		{
			name:  "SAVE IMAGE --push with --push --no-output",
			build: exportTestBuild{export: ExportNone, push: true, inlineCache: true},
			run: func(h *exportTestHarness) {
				root := h.rootConverter(localTestTarget("img"))
				h.saveImage(root, true)
				h.finalize(root)
			},
			want: exportTally{pushes: 1, pushSummaries: 1},
		},
		{
			// BUILD --platform=linux/amd64 --platform=linux/arm64 +img
			name:  "multi-platform SAVE IMAGE --push with --push",
			build: exportTestBuild{export: ExportAll, push: true, inlineCache: true},
			run: func(h *exportTestHarness) {
				root := h.rootConverter(localTestTarget("multi"))
				for _, p := range []specs.Platform{amd64, arm64} {
					child := h.buildChild(root, localTestTarget("img"), &p)
					h.saveImage(child, true)
					h.finalize(child)
				}

				h.finalize(root)
			},
			want: exportTally{loads: 2, pushes: 2, localSummaries: 2, pushSummaries: 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newExportTestHarness(t, tt.build)
			tt.run(h)
			h.endConversion()

			byWaitBlocks, byBuilder := h.waitBlocks(), h.builderGo()
			require.Equal(t, tt.want, byWaitBlocks.add(byBuilder),
				"exported more or less than once:\n  wait blocks: %s\n  builder.go:  %s", byWaitBlocks, byBuilder)
		})
	}
}
