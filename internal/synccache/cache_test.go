package synccache

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

func mustNotLoad[V any](t *testing.T) Loader[V] {
	t.Helper()

	return func(_ context.Context) (V, error) {
		t.Errorf("loader unexpectedly ran")

		var zero V

		return zero, errors.New("loader unexpectedly ran")
	}
}

func TestCache_Loader(t *testing.T) {
	t.Parallel()

	t.Run("nil loader returns error", func(t *testing.T) {
		t.Parallel()

		_, err := NewCache[string, string]().Load(t.Context(), "missing", nil)
		require.Error(t, err)
		require.Equal(t, "cache: load key missing: loader is required", err.Error())
	})

	t.Run("loader that exits prematurely unblocks waiters with error", func(t *testing.T) {
		t.Parallel()

		var (
			waiterErr error
			waiterVal string
		)

		synctest.Test(t, func(t *testing.T) {
			cache := NewCache[string, string]()
			loaderStarted := make(chan struct{})

			var wg sync.WaitGroup

			wg.Go(func() {
				_, _ = cache.Load(t.Context(), "k", func(context.Context) (string, error) {
					close(loaderStarted)
					runtime.Goexit()

					return "never", nil
				})
			})

			<-loaderStarted

			wg.Go(func() {
				waiterVal, waiterErr = cache.Load(t.Context(), "k", mustNotLoad[string](t))
			})

			synctest.Wait()
			wg.Wait()
			synctest.Wait()
		})

		require.Error(t, waiterErr)
		require.Equal(t, "cache: load key k: loader exited without returning a value", waiterErr.Error())
		require.Empty(t, waiterVal)
	})

	t.Run("loader that panics converts panic to error", func(t *testing.T) {
		t.Parallel()

		var (
			waiterErr error
			waiterVal string
		)

		synctest.Test(t, func(t *testing.T) {
			cache := NewCache[string, string]()
			loaderStarted := make(chan struct{})

			var wg sync.WaitGroup

			wg.Go(func() {
				_, _ = cache.Load(t.Context(), "k", func(context.Context) (string, error) {
					close(loaderStarted)
					panic("boom")
				})
			})

			<-loaderStarted

			wg.Go(func() {
				waiterVal, waiterErr = cache.Load(t.Context(), "k", mustNotLoad[string](t))
			})

			synctest.Wait()
			wg.Wait()
			synctest.Wait()
		})

		require.Error(t, waiterErr)
		require.Equal(t, "cache: load key k: loader panicked: boom", waiterErr.Error())
		require.Empty(t, waiterVal)
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

			loader := func(context.Context) (int, error) {
				callCount.Add(1)
				<-release

				return 5, nil
			}

			cache := NewCache[string, int]()

			var wg sync.WaitGroup

			for i := range numGoroutines {
				wg.Go(func() {
					results[i], errs[i] = cache.Load(t.Context(), "hello", loader)
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

			cache := NewCache[int, int]()

			for i := range keys {
				wg.Go(func() {
					results[i], errs[i] = cache.Load(ctx, i, func(context.Context) (int, error) {
						entered.Done()
						<-release

						return i * 3, nil
					})
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

		loader := func(_ context.Context) (int, error) {
			calls.Add(1)

			return 7, wantErr
		}

		cache := NewCache[string, int]()
		ctx := t.Context()

		v1, err1 := cache.Load(ctx, "k", loader)
		v2, err2 := cache.Load(ctx, "k", loader)

		require.ErrorIs(t, err1, wantErr)
		require.ErrorIs(t, err2, wantErr)
		require.Equal(t, 7, v1)
		require.Equal(t, 7, v2, "the failed value is cached verbatim, zero or not")
		require.Equal(t, int32(1), calls.Load(), "a non-cancellation error must be cached")
	})

	t.Run("error returns are prefixed with cache:", func(t *testing.T) {
		t.Parallel()

		cache := NewCache[string, string]()
		_, err := cache.Load(t.Context(), "k", func(_ context.Context) (string, error) {
			return "", context.Canceled
		})
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, "cache: load key k: context canceled", err.Error())
	})
}

func TestCache_Load_ContextCanceled(t *testing.T) {
	t.Parallel()

	t.Run("caller with already canceled context while load in flight returns error", func(t *testing.T) {
		t.Parallel()

		var (
			firstErr, secondErr error
			secondFinished      atomic.Bool
		)

		synctest.Test(t, func(t *testing.T) {
			firstCtx, cancelFirst := context.WithCancel(t.Context())
			defer cancelFirst()

			cancCtx, cancelSecond := context.WithCancel(t.Context())
			cancelSecond() // pre-cancel second context

			loaderEntered := make(chan struct{})
			releaseLoader := make(chan struct{})

			loader := func(_ context.Context) (string, error) {
				close(loaderEntered)
				<-releaseLoader

				return "val", nil
			}

			cache := NewCache[string, string]()

			var wg sync.WaitGroup

			wg.Go(func() {
				_, firstErr = cache.Load(firstCtx, "k", loader)
			})

			<-loaderEntered

			wg.Go(func() {
				_, secondErr = cache.Load(cancCtx, "k", mustNotLoad[string](t))

				secondFinished.Store(true)
			})

			synctest.Wait()

			require.True(t, secondFinished.Load(), "already-canceled caller must not block on in-flight load")
			require.ErrorIs(t, secondErr, context.Canceled)

			close(releaseLoader)
			wg.Wait()
			synctest.Wait()
		})

		require.NoError(t, firstErr)
	})

	t.Run("caller context cancellation while waiting unblocks promptly", func(t *testing.T) {
		t.Parallel()

		var (
			firstErr, secondErr error
			secondUnblocked     atomic.Bool
		)

		synctest.Test(t, func(t *testing.T) {
			firstCtx, cancelFirst := context.WithCancel(t.Context())
			defer cancelFirst()

			secondCtx, cancelSecond := context.WithCancel(t.Context())

			loaderEntered := make(chan struct{})
			releaseLoader := make(chan struct{})

			loader := func(_ context.Context) (string, error) {
				close(loaderEntered)
				<-releaseLoader

				return "val", nil
			}

			cache := NewCache[string, string]()

			var wg sync.WaitGroup

			wg.Go(func() {
				_, firstErr = cache.Load(firstCtx, "k", loader)
			})

			<-loaderEntered

			wg.Go(func() {
				_, secondErr = cache.Load(secondCtx, "k", mustNotLoad[string](t))

				secondUnblocked.Store(true)
			})

			synctest.Wait()

			cancelSecond()

			synctest.Wait()

			require.True(t, secondUnblocked.Load(), "canceled caller must unblock promptly without waiting for loader")
			require.ErrorIs(t, secondErr, context.Canceled)

			close(releaseLoader)
			wg.Wait()
			synctest.Wait()
		})

		require.NoError(t, firstErr)
	})

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

			loader := func(ctx context.Context) (string, error) {
				<-loadCanFinish

				sharedCtxErr = ctx.Err()

				return "loaded-key1", nil
			}

			cache := NewCache[string, string]()

			var (
				wg   sync.WaitGroup
				ctxs = []context.Context{ctx1, ctx2, ctx3}
			)

			wg.Go(func() {
				results[0], errs[0] = cache.Load(ctx1, "key1", loader)
			})

			synctest.Wait()

			for i := 1; i < 3; i++ {
				wg.Go(func() {
					results[i], errs[i] = cache.Load(ctxs[i], "key1", mustNotLoad[string](t))
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
		require.ErrorIs(t, errs[0], context.Canceled)
		require.ErrorIs(t, errs[1], context.Canceled)
		require.NoError(t, errs[2])
		require.Equal(t, want, results[2])
	})

	t.Run("all contexts canceled aborts load", func(t *testing.T) {
		t.Parallel()

		errs := make([]error, 2)

		synctest.Test(t, func(t *testing.T) {
			ctx1, cancel1 := context.WithCancel(t.Context())
			ctx2, cancel2 := context.WithCancel(t.Context())

			loader := func(ctx context.Context) (string, error) {
				<-ctx.Done()

				return "", ctx.Err()
			}

			cache := NewCache[string, string]()

			var wg sync.WaitGroup

			wg.Go(func() {
				_, errs[0] = cache.Load(ctx1, "key1", loader)
			})

			synctest.Wait()

			wg.Go(func() {
				_, errs[1] = cache.Load(ctx2, "key1", mustNotLoad[string](t))
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

		_, err := cache.Load(ctx, "k1", func(_ context.Context) (string, error) {
			return "", context.Canceled
		})

		require.ErrorIs(t, err, context.Canceled)

		const want = "success"

		var called bool

		val, err := cache.Load(ctx, "k1", func(_ context.Context) (string, error) {
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

		_, err := cache.Load(ctx, "k", func(_ context.Context) (string, error) {
			return "", fmt.Errorf("resolve git project: %w", context.Canceled)
		})
		require.ErrorIs(t, err, context.Canceled)

		got, err := cache.Load(ctx, "k", func(_ context.Context) (string, error) {
			return "second chance", nil
		})
		require.NoError(t, err)
		require.Equal(t, "second chance", got, "a wrapped cancellation must be evicted just like a bare one")
	})

	t.Run("context.DeadlineExceeded is evicted", func(t *testing.T) {
		t.Parallel()

		cache := NewCache[string, string]()
		ctx := t.Context()

		_, err := cache.Load(ctx, "k", func(_ context.Context) (string, error) {
			return "", fmt.Errorf("get git meta: %w", context.DeadlineExceeded)
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)

		got, err := cache.Load(ctx, "k", func(_ context.Context) (string, error) {
			return "second chance", nil
		})
		require.NoError(t, err)
		require.Equal(t, "second chance", got,
			"a timed-out load must be retryable, not cached for the life of the build")
	})

	t.Run("already canceled caller does not orphan an uncancellable load", func(t *testing.T) {
		t.Parallel()

		var orphaned atomic.Bool

		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			loaderDone := make(chan struct{})

			cache := NewCache[string, string]()

			_, err := cache.Load(ctx, "k", func(execCtx context.Context) (string, error) {
				defer close(loaderDone)

				select {
				case <-execCtx.Done():
					return "", execCtx.Err()
				case <-time.After(time.Minute):
					orphaned.Store(true)

					return "orphan", nil
				}
			})
			require.ErrorIs(t, err, context.Canceled)

			<-loaderDone
		})

		require.False(t, orphaned.Load(),
			"a load whose only caller was already gone must still be cancelable")
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

			loader := func(ctx context.Context) (string, error) {
				<-ctx.Done()
				close(sawCancel)
				<-releaseFirst

				return "", ctx.Err()
			}

			cache := NewCache[string, string]()

			wg.Go(func() {
				_, firstErr = cache.Load(firstCtx, "k", loader)
			})

			synctest.Wait()

			cancelFirst()
			<-sawCancel

			wg.Go(func() {
				lateVal, lateErr = cache.Load(lateCtx, "k", func(_ context.Context) (string, error) {
					lateConstructed.Store(true)

					return "late-k", nil
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

		val, err := cache.Load(ctx, "k1", mustNotLoad[string](t))

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

			loader := func(context.Context) (string, error) {
				<-release

				return "", context.Canceled
			}

			cache := NewCache[string, string]()

			wg.Go(func() {
				_, loadErr = cache.Load(ctx, "k", loader)
			})

			synctest.Wait()

			addDuringBuild = cache.Store("k", "usurper")

			close(release)
			wg.Wait()

			addAfterEviction = cache.Store("k", "survivor")

			got, getErr = cache.Load(ctx, "k", mustNotLoad[string](t))

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

	cache := NewCache[int, int]()
	ctx := t.Context()

	var wg sync.WaitGroup

	for i := range entries {
		wg.Go(func() {
			_, err := cache.Load(ctx, i, func(_ context.Context) (int, error) {
				return i * 2, nil
			})
			require.NoError(t, err)
		})
	}

	wg.Wait()

	retained := settledGoroutines() - baseline

	require.Less(t, retained, entries/10,
		"%d completed entries retained %d goroutines; a finished load must release its context",
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

var benchLoader Loader[int] = func(_ context.Context) (int, error) {
	return 42, nil
}

func BenchmarkCache_Load_Hit(b *testing.B) {
	cache := NewCache[string, int]()
	ctx := b.Context()
	_, _ = cache.Load(ctx, "key", benchLoader)

	b.ReportAllocs()

	for b.Loop() {
		_, _ = cache.Load(ctx, "key", benchLoader)
	}
}

func BenchmarkCache_Load_ConcurrentHits(b *testing.B) {
	cache := NewCache[string, int]()
	ctx := b.Context()
	_, _ = cache.Load(ctx, "key", benchLoader)

	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cache.Load(ctx, "key", benchLoader)
		}
	})
}
