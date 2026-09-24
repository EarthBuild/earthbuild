package guest

import (
	"strings"
	"testing"
)

// The shim's argv is an argv, not a command line.
//
// §5.3: the root and the socket arrive from the host, and `checkDaemon`
// establishes only that they are absolute. Built into a `sh -c` string - which
// is what `unshare -Ur --mount sh -c "mount …; exec dockerd …"` requires, and
// what E364 used by hand - a path containing a quote stops being a path.
//
// So the shim is this binary re-executing itself with the daemon's arguments as
// separate argv entries, and the test is that nothing in the launch is ever a
// single string a shell could reinterpret.
func TestTheShimsArgumentsAreNeverAShellCommand(t *testing.T) {
	t.Parallel()

	nasty := []string{
		"--data-root=/tmp/a b/data",
		`--host=unix:///tmp/"; rm -rf /; "/x.sock`,
	}

	argv := shimArgv("/usr/bin/dockerd", nasty)

	if argv[0] != daemonShimFlag {
		t.Errorf("the shim is not named first: %v", argv)
	}

	for _, s := range argv {
		if strings.Contains(s, " rm -rf ") && !strings.HasPrefix(s, "--host=") {
			t.Errorf("an argument was recombined into something else: %q", s)
		}
	}

	// Each argument survives whole. A join-and-split anywhere in the launch
	// would split the first of these in two and the build would fail on a path
	// nobody wrote.
	for _, want := range append([]string{"/usr/bin/dockerd"}, nasty...) {
		found := false

		for _, got := range argv {
			if got == want {
				found = true
			}
		}

		if !found {
			t.Errorf("%q did not survive the launch intact: %v", want, argv)
		}
	}
}
