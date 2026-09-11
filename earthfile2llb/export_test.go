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
