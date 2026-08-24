package guest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeDaemon struct {
	says    string
	err     error
	stopped int
	asked   int
	stopErr error
}

func (f *fakeDaemon) Ask(context.Context) (string, error) {
	f.asked++

	return f.says, f.err
}

func (f *fakeDaemon) Stop() error { f.stopped++; return f.stopErr }

// The body does not run until the daemon answers.
//
// The whole point of the wait. A body started against a socket with nothing
// behind it fails on its first `docker` command, and the author reads a message
// about Docker rather than about a daemon that was still starting.
func TestTheBodyDoesNotRunUntilTheDaemonAnswers(t *testing.T) {
	t.Parallel()

	var where [2]string
	_ = where

	f := &fakeDaemon{says: "29.4.3 vfs"}
	ran := false

	err := withDaemon(t.Context(), t.TempDir(), &Daemon{Root: "/d", Socket: "/var/run/docker.sock"},
		func(context.Context, []string, string) (daemonProcess, error) { return f, nil },
		published(&where),
		func() error { ran = true; return nil })
	if err != nil {
		t.Fatalf("a daemon that answered still failed the step: %v", err)
	}

	if !ran {
		t.Error("the body did not run")
	}

	if f.asked == 0 {
		t.Error("the daemon was never asked whether it was up")
	}
}

// A daemon that never answers is stopped, and the body does not run.
//
// *A resource acquired and not released on the error path.* The failing wait is
// the natural place to return early, and returning there leaves a `dockerd`
// running against the step's filesystem after the step has been abandoned -
// holding the overlay open while the capture takes a layer of it.
func TestADaemonThatNeverAnswersIsStoppedAnyway(t *testing.T) {
	t.Parallel()

	var where [2]string
	_ = where

	f := &fakeDaemon{says: ""} // exits zero, says nothing: never started
	ran := false

	ctx, done := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer done()

	err := withDaemon(ctx, t.TempDir(), &Daemon{Root: "/d", Socket: "/var/run/docker.sock"},
		func(context.Context, []string, string) (daemonProcess, error) { return f, nil },
		published(&where),
		func() error { ran = true; return nil })

	if err == nil {
		t.Fatal("a step whose daemon never started was reported as fine")
	}

	if ran {
		t.Error("the body ran against a daemon that had not started")
	}

	if f.stopped != 1 {
		t.Errorf("the daemon was stopped %d times, want 1: a failed wait leaks the"+
			" process it started", f.stopped)
	}
}

// The daemon is stopped when the body fails, and the body's failure is what is
// reported.
//
// Two things that must not be confused: a step that failed is the build's news,
// and the daemon's shutdown is housekeeping. Returning the stop's error over the
// body's would replace `exit status 1` with something about a signal.
func TestAFailingBodyStillStopsTheDaemonAndKeepsItsOwnError(t *testing.T) {
	t.Parallel()

	var where [2]string
	_ = where

	f := &fakeDaemon{says: "29.4.3 vfs"}
	boom := errors.New("the step failed")

	err := withDaemon(t.Context(), t.TempDir(), &Daemon{Root: "/d", Socket: "/var/run/docker.sock"},
		func(context.Context, []string, string) (daemonProcess, error) { return f, nil },
		published(&where),
		func() error { return boom })

	if !errors.Is(err, boom) {
		t.Errorf("the body's failure was replaced by housekeeping: %v", err)
	}

	if f.stopped != 1 {
		t.Errorf("the daemon was stopped %d times, want 1", f.stopped)
	}
}

// The directory the daemon listens in exists before it is launched.
//
// `dockerd` does not create the directory it is told to listen in. That
// directory used to be inside the step, where a scratch image has no `/var/run`
// at all; it is now a short path of the guest's own, because a step's root is
// longer than a sockaddr allows (E396). The requirement is unchanged and the
// place it applies to has moved - which is why this test moved with it rather
// than being deleted.
func TestTheSocketsDirectoryIsMadeBeforeTheDaemonStarts(t *testing.T) {
	t.Parallel()

	var where [2]string
	_ = where

	root := t.TempDir()
	f := &fakeDaemon{says: "29.4.3 vfs"}
	there := false

	err := withDaemon(t.Context(), root, &Daemon{Root: "/d", Socket: "/var/run/docker.sock"},
		func(_ context.Context, _ []string, sock string) (daemonProcess, error) {
			_, err := os.Stat(filepath.Dir(sock))
			there = err == nil

			return f, nil
		},
		published(&where),
		func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	if !there {
		t.Error("the daemon was launched before the directory it listens in existed")
	}
}

// A daemon that will not stop is news when nothing else went wrong.
//
// The rule has two halves and only the first is obvious. A failing body outranks
// a failing shutdown, because `exit status 1` is what the author needs and a
// complaint about a signal would bury it. But when the body succeeded, a daemon
// that would not die is the *only* thing that went wrong - and discarding it
// there leaves a process running against a released handle with nobody told.
//
// *The return value that says what was not understood, assigned to `_`*.
func TestADaemonThatWillNotStopIsNewsWhenNothingElseWentWrong(t *testing.T) {
	t.Parallel()

	var where [2]string
	_ = where

	f := &fakeDaemon{says: "29.4.3 vfs", stopErr: errors.New("it would not die")}

	err := withDaemon(t.Context(), t.TempDir(), &Daemon{Root: "/d", Socket: "/var/run/docker.sock"},
		func(context.Context, []string, string) (daemonProcess, error) { return f, nil },
		published(&where),
		func() error { return nil })

	if err == nil {
		t.Fatal("a daemon that would not stop was not reported at all")
	}

	if !strings.Contains(err.Error(), "would not die") {
		t.Errorf("the reason it would not stop was discarded: %v", err)
	}
}

// The daemon is told the guest's paths, not the step's.
//
// It runs beside the step (E368), so `--data-root` and `--host=unix://` have to
// name paths on the guest's own filesystem. The step's names for the same files
// - `/var/lib/earthbuild-docker`, `/var/run/docker.sock` - are what the step's
// client uses, and handing them to a daemon that is not chrooted points it at
// the *guest's* `/var/run`, where it would either fail on permissions or, worse,
// succeed and serve every step at once from one storage area on the host.
//
// The two path sets are the same files under different names, which is exactly
// the condition under which a mix-up is invisible in review.
func TestTheDaemonIsToldTheGuestsPathsNotTheSteps(t *testing.T) {
	t.Parallel()

	var where [2]string
	_ = where

	root := t.TempDir()

	var argv []string

	err := withDaemon(t.Context(), root, &Daemon{Root: "/d", Socket: "/var/run/docker.sock"},
		func(_ context.Context, a []string, _ string) (daemonProcess, error) {
			argv = a

			return &fakeDaemon{says: "29.4.3 vfs"}, nil
		},
		published(&where),
		func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	// The storage, at the guest's path for it.
	if want := "--data-root=" + filepath.Join(root, "d", "data"); !slices.Contains(argv, want) {
		t.Errorf("the daemon was not told %s:\n  %s", want, strings.Join(argv, "\n  "))
	}

	// And the socket is *not* the step's path, which is the other half of the
	// same rule and now has a second reason: a step's root is longer than a
	// sockaddr allows, so the daemon listens elsewhere and the socket is bound
	// in afterwards (E396).
	for _, a := range argv {
		if strings.HasPrefix(a, "--host=") && strings.Contains(a, root) {
			t.Errorf("the daemon listens inside the step, where the path is too"+
				" long for the kernel to bind: %s", a)
		}
	}
}

// published is a stand-in for the bind, recording what would have appeared where.
//
// A fake rather than the real mount, because the unit tests run on a machine
// that cannot bind and the ordering is what they are about: the real bind is
// exercised end to end (E386, E396).
func published(seen *[2]string) publishDaemon {
	return func(from, to string) (func(), error) {
		*seen = [2]string{from, to}

		return func() {}, nil
	}
}

// The socket is published where the image's symlink leads, not where the step
// named.
//
// `socketTargetIn` has tests of its own and they were not enough: the mutation
// sweep replaced the call with the unresolved path and nothing failed, because
// every test here used a root with no symlink in it. *A resolver that is tested
// and not called is a resolver that is not running* - this project's most
// recorded failure, and the reason the fake publisher records where it was asked
// to bind.
func TestTheSocketIsPublishedWhereTheImagesSymlinkLeads(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, dir := range []string{"run", "var"} {
		err := os.MkdirAll(filepath.Join(root, dir), 0o750)
		if err != nil {
			t.Fatal(err)
		}
	}

	// What an Alpine-derived image ships, which is every image a WITH DOCKER
	// step is likely to use.
	err := os.Symlink("../run", filepath.Join(root, "var", "run"))
	if err != nil {
		t.Fatal(err)
	}

	var where [2]string

	err = withDaemon(t.Context(), root, &Daemon{Root: "/d", Socket: "/var/run/docker.sock"},
		func(context.Context, []string, string) (daemonProcess, error) {
			return &fakeDaemon{says: "29.4.3 vfs"}, nil
		},
		published(&where),
		func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(root, "run", "docker.sock"); where[1] != want {
		t.Errorf("the socket was bound at %s, and the step looks in %s",
			where[1], want)
	}
}
