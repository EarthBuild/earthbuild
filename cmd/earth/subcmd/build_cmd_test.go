package subcmd

import (
	"testing"

	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/stretchr/testify/require"
)

// TestToExport tests that the --no-output / --no-image-output flags map onto the
// expected Export, including that the broader --no-output wins when both are given.
func TestToExport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		noOutput      bool
		noImageOutput bool
		expected      earthfile2llb.Export
	}{
		{name: "neither flag exports everything", expected: earthfile2llb.ExportAll},
		{name: "no-image-output keeps artifacts", noImageOutput: true, expected: earthfile2llb.ExportArtifactsOnly},
		{name: "no-output suppresses everything", noOutput: true, expected: earthfile2llb.ExportNone},
		{
			name:          "no-output wins over no-image-output",
			noOutput:      true,
			noImageOutput: true,
			expected:      earthfile2llb.ExportNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.expected, toExport(tt.noOutput, tt.noImageOutput))
		})
	}
}
