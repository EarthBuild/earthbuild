package core_test

import (
	"context"
	"maps"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

// memCache is the in-memory 𝔄 fake. It stays in the tree for the life of the
// project: the fakes are the fast test double, not scaffolding.
//
// **Locked**, because `ActionCache` is called concurrently and a bare map is a
// `fatal error: concurrent map writes` rather than a wrong answer. It was a
// bare map until a test ran six independent steps at once and the whole package
// died about one in five runs (E139) - a fake that violates the port's contract
// is a flake attributed to the engine.
type memCache struct {
	mu sync.Mutex
	m  map[core.Key]core.Entry
}

func newMemCache() *memCache { return &memCache{m: map[core.Key]core.Entry{}} }

func (c *memCache) Get(k core.Key) (core.Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.m[k]

	return e, ok
}

func (c *memCache) Put(k core.Key, e core.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.m[k] = e
}

// all is a copy of the entries, so a test can iterate without holding the lock
// or racing a build that is still running.
func (c *memCache) all() map[core.Key]core.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make(map[core.Key]core.Entry, len(c.m))
	maps.Copy(out, c.m)

	return out
}

// len is how many entries the fake holds, for tests that count them.
func (c *memCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.m)
}

// allBlobs is a 𝔅 fake that claims to hold everything.
type allBlobs struct{}

func (allBlobs) Has(ir.NodeID) bool { return true }

// noBlobs holds nothing, which is how a dangling cache entry is simulated.
type noBlobs struct{}

func (noBlobs) Has(ir.NodeID) bool { return false }

func chain(base *ir.Node, args ...string) *ir.Node {
	cur := base
	for _, a := range args {
		cur = &ir.Node{
			Op: ir.Op{Kind: ir.OpExec, Args: []string{a}}, Platform: amd64,
			Inputs: []*ir.Node{cur},
		}
	}

	return cur
}

func newSched(c core.ActionCache, b core.BlobStore, e core.Executor) *core.Scheduler {
	return &core.Scheduler{
		Workers:  []core.Worker{{ID: "w1", Platform: amd64, IsInvoker: true}},
		Executor: e,
		Cache:    c,
		Blobs:    b,
		Writer:   testStep,
	}
}

// TestSecondBuildIsAllHits is stage S1's core claim: a rebuild of an unchanged
// graph executes nothing.
func TestSecondBuildIsAllHits(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}
	g := &ir.Graph{Root: chain(img, "a", "b", "c")}

	cache := newMemCache()

	first := &sim.Executor{Seed: 1}
	_, err := newSched(cache, allBlobs{}, first).Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	second := &sim.Executor{Seed: 1}

	s := newSched(cache, allBlobs{}, second)
	_, err = s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if len(second.Log) != 0 {
		t.Errorf("rebuild executed %d steps, want 0", len(second.Log))
	}

	if s.Stats.Misses != 0 {
		t.Errorf("rebuild had %d misses, want 0", s.Stats.Misses)
	}
}

// TestEditInvalidatesDownstreamOnly checks the chain key's defining behaviour:
// editing a step invalidates that step and everything after it, and nothing
// before it.
//
// This is also the measurement that motivates observed-input caching. Under a
// chain key, an edit near the root invalidates the world even where nothing
// downstream could observe the change.
func TestEditInvalidatesDownstreamOnly(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}

	cache := newMemCache()

	warm := &sim.Executor{Seed: 1}
	_, err := newSched(cache, allBlobs{}, warm).Run(
		context.Background(), &ir.Graph{Root: chain(img, "a", "b", "c", "d")},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Edit the second step of four. Expect: image and "a" hit; "b" (edited),
	// "c" and "d" miss.
	edited := &sim.Executor{Seed: 1}

	s := newSched(cache, allBlobs{}, edited)
	_, err = s.Run(
		context.Background(), &ir.Graph{Root: chain(img, "a", "B", "c", "d")},
	)
	if err != nil {
		t.Fatal(err)
	}

	if s.Stats.Hits != 2 {
		t.Errorf("hits = %d, want 2 (the image and the step before the edit)", s.Stats.Hits)
	}

	if s.Stats.Misses != 3 {
		t.Errorf("misses = %d, want 3 (the edited step and its two successors)", s.Stats.Misses)
	}
}

// TestEnvIsInTheKey checks that ε reaches the key. A variable a step can
// observe but the key omits is a false cache hit, which is invariant I3 and the
// one failure a build system must never have.
func TestEnvIsInTheKey(t *testing.T) {
	t.Parallel()

	n1 := &ir.Node{
		Op:       ir.Op{Kind: ir.OpExec, Args: []string{"go build"}, Env: map[string]string{"GOFLAGS": "-race"}},
		Platform: amd64,
	}
	n2 := &ir.Node{
		Op:       ir.Op{Kind: ir.OpExec, Args: []string{"go build"}, Env: map[string]string{"GOFLAGS": ""}},
		Platform: amd64,
	}

	if core.DeriveChainKey(n1, nil, nil) == core.DeriveChainKey(n2, nil, nil) {
		t.Fatal("steps differing only in ε share a key: I3 violated")
	}
}

// TestPlatformIsInTheKey checks the same for π: the identical command for two
// architectures must not share a cache entry.
func TestPlatformIsInTheKey(t *testing.T) {
	t.Parallel()

	a := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testCommand}}, Platform: amd64}
	b := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testCommand}}, Platform: arm64}

	if core.DeriveChainKey(a, nil, nil) == core.DeriveChainKey(b, nil, nil) {
		t.Fatal("steps differing only in π share a key")
	}
}

// TestBaseIsInTheKey checks that a step over different resolved inputs derives
// a different key even though the node is identical.
func TestBaseIsInTheKey(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testCommand}}, Platform: amd64}

	var one, two ir.NodeID

	one[0], two[0] = 1, 2

	if core.DeriveChainKey(n, []ir.NodeID{one}, nil) == core.DeriveChainKey(n, []ir.NodeID{two}, nil) {
		t.Fatal("the same step over different bases shares a key")
	}
}

// TestPoisonedCacheIsSlowNeverWrong is E5c in miniature, and the invariant this
// whole design is built around: a poisoned cache may cost time. It may never
// cost correctness.
//
// Every fault below must degrade to a miss - not to an error, and not to using
// the entry. An error would fail this test as surely as a wrong answer, because
// the rule is degrade-to-miss, not degrade-to-crash (I4).
func TestPoisonedCacheIsSlowNeverWrong(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}
	g := &ir.Graph{Root: chain(img, "a", "b")}

	// The honest result, with no cache at all.
	clean := &sim.Executor{Seed: 3}
	_, err := newSched(nil, nil, clean).Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	want := clean.Log[len(clean.Log)-1].Node

	for _, tc := range []struct {
		name  string
		cache func() core.ActionCache
		blobs core.BlobStore
	}{
		{"entry claims a result that does not exist", func() core.ActionCache {
			c := newMemCache()
			for k := range warmKeys(t, g) {
				c.Put(k, core.Entry{Layer: ir.NodeID{0xff}, Writer: testStep})
			}

			return c
		}, noBlobs{}},

		{"entry is empty", func() core.ActionCache {
			c := newMemCache()
			for k := range warmKeys(t, g) {
				c.Put(k, core.Entry{Writer: testStep})
			}

			return c
		}, allBlobs{}},

		{"entry is from an unknown writer", func() core.ActionCache {
			c := newMemCache()
			for k := range warmKeys(t, g) {
				c.Put(k, core.Entry{Layer: ir.NodeID{0xaa}, Writer: "attacker"})
			}

			return c
		}, allBlobs{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exec := &sim.Executor{Seed: 3}

			s := newSched(tc.cache(), tc.blobs, exec)
			s.Trusted = map[string]bool{testStep: true}

			_, err := s.Run(context.Background(), g)
			if err != nil {
				t.Fatalf("poisoned cache produced an error; it must degrade to a miss: %v", err)
			}

			if len(exec.Log) == 0 {
				t.Fatal("poisoned cache was trusted: nothing executed")
			}

			if got := exec.Log[len(exec.Log)-1].Node; got != want {
				t.Fatalf("poisoned cache changed the result\n got %s\nwant %s", got, want)
			}
		})
	}
}

// warmKeys returns every chain key a build of g would probe, by running it once
// against a recording cache.
func warmKeys(t *testing.T, g *ir.Graph) map[core.Key]core.Entry {
	t.Helper()

	c := newMemCache()
	_, err := newSched(c, allBlobs{}, &sim.Executor{Seed: 3}).Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	return c.all()
}

// The chain key must cover an operation's external content.
//
// This is the defect that produced a real false hit: `Op.Content` was added to
// node *identity* so the graph changed when a copied file was edited, but the
// key is derived separately - and it did not. The build reported four L1 hits
// and wrote the previous output over an edited source.
//
// Identity and key are computed by different functions over the same operation.
// Anything an operation's result depends on has to be in *both*, and adding it
// to one is exactly as wrong as adding it to neither.
func TestChainKeyCoversOperationContent(t *testing.T) {
	t.Parallel()

	mk := func(content ir.NodeID) core.Key {
		n := &ir.Node{Op: ir.Op{Kind: ir.OpLocal, Args: []string{testDir}, Content: content}}

		return core.DeriveChainKey(n, nil, nil)
	}

	if mk(ir.NodeID{1}) == mk(ir.NodeID{2}) {
		t.Error("two contexts with different contents produced the same chain key")
	}

	// Two identities built separately rather than one value used twice, so
	// what is asserted is that the key follows the *content*.
	one, alsoOne := ir.NodeID{1}, ir.NodeID{1}
	if mk(one) != mk(alsoOne) {
		t.Error("the same content produced different chain keys")
	}
}

// A step's key must cover every input, including those not stacked into its
// base.
//
// A local context is a source, not a base layer, so it is deliberately absent
// from the stack (see TestLocalContextsAreNotStacked). It must still reach the
// key: COPY's result depends on the bytes it copied, and a key derived only from
// stacked layers cannot see them. Getting this wrong produced a build where
// editing a source file left COPY and every later step hitting the cache.
func TestChainKeyCoversUnstackedInputs(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpFile, Args: []string{testDir, "/app/"}}}

	base := []ir.NodeID{{1}}

	if core.DeriveChainKey(n, base, []ir.NodeID{{2}}) == core.DeriveChainKey(n, base, []ir.NodeID{{3}}) {
		t.Error("two different sources produced the same chain key")
	}

	if core.DeriveChainKey(n, base, []ir.NodeID{{2}}) == core.DeriveChainKey(n, base, nil) {
		t.Error("a step with a source keys identically to one without")
	}
}
