package pin_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/pin"
)

const digest = "sha256:787328cefd7937073af18fc4b3a725f47e011ffdde9c2908239a25cae6b2f02b"

// resolveTo answers every reference with one digest, and records what it was
// asked. A rewrite is a text transformation; what a registry says is somebody
// else's test.
func resolveTo(asked *[]string) func(string) (string, error) {
	return func(ref string) (string, error) {
		*asked = append(*asked, ref)

		return ref + "@" + digest, nil
	}
}

// A tagged reference gains its digest and keeps its tag.
//
// `image:tag@digest` rather than `image@digest`: renovate's docker datasource
// reads that form natively and bumps both halves, and a reader can still see
// which version they are on. The digest is what lets the build skip the registry
// entirely - 0.60s of planning against 0.03s (E534).
func TestATaggedReferenceGainsItsDigest(t *testing.T) {
	t.Parallel()

	var asked []string

	src := "VERSION 0.8\n\ndeps:\n    FROM golang:1.26.5-alpine3.24\n    RUN go version\n"

	out, changed, err := pin.Rewrite([]byte(src), resolveTo(&asked))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	want := "VERSION 0.8\n\ndeps:\n    FROM golang:1.26.5-alpine3.24@" + digest + "\n    RUN go version\n"
	if string(out) != want {
		t.Errorf("rewrote to:\n%s\nwant:\n%s", out, want)
	}

	if len(changed) != 1 || changed[0].Line != 4 {
		t.Errorf("changes %+v, want one on line 4", changed)
	}
}

// Everything that is not an image is left alone.
//
// A target reference is not an image and has no digest to name; `scratch` is not
// a registry's to answer for; a reference built from an argument is not knowable
// until the build runs; and one that already names a digest is already the thing
// this produces.
func TestOnlyImagesArePinned(t *testing.T) {
	t.Parallel()

	src := strings.Join([]string{
		"VERSION 0.8",
		"",
		"base:",
		"    FROM scratch",
		"a:",
		"    FROM +base",
		"b:",
		"    FROM ./other+thing",
		"c:",
		"    FROM $SOME_IMAGE",
		"d:",
		"    FROM alpine:3.20@" + digest,
		"e:",
		"    FROM DOCKERFILE -f Dockerfile .",
		"",
	}, "\n")

	var asked []string

	out, changed, err := pin.Rewrite([]byte(src), resolveTo(&asked))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if string(out) != src {
		t.Errorf("something was rewritten:\n%s", out)
	}

	if len(changed) != 0 || len(asked) != 0 {
		t.Errorf("asked %v and changed %+v, want neither", asked, changed)
	}
}

// A flag before the reference stays where it is.
func TestFlagsBeforeTheReferenceSurvive(t *testing.T) {
	t.Parallel()

	var asked []string

	src := "VERSION 0.8\n\na:\n    FROM --platform=linux/amd64 alpine:3.20\n"

	out, _, err := pin.Rewrite([]byte(src), resolveTo(&asked))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	want := "VERSION 0.8\n\na:\n    FROM --platform=linux/amd64 alpine:3.20@" + digest + "\n"
	if string(out) != want {
		t.Errorf("rewrote to %q, want %q", out, want)
	}

	if len(asked) != 1 || asked[0] != "alpine:3.20" {
		t.Errorf("asked %v, want the reference alone", asked)
	}
}

// One reference named twice is resolved once.
func TestARepeatedReferenceIsResolvedOnce(t *testing.T) {
	t.Parallel()

	var asked []string

	src := "VERSION 0.8\n\na:\n    FROM alpine:3.20\nb:\n    FROM alpine:3.20\n"

	out, changed, err := pin.Rewrite([]byte(src), resolveTo(&asked))
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if len(asked) != 1 {
		t.Errorf("resolved %d times, want 1: %v", len(asked), asked)
	}

	if len(changed) != 2 {
		t.Errorf("%d lines changed, want 2", len(changed))
	}

	if strings.Count(string(out), digest) != 2 {
		t.Errorf("both lines should name the digest:\n%s", out)
	}
}

// A reference that cannot be resolved leaves its line as written.
//
// The same trade the resolver makes during a build: an unreachable registry
// means a coarser key, not a failed build - and here, a file this could not
// improve rather than a file it damaged.
func TestAnUnresolvableReferenceIsLeftAlone(t *testing.T) {
	t.Parallel()

	src := "VERSION 0.8\n\na:\n    FROM alpine:3.20\n    FROM golang:1.26\n"

	out, changed, err := pin.Rewrite([]byte(src), func(ref string) (string, error) {
		if strings.HasPrefix(ref, "golang") {
			return "", errors.New("no network")
		}

		return ref + "@" + digest, nil
	})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if !strings.Contains(string(out), "FROM golang:1.26\n") {
		t.Errorf("the unresolvable line was altered:\n%s", out)
	}

	// Both attempts are reported: the caller says what it pinned *and* what it
	// could not, because silence about the second reads as "nothing to do".
	if len(changed) != 2 {
		t.Fatalf("%d attempts reported, want 2", len(changed))
	}

	if changed[0].Err != nil {
		t.Errorf("the reference that resolved carries an error: %v", changed[0].Err)
	}

	if changed[1].Err == nil {
		t.Error("the reference that did not resolve carries no error")
	}

	if changed[1].To != "" {
		t.Errorf("a failed attempt names a replacement %q", changed[1].To)
	}
}

// The pinned form keeps the tag the author wrote.
//
// `Resolve` answers in its own canonical form, which names the repository and
// the digest and drops the tag. That is right for provenance and wrong for a
// file somebody reads: the tag is which version they are on, and it is what
// renovate's docker datasource matches to bump both halves. A first attempt at
// this wrote `golang@sha256:...` into an Earthfile and lost that.
func TestThePinnedFormKeepsTheTag(t *testing.T) {
	t.Parallel()

	got, err := pin.WithDigest("golang:1.26.5-alpine3.24", "golang@"+digest)
	if err != nil {
		t.Fatalf("with digest: %v", err)
	}

	if want := "golang:1.26.5-alpine3.24@" + digest; got != want {
		t.Errorf("pinned to %q, want %q", got, want)
	}
}

// A resolution that names no digest is refused rather than written down.
func TestAResolutionWithoutADigestIsRefused(t *testing.T) {
	t.Parallel()

	_, err := pin.WithDigest("golang:1.26", "golang:1.26")
	if err == nil {
		t.Error("a reference with no digest was accepted as a pin")
	}
}
