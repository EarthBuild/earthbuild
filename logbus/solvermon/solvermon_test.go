package solvermon

import (
	"context"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/logbus"
	"github.com/EarthBuild/earthbuild/logstream"
	"github.com/EarthBuild/earthbuild/util/vertexmeta"
	"github.com/moby/buildkit/client"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestFirstFailureCapturesFirstFatalVertexError(t *testing.T) {
	t.Parallel()

	sm := New(logbus.New())
	completed := time.Now()

	err := sm.handleBuildkitStatus(&client.SolveStatus{
		Vertexes: []*client.Vertex{
			{
				Digest:    digest.FromString("fatal"),
				Name:      (&vertexmeta.VertexMeta{TargetID: "target-id", TargetName: "+target"}).ToVertexPrefix() + "RUN bad",
				Completed: &completed,
				Error:     `process "bad" did not complete successfully: exit code: 42`,
			},
			{
				Digest:    digest.FromString("later"),
				Name:      (&vertexmeta.VertexMeta{TargetID: "later-target", TargetName: "+later"}).ToVertexPrefix() + "RUN worse",
				Completed: &completed,
				Error:     `process "worse" did not complete successfully: exit code: 43`,
			},
		},
	})
	require.NoError(t, err)

	failure, ok := sm.FirstFailure()
	require.True(t, ok)
	require.Equal(t, "target-id", failure.TargetID)
	require.Equal(t, logstream.FailureType_FAILURE_TYPE_NONZERO_EXIT, failure.FailureType)
	require.Contains(t, failure.Error, "RUN bad")
	require.Contains(t, failure.Error, "Exit code 42")
	require.NotContains(t, failure.Error, "RUN worse")
}

func TestFirstFailureIgnoresCancellationOnlyVertexError(t *testing.T) {
	t.Parallel()

	sm := New(logbus.New())
	completed := time.Now()

	err := sm.handleBuildkitStatus(&client.SolveStatus{
		Vertexes: []*client.Vertex{
			{
				Digest:    digest.FromString("canceled"),
				Name:      (&vertexmeta.VertexMeta{TargetID: "target-id", TargetName: "+target"}).ToVertexPrefix() + "RUN bad",
				Completed: &completed,
				Error:     `process "bad" did not complete successfully: exit code: 137: context canceled: context canceled`,
			},
		},
	})
	require.NoError(t, err)

	_, ok := sm.FirstFailure()
	require.False(t, ok)
}

func TestFirstFailureErrorWrapsCause(t *testing.T) {
	t.Parallel()

	cause := context.Canceled
	err := NewFirstFailureError(cause, FirstFailure{
		Error: "first failure",
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "first failure", err.Error())

	failureErr, ok := AsFirstFailureError(err)
	require.True(t, ok)
	require.Equal(t, "first failure", failureErr.Failure.Error)
}

func TestNewFirstFailureErrorReturnsCauseWithoutFailureMessage(t *testing.T) {
	t.Parallel()

	cause := context.Canceled
	err := NewFirstFailureError(cause, FirstFailure{})

	require.ErrorIs(t, err, context.Canceled)
	require.NotContains(t, err.Error(), "build failed in target")
}
