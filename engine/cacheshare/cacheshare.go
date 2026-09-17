// Package cacheshare files a portable cache mount's units where another machine
// can have them, and fills one from what this machine already holds.
//
// **One copy, because both ends of a fleet do both halves.** A driver that only
// exported and a worker that only imported would be two mechanisms sharing a
// name, and the interesting builds are the ones where a machine does both - a
// worker stocks from what some earlier step produced and offers what this one
// did. This lived in `engine/cli` while only a local build used it;
// `cmd/earth-worker` builds its executor through `exec.New` rather than through
// the CLI, so keeping it there meant a worker could not share at all.
package cacheshare

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/helper"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Sharing files a portable cache mount's units so that another machine can have
// them, and fills one from what this machine already holds.
//
// **Everything expensive is per build, not per step.** A helper is compiled once
// - about a hundred milliseconds for a four-megabyte module - and instantiated
// per verb in under a millisecond, which is what makes a verb-per-invocation
// contract affordable. A build touching one cache in forty steps compiles
// nothing after the first.
type Sharing struct {
	root string
	out  io.Writer
	dir  string

	// mu guards the compilation memo, and is held across a compile so that a
	// build touching one cache in forty steps compiles the module once.
	mu sync.Mutex
	rt *helper.Runtime
	by map[string]*helper.Helper

	// smu guards where blobs come from. **A separate lock, because `fetch` is
	// called from under `mu`**: a module is fetched while choosing a helper, and
	// one mutex for both would be asked to be reentrant.
	// dmu guards the per-directory locks below, which are what keeps two of
	// this engine's own writers out of one cache at a time.
	dmu   sync.Mutex
	inUse map[string]*sync.Mutex
	// have is what this process knows is already filed per cache directory, so
	// an export can be narrowed to what is not. See filed.
	have map[string]helper.Map

	smu  sync.Mutex
	sink *blob.Store
	away Elsewhere
	told func(key string) (string, bool)
}

// Told says where to find out which map describes a cache.
//
// **The one thing about a shared cache a machine cannot derive.** A map names a
// cache's units by ℋ and is a blob like they are; the pointer from a cache to
// its latest map is mutable, so it is deliberately not content-addressed and a
// machine that has never filled this cache has nothing to look up. The driver
// says, in a hint (I5).
//
// Nil is the ordinary case and means "whatever this machine filed itself",
// which is every local build.
func (s *Sharing) Told(of func(key string) (string, bool)) {
	s.smu.Lock()
	defer s.smu.Unlock()

	s.told = of
}

// Known is what this machine has filed, keyed as a worker will look it up.
//
// Read from disk rather than remembered, because a map filed by an *earlier*
// build is as good as one filed by this one: the units it names are still in 𝔅
// and still describe the cache. A table built in memory would make a driver
// that has shared nothing yet look like a driver with nothing to share.
func (s *Sharing) Known() map[string]string {
	at := filepath.Join(s.root, "cachemaps")

	ids, err := os.ReadDir(at)
	if err != nil {
		return nil //nolint:nilerr // a machine that has filed nothing says nothing
	}

	var out map[string]string

	for _, id := range ids {
		if !id.IsDir() {
			continue
		}

		scopes, scopeErr := os.ReadDir(filepath.Join(at, id.Name()))
		if scopeErr != nil {
			continue
		}

		for _, scope := range scopes {
			b, readErr := os.ReadFile(filepath.Join(at, id.Name(), scope.Name())) //nolint:gosec // a path this engine wrote
			if readErr != nil {
				continue
			}

			if _, parseErr := ir.ParseNodeID(strings.TrimSpace(string(b))); parseErr != nil {
				continue
			}

			if out == nil {
				out = map[string]string{}
			}

			out[id.Name()+"/"+scope.Name()] = strings.TrimSpace(string(b))
		}
	}

	return out
}

// Elsewhere answers for a blob this machine does not hold.
//
// **Nil is the ordinary case and means "this machine is on its own".** A local
// build has no fleet, and a worker before an assignment has no holders; both
// then behave as every build did before any of this - the cache does not cross,
// which is a slower build somewhere else and never a wrong one.
//
// Shaped after `fleet.Nearby.Node`, which is the implementation, and after
// `remote.Elsewhere` one layer over, which is the same idea for the REAPI
// surface. Declared here rather than imported so that this package does not
// depend on the fleet to know a cache can be shared over one.
type Elsewhere interface {
	// Node is the blob under this digest, or an error meaning "not from me".
	Node(ctx context.Context, id ir.NodeID) ([]byte, error)
}

// Away says where to look for a blob this store lacks.
func (s *Sharing) Away(e Elsewhere) {
	s.smu.Lock()
	defer s.smu.Unlock()

	s.away = e
}

// fetch is the blob under this digest: locally, or from the fleet, or not.
//
// **Kept when it arrives**, which is the difference between a read-through and a
// round trip per use. A helper is asked for once per cache mount per step and a
// worker runs many, so re-fetching four megabytes each time would make the pin
// more expensive than the path it replaced.
//
// **Verified before it is kept.** 𝔅's whole property is that a name hashes its
// contents, so filing a peer's answer under a name it does not hash to would
// poison the one store in this engine that cannot be poisoned. A mismatch is a
// miss (I4): the cache does not cross and nothing is written down.
func (s *Sharing) fetch(ctx context.Context, id ir.NodeID) ([]byte, error) {
	sink, err := s.store()
	if err != nil {
		return nil, err
	}

	if b, getErr := sink.Get(id); getErr == nil {
		return b, nil
	}

	s.smu.Lock()
	away := s.away
	s.smu.Unlock()

	if away == nil {
		return nil, fmt.Errorf("%s is not in this store and there is nobody to ask", id)
	}

	b, err := away.Node(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", id, err)
	}

	if ir.DigestOf(b) != id {
		return nil, fmt.Errorf("what arrived for %s does not hash to that name,"+
			" so it is not what was asked for", id)
	}

	if _, _, putErr := sink.Put(bytes.NewReader(b)); putErr != nil {
		// Kept is an optimisation and failing to keep it is not a reason to
		// refuse: the bytes are in hand and verified.
		_ = putErr
	}

	return b, nil
}

// New is what the executor offers each portable cache mount to.
//
// `root` is the store, where units, maps and compiled helpers all live. `dir`
// is the build's directory, against which an unpinned `--helper` path is
// resolved - **empty on a worker**, which has no Earthfile and no such path, so
// a helper there arrives pinned or not at all. `out` is where a cache that did
// not cross says so, and may be nil.
func New(root, dir string, out io.Writer) *Sharing {
	return &Sharing{root: root, out: out, dir: dir, by: map[string]*helper.Helper{}}
}

// alone takes this cache directory and gives back its release.
//
// **The gate `--sharing=shared` does not cover.** `core.ClaimOrder` and
// `guest.LockOrder` serialise steps declaring `--sharing=locked`, so one of
// those is already alone here. `shared` is the author saying several steps may
// use the directory at once and the tools inside cope with their own locks -
// which is an assertion about *npm's* locking and *cargo's*, and an importer
// writing raw files is not one of those tools. So this engine serialises its own
// writers rather than reading permission into a claim that was never about them.
//
// Per directory, not per `Sharing`, for `mountLocks`' reason: steps using
// unrelated caches waiting on each other is a real cost paid for nothing.
//
// One at a time and released before the next, so a step with two portable caches
// never holds both - which is the whole of the deadlock argument here, where
// `mountLocks` needs a sort because it holds a set.
func (s *Sharing) alone(dir string) func() {
	s.dmu.Lock()

	if s.inUse == nil {
		s.inUse = map[string]*sync.Mutex{}
	}

	m, ok := s.inUse[dir]
	if !ok {
		m = &sync.Mutex{}
		s.inUse[dir] = m
	}

	s.dmu.Unlock()

	m.Lock()

	return m.Unlock
}

// Offer files one cache mount's units.
func (s *Sharing) Offer(ctx context.Context, m ir.Mount, dir, withheld string) error {
	// **Said, not swallowed** (I11). A cache that could have crossed and did not
	// is a slower build on some other machine, which is fine; a cache silently
	// not crossing is a fleet nobody can explain. Reported once per mount per
	// step, which is where the author can act on it - by moving the secret to a
	// step of its own.
	if withheld != "" {
		if s.out != nil {
			fmt.Fprintf(s.out, "cache %s: not shared: %s\n", m.ID, withheld)
		}

		return nil
	}

	// A cache the step never wrote is not an empty cache, it is no cache: the
	// directory is made when a step binds one, so its absence means this mount
	// was never used here.
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil //nolint:nilerr // nothing to share is not a failure
	}

	defer s.alone(dir)()

	h, err := s.helperFor(ctx, m)
	if err != nil {
		return s.say(m, err)
	}

	sink, err := s.store()
	if err != nil {
		return s.say(m, err)
	}

	index, err := helper.Index(ctx, h, dir)
	if err != nil {
		return s.say(m, err)
	}

	// **Only what is not already filed, where the helper permits it.** An
	// export otherwise reads and frames every unit in the mount to learn what
	// the last map already records: a warm 88,000-unit cache re-read because a
	// step touched a hundred of it. The units dedupe in 𝔅, being the same bytes
	// under the same names, so the cost is work rather than space - which is the
	// kind of waste that never announces itself.
	//
	// The permission has to come from the helper, and `helper.Needed` says why:
	// an npm bucket is append-only, so a key present in both indexes may have
	// gained a record and a key set that compares equal is a cache that has
	// changed.
	immutable := helper.Claims(helper.Props(ctx, h, dir), helper.PropImmutableUnits)

	want, keep := helper.Needed(s.filed(ctx, m, dir, immutable), index, immutable)

	fresh, err := helper.ExportKeys(ctx, h, dir, sink, want)
	if err != nil {
		return s.say(m, err)
	}

	units := keep
	for k, id := range fresh {
		units[k] = id
	}

	if len(units) == 0 {
		return nil
	}

	// **The map is a blob like the units are.** It is the one part of a cache
	// that is not already content-addressed - a helper's key is its tool's name
	// for a thing and no amount of hashing produces it - so it is filed the same
	// way and named the same way, and travels by the transport that already
	// moves everything else.
	id, _, err := sink.Put(bytes.NewReader(encodeMap(units)))
	if err != nil {
		return s.say(m, err)
	}

	// **A share with nothing to add says nothing.** A build of forty steps over
	// one cache would otherwise print forty identical lines, which trains the
	// reader to skip the one that differs. Detected rather than guessed: the map
	// is content-addressed, so an unchanged digest is an unchanged cache.
	was, had := s.pointerMap(m, dir)
	quiet := had && was == id

	if err := s.note(m, dir, id); err != nil {
		return s.say(m, err)
	}

	s.remember(dir, units)

	if quiet {
		return nil
	}

	if s.out != nil {
		// Both numbers, because they answer different questions. The first is
		// what a peer can have; the second is what this step cost to file, and
		// a warm cache where they diverge is the whole point of the narrowing.
		if len(fresh) != len(units) {
			fmt.Fprintf(s.out, "cache %s: %d units shared (%d new), map %s\n",
				m.ID, len(units), len(fresh), id)
		} else {
			fmt.Fprintf(s.out, "cache %s: %d units shared, map %s\n", m.ID, len(units), id)
		}
	}

	return nil
}

// filed is what this machine already has blobs for in this cache directory.
//
// **Memory first, then the pointer.** What this process has filed or stocked is
// the better answer, because after a stock the pointer is short of every unit
// that just arrived - which is exactly the case this narrowing exists for. But a
// build is a fresh process, so without the second source nothing carries across
// invocations and a driver re-exports its whole cache on every run.
//
// **Only where units are immutable**, which is the same condition `Needed` uses
// the result under. The pointer names a digest per key, and trusting it means
// asserting the unit under that key still has those bytes - true by the helper's
// claim, and not otherwise. Where the claim is absent this is not read at all,
// which also keeps a map nobody will use off the disk queue.
func (s *Sharing) filed(ctx context.Context, m ir.Mount, dir string, immutable bool) helper.Map {
	if !immutable {
		return nil
	}

	s.dmu.Lock()
	held := s.have[dir]
	s.dmu.Unlock()

	if len(held) > 0 {
		return held
	}

	at, ok := s.pointerMap(m, dir)
	if !ok {
		return nil
	}

	b, err := s.fetch(ctx, at)
	if err != nil {
		return nil //nolint:nilerr // a map we cannot read is a full export
	}

	return decodeMap(b)
}

// pointerMap is the map this machine last filed for a cache, ignoring hints.
//
// `mapOf` prefers what the driver said, which is right for stocking and wrong
// here: the question is what *this* store already has blobs for, and another
// machine's map answers a different one.
func (s *Sharing) pointerMap(m ir.Mount, dir string) (ir.NodeID, bool) {
	b, err := os.ReadFile(s.pointer(m, dir)) //nolint:gosec // a path this engine wrote
	if err != nil {
		return ir.NodeID{}, false
	}

	id, err := ir.ParseNodeID(strings.TrimSpace(string(b)))
	if err != nil {
		return ir.NodeID{}, false
	}

	return id, true
}

// remember adds what is now known to be filed for this directory.
func (s *Sharing) remember(dir string, m helper.Map) {
	if len(m) == 0 {
		return
	}

	s.dmu.Lock()
	defer s.dmu.Unlock()

	if s.have == nil {
		s.have = map[string]helper.Map{}
	}

	held := s.have[dir]
	if held == nil {
		held = helper.Map{}
		s.have[dir] = held
	}

	for k, id := range m {
		held[k] = id
	}
}

// say reports a cache that did not cross, and returns the error unchanged.
//
// **Degrade if you must, but say so** (I11). A share that fails is not a build
// failure - the executor discards the error deliberately - and a share that
// fails *silently* is a machine that looks as though it is sharing and is not.
// This is the difference between a slower fleet somebody can diagnose and one
// nobody can.
func (s *Sharing) say(m ir.Mount, err error) error {
	if s.out != nil {
		fmt.Fprintf(s.out, "cache %s: not shared: %v\n", m.ID, err)
	}

	return err
}

// helperFor compiles a helper once and returns it thereafter.
//
// **Keyed on the pin, not on the path.** The digest is what names a module on
// every machine, so a memo under it is a memo two mounts spelling one helper
// differently still share - and, more to the point, it is the key a worker can
// use, which a path is not.
func (s *Sharing) helperFor(ctx context.Context, m ir.Mount) (*helper.Helper, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	at := m.HelperID
	if at == "" {
		at = m.Helper
	}

	if h, ok := s.by[at]; ok {
		return h, nil
	}

	if s.rt == nil {
		// The compilation cache lives beside the store, so a second build does
		// not recompile what the first did.
		rt, err := helper.Open(ctx, filepath.Join(s.root, "helpers"))
		if err != nil {
			return nil, err
		}

		s.rt = rt
	}

	module, err := s.moduleFor(ctx, m)
	if err != nil {
		return nil, err
	}

	h, err := s.rt.Compile(ctx, filepath.Base(m.Helper), module)
	if err != nil {
		return nil, err
	}

	s.by[at] = h

	return h, nil
}

// moduleFor is the helper's bytes, out of 𝔅 where the build pinned them.
//
// **𝔅 first, and the path only as a fallback.** A pinned helper is in the store
// under a digest that hashes its bytes, so reading it there gets exactly the
// module the key was taken over - where reading the path again gets whatever is
// at that path *now*, which on a long build is not necessarily the same file.
// It is also the only route a machine that never saw the Earthfile has.
//
// An unpinned mount falls back to the path, which is what every build did before
// the pin existed and is what a plan built without a resolver still does.
func (s *Sharing) moduleFor(ctx context.Context, m ir.Mount) ([]byte, error) {
	var missed string

	if m.HelperID != "" {
		missed = m.HelperID

		id, err := ir.ParseNodeID(m.HelperID)
		if err == nil {
			if b, getErr := s.fetch(ctx, id); getErr == nil {
				return b, nil
			}
		}
	}

	if m.Helper == "" {
		return nil, fmt.Errorf("the helper pinned as %s is not in this store and"+
			" this machine has no path for it", missed)
	}

	path := m.Helper
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.dir, m.Helper)
	}

	module, err := os.ReadFile(path) //nolint:gosec // a path the Earthfile named
	if err != nil {
		// **Both halves of what was tried**, because on a worker the second is
		// the one that was never going to work and the first is the one that
		// should have. Reporting only the path sends a reader looking for a
		// file on a machine that has no Earthfile, when what actually happened
		// is that a pinned module has not reached this store.
		if missed != "" {
			return nil, fmt.Errorf("the helper pinned as %s is not in this store,"+
				" and %s is not here either: %w"+
				"\n  a machine that did not read the Earthfile has no such path,"+
				" so the pinned module has to reach it", missed, m.Helper, err)
		}

		return nil, fmt.Errorf("read the helper %s: %w", m.Helper, err)
	}

	return module, nil
}

func (s *Sharing) store() (*blob.Store, error) {
	s.smu.Lock()
	defer s.smu.Unlock()

	if s.sink != nil {
		return s.sink, nil
	}

	st, err := blob.New(s.root)
	if err != nil {
		return nil, fmt.Errorf("open the blob store: %w", err)
	}

	s.sink = st

	return st, nil
}

// encodeMap writes a helper's keys against the blobs holding their units.
//
// Sorted, so two machines holding the same cache write the same map and the map
// itself dedupes. Hex and tab-separated rather than packed: E-F11 measured the
// binary form at 5.38 MiB for 88,114 units against 10.9 MiB in hex, which is
// 0.86% of the bytes it indexes either way, and a file a person can read is
// worth more than five megabytes at that scale.
func encodeMap(m helper.Map) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder

	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%s\n", k, m[k])
	}

	return []byte(b.String())
}

// Stock fills a cache mount from what this machine has already filed.
//
// **The other half of Offer, and the half a fleet needs.** A worker that exports
// and never imports is a great deal of hashing in aid of nothing.
//
// A cache nothing has filled here has no directory, which is the case worth
// stocking - so this makes one rather than treating its absence as nothing to
// do.
func (s *Sharing) Stock(ctx context.Context, m ir.Mount, dir string) error {
	defer s.alone(dir)()

	at, ok := s.mapOf(m, dir)
	if !ok {
		return nil
	}

	b, err := s.fetch(ctx, at)
	if err != nil {
		// The pointer names a map this store no longer holds and no peer will
		// answer for - collected, or never filed here at all. Not an error: the
		// step fills the cache itself, which is what it did before any of this.
		return nil //nolint:nilerr // a map we cannot read is a cache we cannot stock
	}

	have := helper.Map{}

	h, err := s.helperFor(ctx, m)
	if err != nil {
		return s.say(m, err)
	}

	// **An index that fails is an empty cache, not a failure.** A directory no
	// helper recognises holds nothing this can name, and a cold one is exactly
	// that - so the error is the answer rather than a reason to stop.
	if held, indexErr := helper.Index(ctx, h, dir); indexErr == nil {
		for _, k := range held {
			have[k] = ir.NodeID{}
		}
	}

	all := decodeMap(b)

	var want []string

	for k := range all {
		if _, held := have[k]; !held {
			want = append(want, k)
		}
	}

	if len(want) == 0 {
		return nil
	}

	sort.Strings(want)

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return s.say(m, err)
	}

	// **Through the fleet, not just out of this store.** The map names units by
	// digest and a machine stocking a cold cache holds none of them, so a
	// fetcher reading only what is here would find the map, want everything in
	// it, and import nothing - which is the shape E-F21 measured for the helper
	// one level up.
	//
	// A unit no peer will answer for is skipped rather than fatal, which is
	// `helper.Import`'s own rule: a cache short of one unit is a cache, where a
	// failed step is a failed build (I11).
	if err := helper.Import(ctx, h, dir, s.units(ctx), all, want); err != nil {
		return s.say(m, err)
	}

	// What arrived is filed, so the export after this step does not re-file it.
	s.remember(dir, all)

	if s.out != nil {
		fmt.Fprintf(s.out, "cache %s: %d units stocked\n", m.ID, len(want))
	}

	return nil
}

// mapOf is the map this machine last filed for a cache, if any.
//
// **A pointer, and deliberately not a blob.** Its name would have to be derived
// from the cache's id rather than from its contents, and 𝔅 refuses anything that
// does not hash to the name it is filed under - which is the property that makes
// the store impossible to poison. So a mutable pointer lives in a plain file
// beside the store, where nothing claims that invariant for it.
func (s *Sharing) mapOf(m ir.Mount, dir string) (ir.NodeID, bool) {
	// **What the driver said, before what this machine filed.** A worker that
	// has never filled this cache has no pointer at all, which is the case the
	// hint exists for; and where both exist the driver's is the one that has
	// seen the whole build, while a worker's describes only what it filled
	// itself. A unit already here is filtered out by the index either way, so
	// the richer map costs nothing.
	s.smu.Lock()
	told := s.told
	s.smu.Unlock()

	if told != nil {
		if hex, ok := told(m.ID + "/" + filepath.Base(dir)); ok {
			if id, err := ir.ParseNodeID(hex); err == nil {
				return id, true
			}
		}
	}

	b, err := os.ReadFile(s.pointer(m, dir)) //nolint:gosec // a path this engine wrote
	if err != nil {
		return ir.NodeID{}, false
	}

	id, err := ir.ParseNodeID(strings.TrimSpace(string(b)))
	if err != nil {
		return ir.NodeID{}, false
	}

	return id, true
}

// note records which map describes a cache as this machine last filed it.
func (s *Sharing) note(m ir.Mount, dir string, id ir.NodeID) error {
	at := s.pointer(m, dir)

	if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
		return err
	}

	return os.WriteFile(at, []byte(id.String()), 0o600) //nolint:wrapcheck // the caller says which cache
}

// pointer names the file holding a cache's latest map.
//
// Keyed by the same scope the directory is, so a claim or a trust domain that
// separates two caches separates their maps too - otherwise a fork's map would
// name the units a protected branch should be stocking from.
func (s *Sharing) pointer(m ir.Mount, dir string) string {
	return filepath.Join(s.root, "cachemaps", m.ID, filepath.Base(dir))
}

// decodeMap reads what encodeMap wrote, skipping anything it cannot.
//
// A line this cannot parse is a line some later version wrote, and a map is
// advice: reading the rest is better than refusing all of it.
func decodeMap(b []byte) helper.Map {
	out := helper.Map{}

	for _, line := range strings.Split(string(b), "\n") {
		key, digest, found := strings.Cut(line, "\t")
		if !found {
			continue
		}

		id, err := ir.ParseNodeID(strings.TrimSpace(digest))
		if err != nil {
			continue
		}

		out[key] = id
	}

	return out
}

// units is the store as `helper.Import` wants it, reading through to the fleet.
//
// The context is carried here because `helper.Fetcher` has none: it is the
// interface a helper's units are handed over, and threading a context through it
// would put "where might this come from" into a contract that is deliberately
// about nothing but bytes and names.
func (s *Sharing) units(ctx context.Context) helper.Fetcher {
	return fetching{s: s, ctx: ctx}
}

type fetching struct {
	s   *Sharing
	ctx context.Context //nolint:containedctx // see Sharing.units
}

func (f fetching) Get(id ir.NodeID) ([]byte, error) { return f.s.fetch(f.ctx, id) }
