package builder

import (
	"testing"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/stretchr/testify/require"
)

// With --use-inline-cache (and no --global-wait-end), SAVE IMAGE in a target's
// implicit wait block keeps SkipBuilder == false, and waitBlock.saveImages now
// skips every such item: "This image is delegated to builder.go for export".
// Before that change the wait block exported these images itself.
//
// Delegation is only sound if planImage does whatever the wait item would have
// done. The wait item pushes when SAVE IMAGE --push is used and the target's
// pushes are on (sts.SetDoPushes, which under VERSION 0.7+ is propagated over
// BUILD edges to remote targets too, see prepBuildTarget). planImage never
// pushes a remote target, so `BUILD github.com/org/repo+img` under
// `earth --push --use-inline-cache` silently stops pushing, while the summary
// written by Converter.SaveImage (AddPushedImageSummary(..., DoPushes)) still
// reports it as pushed.
func TestPlanImagePushesWhatTheWaitBlockDelegates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		target        domain.Target
		waitItemPush  bool
		waitItemLocal bool
	}{
		{
			// Control.
			name:          "local target SAVE IMAGE --push",
			target:        localTarget(),
			waitItemPush:  true,
			waitItemLocal: true,
		},
		{
			name:          "remote target SAVE IMAGE --push reached by BUILD",
			target:        remoteTarget(),
			waitItemPush:  true,
			waitItemLocal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opt := BuildOpt{Export: earthfile2llb.ExportAll, Push: true}
			sts := newSts(t, tt.target, true, true)
			saveImage := states.SaveImage{
				DockerTag:   "registry.example.com/" + testDockerTag,
				Push:        true,
				SkipBuilder: false, // delegated by waitBlock.saveImages
			}

			plan := planImage(opt, sts, false, saveImage)

			require.Equal(t, tt.waitItemLocal, plan.export,
				"builder.go must load the image the wait block delegated to it")
			require.Equal(t, tt.waitItemPush, plan.push,
				"builder.go must push the image the wait block delegated to it")
		})
	}
}
