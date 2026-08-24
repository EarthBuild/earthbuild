package cli_test

import (
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// buildNativeCLI builds the front end as a binary, so a test can kill it.
//
// A subprocess and not a goroutine, because the property under test is what
// SIGKILL leaves behind and a cancelled context is the opposite: it is the
// graceful path, which already has its own tests. Nothing in-process can
// simulate a build that stops between two instructions.
func buildNativeCLI(t *testing.T) string {
	t.Helper()

	out := filepath.Join(t.TempDir(), "earth-native")

	build := osexec.Command("go", testTarget, "-o", out,
		"github.com/EarthBuild/earthbuild/cmd/earth-native")

	msg, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build earth-native: %v: %s", err, msg)
	}

	return out
}

// layerCount is how many layers the store holds.
func layerCount(t *testing.T, store string) int {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(store, "layers"))
	if err != nil {
		return 0
	}

	n := 0

	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}

	return n
}

// A build killed mid-flight leaves a store the next build can use.
//
// Test-plan **c4**, the half that is tractable today. Green paper I9 says state
// is inserted or removed and never modified, and §5.1 records that E76 covers
// the in-process half - a rewrite is refused, a temporary file is linked into
// place rather than renamed over. What none of that establishes is what happens
// when the process stops *between* two of those steps, which is the case the
// invariant exists for: a consumer holding a digest must find the expected
// bytes or nothing.
//
// SIGKILL rather than SIGTERM or a cancelled context. Both of those are the
// graceful path and both already have tests; the interesting state is the one
// no cleanup handler got to tidy.
//
// It asserts recovery rather than a clean store, and the difference matters:
// a temporary file left behind is *allowed*, because a crashed build cannot be
// expected to have removed it and the store's own rules make it harmless. What
// is not allowed is a layer directory or a cache entry that a later build reads
// as real and is not.
func TestABuildKilledMidFlightLeavesAUsableStore(t *testing.T) { //nolint:paralleltest // boots a VM
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	engine := buildNativeCLI(t)
	guest := buildGuestd(t)
	store := storeDir(t)

	dir := t.TempDir()

	// Several steps, each slow enough that the kill lands inside one rather
	// than between builds. A single-step build would be killed either before
	// anything was committed or after everything was, and neither is the case
	// this is about.
	err := os.WriteFile(filepath.Join(dir, testEarthfile), []byte(`VERSION 0.8

probe:
    FROM alpine:3.22
    RUN echo one > /a.txt && sleep 1
    RUN echo two > /b.txt && sleep 1
    RUN echo three > /c.txt && sleep 1
    SAVE ARTIFACT /c.txt AS LOCAL c.txt
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	run := func() *osexec.Cmd {
		c := osexec.Command(engine, "+probe")
		c.Dir = dir
		c.Env = append(os.Environ(),
			"EARTH_CACHE_DIR="+store,
			"EARTH_GUESTD="+guest,
			"EARTH_IMAGE_CACHE_DIR="+sharedImages(t))

		return c
	}

	first := run()

	err = first.Start()
	if err != nil {
		t.Fatal(err)
	}

	// Killed once the store has something in it, so the crash is genuinely
	// mid-build. Waiting for a fixed duration instead would make the test a
	// measurement of this machine's speed.
	deadline := time.Now().Add(3 * time.Minute)

	for layerCount(t, store) < 2 {
		if time.Now().After(deadline) {
			_ = first.Process.Kill()
			t.Skip("the build did not commit two layers in time; nothing to crash into")
		}

		time.Sleep(100 * time.Millisecond)
	}

	err = first.Process.Kill()
	if err != nil {
		t.Fatal(err)
	}

	_ = first.Wait()

	// Every layer the store claims to have is a directory that is really there.
	// A partially written one would be a digest naming bytes that are not the
	// bytes - I2 and I9 at once, and the failure a content-addressed store
	// cannot survive.
	entries, err := os.ReadDir(filepath.Join(store, "layers"))
	if err != nil {
		t.Fatalf("the store has no layers directory after the crash: %v", err)
	}

	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasSuffix(name, ".tmp") || strings.HasPrefix(name, ".") {
			// Allowed: a crashed build cannot tidy up, and a name the store
			// does not read as a layer costs disk and nothing else. The
			// `.config.json` files that sit beside a layer are not layers.
			continue
		}

		// Readable, which is weaker than it looks and is deliberately all this
		// claims. The obvious stronger check - re-digest the directory and
		// require its own name back - **fails on a store that never crashed**:
		// measured on three layers of a clean build, neither the with-times
		// digest nor the without-times one comes back equal to the name it is
		// filed under. Whatever the capture digests is not what a later walk of
		// the stored directory digests.
		//
		// So a layer's identity cannot be re-verified from the store, and an
		// assertion that it can would have reported a defect that is not there.
		// The cause is not established and is filed rather than guessed at; the
		// consequence for this test is that the recovery below is the real
		// check, and this loop only catches a directory that cannot be walked
		// at all.
		_, err := layer.Take(filepath.Join(store, "layers", name))
		if err != nil {
			t.Errorf("the store lists a layer it cannot read: %s: %v", name, err)
		}
	}

	// And the build finishes when it is run again. This is the property that
	// matters to a person: a crash costs the work in flight and nothing else.
	second := run()

	out, err := second.CombinedOutput()
	if err != nil {
		t.Fatalf("the build did not recover from a crash: %v\n%s", err, out)
	}

	body, err := os.ReadFile(filepath.Join(dir, "c.txt")) //nolint:gosec // a path this test made
	if err != nil {
		t.Fatalf("the recovered build produced no artifact: %v\n%s", err, out)
	}

	if strings.TrimSpace(string(body)) != "three" {
		t.Errorf("the recovered build produced %q", body)
	}
	// **c4's first clause, in this engine's terms.** The test plan asks that
	// "every blob referenced by a committed manifest exists"; there are no
	// manifests here, and the thing that references a result is a cache entry.
	//
	// A dangling entry is *tolerated* at read time - `Lookup` refuses a claim
	// whose layer is absent, which is what makes a crash survivable - so this
	// is not asserting that the engine would break without it. It pins the
	// **ordering**: the entry is written from `res.Layer`, after the layer is
	// committed, so a crash can leave a layer with no entry (garbage) and never
	// an entry with no layer (a claim pointing at nothing).
	//
	// Reversing those two writes would pass every other test in this file, and
	// would turn every crash into a store full of claims a later build has to
	// disbelieve one at a time.
	for _, k := range cacheEntries(t, store) {
		_, err := os.Stat(filepath.Join(store, "layers", k))
		if err != nil {
			t.Errorf("a cache entry survived the crash naming a layer that did not: %s", k)
		}
	}
}

// cacheEntries is the layer digest each surviving action-cache entry claims.
//
// Read from the store's own files rather than through the cache package,
// because the question is what a *crashed* process left on disk and a reader
// that skipped unreadable entries would answer about the ones that survived
// intact - which is the population that cannot be wrong.
func cacheEntries(t *testing.T, store string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(store, "actions"))
	if err != nil {
		// No action cache is a legitimate outcome of a crash early enough.
		return nil
	}

	var out []string

	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(store, "actions", e.Name())) //nolint:gosec // the engine's own store
		if err != nil {
			continue
		}

		var held struct {
			Layer string `json:"layer"`
		}

		if json.Unmarshal(b, &held) != nil || held.Layer == "" {
			// A half-written entry is not a claim: Get refuses what it cannot
			// parse, so it names no layer and cannot dangle.
			continue
		}

		out = append(out, held.Layer)
	}

	return out
}
