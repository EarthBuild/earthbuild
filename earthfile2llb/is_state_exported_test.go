package earthfile2llb

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/stretchr/testify/require"
)

// exportScenario is one SAVE IMAGE, as the user wrote it and as the build was
// invoked.
type exportScenario struct {
	export      Export
	remote      bool // the target is remote (github.com/...+img)
	topLevel    bool // converted in the build's top-level implicit wait block
	detached    bool // reached by FROM / COPY, so its block is never waited on
	inlineCache bool // --use-inline-cache
	push        bool // SAVE IMAGE --push
	doPushes    bool // --push
	forceSave   bool // legacy ForceSaveImage
}

const scenarioTag = "registry.example.com/" + testDockerTag

// runExportScenario converts one target with a single SAVE IMAGE and finalizes
// it. It returns the converter and the wait block the target was converted in,
// which the caller may then wait on.
func runExportScenario(t *testing.T, gw *exportRecordingGwClient, sc exportScenario) (*Converter, *waitBlock) {
	t.Helper()

	wb := newWaitBlock()
	wb.topLevel = sc.topLevel
	wb.detached = sc.detached

	c, eg := newFinalizeTestConverter(t, finalizeTestOpt{
		gw:          gw,
		export:      sc.export,
		doPushes:    sc.doPushes,
		waitBlock:   wb,
		waitBlockOn: true,
	})
	c.opt.UseInlineCache = sc.inlineCache
	c.opt.ForceSaveImage = sc.forceSave
	c.opt.rootTarget = sc.topLevel
	c.opt.ImagePlan = ImagePlanOpt{Export: sc.export, Push: sc.doPushes}

	if sc.remote {
		c.target.GitURL = "github.com/example/repo"
		c.mts.Final.Target.GitURL = "github.com/example/repo"
	}

	c.mts.Final.RanFromLike = true
	c.mts.Final.MainState = pllb.Image("alpine:3.20")

	err := c.SaveImage(t.Context(), []string{scenarioTag}, sc.push, false, false, nil, false)
	require.NoError(t, err)

	_, err = c.FinalizeStates(t.Context())
	require.NoError(t, err)
	require.NoError(t, eg.Wait())

	return c, wb
}

// solvedByBuilder reports whether builder.go solves one of c's images, by asking
// PlanImage exactly as builder.go does.
func solvedByBuilder(c *Converter) bool {
	return slices.ContainsFunc(c.mts.Final.SaveImages, func(si states.SaveImage) bool {
		return PlanImage(c.opt.ImagePlan, c.mts.Final, c.opt.rootTarget, si).SolvedByBuilder(si, false)
	})
}

// Converter.isStateExported gates FinalizeStates' force execution and
// waitStates' force execution: when it reports true, nothing else solves the
// target's main state. So it must report true exactly when one of the exporters
// really solves the image: the wait block (it ends up in an Export request) or
// builder.go (PlanImage says so). Otherwise the main state is either solved
// twice or not at all.
func TestConverterIsStateExportedAgreesWithExporters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sc   exportScenario
		want bool
	}{
		{
			name: "local SAVE IMAGE --push under --push",
			sc:   exportScenario{export: ExportNone, push: true, doPushes: true},
			want: true,
		},
		{
			name: "SAVE IMAGE under the default output",
			sc:   exportScenario{export: ExportAll},
			want: true,
		},
		{
			// planImage.export requires opt.Export.Images(), and so does the wait
			// item's localExport. ForceSave cannot override --no-output.
			name: "ForceSave under --no-output",
			sc:   exportScenario{export: ExportNone, forceSave: true},
			want: false,
		},
		{
			name: "ForceSave under --no-image-output, delegated to builder.go",
			sc:   exportScenario{export: ExportArtifactsOnly, forceSave: true, topLevel: true, inlineCache: true},
			want: false,
		},
		{
			// builder.go never pushes a remote target, so the wait block keeps it.
			name: "remote target SAVE IMAGE --push under --push --use-inline-cache",
			sc: exportScenario{
				export: ExportNone, remote: true, push: true, doPushes: true, topLevel: true, inlineCache: true,
			},
			want: true,
		},
		{
			name: "local target SAVE IMAGE --push under --push --use-inline-cache, delegated to builder.go",
			sc: exportScenario{
				export: ExportNone, push: true, doPushes: true, topLevel: true, inlineCache: true,
			},
			want: true,
		},
		{
			// Nothing waits on the block of a target reached by FROM or COPY.
			name: "SAVE IMAGE in a detached wait block",
			sc:   exportScenario{export: ExportAll, detached: true},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gw := &exportRecordingGwClient{}
			c, wb := runExportScenario(t, gw, tt.sc)

			got := c.isStateExported(&c.mts.Final.MainState)

			if !tt.sc.detached {
				err := wb.Wait(t.Context(), tt.sc.doPushes, c.opt.doSaves())
				require.NoError(t, err)
			}

			exporterSolves := len(gw.exported) > 0 || solvedByBuilder(c)

			require.Equal(t, exporterSolves, got, "isStateExported must agree with what the exporters do")
			require.Equal(t, tt.want, got)
		})
	}
}
