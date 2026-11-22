package cli_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A WITH DOCKER block works in a target that also has a condition.
//
// The two interact through the sandbox image. A condition that cannot be
// decided without running it is answered by a *probe*, which needs a sandbox
// and gets the plain one, because at that point nobody has read the plan and
// nobody knows a daemon will be wanted. The plan is known moments later, and
// the engine declined to switch:
//
//	if needsDocker(plan) && !g.wasUsed() { g.image = sandboxImage(true) }
//
// The reasoning was that switching "would discard a layer store this build has
// already written to". It would not: both sandboxes take `sb.Store =
// storeDir()`, the same host directory, shared into whichever VM is running.
// Nothing is discarded by changing machines, because the layers never lived in
// the machine.
//
// So a target with an IF and a WITH DOCKER ran its docker steps in a VM with no
// docker in it, and waited ninety seconds for a binary that was never going to
// arrive. Five corpus targets, and the diagnosis that found it says
// `/usr/local/bin exists and holds nothing` - the directory is alpine's, and
// empty.
func TestADockerBlockWorksAfterACondition(t *testing.T) { // not parallel: boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	if !sandboxHostsDocker() {
		t.Skip("this backend has no sandbox image carrying a docker daemon")
	}

	sh := testShell

	// The IF must be one the interpreter cannot decide: it turns on a file an
	// earlier step wrote, so answering it means running the prefix, which means
	// a sandbox. That probe is what used to fix the plain image in place.
	dir := project(t, `VERSION 0.8

app:
    FROM alpine:3.22
    RUN `+sh+` -c "echo served > /hi.txt"
    ENTRYPOINT ["/bin/busybox", "sh", "-c", "cat /hi.txt"]
    SAVE IMAGE switch-probe:latest

check:
    FROM alpine:3.22
    RUN `+sh+` -c "echo marker > /flag"
    IF [ -f /flag ]
        RUN `+sh+` -c "echo conditional > /out.txt"
    END
    WITH DOCKER --load switch-probe:latest=+app
        RUN docker run switch-probe:latest > /ran.txt
    END
    SAVE ARTIFACT /ran.txt AS LOCAL ran.txt
`, nil)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "check", Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		if strings.Contains(err.Error(), "no /usr/local/bin/docker") {
			t.Fatalf("the docker steps ran in the sandbox the probe started:\n%v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}
}
