package builder

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/platutil"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPlatformSts builds a target converted for one platform (BUILD
// --platform), whose SAVE IMAGE --push of testDockerTag was created in the
// top-level wait block under --use-inline-cache, so left to builder.go
// (SkipBuilder == false).
func newPlatformSts(t *testing.T, platform string, doSaves, doPushes bool) *states.SingleTarget {
	t.Helper()

	platr := platutil.NewResolver(specs.Platform{OS: "linux", Architecture: "amd64"})

	p, err := platr.Parse(platform)
	require.NoError(t, err)

	platr = platr.SubResolver(p)

	sts := newSts(t, localTarget(), doSaves, doPushes)
	sts.PlatformResolver = platr
	sts.SaveImages = []states.SaveImage{{
		State:       pllb.Image("alpine:3.20"),
		DockerTag:   testDockerTag,
		Push:        true,
		Platform:    platr.Materialize(platr.Current()),
		HasPlatform: true,
		Export:      &states.ImageExport{},
	}}

	return sts
}

// Under --push --use-inline-cache, `BUILD --platform=linux/amd64
// --platform=linux/arm64 +img` outside any WAIT leaves both platforms of the
// image to builder.go. If `WAIT BUILD --platform=linux/amd64 +img END` follows,
// END takes the amd64 export over and pushes it. builder.go then exports only
// arm64 itself, but it pushes the tag as one manifest list, which replaces what
// the registry had. So that push must still carry amd64. It must not solve or
// load amd64 again, but amd64 stays in the manifest list, and in the local
// multi-platform image too.
func TestBuilderKeepsPlatformsAWaitBlockExported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		export earthfile2llb.Export
		push   bool
	}{
		{name: "push", export: earthfile2llb.ExportNone, push: true},
		{name: "local export", export: earthfile2llb.ExportAll},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			amd64 := newPlatformSts(t, "linux/amd64", !tt.push, tt.push)
			arm64 := newPlatformSts(t, "linux/arm64", !tt.push, tt.push)

			// END took the amd64 export over and exported it.
			amd64Export := amd64.SaveImages[0].Export
			require.True(t, amd64Export.TakeForWaitBlock())
			amd64Export.Outcome.Settle(t.Context(), nil)

			if tt.push {
				amd64Export.Pushed.Settle(t.Context(), nil)
			} else {
				amd64Export.ExportedLocally.Settle(t.Context(), nil)
			}

			mts := &states.MultiTarget{
				Final:   amd64,
				Visited: &staticVisitedCollection{items: []*states.SingleTarget{amd64, arm64}},
			}
			opt := BuildOpt{Push: tt.push, Export: tt.export}

			images, err := takeBuilderImages(opt, mts, false, func(sts *states.SingleTarget) []states.SaveImage {
				return sts.SaveImages
			})
			require.NoError(t, err)

			var platforms []string

			for _, img := range images {
				if img.saveImage.DockerTag != testDockerTag || !img.multiPlatform {
					continue
				}

				if tt.push && img.plan.Push || !tt.push && img.plan.Export {
					platforms = append(platforms, img.saveImage.Platform.String())
				}
			}

			slices.Sort(platforms)

			assert.Equal(t, []string{"linux/amd64", "linux/arm64"}, platforms,
				"builder.go's manifest list for %s must keep the platform a wait block exported", testDockerTag)
		})
	}
}
