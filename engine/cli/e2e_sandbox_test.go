package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/image"
)

// TestTheSameConstructsRunInASandbox runs the *same* cases as the host suite,
// through the VM backend.
//
// It is a differential test, and that is the whole value: a construct that
// behaves differently in a sandbox than on this machine is a bug in one of them,
// and neither suite alone can tell you that. The host suite is fast and runs
// everywhere; this one is slow and proves the fast one is measuring the right
// thing.
// Sequential on purpose, and so is every other test that boots a sandbox.
//
// Each one is an 8 GiB virtual machine. Running them at once does not use the
// laptop harder, it oversubscribes it - and the engine already has an
// intermittent `fork/exec ... operation not permitted` whose leading remaining
// hypothesis is exactly the pressure of several machines at once (E54). Making
// the tests concurrent to satisfy a linter would be tuning the experiment to
// produce the failure it is trying to explain.
//
// The tests that do *not* boot a VM are parallel, which is where the time was
// (E58): the interpreter's suite went from 228 seconds to 88.
func TestTheSameConstructsRunInASandbox(t *testing.T) { // not parallel: boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)

	// One cache for the whole suite, not one per case.
	//
	// A cache per case made every case pull alpine again, and nineteen pulls of
	// the same image in half a minute is what an anonymous Docker Hub quota is
	// there to stop: the suite began failing with 429 and the failure read like
	// a build defect. Sharing the cache is also closer to what a developer has -
	// the base image is fetched once and reused, which is the case the engine is
	// built for. Cases still cannot serve each other's results: their Earthfiles
	// differ, so their keys do.
	cache := storeDir(t)

	for _, tc := range cases(t) {
		t.Run(tc.name, func(t *testing.T) {
			// Inside a sandbox the shell is busybox's, and the artifact has to be
			// carried out explicitly. Everything between is the case verbatim.
			body := strings.ReplaceAll(tc.body, "FILE", "/"+tc.file)
			body = fill(body, testShell)

			version := tc.version
			if version == "" {
				version = "VERSION 0.8"
			}

			src := version + "\n\nbuild:\n    FROM alpine:3.22\n" + body +
				"    SAVE ARTIFACT /" + tc.file + " AS LOCAL " + tc.file + "\n" +
				fill(strings.ReplaceAll(functionBlock, "FILE", "/"+tc.file), testShell)

			dir := project(t, src, tc.context)

			t.Setenv("EARTH_GUESTD", guest)
			t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
			useStore(t, cache)

			var out bytes.Buffer

			err := cli.Run(context.Background(), cli.Options{
				Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
			})
			if err != nil {
				// A registry quota is not a defect in this engine, and a suite
				// that reports one as a failure teaches the next reader to
				// discount its failures.
				if strings.Contains(err.Error(), "429") {
					t.Skipf("docker hub rate limit: %v", err)
				}

				t.Fatalf("%v\n%s", err, out.String())
			}

			b, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}

			if got := string(b); got != tc.want {
				t.Errorf("%s contains %q, want %q\n%s", tc.file, got, tc.want, out.String())
			}
		})
	}
}

// sharedImages is one image cache for every test on this machine.
//
// The suite gave each case a fresh cache directory, which is right for layers
// and wrong for images: alpine was fetched again for every case, every run, and
// a day of that earned a 429 from Docker Hub - a rate limit the tests then
// reported as a skip, thinning the coverage they were meant to provide.
//
// Kept outside t.TempDir() deliberately, so it survives between runs. An image
// is content-addressed by reference and platform, so sharing one cannot leak
// state between tests: two cases asking for alpine:3.22 on linux/arm64 want the
// same bytes by definition.
func sharedImages(t *testing.T) string {
	t.Helper()

	// EARTH_TEST_STORE puts it where the machine chose, alongside the store.
	// The two are probed independently and an image is unpacked into whichever
	// it lands in, so a sweep meant to measure a case-sensitive configuration
	// has to move both - moving only the store leaves every image unpacking on
	// the case-insensitive disk, which is the whole failure (E27).
	parent := os.Getenv("EARTH_TEST_STORE")
	if parent == "" {
		parent = os.TempDir()
	}

	dir := filepath.Join(parent, "earthbuild-test-images")

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

// buildGuestd compiles the sandbox agent for these tests.
func buildGuestd(t *testing.T) string {
	t.Helper()

	if p := os.Getenv("EARTH_GUESTD"); p != "" {
		return p
	}

	out := filepath.Join(t.TempDir(), "earth-guestd")

	build := osexec.CommandContext(t.Context(), "go", testTarget, "-o", out,
		"github.com/EarthBuild/earthbuild/cmd/earth-guestd")
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")

	msg, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build earth-guestd: %v: %s", err, msg)
	}

	return out
}

// A condition that can only be answered by running it, answered by running it.
//
// `[ -f /flag ]` after a step that writes /flag is the case the host backend
// cannot do at all: the file does not exist when the plan is made, so nothing
// short of executing the prefix can decide it. Green paper §3.4a, end to end.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestASandboxedConditionIsDecidedByRunningIt(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	cache := storeDir(t)

	for _, tc := range []struct {
		name string
		cond string
		want string
	}{
		{"a file an earlier step wrote", "[ -f /flag ]", "found-it\n"},
		{"a file nothing wrote", "[ -f /never-written ]", "absent\n"},
		{"a command that is installed", "command -v busybox", "found-it\n"},
		{"a command that is not", "command -v definitely-not-installed", "absent\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh := testShell

			dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN `+sh+` -c "echo marker > /flag"
    IF `+tc.cond+`
        RUN `+sh+` -c "echo found-it > /out.txt"
    ELSE
        RUN `+sh+` -c "echo absent > /out.txt"
    END
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, nil)

			t.Setenv("EARTH_GUESTD", guest)
			t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
			useStore(t, cache)

			var out bytes.Buffer

			err := cli.Run(context.Background(), cli.Options{
				Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
			})
			if err != nil {
				if strings.Contains(err.Error(), "429") {
					t.Skipf("docker hub rate limit: %v", err)
				}

				t.Fatalf("%v\n%s", err, out.String())
			}

			b, err := os.ReadFile(filepath.Join(dir, testArtefact))
			if err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}

			if got := string(b); got != tc.want {
				t.Errorf("%s took the wrong branch: %q, want %q\n%s", tc.cond, got, tc.want, out.String())
			}
		})
	}
}

// A loop over command output, end to end.
//
// `FOR d IN $(...)` is the first construct whose *graph shape* comes from
// running something: the number of iterations is not known until a command has
// run in the sandbox. Green paper §3.4a, one step further than a condition.
func TestALoopOverCommandOutputRunsEachItem(t *testing.T) { // not parallel: boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	sh := testShell

	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN `+sh+` -c "mkdir -p items && touch items/alpha items/beta"
    FOR item IN $(/bin/busybox ls items)
        RUN `+sh+` -c "echo $item >> /out.txt"
    END
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, nil)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}

	b, err := os.ReadFile(filepath.Join(dir, testArtefact))
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	if got := string(b); got != "alpha\nbeta\n" {
		t.Errorf("the loop ran as %q, want one iteration per item", got)
	}
}

// `RUN --no-cache` runs the command, not the flag.
//
// Before RUN's options were parsed this became `sh -c "--no-cache echo ..."`,
// a command nobody wrote, which fails saying `--no-cache` is not a program.
// Run twice against one cache: the second must produce the same result, having
// actually run rather than been served an entry that should never have existed.
func TestANoCacheStepRunsEveryTime(t *testing.T) { // not parallel: boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	cache := storeDir(t)
	sh := testShell

	src := `VERSION 0.8

build:
    FROM alpine:3.22
    RUN --no-cache ` + sh + ` -c "echo ran > /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`

	for _, run := range []string{"first", "second"} {
		t.Run(run, func(t *testing.T) {
			dir := project(t, src, nil)

			t.Setenv("EARTH_GUESTD", guest)
			t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
			useStore(t, cache)

			var out bytes.Buffer

			err := cli.Run(context.Background(), cli.Options{
				Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
			})
			if err != nil {
				if strings.Contains(err.Error(), "429") {
					t.Skipf("docker hub rate limit: %v", err)
				}

				t.Fatalf("%v\n%s", err, out.String())
			}

			b, err := os.ReadFile(filepath.Join(dir, testArtefact))
			if err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}

			if got := string(b); got != "ran\n" {
				t.Errorf("out.txt is %q, want the command's output", got)
			}
		})
	}
}

// `SAVE ARTIFACT --if-exists` skips what the build did not produce.
//
// Before the flag was parsed it *became* the artifact's path, so the build
// exported a file called `--if-exists` and treated the real path as the
// destination - the wrong file, in the wrong place, reported as success.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestSaveArtifactIfExistsSkipsWhatIsAbsent(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	sh := testShell

	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN `+sh+` -c "echo made > /present.txt"
    SAVE ARTIFACT --if-exists /absent.txt AS LOCAL absent.txt
    SAVE ARTIFACT /present.txt AS LOCAL present.txt
`, nil)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}

	// The one that exists arrives.
	b, err := os.ReadFile(filepath.Join(dir, "present.txt"))
	if err != nil || string(b) != "made\n" {
		t.Errorf("present.txt is %q (%v), want the file the build made", b, err)
	}

	// The one that does not is simply absent - not an error, and not a file
	// named after the flag.
	for _, name := range []string{"absent.txt", "--if-exists"} {
		_, err := os.Stat(filepath.Join(dir, name))
		if err == nil {
			t.Errorf("%s was written", name)
		}
	}
}

// TRY saves what the failed step produced, and still fails the build.
//
// This is the shape every TRY in this repository has: `RUN test > report &&
// false` followed by `SAVE ARTIFACT report`. It is the one construct whose
// value is entirely in what happens *after* something goes wrong, and a
// simulator cannot vouch for it - whether a failed step's filesystem survives
// to be exported is a question only a real sandbox answers.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestTrySavesTheFailedStepsArtifactAndStillFails(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	sh := testShell

	dir := project(t, `VERSION --try 0.8

build:
    FROM alpine:3.22
    TRY
        RUN `+sh+` -c "echo magic > /report.txt && false"
    FINALLY
        SAVE ARTIFACT /report.txt AS LOCAL report.txt
    END
`, nil)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil && strings.Contains(err.Error(), "429") {
		t.Skipf("docker hub rate limit: %v", err)
	}

	// The build failed, because the step in the TRY failed.
	if err == nil {
		t.Error("a build whose TRY failed reported success")
	}

	// And the report was saved anyway, which is the whole point.
	b, readErr := os.ReadFile(filepath.Join(dir, "report.txt"))
	if readErr != nil {
		t.Fatalf("the artifact from the failed step was not saved: %v\n%s", readErr, out.String())
	}

	if got := string(b); got != "magic\n" {
		t.Errorf("report.txt is %q, want what the failing step wrote", got)
	}
}

// A build that declares an image writes one, and another tool can read it.
//
// The whole path: an Earthfile says SAVE IMAGE, the steps run in a sandbox,
// their layers are packed, and what lands on disk is an OCI layout. Checked
// with skopeo rather than with this engine's own reader, because the layout
// exists to be handed to something else and only something else can say whether
// it is right.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestABuildWritesAnImageAnotherToolCanRead(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	skopeo, lookErr := osexec.LookPath("skopeo")
	if lookErr != nil {
		t.Skip("skopeo is not installed")
	}

	guest := buildGuestd(t)
	cache := storeDir(t)
	sh := testShell

	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN `+sh+` -c "echo built > /app.txt"
    ENTRYPOINT ["/bin/busybox", "cat", "/app.txt"]
    SAVE IMAGE written-by-earthbuild:latest
`, nil)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, cache)

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}

	layout := filepath.Join(cache, "images", "written-by-earthbuild_latest")

	_, err = os.Stat(filepath.Join(layout, "index.json"))
	if err != nil {
		t.Fatalf("no image was written: %v\n%s", err, out.String())
	}

	// The build says where it put it, because an image written somewhere nobody
	// is told about has not really been produced.
	if !strings.Contains(out.String(), layout) {
		t.Errorf("the build did not say where the image went:\n%s", out.String())
	}

	raw, err := osexec.CommandContext(t.Context(), skopeo, "inspect", "--raw",
		"oci:"+layout+":written-by-earthbuild:latest").Output()
	if err != nil {
		t.Fatalf("skopeo refused the image this build wrote: %v", err)
	}

	var manifest ocispec.Manifest
	err = json.Unmarshal(raw, &manifest)
	if err != nil {
		t.Fatal(err)
	}

	// alpine's own layer, plus the step that wrote app.txt.
	if len(manifest.Layers) < 2 {
		t.Errorf("the image has %d layers, want the base and what the build added", len(manifest.Layers))
	}
}

// The image a build wrote starts, and runs what its ENTRYPOINT said.
//
// skopeo reading the layout proves it parses. Whether a container starts from
// it is a different claim: a missing executable bit, a layer stacked in the
// wrong order, a diff id that disagrees with the manifest - each of those
// produces an image that inspects perfectly and will not run. The only way to
// know is to run it.
//
// The image is loaded into the local daemon under a distinctive name and
// removed afterwards, so the test leaves nothing behind.
func TestTheImageABuildWroteActuallyRuns(t *testing.T) { // not parallel: boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	skopeo, err := osexec.LookPath("skopeo")
	if err != nil {
		t.Skip("skopeo is not installed")
	}

	docker, err := osexec.LookPath("docker")
	if err != nil {
		t.Skip("docker is not installed")
	}

	// Named for what it is, so it does not collide with the build's own output
	// buffer further down - which the hoist out of `if` made visible.
	info, err := osexec.CommandContext(t.Context(), docker, "info", "--format", "{{.ServerVersion}}").Output()
	if err != nil {
		t.Skipf("the docker daemon is not running: %v (%s)", err, info)
	}

	const name = "earthbuild-native-engine-selftest:latest"

	guest := buildGuestd(t)
	cache := storeDir(t)
	sh := testShell

	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN `+sh+` -c "echo it-ran > /message"
    ENTRYPOINT ["/bin/busybox", "cat", "/message"]
    SAVE IMAGE `+name+`
`, nil)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, cache)

	var out bytes.Buffer

	err = cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}

	layout := filepath.Join(cache, "images", "earthbuild-native-engine-selftest_latest")

	// Loaded through skopeo, which is how a layout reaches a daemon.
	// --insecure-policy because skopeo wants a signature-trust policy file that
	// a developer machine has no reason to have, and the question here is
	// whether the image runs rather than who signed it.
	if b, commandErr := osexec.Command(skopeo, "copy", "--insecure-policy",
		"oci:"+layout+":"+name, "docker-daemon:"+name).CombinedOutput(); commandErr != nil {
		t.Fatalf("the image would not load: %v\n%s", commandErr, b)
	}

	t.Cleanup(func() { _ = osexec.CommandContext(t.Context(), docker, "rmi", "-f", name).Run() })

	ran, err := osexec.CommandContext(t.Context(), docker, "run", "--rm", name).Output()
	if err != nil {
		t.Fatalf("the image would not run: %v", err)
	}

	if got := strings.TrimSpace(string(ran)); got != "it-ran" {
		t.Errorf("the container printed %q, want what the build wrote and the entrypoint reads", got)
	}
}

// One image, named by two targets, is pulled once.
//
// The layer store is keyed by node identity, so two targets that both begin
// `FROM alpine:3.22` have different identities for the same bytes and were
// fetching them twice. Measured rather than asserted about: the second target
// is timed against the first, and a second pull over the network is not
// something that hides inside a margin.
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestAnImageNamedTwiceIsFetchedOnce(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	cache := storeDir(t)
	images := t.TempDir()
	sh := testShell

	// Two targets, one base image, deliberately different steps so their node
	// identities differ.
	src := `VERSION 0.8

first:
    FROM alpine:3.22
    RUN ` + sh + ` -c "echo one > /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt

second:
    FROM alpine:3.22
    RUN ` + sh + ` -c "echo two > /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`

	run := func(target string) {
		t.Helper()

		dir := project(t, src, nil)

		t.Setenv("EARTH_GUESTD", guest)
		// Its own image cache, not the machine-wide one: this test counts the
		// entries, and every other test in the suite puts things in the shared
		// one. A test that measures a cache cannot share it.
		t.Setenv("EARTH_IMAGE_CACHE_DIR", images)
		useStore(t, cache)

		var out bytes.Buffer

		err := cli.Run(context.Background(), cli.Options{
			Dir: dir, Target: target, Out: &out, Platform: testPlatform(),
		})
		if err != nil {
			if strings.Contains(err.Error(), "429") {
				t.Skipf("docker hub rate limit: %v", err)
			}

			t.Fatalf("%v\n%s", err, out.String())
		}
	}

	run("first")

	// One entry in the shared cache, whatever the node identities were.
	if n := cachedImages(t, images); n != 1 {
		t.Fatalf("the image cache holds %d images after one build, want 1", n)
	}

	run("second")

	// Still one: the second target found the image already local.
	if n := cachedImages(t, images); n != 1 {
		t.Errorf("the image cache holds %d images after two builds of one image", n)
	}
}

// A build learns what its conditions led to, and the next one fetches it early.
//
// The whole loop: a condition that has to be run is run, which way it went is
// recorded against where it is written, what the build needed is attributed to
// it, and a later build with the same history pulls those images before
// interpreting anything. Nothing here changes what is built - the condition is
// still evaluated and still decides (green paper I5) - only when the bytes
// move.
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestABuildLearnsWhatItsConditionsNeed(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	cache := storeDir(t)
	sh := testShell

	src := `VERSION 0.8

build:
    FROM alpine:3.22
    RUN ` + sh + ` -c "echo marker > /flag"
    IF [ -f /flag ]
        RUN ` + sh + ` -c "echo yes > /out.txt"
    ELSE
        RUN ` + sh + ` -c "echo no > /out.txt"
    END
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`

	run := func() {
		t.Helper()

		dir := project(t, src, nil)

		t.Setenv("EARTH_GUESTD", guest)
		t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
		useStore(t, cache)

		var out bytes.Buffer

		err := cli.Run(context.Background(), cli.Options{
			Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
		})
		if err != nil {
			if strings.Contains(err.Error(), "429") {
				t.Skipf("docker hub rate limit: %v", err)
			}

			t.Fatalf("%v\n%s", err, out.String())
		}

		// The condition really was decided by running it.
		b, err := os.ReadFile(filepath.Join(dir, testArtefact))
		if err != nil || string(b) != "yes\n" {
			t.Fatalf("the condition took the wrong branch: %q (%v)", b, err)
		}
	}

	run()

	// What it learned is on disk, naming the line and the image that build
	// needed.
	b, err := os.ReadFile(filepath.Join(cache, "predictions.json"))
	if err != nil {
		t.Fatalf("the build learned nothing: %v", err)
	}

	for _, want := range []string{testLocPrefix, "-f /flag", testBaseImage} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the history does not mention %q:\n%s", want, b)
		}
	}

	// And a second build, which now has a prediction to act on, still gets the
	// same answer: the history changes when bytes move, never what is built.
	run()
	run()
}

// A cache mount survives from one build to the next.
//
// The whole point of CACHE, and the one thing a unit test cannot show: the
// directory is bound into the step's filesystem by the guest, what the step
// writes there goes to the bound source rather than into the layer, and the
// next build sees it. Written by the first build, read by the second.
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestACacheMountOutlivesTheBuild(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	cache := storeDir(t)
	sh := testShell

	// Appends a line to a file in the cache, then reports the whole file. If
	// the mount persists, the second build sees two lines.
	src := `VERSION 0.8

build:
    FROM alpine:3.22
    CACHE /the-cache
    RUN ` + sh + ` -c "echo ran >> /the-cache/log; cp /the-cache/log /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`

	lines := func() int {
		t.Helper()

		dir := project(t, src, nil)

		t.Setenv("EARTH_GUESTD", guest)
		t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
		useStore(t, cache)

		var out bytes.Buffer

		err := cli.Run(context.Background(), cli.Options{
			Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
		})
		if err != nil {
			if strings.Contains(err.Error(), "429") {
				t.Skipf("docker hub rate limit: %v", err)
			}

			t.Fatalf("%v\n%s", err, out.String())
		}

		b, err := os.ReadFile(filepath.Join(dir, testArtefact))
		if err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}

		return len(strings.Fields(string(b)))
	}

	if got := lines(); got != 1 {
		t.Fatalf("the first build saw %d lines in the cache, want 1", got)
	}

	// The second build appends to what the first left: the mount outlived it.
	if got := lines(); got != 2 {
		t.Errorf("the second build saw %d lines, want 2 - the cache did not survive", got)
	}

	// And the cache is not in the image: the artifact is what the step copied,
	// not the mount itself.
	_, err := os.Stat(filepath.Join(cache, "mounts", "the-cache", "log"))
	if err != nil {
		t.Errorf("the cache is not where the engine says it keeps it: %v", err)
	}
}

// A secret is readable by the step and absent from what the step produces.
//
// The whole hazard in one test. A credential written into the step's own
// filesystem would be captured with everything else the step wrote and end up
// in the image - shipped, pushed, and public. Mounting it from outside the
// overlay is what prevents that, and this is the only way to know it worked:
// the step reads the secret, and the layer it produced does not contain it.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestASecretReachesTheStepAndNotTheLayer(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	const secret = "hunter2-must-not-be-in-the-image"

	guest := buildGuestd(t)
	cache := storeDir(t)
	sh := testShell

	// The step proves it can read the secret by measuring it, and deliberately
	// does not copy it: the artifact carries the length, never the value.
	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN --mount=type=secret,id=TOKEN,target=/run/token `+sh+` -c "wc -c < /run/token > /len.txt"
    SAVE ARTIFACT /len.txt AS LOCAL len.txt
`, nil)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, cache)

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
		Secrets: map[string]string{"TOKEN": secret},
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}

	// The step could read it.
	b, err := os.ReadFile(filepath.Join(dir, "len.txt"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	if got := strings.TrimSpace(string(b)); got != strconv.Itoa(len(secret)) {
		t.Errorf("the step read %s bytes, want %d - it did not see the secret", got, len(secret))
	}

	// And it is nowhere in the layer store: not in a captured layer, not in a
	// mount directory, not left behind anywhere this build wrote.
	var found []string

	err = filepath.WalkDir(cache, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is not this test's business
		}

		b, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(b), secret) {
			found = append(found, p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(found) > 0 {
		t.Errorf("the secret is on disk after the build, in %v", found)
	}

	// Nor in what the build printed.
	if strings.Contains(out.String(), secret) {
		t.Error("the secret appears in the build's output")
	}
}

// A secret given as an environment variable reaches the step and not the cache.
//
// The same hazard as a mounted secret, arriving by a different route and with a
// different trap: `Op.Env` is hashed, so a value placed there would be in the
// cache key - written to disk, shared between machines, and impossible to
// retract. The node records the *name*; the value is added at execution.
//
// Not parallel: boots a VM, see e2e_sandbox_test.go.
func TestASecretEnvReachesTheStepAndNotTheCache(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	const secret = "hunter3-env-must-not-persist"

	guest := buildGuestd(t)
	cache := storeDir(t)
	sh := testShell

	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN --secret TOKEN `+sh+` -c "printf %s \"$TOKEN\" | wc -c > /len.txt"
    SAVE ARTIFACT /len.txt AS LOCAL len.txt
`, nil)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, cache)

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
		Secrets: map[string]string{"TOKEN": secret},
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("%v\n%s", err, out.String())
	}

	b, err := os.ReadFile(filepath.Join(dir, "len.txt"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}

	if got := strings.TrimSpace(string(b)); got != strconv.Itoa(len(secret)) {
		t.Errorf("the step saw %s bytes, want %d - it did not get the secret", got, len(secret))
	}

	var found []string

	err = filepath.WalkDir(cache, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is not this test's business
		}

		b, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(b), secret) {
			found = append(found, p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(found) > 0 {
		t.Errorf("the secret is on disk after the build, in %v", found)
	}
}

// A persisted cache is in the image *and* survives to the next build.
//
// Both halves at once, because either alone is a different feature. An ordinary
// CACHE is bound over the step's filesystem and so is invisible to the capture;
// `--persist` asks for the contents to be in the image as well, which is why it
// is copied rather than bound. A test that only checked persistence would pass
// against a plain bind and prove nothing about the flag.
//
// boots a VM, see e2e_sandbox_test.go
func TestAPersistedCacheIsInTheImageAndSurvives(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	cache := storeDir(t)
	sh := testShell

	// Appends a line to the cache, then copies the whole cache out as the
	// artifact. Reading it back from the *step's own filesystem* is what shows
	// the contents were in the layer rather than only in the mount.
	src := `VERSION 0.8

build:
    FROM alpine:3.22
    CACHE --persist /state
    RUN ` + sh + ` -c "echo ran >> /state/log; cp /state/log /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`

	lines := func() int {
		t.Helper()

		dir := project(t, src, nil)

		t.Setenv("EARTH_GUESTD", guest)
		t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
		useStore(t, cache)

		var out bytes.Buffer

		err := cli.Run(context.Background(), cli.Options{
			Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
		})
		if err != nil {
			if strings.Contains(err.Error(), "429") {
				t.Skipf("docker hub rate limit: %v", err)
			}

			t.Fatalf("%v\n%s", err, out.String())
		}

		b, err := os.ReadFile(filepath.Join(dir, testArtefact))
		if err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}

		return len(strings.Fields(string(b)))
	}

	if got := lines(); got != 1 {
		t.Fatalf("the first build saw %d lines, want 1", got)
	}

	// The second build appends to what the first left: the cache survived.
	if got := lines(); got != 2 {
		t.Errorf("the second build saw %d lines, want 2 - the cache did not survive", got)
	}

	// And it is on this machine, where the engine says it keeps caches.
	_, err := os.Stat(filepath.Join(cache, "mounts", "state", "log"))
	if err != nil {
		t.Errorf("the persisted cache is not in the store: %v", err)
	}
}

// useStore points a test at its own build cache, and takes away the sandbox VM
// that goes with it.
//
// The two belong together, which is why this is a helper rather than two lines
// at fourteen call sites. The VM outlives a build on purpose - booting one costs
// 620-700ms and the next build wants the same machine - and it is named after
// the store, so a test with its own store owns its own VM. Left to itself the
// suite ended a run with eleven of them, a gigabyte apiece.
// storeDir makes a build store that will actually be deleted again.
//
// Not `t.TempDir` on its own, whose cleanup is `os.RemoveAll`: a store holds
// unpacked layers with their modes intact - which is not incidental, it is what
// makes a step's filesystem right - and removing a file inside a directory that
// denies writing needs permission on the directory, not on the file.
// `maven:3.8.5-openjdk-17` ships one, so the corpus build test failed its own
// cleanup after building everything it had been asked to.
//
// Deliberately separate from useStore, which is called once per case with a
// store shared by the whole suite: deleting it there would clear the cache
// between cases that are meant to share it.
func storeDir(t *testing.T) string {
	t.Helper()

	// EARTH_TEST_STORE puts the store somewhere the machine chose - in practice
	// a case-sensitive volume, which is the *supported* configuration and the
	// one a corpus sweep should be measuring. Without it a stock Mac measures
	// its own filesystem: 19 of 26 failures in the first full sweep were the
	// disk rather than the engine (E26).
	parent := os.Getenv("EARTH_TEST_STORE")
	if parent == "" {
		parent = t.TempDir()
	}

	// Under `parent`, which is EARTH_TEST_STORE when it is set - the point of
	// that variable is to put the store on a chosen disk, and t.TempDir would
	// ignore it.
	dir, err := os.MkdirTemp(parent, "store-*") //nolint:usetesting // see above
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	// Registered after the TempDir's own cleanup, so it runs before it and
	// leaves nothing for it to trip over.
	t.Cleanup(func() { _ = image.RemoveAll(dir) })

	return dir
}

func useStore(t *testing.T, dir string) {
	t.Helper()

	t.Setenv(testCacheDirEnv, dir)

	// Registered after Setenv, so it runs before it: cleanups are LIFO, and
	// this one needs the variable still pointing at the store whose VM it is
	// removing.
	t.Cleanup(func() { _ = cli.RemoveSandbox() })
}

// cachedImages counts the images in a shared cache.
//
// Directories only. An entry now has a `.config.json` beside it holding what
// the image declared, which is not another image - counting raw directory
// entries made one cached image look like two.
func cachedImages(t *testing.T, root string) int {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(root, "imagecache"))
	if err != nil {
		t.Fatalf("no shared image cache: %v", err)
	}

	var n int

	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}

	return n
}
