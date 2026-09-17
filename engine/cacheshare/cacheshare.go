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

	mu   sync.Mutex
	rt   *helper.Runtime
	by   map[string]*helper.Helper
	sink *blob.Store
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

// Offer files one cache mount's units.
func (s *Sharing) Offer(ctx context.Context, m ir.Mount, dir string) error {
	// A cache the step never wrote is not an empty cache, it is no cache: the
	// directory is made when a step binds one, so its absence means this mount
	// was never used here.
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil //nolint:nilerr // nothing to share is not a failure
	}

	h, err := s.helperFor(ctx, m)
	if err != nil {
		return s.say(m, err)
	}

	sink, err := s.store()
	if err != nil {
		return s.say(m, err)
	}

	units, err := helper.Export(ctx, h, dir, sink)
	if err != nil {
		return s.say(m, err)
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

	if err := s.note(m, dir, id); err != nil {
		return s.say(m, err)
	}

	if s.out != nil {
		fmt.Fprintf(s.out, "cache %s: %d units shared, map %s\n", m.ID, len(units), id)
	}

	return nil
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

	module, err := s.moduleFor(m)
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
func (s *Sharing) moduleFor(m ir.Mount) ([]byte, error) {
	var missed string

	if m.HelperID != "" {
		missed = m.HelperID

		id, err := ir.ParseNodeID(m.HelperID)
		if err == nil {
			sink, storeErr := s.blobs()
			if storeErr != nil {
				return nil, storeErr
			}

			if b, getErr := sink.Get(id); getErr == nil {
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
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.blobs()
}

// blobs is store without the lock, for callers that already hold it.
func (s *Sharing) blobs() (*blob.Store, error) {
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
	at, ok := s.mapOf(m, dir)
	if !ok {
		return nil
	}

	sink, err := s.store()
	if err != nil {
		return s.say(m, err)
	}

	b, err := sink.Get(at)
	if err != nil {
		// The pointer names a map this store no longer holds - collected, or
		// never fetched. Not an error: the step fills the cache itself.
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

	if err := helper.Import(ctx, h, dir, sink, all, want); err != nil {
		return s.say(m, err)
	}

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
