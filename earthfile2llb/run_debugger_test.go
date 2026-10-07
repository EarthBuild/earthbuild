package earthfile2llb

import (
	"strings"
	"testing"

	debuggercommon "github.com/EarthBuild/earthbuild/debugger/common"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/gatewaycrafter"
	"github.com/EarthBuild/earthbuild/util/llbutil/pllb"
	"github.com/EarthBuild/earthbuild/util/llbutil/secretprovider"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/containerd/platforms"
	solverpb "github.com/moby/buildkit/solver/pb"
)

func TestRunNeedsDebugger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		locally         bool
		debuggerEnabled bool
		isInteractive   bool
		hasSaveFiles    bool
		want            bool
	}{
		{name: "plain RUN", want: false},
		{name: "-i", debuggerEnabled: true, want: true},
		{name: "RUN --interactive", isInteractive: true, want: true},
		{name: "TRY/FINALLY save files", hasSaveFiles: true, want: true},
		{name: "LOCALLY", locally: true, want: false},
		{name: "LOCALLY with -i", locally: true, debuggerEnabled: true, isInteractive: true, hasSaveFiles: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := runNeedsDebugger(tt.locally, tt.debuggerEnabled, tt.isInteractive, tt.hasSaveFiles)
			if got != tt.want {
				t.Errorf("runNeedsDebugger() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShellWrapDebuggerPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		withDebugger bool
		want         bool
	}{
		{name: "without debugger", withDebugger: false, want: false},
		{name: "with debugger", withDebugger: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := withShellAndEnvVars([]string{"echo", "hi"}, nil, true, tt.withDebugger, false)

			got := strings.Contains(strings.Join(args, " "), debuggerPath)
			if got != tt.want {
				t.Errorf("command %q contains %s = %v, want %v", args, debuggerPath, got, tt.want)
			}
		})
	}
}

// newDebuggerTestConverter returns a minimal Converter able to run
// internalRun against a scratch state, rooted at localPath.
func newDebuggerTestConverter(t *testing.T, localPath string, debuggerEnabled bool) *Converter {
	t.Helper()

	target := domain.Target{LocalPath: localPath, Target: "test"}
	platr := platutil.NewResolver(platforms.DefaultSpec())
	ftrs := &features.Features{}
	caps := solverpb.Caps.CapSet(solverpb.Caps.All())

	return &Converter{
		target: target,
		platr:  platr,
		ftrs:   ftrs,
		mts: &states.MultiTarget{
			Final: &states.SingleTarget{ID: "test-id", Target: target, MainState: pllb.Scratch()},
		},
		varCollection: variables.NewCollection(variables.NewCollectionOpt{
			Target:           target,
			PlatformResolver: platr,
			Features:         ftrs,
		}),
		opt: ConvertOpt{
			AllowInteractive:           true,
			InteractiveDebuggerEnabled: debuggerEnabled,
			LLBCaps:                    &caps,
			InternalSecretStore:        secretprovider.NewMutableMapStore(nil),
			LocalArtifactWhiteList:     gatewaycrafter.NewLocalArtifactWhiteList(),
			Logbus:                     logbus.New(),
		},
	}
}

// debuggerPlumbing describes the debugger-related parts of an ExecOp.
type debuggerPlumbing struct {
	interactiveSocket bool
	saveFileSocket    bool
	settingsSecret    bool
	hostBind          bool
	commandPrefix     bool
	sshSocket         bool
}

// execDebuggerPlumbing marshals st and reports the debugger plumbing found on
// its single ExecOp.
func execDebuggerPlumbing(t *testing.T, st pllb.State) debuggerPlumbing {
	t.Helper()

	def, err := st.Marshal(t.Context())
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}

	var (
		got   debuggerPlumbing
		execs int
	)

	for _, dt := range def.Def {
		var op solverpb.Op
		if err := op.Unmarshal(dt); err != nil {
			t.Fatalf("unmarshal op: %v", err)
		}

		exec := op.GetExec()
		if exec == nil {
			continue
		}

		execs++

		for _, m := range exec.GetMounts() {
			switch {
			case m.GetMountType() == solverpb.MountType_SSH:
				got.sshSocket = true
			case m.GetMountType() == solverpb.MountType_SOCKET && m.GetDest() == debuggercommon.DebuggerDefaultSocketPath:
				got.interactiveSocket = true
			case m.GetMountType() == solverpb.MountType_SOCKET && m.GetDest() == debuggercommon.DefaultSaveFileSocketPath:
				got.saveFileSocket = true
			case m.GetMountType() == solverpb.MountType_SECRET &&
				m.GetDest() == "/run/secrets/"+debuggercommon.DebuggerSettingsSecretsKey:
				got.settingsSecret = true
			case m.GetMountType() == solverpb.MountType_HOST_BIND && m.GetDest() == debuggerPath:
				got.hostBind = true
			}
		}

		got.commandPrefix = strings.Contains(strings.Join(exec.GetMeta().GetArgs(), " "), debuggerPath)
	}

	if execs != 1 {
		t.Fatalf("got %d exec ops, want 1", execs)
	}

	return got
}

func TestInternalRunDebuggerPlumbing(t *testing.T) {
	t.Parallel()

	all := debuggerPlumbing{
		interactiveSocket: true,
		saveFileSocket:    true,
		settingsSecret:    true,
		hostBind:          true,
		commandPrefix:     true,
	}

	tests := []struct {
		name            string
		debuggerEnabled bool
		opts            ConvertRunOpts
		want            debuggerPlumbing
	}{
		{
			name: "plain RUN",
			opts: ConvertRunOpts{},
			want: debuggerPlumbing{},
		},
		{
			name: "plain RUN --ssh",
			opts: ConvertRunOpts{WithSSH: true},
			want: debuggerPlumbing{sshSocket: true},
		},
		{
			name:            "RUN with -i",
			debuggerEnabled: true,
			want:            all,
		},
		{
			name: "RUN --interactive",
			opts: ConvertRunOpts{Interactive: true},
			want: all,
		},
		{
			name: "RUN --interactive-keep",
			opts: ConvertRunOpts{InteractiveKeep: true},
			want: all,
		},
		{
			name: "RUN inside TRY with FINALLY SAVE ARTIFACT",
			opts: ConvertRunOpts{
				InteractiveSaveFiles: []debuggercommon.SaveFilesSettings{{Src: "/out.txt", Dst: "out.txt"}},
			},
			want: all,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newDebuggerTestConverter(t, t.TempDir(), tt.debuggerEnabled)

			opts := tt.opts
			opts.CommandName = "RUN"
			opts.Args = []string{"echo", "hi"}
			opts.WithShell = true

			st, err := c.internalRun(t.Context(), opts)
			if err != nil {
				t.Fatalf("internalRun() error = %v", err)
			}

			if opts.Interactive {
				// An ephemeral interactive RUN is recorded as a session rather
				// than applied to the main state.
				st = c.mts.Final.InteractiveSession.State
			}

			if got := execDebuggerPlumbing(t, st); got != tt.want {
				t.Errorf("debugger plumbing = %+v, want %+v", got, tt.want)
			}
		})
	}
}
