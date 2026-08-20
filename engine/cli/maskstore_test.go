package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// A mask stops naming a stale image across real saves and loads.
//
// `TestIdleCountsSurviveTheProcess` proves the counts round-trip through the
// snapshot API. This proves they round-trip through the *file*, which is a
// separate claim: the store's format is JSON with named fields, and a field
// nobody added to `history` is a count that resets every build. The ratchet
// would then never release while every test in `engine/core` passed.
//
// That is the shape of E111 - a policy applied at one of the two places it has
// to be - so it is asserted at the boundary rather than inferred from the one
// above it.
func TestAMaskForgetsAStaleImageAcrossBuilds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	const site = "./Earthfile:12 IF command -v node"

	first := core.NewPredictions()
	first.Needed(site, true, []string{"node:18", testBaseImage})

	err := savePredictions(dir, first)
	if err != nil {
		t.Fatal(err)
	}

	// Each iteration is a whole build: load what is on disk, record what this
	// build wanted, write it back.
	for range core.MaskIdleLimit {
		p, err := loadPredictions(dir)
		if err != nil {
			t.Fatal(err)
		}

		p.Needed(site, true, []string{"node:22", testBaseImage})

		err = savePredictions(dir, p)
		if err != nil {
			t.Fatal(err)
		}
	}

	final, err := loadPredictions(dir)
	if err != nil {
		t.Fatal(err)
	}

	got := final.Needs(site, true)

	if slices.Contains(got, "node:18") {
		t.Errorf("after %d builds that did not want it, the saved mask still"+
			" names node:18: %v", core.MaskIdleLimit, got)
	}

	if !slices.Contains(got, "node:22") || !slices.Contains(got, testBaseImage) {
		t.Errorf("the mask lost something it wants every build: %v", got)
	}
}

// A store written before the counts existed loads, and drops nothing at once.
//
// The field is `omitempty` and absent from every history file already on a
// developer's machine. Decoding it as "no counts" means those entries start
// from zero idle consultations - so an upgrade costs at most a few builds of
// stale prefetch, rather than discarding every mask the moment the engine is
// updated.
func TestAHistoryWithoutIdleCountsStillLoads(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// The needs key is `site NUL branch`, written by needKey. Spelled with an
	// escape rather than a raw byte so this fixture is readable, and matched
	// against the real encoder below rather than trusted.
	body := "{\"taken\":{\"s\":[1,2]},\"needs\":{\"s\\u0000true\":[\"alpine:3.22\"]}}"

	err := os.WriteFile(filepath.Join(dir, historyFile), []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	p, err := loadPredictions(dir)
	if err != nil {
		t.Fatalf("a history from before the counts existed did not load: %v", err)
	}

	if got := p.Needs("s", true); len(got) != 1 {
		t.Errorf("the mask did not survive the upgrade: %v", got)
	}

	// The fixture's key is hand-written, so it is checked against the encoder
	// rather than assumed. A fixture whose key does not match writes a mask
	// nothing reads, and the test above would then be asserting that an
	// unreadable file loads - which it does, saying nothing.
	live := core.NewPredictions()
	live.Needed("s", true, []string{testBaseImage})

	for key := range live.NeedsSnapshot() {
		if !strings.Contains(body, strings.ReplaceAll(key, "\x00", `\u0000`)) {
			t.Errorf("the fixture's needs key is not the one needKey writes:"+
				"\n  encoder %q\n  fixture %s", key, body)
		}
	}
}
