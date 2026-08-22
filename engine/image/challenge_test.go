package image_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// Resolving a tag against a registry that authenticates costs three round trips,
// and one of them fetches nothing.
//
// The probe exists only to collect the `WWW-Authenticate` header, and what it
// collects - a realm and a service - is stable, public metadata about the
// registry. On docker.io that exchange is 0.465s of a build that has nothing to
// do (E534).
func TestATagCostsAProbeATokenAndAManifest(t *testing.T) {
	t.Parallel()

	f := &fakeRegistry{auth: true}
	host := f.start(t)

	got, err := image.Resolve(context.Background(), host+"/thing:latest", image.Options{Plain: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if !strings.Contains(got, "@sha256:") {
		t.Errorf("resolved to %q, which names no digest", got)
	}

	if f.probes != 1 {
		t.Errorf("%d probes, want 1", f.probes)
	}

	if f.tokens != 1 {
		t.Errorf("%d token requests, want 1", f.tokens)
	}

	if f.manifests != 1 {
		t.Errorf("%d manifest requests, want 1", f.manifests)
	}
}

// A registry that does not authenticate is still served, and asks for no token.
//
// The path every other test here takes, stated rather than assumed: `token`
// returns an empty string when the probe is not answered with a challenge, and
// the manifest request then carries no Authorization header at all.
func TestAnUnauthenticatedRegistryNeedsNoToken(t *testing.T) {
	t.Parallel()

	f := &fakeRegistry{}
	host := f.start(t)

	_, err := image.Resolve(context.Background(), host+"/thing:latest", image.Options{Plain: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if f.tokens != 0 {
		t.Errorf("%d token requests against a registry that issues no challenge, want 0", f.tokens)
	}
}

// A registry's challenge is remembered, so the probe is paid once rather than
// once per build.
//
// What is remembered is where to ask for a token, which is public metadata about
// the registry - not the token, which is a credential and a separate decision
// (E535).
func TestTheProbeIsPaidOnce(t *testing.T) {
	t.Parallel()

	f := &fakeRegistry{auth: true}
	host := f.start(t)
	opt := image.Options{Plain: true, Challenges: t.TempDir()}

	for i := range 3 {
		if _, err := image.Resolve(context.Background(), host+"/thing:latest", opt); err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}

	if f.probes != 1 {
		t.Errorf("%d probes for 3 resolutions, want 1", f.probes)
	}

	// Still one token each: a token expires, and this remembers where to ask
	// rather than what was answered.
	if f.tokens != 3 {
		t.Errorf("%d token requests for 3 resolutions, want 3", f.tokens)
	}
}

// A remembered challenge that has gone stale costs a probe, not a build.
//
// A registry may move its realm. The remembered answer is an optimisation, so
// when it stops working the full exchange is done again and the new answer
// replaces it.
func TestAStaleChallengeFallsBackToTheProbe(t *testing.T) {
	t.Parallel()

	f := &fakeRegistry{auth: true}
	host := f.start(t)
	dir := t.TempDir()
	opt := image.Options{Plain: true, Challenges: dir}

	// Somewhere that will not answer: the realm this registry used to name.
	image.RememberChallengeForTest(dir, host+"/thing", "http://127.0.0.1:1/token")

	got, err := image.Resolve(context.Background(), host+"/thing:latest", opt)
	if err != nil {
		t.Fatalf("a stale challenge was not recovered from: %v", err)
	}

	if !strings.Contains(got, "@sha256:") {
		t.Errorf("resolved to %q, which names no digest", got)
	}

	if f.probes != 1 {
		t.Errorf("%d probes, want 1 - the stale answer should have been replaced", f.probes)
	}

	// And the replacement is used next time.
	if _, err := image.Resolve(context.Background(), host+"/thing:latest", opt); err != nil {
		t.Fatalf("second resolve: %v", err)
	}

	if f.probes != 1 {
		t.Errorf("%d probes after the answer was refreshed, want 1", f.probes)
	}
}

// With the challenge already known, the registry is dialled while the token is
// being fetched.
//
// Deleting the probe made the token phase 0.30s cheaper and the build only 0.14s
// cheaper: the probe had been dialling the registry, and the manifest request
// inherited that connection. The two handshakes are to different hosts and have
// nothing to say to each other, so they can happen at once (E535).
func TestTheRegistryIsDialledWhileTheTokenIsFetched(t *testing.T) {
	t.Parallel()

	f := &fakeRegistry{auth: true}
	host := f.start(t)
	opt := image.Options{Plain: true, Challenges: t.TempDir()}

	// First resolution learns the challenge; it probes, so there is nothing to
	// warm in parallel with.
	if _, err := image.Resolve(context.Background(), host+"/thing:latest", opt); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	if f.pings != 0 {
		t.Errorf("%d pings on the resolution that probed, want 0 - the probe warms it", f.pings)
	}

	if _, err := image.Resolve(context.Background(), host+"/thing:latest", opt); err != nil {
		t.Fatalf("second resolve: %v", err)
	}

	if f.pings != 1 {
		t.Errorf("%d pings on the resolution that skipped the probe, want 1", f.pings)
	}

	if f.probes != 1 {
		t.Errorf("%d probes, want 1 - the ping must not become a second probe", f.probes)
	}
}
