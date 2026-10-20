package earthfile2llb

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExportString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want   string
		export Export
	}{
		{want: "all", export: ExportAll},
		{want: "artifacts-only", export: ExportArtifactsOnly},
		{want: "none", export: ExportNone},
		{want: "Export(99)", export: Export(99)},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.export.String())
		})
	}
}

func TestExportQuestions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		export    Export
		images    bool
		artifacts bool
	}{
		{name: "all", export: ExportAll, images: true, artifacts: true},
		{name: "artifacts only", export: ExportArtifactsOnly, images: false, artifacts: true},
		{name: "none", export: ExportNone, images: false, artifacts: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.images, tt.export.Images(), "Images()")
			require.Equal(t, tt.artifacts, tt.export.Artifacts(), "Artifacts()")
		})
	}
}

// TestZeroValueExportsEverything pins the zero value, since a ConvertOpt or
// BuildOpt built without setting Export must keep exporting everything.
func TestZeroValueExportsEverything(t *testing.T) {
	t.Parallel()

	var e Export

	require.Equal(t, ExportAll, e)
	require.True(t, e.Images())
	require.True(t, e.Artifacts())
}
