// Command cachehelper is a prototype of the cache-mount helper contract.
//
// **Stage 0 of the fleet cache-sharing plan, and deliberately outside the
// engine.** The question it exists to answer is whether one interface can carry
// three unlike caches - a compiler's, a downloader's, and one whose index is
// append-only - without special-casing any of them. If it cannot, the engine
// should not grow a plugin surface for it.
//
// The contract under test:
//
//	$EARTH_CACHE_DIR names the cache root. All IO is stdin/stdout. LC_ALL=C.
//
//	  <helper> probe    exit 0 if this directory is a cache this helper knows
//	  <helper> ident    stdout: one opaque line naming this helper and version
//	  <helper> index    stdout: "<key>\t<bytes>\n" per unit, sorted by key
//	  <helper> export   stdin: keys, one per line; stdout, per unit:
//	                    "<key> <length>\n" then <length> opaque bytes
//	  <helper> import   stdin: that stream -> merged in; exit 0 = done
//
// A **key is opaque to EarthBuild**, which compares keys for equality and
// nothing else. That one decision is what lets the helper choose the unit: a
// unit here is not a file, and for `go-build` it is deliberately two of them.
//
// This binary takes the cache type as its first argument, where a real helper
// would be one program per type. That is the only departure from the contract.
package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// only is the one format this build serves, stamped in at link time.
//
// Empty in a bundled binary, which then takes the format as an argument or
// probes for it. A shipped helper sets it: an artefact that is several helpers
// cannot import into a cache that does not exist yet, because there is nothing
// there to recognise.
var only string

// unit is one thing a cache holds, under a key two machines can compare.
//
// Files rather than a file, because the unit that matters is rarely the unit the
// filesystem offers. A Go build-cache entry is an index record naming an output
// blob that lives elsewhere in the tree, and shipping either half alone ships
// something unusable.
type unit struct {
	key   string
	files []string // relative to the cache root, slash-separated
	bytes int64
}

// helper is what one cache format has to be able to say about itself.
type helper interface {
	ident() string
	probe(root string) error
	units(root string) ([]unit, error)
}

// merging is the optional half: a cache whose units share a file has to do its
// own import, because unioning them is a format-specific operation.
type merging interface {
	merge(root string, r io.Reader) error
}

// keying is how a helper says its keys are cheaper than its units.
//
// **Measured, and the reason `bytes` is optional.** A Go build cache's key is the
// name of its index record, but the record's *size* and the blob it points at can
// only be had by opening it. Over 88,114 entries that is 11.61s against 0.39s for
// the bare walk - 97% of the cost, paid for a column the receiver can live
// without. A module cache pays none of it, because its key is a path.
//
// So a helper that can name its units without reading them says so here, and the
// index it produces carries keys alone.
type keying interface {
	keys(root string) ([]string, error)
}

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("usage: cachehelper [go-build|go-mod|npm] <probe|ident|index|export|import>"))
	}

	root := os.Getenv("EARTH_CACHE_DIR")
	if root == "" {
		fatal(errors.New("EARTH_CACHE_DIR is unset, so there is no cache to speak about"))
	}

	// **The contract is `<helper> <verb>` and a helper is one format.** This
	// source holds several, so a build stamps in which one with
	// `-ldflags -X main.only=npm` and the artefact is that helper.
	//
	// Probing is the fallback and cannot be the rule: `import` runs against a
	// directory that may not exist yet - a cold cache is exactly what it is for
	// - and no format is recognisable in an empty one. A bundled binary asked
	// to import therefore refuses, having nothing to look at, which is how this
	// was found.
	args := os.Args[1:]

	var (
		h   helper
		err error
	)

	switch {
	case only != "":
		h, err = helperFor(only)

	default:
		if h, err = helperFor(args[0]); err == nil {
			args = args[1:]
		} else {
			h, err = whichKnows(root)
		}
	}

	if err != nil {
		fatal(err)
	}

	if len(args) == 0 {
		fatal(errors.New("no verb: expected probe, ident, index, export or import"))
	}

	if err := run(h, root, args[0]); err != nil {
		fatal(err)
	}
}

// whichKnows is the helper that recognises this cache, or none.
//
// Ordered, so a directory two helpers would both accept is always read by the
// same one - an answer that depended on map iteration would be a cache exported
// one way today and another tomorrow.
func whichKnows(root string) (helper, error) {
	for _, kind := range []string{"go-build", "go-mod", "npm", "cargo"} {
		h, err := helperFor(kind)
		if err != nil {
			continue
		}

		if h.probe(root) == nil {
			return h, nil
		}
	}

	return nil, fmt.Errorf("no helper here understands %s", root)
}

func run(h helper, root, verb string) error {
	switch verb {
	case "ident":
		fmt.Println(h.ident())

		return nil

	case "probe":
		return h.probe(root)

	case "props":
		// **What the engine may assume, said once and costing nothing.** A
		// property is a fact about the *format*, so it needs no per-unit work -
		// which is the whole reason it is a property and not a column on the
		// index, where `bytes` cost 24.7x for exactly this kind of information
		// (E-F6).
		for _, p := range propsOf(h) {
			fmt.Println(p)
		}

		return nil

	case "index":
		return writeIndex(h, root, os.Stdout)

	case "export":
		return export(h, root, os.Stdin, os.Stdout)

	case "import":
		// **The finding that made `import` a verb.** A file-level importer is
		// right for a content-addressed blob and wrong for an append-only
		// index: refusing to write over a bucket that exists silently discards
		// every record the sender had and the receiver did not. Only the helper
		// knows the format well enough to union them, so only the helper can
		// say. A cache that needs no merging simply does not implement this.
		if m, ok := h.(merging); ok {
			return m.merge(root, os.Stdin)
		}

		return importInto(root, os.Stdin)

	default:
		return fmt.Errorf("%q is not one of probe, ident, index, export, import", verb)
	}
}

func helperFor(kind string) (helper, error) {
	switch kind {
	case "go-build":
		return goBuild{}, nil

	case "go-mod":
		return goMod{}, nil

	case "npm":
		return npmCacache{}, nil

	case "cargo":
		return cargoRegistry{}, nil

	default:
		return nil, fmt.Errorf("no helper for %q", kind)
	}
}

// writeIndex emits the sorted key/size listing.
//
// Sorted because two indexes have to diff cleanly and because a run must be
// reproducible; size because the receiver decides what to ask for before it asks,
// and a key alone cannot be priced.
func writeIndex(h helper, root string, w io.Writer) error {
	keys, sizes, err := indexOf(h, root)
	if err != nil {
		return err
	}

	sort.Strings(keys)

	out := bufio.NewWriter(w)
	defer func() { _ = out.Flush() }()

	for _, k := range keys {
		if !validKey(k) {
			return fmt.Errorf("key %q is not [!-~]+, so it cannot cross a line-oriented protocol", k)
		}

		var err error

		if sizes == nil {
			// Key alone. The receiver cannot price the fetch in advance and
			// finds out by asking, which is the trade this helper has chosen.
			_, err = fmt.Fprintf(out, "%s\n", k)
		} else {
			_, err = fmt.Fprintf(out, "%s\t%d\n", k, sizes[k])
		}

		if err != nil {
			return err
		}
	}

	return out.Flush()
}

// indexOf takes the cheap path where the helper offers one.
func indexOf(h helper, root string) ([]string, map[string]int64, error) {
	if k, ok := h.(keying); ok {
		keys, err := k.keys(root)

		return keys, nil, err
	}

	us, err := h.units(root)
	if err != nil {
		return nil, nil, err
	}

	keys := make([]string, 0, len(us))
	sizes := make(map[string]int64, len(us))

	for _, u := range us {
		keys = append(keys, u.key)
		sizes[u.key] = u.bytes
	}

	return keys, sizes, nil
}

// validKey holds keys to printable ASCII with no space, so that a tab-separated,
// newline-delimited index cannot be broken by a key.
func validKey(k string) bool {
	if k == "" {
		return false
	}

	for _, r := range k {
		if r < '!' || r > '~' {
			return false
		}
	}

	return true
}

// export writes one frame per requested unit.
//
// **Framed rather than one stream, and batched rather than one call.** The
// engine names each unit with ℋ in order to store it, which needs a boundary it
// can find - but a process per unit is the expensive shape, and this helper's
// own measurements say so: indexing a Go build cache went from 11.61s to 0.47s
// purely by not opening 88,114 files, and a fork-exec each would have dwarfed
// both. So one invocation carries as many units as are asked for, each one
// separately addressable.
//
// `<key> <length>\n` then the bytes. Self-describing rather than bare lengths,
// so a reader learns which key a frame answers without tracking the order the
// keys went out in - which lets a helper skip one it no longer holds without the
// reader mis-slicing everything after it.
//
// **The frame is the engine's and the contents are the helper's.** What is
// inside a unit is never parsed by anything else: here it is a tar, because a Go
// build-cache unit is two files that are not beside each other. That layering is
// what keeps a tool's own naming - and its own hash function - out of the engine
// entirely.
//
// A key nobody holds is skipped in silence: the receiver asked from an index
// that may be stale, and a miss is an ordinary answer rather than an error.
func export(h helper, root string, keys io.Reader, w io.Writer) error {
	us, err := h.units(root)
	if err != nil {
		return err
	}

	by := make(map[string]unit, len(us))
	for _, u := range us {
		by[u.key] = u
	}

	out := bufio.NewWriterSize(w, 1<<20)
	defer func() { _ = out.Flush() }()

	sc := bufio.NewScanner(keys)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)

	for sc.Scan() {
		u, ok := by[strings.TrimSpace(sc.Text())]
		if !ok {
			continue
		}

		var unit bytes.Buffer

		tw := tar.NewWriter(&unit)

		whole := true

		for _, rel := range u.files {
			added, err := addFile(tw, root, rel)
			if err != nil {
				return err
			}

			// **A unit is all of its files or none of them.** Skipping one and
			// shipping the rest is how an index record crosses without the
			// content it names - a receiver that then misses on every lookup
			// and cannot tell why. A unit the sender cannot produce whole is a
			// unit it does not have.
			if !added {
				whole = false

				break
			}
		}

		if err := tw.Close(); err != nil {
			return err
		}

		if !whole {
			continue
		}

		if _, err := fmt.Fprintf(out, "%s %d\n", u.key, unit.Len()); err != nil {
			return err
		}

		if _, err := out.Write(unit.Bytes()); err != nil {
			return err
		}
	}

	if err := sc.Err(); err != nil {
		return err
	}

	return out.Flush()
}

// maxUnit bounds one frame, so a helper that writes a wrong length cannot ask
// the reader for unbounded memory. Generous: the largest object in a real Go
// build cache measured 12.7 MiB.
const maxUnit = 1 << 30

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

// addFile writes one of a unit's files, and says whether it was there.
//
// The boolean is load-bearing: an absent file used to be swallowed here, which
// let a unit ship missing half of itself. The caller decides what that means,
// and decides the unit is not one.
func addFile(tw *tar.Writer, root, rel string) (bool, error) {
	at := filepath.Join(root, filepath.FromSlash(rel))

	fi, err := os.Lstat(at)
	if err != nil {
		return false, nil //nolint:nilerr // absent is an answer, not a failure
	}

	if !fi.Mode().IsRegular() {
		return false, nil
	}

	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return false, err
	}

	hdr.Name = rel
	// **A unit's bytes are a function of the cache, never of what read it.**
	// The engine names a unit by ℋ over these bytes, so anything here that
	// varies by machine varies the digest - and two workers holding the same
	// entry would file it under two names and dedup nothing.
	//
	// Modes are the case that proves it rather than an abundance of caution:
	// WASI cannot report a file's real mode, so the same cache exported through
	// a wasm runtime says 0600 where a native run says 0644. Measured, byte 147
	// of the first unit.
	//
	// So the mode is reduced to the one bit that changes what a file *is* -
	// whether it can be executed - and everything else is fixed. Owners and
	// times likewise: nothing downstream reads them and every one of them
	// differs between two machines that hold identical bytes.
	hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
	hdr.AccessTime, hdr.ChangeTime, hdr.ModTime = zeroTime, zeroTime, zeroTime
	hdr.Mode = 0o644

	if fi.Mode().Perm()&0o111 != 0 {
		hdr.Mode = 0o755
	}

	if err := tw.WriteHeader(hdr); err != nil {
		return false, err
	}

	f, err := os.Open(at) //nolint:gosec // a path resolved under the cache root
	if err != nil {
		return false, err
	}

	defer func() { _ = f.Close() }()

	_, err = io.Copy(tw, f)

	return err == nil, err
}

// importInto merges a stream into the cache.
//
// **Atomic per file, and never over an existing one.** A tar extracted directly
// into a live cache leaves a short file behind when the stream ends early, and a
// short file that exists is one nothing will ever repair - `registry/src` has no
// checksum to catch it and no collector to evict it. So each entry is written
// beside its destination and linked into place, and an interrupted import leaves
// the cache exactly as it was.
//
// `O_EXCL` rather than a stat-then-write: absence is only a meaningful answer if
// the check and the creation are the same operation. Two concurrent imports of
// one cache would otherwise both find a path absent and both write it.
func importInto(root string, r io.Reader) error {
	return eachUnit(r, func(_ string, body []byte) error {
		return unpackUnit(root, bytes.NewReader(body))
	})
}

// unpackUnit places the files of one unit, atomically and never over an
// existing path.
func unpackUnit(root string, r io.Reader) error {
	tr := tar.NewReader(r)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			// A truncated stream has left nothing behind: every entry so far
			// was linked into place whole, and the one in flight was a
			// temporary file that is now unreferenced.
			return fmt.Errorf("read the stream: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		// Braces to `inside`'s belt, inline because that is where CodeQL's
		// go/zipslip looks for a guard.
		if !filepath.IsLocal(hdr.Name) {
			return fmt.Errorf("the stream names %q, which is outside the cache", hdr.Name)
		}

		rel, err := inside(root, hdr.Name)
		if err != nil {
			return err
		}

		if err := placeOne(root, rel, hdr, tr); err != nil {
			return err
		}
	}
}

// inside refuses a name that leaves the cache root.
//
// A name hashes perfectly well and still escapes: `../` is the obvious form and
// an absolute path is the other. Checked before anything is created, because a
// refusal after a directory exists has already changed the cache.
func inside(root, name string) (string, error) {
	clean := path.Clean("/" + name)
	if clean == "/" {
		return "", fmt.Errorf("the stream names the cache root itself")
	}

	rel := strings.TrimPrefix(clean, "/")
	if rel == "" || strings.HasPrefix(rel, "../") || rel == ".." {
		return "", fmt.Errorf("the stream names %q, which is outside the cache", name)
	}

	return rel, nil
}

// placeOne writes one entry beside its destination and links it in.
//
// **The arriving file keeps the time it arrived, and that is deliberate.**
// `addFile` zeroes every timestamp on the way out, because a unit's bytes are
// its name and a time that differed between machines would give one entry two
// digests. Restoring those zeros here would be the obvious symmetry and is
// wrong: mtime is not part of a unit's *content*, it is a fact about this
// machine's copy, and several ecosystems read it.
//
// Cargo is the one that proves it. Its fingerprints compare mtimes, so a crate
// or a source tree stamped 1970 looks older than everything built from it -
// which reads as "already fresh, no rebuild needed", the wrong direction for a
// mistake to point. A file that has just arrived is new, and saying so costs
// nothing.
func placeOne(root, rel string, hdr *tar.Header, body io.Reader) error {
	at := filepath.Join(root, filepath.FromSlash(rel))

	if _, err := os.Lstat(at); err == nil {
		// Held already. Not an error and not a merge: the receiver's copy of a
		// unit is as good as the sender's, which is the whole claim.
		return nil
	}

	dir := filepath.Dir(at)
	if err := mkdirNoFollow(root, dir); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".incoming-")
	if err != nil {
		return err
	}

	name := tmp.Name()

	defer func() {
		_ = tmp.Close()
		_ = os.Remove(name) // a no-op once the link below has succeeded
	}()

	if _, err := io.Copy(tmp, body); err != nil {
		return err
	}

	if err := tmp.Chmod(fs.FileMode(hdr.Mode).Perm()); err != nil { //nolint:gosec // a mode from this helper's own export
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	// Link rather than rename: rename would replace a file that appeared while
	// this one was being written, and "never over an existing one" has to hold
	// against a concurrent importer as well as against a stale stat.
	if err := os.Link(name, at); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("place %s: %w", rel, err)
	}

	return nil
}

// mkdirNoFollow creates the ancestors of an entry, refusing to walk a symlink.
//
// The escape this closes: the receiver holds a symlink at `foo`, the stream
// carries `foo/bar`, and a plain `MkdirAll` writes through the link to wherever
// it points. Checking each component is the only way to know, because the
// resolved path of a symlinked directory is a perfectly ordinary directory.
func mkdirNoFollow(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}

	if rel == "." {
		return nil
	}

	at := root

	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		at = filepath.Join(at, part)

		fi, err := os.Lstat(at)
		switch {
		case err == nil && fi.IsDir():
			continue

		case err == nil && fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink, and writing through it would leave the cache", at)

		case err == nil:
			return fmt.Errorf("%s is not a directory", at)
		}

		if err := os.Mkdir(at, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}

	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "cachehelper:", err)
	os.Exit(1)
}

// sizeOf is a stat that reads a missing file as nothing, because a unit whose
// half has been collected is a unit worth less rather than an error.
func sizeOf(at string) int64 {
	fi, err := os.Lstat(at)
	if err != nil {
		return 0
	}

	return fi.Size()
}

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)

	return n
}

// immutableUnits is a helper saying a key's unit never changes content.
//
// Optional, and absent means "it might", which is the conservative reading and
// what every helper meant before this existed.
type immutableUnits interface{ unitsAreImmutable() }

// propsOf is what this helper claims about its format.
func propsOf(h helper) []string {
	var out []string

	if _, ok := h.(immutableUnits); ok {
		out = append(out, "units-immutable")
	}

	return out
}
