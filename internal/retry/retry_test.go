package retry_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/internal/retry"
)

// The policy is a ceiling, not a quota.
func TestAnOperationThatWorksIsTriedOnce(t *testing.T) {
	t.Parallel()

	tries := 0

	err := retry.Do(context.Background(), retry.Policy{Attempts: 3}, func() error {
		tries++

		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tries != 1 {
		t.Errorf("tried %d times, want 1", tries)
	}
}

func TestAnOperationThatFailsOnceIsRetried(t *testing.T) {
	t.Parallel()

	tries := 0

	err := retry.Do(context.Background(),
		retry.Policy{Attempts: 3, Base: time.Millisecond},
		func() error {
			tries++
			if tries == 1 {
				return errors.New("transient")
			}

			return nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tries != 2 {
		t.Errorf("tried %d times, want 2", tries)
	}
}

// The operation's own error survives, and the caller is told how many attempts
// were spent - a failure after several tries reads differently from a first one.
func TestAnOperationThatNeverWorksReturnsItsLastError(t *testing.T) {
	t.Parallel()

	tries := 0

	err := retry.Do(context.Background(),
		retry.Policy{Attempts: 3, Base: time.Millisecond},
		func() error {
			tries++

			return errors.New("still broken")
		})
	if err == nil {
		t.Fatal("want an error")
	}

	if !strings.Contains(err.Error(), "still broken") {
		t.Errorf("the operation's error must survive, got: %v", err)
	}

	if !strings.Contains(err.Error(), "3") {
		t.Errorf("the message should say how many attempts were spent, got: %v", err)
	}

	if tries != 3 {
		t.Errorf("tried %d times, want 3", tries)
	}
}

// A policy may decline a class of error. The error comes back unchanged, so
// `errors.Is` still works on it.
func TestAnErrorThePolicyDeclinesIsNotRetried(t *testing.T) {
	t.Parallel()

	permanent := errors.New("no such image")
	tries := 0

	err := retry.Do(context.Background(),
		retry.Policy{
			Attempts:  5,
			Base:      time.Millisecond,
			Retryable: func(e error) bool { return !errors.Is(e, permanent) },
		},
		func() error {
			tries++

			return permanent
		})
	if !errors.Is(err, permanent) {
		t.Errorf("want the declined error back, got: %v", err)
	}

	if tries != 1 {
		t.Errorf("tried %d times, want 1", tries)
	}
}

// Cancelling stops the waiting, not merely the next attempt: a policy with an
// hour's backoff must not hold a cancelled build open.
func TestACancelledContextStopsRetrying(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	tries := 0

	done := make(chan error, 1)

	go func() {
		done <- retry.Do(ctx, retry.Policy{Attempts: 5, Base: time.Hour}, func() error {
			tries++
			cancel()

			return errors.New("transient")
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("want context.Canceled, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Do did not return after its context was cancelled - it waited out the backoff")
	}

	if tries != 1 {
		t.Errorf("tried %d times, want 1", tries)
	}
}

// Both strategies are usable end to end. What the waits *are* is cenkalti's
// arithmetic and is not retested here; what is ours is that the name selects
// one and that neither hangs.
func TestBothStrategiesRun(t *testing.T) {
	t.Parallel()

	for _, s := range []retry.Strategy{retry.Exponential, retry.Fixed} {
		tries := 0

		err := retry.Do(context.Background(),
			retry.Policy{Attempts: 2, Base: time.Millisecond, Strategy: s},
			func() error {
				tries++
				if tries == 1 {
					return errors.New("transient")
				}

				return nil
			})
		if err != nil {
			t.Errorf("strategy %q: %v", s, err)
		}

		if tries != 2 {
			t.Errorf("strategy %q tried %d times, want 2", s, tries)
		}
	}
}

// The default is usable without being configured, which is the point of it.
func TestTheDefaultPolicyIsSane(t *testing.T) {
	t.Parallel()

	p := retry.Default()
	if p.Attempts < 2 {
		t.Errorf("Attempts = %d; a default that never retries is not a retry policy", p.Attempts)
	}

	if p.Base <= 0 {
		t.Errorf("Base = %v; want a positive first wait", p.Base)
	}

	if p.Max < p.Base {
		t.Errorf("Max %v is below Base %v", p.Max, p.Base)
	}
}

// The environment overrides one field at a time, and says so when it cannot.
func TestThePolicyReadsTheEnvironment(t *testing.T) {
	t.Parallel()

	env := map[string]string{"EARTH_RETRY_ATTEMPTS": "7", "EARTH_RETRY_STRATEGY": "fixed"}

	p, err := retry.FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if p.Attempts != 7 {
		t.Errorf("Attempts = %d, want 7", p.Attempts)
	}

	if p.Strategy != retry.Fixed {
		t.Errorf("Strategy = %q, want fixed", p.Strategy)
	}

	if p.Base != retry.Default().Base {
		t.Errorf("Base = %v; an unset field keeps the default", p.Base)
	}
}

func TestABadSettingIsRefusedByName(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]string{
		"EARTH_RETRY_ATTEMPTS": "many",
		"EARTH_RETRY_BASE":     "soon",
		"EARTH_RETRY_STRATEGY": "vigorous",
	} {
		_, err := retry.FromEnv(func(k string) string {
			if k == name {
				return value
			}

			return ""
		})
		if err == nil {
			t.Errorf("%s=%q was accepted", name, value)

			continue
		}

		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error should name the setting, got: %v", err)
		}
	}
}
