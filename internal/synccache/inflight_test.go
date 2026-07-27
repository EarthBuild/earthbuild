package synccache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInflight(t *testing.T) {
	t.Parallel()

	t.Run("single context done cancels shared load", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		execCtx, _ := newInflight(ctx)

		require.NoError(t, execCtx.Err())

		cancel()
		<-execCtx.Done()

		require.ErrorIs(t, execCtx.Err(), context.Canceled)
	})

	t.Run("cancels only when all contexts are done", func(t *testing.T) {
		t.Parallel()

		ctx1, cancel1 := context.WithCancel(t.Context())
		ctx2, cancel2 := context.WithCancel(t.Context())

		execCtx, inf := newInflight(ctx1)
		err := inf.add(ctx2)
		require.NoError(t, err)

		cancel1()

		select {
		case <-execCtx.Done():
			t.Fatal("shared load done prematurely; ctx2 is still active")
		case <-time.After(50 * time.Millisecond):
		}

		require.NoError(t, execCtx.Err())

		cancel2()
		<-execCtx.Done()

		require.ErrorIs(t, execCtx.Err(), context.Canceled)
	})

	t.Run("add to done context fails", func(t *testing.T) {
		t.Parallel()

		ctx1, cancel1 := context.WithCancel(t.Context())
		execCtx, inf := newInflight(ctx1)

		cancel1()
		<-execCtx.Done()

		ctx2, cancel2 := context.WithCancel(t.Context())
		defer cancel2()

		err := inf.add(ctx2)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("add already-canceled context while active fails", func(t *testing.T) {
		t.Parallel()

		ctx1, cancel1 := context.WithCancel(t.Context())
		defer cancel1()

		_, inf := newInflight(ctx1)

		ctx2, cancel2 := context.WithCancel(t.Context())
		cancel2()

		err := inf.add(ctx2)
		require.ErrorIs(t, err, context.Canceled, "adding an already-canceled context must fail immediately")
	})

	t.Run("add after close returns errClosed", func(t *testing.T) {
		t.Parallel()

		ctx1, cancel1 := context.WithCancel(t.Context())
		defer cancel1()

		_, inf := newInflight(ctx1)
		inf.close()

		ctx2, cancel2 := context.WithCancel(t.Context())
		defer cancel2()

		err := inf.add(ctx2)
		require.ErrorIs(t, err, errClosed)
	})
}
