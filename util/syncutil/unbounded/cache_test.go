package unbounded

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCache_Do(t *testing.T) {
	t.Parallel()

	t.Run("concurrent callers execute constructor once", func(t *testing.T) {
		t.Parallel()

		cache := NewCache[string, int]()

		var callCount atomic.Int32

		constructor := func(_ context.Context, key string) (int, error) {
			if key == "err" {
				return 0, errors.New("err")
			}

			callCount.Add(1)

			time.Sleep(50 * time.Millisecond)

			return len(key), nil
		}

		ctx := t.Context()

		// Concurrent calls for the same key should only execute constructor once.
		const numGoroutines = 10

		var (
			wg      sync.WaitGroup
			results = make([]int, numGoroutines)
			errs    = make([]error, numGoroutines)
		)

		for i := range numGoroutines {
			wg.Go(func() {
				res, err := cache.Do(ctx, "hello", constructor)

				results[i] = res
				errs[i] = err
			})
		}

		wg.Wait()

		require.Equal(t, int32(1), callCount.Load())

		for i := range numGoroutines {
			require.NoError(t, errs[i])
			require.Equal(t, 5, results[i])
		}
	})

	t.Run("partial context cancellation does not abort construction", func(t *testing.T) {
		t.Parallel()

		cache := NewCache[string, string]()

		ctx1, cancel1 := context.WithCancel(t.Context())
		ctx2, cancel2 := context.WithCancel(t.Context())
		ctx3, cancel3 := context.WithCancel(t.Context())

		defer cancel1()
		defer cancel2()
		defer cancel3()

		constructStarted := make(chan struct{})
		constructCanFinish := make(chan struct{})

		const want = "constructed-key1"

		var (
			wg      sync.WaitGroup
			ctxs    = []context.Context{ctx1, ctx2, ctx3}
			results = make([]string, 3)
			errs    = make([]error, 3)
		)

		// First caller starts constructor
		wg.Go(func() {
			res, err := cache.Do(ctx1, "key1", func(ctx context.Context, key string) (string, error) {
				close(constructStarted)

				<-constructCanFinish

				require.NoError(t, ctx.Err())

				return "constructed-" + key, nil
			})

			results[0], errs[0] = res, err
		})

		<-constructStarted

		// Callers 2 and 3 join
		for i := 1; i < 3; i++ {
			wg.Go(func() {
				res, err := cache.Do(ctxs[i], "key1", nil)

				results[i], errs[i] = res, err
			})
		}

		time.Sleep(20 * time.Millisecond)

		cancel1()
		cancel2()

		time.Sleep(20 * time.Millisecond)

		close(constructCanFinish)

		wg.Wait()

		for i := range 3 {
			require.NoError(t, errs[i])
			require.Equal(t, want, results[i])
		}
	})

	t.Run("all contexts canceled aborts construction", func(t *testing.T) {
		t.Parallel()

		cache := NewCache[string, string]()

		ctx1, cancel1 := context.WithCancel(t.Context())
		ctx2, cancel2 := context.WithCancel(t.Context())

		defer cancel1()
		defer cancel2()

		constructStarted := make(chan struct{})

		var (
			wg   sync.WaitGroup
			ctxs = []context.Context{ctx1, ctx2}
			errs = make([]error, 2)
		)

		wg.Go(func() {
			_, errs[0] = cache.Do(ctxs[0], "key1", func(ctx context.Context, _ string) (string, error) {
				close(constructStarted)

				<-ctx.Done()

				return "", ctx.Err()
			})
		})

		<-constructStarted

		wg.Go(func() {
			_, errs[1] = cache.Do(ctxs[1], "key1", nil)
		})

		time.Sleep(20 * time.Millisecond)

		cancel1()
		cancel2()

		wg.Wait()

		for _, err := range errs {
			require.ErrorIs(t, err, context.Canceled)
		}
	})
}

func TestCache_Add(t *testing.T) {
	t.Parallel()

	cache := NewCache[string, string]()

	ctx := t.Context()

	const want = "v1"

	err := cache.Add("k1", want)
	require.NoError(t, err)

	// Adding duplicate key should return error
	err = cache.Add("k1", "v2")
	require.Error(t, err)

	// Do should return the added value without calling constructor
	val, err := cache.Do(ctx, "k1", func(_ context.Context, _ string) (string, error) {
		t.Fatal("constructor should not be called")

		return "", nil
	})

	require.NoError(t, err)
	require.Equal(t, want, val)
}

func TestCache_ContextCanceled(t *testing.T) {
	t.Parallel()

	cache := NewCache[string, string]()

	// First call returns context.Canceled error
	ctx := t.Context()

	_, err := cache.Do(ctx, "k1", func(_ context.Context, _ string) (string, error) {
		return "", context.Canceled
	})

	require.ErrorIs(t, err, context.Canceled)

	// Second call should retry constructor because context.Canceled is not cached
	var called bool

	const want = "success"

	val, err := cache.Do(ctx, "k1", func(_ context.Context, _ string) (string, error) {
		called = true

		return want, nil
	})

	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, want, val)
}

func BenchmarkCache_Do_Hit(b *testing.B) {
	cache := NewCache[string, int]()
	ctx := b.Context()
	_, _ = cache.Do(ctx, "key", func(_ context.Context, _ string) (int, error) {
		return 42, nil
	})

	b.ReportAllocs()

	for b.Loop() {
		_, _ = cache.Do(ctx, "key", nil)
	}
}

func BenchmarkCache_Do_ConcurrentHits(b *testing.B) {
	cache := NewCache[string, int]()
	ctx := b.Context()
	_, _ = cache.Do(ctx, "key", func(_ context.Context, _ string) (int, error) {
		return 42, nil
	})

	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cache.Do(ctx, "key", nil)
		}
	})
}
