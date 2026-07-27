package unbounded

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// Two rules for the synctest bubbles in this package:
//
//   - end each bubble with synctest.Wait, so a completed construction's
//     MetaContext.Close has landed and its goroutines have gone. A bubble that ends with
//     goroutines still parked dies with "deadlock: blocked goroutines remain" — which is
//     also how the retention TestCache_CompletedEntriesDoNotRetainGoroutines guards
//     against will announce itself if Close ever regresses;
//   - assert outside the bubble, never inside. A failing require inside calls Goexit,
//     which skips the drain above and turns a clean red into that same panic.

const nilConstructorProbeEnv = "EARTHBUILD_UNBOUNDED_NIL_CONSTRUCTOR_PROBE"

// TestCache_NilConstructorDoesNotPanic asserts that a nil constructor is reported to the
// caller rather than dereferenced.
//
// RED. Do spawns construct in a goroutine, so a nil constructor is an uncatchable
// SIGSEGV that takes the whole process down — hence the child-process harness: an
// in-process assertion would kill the test binary before any other test could run.
// Reachable from ordinary code because a cancelled construction evicts its own entry, so
// "the entry is always there by the time I call Do" is not something a caller can rely on.
func TestCache_NilConstructorDoesNotPanic(t *testing.T) {
	if os.Getenv(nilConstructorProbeEnv) == "1" {
		// Child process. This is the code under test.
		_, err := NewCache[string, string]().Do(context.Background(), "missing", nil)
		if err == nil {
			os.Exit(3) // A nil constructor cannot legitimately have succeeded.
		}

		os.Exit(0)
	}

	t.Parallel()

	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(), nilConstructorProbeEnv+"=1")

	out, err := cmd.CombinedOutput()

	require.NoError(t, err, "Do with a nil constructor must return an error, not crash:\n%s", out)
	require.NotContains(t, string(out), "nil pointer dereference")
}

// TestCache_LiveContextDoesNotInheritCancellation asserts that a caller whose own context
// is alive never receives another caller's cancellation.
//
// RED. A late caller that arrives after the entry's MetaContext is already done gets its
// MetaContext.Add rejected — Do discards that error and attaches to the doomed
// construction anyway, so it is handed context.Canceled with a perfectly live context.
// In a build this shows up as one target's cancellation poisoning an unrelated target
// that happens to share a git URL or an image ref.
func TestCache_LiveContextDoesNotInheritCancellation(t *testing.T) {
	var (
		firstErr        error
		lateErr         error
		lateVal         string
		lateConstructed atomic.Bool
	)

	synctest.Test(t, func(t *testing.T) {
		cache := NewCache[string, string]()

		firstCtx, cancelFirst := context.WithCancel(context.Background())
		lateCtx, cancelLate := context.WithCancel(context.Background())

		var (
			wg           sync.WaitGroup
			sawCancel    = make(chan struct{})
			releaseFirst = make(chan struct{})
		)

		wg.Go(func() {
			_, firstErr = cache.Do(firstCtx, "k", func(ctx context.Context, _ string) (string, error) {
				<-ctx.Done()
				close(sawCancel)
				<-releaseFirst

				return "", ctx.Err()
			})
		})

		synctest.Wait() // the first constructor is blocked on ctx.Done

		cancelFirst()
		<-sawCancel // the MetaContext is done, but the entry is still in the store

		wg.Go(func() {
			lateVal, lateErr = cache.Do(lateCtx, "k", func(_ context.Context, key string) (string, error) {
				lateConstructed.Store(true)

				return "late-" + key, nil
			})
		})

		synctest.Wait() // the late caller has attached to the doomed entry

		close(releaseFirst)
		wg.Wait()

		cancelLate()
		synctest.Wait()
	})

	require.ErrorIs(t, firstErr, context.Canceled)

	require.NoError(t, lateErr, "a caller with a live context must not inherit someone else's cancellation")
	require.True(t, lateConstructed.Load(), "the late caller's constructor must run")
	require.Equal(t, "late-k", lateVal)
}

// TestCache_CompletedEntriesDoNotRetainGoroutines asserts that a finished construction
// releases the machinery that kept it alive.
//
// RED. metacontext.New starts monitor(), and each Add starts a watcher; both block until
// every sub-context is cancelled, and both hold the MetaContext themselves — so
// construct's `e.metaCtx.Store(nil) // ...allow GC of underlying sub-contexts` collects
// nothing. Measured at 2 retained goroutines per cached entry, for the life of the
// process. Deliberately phrased as observable behaviour rather than against a specific
// API, so it stays honest whichever way MetaContext grows a release path.
//
// Not parallel: it counts goroutines.
func TestCache_CompletedEntriesDoNotRetainGoroutines(t *testing.T) {
	const entries = 100

	baseline := settledGoroutines()

	cache := NewCache[int, int]()
	ctx := context.Background() // never cancelled, as in a long-running build

	var wg sync.WaitGroup

	for i := range entries {
		wg.Go(func() {
			_, err := cache.Do(ctx, i, func(_ context.Context, key int) (int, error) {
				return key * 2, nil
			})
			assertNoError(t, err)
		})
	}

	wg.Wait()

	retained := settledGoroutines() - baseline

	require.Less(t, retained, entries/10,
		"%d completed entries retained %d goroutines; a finished construction must release its MetaContext",
		entries, retained)
}

// assertNoError records a failure without Goexit, for use off the test goroutine.
func assertNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// settledGoroutines waits for transient goroutines to exit before counting, so the result
// reflects retention rather than in-flight work.
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

// TestCache_NonCanceledErrorsAreCached pins the asymmetry that makes the context.Canceled
// eviction in construct meaningful: every other error is a permanent answer.
func TestCache_NonCanceledErrorsAreCached(t *testing.T) {
	t.Parallel()

	cache := NewCache[string, int]()
	ctx := t.Context()
	wantErr := errors.New("boom")

	var calls atomic.Int32

	constructor := func(_ context.Context, _ string) (int, error) {
		calls.Add(1)

		return 7, wantErr
	}

	v1, err1 := cache.Do(ctx, "k", constructor)
	v2, err2 := cache.Do(ctx, "k", constructor)

	require.ErrorIs(t, err1, wantErr)
	require.ErrorIs(t, err2, wantErr)
	require.Equal(t, 7, v1)
	require.Equal(t, 7, v2, "the failed value is cached verbatim, zero or not")
	require.Equal(t, int32(1), calls.Load(), "a non-cancellation error must be cached")
}

// TestCache_WrappedContextCanceledIsNotCached checks that the eviction test is errors.Is
// and not equality — constructors wrap.
func TestCache_WrappedContextCanceledIsNotCached(t *testing.T) {
	t.Parallel()

	cache := NewCache[string, string]()
	ctx := t.Context()

	_, err := cache.Do(ctx, "k", func(_ context.Context, _ string) (string, error) {
		return "", fmt.Errorf("resolve git project: %w", context.Canceled)
	})
	require.ErrorIs(t, err, context.Canceled)

	got, err := cache.Do(ctx, "k", func(_ context.Context, _ string) (string, error) {
		return "second chance", nil
	})
	require.NoError(t, err)
	require.Equal(t, "second chance", got, "a wrapped cancellation must be evicted just like a bare one")
}

// TestCache_DeleteEntryIdentity pins the single-deleter invariant documented on
// deleteEntry: deleting by key is only safe while an entry can be evicted by nothing but
// its own construct goroutine. Currently green — it is a guard for whoever adds the
// second deletion path.
func TestCache_DeleteEntryIdentity(t *testing.T) {
	var (
		doErr            error
		addDuringBuild   error
		addAfterEviction error
		got              string
		getErr           error
		constructorRan   atomic.Bool
	)

	synctest.Test(t, func(t *testing.T) {
		cache := NewCache[string, string]()

		ctx := context.Background()

		var (
			wg      sync.WaitGroup
			release = make(chan struct{})
		)

		wg.Go(func() {
			_, doErr = cache.Do(ctx, "k", func(context.Context, string) (string, error) {
				<-release

				return "", context.Canceled
			})
		})

		synctest.Wait()

		// The entry is present for the whole of its construction, so nothing can slip a
		// second entry in underneath it.
		addDuringBuild = cache.Add("k", "usurper")

		close(release)
		wg.Wait()

		// The cancelled construct evicted its own entry, freeing the key...
		addAfterEviction = cache.Add("k", "survivor")

		// ...and no straggler evicts the replacement afterwards.
		got, getErr = cache.Do(ctx, "k", func(context.Context, string) (string, error) {
			constructorRan.Store(true)

			return "", errors.New("constructor must not run: the added value was evicted")
		})

		synctest.Wait()
	})

	require.ErrorIs(t, doErr, context.Canceled)
	require.Error(t, addDuringBuild, "Add must refuse while construction is in flight")
	require.NoError(t, addAfterEviction)
	require.NoError(t, getErr)
	require.False(t, constructorRan.Load())
	require.Equal(t, "survivor", got)
}

// TestCache_DistinctKeysConstructConcurrently checks that construction of one key does
// not block another — the store lock is only ever held around the map itself.
func TestCache_DistinctKeysConstructConcurrently(t *testing.T) {
	const keys = 8

	var (
		results = make([]int, keys)
		errs    = make([]error, keys)
	)

	synctest.Test(t, func(t *testing.T) {
		cache := NewCache[int, int]()

		ctx := context.Background()

		var (
			wg      sync.WaitGroup
			entered sync.WaitGroup
			release = make(chan struct{})
		)

		entered.Add(keys)

		for i := range keys {
			wg.Go(func() {
				results[i], errs[i] = cache.Do(ctx, i, func(_ context.Context, key int) (int, error) {
					entered.Done()
					<-release // every constructor must reach here before any may return

					return key * 3, nil
				})
			})
		}

		entered.Wait() // deadlocks if construction of distinct keys is serialised
		close(release)
		wg.Wait()
		synctest.Wait()
	})

	for i := range keys {
		require.NoError(t, errs[i])
		require.Equal(t, i*3, results[i])
	}
}
