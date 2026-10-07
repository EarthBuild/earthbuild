package app

import (
	"context"
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earth/flag"
	"github.com/EarthBuild/earthbuild/internal/env"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

// --buildkit-tls / EARTH_BUILDKIT_TLS must only override the config file when
// actually given, and must be able to turn TLS off (#862).
func TestBuildkitTLSFlag(t *testing.T) {
	for _, tc := range []struct {
		name    string
		envVar  string
		envVal  string
		args    []string
		wantSet bool
		want    bool
	}{
		{name: "unset leaves config alone", wantSet: false},
		{name: "flag disables", args: []string{"--buildkit-tls=false"}, wantSet: true, want: false},
		{name: "flag enables", args: []string{"--buildkit-tls"}, wantSet: true, want: true},
		{name: "env disables", envVar: env.Prefix + "BUILDKIT_TLS", envVal: "false", wantSet: true, want: false},
		{name: "env enables", envVar: env.Prefix + "BUILDKIT_TLS", envVal: "true", wantSet: true, want: true},
		{
			name:   "deprecated env disables",
			envVar: env.DeprecatedPrefix + "BUILDKIT_TLS", envVal: "false",
			wantSet: true, want: false,
		},
		{
			name: "flag beats env", args: []string{"--buildkit-tls=false"},
			envVar: env.Prefix + "BUILDKIT_TLS", envVal: "true",
			wantSet: true, want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Do not use t.Parallel() because t.Setenv modifies process-wide state.
			for _, k := range []string{env.Prefix + "BUILDKIT_TLS", env.DeprecatedPrefix + "BUILDKIT_TLS"} {
				t.Setenv(k, "")
				require.NoError(t, os.Unsetenv(k))
			}

			if tc.envVar != "" {
				t.Setenv(tc.envVar, tc.envVal)
			}

			var (
				global flag.Global
				gotSet bool
			)

			noop := func(context.Context, *cli.Command) error { return nil }
			root := &cli.Command{
				Flags: global.RootFlags("earth", "img"),
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					gotSet = cmd.IsSet(flag.BuildkitTLSFlag)

					return ctx, nil
				},
				Action: noop,
			}

			require.NoError(t, root.Run(t.Context(), append([]string{cmdName}, tc.args...)))
			require.Equal(t, tc.wantSet, gotSet)
			require.Equal(t, tc.want, global.BuildkitTLS)
		})
	}
}

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
// stub engine. Driven through a real parse, because that is the whole of the
// fix.
func TestNeedsEngine(t *testing.T) {
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
				got = needsEngine(cmd)

				return ctx, nil
			},
			Action: noop,
		}

		require.NoError(t, root.Run(t.Context(), append([]string{cmdName}, c.args...)))

		if got != c.want {
			t.Errorf("needsEngine(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}
