package cli_test

import (
	"bytes"
	"context"
	"debug/elf"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// The engine builds the engine.
//
// `TestTheRepositorysOwnTargetsPlan` resolves these targets and stops there,
// which leaves the gap every defect in E49 lived in: a plan that resolves is
// not a build that works. `+earthly` is the target that closes it - it compiles
// the whole tool, and on the way it exercises fourteen directories copied in
// three steps and saved as one artifact, `ARG GOOS=$TARGETOS` derived from a
// built-in, cache mounts, and a stack deep enough to need flattening. Every one
// of those was broken at some point this session, and each broke *here* rather
// than anywhere the corpus could see.
//
// Behind EARTH_TEST_BUILD, like the corpus sweep, because it is minutes rather
// than seconds on a cold store. That makes it a test somebody runs deliberately
// - but it is a *test*, so running it is one command and its verdict is not a
// judgement call, which is the whole difference from the shell loop it replaces.
//
// The assertion is the binary. Not "the build succeeded": `+earthly` writes to
// `build/$GOOS/$GOARCH$VARIANT/earthly`, and the engine reported success while
// putting it in a directory literally named `$TARGETOS` (E49). A build that
// says it worked is not evidence that it did.
func TestTheRepositoryBuildsItself(t *testing.T) { // not parallel: boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_BUILD") == "" {
		t.Skip("set EARTH_TEST_BUILD=1 to build the repository rather than plan it")
	}

	requireSandbox(t)

	// A copy, because `+deps` and `+earthly` write `AS LOCAL` - into go.mod,
	// go.sum and build/. A build tool under test must not edit the tree it is
	// being tested from, and 58,000 lines of build output once came within one
	// `git add -A` of a commit.
	// corpusRoot copies the whole repository and hands back the `examples`
	// directory inside the copy; the repository root is its parent.
	root := filepath.Join(corpusRoot(t), "..")

	// Fatal, not Skip. The copy is made by this test's own helper, so a missing
	// Earthfile is this test being wrong rather than the machine being
	// unsuitable - and the first version skipped here, which turned my own bug
	// into a green run that had built nothing.
	_, err := os.Stat(filepath.Join(root, testEarthfile))
	if err != nil {
		t.Fatalf("the copy holds no Earthfile, so the copy is wrong: %v", err)
	}

	// Whatever is under build/ afterwards was written by *this* build.
	//
	// The working tree has a `build/linux/arm64/earthly` in it - gitignored, 49
	// MB, dated 2020 - and the copy brings it along. The first version of this
	// test asserted that file existed, which it did before the build started:
	// withholding the built-in platform arguments (the E49 defect, which sends
	// the binary to a directory named `$TARGETOS`) did not fail it. A test that
	// cannot fail is worse than no test, and only the mutation check said so.
	err = os.RemoveAll(filepath.Join(root, testTarget))
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	var out bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{
		Dir: root, Target: "earthly", Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("the repository did not build itself: %v\n%s", err, tail(out.String()))
	}

	// Where the Earthfile says, which is two arguments derived from built-ins
	// and one that is declared nowhere.
	//
	// The architecture is this machine's, not a constant. It read `arm64`, which
	// is right on the machine this was written on and wrong on every x86 one -
	// so the test failed on the engine's own Linux box with the binary sitting
	// next to where it looked, and would have failed on any CI runner. The
	// engine had done the correct thing and the assertion had not (E163).
	bin := filepath.Join(root, testTarget, "linux", runtime.GOARCH, "earthly")

	fi, err := os.Stat(bin)
	if err != nil {
		found, _ := filepath.Glob(filepath.Join(root, testTarget, "*", "*", "earthly"))
		t.Fatalf("no binary at %s\n  what was written instead: %v", bin, found)
	}

	if fi.Size() < 1<<20 {
		t.Errorf("the binary is %d bytes, which is not a compiled Go program", fi.Size())
	}

	// And it is a Linux executable for the architecture the path names. A binary
	// in the right place that would not run on the target is the same defect one
	// step later - which is why this is checked at all, and why the machine it
	// checks for has to be the one the build asked for rather than a constant.
	//
	// The second hardcoded `arm64` in this test, and it survived the first fix
	// because the first failure stopped before reaching it. One assertion per
	// run is what a `Fatalf` buys, and it is why the same mistake twice in one
	// function takes two runs to find.
	want, ok := elfMachine[runtime.GOARCH]
	if !ok {
		t.Skipf("no ELF machine recorded for %s, so this cannot check the binary",
			runtime.GOARCH)
	}

	f, err := elf.Open(bin)
	if err != nil {
		t.Fatalf("the binary is not an ELF executable: %v", err)
	}

	defer f.Close()

	if f.Machine != want {
		t.Errorf("the binary is for %v, and the build asked for linux/%s",
			f.Machine, runtime.GOARCH)
	}
}

// tail keeps the end of a long build log, which is where the failure is.
func tail(s string) string {
	const keep = 4000

	if len(s) <= keep {
		return s
	}

	return "..." + s[len(s)-keep:]
}

// elfMachine is the ELF machine each Go architecture compiles to.
//
// Only the two this engine is built and tested on. An architecture with no
// entry skips rather than guessing, because a wrong entry would assert that a
// correct binary is the wrong one.
var elfMachine = map[string]elf.Machine{
	"arm64": elf.EM_AARCH64,
	"amd64": elf.EM_X86_64,
}
