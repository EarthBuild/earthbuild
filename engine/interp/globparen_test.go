package interp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAGlobbedReferenceKeepsItsBuildArguments.
//
// `COPY (./wildcard/*+test/out* --NAME=out) .` is both halves of the language
// at once: a reference carrying build-argument overrides, written with a
// pattern in the directory. `ProcessParamsAndQuotes` merges the overrides and
// the reference into a single token, brackets included, so the expander read
// `(./wildcard/*` as the directory to match, found nothing of that name, and
// produced **no sources at all**.
//
// Silently, which is the part worth fixing: a COPY that expands to nothing is
// an error nowhere, so the step did not happen and the target failed two lines
// later looking for files nothing had copied.
//
// Each expansion keeps the overrides, because they are what the reference
// means - the same directory built with different arguments is a different
// build, and dropping them would quietly take whatever was built last.
func TestAGlobbedReferenceKeepsItsBuildArguments(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, d := range []string{"wildcard/bar", "wildcard/foo"} {
		err := os.MkdirAll(filepath.Join(root, d), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(root, d, "Earthfile"),
			[]byte("VERSION 0.8\ntest:\n    FROM alpine:3.21\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := expandArtifactRef(root, "(./wildcard/*+test/out --NAME=given)")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"(./wildcard/bar+test/out --NAME=given)",
		"(./wildcard/foo+test/out --NAME=given)",
	}

	if len(got) != len(want) {
		t.Fatalf("expanded to %q, want %q\n  a pattern that matches nothing"+
			" copies nothing, and says so nowhere", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("expansion %d is %q, want %q", i, got[i], want[i])
		}
	}
}
