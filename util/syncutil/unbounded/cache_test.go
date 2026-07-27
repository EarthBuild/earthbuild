package unbounded

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func mustNotLoad[K comparable, V any](t *testing.T) Loader[K, V] {
	t.Helper()

	return func(_ context.Context, key K) (V, error) {
		t.Errorf("loader unexpectedly ran for key %v", key)

		var zero V

		return zero, fmt.Errorf("loader unexpectedly ran for key %v", key)
	}
}

func TestCache_Loader(t *testing.T) {
	t.Parallel()

	t.Run("nil loader returns error", func(t *testing.T) {
		t.Parallel()

		_, err := NewCache[string, string]().Load(t.Context(), "missing")
		require.Error(t, err)
		require.Equal(t, "cache: load key missing: loader is required", err.Error())
	})

	t.Run("sets default loader on NewCache", func(t *testing.T) {
		t.Parallel()

		defaultLoader := func(_ context.Context, key string) (string, error) {
			return "loaded-" + key, nil
		}

		cache := NewCache(defaultLoader)

		val, err := cache.Load(t.Context(), "hello")
		require.NoError(t, err)
		require.Equal(t, "loaded-hello", val)
	})

	t.Run("allows call-site loader override", func(t *testing.T) {
		t.Parallel()

		defaultLoader := func(_ context.Context, key string) (string, error) {
			return "default-" + key, nil
		}

		overrideLoader := func(_ context.Context, key string) (string, error) {
			return "override-" + key, nil
		}

		cache := NewCache(defaultLoader)
		ctx := t.Context()

		val, err := cache.Load(ctx, "hello", overrideLoader)
		require.NoError(t, err)
		require.Equal(t, "override-hello", val)
	})
}

func TestCache_Load(t *testing.T) {
	t.Parallel()

	t.Run("concurrent callers execute loader once", func(t *testing.T) {
		t.Parallel()

		const numGoroutines = 10

		var (
			callCount atomic.Int32
			results   = make([]int, numGoroutines)
			errs      = make([]error, numGoroutines)
		)

		synctest.Test(t, func(t *testing.T) {
			release := make(chan struct{})

			loader := func(_ context.Context, key string) (int, error) {
				callCount.Add(1)
				<-release

				return len(key), nil
			}

			cache := NewCache(loader)

			var wg sync.WaitGroup

			for i := range numGoroutines {
				wg.Go(func() {
					results[i], errs[i] = cache.Load(t.Context(), "hello")
				})
			}

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

	t.Run("distinct keys load concurrently", func(t *testing.T) {
		t.Parallel()

		const keys = 8

		var (
			results = make([]int, keys)
			errs    = make([]error, keys)
		)

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var (
				wg, entered sync.WaitGroup
				release     = make(chan struct{})
			)

			entered.Add(keys)

			cache := NewCache(func(_ context.Context, key int) (int, error) {
				entered.Done()
				<-release

				return key * 3, nil
			})

			for i := range keys {
				wg.Go(func() {
					results[i], errs[i] = cache.Load(ctx, i)
				})
			}

			entered.Wait()
			close(release)
			wg.Wait()
			synctest.Wait()
		})

		for i := range keys {
			require.NoError(t, errs[i])
			require.Equal(t, i*3, results[i])
		}
	})

	t.Run("non-canceled errors are cached", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("boom")

		var calls atomic.Int32

		loader := func(_ context.Context, _ string) (int, error) {
			calls.Add(1)

			return 7, wantErr
		}

		cache := NewCache(loader)
		ctx := t.Context()

		v1, err1 := cache.Load(ctx, "k")
		v2, err2 := cache.Load(ctx, "k")

		require.ErrorIs(t, err1, wantErr)
		require.ErrorIs(t, err2, wantErr)
		require.Equal(t, 7, v1)
		require.Equal(t, 7, v2, "the failed value is cached verbatim, zero or not")
		require.Equal(t, int32(1), calls.Load(), "a non-cancellation error must be cached")
	})

	t.Run("error returns are prefixed with cache:", func(t *testing.T) {
		t.Parallel()

		cache := NewCache[string, string]()
		_, err := cache.Load(t.Context(), "k", func(_ context.Context, _ string) (string, error) {
			return "", context.Canceled
		})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, "cache: load key k: context canceled", err.Error())
	})
}

func TestCache_Load_ContextCanceled(t *testing.T) {
	t.Parallel()

	t.Run("partial context cancellation does not abort load", func(t *testing.T) {
		t.Parallel()

		const want = "loaded-key1"

		var (
			results      = make([]string, 3)
			errs         = make([]error, 3)
			sharedCtxErr error
		)

		synctest.Test(t, func(t *testing.T) {
			ctx1, cancel1 := context.WithCancel(t.Context())
			ctx2, cancel2 := context.WithCancel(t.Context())
			ctx3, cancel3 := context.WithCancel(t.Context())

			defer cancel3()

			loadCanFinish := make(chan struct{})

			cache := NewCache(func(ctx context.Context, key string) (string, error) {
				<-loadCanFinish

				sharedCtxErr = ctx.Err()

				return "loaded-" + key, nil
			})

			var (
				wg   sync.WaitGroup
				ctxs = []context.Context{ctx1, ctx2, ctx3}
			)

			wg.Go(func() {
				results[0], errs[0] = cache.Load(ctx1, "key1")
			})

			synctest.Wait()

			for i := 1; i < 3; i++ {
				wg.Go(func() {
					results[i], errs[i] = cache.Load(ctxs[i], "key1", mustNotLoad[string, string](t))
				})
			}

			synctest.Wait()

			cancel1()
			cancel2()

			synctest.Wait()

			close(loadCanFinish)
			wg.Wait()
			synctest.Wait()
		})

		require.NoError(t, sharedCtxErr, "ctx3 is still live, so the shared context must not be done")

		for i := range 3 {
			require.NoError(t, errs[i])
			require.Equal(t, want, results[i])
		}
	})

	t.Run("all contexts canceled aborts load", func(t *testing.T) {
		t.Parallel()

		errs := make([]error, 2)

		synctest.Test(t, func(t *testing.T) {
			ctx1, cancel1 := context.WithCancel(t.Context())
			ctx2, cancel2 := context.WithCancel(t.Context())

			cache := NewCache(func(ctx context.Context, _ string) (string, error) {
				<-ctx.Done()

				return "", ctx.Err()
			})

			var wg sync.WaitGroup

			wg.Go(func() {
				_, errs[0] = cache.Load(ctx1, "key1")
			})

			synctest.Wait()

			wg.Go(func() {
				_, errs[1] = cache.Load(ctx2, "key1", mustNotLoad[string, string](t))
			})

			synctest.Wait()

			cancel1()
			cancel2()

			wg.Wait()
			synctest.Wait()
		})

		for _, err := range errs {
			require.ErrorIs(t, err, context.Canceled)
		}
	})

	t.Run("bare context.Canceled is evicted", func(t *testing.T) {
		t.Parallel()

		cache := NewCache[string, string]()
		ctx := t.Context()

		_, err := cache.Load(ctx, "k1", func(_ context.Context, _ string) (string, error) {
			return "", context.Canceled
		})

		require.ErrorIs(t, err, context.Canceled)

		const want = "success"

		var called bool

		val, err := cache.Load(ctx, "k1", func(_ context.Context, _ string) (string, error) {
			called = true

			return want, nil
		})

		require.NoError(t, err)
		require.True(t, called)
		require.Equal(t, want, val)
	})

	t.Run("wrapped context.Canceled is evicted", func(t *testing.T) {
		t.Parallel()

		cache := NewCache[string, string]()
		ctx := t.Context()

		_, err := cache.Load(ctx, "k", func(_ context.Context, _ string) (string, error) {
			return "", fmt.Errorf("resolve git project: %w", context.Canceled)
		})
		require.ErrorIs(t, err, context.Canceled)

		got, err := cache.Load(ctx, "k", func(_ context.Context, _ string) (string, error) {
			return "second chance", nil
		})
		require.NoError(t, err)
		require.Equal(t, "second chance", got, "a wrapped cancellation must be evicted just like a bare one")
	})

	t.Run("live context does not inherit cancellation", func(t *testing.T) {
		t.Parallel()

		var (
			firstErr, lateErr error
			lateVal           string
			lateConstructed   atomic.Bool
		)

		synctest.Test(t, func(t *testing.T) {
			firstCtx, cancelFirst := context.WithCancel(t.Context())
			lateCtx, cancelLate := context.WithCancel(t.Context())

			var (
				wg           sync.WaitGroup
				sawCancel    = make(chan struct{})
				releaseFirst = make(chan struct{})
			)

			cache := NewCache(func(ctx context.Context, _ string) (string, error) {
				<-ctx.Done()
				close(sawCancel)
				<-releaseFirst

				return "", ctx.Err()
			})

			wg.Go(func() {
				_, firstErr = cache.Load(firstCtx, "k")
			})

			synctest.Wait()

			cancelFirst()
			<-sawCancel

			wg.Go(func() {
				lateVal, lateErr = cache.Load(lateCtx, "k", func(_ context.Context, key string) (string, error) {
					lateConstructed.Store(true)

					return "late-" + key, nil
				})
			})

			synctest.Wait()

			close(releaseFirst)
			wg.Wait()

			cancelLate()
			synctest.Wait()
		})

		require.ErrorIs(t, firstErr, context.Canceled)
		require.NoError(t, lateErr, "a caller with a live context must not inherit someone else's cancellation")
		require.True(t, lateConstructed.Load(), "the late caller's loader must run")
		require.Equal(t, "late-k", lateVal)
	})
}

func TestCache_Store(t *testing.T) {
	t.Parallel()

	t.Run("stores value and prevents duplicate store", func(t *testing.T) {
		t.Parallel()

		cache := NewCache[string, string]()
		ctx := t.Context()

		const want = "v1"

		err := cache.Store("k1", want)
		require.NoError(t, err)

		err = cache.Store("k1", "v2")
		require.ErrorIs(t, err, ErrAlreadyExists)
		require.EqualError(t, err, "cache: store key k1: already exists")

		val, err := cache.Load(ctx, "k1", mustNotLoad[string, string](t))

		require.NoError(t, err)
		require.Equal(t, want, val)
	})

	t.Run("refuses store while load in flight and allows replacement after eviction", func(t *testing.T) {
		t.Parallel()

		var (
			loadErr          error
			addDuringBuild   error
			addAfterEviction error
			got              string
			getErr           error
		)

		synctest.Test(t, func(t *testing.T) {
			ctx := t.Context()

			var (
				wg      sync.WaitGroup
				release = make(chan struct{})
			)

			cache := NewCache(func(context.Context, string) (string, error) {
				<-release

				return "", context.Canceled
			})

			wg.Go(func() {
				_, loadErr = cache.Load(ctx, "k")
			})

			synctest.Wait()

			addDuringBuild = cache.Store("k", "usurper")

			close(release)
			wg.Wait()

			addAfterEviction = cache.Store("k", "survivor")

			got, getErr = cache.Load(ctx, "k", mustNotLoad[string, string](t))

			synctest.Wait()
		})

		require.ErrorIs(t, loadErr, context.Canceled)
		require.ErrorIs(t, addDuringBuild, ErrAlreadyExists, "Store must refuse while load is in flight")
		require.NoError(t, addAfterEviction)
		require.NoError(t, getErr)
		require.Equal(t, "survivor", got)
	})
}

//nolint:paralleltest // Not parallel: it counts goroutines.
func TestCache_CompletedEntriesDoNotRetainGoroutines(t *testing.T) {
	const entries = 100

	baseline := settledGoroutines()

	cache := NewCache(func(_ context.Context, key int) (int, error) {
		return key * 2, nil
	})
	ctx := t.Context()

	var wg sync.WaitGroup

	for i := range entries {
		wg.Go(func() {
			_, err := cache.Load(ctx, i)
			require.NoError(t, err)
		})
	}

	wg.Wait()

	retained := settledGoroutines() - baseline

	require.Less(t, retained, entries/10,
		"%d completed entries retained %d goroutines; a finished load must release its MetaContext",
		entries, retained)
}

func settledGoroutines() int {
	var (
		prev  = -1
		count int
	)

	for range 50 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)

		count = runtime.NumGoroutine()
		if count == prev {
			break
		}

		prev = count
	}

	return count
}

var benchLoader Loader[string, int] = func(_ context.Context, _ string) (int, error) {
	return 42, nil
}

func BenchmarkCache_Load_Hit(b *testing.B) {
	cache := NewCache(benchLoader)
	ctx := b.Context()
	_, _ = cache.Load(ctx, "key")

	b.ReportAllocs()

	for b.Loop() {
		_, _ = cache.Load(ctx, "key")
	}
}

func BenchmarkCache_Load_ConcurrentHits(b *testing.B) {
	cache := NewCache(benchLoader)
	ctx := b.Context()
	_, _ = cache.Load(ctx, "key")

	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cache.Load(ctx, "key")
		}
	})
}
