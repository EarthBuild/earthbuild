package cli

import (
	"strings"
	"testing"
	"time"
)

// A build that had to resolve a reference says how to stop having to.
//
// Resolving a tag is a registry round trip on every invocation - most of a build
// with nothing else to do - and a reference that names its digest skips it
// entirely. The engine knows the digest; the note is how the reader learns they
// can write it down (E534).
func TestABuildThatPinnedSaysHowToWriteItDown(t *testing.T) {
	t.Parallel()

	var out strings.Builder

	recordPinning(&out, map[string]string{
		"golang:1.26.5-alpine3.24": "golang:1.26.5-alpine3.24@sha256:787328",
	}, 0)

	got := out.String()
	if !strings.Contains(got, "--pin") {
		t.Errorf("a build that pinned did not mention the flag that writes it down:\n%s", got)
	}
}

// A build with nothing to pin says nothing.
//
// Every reference already a digest, or no resolver at all. Advice on how to fix
// what is not broken is how a reader learns to skip the line on the day it
// matters.
func TestABuildWithNothingToPinIsSilent(t *testing.T) {
	t.Parallel()

	var out strings.Builder

	recordPinning(&out, nil, 0)

	if out.Len() != 0 {
		t.Errorf("said something about nothing: %q", out.String())
	}
}

// A build says what the lookups cost it, when that is worth acting on.
//
// The advice is the same either way; the number is what makes it advice rather
// than a chore. A reader told "these took 0.41s of a 0.43s build" has a reason,
// and on the commonest thing a developer does - build again after changing
// nothing - that is nearly the whole invocation (E550).
func TestABuildSaysWhatItsLookupsCost(t *testing.T) {
	t.Parallel()

	var out strings.Builder

	recordPinning(&out, map[string]string{"alpine:3.22": "alpine@sha256:2c9d26"},
		410*time.Millisecond)

	got := out.String()
	if !strings.Contains(got, "0.41s") {
		t.Errorf("the note does not say what the lookups cost:\n%s", got)
	}
}

// A cost too small to act on is left out rather than shrunk to nothing.
//
// "skips the 0.00s these lookups cost" reads as advice not worth taking, which
// on the next build against a slower registry it is.
func TestATinyLookupCostIsNotQuoted(t *testing.T) {
	t.Parallel()

	var out strings.Builder

	recordPinning(&out, map[string]string{"alpine:3.22": "alpine@sha256:2c9d26"},
		2*time.Millisecond)

	got := out.String()
	if strings.Contains(got, "0.00s") {
		t.Errorf("the note quotes a cost nobody can act on:\n%s", got)
	}

	if !strings.Contains(got, "--pin") {
		t.Errorf("the note stopped naming the flag:\n%s", got)
	}
}
