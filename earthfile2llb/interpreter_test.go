package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/stretchr/testify/require"
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
		wantHeredoc      *earthfile.Heredoc
		name             string
		wantErr          string
		wantLeadingArgs  []string
		wantTrailingArgs []string
		cmd              earthfile.Command
	}{
		{
			name: "no heredocs preserves args",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"true"},
			},
			wantHeredoc:     nil,
			wantLeadingArgs: []string{"true"},
		},
		{
			name: "preserves non-heredoc args and strips matching heredoc decls",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"cat", "<<<\"here-string\"", "'<<quoted'", "0<<EOF", "<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "hello\n"},
				},
			},
			wantHeredoc:     &earthfile.Heredoc{Name: "EOF", Content: "hello\n"},
			wantLeadingArgs: []string{"cat", "<<<\"here-string\"", "'<<quoted'", "0<<EOF"},
		},
		{
			name: "splits interpreter leading args and script trailing args",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"python3", "<<EOF", "arg1", "arg2"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "print(1)\n"},
				},
			},
			wantHeredoc:      &earthfile.Heredoc{Name: "EOF", Content: "print(1)\n"},
			wantLeadingArgs:  []string{"python3"},
			wantTrailingArgs: []string{"arg1", "arg2"},
		},
		{
			name: "standalone heredoc with trailing arg",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF", "arg1"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "echo \"$1\"\n"},
				},
			},
			wantHeredoc:      &earthfile.Heredoc{Name: "EOF", Content: "echo \"$1\"\n"},
			wantLeadingArgs:  nil,
			wantTrailingArgs: []string{"arg1"},
		},
		{
			name: "cat heredoc with shell redirection trailing args",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"cat", "<<EOF", ">", "/etc/motd"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "hello\n"},
				},
			},
			wantHeredoc:      &earthfile.Heredoc{Name: "EOF", Content: "hello\n"},
			wantLeadingArgs:  []string{"cat"},
			wantTrailingArgs: []string{">", "/etc/motd"},
		},
		{
			name: "chomps heredoc when chomp is true",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "\t\techo hi\n", Chomp: true},
				},
			},
			wantHeredoc:     &earthfile.Heredoc{Name: "EOF", Content: "echo hi\n", Chomp: true},
			wantLeadingArgs: nil,
		},
		{
			name: "normalizes CRLF line endings to LF",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "true\r\n"},
				},
			},
			wantHeredoc: &earthfile.Heredoc{Name: "EOF", Content: "true\n"},
		},
		{
			name: "normalizes CRLF line endings before chomping leading tabs",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "\t\techo hi\r\n", Chomp: true},
				},
			},
			wantHeredoc: &earthfile.Heredoc{Name: "EOF", Content: "echo hi\n", Chomp: true},
		},
		{
			name: "preserves standalone carriage return",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{"<<EOF"},
				Heredocs: []earthfile.Heredoc{
					{Name: "EOF", Content: "printf 'a\rb'\n"},
				},
			},
			wantHeredoc: &earthfile.Heredoc{Name: "EOF", Content: "printf 'a\rb'\n"},
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

			var origContent string
			if len(tt.cmd.Heredocs) > 0 {
				origContent = tt.cmd.Heredocs[0].Content
			}

			i := &Interpreter{}

			h, leadingArgs, trailingArgs, err := i.processRunHeredocs(t.Context(), tt.cmd, tt.cmd.Args)
			if len(tt.cmd.Heredocs) > 0 {
				require.Equal(t, origContent, tt.cmd.Heredocs[0].Content)
			}

			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantHeredoc, h)
			require.Equal(t, tt.wantLeadingArgs, leadingArgs)
			require.Equal(t, tt.wantTrailingArgs, trailingArgs)
		})
	}
}

func TestHandleCopy_HeredocValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantErr  string
		args     []string
		heredocs []earthfile.Heredoc
		local    bool
	}{
		{
			name:    "locally heredoc rejected",
			local:   true,
			args:    []string{"<<EOF", "/dest/path"},
			wantErr: "COPY with heredoc is not supported in LOCALLY targets",
		},
		{
			name:    "missing destination with heredoc last rejected",
			args:    []string{"regular-file.txt", "<<EOF"},
			wantErr: "cannot be a heredoc; missing destination path",
		},
		{
			name:    "mixed regular file then heredoc rejected",
			args:    []string{"regular-file.txt", "<<EOF", "/dest/"},
			wantErr: "mixing heredocs with regular files or artifacts in COPY is not supported",
		},
		{
			name: "mixed source rejected even when heredoc slice count matches srcs",
			args: []string{"regular-file.txt", "<<EOF", "/dest/"},
			heredocs: []earthfile.Heredoc{
				{Name: "unused", Content: "x\n"},
				{Name: "EOF", Content: "hello\n"},
			},
			wantErr: "mixing heredocs with regular files or artifacts in COPY is not supported",
		},
		{
			name:    "mixed sources rejected",
			args:    []string{"<<EOF", "regular-file.txt", "/dest/"},
			wantErr: "mixing heredocs with regular files or artifacts in COPY is not supported",
		},
		{
			name:    "dir flag rejected",
			args:    []string{"--dir", "<<EOF", "/dest/"},
			wantErr: "COPY --dir is not supported with heredoc",
		},
		{
			name:    "symlink-no-follow rejected",
			args:    []string{"--symlink-no-follow", "<<EOF", "/dest/"},
			wantErr: "COPY --symlink-no-follow is not supported with heredoc",
		},
		{
			name:    "platform rejected",
			args:    []string{"--platform=linux/amd64", "<<EOF", "/dest/"},
			wantErr: "COPY --platform is not supported with heredoc",
		},
		{
			name:    "keep-own rejected",
			args:    []string{"--keep-own", "<<EOF", "/dest/"},
			wantErr: "COPY --keep-own is not supported with heredoc",
		},
		{
			name:    "allow-privileged rejected",
			args:    []string{"--allow-privileged", "<<EOF", "/dest/"},
			wantErr: "COPY --allow-privileged is not supported with heredoc",
		},
		{
			name:    "build-arg rejected",
			args:    []string{"--build-arg=FOO=bar", "<<EOF", "/dest/"},
			wantErr: "COPY --build-arg is not supported with heredoc",
		},
		{
			name:    "pass-args rejected",
			args:    []string{"--pass-args", "<<EOF", "/dest/"},
			wantErr: "COPY --pass-args is not supported with heredoc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			heredocs := tt.heredocs
			if heredocs == nil {
				heredocs = []earthfile.Heredoc{
					{Name: "EOF", Content: "hello\n"},
				}
			}

			i := &Interpreter{local: tt.local}
			cmd := earthfile.Command{
				Name:     earthfile.CmdCopy,
				Args:     tt.args,
				Heredocs: heredocs,
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

func TestProcessCopyHeredoc_PreservesCRLF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		wantContent string
		heredoc     earthfile.Heredoc
	}{
		{
			name: "quoted raw CRLF preserved",
			heredoc: earthfile.Heredoc{
				Name:    "EOF",
				Content: "a=1\r\nb=2\r\n",
			},
			wantContent: "a=1\r\nb=2\r\n",
		},
		{
			name: "unquoted expanded CRLF preserved",
			heredoc: earthfile.Heredoc{
				Name:    "EOF",
				Content: "k=$FOO\r\n",
				Expand:  true,
			},
			wantContent: "k=bar\r\n",
		},
		{
			name: "tab-chomped CRLF preserved",
			heredoc: earthfile.Heredoc{
				Name:    "EOF",
				Content: "\ta=1\r\n\tb=2\r\n",
				Chomp:   true,
			},
			wantContent: "a=1\r\nb=2\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newTestConverter(t)
			_, _, err := c.varCollection.DeclareVar(
				"FOO",
				variables.AsArg(),
				variables.WithValue("bar"),
			)
			require.NoError(t, err)

			i := &Interpreter{converter: c}
			cmd := earthfile.Command{
				Name:     earthfile.CmdCopy,
				Args:     []string{"<<EOF", "/etc/app.conf"},
				Heredocs: []earthfile.Heredoc{tt.heredoc},
			}

			err = i.processCopyHeredoc(t.Context(), cmd, cmdopts.Copy{}, []string{"<<EOF"}, "/etc/app.conf")
			require.NoError(t, err)

			ops := marshalStateOps(t, c.mts.Final.MainState)
			require.Len(t, ops.mkfiles, 1)
			require.Equal(t, []byte(tt.wantContent), ops.mkfiles[0].GetData())
		})
	}
}
