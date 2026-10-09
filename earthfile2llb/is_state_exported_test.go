package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/stretchr/testify/require"
)

// Converter.isStateExported gates FinalizeStates' force-execution, and with it
// the BUILD --auto-skip callback and the target's SUCCESS status. builder.planImage
// documents itself as the only place the export/push decision is made; this
// predicate re-derives that decision and drifts from it. Each row below is an
// input where isStateExported reports true, yet neither waitBlock.saveImages
// nor builder.planImage loads or pushes the image.
func TestConverterIsStateExportedAgreesWithExporters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		export      Export
		remote      bool
		forceSave   bool
		push        bool
		doPushes    bool
		skipBuilder bool
		want        bool
	}{
		{
			// Control: both exporters push this.
			name:        "local target SAVE IMAGE --push under --push",
			export:      ExportNone,
			push:        true,
			doPushes:    true,
			skipBuilder: true,
			want:        true,
		},
		{
			// planImage.export requires opt.Export.Images(), and so does the wait
			// item's localExport. ForceSave cannot override --no-output.
			name:        "ForceSave under --no-output",
			export:      ExportNone,
			forceSave:   true,
			skipBuilder: true,
			want:        false,
		},
		{
			name:        "ForceSave under --no-image-output",
			export:      ExportArtifactsOnly,
			forceSave:   true,
			skipBuilder: false,
			want:        false,
		},
		{
			// SkipBuilder == false (--use-inline-cache, implicit wait block): the
			// wait block delegates the push to builder.go, and planImage never
			// pushes a remote target.
			name:        "remote target SAVE IMAGE --push delegated to builder.go",
			export:      ExportNone,
			remote:      true,
			push:        true,
			doPushes:    true,
			skipBuilder: false,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := newFinalizeTestConverter(t, finalizeTestOpt{
				export:   tt.export,
				doPushes: tt.doPushes,
			})

			if tt.remote {
				c.target.GitURL = "github.com/example/repo"
				c.mts.Final.Target.GitURL = "github.com/example/repo"
			}

			state := pllb.Image("alpine:3.20")
			c.mts.Final.MainState = state
			c.mts.Final.SaveImages = append(c.mts.Final.SaveImages, states.SaveImage{
				DockerTag:   "registry.example.com/" + testDockerTag,
				State:       state,
				Push:        tt.push,
				ForceSave:   tt.forceSave,
				SkipBuilder: tt.skipBuilder,
			})

			require.Equal(t, tt.want, c.isStateExported(&state))
		})
	}
}
