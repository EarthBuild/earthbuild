package cli

import (
	"strings"
	"testing"
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
	})

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

	recordPinning(&out, nil)

	if out.Len() != 0 {
		t.Errorf("said something about nothing: %q", out.String())
	}
}
