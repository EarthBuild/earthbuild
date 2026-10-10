package exec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A sandbox directory left by a build that was killed is cleared, and one a
// live build is using is not.
//
// **The host-side twin of the guest's half-written layers.** A sandbox keeps
// its sockets, its VM config and a sparse export device in a temporary
// directory, and removes the lot on a clean stop - every ordinary path is
// tidy. A build killed with SIGKILL runs no code at all, so its directory
// stays for ever, and nothing ever swept them: two were found holding 65G of
// sparse export device between them.
//
// Told apart by a lock rather than by age, because age cannot distinguish a
// long build from an abandoned one - and the answer has to be certain in the
// direction that matters: deleting the directory of a *running* build takes its
// vsock socket out from under it.
//
// `flock` is the same mechanism claimStore uses, and for the same reason: the
// kernel drops it when the process ends, however it ended, so an abandoned
// sandbox cannot hold its claim the way a pid file would.
func TestAnAbandonedSandboxIsClearedAndALiveOneIsNot(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	// One whose owner has gone: the lock file is there, nobody holds it.
	dead := filepath.Join(tmp, sandboxPrefix+"dead")
	mustDir(t, dead)
	mustFile(t, filepath.Join(dead, sandboxLock))

	// One a build is using, with the lock held as a running sandbox holds it.
	live := filepath.Join(tmp, sandboxPrefix+"live")
	mustDir(t, live)

	release, err := holdSandbox(live)
	if err != nil {
		t.Fatal(err)
	}

	defer release()

	// Something that is not ours at all.
	other := filepath.Join(tmp, "not-a-sandbox")
	mustDir(t, other)

	aged(t, dead, live, other)

	swept := sweepSandboxes(tmp)

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Error("an abandoned sandbox survived, so its export device is leaked for good")
	}

	if _, err := os.Stat(live); err != nil {
		t.Errorf("a sandbox a build is using was removed: %v", err)
	}

	if _, err := os.Stat(other); err != nil {
		t.Errorf("a directory that is not ours was removed: %v", err)
	}

	if swept != 1 {
		t.Errorf("swept %d sandboxes, wanted 1", swept)
	}
}

// A sandbox still being set up is left alone.
//
// The lock is taken just after the directory is made, so there is an instant
// where a live sandbox has no lock file. Sweeping on that would delete a
// directory a build is in the middle of creating, which is the one mistake this
// must not make.
func TestASandboxBeingSetUpIsLeftAlone(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	fresh := filepath.Join(tmp, sandboxPrefix+"fresh")
	mustDir(t, fresh)

	if swept := sweepSandboxes(tmp); swept != 0 {
		t.Errorf("swept %d sandboxes; a directory made moments ago is not abandoned", swept)
	}

	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a sandbox still being set up was removed: %v", err)
	}
}

func mustDir(t *testing.T, at string) {
	t.Helper()

	if err := os.MkdirAll(at, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustFile(t *testing.T, at string) {
	t.Helper()

	f, err := os.Create(at)
	if err != nil {
		t.Fatal(err)
	}

	_ = f.Close()
}

// aged makes directories old enough to be considered, so the grace period for
// one being set up does not decide the test.
func aged(t *testing.T, dirs ...string) {
	t.Helper()

	old := time.Now().Add(-time.Hour)

	for _, d := range dirs {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}
}
