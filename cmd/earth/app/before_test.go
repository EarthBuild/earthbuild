package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutoSkipDeprecationWarning(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		localSkipDB  string
		engine       string
		skipBuildkit bool
		noAutoSkip   bool
		wantWarning  bool
	}{
		{
			name:        "no auto-skip flags set",
			engine:      "buildkit",
			wantWarning: false,
		},
		{
			name:         "--auto-skip set",
			engine:       "buildkit",
			skipBuildkit: true,
			wantWarning:  true,
		},
		{
			name:        "--no-auto-skip set",
			engine:      "buildkit",
			noAutoSkip:  true,
			wantWarning: true,
		},
		{
			name:        "--auto-skip-db-path set",
			engine:      "buildkit",
			localSkipDB: "/tmp/skip.db",
			wantWarning: true,
		},
		// **The native engine supports these, so it must not call them
		// deprecated.** They are its documented interface for job skipping
		// (docs/native/skipping-a-job.md), and the notice is about the cloud
		// backend that the buildkit path lost. Every run of the recommended
		// path was announcing that the recommended path was going away.
		{
			name:         "--auto-skip set, native engine",
			engine:       "native",
			skipBuildkit: true,
			wantWarning:  false,
		},
		{
			name:        "--auto-skip-db-path set, native engine",
			engine:      "native",
			localSkipDB: "/tmp/skip.db",
			wantWarning: false,
		},
		// Native is the default, so an unnamed engine is native. The timid
		// direction here is the opposite of the frontend detection's: a
		// spurious deprecation notice on the default path is the fault being
		// fixed, and a missing nudge on a buildkit build costs nothing but the
		// nudge.
		{
			name:         "--auto-skip set, engine unnamed",
			skipBuildkit: true,
			wantWarning:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warning := autoSkipDeprecationWarning(
				tc.skipBuildkit, tc.noAutoSkip, tc.localSkipDB, tc.engine)
			if tc.wantWarning {
				require.Contains(t, warning, "Deprecation:")
				require.Contains(t, warning, "discussions/707")
				// And it says where they still work, so the reader is told
				// what to do rather than only what is going away.
				require.Contains(t, warning, "native")
			} else {
				require.Empty(t, warning)
			}
		})
	}
}

// The engine a build will use, decided before the build subcommand's flags are
// parsed - which is where the deprecation notice is emitted from.
func TestEngineChosen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, env, want string
		args            []string
	}{
		{name: "nothing named", want: "native"},
		{name: "flag, joined", args: []string{"--engine=buildkit", "+x"}, want: "buildkit"},
		{name: "flag, separate", args: []string{"--engine", "buildkit"}, want: "buildkit"},
		{name: "environment", env: "buildkit", want: "buildkit"},
		{
			name: "the command line beats the environment",
			args: []string{"--engine=native"}, env: "buildkit", want: "native",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, engineChosen(tc.args, tc.env))
		})
	}
}
