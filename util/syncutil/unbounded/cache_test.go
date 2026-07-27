package unbounded

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

// mustNotConstruct is for callers that are expected to join an existing construction. A
// nil constructor would be shorter, but Do rejects that outright — and it used to be a
// process-killing panic, since construct runs on a goroutine. This fails loudly and
// legibly instead, on the test goroutine, if the join is mistimed.
func mustNotConstruct(constructed *atomic.Bool) Constructor[string, string] {
	return func(_ context.Context, key string) (string, error) {
		constructed.Store(true)

		return "", fmt.Errorf("constructor unexpectedly ran for key %q", key)
	}
}

func TestCache_Do(t *testing.T) {
	t.Parallel()

	t.Run("concurrent callers execute constructor once", func(t *testing.T) {
		t.Parallel()

		// Concurrent calls for the same key should only execute constructor once.
		const numGoroutines = 10

		var (
			callCount atomic.Int32
			results   = make([]int, numGoroutines)
			errs      = make([]error, numGoroutines)
		)

		synctest.Test(t, func(t *testing.T) {
			cache := NewCache[string, int]()

			release := make(chan struct{})

			constructor := func(_ context.Context, key string) (int, error) {
				callCount.Add(1)
				<-release // hold the construction open until every caller has joined

				return len(key), nil
			}

			var wg sync.WaitGroup

			for i := range numGoroutines {
				wg.Go(func() {
					results[i], errs[i] = cache.Do(context.Background(), "hello", constructor)
				})
			}

			// Every caller is now parked on one in-flight construction. That is the
			// property under test, and it is the thing a sleep could only ever
			// approximate — from outside, a joined caller and a caller that has not
			// arrived yet look identical.
			synctest.Wait()

			close(release)
			wg.Wait()
			synctest.Wait()
		})

		require.Equal(t, int32(1), callCount.Load())

		for i := range numGoroutines {
			require.NoError(t, errs[i])
			require.Equal(t, 5, results[i])
		}
	})

	t.Run("partial context cancellation does not abort construction", func(t *testing.T) {
		t.Parallel()

		const want = "constructed-key1"

		var (
			results           = make([]string, 3)
			errs              = make([]error, 3)
			joinerConstructed atomic.Bool
			sharedCtxErr      error
		)

		synctest.Test(t, func(t *testing.T) {
			cache := NewCache[string, string]()

			ctx1, cancel1 := context.WithCancel(context.Background())
			ctx2, cancel2 := context.WithCancel(context.Background())
			ctx3, cancel3 := context.WithCancel(context.Background())

			defer cancel3()

			constructCanFinish := make(chan struct{})

			var (
				wg   sync.WaitGroup
				ctxs = []context.Context{ctx1, ctx2, ctx3}
			)

			// First caller starts constructor
			wg.Go(func() {
				results[0], errs[0] = cache.Do(ctx1, "key1", func(ctx context.Context, key string) (string, error) {
					<-constructCanFinish

					// Recorded, not asserted: this runs off the test goroutine, where
					// require's FailNow is undefined behaviour.
					sharedCtxErr = ctx.Err()

					return "constructed-" + key, nil
				})
			})

			synctest.Wait() // the constructor is running

			// Callers 2 and 3 join
			for i := 1; i < 3; i++ {
				wg.Go(func() {
					results[i], errs[i] = cache.Do(ctxs[i], "key1", mustNotConstruct(&joinerConstructed))
				})
			}

			synctest.Wait() // callers 2 and 3 have joined the in-flight construction

			cancel1()
			cancel2()

			synctest.Wait() // both cancellations have reached the shared MetaContext

			close(constructCanFinish)
			wg.Wait()
			synctest.Wait()
		})

		require.NoError(t, sharedCtxErr, "ctx3 is still live, so the shared context must not be done")
		require.False(t, joinerConstructed.Load(), "callers 2 and 3 must join the in-flight construction")

		for i := range 3 {
			require.NoError(t, errs[i])
			require.Equal(t, want, results[i])
		}
	})

	t.Run("all contexts canceled aborts construction", func(t *testing.T) {
		t.Parallel()

		var (
			errs              = make([]error, 2)
			joinerConstructed atomic.Bool
		)

		synctest.Test(t, func(t *testing.T) {
			cache := NewCache[string, string]()

			ctx1, cancel1 := context.WithCancel(context.Background())
			ctx2, cancel2 := context.WithCancel(context.Background())

			var wg sync.WaitGroup

			wg.Go(func() {
				_, errs[0] = cache.Do(ctx1, "key1", func(ctx context.Context, _ string) (string, error) {
					<-ctx.Done()

					return "", ctx.Err()
				})
			})

			synctest.Wait() // the constructor is blocked on the shared context

			wg.Go(func() {
				_, errs[1] = cache.Do(ctx2, "key1", mustNotConstruct(&joinerConstructed))
			})

			synctest.Wait() // caller 2 has joined, so its cancellation counts too

			cancel1()
			cancel2()

			wg.Wait()
			synctest.Wait()
		})

		require.False(t, joinerConstructed.Load(), "caller 2 must join rather than start its own construction")

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
	var constructed atomic.Bool

	val, err := cache.Do(ctx, "k1", mustNotConstruct(&constructed))

	require.NoError(t, err)
	require.False(t, constructed.Load(), "constructor should not be called")
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

// benchConstructor is hoisted so the func value is created once: a literal in the loop
// would be measured as an allocation that the cache did not make.
var benchConstructor Constructor[string, int] = func(_ context.Context, _ string) (int, error) {
	return 42, nil
}

func BenchmarkCache_Do_Hit(b *testing.B) {
	cache := NewCache[string, int]()
	ctx := b.Context()
	_, _ = cache.Do(ctx, "key", benchConstructor)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = cache.Do(ctx, "key", benchConstructor)
	}
}

func BenchmarkCache_Do_ConcurrentHits(b *testing.B) {
	cache := NewCache[string, int]()
	ctx := b.Context()
	_, _ = cache.Do(ctx, "key", benchConstructor)

	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cache.Do(ctx, "key", benchConstructor)
		}
	})
}
