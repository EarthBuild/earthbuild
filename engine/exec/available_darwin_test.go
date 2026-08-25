package exec

import (
	"errors"
	"testing"
)

// The container service is asked about once, however many times it is asked.
//
// `Available` shells out to `container system status`, which costs 36ms, and a
// single build asked it **four times** - a third of the 165ms every invocation
// spent obtaining a guest client before it could run anything (E645).
//
// Memoised rather than made cheaper: the question is whether the service is
// running, and a service that stops mid-build is reported by the operation that
// then fails, not by a probe that happened to run again. The engine already
// answers a probe this way - see `needsUserXattr` - and for the same reason.
//
//nolint:paralleltest // swaps a package-level probe
func TestTheContainerServiceIsAskedAboutOnce(t *testing.T) {
	asked := 0

	restore := probeService
	probeService = func() error {
		asked++

		return nil
	}

	t.Cleanup(func() { probeService = restore; availableOnce = onceFor() })

	availableOnce = onceFor()

	var a Apple

	for range 4 {
		err := a.Available()
		if err != nil {
			t.Fatalf("the probe reported unavailable: %v", err)
		}
	}

	if asked != 1 {
		t.Errorf("the service was asked about %d times, want 1"+
			"\n  each ask is a `container system status`, and they cost 36ms apiece", asked)
	}
}

// And a service that is not there stays not there, rather than being re-asked.
//
//nolint:paralleltest // swaps a package-level probe
func TestAnUnavailableServiceIsRemembered(t *testing.T) {
	asked := 0
	want := errors.New("the apiserver is not running")

	restore := probeService
	probeService = func() error {
		asked++

		return want
	}

	t.Cleanup(func() { probeService = restore; availableOnce = onceFor() })

	availableOnce = onceFor()

	var a Apple

	for range 3 {
		err := a.Available()
		if !errors.Is(err, want) {
			t.Fatalf("got %v, want the probe's own error", err)
		}
	}

	if asked != 1 {
		t.Errorf("the service was asked about %d times, want 1", asked)
	}
}
