package guest

import (
	"strings"
	"testing"
)

// A step that asked for nothing is not asked about.
//
// Almost every step is this one, and a check that refused something here would
// refuse the whole corpus.
func TestNoDaemonAskedForIsNotRefused(t *testing.T) {
	t.Parallel()

	if err := checkDaemon(nil); err != nil {
		t.Errorf("a step wanting no daemon was refused: %v", err)
	}
}

// A daemon asked for and not described is a caller bug, and says which half.
//
// This is why the field is a pointer (E366): "not asked for" and "asked for,
// empty" are different mistakes, and only the second is worth a message. The
// message names the field, because the caller is a program and the person
// reading the error is the one who wrote it.
func TestADaemonAskedForAndNotDescribedIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		d    Daemon
		says string
	}{
		{"no root", Daemon{Socket: "/var/run/docker.sock"}, "root"},
		{"no socket", Daemon{Root: "/var/lib/earthbuild-docker"}, "socket"},
		{"neither", Daemon{}, "root"},
		{"relative root", Daemon{Root: "lib/d", Socket: "/s"}, "absolute"},
		{"relative socket", Daemon{Root: "/lib/d", Socket: "s"}, "absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkDaemon(&tc.d)
			if err == nil {
				t.Fatalf("%+v was accepted", tc.d)
			}

			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal does not say %q: %v", tc.says, err)
			}
		})
	}
}

// A platform that cannot run one refuses, rather than running the body without.
//
// The failure this prevents is the one the version bump prevents from the other
// direction: a guest that accepts the request and quietly does not honour it
// gives the step a socket with nothing behind it, and the author gets a message
// about Docker rather than about this engine. I10 - a refusal is honest, and an
// unhonoured request is not a refusal.
func TestAPlatformThatCannotRunOneSaysSo(t *testing.T) {
	t.Parallel()

	good := &Daemon{Root: "/var/lib/earthbuild-docker", Socket: "/var/run/docker.sock"}
	err := checkDaemon(good)

	switch why := cannotRunDaemon(); why {
	case "":
		if err != nil {
			t.Errorf("a platform that can run a daemon refused one: %v", err)
		}

	default:
		if err == nil {
			t.Fatalf("this platform cannot run a daemon (%s) and accepted one anyway", why)
		}

		if !strings.Contains(err.Error(), why) {
			t.Errorf("the refusal does not carry the reason %q: %v", why, err)
		}
	}
}
