package fleet

import (
	"fmt"
	"os"
	"strconv"
)

// Environment variables a fleet is configured with.
//
// Environment rather than flags, because the values come from CI: a run
// identifier, a repository, an attempt number are already there, and a secret
// passed as a flag is a secret in a process listing.
const (
	// EnvSession names this fleet. **It must be unique per fleet, not per run.**
	// The driver's identity is derived from it, so two fleets sharing one value
	// advertise the same driver and the mesh connects them to each other - which
	// prior art on this mechanism reports as `workers joined: 3/2` on one CI
	// runner and `0/2` on another. A matrix axis belongs in this value.
	EnvSession = "EARTH_FLEET_SESSION"
	// EnvRun is the CI run or invocation.
	EnvRun = "EARTH_FLEET_RUN"
	// EnvAttempt distinguishes a retry from what it retries, so a re-run does
	// not join the previous attempt's mesh.
	EnvAttempt = "EARTH_FLEET_ATTEMPT"
	// EnvRepo is the repository being built.
	EnvRepo = "EARTH_FLEET_REPO"
	// EnvSecret is the term that makes the key unguessable (C.1). Everything
	// else here is visible to anyone watching a public repository.
	EnvSecret = "EARTH_FLEET_SECRET" //nolint:gosec // the name of a variable, not a credential
	// EnvCapacity is how many steps this worker runs at once. Absent means every
	// core the machine has.
	EnvCapacity = "EARTH_FLEET_CAPACITY"
	// EnvDriver is where the driver can be reached, as host:port. A worker is
	// told this; it derives the driver's *identity* rather than being told that
	// too.
	EnvDriver = "EARTH_FLEET_DRIVER"
)

// FromEnv reads a fleet's configuration.
//
// **Refuses without a secret**, which is C.1's normative term: a key derived
// from public metadata alone can be derived by any observer, who then joins the
// mesh and serves results into somebody's build. A worker misconfigured this way
// would not fail - it would join a fleet anyone could join - so the refusal is
// here rather than at the first surprising result.
//
// Everything else may be empty. A fleet of one person's two laptops needs no run
// identifier, and requiring one would be ceremony; the secret is the only term
// whose absence is unsafe.
func FromEnv() (Session, []byte, error) {
	secret := os.Getenv(EnvSecret)
	if secret == "" {
		return Session{}, nil, fmt.Errorf("%w: set %s"+
			"\n  every other term is visible to anyone watching the repository,"+
			" so without this the driver's identity is derivable by them too",
			ErrNoSecret, EnvSecret)
	}

	attempt := 0

	if v := os.Getenv(EnvAttempt); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Session{}, nil, fmt.Errorf("%s is %q, which is not a number"+
				"\n  it distinguishes a retry from the run it retries", EnvAttempt, v)
		}

		attempt = n
	}

	return Session{
		Session: os.Getenv(EnvSession),
		RunID:   os.Getenv(EnvRun),
		Attempt: attempt,
		Repo:    os.Getenv(EnvRepo),
	}, []byte(secret), nil
}

// CapacityFromEnv is how much of this machine a worker may use.
//
// The default is the whole machine, which is right for a builder and wrong for a
// machine somebody is also using - and that is most of the second machines
// anybody has. The default stays the whole machine anyway, because a worker that
// quietly took half of a dedicated builder would be a puzzle nobody thinks to
// look for.
//
// **Refused rather than clamped** when it will not parse. A worker silently
// ignoring `EARTH_FLEET_CAPACITY=eight` would take the whole machine on the one
// occasion somebody was explicitly trying to stop it.
func CapacityFromEnv() (int, error) {
	v := os.Getenv(EnvCapacity)
	if v == "" {
		return DefaultCapacity(), nil
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s is %q, which is not a number"+
			"\n  it is how many steps this worker runs at once; unset it to use"+
			" every core", EnvCapacity, v)
	}

	if n < 1 {
		return 0, fmt.Errorf("%s is %q, and a worker with no room joins the"+
			" fleet, is placed on, and then answers nothing"+
			"\n  to keep a machine out of a fleet, do not start a worker on it",
			EnvCapacity, v)
	}

	return n, nil
}
