package builder

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExportFor tests that the --no-output / --no-image-output flags map onto the
// expected Export, including that the broader --no-output wins when both are given.
func TestExportFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		noOutput      bool
		noImageOutput bool
		expected      Export
	}{
		{name: "neither flag exports everything", expected: ExportAll},
		{name: "no-image-output keeps artifacts", noImageOutput: true, expected: ExportArtifactsOnly},
		{name: "no-output suppresses everything", noOutput: true, expected: ExportNone},
		{
			name:          "no-output wins over no-image-output",
			noOutput:      true,
			noImageOutput: true,
			expected:      ExportNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.expected, ExportFor(tt.noOutput, tt.noImageOutput))
		})
	}
}
