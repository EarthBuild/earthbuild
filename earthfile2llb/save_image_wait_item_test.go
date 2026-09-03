package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/states"
	"github.com/stretchr/testify/require"
)

// SetDoSave is what propagates local image export down BUILD edges. It must
// honour NoLocalImageExport, otherwise a child target's image is exported even
// though conversion already declined to export it. See #855.
const testDockerTag = "myimg:latest"

func TestSaveImageWaitItemSetDoSave(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		dockerTag          string
		noLocalImageExport bool
		wantLocalExport    bool
	}{
		{
			name:            "propagates local export by default",
			dockerTag:       testDockerTag,
			wantLocalExport: true,
		},
		{
			name:               "no-image-output suppresses propagated local export",
			noLocalImageExport: true,
			dockerTag:          testDockerTag,
			wantLocalExport:    false,
		},
		{
			name:            "untagged image is never exported",
			dockerTag:       "",
			wantLocalExport: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Converter{opt: ConvertOpt{NoLocalImageExport: tt.noLocalImageExport}}
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
func TestSaveImageWaitItemPushUnaffectedByNoLocalImageExport(t *testing.T) {
	t.Parallel()

	c := &Converter{opt: ConvertOpt{NoLocalImageExport: true}}
	item := newSaveImage(states.SaveImage{DockerTag: testDockerTag}, c, true, false)

	item.SetDoPush()
	item.SetDoSave()

	siwi, ok := item.(*saveImageWaitItem)
	require.True(t, ok)
	require.True(t, siwi.doPush, "push should be unaffected by NoLocalImageExport")
	require.False(t, siwi.localExport)
}
