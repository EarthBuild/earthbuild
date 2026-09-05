// Package enginetrace is a Phase 0 measurement harness for the native-engine work.
//
// It answers one question: how much of a build's wall clock is spent crossing the
// BuildKit process boundary rather than doing work? See
// docs-internals/experiments-adversarial.md, experiment E2, whose kill criterion is
// that marshal plus gRPC plus export account for under 15% of wall clock - in which
// case the performance argument for a native engine is dead and must be withdrawn.
//
// Disabled unless EARTH_ENGINE_TRACE is set, and cheap when disabled: one atomic load
// per call site.
//
// This package is measurement scaffolding. It is expected to be deleted once Phase 0
// reports, and it should not grow features.
package enginetrace

import (
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"
)

// Kind names a measured operation. Kinds are free-form so call sites can be added
// without touching this file.
type Kind string

// Kinds recorded by the current call sites.
const (
	// KindMarshal is time spent turning an LLB state into a protobuf definition.
	KindMarshal Kind = "marshal"
	// KindLockWait is time blocked on pllb's process-global mutex before marshalling.
	KindLockWait Kind = "lock-wait"
	// KindSolve is a full gateway Solve round trip, excluding the marshal that fed it.
	KindSolve Kind = "solve"
	// KindRead is a ReadFile or ReadDir round trip against a solved reference.
	KindRead Kind = "read"
)

var (
	enabled = os.Getenv("EARTH_ENGINE_TRACE") != ""

	mu    sync.Mutex
	stats = map[Kind]*stat{}
	start = time.Now()
)

type stat struct {
	count int64
	total time.Duration
	max   time.Duration
	bytes int64
}

// Enabled reports whether tracing is on. Call sites that would have to do extra work
// to produce a measurement should check this first.
func Enabled() bool { return enabled }

// Record adds one observation. bytes is the size of any payload crossing the
// boundary, or 0 where that is meaningless.
func Record(k Kind, d time.Duration, bytes int) {
	if !enabled {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	s := stats[k]
	if s == nil {
		s = &stat{}
		stats[k] = s
	}

	s.count++
	s.total += d
	s.bytes += int64(bytes)

	if d > s.max {
		s.max = d
	}
}

// Time records the duration of f under kind k.
func Time(k Kind, bytes int, f func()) {
	if !enabled {
		f()

		return
	}

	t0 := time.Now()
	f()
	Record(k, time.Since(t0), bytes)
}

// Dump writes the accumulated table. It is a no-op when tracing is disabled, so it
// can be deferred unconditionally.
//
// Percentages are of wall clock since process start and will not sum to 100: these
// operations overlap across goroutines, and a build is concurrent. A figure above
// 100% means the work was parallel, not that the arithmetic is wrong.
func Dump(w io.Writer) {
	if !enabled {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	wall := time.Since(start)

	kinds := make([]Kind, 0, len(stats))
	for k := range stats {
		kinds = append(kinds, k)
	}

	sort.Slice(kinds, func(i, j int) bool { return stats[kinds[i]].total > stats[kinds[j]].total })

	fmt.Fprintf(w, "\nengine trace (wall %s)\n", wall.Round(time.Millisecond))
	fmt.Fprintf(w, "%-12s %8s %12s %12s %10s %8s\n", "kind", "count", "total", "max", "MiB", "%wall")

	for _, k := range kinds {
		s := stats[k]
		fmt.Fprintf(w, "%-12s %8d %12s %12s %10.1f %7.1f%%\n",
			k, s.count,
			s.total.Round(time.Millisecond),
			s.max.Round(time.Millisecond),
			float64(s.bytes)/(1024*1024),
			100*float64(s.total)/float64(wall))
	}
}
