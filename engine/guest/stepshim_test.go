package guest

import (
	"reflect"
	"testing"
)

// TestWhatTheStepShimIsAskedToDo.
//
// **The parsing is the part that can be got wrong quietly.** What the shim then
// does - mount, chroot, exec - needs root and a namespace and cannot be
// exercised here, exactly as `prepareShim` cannot. What it was *asked* to do can
// be, and an argv split one place out runs the wrong program in the right
// namespace, or the right program with the root as its first argument.
func TestWhatTheStepShimIsAskedToDo(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		args []string
		want *stepShim
	}{
		{"not asked", []string{"earth-guestd"}, nil},
		{"another shim", []string{"earth-guestd", daemonShimFlag, "/bin/x"}, nil},
		{
			"asked",
			[]string{"earth-guestd", stepShimFlag, "/root", "/w", "/bin/sh", "-c", "x"},
			&stepShim{root: "/root", dir: "/w", argv: []string{"/bin/sh", "-c", "x"}},
		},
		{
			// A step with no WORKDIR: the empty string means "wherever the
			// chroot leaves us", which is the root.
			"no working directory",
			[]string{"earth-guestd", stepShimFlag, "/root", "", "/bin/true"},
			&stepShim{root: "/root", dir: "", argv: []string{"/bin/true"}},
		},
		// Too short to name a command. Refused rather than guessed: a shim that
		// execs the wrong thing does it inside a namespace nobody is watching.
		{"no command", []string{"earth-guestd", stepShimFlag, "/root", "/w"}, nil},
		{"no root", []string{"earth-guestd", stepShimFlag}, nil},
	} {
		got := stepShimAsked(c.args)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// TestTheStepShimArgvIsTheStepsOwn.
//
// The command keeps its own argv[0]. A shim that passed its own name along would
// give the step a different `$0` from the one the Earthfile named, which shells
// and build tools read.
func TestTheStepShimArgvIsTheStepsOwn(t *testing.T) {
	t.Parallel()

	got := stepShimAsked([]string{"guestd", stepShimFlag, "/r", "/w", "/bin/sh", "-lc", "echo"})
	if got == nil {
		t.Fatal("the shim did not recognise its own flag")
	}

	if got.argv[0] != "/bin/sh" {
		t.Errorf("argv[0] is %q, want the step's own command", got.argv[0])
	}
}

// TestTheShimsWorkingDirectoryIsAlwaysInsideTheRoot.
//
// **`chroot` does not change the working directory.** The shim is still standing
// where it started when it chroots, which is outside the root it just entered -
// so a relative working directory resolves against the *old* place, naming a
// directory in the guest rather than in the step, and possibly one outside the
// new root entirely.
//
// The path this replaced applies `filepath.Clean("/" + dir)` for the same
// reason, and `req.Dir` reaches both of them the same way. A rule held in one
// branch and not the other is the shape of E48, where `--dir` was right on one
// side of a copy and wrong on the other for a year.
func TestTheShimsWorkingDirectoryIsAlwaysInsideTheRoot(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ in, want string }{
		{"", "/"},
		{"/", "/"},
		{"/earthly", "/earthly"},
		// The case that motivated this: relative, and so resolved against
		// wherever the shim happened to be standing.
		{"sub", "/sub"},
		{"sub/deeper", "/sub/deeper"},
		{"./sub", "/sub"},
		// Contained rather than escaping, which `Clean` gives for free once the
		// path is rooted.
		{"../etc", "/etc"},
		{"/a/../b", "/b"},
	} {
		if got := stepDir(c.in); got != c.want {
			t.Errorf("stepDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
