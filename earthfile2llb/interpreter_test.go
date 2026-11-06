package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/stretchr/testify/require"
)

const (
	testHeredocDecl    = "<<EOF"
	testHeredocName    = "EOF"
	testHeredocContent = "hello\n"
	testDestDir        = "/dest/"
)

func TestHandleRun_Heredocs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr string
		cmd     earthfile.Command
	}{
		{
			name: "multiple heredocs rejected",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF1", "<<EOF2"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF1", Content: "echo 1\n"},
					{Name: "EOF2", Content: "echo 2\n"},
				},
			},
			wantErr: "RUN only supports a single heredoc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			i := &Interpreter{}
			err := i.handleRun(t.Context(), tt.cmd)

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestProcessRunHeredocs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantHeredoc *earthfile.Heredoc
		name        string
		wantErr     string
		wantArgs    []string
		cmd         earthfile.Command
	}{
		{
			name: "no heredocs preserves args",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"true"},
			},
			wantHeredoc: nil,
			wantArgs:    []string{"true"},
		},
		{
			name: "preserves non-heredoc args and strips matching heredoc decls",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"cat", "<<<\"here-string\"", "'<<quoted'", "0<<EOF", testHeredocDecl},
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: testHeredocContent},
				},
			},
			wantHeredoc: &earthfile.Heredoc{Name: testHeredocName, Content: testHeredocContent},
			wantArgs:    []string{"cat", "<<<\"here-string\"", "'<<quoted'", "0<<EOF"},
		},
		{
			name: "chomps heredoc when chomp is true",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{testHeredocDecl},
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: "\t\techo hi\n", Chomp: true},
				},
			},
			wantHeredoc: &earthfile.Heredoc{Name: testHeredocName, Content: "echo hi\n", Chomp: true},
			wantArgs:    nil,
		},
		{
			name: "multiple heredocs rejected",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF1", "<<EOF2"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF1", Content: "echo 1\n"},
					{Name: "EOF2", Content: "echo 2\n"},
				},
			},
			wantErr: "RUN only supports a single heredoc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			i := &Interpreter{}

			h, filteredArgs, err := i.processRunHeredocs(t.Context(), tt.cmd, tt.cmd.Args)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantHeredoc, h)
			require.Equal(t, tt.wantArgs, filteredArgs)
		})
	}
}

func TestHandleCopy_HeredocValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr string
		args    []string
		local   bool
	}{
		{
			name:    "locally heredoc rejected",
			local:   true,
			args:    []string{testHeredocDecl, "/dest/path"},
			wantErr: "COPY with heredoc is not supported in LOCALLY targets",
		},
		{
			name:    "mixed sources rejected",
			args:    []string{testHeredocDecl, "regular-file.txt", testDestDir},
			wantErr: "mixing heredocs with regular files or artifacts in COPY is not supported",
		},
		{
			name:    "dir flag rejected",
			args:    []string{"--dir", testHeredocDecl, testDestDir},
			wantErr: "COPY --dir is not supported with heredoc",
		},
		{
			name:    "symlink-no-follow rejected",
			args:    []string{"--symlink-no-follow", testHeredocDecl, testDestDir},
			wantErr: "COPY --symlink-no-follow is not supported with heredoc",
		},
		{
			name:    "platform rejected",
			args:    []string{"--platform=linux/amd64", testHeredocDecl, testDestDir},
			wantErr: "COPY --platform is not supported with heredoc",
		},
		{
			name:    "keep-own rejected",
			args:    []string{"--keep-own", testHeredocDecl, testDestDir},
			wantErr: "COPY --keep-own is not supported with heredoc",
		},
		{
			name:    "allow-privileged rejected",
			args:    []string{"--allow-privileged", testHeredocDecl, testDestDir},
			wantErr: "COPY --allow-privileged is not supported with heredoc",
		},
		{
			name:    "build-arg rejected",
			args:    []string{"--build-arg=FOO=bar", testHeredocDecl, testDestDir},
			wantErr: "COPY --build-arg is not supported with heredoc",
		},
		{
			name:    "pass-args rejected",
			args:    []string{"--pass-args", testHeredocDecl, testDestDir},
			wantErr: "COPY --pass-args is not supported with heredoc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			i := &Interpreter{local: tt.local}
			cmd := earthfile.Command{
				Name: earthfile.CmdCopy,
				Args: tt.args,
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: testHeredocContent},
				},
			}
			err := i.handleCopy(t.Context(), cmd)

			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestChompHeredocContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "removes common leading tabs",
			input: "\t\techo \"indented with tabs\"\n\t\tif true; then\n\t\t    echo \"nested with spaces\"\n\t\tfi\n",
			want:  "echo \"indented with tabs\"\nif true; then\n    echo \"nested with spaces\"\nfi\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := earthfile.ChompHeredocContent(tt.input)
			require.Equal(t, tt.want, got)
		})
	}
}
