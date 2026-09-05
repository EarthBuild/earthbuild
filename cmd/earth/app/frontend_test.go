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
		env  string
		want bool
	}{
		{"a bare target builds, and builds are native by default", []string{"+build"}, "", false},
		{"an explicit native build", []string{"--engine", "native", "+build"}, "", false},
		{"an explicit native build, joined form", []string{"--engine=native", "+build"}, "", false},
		{"native from the environment", []string{"+build"}, "native", false},
		{"the build subcommand named outright", []string{"build", "+build"}, "", false},

		{"buildkit asked for on the command line", []string{"--engine", "buildkit", "+b"}, "", true},
		{"buildkit asked for, joined form", []string{"--engine=buildkit", "+b"}, "", true},
		{"buildkit from the environment", []string{"+build"}, "buildkit", true},
		{"the command line beats the environment", []string{"--engine=buildkit", "+b"}, "native", true},
		{"and the other way round", []string{"--engine=native", "+b"}, "buildkit", false},

		{"another command entirely", []string{"prune"}, "", true},
		{"bootstrap, which is about the daemon", []string{"bootstrap"}, "", true},
		{"no arguments at all", nil, "", true},
		{"an unrecognised engine is not assumed to be native", []string{"--engine=podman", "+b"}, "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := needsContainerFrontend(c.args, commands, c.env); got != c.want {
				t.Errorf("needsContainerFrontend(%q, env=%q) = %v, want %v", c.args, c.env, got, c.want)
			}
		})
	}
}
