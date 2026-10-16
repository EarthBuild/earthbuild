//go:build linux

package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Waiting is for what is late, not for what is absent.
//
// `waitFor` exists because the docker daemon creates its *socket* several
// seconds after the VM boots, and the first build to want one arrives before
// it. That is a real race and waiting is the right answer.
//
// It waited the same ninety seconds for `/usr/local/bin/docker`, which is a
// **binary in an image**. An image either has it or does not; the layers are
// mounted before the step runs and nothing is going to add one. So the engine
// spent a minute and a half discovering something it could have read at the
// first stat, and then printed the diagnosis it already had:
//
//	the sandbox has no /usr/local/bin/docker to give this step, after waiting 1m30s
//	/usr/local/bin does not exist; the nearest directory that does is /usr
//
// The second line is computed from the filesystem *after* the wait, and it is
// the same answer the filesystem would have given immediately. Across the
// corpus that is eleven targets at ninety seconds each - sixteen minutes of a
// sweep spent waiting for eleven things that were never coming.
//
// The rule is one stat: **a daemon creates a socket inside a directory that
// exists; nothing conjures the directory.** Where the containing directory is
// missing, the path is not late, it is absent, and the honest refusal is
// available now.
//
// Being wrong about that costs a fast refusal instead of a slow one, with an
// identical message - which is the direction to be wrong in.
func TestWaitingStopsImmediatelyForWhatCannotArrive(t *testing.T) {
	t.Parallel()

	t.Run("a path whose directory is missing does not wait", func(t *testing.T) {
		t.Parallel()

		absent := filepath.Join(t.TempDir(), "usr", "local", "bin", "docker")

		start := time.Now()
		err := waitFor(absent)
		took := time.Since(start)

		if err == nil {
			t.Fatal("a path that does not exist was reported as present")
		}

		if took > 5*time.Second {
			t.Errorf("waited %s for a binary in a directory that does not exist", took)
		}

		// The diagnosis must not be degraded by arriving sooner: it is the
		// whole value of the failure, and E28 was five failures nobody could
		// attribute because they shared one sentence.
		for _, want := range []string{"WITH DOCKER", testMissingWord} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the quick refusal lost %q from its diagnosis:\n%s", want, err)
			}
		}
	})

	// The behaviour the wait exists for, unchanged. A socket appears inside a
	// directory that is already there, which is exactly the case that must
	// still be given time.
	t.Run("a path in a directory that exists is waited for", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		sock := filepath.Join(dir, "docker.sock")

		go func() {
			time.Sleep(200 * time.Millisecond)

			_ = os.WriteFile(sock, nil, 0o600)
		}()

		start := time.Now()

		err := waitFor(sock)
		if err != nil {
			t.Fatalf("a socket that arrived late was refused: %v", err)
		}

		if took := time.Since(start); took < 100*time.Millisecond {
			t.Errorf("returned in %s, before the socket could have appeared", took)
		}
	})

	t.Run("a path already there returns at once", func(t *testing.T) {
		t.Parallel()

		p := filepath.Join(t.TempDir(), "docker.sock")

		err := os.WriteFile(p, nil, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		start := time.Now()

		err = waitFor(p)
		if err != nil {
			t.Fatal(err)
		}

		if took := time.Since(start); took > time.Second {
			t.Errorf("took %s for a path that was already there", took)
		}
	})
}
