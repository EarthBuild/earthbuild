package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// TestCopyingADirectoryMergesIntoTheOneThere.
//
// **`COPY --dir` merges; it does not replace.** A directory already at the
// destination keeps whatever the copied one does not also carry - that is
// overlayfs's rule for directories and it is what the reference engine does.
//
// This engine replaced it, but only in one shape: when the copied directory
// exists in a layer the destination *also* stands on. `+code` gets `/earthly`
// from `+go`'s WORKDIR and writes into it, so its delta holds a copy-up rather
// than a creation; a destination built on the same base then lost everything it
// had put there. Where each target makes the directory itself, the merge was
// correct, which is why this went unnoticed.
//
// Found by running this repository's own CI line, `earth --ci +lint`, which
// fails with `open /earthly/.golangci.yaml: no such file or directory` - the
// config is copied in and then destroyed by the COPY after it. The reference
// engine gets past that point and fails on lint findings instead.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestCopyingADirectoryMergesIntoTheOneThere(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	sh := testShell

	dir := project(t, `VERSION 0.8

shared:
    FROM alpine:3.22
    WORKDIR /work

producer:
    FROM +shared
    COPY --dir theirs ./
    SAVE ARTIFACT /work

taker:
    FROM +shared
    RUN `+sh+` -c "echo from-taker > /work/mine.txt"
    COPY --dir +producer/work /
    RUN `+sh+` -c "cat /work/mine.txt /work/theirs/theirs.txt > /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, map[string]string{"theirs/theirs.txt": "from-producer\n"})

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "taker", Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("the copy destroyed what the destination already held: %v\n%s",
			err, out.String())
	}

	got, err := os.ReadFile(filepath.Join(dir, testArtefact))
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != "from-taker\nfrom-producer\n" {
		t.Errorf("merged view is %q, want both files"+
			"\n  COPY --dir must not remove what the destination directory already had",
			string(got))
	}
}
