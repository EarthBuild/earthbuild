package app

import "testing"

// A command that only reads files needs no container runtime.
//
// `ls` parses an Earthfile and prints target names; the probe before it -
// `docker ps`, then an info-style question about rootless mode, user namespaces
// and security options - was 96ms of a 270ms invocation, for a capability it
// never touches.
//
// The decision is made in `before`, where the subcommand's flags are not parsed
// yet, so it reads the raw arguments. Everything it cannot recognise is treated
// as needing a frontend: keeping today's behaviour is the safe error.
func TestWhenAContainerFrontendCanBeSkipped(t *testing.T) {
	t.Parallel()

	commands := []string{"build", "bootstrap", "prune", "ls", "doc", "account"}

	for _, c := range []struct {
		name string
		args []string
		want bool
	}{
		{"ls reads a file and nothing else", []string{"ls"}, false},
		{"ls with a path", []string{"ls", "./examples"}, false},
		{"ls with its own flags", []string{"ls", "--args"}, false},
		{"ls long form", []string{"ls", "-l"}, false},

		{"a build still needs one", []string{"build", "+all"}, true},
		{"a bare target still needs one", []string{"+all"}, true},
		{"prune is about the daemon", []string{"prune"}, true},
		{"bootstrap is about the daemon", []string{"bootstrap"}, true},
		{"no arguments at all", nil, true},
		{"something unrecognised", []string{"--wat"}, true},
		// A flag *value* that happens to match a command name must not be read
		// as one - but the safe error here is to detect, which is what an
		// unrecognised line gets anyway.
		{"a target named after nothing", []string{"./ls+build"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := needsContainerFrontend(c.args, commands); got != c.want {
				t.Errorf("needsContainerFrontend(%q) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}
