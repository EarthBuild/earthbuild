package builder

import (
	"testing"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/stretchr/testify/require"
)

const testDockerTag = "myimg:latest"

func localTarget() domain.Target {
	return domain.Target{LocalPath: "."}
}

func remoteTarget() domain.Target {
	return domain.Target{GitURL: "github.com/example/repo"}
}

// newSts builds a SingleTarget in the state planImage cares about: whether the
// target participates in saves and pushes at all.
func newSts(t *testing.T, target domain.Target, doSaves, doPushes bool) *states.SingleTarget {
	t.Helper()

	sts := &states.SingleTarget{Target: target}
	if doSaves {
		sts.SetDoSaves()
	}

	if doPushes {
		sts.SetDoPushes()
	}

	return sts
}

func TestPlanImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		opt        BuildOpt
		target     domain.Target
		saveImage  states.SaveImage
		doSaves    bool
		doPushes   bool
		isFinal    bool
		wantExport bool
		wantPush   bool
	}{
		{
			name:       "tagged image with saves on is exported",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll},
			target:     localTarget(),
			doSaves:    true,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag},
			wantExport: true,
		},
		{
			name:       "push and export together",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll, Push: true},
			target:     localTarget(),
			doSaves:    true,
			doPushes:   true,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag, Push: true},
			wantExport: true,
			wantPush:   true,
		},
		{
			// The case #855 exists for: push to the registry, skip the local load.
			name:       "no-image-output pushes without exporting",
			opt:        BuildOpt{Export: earthfile2llb.ExportArtifactsOnly, Push: true},
			target:     localTarget(),
			doSaves:    true,
			doPushes:   true,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag, Push: true},
			wantExport: false,
			wantPush:   true,
		},
		{
			name:       "no-output suppresses the export but not the push",
			opt:        BuildOpt{Export: earthfile2llb.ExportNone, Push: true},
			target:     localTarget(),
			doSaves:    true,
			doPushes:   true,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag, Push: true},
			wantExport: false,
			wantPush:   true,
		},
		{
			name:       "untagged image is neither exported nor pushed",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll, Push: true},
			target:     localTarget(),
			doSaves:    true,
			doPushes:   true,
			isFinal:    true,
			saveImage:  states.SaveImage{Push: true},
			wantExport: false,
			wantPush:   false,
		},
		{
			name:       "target with saves off is not exported",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll},
			target:     localTarget(),
			doSaves:    false,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag},
			wantExport: false,
		},
		{
			name:       "ForceSave exports even when the target has saves off",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll},
			target:     localTarget(),
			doSaves:    false,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag, ForceSave: true},
			wantExport: true,
		},
		{
			name:       "remote targets are never pushed",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll, Push: true},
			target:     remoteTarget(),
			doSaves:    true,
			doPushes:   true,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag, Push: true},
			wantExport: true,
			wantPush:   false,
		},
		{
			name:       "artifact mode suppresses image export",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll, OnlyArtifact: &domain.Artifact{}},
			target:     localTarget(),
			doSaves:    true,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag},
			wantExport: false,
		},
		{
			name:       "image mode exports only the final target",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll, OnlyFinalTargetImages: true},
			target:     localTarget(),
			doSaves:    true,
			isFinal:    false,
			saveImage:  states.SaveImage{DockerTag: testDockerTag},
			wantExport: false,
		},
		{
			name:       "image mode exports the final target",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll, OnlyFinalTargetImages: true},
			target:     localTarget(),
			doSaves:    true,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag},
			wantExport: true,
		},
		{
			name:       "push requires the image to opt in",
			opt:        BuildOpt{Export: earthfile2llb.ExportAll, Push: true},
			target:     localTarget(),
			doSaves:    true,
			doPushes:   true,
			isFinal:    true,
			saveImage:  states.SaveImage{DockerTag: testDockerTag, Push: false},
			wantExport: true,
			wantPush:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sts := newSts(t, tt.target, tt.doSaves, tt.doPushes)

			plan := planImage(tt.opt, sts, tt.isFinal, tt.saveImage)

			require.Equal(t, tt.wantExport, plan.export, "export")
			require.Equal(t, tt.wantPush, plan.push, "push")
		})
	}
}
