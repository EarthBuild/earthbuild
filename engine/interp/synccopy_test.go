package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `COPY --sync` is gated on the VERSION line.
//
// **Accepting a flag is a statement about the dialect.** The reference has no
// equivalent, so a file using this builds here and nowhere else - and the
// VERSION line is where an Earthfile says which dialect it is written in. A file
// that used it silently would be one whose author had not been told it had
// stopped being portable.
func TestSyncNeedsItsFeature(t *testing.T) {
	t.Parallel()

	const src = "build:\n    FROM alpine:3.20\n    COPY --sync features.go /app\n"

	_, err := interp.Build("VERSION 0.8\n"+src, "build")
	if err == nil {
		t.Fatal("COPY --sync was accepted with no feature asking for it")
	}

	// It says which flag to write, because the remedy is one word and a reader
	// who has to go looking for it has been failed by the message.
	if !strings.Contains(err.Error(), "--sync") {
		t.Errorf("the refusal does not name the feature: %v", err)
	}
}

// Declared, it builds.
func TestSyncWorksWhenAskedFor(t *testing.T) {
	t.Parallel()

	// `--dir` because `--sync` requires it, and `.` because the package
	// directory is this build's context.
	_, err := interp.Build(
		"VERSION --sync 0.8\nbuild:\n    FROM alpine:3.20\n    COPY --sync --dir . /app\n",
		"build")
	if err != nil {
		t.Fatalf("a file that asked for the feature was refused: %v", err)
	}
}

// And an ordinary COPY is unaffected either way.
func TestAnOrdinaryCopyNeedsNoFeature(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(
		"VERSION 0.8\nbuild:\n    FROM alpine:3.20\n    COPY features.go /app\n", "build")
	if err != nil {
		t.Fatalf("an ordinary COPY was refused: %v", err)
	}
}

// `--sync` needs `--dir`, because deleting needs a scope.
//
// A copy of a list of files into a directory says nothing about what else that
// directory may hold; removing on that basis would delete what the Earthfile
// never mentioned. With `--dir` the destination is the copied directory itself,
// which is the scope the author named.
func TestSyncNeedsDir(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(
		"VERSION --sync 0.8\nbuild:\n    FROM alpine:3.20\n    COPY --sync features.go /app\n",
		"build")
	if err == nil {
		t.Fatal("COPY --sync was accepted without --dir")
	}

	if !strings.Contains(err.Error(), "--dir") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}
}
