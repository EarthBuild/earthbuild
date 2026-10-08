package earthfile2llb_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArg(t *testing.T) {
	t.Parallel()

	defaultProd := "prod"

	tests := []struct {
		name           string
		errContains    string
		wantInfo       earthfile2llb.ArgInfo
		cmd            earthfile.Command
		isBase         bool
		explicitGlobal bool
		wantErr        bool
	}{
		{
			name: "basic arg with default and description",
			cmd: earthfile.Command{
				Name: earthfile.CmdArg,
				Args: []string{`--description="Environment stage"`, "ENV", "=", "prod"},
			},
			isBase:         false,
			explicitGlobal: true,
			wantInfo: earthfile2llb.ArgInfo{
				DefaultVal:  &defaultProd,
				Name:        "ENV",
				Description: "Environment stage",
				Required:    false,
				Global:      false,
			},
		},
		{
			name: "required global arg",
			cmd: earthfile.Command{
				Name: earthfile.CmdArg,
				Args: []string{"--required", "--global", `--description="API Key"`, "API_KEY"},
			},
			isBase:         true,
			explicitGlobal: true,
			wantInfo: earthfile2llb.ArgInfo{
				DefaultVal:  nil,
				Name:        "API_KEY",
				Description: "API Key",
				Required:    true,
				Global:      true,
			},
		},
		{
			name: "non-ARG command returns error",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"test-arg"},
			},
			wantErr:     true,
			errContains: "non-arg command type",
		},
		{
			name: "invalid syntax returns error",
			cmd: earthfile.Command{
				Name: earthfile.CmdArg,
				Args: []string{"foo", "bar", "baz", "qux"},
			},
			wantErr:     true,
			errContains: "could not parse opts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			info, err := earthfile2llb.ParseArg(tt.cmd, tt.isBase, tt.explicitGlobal)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantInfo.Name, info.Name)
			assert.Equal(t, tt.wantInfo.DefaultVal, info.DefaultVal)
			assert.Equal(t, tt.wantInfo.Description, info.Description)
			assert.Equal(t, tt.wantInfo.Required, info.Required)
			assert.Equal(t, tt.wantInfo.Global, info.Global)
		})
	}
}

func TestArtifactName(t *testing.T) {
	t.Parallel()

	localDest := "out/baz.txt"

	tests := []struct {
		wantLocal   *string
		name        string
		wantName    string
		errContains string
		cmd         earthfile.Command
		wantErr     bool
	}{
		{
			name: "basic artifact",
			cmd: earthfile.Command{
				Name: earthfile.CmdSaveArtifact,
				Args: []string{"artifact.tar"},
			},
			wantName:  "artifact.tar",
			wantLocal: nil,
		},
		{
			name: "artifact saved as local",
			cmd: earthfile.Command{
				Name: earthfile.CmdSaveArtifact,
				Args: []string{"baz.txt", "AS", "LOCAL", "out/baz.txt"},
			},
			wantName:  "baz.txt",
			wantLocal: &localDest,
		},
		{
			name: "non-save-artifact command returns error",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"cat", "input"},
			},
			wantErr:     true,
			errContains: "non-save-artifact command type",
		},
		{
			name: "invalid syntax returns error",
			cmd: earthfile.Command{
				Name: earthfile.CmdSaveArtifact,
				Args: []string{"broken.bin", "extra", "bad"},
			},
			wantErr:     true,
			errContains: "could not parse opts for SAVE ARTIFACT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			name, local, err := earthfile2llb.ArtifactName(tt.cmd)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantLocal, local)
		})
	}
}

func TestImageNames(t *testing.T) {
	t.Parallel()

	const (
		imageV1 = "my-app:v1.0.0"
		imageV2 = "my-app:latest"
	)

	tests := []struct {
		wantNames   []string
		name        string
		errContains string
		cmd         earthfile.Command
		wantErr     bool
	}{
		{
			name: "single image name",
			cmd: earthfile.Command{
				Name: earthfile.CmdSaveImage,
				Args: []string{imageV1},
			},
			wantNames: []string{imageV1},
		},
		{
			name: "multiple image names",
			cmd: earthfile.Command{
				Name: earthfile.CmdSaveImage,
				Args: []string{imageV1, imageV2},
			},
			wantNames: []string{imageV1, imageV2},
		},
		{
			name: "non-save-image command returns error",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"printf", "ok"},
			},
			wantErr:     true,
			errContains: "non-save-image command type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			names, err := earthfile2llb.ImageNames(tt.cmd)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantNames, names)
		})
	}
}

func TestTargetArgs(t *testing.T) {
	t.Parallel()

	ef := earthfile.Tree{
		BaseRecipe: earthfile.Block{
			{
				Command: &earthfile.Command{
					Name: earthfile.CmdArg,
					Args: []string{"GLOBAL_ARG", "=", "default"},
				},
			},
		},
		Targets: []earthfile.Target{
			{
				Name: "build",
				Recipe: earthfile.Block{
					{
						Command: &earthfile.Command{
							Name: earthfile.CmdArg,
							Args: []string{"LOCAL_ARG"},
						},
					},
					{
						Command: &earthfile.Command{
							Name: earthfile.CmdRun,
							Args: []string{"echo", "hi"},
						},
					},
				},
			},
			{
				Name: "empty",
			},
		},
	}

	tests := []struct {
		name        string
		target      string
		errContains string
		wantArgs    []string
		wantErr     bool
	}{
		{
			name:     "base target arguments",
			target:   earthfile.TargetBase,
			wantArgs: []string{"GLOBAL_ARG"},
		},
		{
			name:     "named target arguments",
			target:   "build",
			wantArgs: []string{"LOCAL_ARG"},
		},
		{
			name:     "empty target without arguments",
			target:   "empty",
			wantArgs: nil,
		},
		{
			name:        "nonexistent target returns error",
			target:      "nonexistent",
			wantErr:     true,
			errContains: `target "nonexistent" not found`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args, err := earthfile2llb.TargetArgs(ef, tt.target)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantArgs, args)
		})
	}
}
