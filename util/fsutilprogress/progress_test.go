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

	tests := []struct {
		name       string
		pathPrefix string
		wantSubstr string
		numBytes   int
	}{
		{
			name:       "with pathPrefix",
			pathPrefix: "src/context",
			wantSubstr: "transferred 1.0 kB for context src/context\n",
			numBytes:   1024,
		},
		{
			name:       "without pathPrefix",
			pathPrefix: "",
			wantSubstr: "",
			numBytes:   2048,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := newTestLogger(&buf)
			cb := New(tt.pathPrefix, logger)

			cb.Info(tt.numBytes, true)

			got := buf.String()
			if tt.wantSubstr == "" {
				require.Empty(t, got)
			} else {
				require.Contains(t, got, tt.wantSubstr)
			}
		})
	}
}

func TestProgressCallback_Info_StateResetOnCompletion(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	raw := New("ctx", logger)

	cb, ok := raw.(*progressCallback)
	require.True(t, ok)

	// Simulate first periodic update.
	cb.Info(5000, false)

	require.Equal(t, 5000, cb.lastBytes)
	require.False(t, cb.lastUpdate.IsZero())

	// Completion of first transfer.
	cb.Info(10000, true)

	// Verify state reset.
	require.Equal(t, 0, cb.lastBytes)
	require.True(t, cb.lastUpdate.IsZero())

	// Start a second transfer.
	cb.Info(200, false)

	require.Equal(t, 200, cb.lastBytes)
}

func TestProgressCallback_Info_PeriodicThrottling(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	raw := New("", logger)

	cb, ok := raw.(*progressCallback)
	require.True(t, ok)

	// First call sets lastUpdate and lastBytes.
	cb.Info(1000, false)
	buf.Reset()

	// Immediate subsequent call (delta <= 15s) should be throttled.
	cb.Info(2000, false)
	require.Empty(t, buf.String())

	// Force time back >15 seconds to simulate elapsed interval.
	cb.lastUpdate = time.Now().Add(-16 * time.Second)
	cb.Info(5000, false)

	want := "transferred 5.0 kB; transfer rate:"
	require.Contains(t, buf.String(), want)
}

func TestProgressCallback_File(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		pathPrefix string
		relPath    string
		wantSubstr string
		size       int64
	}{
		{
			name:       "known file size",
			pathPrefix: "build",
			relPath:    "output.tar",
			wantSubstr: "received data for build/output.tar (1.0 MB)\n",
			size:       1024 * 1024,
		},
		{
			name:       "unknown size (-1)",
			pathPrefix: "build",
			relPath:    "stream.dat",
			wantSubstr: "received data for build/stream.dat\n",
			size:       -1,
		},
		{
			name:       "without pathPrefix",
			pathPrefix: "",
			relPath:    "artifact.bin",
			wantSubstr: "received data for artifact.bin (500 B)\n",
			size:       500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := newTestLogger(&buf)
			cb := New(tt.pathPrefix, logger)

			cb.File(tt.relPath, tt.size)

			got := buf.String()
			if tt.wantSubstr == "" {
				require.Empty(t, got)
			} else {
				require.Contains(t, got, tt.wantSubstr)
			}
		})
	}
}

func TestProgressCallback_Concurrent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	cb := New("concurrent", logger)

	var wg sync.WaitGroup

	for n := range 20 {
		wg.Add(2)

		go func(val int) {
			defer wg.Done()

			cb.Info(val*100, false)
			cb.Info(val*200, true)
		}(n)

		go func(val int) {
			defer wg.Done()

			cb.File("file.txt", int64(val*50))
		}(n)
	}

	wg.Wait()
}
