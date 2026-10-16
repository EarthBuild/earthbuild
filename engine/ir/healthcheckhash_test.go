package ir_test

import (
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A HEALTHCHECK's durations are scalars, encoded fixed-width rather than as
// counts.
//
// They went through Count, the u32 length prefix of green paper §1.4, which
// refuses anything above 4294967295 - and a duration is nanoseconds, so every
// interval longer than 4.3s panicked the planner. Docker's default interval is
// 30s, which makes that every realistic HEALTHCHECK; the
// `tests/with-docker-healthcheck` fixture was the first to carry one.
func TestAHealthcheckWithRealisticDurationsHashes(t *testing.T) {
	t.Parallel()

	key := func(hc ir.Healthcheck) ir.NodeID {
		h := ir.NewHasher()
		ir.HashImage(h, &ir.ImageConfig{Healthcheck: &hc})

		return h.Sum()
	}

	base := ir.Healthcheck{
		Test:          []string{"CMD-SHELL", "curl -f http://localhost/ || exit 1"},
		Interval:      30 * time.Second,
		Timeout:       5 * time.Second,
		StartPeriod:   10 * time.Second,
		StartInterval: 2 * time.Second,
		Retries:       3,
	}

	k := key(base)

	// Each duration is its own field: the same values in different fields
	// are a different healthcheck.
	swapped := base
	swapped.Interval, swapped.Timeout = base.Timeout, base.Interval

	if key(swapped) == k {
		t.Error("swapping Interval and Timeout left the key unchanged: the fields are not positional")
	}

	// Durations a u32 cannot tell apart are still told apart. A fix that
	// truncated to 32 bits would stop the panic and start a false cache hit.
	wrapped := base
	wrapped.Interval = base.Interval + (1<<32)*time.Nanosecond

	if key(wrapped) == k {
		t.Errorf("Interval %v and %v share a key: the duration was truncated", base.Interval, wrapped.Interval)
	}
}
