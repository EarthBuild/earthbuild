package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// What a build learned about a condition survives to the next build.
//
// "Past performance" is only past if it outlives the process. Held in memory it
// is a statistic about the build that is already finished, which is the one
// build it cannot help.
func TestPredictionsSurviveTheProcess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	const site = "Earthfile:12 command -v unbuffer"

	learn := core.NewPredictions()
	for range 4 {
		learn.Observe(site, true)
	}

	err := savePredictions(dir, learn)
	if err != nil {
		t.Fatal(err)
	}

	// A different predictor, as the next build would have.
	later, err := loadPredictions(dir)
	if err != nil {
		t.Fatal(err)
	}

	branch, confident := later.Predict(site)
	if !confident {
		t.Fatal("what the last build learned did not survive")
	}

	if !branch {
		t.Error("the surviving prediction is the wrong way round")
	}
}

// A site keyed on where it is written, not on what it stood on.
//
// The probe that answers a condition runs on the filesystem built so far, so
// its identity changes whenever anything before it changes - which is most
// commits. Keyed on that, history would be discarded exactly when a developer
// is iterating, which is when it is worth having.
func TestHistoryIsKeptPerSiteNotPerFilesystem(t *testing.T) {
	t.Parallel()

	p := core.NewPredictions()

	for range 4 {
		p.Observe("Earthfile:12 command -v unbuffer", true)
	}

	if _, confident := p.Predict("Earthfile:12 command -v unbuffer"); !confident {
		t.Error("a site with consistent history is not predicted")
	}

	if _, confident := p.Predict("Earthfile:40 command -v something-else"); confident {
		t.Error("a site with no history of its own was predicted from another's")
	}
}

// Nothing to load is not a failure: the first build on a machine has no history
// and must not be treated as broken.
func TestLoadingNoHistoryIsFine(t *testing.T) {
	t.Parallel()

	p, err := loadPredictions(t.TempDir())
	if err != nil {
		t.Fatalf("a machine with no history reported an error: %v", err)
	}

	if _, confident := p.Predict("Earthfile:1 anything"); confident {
		t.Error("a predictor with no history claimed confidence")
	}
}

// The file is written deterministically, so two machines that learned the same
// thing hold identical bytes.
func TestTheHistoryFileIsDeterministic(t *testing.T) {
	t.Parallel()

	write := func() []byte {
		dir := t.TempDir()

		p := core.NewPredictions()
		for _, s := range []string{"Earthfile:9 c", "Earthfile:3 a", "Earthfile:5 b"} {
			p.Observe(s, true)
			p.Observe(s, false)
		}

		err := savePredictions(dir, p)
		if err != nil {
			t.Fatal(err)
		}

		b, err := os.ReadFile(filepath.Join(dir, "predictions.json")) //nolint:gosec // a fixture this test wrote
		if err != nil {
			t.Fatal(err)
		}

		return b
	}

	if a, b := write(), write(); string(a) != string(b) {
		t.Errorf("two writes of one history differ:\n%s\n%s", a, b)
	}
}
