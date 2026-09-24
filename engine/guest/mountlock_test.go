package guest

import (
	"sync"
	"testing"
	"time"
)

// Two steps sharing a cache do not use it at once.
//
// `CACHE --sharing=locked` is the default and the only mode this engine accepts
// - it refuses `shared` and `private` with a comment saying that accepting them
// "while providing locked" would be answering a question about concurrency with
// a guess. It was not providing locked: the only lock in the guest is per
// *handle*, so two steps naming one cache id ran into it simultaneously (E427).
//
// That is an option accepted and not provided, which this project refuses on
// principle - the author wrote nothing and got the default, and the default is a
// promise.
func TestTwoStepsSharingACacheDoNotUseItAtOnce(t *testing.T) {
	t.Parallel()

	var l mountLocks

	first := l.hold([]Mount{{ID: "m2", Target: "/root/.m2", Exclusive: true}})

	got := make(chan struct{})

	go func() {
		second := l.hold([]Mount{{ID: "m2", Target: "/root/.m2", Exclusive: true}})
		close(got)
		second()
	}()

	select {
	case <-got:
		t.Fatal("a second step took a cache the first was holding")
	case <-time.After(50 * time.Millisecond):
	}

	first()

	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("releasing the cache did not let the waiting step in")
	}
}

// Different caches do not wait for each other.
//
// The lock is per id, not a single lock over all mounts: a build whose steps use
// unrelated caches would otherwise serialise for no reason, which is a real cost
// paid for nothing.
func TestDifferentCachesDoNotWaitForEachOther(t *testing.T) {
	t.Parallel()

	var l mountLocks

	release := l.hold([]Mount{{ID: "m2", Exclusive: true}})
	defer release()

	done := make(chan struct{})

	go func() {
		l.hold([]Mount{{ID: "cargo", Exclusive: true}})()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a step waited for a cache it does not use")
	}
}

// Two steps needing the same two caches cannot deadlock.
//
// Acquired in a fixed order - sorted by id - so a step wanting {A,B} and one
// wanting {B,A} cannot each hold what the other needs. The classic hold-and-wait
// deadlock, avoided by ordering rather than by hoping the declaration order
// agrees.
func TestTwoStepsNeedingTwoCachesCannotDeadlock(t *testing.T) {
	t.Parallel()

	var (
		l    mountLocks
		wg   sync.WaitGroup
		done = make(chan struct{})
	)

	for _, order := range [][]Mount{
		{{ID: "a", Exclusive: true}, {ID: "b", Exclusive: true}},
		{{ID: "b", Exclusive: true}, {ID: "a", Exclusive: true}},
	} {
		wg.Add(1)

		go func(ms []Mount) {
			defer wg.Done()

			for range 50 {
				l.hold(ms)()
			}
		}(order)
	}

	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("two steps deadlocked over two caches")
	}
}

// A secret is not a cache and is not serialised.
//
// Every step has its own secret file staged for it, so there is nothing shared
// to wait for - and making steps queue on a credential's name would serialise a
// build for a resource that does not exist.
func TestASecretDoesNotSerialiseSteps(t *testing.T) {
	t.Parallel()

	var l mountLocks

	release := l.hold([]Mount{{ID: "token", Secret: "x"}})
	defer release()

	done := make(chan struct{})

	go func() {
		l.hold([]Mount{{ID: "token", Secret: "x"}})()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("two steps queued on a secret, which is staged per step")
	}
}

// The order caches are taken in is fixed, deduplicated, and excludes secrets.
//
// Asserted directly rather than inferred from a deadlock that did not happen:
// the sweep deleted the sort and the racing test passed anyway, because two
// goroutines failing to interleave badly is not evidence that they cannot.
func TestTheOrderCachesAreTakenInIsFixed(t *testing.T) {
	t.Parallel()

	got := LockOrder([]Mount{
		{ID: "cargo", Exclusive: true},
		{ID: "m2", Exclusive: true},
		{ID: "cargo", Exclusive: true},
		{ID: "token", Secret: "x", Exclusive: true},
		{Target: "/no-id", Exclusive: true},
		{ID: "npm"},
	})

	want := []string{"cargo", "m2"}

	if len(got) != len(want) {
		t.Fatalf("takes %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("takes %v, want %v - sorted, so two steps wanting the same"+
				" two caches cannot each hold what the other needs", got, want)
		}
	}

	// Reversed input, same order out. This is the property; the deadlock test
	// below is the belt.
	rev := LockOrder([]Mount{{ID: "m2", Exclusive: true}, {ID: "cargo", Exclusive: true}})
	if rev[0] != "cargo" {
		t.Errorf("declaration order reached the lock order: %v", rev)
	}
}

// A shared cache does not serialise steps.
//
// `--sharing=shared` says several steps may use one directory at once and the
// tools inside are trusted to cope - which npm and cargo do with their own
// locks. A lock that ignored the mode would provide `locked` under both names,
// which is the failure E427 fixed in the other direction (E432).
func TestASharedCacheDoesNotSerialiseSteps(t *testing.T) {
	t.Parallel()

	var l mountLocks

	release := l.hold([]Mount{{ID: "npm", Exclusive: false}})
	defer release()

	done := make(chan struct{})

	go func() {
		l.hold([]Mount{{ID: "npm", Exclusive: false}})()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("two steps queued on a cache declared --sharing=shared")
	}
}
