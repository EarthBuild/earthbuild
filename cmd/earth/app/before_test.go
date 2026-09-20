package app

import (
	"context"
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

// A command that starts no container should not wait for one to be found, and
// a global flag's value must never be read as that command: scanned,
// `--git-username doc build +all` named doc, and the build then ran against a
// stub frontend. Driven through a real parse, because that is the whole of the
// fix.
func TestNeedsFrontend(t *testing.T) {
	t.Parallel()

	const (
		buildCmd = "build"
		docCmd   = "doc"
		target   = "+all"
	)

	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"ls"}, false},
		{[]string{"ls", "./examples"}, false},
		{[]string{docCmd}, false},
		{[]string{buildCmd, target}, true},
		{[]string{"--git-username", docCmd, buildCmd, target}, true},
		{[]string{target}, true},
		{[]string{"prune"}, true},
		{nil, true},
	} {
		got := true
		noop := func(context.Context, *cli.Command) error { return nil }

		root := &cli.Command{
			Flags: []cli.Flag{&cli.StringFlag{Name: "git-username"}},
			Commands: []*cli.Command{
				{Name: buildCmd, Action: noop},
				{Name: "ls", Action: noop},
				{Name: docCmd, Action: noop},
				{Name: "prune", Action: noop},
			},
			Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
				got = needsFrontend(cmd)

				return ctx, nil
			},
			Action: noop,
		}

		require.NoError(t, root.Run(t.Context(), append([]string{cmdName}, c.args...)))

		if got != c.want {
			t.Errorf("needsFrontend(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}
