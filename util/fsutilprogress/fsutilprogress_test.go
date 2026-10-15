package fsutilprogress

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/stretchr/testify/require"
)

func newTestLogger(buf *bytes.Buffer) *conslogging.ConsoleLogger {
	return conslogging.New(buf, nil, 0, conslogging.Debug, false)
}

func TestProgressCallback_Info_Completion(t *testing.T) {
	t.Parallel()

	t.Run("with context name", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		cb := NewContext("src/context", newTestLogger(&buf))
		cb.Info(1024, true)

		require.Contains(t, buf.String(), "transferred 1.0 kB for context src/context\n")
	})

	t.Run("without context name", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		cb := New(newTestLogger(&buf))
		cb.Info(2048, true)

		require.Empty(t, buf.String())
	})
}

func TestProgressCallback_Info_StateResetOnCompletion(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cb := NewContext("ctx", newTestLogger(&buf))
	cb.now = func() time.Time { return now }

	// Start first transfer.
	cb.Info(5000, false)
	require.Empty(t, buf.String())

	// Complete first transfer after 10s.
	now = now.Add(10 * time.Second)

	cb.Info(10000, true)
	require.Contains(t, buf.String(), "transferred 10 kB for context ctx\n")

	buf.Reset()

	// Advance across a 20s idle gap and start second transfer;
	// baseline was reset on completion so no premature periodic summary is logged.
	now = now.Add(20 * time.Second)

	cb.Info(200, false)
	require.Empty(t, buf.String())

	// Advance 16s into second transfer and verify periodic rate is measured from the new baseline.
	now = now.Add(16 * time.Second)

	cb.Info(3400, false)
	require.Contains(t, buf.String(), "transferred 3.4 kB; transfer rate: 200 B/s\n")
}

func TestProgressCallback_Info_PeriodicThrottling(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cb := New(newTestLogger(&buf))
	cb.now = func() time.Time { return now }

	// First call at t=0 sets baseline without emitting log output.
	cb.Info(1000, false)
	require.Empty(t, buf.String())

	// Call at exact 15s boundary (d <= 15s) is throttled and emits nothing.
	now = now.Add(15 * time.Second)

	cb.Info(2000, false)
	require.Empty(t, buf.String())

	// Call at t=16s logs summary computed from t=0 baseline: (5000-1000)/16s = 250 B/s.
	now = now.Add(time.Second)

	cb.Info(5000, false)
	require.Contains(t, buf.String(), "transferred 5.0 kB; transfer rate: 250 B/s\n")
}

func TestProgressCallback_Info_NegativeDeltaResetsBaseline(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cb := New(newTestLogger(&buf))
	cb.now = func() time.Time { return now }

	// Initial observation at t=0.
	cb.Info(5000, false)
	require.Empty(t, buf.String())

	// Smaller cumulative byte count at t=5s resets baseline without logging.
	now = now.Add(5 * time.Second)

	cb.Info(1000, false)
	require.Empty(t, buf.String())

	// Observation 16s after reset (t=21s) logs rate from new baseline: (2600-1000)/16s = 100 B/s.
	now = now.Add(16 * time.Second)

	cb.Info(2600, false)
	require.Contains(t, buf.String(), "transferred 2.6 kB; transfer rate: 100 B/s\n")
}

func TestReceiveFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		relPath string
		want    string
		size    int64
	}{
		{
			name:    "known file size",
			relPath: "output.tar",
			want:    "received data for output.tar (1.0 MB)\n",
			size:    1024 * 1024,
		},
		{
			name:    "zero file size",
			relPath: "empty.txt",
			want:    "received data for empty.txt (0 B)\n",
			size:    0,
		},
		{
			name:    "unknown size (-1)",
			relPath: "stream.dat",
			want:    "received data for stream.dat\n",
			size:    -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			onFile := ReceiveFile(newTestLogger(&buf))
			onFile(tt.relPath, tt.size)

			require.Contains(t, buf.String(), tt.want)
		})
	}
}

func TestProgressCallback_Concurrent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	cb := NewContext("concurrent", logger)

	var wg sync.WaitGroup

	for n := range 20 {
		wg.Go(func() {
			cb.Info(n*100, false)
			cb.Info(n*200, true)
		})
	}

	wg.Wait()
	require.NotEmpty(t, buf.String())
}
