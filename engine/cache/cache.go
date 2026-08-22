// Package cache is the on-disk action cache: 𝔄, the map from a step's key to a
// claim about what it produces.
//
// One file per entry, named by the key. That is deliberately the least clever
// arrangement available: two builds running at once need no coordination beyond
// what the filesystem already provides, a damaged entry affects one step rather
// than the whole cache, and eviction is `rm`.
//
// Everything here treats a stored entry as a **claim rather than a fact** (green
// paper §5.2). The blob store is self-verifying; this is not. So an entry that
// cannot be read, or that names nothing, is a miss - never an error and never a
// guess. A damaged cache costs time and nothing else.
package cache

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// maxRecordedConflicts bounds what a single build keeps. Every conflict is
// counted; the first few are kept in full, because the twentieth instance of a
// non-deterministic step says nothing the first one did not and a build that
// has gone wrong should not also exhaust memory describing it.
const maxRecordedConflicts = 32

// Cache is an action cache rooted at a directory.
type Cache struct {
	dir string

	// Guards the conflict record only. The entries themselves need no lock -
	// one file each, inserted atomically - which is the arrangement this
	// package's doc comment is about.
	mu        sync.Mutex
	conflicts []Conflict
	count     int
}

// Open prepares a cache under root.
//
// Reports a directory it cannot use rather than degrading to no cache: a build
// silently running uncached is a build whose speed nobody can account for
// (green paper I11 - degrade if you must, but say so).
func Open(root string) (*Cache, error) {
	dir := filepath.Join(root, "actions")

	// 0750: the cache holds what this machine has built and the keys those
	// results are filed under, which is a record of what a developer works on.
	// A directory this engine owns is as tight as it can be.
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return nil, fmt.Errorf("prepare the action cache at %s: %w", dir, err)
	}

	return &Cache{dir: dir}, nil
}

// path is where an entry lives. Keys are fixed-width digests, so the hex is a
// safe filename with no escaping and no collisions.
func (c *Cache) path(k core.Key) string {
	return filepath.Join(c.dir, hex.EncodeToString(k[:])+".json")
}

// stored is the on-disk form.
//
// Explicit rather than reusing core.Entry directly: this is a wire format that
// outlives the process and has to survive the struct changing. Layer is hex
// because a byte array in JSON is neither readable nor stable.
type stored struct {
	Layer string `json:"layer"`
	// Content is omitempty because entries written before it existed have none,
	// and an absent field must stay absent rather than becoming a zero digest -
	// Get distinguishes the two, and the comparison in Put depends on it.
	Content string `json:"content,omitempty"`
	Exit    int    `json:"exit"`
	Bytes   int64  `json:"bytes"`
	Writer  string `json:"writer"`
	// Declares is the declaration the result carries, and Declared says somebody
	// looked. Both omitempty for the reason Content is: an entry written before
	// they existed has neither, and absent must stay absent rather than becoming
	// "this image declares nothing".
	Declares string `json:"declares,omitempty"`
	Declared bool   `json:"declared,omitempty"`
}

// Get returns a claim, if there is a readable one.
func (c *Cache) Get(k core.Key) (core.Entry, bool) {
	b, err := os.ReadFile(c.path(k))
	if err != nil {
		return core.Entry{}, false
	}

	var s stored
	err = json.Unmarshal(b, &s)
	if err != nil {
		// Unreadable is a miss. The alternative - failing the build - hands a
		// corrupted cache the power to stop work that would otherwise succeed.
		return core.Entry{}, false
	}

	id, err := parseID(s.Layer)
	if err != nil {
		return core.Entry{}, false
	}

	var zero ir.NodeID
	if id == zero {
		// A well-formed digest naming nothing. A build trusting it would
		// materialise an empty base and cache the result.
		return core.Entry{}, false
	}

	// A content digest that will not parse is treated as absent rather than as
	// a failure: the entry's claim is still usable, and the comparison falls
	// back to layers, which is what an entry without one does anyway.
	content, err := parseID(s.Content)
	if err != nil {
		content = ir.NodeID{}
	}

	// Unparseable is absent here too, and absent is honest: a declaration this
	// cannot name is one the reader must not believe it has.
	declares, err := parseID(s.Declares)
	if err != nil {
		declares = ir.NodeID{}
	}

	return core.Entry{
		Layer: id, Content: content, Exit: s.Exit, Bytes: s.Bytes, Writer: s.Writer,
		Declares: declares, Declared: s.Declared,
	}, true
}

// Conflict is a rewrite that was refused: one key, two different layers.
//
// Worth surfacing rather than merely preventing. A key determines a result by
// construction - Κ₂ hashes the operation, the environment and the platform
// along with everything the step observed (green paper 4.6) - so two layers
// under one key is a step that read the same things twice and produced
// different output. That is I1, and §6's screening exists to find it.
//
// Overwriting was how it got laundered: the second build won and there was no
// longer any evidence that there had been a first.
type Conflict struct {
	Key   core.Key
	Held  ir.NodeID // what the cache already had
	Given ir.NodeID // what the later Put offered
}

// ConflictCount is how many rewrites were refused, including any past the cap.
func (c *Cache) ConflictCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.count
}

// Conflicts are the refused rewrites, ordered by key.
//
// Ordered rather than in arrival order: they arrive from steps running in
// parallel, so arrival order is a property of the machine's scheduling and
// would put the machine into a build's report (I12). Sorting by key makes two
// runs of the same broken build produce the same list.
func (c *Cache) Conflicts() []Conflict {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := append([]Conflict(nil), c.conflicts...)

	sort.Slice(out, func(i, j int) bool { return out[i].Key.String() < out[j].Key.String() })

	return out
}

// note records a refused rewrite.
func (c *Cache) note(k core.Key, held, given ir.NodeID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.count++

	if len(c.conflicts) < maxRecordedConflicts {
		c.conflicts = append(c.conflicts, Conflict{Key: k, Held: held, Given: given})
	}
}

// Put stores a claim.
//
// Errors are not returned, because the interface does not have them and because
// there is nothing useful to do: a claim that could not be written means the
// next build repeats work. Silent, but only ever slower.
func (c *Cache) Put(k core.Key, e core.Entry) {
	var zero ir.NodeID
	if e.Layer == zero {
		// Nothing to claim. Storing it would create exactly the entry Get is
		// written to reject.
		return
	}

	rec := stored{
		Layer:  e.Layer.String(),
		Exit:   e.Exit,
		Bytes:  e.Bytes,
		Writer: e.Writer,
	}

	if e.Content != zero {
		rec.Content = e.Content.String()
	}

	// Declared travels even when there is nothing to declare: it is the record
	// that somebody looked, which is what tells a later reader that an absent
	// declaration is the image's answer and not this entry's age.
	rec.Declared = e.Declared
	if e.Declares != zero {
		rec.Declares = e.Declares.String()
	}

	b, err := json.Marshal(rec)
	if err != nil {
		return
	}

	// An entry already here is left alone: state is inserted or removed, never
	// modified in place (I9). The early check is for the common case - the same
	// claim arriving twice - and os.Link below is what actually makes it true,
	// since two steps can pass this check together.
	if held, ok := c.Get(k); ok {
		if disagree(held, e) {
			c.note(k, held.Layer, e.Layer)
		}

		return
	}

	// A unique temporary, then a *link*. The name used to be
	// `<key>.<pid>.tmp`, and the comment said the pid "keeps concurrent builds
	// from sharing a temporary file" - which it does, and which says nothing
	// about concurrent steps of one build. Two of them share a key whenever the
	// same target is reached twice or two observations coincide under Κ₂, and
	// they then opened one path with O_TRUNC and wrote claims of different
	// lengths, leaving the loser's tail past the winner's end. Get reads that as
	// a miss, so it cost work rather than correctness and nothing ever
	// reported it.
	tmp, err := os.CreateTemp(c.dir, "entry-*.tmp")
	if err != nil {
		return
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = tmp.Write(b)
	if err != nil {
		_ = tmp.Close()

		return
	}

	err = tmp.Close()
	if err != nil {
		return
	}

	// Link rather than rename, because rename would overwrite: it is the
	// insert-only primitive the filesystem offers, failing with EEXIST when
	// somebody inserted between the check above and here. The loser of that
	// race then looks at what won, and records it if they disagree - so the
	// TOCTOU closes into the same report rather than into silence.
	err = os.Link(tmp.Name(), c.path(k))
	if err != nil {
		if held, ok := c.Get(k); ok && disagree(held, e) {
			c.note(k, held.Layer, e.Layer)
		}
	}
}

// disagree reports whether two claims under one key describe different results.
//
// On *content* where both sides have it, because a layer's identity includes
// its timestamps (I8) and two runs of one deterministic step therefore produce
// two layer digests - creating a directory stamps it with the wall clock.
// Comparing layers read every re-run after eviction as a step that produced two
// different results, which is most steps.
//
// On layers where either side has no content: a host step computes none, and so
// does an entry written before the field existed. Treating an absent digest as
// equal to a present one would declare those pairs identical without looking,
// and the direction that loses a real finding is worse than the one that
// over-reports.
func disagree(held, given core.Entry) bool {
	var zero ir.NodeID
	if held.Content != zero && given.Content != zero {
		return held.Content != given.Content
	}

	return held.Layer != given.Layer
}

func parseID(s string) (ir.NodeID, error) {
	var id ir.NodeID

	b, err := hex.DecodeString(s)
	if err != nil {
		return id, fmt.Errorf("layer digest is not hex: %w", err)
	}

	if len(b) != ir.HashSize {
		return id, fmt.Errorf("layer digest is %d bytes, want %d", len(b), ir.HashSize)
	}

	copy(id[:], b)

	return id, nil
}
