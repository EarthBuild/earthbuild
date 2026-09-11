package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/states"
	"github.com/stretchr/testify/require"
)

// SetDoSave is what propagates local image export down BUILD edges. It must
// honour Export == ExportAll, otherwise a child target's image is exported even
// though conversion already declined to export it. See #855.
const testDockerTag = "myimg:latest"

func TestSaveImageWaitItemSetDoSave(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		dockerTag       string
		export          Export
		wantLocalExport bool
	}{
		{
			name:            "propagates local export by default (ExportAll)",
			dockerTag:       testDockerTag,
			export:          ExportAll,
			wantLocalExport: true,
		},
		{
			name:            "no-image-output suppresses propagated local export",
			export:          ExportArtifactsOnly,
			dockerTag:       testDockerTag,
			wantLocalExport: false,
		},
		{
			name:            "no-output suppresses propagated local export",
			export:          ExportNone,
			dockerTag:       testDockerTag,
			wantLocalExport: false,
		},
		{
			name:            "untagged image is never exported",
			dockerTag:       "",
			export:          ExportAll,
			wantLocalExport: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Converter{opt: ConvertOpt{Export: tt.export}}
			si := states.SaveImage{DockerTag: tt.dockerTag}

			// localExport=false mirrors conversion having declined the export;
			// SetDoSave is the path that can flip it back on.
			item := newSaveImage(si, c, true, false)
			item.SetDoSave()

			siwi, ok := item.(*saveImageWaitItem)
			require.True(t, ok)
			require.Equal(t, tt.wantLocalExport, siwi.localExport)
		})
	}
}

// A push must still happen when local image export is suppressed -- that is the
// entire point of --no-image-output.
func TestSaveImageWaitItemPushUnaffectedByExportArtifactsOnly(t *testing.T) {
	t.Parallel()

	c := &Converter{opt: ConvertOpt{Export: ExportArtifactsOnly}}
	item := newSaveImage(states.SaveImage{DockerTag: testDockerTag}, c, true, false)

	item.SetDoPush()
	item.SetDoSave()

	siwi, ok := item.(*saveImageWaitItem)
	require.True(t, ok)
	require.True(t, siwi.doPush, "push should be unaffected by ExportArtifactsOnly")
	require.False(t, siwi.localExport)
}
