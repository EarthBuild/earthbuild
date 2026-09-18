package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAutoSkipDeprecationWarning(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		localSkipDB  string
		skipBuildkit bool
		noAutoSkip   bool
		wantWarning  bool
	}{
		{
			name:        "no auto-skip flags set",
			wantWarning: false,
		},
		{
			name:         "--auto-skip set",
			skipBuildkit: true,
			wantWarning:  true,
		},
		{
			name:        "--no-auto-skip set",
			noAutoSkip:  true,
			wantWarning: true,
		},
		{
			name:        "--auto-skip-db-path set",
			localSkipDB: "/tmp/skip.db",
			wantWarning: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warning := autoSkipDeprecationWarning(tc.skipBuildkit, tc.noAutoSkip, tc.localSkipDB)
			if tc.wantWarning {
				require.Contains(t, warning, "Deprecation:")
				require.Contains(t, warning, "discussions/707")
			} else {
				require.Empty(t, warning)
			}
		})
	}
}

// A command that starts no container should not wait for one to be found.
// Anything unrecognised is answered yes, which is what every invocation did
// before the gate existed.
func TestNeedsFrontend(t *testing.T) {
	t.Parallel()

	cmds := []*cli.Command{{Name: "build"}, {Name: "ls"}, {Name: "doc"}, {Name: "prune"}}

	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"ls"}, false},
		{[]string{"ls", "./examples"}, false},
		{[]string{"ls", "--args"}, false},
		{[]string{"doc"}, false},
		{[]string{"build", "+all"}, true},
		{[]string{"+all"}, true},
		{[]string{"prune"}, true},
		{nil, true},
	} {
		if got := needsFrontend(c.args, cmds); got != c.want {
			t.Errorf("needsFrontend(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}
