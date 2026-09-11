package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/states"
	"github.com/stretchr/testify/require"
)

// SetDoSave is what propagates local artifact export down BUILD edges. It must
// honour Export != ExportNone, otherwise a child target's artifact is exported even
// though conversion already declined to export it.
func TestSaveArtifactLocalWaitItemSetDoSave(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		export          Export
		wantLocalExport bool
	}{
		{
			name:            "propagates local export by default (ExportAll)",
			export:          ExportAll,
			wantLocalExport: true,
		},
		{
			name:            "no-image-output keeps artifact export (ExportArtifactsOnly)",
			export:          ExportArtifactsOnly,
			wantLocalExport: true,
		},
		{
			name:            "no-output suppresses propagated local export (ExportNone)",
			export:          ExportNone,
			wantLocalExport: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Converter{opt: ConvertOpt{Export: tt.export}}
			sl := states.SaveLocal{}

			// localExport=false mirrors conversion having declined the export;
			// SetDoSave is the path that can flip it back on.
			item := newSaveArtifactLocal(sl, c, false)
			item.SetDoSave()

			salwi, ok := item.(*saveArtifactLocalWaitItem)
			require.True(t, ok)
			require.Equal(t, tt.wantLocalExport, salwi.localExport)
		})
	}
}
