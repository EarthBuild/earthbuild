package cli

import (
	"bytes"
	"strings"
	"testing"
)

// A step that must supply its own docker client is told so.
//
// E145 made an unusable host client non-fatal: the socket goes in, the client
// does not, and a step whose image carries `docker-cli` works. **That leaves the
// case it does not carry one**, and what the step then prints is
//
//	/bin/sh: docker: not found
//
// which is the message E117 existed to remove - it sends a reader to look at
// the mount, and the mount is working exactly as designed.
//
// The engine knows at mount time that no client was provided, and the honest
// thing is I11's: degrade, and say so. A build-level note beside the unbounded
// warning, once, naming the remedy that actually works.
func TestABuildWithoutADockerClientSaysSo(t *testing.T) {
	t.Parallel()

	t.Run("silent when a client was provided", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer

		warnNoDockerClient(&b, "")

		if b.Len() != 0 {
			t.Errorf("a build that got a client printed a warning: %q", b.String())
		}
	})

	t.Run("names the cause and the remedy", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer

		warnNoDockerClient(&b, "/usr/bin/docker is dynamically linked")

		out := b.String()

		// The cause, because "no client" leaves a reader guessing between a
		// machine with no docker, one whose client cannot run in the step, and
		// a step image that was expected to carry one.
		if !strings.Contains(out, "dynamically linked") {
			t.Errorf("the warning does not carry the reason: %q", out)
		}

		// And the remedy that works, which is the step's image supplying its
		// own - not "install docker", which is already true here.
		if !strings.Contains(out, "docker-cli") {
			t.Errorf("the warning does not say what to do: %q", out)
		}

		// And what it will otherwise look like, so the message a reader meets
		// next is one they have already been warned about.
		if !strings.Contains(out, "not found") {
			t.Errorf("the warning does not name the failure it predicts: %q", out)
		}
	})

	t.Run("a nil writer is not a crash", func(t *testing.T) {
		t.Parallel()

		warnNoDockerClient(nil, "anything")
	})
}

// The note is reachable from a build.
//
// The half this session keeps finding missing: a value produced by one side and
// never consumed by the other. `warnNoDockerClient` on its own is a function
// nobody calls, which is exactly what the guest's own shutdown message was
// before E123.
func TestTheBuildAsksWhyItHasNoDockerClient(t *testing.T) {
	t.Parallel()

	found, err := nonTestFilesContaining(".", "warnNoDockerClient(")
	if err != nil {
		t.Fatal(err)
	}

	if len(found) < 2 {
		t.Errorf("warnNoDockerClient is defined and not called from a build: %v", found)
	}
}
