package cli

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

// shares files a portable cache mount's units so that another machine can have
// them.
//
// **Everything expensive is per build, not per step.** A helper is compiled once
// - about a hundred milliseconds for a four-megabyte module - and instantiated
// per verb in under a millisecond, which is what makes a verb-per-invocation
// contract affordable. A build touching one cache in forty steps compiles
// nothing after the first.
type shares struct {
	root string
	out  io.Writer
	dir  string

	mu   sync.Mutex
	rt   *helper.Runtime
	by   map[string]*helper.Helper
	sink *blob.Store
}

// shareCache is the hook the executor offers each portable cache mount to.
//
// Nil where nothing can collect them - no store to file units in - because the
// executor treats a nil hook as "this machine is not sharing", which is what
// every build did before any of this.
func (g *engine) shareCache(storeDir string) func(context.Context, ir.Mount, string) error {
	s := &shares{root: storeDir, out: g.o.Out, dir: g.o.Dir, by: map[string]*helper.Helper{}}

	return s.offer
}

// offer files one cache mount's units.
func (s *shares) offer(ctx context.Context, m ir.Mount, dir string) error {
	// A cache the step never wrote is not an empty cache, it is no cache: the
	// directory is made when a step binds one, so its absence means this mount
	// was never used here.
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil //nolint:nilerr // nothing to share is not a failure
	}

	h, err := s.helperFor(ctx, m.Helper)
	if err != nil {
		return err
	}

	sink, err := s.store()
	if err != nil {
		return err
	}

	units, err := helper.Export(ctx, h, dir, sink)
	if err != nil {
		return fmt.Errorf("export the cache %s: %w", m.ID, err)
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
		return fmt.Errorf("file the map for %s: %w", m.ID, err)
	}

	if s.out != nil {
		fmt.Fprintf(s.out, "cache %s: %d units shared, map %s\n", m.ID, len(units), id)
	}

	return nil
}

// helperFor compiles a helper once and returns it thereafter.
//
// The path is resolved against the build's directory, so `--helper ./go.wasm`
// means what an author reading the Earthfile thinks it means.
func (s *shares) helperFor(ctx context.Context, at string) (*helper.Helper, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	path := at
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.dir, at)
	}

	module, err := os.ReadFile(path) //nolint:gosec // a path the Earthfile named
	if err != nil {
		return nil, fmt.Errorf("read the helper %s: %w", at, err)
	}

	h, err := s.rt.Compile(ctx, filepath.Base(at), module)
	if err != nil {
		return nil, err
	}

	s.by[at] = h

	return h, nil
}

func (s *shares) store() (*blob.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
