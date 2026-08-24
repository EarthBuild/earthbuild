package fleet_test

import (
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A fleet without a secret is refused, not joined.
//
// C.1's normative term. A key derived from public metadata alone can be derived
// by **any observer**, who then joins the mesh and serves results into somebody
// else's build - and a worker misconfigured this way would not fail. It would
// join a fleet anyone could join, and the first sign of trouble would be a wrong
// result nobody could explain.
//
// So the refusal is at configuration rather than at the first surprising answer.
func TestAFleetWithoutASecretIsRefused(t *testing.T) {
	t.Setenv(fleet.EnvSession, "s")
	t.Setenv(fleet.EnvRun, "1")
	t.Setenv(fleet.EnvSecret, "")

	_, _, err := fleet.FromEnv()
	if !errors.Is(err, fleet.ErrNoSecret) {
		t.Errorf("a fleet configured with everything but a secret gave %v", err)
	}
}

// Everything but the secret may be absent.
//
// A fleet of one person's two laptops needs no run identifier, and requiring one
// would be ceremony. Only the secret's absence is unsafe.
func TestOnlyTheSecretIsRequired(t *testing.T) {
	t.Setenv(fleet.EnvSecret, "shh")
	t.Setenv(fleet.EnvSession, "")
	t.Setenv(fleet.EnvRun, "")
	t.Setenv(fleet.EnvAttempt, "")
	t.Setenv(fleet.EnvRepo, "")

	s, secret, err := fleet.FromEnv()
	if err != nil {
		t.Fatalf("a fleet with only a secret was refused: %v", err)
	}

	if string(secret) != "shh" {
		t.Errorf("the secret arrived as %q", secret)
	}

	if s.Attempt != 0 {
		t.Errorf("an absent attempt became %d", s.Attempt)
	}
}

// An attempt that is not a number is a misconfiguration, not a zero.
//
// Defaulting would silently merge a retry with the run it retries - the two
// would derive the same driver and join one mesh - which is precisely what the
// term exists to prevent.
func TestAnUnreadableAttemptIsRefused(t *testing.T) {
	t.Setenv(fleet.EnvSecret, "shh")
	t.Setenv(fleet.EnvAttempt, "second")

	_, _, err := fleet.FromEnv()
	if err == nil {
		t.Fatal("an attempt of \"second\" was accepted; a retry would derive" +
			" the same driver as the run it retries")
	}

	if got := err.Error(); got == "" {
		t.Error("the refusal says nothing")
	}
}

// The session reaches the identity, so a misconfigured fleet is a separate one.
func TestTheEnvironmentReachesTheIdentity(t *testing.T) { //nolint:paralleltest // see the note below
	// Not parallel: t.Setenv, which panics in a parallel test.
	id := func(session string) string {
		t.Helper()

		t.Setenv(fleet.EnvSecret, "shh")
		t.Setenv(fleet.EnvSession, session)

		s, secret, err := fleet.FromEnv()
		if err != nil {
			t.Fatal(err)
		}

		got, err := fleet.DriverID(s, secret)
		if err != nil {
			t.Fatal(err)
		}

		return got.String()
	}

	if id("one") == id("two") {
		t.Error("two sessions derived one driver; a matrix axis in the session" +
			" would not separate two fleets")
	}
}
