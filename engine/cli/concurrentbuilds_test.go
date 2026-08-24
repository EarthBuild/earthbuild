package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// Two builds sharing one store, at the same time.
//
// The common case and the one nothing had ever run: CI building two targets, or
// a developer with two terminals. Everything in the store is designed for it -
// the action cache is insert-only so an existing entry is never rewritten (I9),
// layers are committed under a temporary name and renamed, profiles the same -
// and **no test had ever had two builds in flight at once**.
//
// The two targets share a base, so they race for the same layer and the same
// cache entries rather than politely using different ones. That is the
// interesting case: two builds that touch nothing in common would pass against
// a store with no concurrency control at all.
//
// What is asserted is what a person would notice: both builds succeed, and both
// artifacts are right. A conflict *reported* is not a failure - two writers
// agreeing on a key and disagreeing on the result is exactly what the report
// exists to say - but neither build may produce the wrong bytes.
func TestTwoBuildsShareAStoreAtOnce(t *testing.T) { // not parallel: boots a sandbox
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	store := storeDir(t)
	useStore(t, store)

	// One Earthfile, two targets over a shared base. Setenv is done before any
	// goroutine starts, because the environment is process-wide and a test that
	// changed it under a running build would be testing the harness.
	body := `VERSION 0.8

common:
    FROM alpine:3.22
    RUN /bin/busybox sh -c "echo shared > /shared.txt"

one:
    FROM +common
    RUN /bin/busybox sh -c "cat /shared.txt > /out.txt && echo one >> /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL one.txt

two:
    FROM +common
    RUN /bin/busybox sh -c "cat /shared.txt > /out.txt && echo two >> /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL two.txt
`

	dirs := map[string]string{"one": t.TempDir(), "two": t.TempDir()}

	for _, d := range dirs {
		err := os.WriteFile(filepath.Join(d, testEarthfile), []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		logs = map[string]string{}
		errs = map[string]error{}
	)

	for target, dir := range dirs {
		wg.Go(func() {
			var out bytes.Buffer

			err := cli.Run(context.Background(), cli.Options{
				Dir: dir, Target: target, Out: &out, Platform: testPlatform(),
			})

			mu.Lock()
			logs[target], errs[target] = out.String(), err
			mu.Unlock()
		})
	}

	wg.Wait()

	for target, err := range errs {
		if err != nil {
			t.Fatalf("+%s failed while another build shared its store: %v\n%s",
				target, err, logs[target])
		}
	}

	// Nothing half-written left over. Both builds staged and committed for
	// real - image placement, layer commits, whiteout translations all put a
	// directory beside its destination and rename it in (E142) - so a leak here
	// is a leak on a path that ran, which is what the same check in
	// `TestABuildThatFailedLeavesAUsableStore` cannot say.
	if leaks := staging(t, store); len(leaks) != 0 {
		t.Errorf("two concurrent builds left staging directories behind: %v", leaks)
	}

	// Both artifacts, because a store that served one build the other's layer
	// would produce two builds that both succeeded and one that is wrong -
	// which is the failure mode a shared cache has and an unshared one does not.
	for target, dir := range dirs {
		b, err := os.ReadFile(filepath.Join(dir, target+".txt")) //nolint:gosec // a path this test made
		if err != nil {
			t.Fatalf("+%s produced no artifact: %v\n%s", target, err, logs[target])
		}

		got := strings.TrimSpace(string(b))

		want := "shared\n" + target
		if got != want {
			t.Errorf("+%s produced %q, want %q:"+
				"\n  two builds sharing a store served one of them the other's result",
				target, got, want)
		}
	}
}

// Two builds of the *same* target, at once.
//
// The other concurrent case, and it exercises different machinery. E140's two
// targets raced for a *mount*; two builds of one target race for the same
// **cache entry** and the same **layer**, which is what the store's design is
// explicitly about:
//
//	the action cache is insert-only, so an existing entry is never rewritten (I9)
//	a layer already present is left alone, which is the deduplication property
//
// Both were written for this and neither had ever seen a collision: every test
// that put the same key twice did it sequentially, from one goroutine.
//
// A conflict *reported* would be a defect here rather than information. Two
// writers agreeing on a key and disagreeing on the result is what the conflict
// report exists to say, and two runs of one target over one store must agree -
// if they do not, the step is not reproducible and I1 is the failure, not I9.
func TestTwoBuildsOfOneTargetAtOnce(t *testing.T) { // not parallel: boots a sandbox
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	body := `VERSION 0.8

build:
    FROM alpine:3.22
    RUN /bin/busybox sh -c "echo deterministic > /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`

	// Separate directories, because the two builds export to the same file
	// name and the question is about the store, not about who wrote last.
	dirs := []string{t.TempDir(), t.TempDir()}

	for _, d := range dirs {
		err := os.WriteFile(filepath.Join(d, testEarthfile), []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		logs = make([]string, len(dirs))
		errs = make([]error, len(dirs))
	)

	for i, dir := range dirs {
		wg.Go(func() {
			var out bytes.Buffer

			err := cli.Run(context.Background(), cli.Options{
				Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
			})

			mu.Lock()
			logs[i], errs[i] = out.String(), err
			mu.Unlock()
		})
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("build %d failed while an identical build shared its store: %v\n%s",
				i, err, logs[i])
		}
	}

	var first string

	for i, dir := range dirs {
		b, err := os.ReadFile(filepath.Join(dir, testArtefact)) //nolint:gosec // a path this test made
		if err != nil {
			t.Fatalf("build %d produced no artifact: %v\n%s", i, err, logs[i])
		}

		got := strings.TrimSpace(string(b))
		if got != "deterministic" {
			t.Errorf("build %d produced %q", i, got)
		}

		if i == 0 {
			first = got
		} else if got != first {
			t.Errorf("two builds of one target produced %q and %q", first, got)
		}
	}

	// And no conflict was reported, because there is nothing to disagree
	// about: two runs of one deterministic step produce one layer, and a
	// conflict here would mean the step is not reproducible (I1) rather than
	// that the cache mishandled a race.
	for i, log := range logs {
		if strings.Contains(log, "conflict") {
			t.Errorf("build %d reported a cache conflict against an identical build:"+
				"\n  two writers agreeing on a key and disagreeing on the result is I1,"+
				"\n  not a concurrency defect\n%s", i, log)
		}
	}
}
