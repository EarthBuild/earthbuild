package earthfile2llb

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"al.essio.dev/pkg/shellescape"
	"github.com/EarthBuild/earthbuild/cleanup"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/states/image"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/llbutil/secretprovider"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/util/syncutil/semutil"
	"github.com/EarthBuild/earthbuild/util/syncutil/serrgroup"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session/localhost"
	solverpb "github.com/moby/buildkit/solver/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type noopGwClient struct {
	gwclient.Client
}

func (noopGwClient) Solve(_ context.Context, _ gwclient.SolveRequest) (*gwclient.Result, error) {
	return gwclient.NewResult(), nil
}

type noopMultiImageSolver struct{}

func (noopMultiImageSolver) SolveImages(
	_ context.Context, _ []*states.ImageDef,
) (*states.ImageSolverResults, error) {
	resCh := make(chan *states.ImageResult)
	close(resCh)

	errCh := make(chan error)
	close(errCh)

	return &states.ImageSolverResults{
		ResultChan:  resCh,
		ErrChan:     errCh,
		ReleaseFunc: func() {},
	}, nil
}

func newTestConverter(t *testing.T) *Converter {
	t.Helper()

	platr := platutil.NewResolver(platforms.DefaultSpec())
	ftrs := &features.Features{
		NewPlatform: true,
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
	logbusTarget, err := bus.Run().NewTarget(sts.ID, target, nil, platr.Current().String(), "local")
	require.NoError(t, err)

	varColl := variables.NewCollection(variables.NewCollectionOpt{
		PlatformResolver: platr,
		OverridingVars:   variables.NewScope(),
		Features:         ftrs,
		Target:           target,
	})

	capSet := solverpb.Caps.CapSet(solverpb.Caps.All())
	logger := conslogging.New(io.Discard, nil, 0, conslogging.Silent, false)

	return &Converter{
		target:          target,
		platr:           platr,
		ftrs:            ftrs,
		logbusTarget:    logbusTarget,
		localWorkingDir: ".",
		opt: ConvertOpt{
			Logbus:              bus,
			Log:                 logger,
			LLBCaps:             &capSet,
			Features:            ftrs,
			GwClient:            noopGwClient{},
			MultiImageSolver:    noopMultiImageSolver{},
			CacheImports:        states.NewCacheImports(nil),
			Parallelism:         semutil.NewWeighted(1),
			CleanCollection:     cleanup.NewCollection(),
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
		{name: "simple delimiter", input: "EOF", want: "/EOF"},
		{name: "filename with extension", input: "app.conf", want: "/app.conf"},
		{name: "glob and utf8 filename", input: "[init]-файл.conf", want: "/[init]-файл.conf"},
		{name: "nested relative path", input: "sub/dir/app.conf", want: "/sub/dir/app.conf"},
		{name: "leading slash normalized", input: "/app.conf", want: "/app.conf"},
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
			dest:    "/etc/",
			wantErr: "CopyHeredoc requires at least one file",
		},
		{
			name:    "rejects invalid heredoc file name",
			files:   []HeredocFile{{Name: "../etc/passwd", Content: "bad"}},
			dest:    "/etc/",
			wantErr: "invalid heredoc file name",
		},
		{
			name:           "copy heredoc into dot directory preserves delimiter filename",
			files:          []HeredocFile{{Name: "EOF", Content: "hello\n"}},
			dest:           ".",
			wantMkfiles:    []string{"/EOF"},
			wantCopySrcs:   []string{"/EOF"},
			wantCopyDest:   "/",
			wantCustomName: "COPY <<EOF .",
		},
		{
			name:           "copy heredoc to explicit destination file path",
			files:          []HeredocFile{{Name: "EOF", Content: "hello\n"}},
			dest:           "/etc/app.conf",
			wantMkfiles:    []string{"/EOF"},
			wantCopySrcs:   []string{"/EOF"},
			wantCopyDest:   "/etc/app.conf",
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
			files:          []HeredocFile{{Name: "app.conf", Content: "k=v\n"}},
			dest:           "/etc/",
			imageUser:      "appuser:appgroup",
			wantMkfiles:    []string{"/app.conf"},
			wantCopySrcs:   []string{"/app.conf"},
			wantCopyDest:   "/etc/",
			wantUser:       "appuser",
			wantGroup:      "appgroup",
			wantCustomName: "COPY <<app.conf /etc/",
		},
		{
			name:           "explicit chown overrides active USER ownership",
			files:          []HeredocFile{{Name: "app.conf", Content: "k=v\n"}},
			dest:           "/etc/",
			imageUser:      "appuser:appgroup",
			chown:          "customuser:customgroup",
			wantMkfiles:    []string{"/app.conf"},
			wantCopySrcs:   []string{"/app.conf"},
			wantCopyDest:   "/etc/",
			wantUser:       "customuser",
			wantGroup:      "customgroup",
			wantCustomName: "COPY <<app.conf /etc/",
		},
		{
			name:           "if-exists with bracket and utf8 name disables wildcard globbing",
			files:          []HeredocFile{{Name: "[init]-файл.conf", Content: "data\n"}},
			dest:           "/etc/",
			ifExists:       true,
			wantMkfiles:    []string{"/[init]-файл.conf"},
			wantCopySrcs:   []string{"/[init]-файл.conf"},
			wantCopyDest:   "/etc/",
			wantCustomName: "COPY --if-exists <<[init]-файл.conf /etc/",
		},
		{
			name:           "nested delimiter path creates parent directory first",
			files:          []HeredocFile{{Name: "sub/app.conf", Content: "nested\n"}},
			dest:           "/etc/",
			wantMkdirs:     []string{"/sub"},
			wantMkfiles:    []string{"/sub/app.conf"},
			wantCopySrcs:   []string{"/sub/app.conf"},
			wantCopyDest:   "/etc/",
			wantCustomName: "COPY <<sub/app.conf /etc/",
		},
		{
			name:           "copy heredoc preserves raw CRLF bytes",
			files:          []HeredocFile{{Name: "app.conf", Content: "line1\r\nline2\r\n"}},
			dest:           "/etc/app.conf",
			wantMkfiles:    []string{"/app.conf"},
			wantCopySrcs:   []string{"/app.conf"},
			wantCopyDest:   "/etc/app.conf",
			wantCustomName: "COPY <<app.conf /etc/app.conf",
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

			for idx, mkfile := range ops.mkfiles {
				gotMkfiles = append(gotMkfiles, mkfile.GetPath())

				require.Less(t, idx, len(tt.files))
				assert.Equal(t, []byte(tt.files[idx].Content), mkfile.GetData())
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

	allOpts := append([]llb.RunOption{llb.Args([]string{"true"})}, runOpts...)
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
				Name:    "EOF",
				Content: "echo hello\n",
			},
			wantFinalArgs:   []string{"set -e\necho hello\n"},
			wantDisplayArgs: []string{"<<EOF"},
		},
		{
			name: "shebang heredoc mounts script and keeps WithShell true",
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "#!/usr/bin/env python3\nprint('ok')\n",
			},
			wantFinalArgs:   []string{"/dev/pipes/earth-heredoc-script"},
			wantDisplayArgs: []string{"<<EOF"},
			wantScriptMount: "#!/usr/bin/env python3\nprint('ok')\n",
		},
		{
			name:         "interpreter with leading and trailing args preserves order",
			args:         []string{"python3", "-u"},
			trailingArgs: []string{"arg1", "\"two words\""},
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "import sys\nprint(sys.argv)\n",
			},
			wantFinalArgs: []string{
				"python3", "-u", "/dev/pipes/earth-heredoc-script", "arg1", "\"two words\"",
			},
			wantDisplayArgs: []string{"python3", "-u", "<<EOF", "arg1", "\"two words\""},
			wantScriptMount: "import sys\nprint(sys.argv)\n",
		},
		{
			name:         "standalone heredoc with trailing args synthesizes sh shebang and set -e",
			trailingArgs: []string{"arg1"},
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "echo \"$1\"\n",
			},
			wantFinalArgs:   []string{"/dev/pipes/earth-heredoc-script", "arg1"},
			wantDisplayArgs: []string{"<<EOF", "arg1"},
			wantScriptMount: "#!/bin/sh\nset -e\necho \"$1\"\n",
		},
		{
			name:           "entrypoint without shebang synthesizes sh shebang and set -e",
			withEntrypoint: true,
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "echo via-entrypoint\n",
			},
			wantFinalArgs:   []string{"/dev/pipes/earth-heredoc-script"},
			wantDisplayArgs: []string{"<<EOF"},
			wantScriptMount: "#!/bin/sh\nset -e\necho via-entrypoint\n",
		},
		{
			name:           "entrypoint with existing set -e synthesizes sh shebang without duplicating set -e",
			withEntrypoint: true,
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "set -ex\necho via-entrypoint\n",
			},
			wantFinalArgs:   []string{"/dev/pipes/earth-heredoc-script"},
			wantDisplayArgs: []string{"<<EOF"},
			wantScriptMount: "#!/bin/sh\nset -ex\necho via-entrypoint\n",
		},
		{
			name: "bare CRLF heredoc normalizes line endings on inline fast path",
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "true\r\n",
			},
			wantFinalArgs:   []string{"set -e\ntrue\n"},
			wantDisplayArgs: []string{"<<EOF"},
		},
		{
			name: "bare CRLF heredoc starting with set -e normalizes without duplicating set -e",
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "set -e\r\ntrue\r\n",
			},
			wantFinalArgs:   []string{"set -e\ntrue\n"},
			wantDisplayArgs: []string{"<<EOF"},
		},
		{
			name: "CRLF shebang heredoc normalizes interpreter and script lines",
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "#!/bin/sh\r\ntrue\r\n",
			},
			wantFinalArgs:   []string{"/dev/pipes/earth-heredoc-script"},
			wantDisplayArgs: []string{"<<EOF"},
			wantScriptMount: "#!/bin/sh\ntrue\n",
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
			tmpSubdir:    "dir with spaces",
			args:         []string{"python3"},
			trailingArgs: []string{"--flag"},
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "print('local')\n",
			},
			useCleanCollection: false,
			wantLeadingArgs:    []string{"python3"},
			wantTrailingArgs:   []string{"--flag"},
			wantDisplayArgs:    []string{"python3", "<<EOF", "--flag"},
			wantFileContent:    "print('local')\n",
			wantQuotedDiffers:  true,
		},
		{
			name:           "non-nil CleanCollection synthesizes sh shebang and removes temp script on Close",
			tmpSubdir:      "dir with spaces",
			withEntrypoint: true,
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "echo cleaned\n",
			},
			useCleanCollection: true,
			wantDisplayArgs:    []string{"<<EOF"},
			wantFileContent:    "#!/bin/sh\nset -e\necho cleaned\n",
			wantQuotedDiffers:  true,
		},
		{
			name:               "standalone heredoc with trailing args in plain TMPDIR synthesizes shebang",
			tmpSubdir:          "plain-tmp",
			trailingArgs:       []string{"arg1"},
			useCleanCollection: true,
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "echo \"$1\"\n",
			},
			wantTrailingArgs:  []string{"arg1"},
			wantDisplayArgs:   []string{"<<EOF", "arg1"},
			wantFileContent:   "#!/bin/sh\nset -e\necho \"$1\"\n",
			wantQuotedDiffers: false,
		},
		{
			name:               "CRLF shebang heredoc writes LF-normalized script to temp file",
			tmpSubdir:          "crlf-tmp",
			useCleanCollection: true,
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
				Content: "#!/bin/sh\r\ntrue\r\n",
			},
			wantDisplayArgs:   []string{"<<EOF"},
			wantFileContent:   "#!/bin/sh\ntrue\n",
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
				Name:    "EOF",
				Content: "echo one\n",
			},
			wantCmdContains: "/entrypoint.sh --base /dev/pipes/earth-heredoc-script",
		},
		{
			name:         "script-mode heredoc with no-cache exports build arg and passes UUID as shell $0",
			buildArgKey:  "MY_VAR",
			buildArgVal:  "hello world",
			args:         []string{"python3"},
			trailingArgs: []string{"arg1"},
			noCache:      true,
			heredoc: &earthfile.Heredoc{
				Name:    "EOF",
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
				Name:    "EOF",
				Content: "#!/usr/bin/env python3\nprint(1)\n",
			},
			wantExportContains: "APP_ENV=prod",
			wantCmdContains:    "/dev/pipes/earth-heredoc-script",
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

func TestEndToEndHeredoc_CRLF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		earthfile        string
		wantExecContains string
		wantMkfileData   string
	}{
		{
			name:             "quoted bare RUN heredoc normalizes CRLF to LF",
			earthfile:        "VERSION 0.8\r\nFROM scratch\r\nRUN <<'EOF'\r\ntrue\r\nEOF\r\n",
			wantExecContains: "'set -e\ntrue\n'",
		},
		{
			name:             "unquoted expanded RUN heredoc normalizes CRLF to LF",
			earthfile:        "VERSION 0.8\r\nFROM scratch\r\nARG MSG=hello\r\nRUN <<EOF\r\necho $MSG\r\nEOF\r\n",
			wantExecContains: "'set -e\necho hello\n'",
		},
		{
			name:           "quoted COPY heredoc preserves raw CRLF bytes",
			earthfile:      "VERSION 0.8\r\nFROM scratch\r\nCOPY <<'EOF' /etc/app.conf\r\nk=val\r\nEOF\r\n",
			wantMkfileData: "k=val\r\n",
		},
		{
			name: "unquoted tab-chomped COPY heredoc expands vars and preserves CRLF bytes",
			earthfile: "VERSION 0.8\r\nFROM scratch\r\nARG MSG=val\r\n" +
				"COPY <<-EOF /etc/app.conf\r\n\tk=$MSG\r\nEOF\r\n",
			wantMkfileData: "k=val\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ef, err := earthfile.Parse("Earthfile", tt.earthfile)
			require.NoError(t, err)

			c := newTestConverter(t)
			interp := newInterpreter(
				c,
				domain.Target{LocalPath: ".", Target: earthfile.TargetBase},
				true,
				false,
				c.opt.Log,
				nil,
			)
			require.NoError(t, interp.Run(t.Context(), ef))

			ops := marshalStateOps(t, c.mts.Final.MainState)

			if tt.wantExecContains != "" {
				require.Len(t, ops.execs, 1)

				execArgs := ops.execs[0].GetMeta().GetArgs()
				require.Len(t, execArgs, 3)
				assert.Equal(t, []string{shellPath, "-c"}, execArgs[:2])
				assert.Contains(t, execArgs[2], tt.wantExecContains)
				assert.NotContains(t, execArgs[2], "\r")

				return
			}

			require.Len(t, ops.mkfiles, 1)
			assert.Equal(t, []byte(tt.wantMkfileData), ops.mkfiles[0].GetData())
		})
	}
}

func TestWithDockerRun_Heredoc(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		earthfile       string
		wantScriptMount string
		wantUserCmd     string
		wantCustomName  string
		useRegistry     bool
	}{
		{
			name: "tar runner standalone heredoc forwards trailing args and normalizes CRLF",
			earthfile: "VERSION 0.8\r\nFROM scratch\r\nWITH DOCKER\r\n" +
				"    RUN <<'EOF' arg1 \"two words\"\r\necho \"$1|$2\"\r\nEOF\r\nEND\r\n",
			useRegistry:     false,
			wantScriptMount: "#!/bin/sh\nset -e\necho \"$1|$2\"\n",
			wantUserCmd:     "'/dev/pipes/earth-heredoc-script arg1 \"two words\"'",
			wantCustomName:  "WITH DOCKER RUN  --privileged <<EOF arg1 \"two words\"",
		},
		{
			name: "tar runner bare CRLF heredoc uses inline fast path",
			earthfile: "VERSION 0.8\r\nFROM scratch\r\nWITH DOCKER\r\n" +
				"    RUN <<'EOF'\r\ntrue\r\nEOF\r\nEND\r\n",
			useRegistry:    false,
			wantUserCmd:    "'set -e\ntrue\n'",
			wantCustomName: "WITH DOCKER RUN  --privileged <<EOF",
		},
		{
			name: "tar runner custom interpreter forwards trailing args and normalizes CRLF",
			earthfile: "VERSION 0.8\r\nFROM scratch\r\nWITH DOCKER\r\n" +
				"    RUN python3 <<'EOF' arg1 \"two words\"\r\nimport sys\r\nprint(sys.argv)\r\nEOF\r\nEND\r\n",
			useRegistry:     false,
			wantScriptMount: "import sys\nprint(sys.argv)\n",
			wantUserCmd:     "'python3 /dev/pipes/earth-heredoc-script arg1 \"two words\"'",
			wantCustomName:  "WITH DOCKER RUN  --privileged python3 <<EOF arg1 \"two words\"",
		},
		{
			name: "registry runner standalone heredoc forwards trailing args and normalizes CRLF",
			earthfile: "VERSION 0.8\r\nFROM scratch\r\nWITH DOCKER\r\n" +
				"    RUN <<'EOF' arg1 \"two words\"\r\necho \"$1|$2\"\r\nEOF\r\nEND\r\n",
			useRegistry:     true,
			wantScriptMount: "#!/bin/sh\nset -e\necho \"$1|$2\"\n",
			wantUserCmd:     "'/dev/pipes/earth-heredoc-script arg1 \"two words\"'",
			wantCustomName:  "WITH DOCKER RUN  --privileged <<EOF arg1 \"two words\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ef, err := earthfile.Parse("Earthfile", tt.earthfile)
			require.NoError(t, err)

			c := newTestConverter(t)
			c.ftrs.UseRegistryForWithDocker = tt.useRegistry

			eg, ctx := serrgroup.WithContext(t.Context())
			c.opt.ErrorGroup = eg

			interp := newInterpreter(
				c,
				domain.Target{LocalPath: ".", Target: earthfile.TargetBase},
				true,
				false,
				c.opt.Log,
				nil,
			)
			require.NoError(t, interp.Run(ctx, ef))
			require.NoError(t, eg.Wait())

			ops := marshalStateOps(t, c.mts.Final.MainState)
			require.Len(t, ops.execs, 2)

			execArgs := ops.execs[1].GetMeta().GetArgs()
			require.Len(t, execArgs, 3)
			assert.Equal(t, []string{shellPath, "-c"}, execArgs[:2])

			flags, userCmd, found := strings.Cut(execArgs[2], " -- ")
			require.True(t, found)
			assert.Contains(t, flags, dockerdWrapperPath+" execute")
			assert.Contains(t, userCmd, tt.wantUserCmd)
			assert.NotContains(t, userCmd, "\r")

			if tt.wantScriptMount == "" {
				assert.Empty(t, ops.mkfiles)
			} else {
				require.Len(t, ops.mkfiles, 1)
				assert.Equal(t, []byte(tt.wantScriptMount), ops.mkfiles[0].GetData())
			}

			assert.True(
				t,
				hasCustomNameSuffix(ops.customNames, tt.wantCustomName),
				"custom names %v did not contain %q",
				ops.customNames,
				tt.wantCustomName,
			)
			assert.Empty(t, c.opt.CleanCollection.Close())
		})
	}
}

func TestWithDockerRun_HeredocLocally(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	tests := []struct {
		name             string
		tmpSubdir        string
		useRegistry      bool
		useLocalRegistry bool
	}{
		{
			name:             "local tar runner writes LF script and forwards trailing args",
			tmpSubdir:        "dir with spaces",
			useRegistry:      false,
			useLocalRegistry: false,
		},
		{
			name:             "local registry runner writes LF script and forwards trailing args",
			tmpSubdir:        "local-reg-tmp",
			useRegistry:      true,
			useLocalRegistry: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := filepath.Join(t.TempDir(), tt.tmpSubdir)
			require.NoError(t, os.MkdirAll(tmpDir, 0o750))
			t.Setenv("TMPDIR", tmpDir)

			ef, err := earthfile.Parse(
				"Earthfile",
				"VERSION 0.8\r\nLOCALLY\r\nWITH DOCKER\r\n"+
					"    RUN <<'EOF' arg1 \"two words\"\r\necho \"$1|$2\"\r\nEOF\r\nEND\r\n",
			)
			require.NoError(t, err)

			c := newTestConverter(t)
			c.platr.AllowNativeAndUser = true
			c.ftrs.UseRegistryForWithDocker = tt.useRegistry
			c.opt.UseLocalRegistry = tt.useLocalRegistry
			c.opt.AllowLocally = true

			eg, ctx := serrgroup.WithContext(t.Context())
			c.opt.ErrorGroup = eg

			interp := newInterpreter(
				c,
				domain.Target{LocalPath: ".", Target: earthfile.TargetBase},
				true,
				false,
				c.opt.Log,
				nil,
			)
			require.NoError(t, interp.Run(ctx, ef))
			require.NoError(t, eg.Wait())

			matches, err := filepath.Glob(filepath.Join(tmpDir, "earth-heredoc-script-*"))
			require.NoError(t, err)
			require.Len(t, matches, 1)

			tmpPath := matches[0]

			t.Cleanup(func() { _ = os.Remove(tmpPath) })

			info, err := os.Stat(tmpPath)
			require.NoError(t, err)
			assert.Equal(t, fs.FileMode(0o700), info.Mode().Perm())

			data, err := fs.ReadFile(os.DirFS(tmpDir), filepath.Base(tmpPath))
			require.NoError(t, err)
			assert.Equal(t, "#!/bin/sh\nset -e\necho \"$1|$2\"\n", string(data))

			ops := marshalStateOps(t, c.mts.Final.MainState)
			require.Len(t, ops.execs, 1)

			execArgs := ops.execs[0].GetMeta().GetArgs()
			require.Len(t, execArgs, 4)
			assert.Equal(t, []string{localhost.RunOnLocalHostMagicStr, shellPath, "-c"}, execArgs[:3])

			quotedPath := shellescape.Quote(tmpPath)
			wantLocalCmd := "'" + escapeShellSingleQuotes(quotedPath) + " arg1 \"two words\"'"
			assert.Contains(t, execArgs[3], wantLocalCmd)
			assert.NotContains(t, execArgs[3], "\r")

			assert.True(
				t,
				hasCustomNameSuffix(ops.customNames, "WITH DOCKER RUN  <<EOF arg1 \"two words\""),
				"custom names %v did not contain %q",
				ops.customNames,
				"WITH DOCKER RUN  <<EOF arg1 \"two words\"",
			)

			assert.Empty(t, c.opt.CleanCollection.Close())

			_, statErr := os.Stat(tmpPath)
			assert.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}
