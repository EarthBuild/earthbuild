package cli

import (
	"path/filepath"
	"testing"
)

func askFor(t *testing.T, root string, s skipRecordStore, src string) (bool, error) {
	t.Helper()

	skip, _, _, err := wouldSkip(shapeInput{
		Source: []byte(src), Target: "build", Platform: "linux/arm64",
	}, root, s)

	return skip, err
}

// Nothing recorded is not a reason to skip, and is not an error: it is every
// first build, and every build on a runner whose cache was empty.
func TestWithNoRecordNothingIsSkipped(t *testing.T) {
	t.Parallel()

	skip, err := askFor(t, t.TempDir(), skipRecordStore{at: filepath.Join(t.TempDir(), "r")}, shapeSrc)
	if err != nil || skip {
		t.Errorf("with no record: skip=%t err=%v", skip, err)
	}
}

// **A shape that cannot be computed is a build, not a failure.** An unpinned
// reference, an Earthfile that reaches another one: each falls back to running,
// and none of them stops the build.
func TestAShapeThatCannotBeHadIsNotAnError(t *testing.T) {
	t.Parallel()

	s := skipRecordStore{at: filepath.Join(t.TempDir(), "r")}

	skip, err := askFor(t, t.TempDir(), s, "VERSION 0.8\n\nbuild:\n    FROM alpine:3.22\n")
	if err != nil || skip {
		t.Errorf("an unpinned reference: skip=%t err=%v", skip, err)
	}
}

// A record that holds is a build that need not run.
func TestARecordThatHoldsSkipsTheBuild(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src.txt": "one"})
	s := skipRecordStore{at: filepath.Join(t.TempDir(), "r")}

	in := shapeInput{Source: []byte(shapeSrc), Target: "build", Platform: "linux/arm64"}

	shape, err := shapeOf(in)
	if err != nil {
		t.Fatal(err)
	}

	inputs, err := hostInputsFrom(map[string]bool{contextLayer: true},
		placedAt(), read("/w/src/read.txt"), root)
	if err != nil {
		t.Fatal(err)
	}

	s.put(skipRecord{
		Version: skipRecordVersion, Target: "build", Platform: "linux/arm64",
		Shape: shape.String(), Inputs: inputs, Key: jobKey(shape, inputs),
	})

	skip, err := askFor(t, root, s, shapeSrc)
	if err != nil || !skip {
		t.Errorf("an unchanged build: skip=%t err=%v", skip, err)
	}

	// An edited Earthfile is not a different *shape* - the shape is the
	// invocation - it is a changed input, which the record carries and
	// `TestAnEarthfilesDigestIsWhatItMeans` covers. What must still hold here is
	// that a different invocation is a different question.
	skip, _, _, err = wouldSkip(shapeInput{
		Source: []byte(shapeSrc), Target: "build", Platform: "linux/amd64",
	}, root, s)
	if err != nil || skip {
		t.Errorf("another platform: skip=%t err=%v", skip, err)
	}
}

// A record about another target does not answer for this one.
func TestARecordForAnotherTargetIsNotUsed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := skipRecordStore{at: filepath.Join(t.TempDir(), "r")}

	shape, err := shapeOf(shapeInput{
		Source: []byte(shapeSrc), Target: "test", Platform: "linux/arm64",
	})
	if err != nil {
		t.Fatal(err)
	}

	s.put(skipRecord{
		Version: skipRecordVersion, Target: "test", Platform: "linux/arm64",
		Shape: shape.String(), Key: jobKey(shape, nil),
	})

	skip, err := askFor(t, root, s, shapeSrc)
	if err != nil || skip {
		t.Errorf("a record about +test: skip=%t err=%v", skip, err)
	}
}

// **A build whose record was written before a key was configured is not found
// afterwards.** The shape carries which way secrets were covered, so the two
// cannot be compared - and being unable to find it is the right outcome, not a
// bug to work around.
func TestARecordWrittenWithoutAKeyIsNotFoundWithOne(t *testing.T) {
	t.Parallel()

	const src = "VERSION 0.8\n\nbuild:\n    FROM alpine@sha256:aa\n" +
		"    RUN --secret TOKEN echo hi\n"

	root := t.TempDir()
	s := skipRecordStore{at: filepath.Join(t.TempDir(), "r")}

	byName, err := shapeOf(shapeInput{
		Source: []byte(src), Target: "build", Platform: "linux/arm64",
		SecretNames: []string{"TOKEN"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s.put(skipRecord{
		Version: skipRecordVersion, Target: "build", Platform: "linux/arm64",
		Shape: byName.String(), Key: jobKey(byName, nil),
	})

	skip, _, _, err := wouldSkip(shapeInput{
		Source: []byte(src), Target: "build", Platform: "linux/arm64",
		SecretDigests: map[string]string{"TOKEN": "a-keyed-digest"},
	}, root, s)

	if err != nil || skip {
		t.Errorf("a name-keyed record answered a digest-keyed build: skip=%t err=%v", skip, err)
	}
}
