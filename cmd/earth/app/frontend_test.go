package app

import "testing"

// The native engine never uses a Docker or Podman frontend - engine/ does not
// reference one - so detecting a daemon before a native build costs a third of
// a cached build's wall clock and is thrown away (E871).
//
// The decision has to be made in `before`, where the subcommand's flags are not
// parsed yet, so it reads the raw arguments. Everything it cannot recognise is
// treated as needing a frontend: keeping today's behaviour is the safe error.
func TestWhenAContainerFrontendCanBeSkipped(t *testing.T) {
	t.Parallel()

	commands := []string{"build", "bootstrap", "prune", "ls", "doc", "account"}

	for _, c := range []struct {
		name string
		args []string
		// sub is what the parser hands `before` as the subcommand -
		// cmd.Args().First(). Stated per case rather than derived, because
		// deriving it in the test would reimplement the bug it guards against.
		sub  string
		env  string
		want bool
	}{
		{"a bare target builds, and builds are native by default", []string{"+build"}, "", "", false},
		{"an explicit native build", []string{"--engine", "native", "+build"}, "--engine", "", false},
		{"an explicit native build, joined form", []string{"--engine=native", "+build"}, "--engine=native", "", false},
		{"native from the environment", []string{"+build"}, "", "native", false},
		{"the build subcommand named outright", []string{"build", "+build"}, "build", "", false},

		{"buildkit asked for on the command line", []string{"--engine", "buildkit", "+b"}, "--engine", "", true},
		{"buildkit asked for, joined form", []string{"--engine=buildkit", "+b"}, "--engine=buildkit", "", true},
		{"buildkit from the environment", []string{"+build"}, "", "buildkit", true},
		{"the command line beats the environment", []string{"--engine=buildkit", "+b"}, "--engine=buildkit", "native", true},
		{"and the other way round", []string{"--engine=native", "+b"}, "--engine=native", "buildkit", false},

		// **A command that only reads files needs no daemon.** `ls` parses an
		// Earthfile and prints target names; detecting a frontend for it cost
		// 96ms of a 140ms invocation, for a capability it never touches.
		{"ls reads a file and nothing else", []string{"ls"}, "ls", "", false},
		{"ls with a path", []string{"ls", "./examples"}, "ls", "", false},
		{"ls with its own flags", []string{"ls", "--args"}, "ls", "", false},
		// But an engine asked for by name still wins: somebody who says
		// `--engine buildkit` has asked for the daemon, whatever the command.
		{"ls with buildkit asked for outright", []string{"--engine=buildkit", "ls"}, "--engine=buildkit", "", true},

		{"another command entirely", []string{"prune"}, "prune", "", true},
		{"bootstrap, which is about the daemon", []string{"bootstrap"}, "bootstrap", "", true},
		{"no arguments at all", nil, "", "", true},
		{"an unrecognised engine is not assumed to be native", []string{"--engine=podman", "+b"}, "--engine=podman", "", true},

		// **A global flag's value is not a subcommand.** Scanning the raw
		// arguments for anything that matches a command name cannot tell
		// `--git-username ls` - a username that happens to read "ls" - from the
		// `ls` command, and the first match wins. Here the command is `prune`,
		// which is about the daemon, and the scan answers for `ls`, which is
		// not: prune then runs against a stub frontend and quietly prunes
		// nothing. Upstream hit the same shape with `--git-username doc build`.
		{
			"a global flag's value that reads like a file-only command",
			[]string{"--git-username", "ls", "prune"}, "prune", "", true,
		},
		{
			"and the same for build",
			[]string{"--git-username", "build", "bootstrap"}, "bootstrap", "", true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := needsContainerFrontend(c.args, c.sub, commands, c.env); got != c.want {
				t.Errorf("needsContainerFrontend(%q, sub=%q, env=%q) = %v, want %v",
					c.args, c.sub, c.env, got, c.want)
			}
		})
	}
}
