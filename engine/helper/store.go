package helper

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// maxUnit bounds one frame, so a helper that writes a wrong length cannot ask
// the engine for unbounded memory. Generous: the largest object in a real Go
// build cache measured 12.7 MiB.
const maxUnit = 1 << 30

// Keeper is where a unit is filed.
//
// Shaped after `blob.Store.Put` rather than after anything new, so that 𝔅 is
// already one of these - and so that the store names the blob rather than being
// told what to call it, which is the property that makes a unit's identity a
// function of its bytes and nothing else.
type Keeper interface {
	Put(r io.Reader) (ir.NodeID, int64, error)
}

// Fetcher is where a unit is found again.
type Fetcher interface {
	Get(id ir.NodeID) ([]byte, error)
}

// Map is a helper's keys against the blobs holding their units.
//
// **The join, and the only part of a cache that is not already
// content-addressed.** A helper's key is its tool's name for a thing - an action
// id, a module version, a record digest - and no amount of hashing the engine
// does will produce it. So this is carried, and E-F11 measured the cost: 5.38
// MiB for 88,114 units, 0.86% of the bytes they index.
type Map map[string]ir.NodeID

// Export runs a helper over a cache and files every unit it names.
//
// **This is where a cache mount stops being a directory and becomes content.**
// Everything downstream already exists: 𝔅 names a unit by ℋ over its bytes and
// cannot be poisoned, `fleet.Nodes` serves it, `earth/blob/1` moves it verified
// per chunk, and `remote.Cache.Elsewhere` finds it. What was missing was
// anything putting a unit in.
//
// The engine parses nothing. A key is opaque, a frame is a length, and what is
// inside one is the helper's business - which is what lets a tool's own naming,
// and its own hash function, stay out of the engine entirely.
func Export(ctx context.Context, h *Helper, cacheDir string, into Keeper) (Map, error) {
	keys, err := Index(ctx, h, cacheDir)
	if err != nil {
		return nil, err
	}

	return ExportKeys(ctx, h, cacheDir, into, keys)
}

// ExportKeys files only the units named, and is what an incremental export uses.
//
// Separate from [Export] because the caller that can narrow the set is the one
// holding the last map, and this package has no memory between builds. See
// [Needed] for which keys those are and why the narrowing needs the helper's
// permission.
func ExportKeys(
	ctx context.Context, h *Helper, cacheDir string, into Keeper, keys []string,
) (Map, error) {
	if len(keys) == 0 {
		return Map{}, nil
	}

	out := make(Map, len(keys))

	// A pipe rather than a buffer: a real cache is hundreds of megabytes and
	// the engine has no reason to hold one in memory to hash it a frame at a
	// time.
	pr, pw := io.Pipe()

	go func() {
		err := h.Run(ctx, cacheDir, []string{"export"}, strings.NewReader(strings.Join(keys, "\n")+"\n"), pw)
		_ = pw.CloseWithError(err)
	}()

	defer func() { _ = pr.Close() }()

	err := eachUnit(pr, func(key string, body []byte) error {
		id, _, err := into.Put(bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("file unit %s: %w", key, err)
		}

		out[key] = id

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Import hands a helper the units named by these keys, out of the store.
//
// **Only what is asked for.** The caller has already decided which keys are
// worth having - the intersection of what a peer holds with what this machine
// lacks - so this moves that set and no more.
//
// A key the store cannot answer for is skipped rather than fatal: a map may name
// a blob this machine never fetched, and a cache short of one unit is a cache,
// where a failed step is a failed build (I11).
//
// **Two obligations on the helper, and only it can meet them.**
//
// A unit becomes visible **whole or not at all**. An interrupted import must
// leave a cache no worse than it found it, and "never overwrite" is the wrong
// rule for that - extract-and-skip-if-present turns a truncated fetch into
// permanent corruption that nothing later repairs, because the half-written file
// is exactly what a skip preserves. Stage beside the destination and rename,
// which is what `engine/fleet/fragments.go` does one directory over.
//
// And an import runs **while the tools that own this cache may be reading it**.
// `--sharing=locked` gives the step the directory alone, but `shared` is the
// author saying several steps use it at once and those tools cope with their own
// locks - an assertion about npm's locking and cargo's, not about an importer.
// The engine serialises its *own* writers (see `cacheshare.Sharing.alone`) and
// cannot do more: whether this format tolerates a concurrent reader is a fact
// about the format, which is the whole reason a helper exists.
func Import(
	ctx context.Context, h *Helper, cacheDir string, from Fetcher, m Map, keys []string,
) error {
	pr, pw := io.Pipe()

	go func() {
		var err error

		for _, k := range keys {
			id, ok := m[k]
			if !ok {
				continue
			}

			b, getErr := from.Get(id)
			if getErr != nil {
				continue
			}

			if _, err = fmt.Fprintf(pw, "%s %d\n", k, len(b)); err != nil {
				break
			}

			if _, err = pw.Write(b); err != nil {
				break
			}
		}

		_ = pw.CloseWithError(err)
	}()

	defer func() { _ = pr.Close() }()

	return h.Run(ctx, cacheDir, []string{"import"}, pr, io.Discard)
}

// Index is every key a helper names, sorted.
//
// Sorted because the helper's contract says so and because two indexes have to
// diff cleanly; the size column, where a helper offers one, is dropped - the
// engine decides what to fetch by comparing keys, and prices it by asking.
func Index(ctx context.Context, h *Helper, cacheDir string) ([]string, error) {
	var out bytes.Buffer

	if err := h.Run(ctx, cacheDir, []string{"index"}, nil, &out); err != nil {
		return nil, err
	}

	var keys []string

	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<22)

	for sc.Scan() {
		key, _, _ := strings.Cut(sc.Text(), "\t")
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the helper's index: %w", err)
	}

	sort.Strings(keys)

	return keys, nil
}

// eachUnit reads the framed stream and hands each unit's bytes on.
//
// Streaming: one unit is held at a time, so a batch of ten thousand costs one
// unit's memory rather than the batch's. A short read is an error and not a
// smaller stream - the position `engine/layer/unpack.go` takes, because half a
// unit is not a smaller unit.
func eachUnit(r io.Reader, take func(key string, body []byte) error) error {
	br := bufio.NewReaderSize(r, 1<<20)

	for {
		line, err := br.ReadString('\n')
		if errors.Is(err, io.EOF) && strings.TrimSpace(line) == "" {
			return nil
		}

		if err != nil {
			return fmt.Errorf("read a frame header: %w", err)
		}

		key, size, found := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
		if !found {
			return fmt.Errorf("a frame header without a length: %q", line)
		}

		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil || n < 0 || n > maxUnit {
			return fmt.Errorf("unit %s is framed as %q bytes, which is not a length this reads", key, size)
		}

		body := make([]byte, n)
		if _, err := io.ReadFull(br, body); err != nil {
			return fmt.Errorf("read unit %s: %w", key, err)
		}

		if err := take(key, body); err != nil {
			return err
		}
	}
}

// PropImmutableUnits is a helper saying a key's unit never changes content.
//
// The permission [Needed] requires, and it has to come from here: the engine
// cannot tell a Go build cache - where an action id is a hash of the inputs, so
// the output under it is fixed - from an npm cacache, where a bucket is
// append-only and a key's record set grows.
const PropImmutableUnits = "units-immutable"

// Props is what a helper says about its format, or nothing.
//
// A sixth verb, and the only optional one. A helper that does not implement it
// exits non-zero and is read as claiming nothing, which is the conservative
// answer and the behaviour every helper had before the verb existed.
func Props(ctx context.Context, h *Helper, cacheDir string) []string {
	var out bytes.Buffer

	if err := h.Run(ctx, cacheDir, []string{"props"}, nil, &out); err != nil {
		return nil
	}

	var props []string

	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		if p := strings.TrimSpace(sc.Text()); p != "" {
			props = append(props, p)
		}
	}

	return props
}

// Claims reports whether a helper named this property.
func Claims(props []string, want string) bool {
	for _, p := range props {
		if p == want {
			return true
		}
	}

	return false
}
