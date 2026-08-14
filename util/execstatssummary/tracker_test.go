package execstatssummary

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTracker(t *testing.T) {
	t.Parallel()

	t.Run("observe and summarize", func(t *testing.T) {
		t.Parallel()

		tracker := NewTracker("-")
		tracker.Observe("target1", "cmd1", 1024, 100*time.Millisecond)
		tracker.Observe("target2", "cmd2", 2048, 200*time.Millisecond)

		summary := tracker.String()
		want := `target   command  memory  cpu
target1  cmd1     1.0 kB  100ms
target2  cmd2     2.0 kB  200ms
`

		require.Equal(t, want, summary)
	})

	t.Run("observe updates max values", func(t *testing.T) {
		t.Parallel()

		tracker := NewTracker("-")
		tracker.Observe("target1", "cmd1", 512, 50*time.Millisecond)
		tracker.Observe("target1", "cmd1", 1024, 100*time.Millisecond)

		summary := tracker.String()
		want := `target   command  memory  cpu
target1  cmd1     1.0 kB  100ms
`

		require.Equal(t, want, summary)
	})
}
