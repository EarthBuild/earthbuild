package earthfile2llb

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"al.essio.dev/pkg/shellescape"
	"github.com/EarthBuild/earthbuild/cleanup"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/states/image"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/llbutil/secretprovider"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	solverpb "github.com/moby/buildkit/solver/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testPipeScriptPath = "/dev/pipes/earth-heredoc-script"
	testEtcDir         = "/etc/"
	testEtcAppConf     = "/etc/app.conf"
	testAppConfName    = "app.conf"
	testRootEOF        = "/EOF"
	testRootAppConf    = "/app.conf"
	testRootInitUTF8   = "/[init]-файл.conf"
	testTwoWordsArg    = "\"two words\""
	testSynthShHeader  = "#!/bin/sh\nset -e\n"
	testSpacesSubdir   = "dir with spaces"
	testFlagArg        = "--flag"
)

func newTestConverter(t *testing.T) *Converter {
	t.Helper()

	platr := platutil.NewResolver(platforms.DefaultSpec())
	ftrs := &features.Features{
		UseChmod:    true,
		UseCopyLink: true,
	}
	target := domain.Target{
		LocalPath: ".",
		Target:    "test",
	}
	bus := logbus.New()
	sts := &states.SingleTarget{
		ID:          "test-target-id",
		Target:      target,
		MainState:   pllb.Scratch(),
		MainImage:   image.NewImage(),
		RanFromLike: true,
	}
	_, err := bus.Run().NewTarget(sts.ID, target, nil, platr.Current().String(), "local")
	require.NoError(t, err)

	varColl := variables.NewCollection(variables.NewCollectionOpt{
		PlatformResolver: platr,
		OverridingVars:   variables.NewScope(),
		Features:         ftrs,
		Target:           target,
	})

	capSet := solverpb.Caps.CapSet(solverpb.Caps.All())

	return &Converter{
		target: target,
		platr:  platr,
		ftrs:   ftrs,
		opt: ConvertOpt{
			Logbus:              bus,
			LLBCaps:             &capSet,
			InternalSecretStore: secretprovider.NewMutableMapStore(nil),
		},
		mts:                 &states.MultiTarget{Final: sts},
		varCollection:       varColl,
		persistentCacheDirs: make(map[string]states.CacheMount),
	}
}

type marshaledLLB struct {
	mkdirs      []*solverpb.FileActionMkDir
	mkfiles     []*solverpb.FileActionMkFile
	copies      []*solverpb.FileActionCopy
	execs       []*solverpb.ExecOp
	customNames []string
}

func marshalStateOps(t *testing.T, st pllb.State) marshaledLLB {
	t.Helper()

	def, err := st.Marshal(t.Context())
	require.NoError(t, err)

	var res marshaledLLB

	for _, dt := range def.Def {
		var op solverpb.Op
		require.NoError(t, op.Unmarshal(dt))

		if execOp := op.GetExec(); execOp != nil {
			res.execs = append(res.execs, execOp)
		}

		fileOp := op.GetFile()
		if fileOp == nil {
			continue
		}

		for _, act := range fileOp.GetActions() {
			if mkdir := act.GetMkdir(); mkdir != nil {
				res.mkdirs = append(res.mkdirs, mkdir)
			}

			if mkfile := act.GetMkfile(); mkfile != nil {
				res.mkfiles = append(res.mkfiles, mkfile)
			}

			if cp := act.GetCopy(); cp != nil {
				res.copies = append(res.copies, cp)
			}
		}
	}

	for _, meta := range def.Metadata {
		if name, ok := meta.Description["llb.customname"]; ok {
			res.customNames = append(res.customNames, name)
		}
	}

	return res
}

func assertCopyOwner(t *testing.T, owner *solverpb.ChownOpt, wantUser, wantGroup string) {
	t.Helper()

	require.NotNil(t, owner)

	if wantUser != "" {
		assert.Equal(t, wantUser, owner.GetUser().GetByName().GetName())
		assert.Equal(t, wantGroup, owner.GetGroup().GetByName().GetName())

		return
	}

	assert.Equal(t, &solverpb.UserOpt_ByID{ByID: 0}, owner.GetUser().GetUser())
	assert.Equal(t, &solverpb.UserOpt_ByID{ByID: 0}, owner.GetGroup().GetUser())
}

func hasCustomNameSuffix(customNames []string, wantSuffix string) bool {
	for _, cn := range customNames {
		if strings.HasSuffix(cn, wantSuffix) {
			return true
		}
	}

	return false
}

func TestNormalizeHeredocFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "simple delimiter", input: testHeredocName, want: testRootEOF},
		{name: "filename with extension", input: testAppConfName, want: testRootAppConf},
		{name: "glob and utf8 filename", input: "[init]-файл.conf", want: testRootInitUTF8},
		{name: "nested relative path", input: "sub/dir/app.conf", want: "/sub/dir/app.conf"},
		{name: "leading slash normalized", input: testRootAppConf, want: testRootAppConf},
		{name: "filename with space", input: "my file", want: "/my file"},
		{name: "empty", input: "", wantErr: true},
		{name: "root slash", input: "/", wantErr: true},
		{name: "double leading slash", input: "//app.conf", wantErr: true},
		{name: "trailing slash", input: "dir/", wantErr: true},
		{name: "dot", input: ".", wantErr: true},
		{name: "dotdot", input: "..", wantErr: true},
		{name: "leading dot segment", input: "./a", wantErr: true},
		{name: "middle dot segment", input: "a/./b", wantErr: true},
		{name: "middle dotdot segment", input: "a/../b", wantErr: true},
		{name: "parent traversal", input: "../etc/passwd", wantErr: true},
		{name: "empty middle segment", input: "a//b", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeHeredocFilePath(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid heredoc file name")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCopyHeredoc_LLBOps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		dest           string
		imageUser      string
		chown          string
		wantCopyDest   string
		wantUser       string
		wantGroup      string
		wantCustomName string
		wantErr        string
		files          []HeredocFile
		wantMkdirs     []string
		wantMkfiles    []string
		wantCopySrcs   []string
		ifExists       bool
	}{
		{
			name:    "rejects empty files slice",
			files:   nil,
			dest:    testEtcDir,
			wantErr: "CopyHeredoc requires at least one file",
		},
		{
			name:    "rejects invalid heredoc file name",
			files:   []HeredocFile{{Name: "../etc/passwd", Content: "bad"}},
			dest:    testEtcDir,
			wantErr: "invalid heredoc file name",
		},
		{
			name:           "copy heredoc into dot directory preserves delimiter filename",
			files:          []HeredocFile{{Name: testHeredocName, Content: testHeredocContent}},
			dest:           ".",
			wantMkfiles:    []string{testRootEOF},
			wantCopySrcs:   []string{testRootEOF},
			wantCopyDest:   "/",
			wantCustomName: "COPY <<EOF .",
		},
		{
			name:           "copy heredoc to explicit destination file path",
			files:          []HeredocFile{{Name: testHeredocName, Content: testHeredocContent}},
			dest:           testEtcAppConf,
			wantMkfiles:    []string{testRootEOF},
			wantCopySrcs:   []string{testRootEOF},
			wantCopyDest:   testEtcAppConf,
			wantCustomName: "COPY <<EOF /etc/app.conf",
		},
		{
			name: "multiple heredoc files copy into destination directory",
			files: []HeredocFile{
				{Name: "a.conf", Content: "a=1\n"},
				{Name: "b.conf", Content: "b=2\n"},
			},
			dest:           "/etc/conf.d",
			wantMkfiles:    []string{"/a.conf", "/b.conf"},
			wantCopySrcs:   []string{"/a.conf", "/b.conf"},
			wantCopyDest:   "/etc/conf.d/",
			wantCustomName: "COPY <<a.conf <<b.conf /etc/conf.d",
		},
		{
			name:           "inherits active USER ownership when chown is unset",
			files:          []HeredocFile{{Name: testAppConfName, Content: "k=v\n"}},
			dest:           testEtcDir,
			imageUser:      "appuser:appgroup",
			wantMkfiles:    []string{testRootAppConf},
			wantCopySrcs:   []string{testRootAppConf},
			wantCopyDest:   testEtcDir,
			wantUser:       "appuser",
			wantGroup:      "appgroup",
			wantCustomName: "COPY <<app.conf /etc/",
		},
		{
			name:           "explicit chown overrides active USER ownership",
			files:          []HeredocFile{{Name: testAppConfName, Content: "k=v\n"}},
			dest:           testEtcDir,
			imageUser:      "appuser:appgroup",
			chown:          "customuser:customgroup",
			wantMkfiles:    []string{testRootAppConf},
			wantCopySrcs:   []string{testRootAppConf},
			wantCopyDest:   testEtcDir,
			wantUser:       "customuser",
			wantGroup:      "customgroup",
			wantCustomName: "COPY <<app.conf /etc/",
		},
		{
			name:           "if-exists with bracket and utf8 name disables wildcard globbing",
			files:          []HeredocFile{{Name: "[init]-файл.conf", Content: "data\n"}},
			dest:           testEtcDir,
			ifExists:       true,
			wantMkfiles:    []string{testRootInitUTF8},
			wantCopySrcs:   []string{testRootInitUTF8},
			wantCopyDest:   testEtcDir,
			wantCustomName: "COPY --if-exists <<[init]-файл.conf /etc/",
		},
		{
			name:           "nested delimiter path creates parent directory first",
			files:          []HeredocFile{{Name: "sub/app.conf", Content: "nested\n"}},
			dest:           testEtcDir,
			wantMkdirs:     []string{"/sub"},
			wantMkfiles:    []string{"/sub/app.conf"},
			wantCopySrcs:   []string{"/sub/app.conf"},
			wantCopyDest:   testEtcDir,
			wantCustomName: "COPY <<sub/app.conf /etc/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newTestConverter(t)
			c.mts.Final.MainImage.Config.User = tt.imageUser

			err := c.CopyHeredoc(t.Context(), tt.files, tt.dest, tt.chown, nil, false, tt.ifExists)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)

			ops := marshalStateOps(t, c.mts.Final.MainState)

			var gotMkdirs, gotMkfiles, gotCopySrcs []string

			for _, mkdir := range ops.mkdirs {
				gotMkdirs = append(gotMkdirs, mkdir.GetPath())
				assert.True(t, mkdir.GetMakeParents())
			}

			for _, mkfile := range ops.mkfiles {
				gotMkfiles = append(gotMkfiles, mkfile.GetPath())
			}

			for _, cp := range ops.copies {
				gotCopySrcs = append(gotCopySrcs, cp.GetSrc())
				assert.Equal(t, tt.wantCopyDest, cp.GetDest())
				assert.False(t, cp.GetAllowWildcard())
				assert.False(t, cp.GetAllowEmptyWildcard())
				assertCopyOwner(t, cp.GetOwner(), tt.wantUser, tt.wantGroup)
			}

			assert.Equal(t, tt.wantMkdirs, gotMkdirs)
			assert.Equal(t, tt.wantMkfiles, gotMkfiles)
			assert.Equal(t, tt.wantCopySrcs, gotCopySrcs)
			assert.True(
				t,
				hasCustomNameSuffix(ops.customNames, tt.wantCustomName),
				"custom names %v did not contain %q",
				ops.customNames,
				tt.wantCustomName,
			)
		})
	}
}

func extractMountedScriptData(t *testing.T, runOpts []llb.RunOption) string {
	t.Helper()

	allOpts := append([]llb.RunOption{llb.Args([]string{testTrueCmd})}, runOpts...)
	st := pllb.Scratch().Run(allOpts...).Root()
	ops := marshalStateOps(t, st)
	require.Len(t, ops.mkfiles, 1)

	return string(ops.mkfiles[0].GetData())
}

func TestSetupHeredocRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		heredoc         *earthfile.Heredoc
		name            string
		wantScriptMount string
		args            []string
		trailingArgs    []string
		wantFinalArgs   []string
		wantDisplayArgs []string
		withEntrypoint  bool
	}{
		{
			name: "bare shell heredoc uses inline fast path with set -e",
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "echo hello\n",
			},
			wantFinalArgs:   []string{"set -e\necho hello\n"},
			wantDisplayArgs: []string{testHeredocDecl},
		},
		{
			name: "shebang heredoc mounts script and keeps WithShell true",
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "#!/usr/bin/env python3\nprint('ok')\n",
			},
			wantFinalArgs:   []string{testPipeScriptPath},
			wantDisplayArgs: []string{testHeredocDecl},
			wantScriptMount: "#!/usr/bin/env python3\nprint('ok')\n",
		},
		{
			name:         "interpreter with leading and trailing args preserves order",
			args:         []string{testPythonCmd, "-u"},
			trailingArgs: []string{testArg1, testTwoWordsArg},
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "import sys\nprint(sys.argv)\n",
			},
			wantFinalArgs:   []string{testPythonCmd, "-u", testPipeScriptPath, testArg1, testTwoWordsArg},
			wantDisplayArgs: []string{testPythonCmd, "-u", testHeredocDecl, testArg1, testTwoWordsArg},
			wantScriptMount: "import sys\nprint(sys.argv)\n",
		},
		{
			name:         "standalone heredoc with trailing args synthesizes sh shebang and set -e",
			trailingArgs: []string{testArg1},
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: testEchoArg1Body,
			},
			wantFinalArgs:   []string{testPipeScriptPath, testArg1},
			wantDisplayArgs: []string{testHeredocDecl, testArg1},
			wantScriptMount: testSynthShHeader + testEchoArg1Body,
		},
		{
			name:           "entrypoint without shebang synthesizes sh shebang and set -e",
			withEntrypoint: true,
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "echo via-entrypoint\n",
			},
			wantFinalArgs:   []string{testPipeScriptPath},
			wantDisplayArgs: []string{testHeredocDecl},
			wantScriptMount: testSynthShHeader + "echo via-entrypoint\n",
		},
		{
			name:           "entrypoint with existing set -e synthesizes sh shebang without duplicating set -e",
			withEntrypoint: true,
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "set -ex\necho via-entrypoint\n",
			},
			wantFinalArgs:   []string{testPipeScriptPath},
			wantDisplayArgs: []string{testHeredocDecl},
			wantScriptMount: "#!/bin/sh\nset -ex\necho via-entrypoint\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Converter{}
			opts := ConvertRunOpts{
				Args:                tt.args,
				Heredoc:             tt.heredoc,
				HeredocTrailingArgs: tt.trailingArgs,
				WithEntrypoint:      tt.withEntrypoint,
			}

			finalArgs, displayArgs, err := c.setupHeredocRun(&opts)
			require.NoError(t, err)
			assert.True(t, opts.WithShell)
			assert.Equal(t, tt.wantFinalArgs, finalArgs)
			assert.Equal(t, tt.wantDisplayArgs, displayArgs)

			if tt.wantScriptMount == "" {
				assert.Empty(t, opts.extraRunOpts)
				return
			}

			require.Len(t, opts.extraRunOpts, 1)
			assert.Equal(t, tt.wantScriptMount, extractMountedScriptData(t, opts.extraRunOpts))
		})
	}
}

func TestSetupHeredocRun_Locally(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	tests := []struct {
		heredoc            *earthfile.Heredoc
		name               string
		tmpSubdir          string
		wantFileContent    string
		args               []string
		trailingArgs       []string
		wantLeadingArgs    []string
		wantTrailingArgs   []string
		wantDisplayArgs    []string
		withEntrypoint     bool
		useCleanCollection bool
		wantQuotedDiffers  bool
	}{
		{
			name:         "nil CleanCollection quotes TMPDIR spaces and preserves leading and trailing args",
			tmpSubdir:    testSpacesSubdir,
			args:         []string{testPythonCmd},
			trailingArgs: []string{testFlagArg},
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "print('local')\n",
			},
			useCleanCollection: false,
			wantLeadingArgs:    []string{testPythonCmd},
			wantTrailingArgs:   []string{testFlagArg},
			wantDisplayArgs:    []string{testPythonCmd, testHeredocDecl, testFlagArg},
			wantFileContent:    "print('local')\n",
			wantQuotedDiffers:  true,
		},
		{
			name:           "non-nil CleanCollection synthesizes sh shebang and removes temp script on Close",
			tmpSubdir:      testSpacesSubdir,
			withEntrypoint: true,
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "echo cleaned\n",
			},
			useCleanCollection: true,
			wantDisplayArgs:    []string{testHeredocDecl},
			wantFileContent:    testSynthShHeader + "echo cleaned\n",
			wantQuotedDiffers:  true,
		},
		{
			name:               "standalone heredoc with trailing args in plain TMPDIR synthesizes shebang",
			tmpSubdir:          "plain-tmp",
			trailingArgs:       []string{testArg1},
			useCleanCollection: true,
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: testEchoArg1Body,
			},
			wantTrailingArgs:  []string{testArg1},
			wantDisplayArgs:   []string{testHeredocDecl, testArg1},
			wantFileContent:   testSynthShHeader + testEchoArg1Body,
			wantQuotedDiffers: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := filepath.Join(t.TempDir(), tt.tmpSubdir)
			require.NoError(t, os.MkdirAll(tmpDir, 0o750))
			t.Setenv("TMPDIR", tmpDir)

			var cleanColl *cleanup.Collection
			if tt.useCleanCollection {
				cleanColl = cleanup.NewCollection()
			}

			c := &Converter{
				opt: ConvertOpt{CleanCollection: cleanColl},
			}
			opts := ConvertRunOpts{
				Args:                tt.args,
				Heredoc:             tt.heredoc,
				HeredocTrailingArgs: tt.trailingArgs,
				Locally:             true,
				WithEntrypoint:      tt.withEntrypoint,
			}

			finalArgs, displayArgs, err := c.setupHeredocRun(&opts)
			require.NoError(t, err)
			assert.True(t, opts.WithShell)
			assert.Equal(t, tt.wantDisplayArgs, displayArgs)

			matches, err := filepath.Glob(filepath.Join(tmpDir, "earth-heredoc-script-*"))
			require.NoError(t, err)
			require.Len(t, matches, 1)

			tmpPath := matches[0]

			t.Cleanup(func() { _ = os.Remove(tmpPath) })

			quotedPath := shellescape.Quote(tmpPath)
			assert.Equal(t, tt.wantQuotedDiffers, quotedPath != tmpPath)
			assert.Equal(t, slices.Concat(tt.wantLeadingArgs, []string{quotedPath}, tt.wantTrailingArgs), finalArgs)

			info, err := os.Stat(tmpPath)
			require.NoError(t, err)
			assert.Equal(t, fs.FileMode(0o700), info.Mode().Perm())

			data, err := fs.ReadFile(os.DirFS(tmpDir), filepath.Base(tmpPath))
			require.NoError(t, err)
			assert.Equal(t, tt.wantFileContent, string(data))

			if cleanColl != nil {
				assert.Empty(t, cleanColl.Close())

				_, statErr := os.Stat(tmpPath)
				assert.ErrorIs(t, statErr, os.ErrNotExist)
			}
		})
	}
}

func TestInternalRun_Heredoc(t *testing.T) {
	t.Parallel()

	tests := []struct {
		heredoc            *earthfile.Heredoc
		name               string
		buildArgKey        string
		buildArgVal        string
		wantCmdContains    string
		wantExportContains string
		entrypoint         []string
		args               []string
		trailingArgs       []string
		withEntrypoint     bool
		noCache            bool
	}{
		{
			name:           "entrypoint slice backing array is not mutated and script runs under shell",
			entrypoint:     []string{"/entrypoint.sh", "--base"},
			withEntrypoint: true,
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "echo one\n",
			},
			wantCmdContains: "/entrypoint.sh --base " + testPipeScriptPath,
		},
		{
			name:         "script-mode heredoc with no-cache exports build arg and passes UUID as shell $0",
			buildArgKey:  "MY_VAR",
			buildArgVal:  "hello world",
			args:         []string{testPythonCmd},
			trailingArgs: []string{testArg1},
			noCache:      true,
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "import os\n",
			},
			wantExportContains: "MY_VAR='hello world'",
			wantCmdContains:    "python3 /dev/pipes/earth-heredoc-script arg1",
		},
		{
			name:        "shebang heredoc exports active build arg via shell wrapper",
			buildArgKey: "APP_ENV",
			buildArgVal: "prod",
			heredoc: &earthfile.Heredoc{
				Name:    testHeredocName,
				Content: "#!/usr/bin/env python3\nprint(1)\n",
			},
			wantExportContains: "APP_ENV=prod",
			wantCmdContains:    testPipeScriptPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newTestConverter(t)

			var backing []string
			if len(tt.entrypoint) > 0 {
				backing = make([]string, 0, len(tt.entrypoint)+2)
				backing = append(backing, tt.entrypoint...)
				backing = append(backing, "sentinel-0", "sentinel-1")
				c.mts.Final.MainImage.Config.Entrypoint = backing[:len(tt.entrypoint)]
			}

			if tt.buildArgKey != "" {
				_, _, err := c.varCollection.DeclareVar(
					tt.buildArgKey,
					variables.AsArg(),
					variables.WithValue(tt.buildArgVal),
				)
				require.NoError(t, err)
			}

			st, err := c.internalRun(t.Context(), ConvertRunOpts{
				CommandName:         "RUN",
				Args:                tt.args,
				Heredoc:             tt.heredoc,
				HeredocTrailingArgs: tt.trailingArgs,
				WithEntrypoint:      tt.withEntrypoint,
				NoCache:             tt.noCache,
			})
			require.NoError(t, err)

			if len(tt.entrypoint) > 0 {
				assert.Equal(t, tt.entrypoint, c.mts.Final.MainImage.Config.Entrypoint)
				assert.Equal(t, []string{"sentinel-0", "sentinel-1"}, backing[len(tt.entrypoint):])
			}

			ops := marshalStateOps(t, st)
			require.Len(t, ops.execs, 1)

			execArgs := ops.execs[0].GetMeta().GetArgs()
			require.GreaterOrEqual(t, len(execArgs), 3)
			assert.Equal(t, shellPath, execArgs[0])
			assert.Equal(t, "-c", execArgs[1])
			assert.Contains(t, execArgs[2], tt.wantCmdContains)

			if tt.wantExportContains != "" {
				assert.Contains(t, execArgs[2], tt.wantExportContains)
			}

			if tt.noCache {
				require.Len(t, execArgs, 4)
				assert.True(t, strings.HasPrefix(execArgs[3], "#"))
				assert.NotContains(t, execArgs[2], execArgs[3])
			} else {
				assert.Len(t, execArgs, 3)
			}
		})
	}
}
