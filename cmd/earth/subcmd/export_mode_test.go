package subcmd

import (
	"testing"

	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/stretchr/testify/require"
)

func TestResolveExport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		name    string
		flags   outputFlags
		want    earthfile2llb.Export
	}{
		// Plain invocations.
		{
			name:  "no flags exports everything",
			flags: outputFlags{},
			want:  earthfile2llb.ExportAll,
		},
		{
			name:  "no-output suppresses everything",
			flags: outputFlags{NoOutput: true},
			want:  earthfile2llb.ExportNone,
		},
		{
			name:  "no-image-output keeps artifacts",
			flags: outputFlags{NoImageOutput: true},
			want:  earthfile2llb.ExportArtifactsOnly,
		},

		// --ci defaults to writing nothing unless output was asked for.
		{
			name:  "ci alone writes nothing",
			flags: outputFlags{CI: true},
			want:  earthfile2llb.ExportNone,
		},
		{
			name:  "ci with output exports everything",
			flags: outputFlags{CI: true, Output: true},
			want:  earthfile2llb.ExportAll,
		},
		{
			name:  "ci with artifact mode exports everything",
			flags: outputFlags{CI: true, ArtifactMode: true},
			want:  earthfile2llb.ExportAll,
		},
		{
			name:  "ci with image mode exports everything",
			flags: outputFlags{CI: true, ImageMode: true},
			want:  earthfile2llb.ExportAll,
		},

		// The regression this exists to prevent: --no-image-output is a request
		// for artifact output, so --ci must not flatten it to ExportNone. See #855.
		{
			name:  "ci with no-image-output still writes artifacts",
			flags: outputFlags{CI: true, NoImageOutput: true},
			want:  earthfile2llb.ExportArtifactsOnly,
		},
		{
			name:  "ci with output and no-image-output still writes artifacts",
			flags: outputFlags{CI: true, NoImageOutput: true, Output: true},
			want:  earthfile2llb.ExportArtifactsOnly,
		},

		// Contradictions are reported, not silently resolved.
		{
			name:    "no-image-output with image mode is rejected",
			flags:   outputFlags{ImageMode: true, NoImageOutput: true},
			wantErr: errNoImageOutputWithImageMode,
		},
		{
			name:    "no-output with no-image-output is rejected",
			flags:   outputFlags{NoOutput: true, NoImageOutput: true},
			wantErr: errNoOutputWithNoImageOutput,
		},
		{
			name:    "no-output with image mode is rejected",
			flags:   outputFlags{NoOutput: true, ImageMode: true},
			wantErr: errNoOutputWithMode,
		},
		{
			name:    "no-output with artifact mode is rejected",
			flags:   outputFlags{NoOutput: true, ArtifactMode: true},
			wantErr: errNoOutputWithMode,
		},

		// ... except under --ci, where the explicit mode has long won instead.
		{
			name:  "ci lets image mode win over no-output",
			flags: outputFlags{CI: true, NoOutput: true, ImageMode: true},
			want:  earthfile2llb.ExportAll,
		},
		{
			name:  "ci lets artifact mode win over no-output",
			flags: outputFlags{CI: true, NoOutput: true, ArtifactMode: true},
			want:  earthfile2llb.ExportAll,
		},

		// Image mode still rejects no-image-output even under --ci: unlike
		// --no-output there is no historical behaviour to preserve.
		{
			name:    "ci does not excuse no-image-output with image mode",
			flags:   outputFlags{CI: true, ImageMode: true, NoImageOutput: true},
			wantErr: errNoImageOutputWithImageMode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveExport(tt.flags)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
