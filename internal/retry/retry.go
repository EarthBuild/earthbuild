// Package retry runs an operation again when it fails, on a policy the operator
// can change.
//
// **One policy, many sites.** Before this, every place that wanted to try again
// wrote its own loop: the sandbox boot retried once after removing a stale VM,
// the fleet join looped forever on a fixed interval, and a pull from the local
// registry did not retry at all and failed about one CI job-run in a hundred.
// Three shapes, three sets of constants, and no way to change any of them
// without editing Go. The shapes were not the problem - the operations really do
// differ - but the arithmetic and the context handling were copied each time,
// and only some copies got the context handling right.
//
// **The waits come from `cenkalti/backoff`**, which was already in the module
// graph indirectly. Nothing here reimplements exponential growth or jitter, and
// the jitter is the part worth having a dependency for: EarthBuild pulls images
// concurrently through an `errgroup`, so a hand-rolled fixed backoff has every
// failed pull retrying in lockstep at exactly the moment the last one did.
package retry

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// Strategy is how the wait grows between attempts.
//
// A string rather than an integer so that the environment variable and the Go
// constant are the same word, and a typo in a setting is reported as the word
// somebody typed rather than as a number they never chose.
type Strategy string

const (
	// Exponential lengthens the wait after each failure. The default, and the
	// right shape for a contended or recovering resource: the longer it has
	// been failing, the less likely another immediate attempt helps.
	Exponential Strategy = "exponential"

	// Fixed waits the same each time. For an operation whose failure is a race
	// rather than a load problem, where waiting longer buys nothing - a
	// keep-alive connection closed under a client that was about to reuse it
	// resolves on the next attempt or not at all.
	Fixed Strategy = "fixed"
)

// Environment variables that change the default policy. See docs/native/settings.md.
const (
	EnvAttempts = "EARTH_RETRY_ATTEMPTS"
	EnvBase     = "EARTH_RETRY_BASE"
	EnvStrategy = "EARTH_RETRY_STRATEGY"
)

// Policy is how many times to try and how long to wait between tries.
//
// The zero value is usable: `Do` fills each field from `Default` as it reads it,
// so a caller that cares only about the count writes `Policy{Attempts: 5}` and
// gets sensible waits.
type Policy struct {
	// Attempts is the total number of tries, not the number of extra ones. One
	// means "do not retry", which is a policy and not a mistake.
	Attempts int

	// Base is the wait after the first failure.
	Base time.Duration

	// Max caps the wait however far the strategy has grown it.
	Max time.Duration

	// Jitter spreads the waits of concurrent callers, as a fraction of the
	// interval. Zero here means "use the default"; a policy that genuinely
	// wants none sets it negative, which reads oddly and is why nothing does.
	Jitter float64

	// Strategy selects how the wait grows. Empty means Exponential.
	Strategy Strategy

	// Retryable reports whether an error is worth another attempt. Nil retries
	// every error, which is right where the operation has no permanent failure
	// mode worth failing fast on - see the local-registry pull.
	Retryable func(error) bool
}

// Default is the policy a caller gets without saying anything.
//
// Four attempts over roughly a second and a half. Chosen against the fault this
// package was written for - a transient close on loopback, seen in about 1% of
// CI job-runs - where the first retry removes nearly all of it and the rest are
// for the tail. Long enough to cross a brief outage, short enough that a
// genuinely broken operation still fails while somebody is watching.
func Default() Policy {
	return Policy{
		Attempts: 4,
		Base:     150 * time.Millisecond,
		Max:      2 * time.Second,
		Jitter:   0.5,
		Strategy: Exponential,
	}
}

// FromEnv is the default policy with any settings the environment names applied.
//
// `lookup` is passed in rather than reading `os.Getenv` directly so a test can
// supply an environment without touching the process's own.
//
// Each field is independent: setting the strategy does not silently reset the
// count. An unparseable value is an error naming the variable, because a
// mistyped duration that quietly falls back to the default is a setting that
// appears to work and does nothing.
func FromEnv(lookup func(string) string) (Policy, error) {
	p := Default()

	if v := lookup(EnvAttempts); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Policy{}, fmt.Errorf(
				"%s=%q: expected a whole number of attempts, at least 1"+
					"\n  1 means do not retry; the default is %d",
				EnvAttempts, v, Default().Attempts)
		}

		p.Attempts = n
	}

	if v := lookup(EnvBase); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return Policy{}, fmt.Errorf(
				"%s=%q: expected a positive duration such as 150ms or 2s"+
					"\n  the default is %s",
				EnvBase, v, Default().Base)
		}

		p.Base = d
	}

	if v := lookup(EnvStrategy); v != "" {
		switch Strategy(v) {
		case Exponential, Fixed:
			p.Strategy = Strategy(v)
		default:
			return Policy{}, fmt.Errorf(
				"%s=%q: expected %q or %q",
				EnvStrategy, v, Exponential, Fixed)
		}
	}

	return p, nil
}

// backOff is the policy expressed in the terms the backoff library takes.
func (p Policy) backOff() backoff.BackOff {
	d := Default()

	base := p.Base
	if base <= 0 {
		base = d.Base
	}

	if p.Strategy == Fixed {
		return backoff.NewConstantBackOff(base)
	}

	ceiling := p.Max
	if ceiling <= 0 {
		ceiling = d.Max
	}

	jitter := p.Jitter
	if jitter == 0 {
		jitter = d.Jitter
	}

	if jitter < 0 {
		jitter = 0
	}

	b := backoff.NewExponentialBackOff()
	b.InitialInterval = base
	b.MaxInterval = ceiling
	b.RandomizationFactor = jitter

	return b
}

// Do runs op until it succeeds, the policy is exhausted, or ctx ends.
//
// The operation's own error is returned, wrapped with the number of attempts
// spent when there was more than one: "failed after 4 attempts: ..." says
// something a bare error does not, which is that retrying was tried and did not
// help. `errors.Is` and `errors.As` see through it.
//
// An error the policy declines is returned unchanged and immediately - no wrap,
// because nothing was retried and saying "after 1 attempt" would be noise.
func Do(ctx context.Context, p Policy, op func() error) error {
	attempts := p.Attempts
	if attempts < 1 {
		attempts = Default().Attempts
	}

	spent := 0

	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		spent++

		opErr := op()
		if opErr == nil {
			return struct{}{}, nil
		}

		// Permanent stops the library retrying and is unwrapped below, so the
		// caller sees exactly what the operation returned.
		if p.Retryable != nil && !p.Retryable(opErr) {
			return struct{}{}, backoff.Permanent(opErr)
		}

		return struct{}{}, opErr
	},
		backoff.WithBackOff(p.backOff()),
		backoff.WithMaxTries(uint(attempts)),
	)

	switch {
	case err == nil:
		return nil
	case spent <= 1:
		// Declined, or failed on a policy of one attempt. Either way nothing
		// was retried and the error stands on its own.
		return err
	default:
		return fmt.Errorf("failed after %d attempts: %w", spent, err)
	}
}

// Cancelled reports whether an error is a context ending rather than the
// operation failing, for callers that treat the two differently.
func Cancelled(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
