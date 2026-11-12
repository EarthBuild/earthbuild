package earthfile2llb

import (
	"testing"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/stretchr/testify/require"
)

const (
	testHeredocDecl         = "<<EOF"
	testHeredocName         = "EOF"
	testHeredocContent      = "hello\n"
	testDestDir             = "/dest/"
	testTrueCmd             = "true"
	testCRLFTrueBody        = "true\r\n"
	testChompedEchoBody     = "echo hi\n"
	testRawCRLFPairs        = "a=1\r\nb=2\r\n"
	testPythonCmd           = "python3"
	testCatCmd              = "cat"
	testArg1                = "arg1"
	testEchoArg1Body        = "echo \"$1\"\n"
	testRegularFile         = "regular-file.txt"
	testErrMixedCopyHeredoc = "mixing heredocs with regular files or artifacts in COPY is not supported"
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
				Args: []string{testTrueCmd},
			},
			wantHeredoc:     nil,
			wantLeadingArgs: []string{testTrueCmd},
		},
		{
			name: "preserves non-heredoc args and strips matching heredoc decls",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{testCatCmd, "<<<\"here-string\"", "'<<quoted'", "0<<EOF", testHeredocDecl},
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: testHeredocContent},
				},
			},
			wantHeredoc:     &earthfile.Heredoc{Name: testHeredocName, Content: testHeredocContent},
			wantLeadingArgs: []string{testCatCmd, "<<<\"here-string\"", "'<<quoted'", "0<<EOF"},
		},
		{
			name: "splits interpreter leading args and script trailing args",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{testPythonCmd, testHeredocDecl, testArg1, "arg2"},
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: "print(1)\n"},
				},
			},
			wantHeredoc:      &earthfile.Heredoc{Name: testHeredocName, Content: "print(1)\n"},
			wantLeadingArgs:  []string{testPythonCmd},
			wantTrailingArgs: []string{testArg1, "arg2"},
		},
		{
			name: "standalone heredoc with trailing arg",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{testHeredocDecl, testArg1},
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: testEchoArg1Body},
				},
			},
			wantHeredoc:      &earthfile.Heredoc{Name: testHeredocName, Content: testEchoArg1Body},
			wantLeadingArgs:  nil,
			wantTrailingArgs: []string{testArg1},
		},
		{
			name: "cat heredoc with shell redirection trailing args",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{testCatCmd, testHeredocDecl, ">", "/etc/motd"},
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: testHeredocContent},
				},
			},
			wantHeredoc:      &earthfile.Heredoc{Name: testHeredocName, Content: testHeredocContent},
			wantLeadingArgs:  []string{testCatCmd},
			wantTrailingArgs: []string{">", "/etc/motd"},
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
			wantHeredoc:     &earthfile.Heredoc{Name: testHeredocName, Content: testChompedEchoBody, Chomp: true},
			wantLeadingArgs: nil,
		},
		{
			name: "normalizes CRLF line endings to LF",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{testHeredocDecl},
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: testCRLFTrueBody},
				},
			},
			wantHeredoc: &earthfile.Heredoc{Name: testHeredocName, Content: "true\n"},
		},
		{
			name: "normalizes CRLF line endings before chomping leading tabs",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{testHeredocDecl},
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: "\t\techo hi\r\n", Chomp: true},
				},
			},
			wantHeredoc: &earthfile.Heredoc{Name: testHeredocName, Content: testChompedEchoBody, Chomp: true},
		},
		{
			name: "preserves standalone carriage return",
			cmd: earthfile.Command{
				Name: earthfile.CmdRun,
				Args: []string{testHeredocDecl},
				Heredocs: []earthfile.Heredoc{
					{Name: testHeredocName, Content: "printf 'a\rb'\n"},
				},
			},
			wantHeredoc: &earthfile.Heredoc{Name: testHeredocName, Content: "printf 'a\rb'\n"},
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
			args:    []string{testHeredocDecl, "/dest/path"},
			wantErr: "COPY with heredoc is not supported in LOCALLY targets",
		},
		{
			name:    "missing destination with heredoc last rejected",
			args:    []string{testRegularFile, testHeredocDecl},
			wantErr: "cannot be a heredoc; missing destination path",
		},
		{
			name:    "mixed regular file then heredoc rejected",
			args:    []string{testRegularFile, testHeredocDecl, testDestDir},
			wantErr: testErrMixedCopyHeredoc,
		},
		{
			name: "mixed source rejected even when heredoc slice count matches srcs",
			args: []string{testRegularFile, testHeredocDecl, testDestDir},
			heredocs: []earthfile.Heredoc{
				{Name: "unused", Content: "x\n"},
				{Name: testHeredocName, Content: testHeredocContent},
			},
			wantErr: testErrMixedCopyHeredoc,
		},
		{
			name:    "mixed sources rejected",
			args:    []string{testHeredocDecl, testRegularFile, testDestDir},
			wantErr: testErrMixedCopyHeredoc,
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

			heredocs := tt.heredocs
			if heredocs == nil {
				heredocs = []earthfile.Heredoc{
					{Name: testHeredocName, Content: testHeredocContent},
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
				Name:    testHeredocName,
				Content: testRawCRLFPairs,
			},
			wantContent: testRawCRLFPairs,
		},
		{
			name: "unquoted expanded CRLF preserved",
			heredoc: earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "k=$FOO\r\n",
				Expand:  true,
			},
			wantContent: "k=bar\r\n",
		},
		{
			name: "tab-chomped CRLF preserved",
			heredoc: earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "\ta=1\r\n\tb=2\r\n",
				Chomp:   true,
			},
			wantContent: testRawCRLFPairs,
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
				Args:     []string{testHeredocDecl, testEtcAppConf},
				Heredocs: []earthfile.Heredoc{tt.heredoc},
			}

			err = i.processCopyHeredoc(t.Context(), cmd, cmdopts.Copy{}, []string{testHeredocDecl}, testEtcAppConf)
			require.NoError(t, err)

			ops := marshalStateOps(t, c.mts.Final.MainState)
			require.Len(t, ops.mkfiles, 1)
			require.Equal(t, []byte(tt.wantContent), ops.mkfiles[0].GetData())
		})
	}
}
