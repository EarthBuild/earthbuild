package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// zeroTime is what times are normalised to, so two exports of one cache are the
// same bytes and a round-trip test can compare them.
var zeroTime = time.Unix(0, 0)

// goBuild is Go's build cache, and the case that makes the unit worth having.
//
// An entry is two files that are not beside each other: an index record
// `<ActionID>-a`, and the output it names, `<OutputID>-d`, filed under a
// different two-hex-digit prefix. Ship either alone and the receiver holds
// something it can never use - the record points at an absent blob, or the blob
// is unreachable because nothing indexes it.
//
// So the key is the ActionID and the unit is both files. That is the whole
// argument for letting a helper choose the unit rather than the engine assuming
// a file is one.
type goBuild struct{}

func (goBuild) ident() string { return "earthbuild/go-build/1" }

func (goBuild) probe(root string) error {
	// `trim.txt` is the collector's bookkeeping and the one file every populated
	// build cache has. Its absence is how this tells a build cache from any
	// other directory of two-hex-digit subdirectories.
	if _, err := os.Lstat(filepath.Join(root, "trim.txt")); err != nil {
		return fmt.Errorf("no trim.txt: %s is not a Go build cache", root)
	}

	return nil
}

func (goBuild) units(root string) ([]unit, error) {
	var out []unit

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), "-a") {
			return nil //nolint:nilerr // an unreadable entry is one fewer unit, not a failure
		}

		action := strings.TrimSuffix(d.Name(), "-a")

		output, size, ok := readEntry(p)
		if !ok {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil //nolint:nilerr // outside the root is not ours
		}

		// The output lives under its own first two hex digits, which is why the
		// unit cannot be inferred from the index file's location.
		data := path.Join(output[:2], output+"-d")

		out = append(out, unit{
			key:   action,
			files: []string{filepath.ToSlash(rel), data},
			bytes: sizeOf(p) + size,
		})

		return nil
	})

	return out, err
}

// keys names every unit without opening one.
//
// The key *is* the index record's filename, so a walk answers the whole
// question. What a walk cannot answer is which output blob the record points at
// or how large the pair is - both of which are export-time questions, and
// neither of which the receiver needs in order to decide what to ask for.
func (goBuild) keys(root string) ([]string, error) {
	var out []string

	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), "-a") {
			return nil //nolint:nilerr // an unreadable entry is one fewer unit
		}

		out = append(out, strings.TrimSuffix(d.Name(), "-a"))

		return nil
	})

	return out, err
}

// readEntry parses `v1 <ActionID> <OutputID> <size> <nanotime>`.
//
// The trailing field is a write time, which is why this cache is immutable and
// not reproducible: two machines compute the same ActionID and the same
// OutputID for one compilation and record different bytes. It decides nothing -
// `cmd/go` performs no freshness check - and it is exactly the case a rule
// demanding identical bytes would have refused to share.
func readEntry(at string) (output string, size int64, ok bool) {
	b, err := os.ReadFile(at) //nolint:gosec // a path from this cache's own walk
	if err != nil {
		return "", 0, false
	}

	f := strings.Fields(string(b))
	if len(f) < 4 || f[0] != "v1" || len(f[2]) < 2 {
		return "", 0, false
	}

	return f[2], atoi(f[3]), true
}

// goMod is the module cache, where a unit is a module version.
//
// Measured byte-identical across darwin/arm64 and linux/amd64 over 94,162 shared
// paths (E-F4), which is what a cache of *source* should be. The excluded region
// is the checksum database's `lookup/`, whose records carry the signed tree head
// at the time of the lookup and so differ between machines.
type goMod struct{}

func (goMod) ident() string { return "earthbuild/go-mod/1" }

func (goMod) probe(root string) error {
	at := filepath.Join(root, "cache", "download")
	if fi, err := os.Lstat(at); err != nil || !fi.IsDir() {
		return fmt.Errorf("no cache/download: %s is not a Go module cache", root)
	}

	return nil
}

func (goMod) units(root string) ([]unit, error) {
	download := filepath.Join(root, "cache", "download")
	byKey := map[string]*unit{}

	err := filepath.WalkDir(download, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry is one fewer unit
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil //nolint:nilerr // outside the root is not ours
		}

		slash := filepath.ToSlash(rel)

		if !sharableModulePath(slash) {
			return nil
		}

		// `<anything>/@v/<version>.<ext>` - the module path is everything left
		// of `/@v/`, and the version is the base name without its extension.
		at := strings.LastIndex(slash, "/@v/")
		if at < 0 {
			return nil
		}

		module := strings.TrimPrefix(slash[:at], "cache/download/")
		version := strings.TrimSuffix(path.Base(slash), path.Ext(slash))
		key := module + "@" + version

		u, seen := byKey[key]
		if !seen {
			u = &unit{key: key}
			byKey[key] = u
		}

		u.files = append(u.files, slash)
		u.bytes += sizeOf(p)

		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]unit, 0, len(byKey))
	for _, u := range byKey {
		out = append(out, *u)
	}

	return out, nil
}

// sharableModulePath is the measured exclusion list, anchored at the mount root.
//
// Two traps a filename-matched list falls into, both found by diffing two
// independently-filled caches (E-F4). `**/*.lock` catches eleven third-party
// `Cargo.lock` and `Gemfile.lock` files inside extracted module trees, which are
// as immutable as the code beside them. A bare `lock` catches a gvisor
// *directory*. Hence: anchored, and only under `cache/download`.
func sharableModulePath(slash string) bool {
	switch {
	case strings.HasPrefix(slash, "cache/download/sumdb/"):
		return false

	case strings.HasSuffix(slash, ".lock"), strings.HasSuffix(slash, ".partial"):
		return false

	case path.Base(slash) == "lock", path.Base(slash) == "list":
		return false
	}

	return strings.Contains(slash, "/@v/")
}

// npmCacache is the case that decides whether one interface is enough.
//
// cacache is two stores: `content-v2/`, addressed by integrity hash and
// genuinely content-addressed, and `index-v5/`, whose buckets are **append-only
// files holding several entries each** - 99 of 400 sampled here hold more than
// one. Two machines' copies of one bucket therefore each hold entries the other
// lacks, and neither is "as good as" the other.
//
// That is what makes npm not *union-complete*, and it is why `import` has to be
// the helper's verb rather than the engine's: merging these buckets is a
// line-level operation over a format only this helper knows.
type npmCacache struct{}

func (npmCacache) ident() string { return "earthbuild/npm-cacache/1" }

func (npmCacache) probe(root string) error {
	for _, want := range []string{"index-v5", "content-v2"} {
		if fi, err := os.Lstat(filepath.Join(root, want)); err != nil || !fi.IsDir() {
			return fmt.Errorf("no %s: %s is not a cacache store", want, root)
		}
	}

	return nil
}

// entry is one line of an index bucket: a hash of the record, then the record.
type entry struct {
	bucket string // relative, slash-separated
	line   string
	digest string // the record's own hash, the part before the tab
	blob   string // relative path in content-v2, or empty
	bytes  int64
}

func (n npmCacache) units(root string) ([]unit, error) {
	es, err := n.entries(root)
	if err != nil {
		return nil, err
	}

	out := make([]unit, 0, len(es))
	// A bucket is an append-only log and cacache re-appends an unchanged record
	// when its key is fetched again, so one bucket can hold the same line twice.
	// Two identical records are one unit, and deduping them is what makes the
	// key unique rather than a workaround for it not being.
	seen := make(map[string]bool, len(es))

	for _, e := range es {
		files := []string{e.bucket}
		if e.blob != "" {
			files = append(files, e.blob)
		}

		// Keyed by bucket *and* record hash. The record hash alone looked like
		// the obvious key and is not unique: 155 of 30,162 entries in a real
		// cacache share one with an entry in another bucket, so a key-to-unit
		// map collapses them and the loser is never shipped.
		//
		// **A key must be unique within a cache**, which was not in the contract
		// until this helper broke it. The bucket is what disambiguates, and it
		// is stable across machines because cacache derives it from the entry's
		// own key.
		key := e.bucket + ":" + e.digest
		if seen[key] {
			continue
		}

		seen[key] = true

		out = append(out, unit{key: key, files: files, bytes: e.bytes})
	}

	return out, nil
}

func (npmCacache) entries(root string) ([]entry, error) {
	index := filepath.Join(root, "index-v5")

	var out []entry

	err := filepath.WalkDir(index, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable bucket is fewer units
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil //nolint:nilerr // outside the root is not ours
		}

		b, err := os.ReadFile(p) //nolint:gosec // a path from this cache's own walk
		if err != nil {
			return nil //nolint:nilerr // likewise
		}

		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}

			tab := strings.Index(line, "\t")
			if tab < 0 {
				continue
			}

			e := entry{
				bucket: filepath.ToSlash(rel),
				line:   line,
				digest: line[:tab],
				bytes:  int64(len(line)),
			}

			var rec struct {
				Integrity string `json:"integrity"`
				Size      int64  `json:"size"`
			}

			if json.Unmarshal([]byte(line[tab+1:]), &rec) == nil && rec.Integrity != "" {
				e.blob = contentPath(rec.Integrity)
				e.bytes += rec.Size
			}

			out = append(out, e)
		}

		return nil
	})

	return out, err
}

// contentPath is cacache's layout for an integrity string:
// `content-v2/<alg>/<first two>/<next two>/<rest>` over the **hex** digest.
//
// **Hex, not the base64 the integrity is written in.** An SRI string carries
// base64 and cacache addresses by `ssri.parse(integrity).hexDigest()`, so a path
// built from the base64 - however carefully its `/` and `+` are made
// filesystem-safe - names a file that is not there. It produced
// `content-v2/sha512/XI/5M/...` where the store holds
// `content-v2/sha512/5c/8e/...`, and every content blob was quietly left behind:
// index records crossed, the tarballs they name did not, and a receiver would
// have had an index that missed on every lookup.
func contentPath(integrity string) string {
	fields := strings.Fields(integrity)
	if len(fields) == 0 {
		return ""
	}

	alg, b64, ok := strings.Cut(fields[0], "-")
	if !ok {
		return ""
	}

	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) < 3 {
		return ""
	}

	hex := hex.EncodeToString(raw)

	return path.Join("content-v2", alg, hex[:2], hex[2:4], hex[4:])
}

// merge is npm's own import, and the reason `import` is a verb rather than a
// behaviour the engine supplies.
//
// The generic importer refuses to write over a file that exists, which is right
// for a content-addressed blob and wrong for a bucket: the receiver's bucket and
// the sender's each hold records the other lacks, so "skip it, mine is as good"
// silently discards the sender's. This reads both, unions the records by their
// own hashes, and writes the result - atomically, beside the original, so an
// interrupted merge leaves the bucket as it was.
func (n npmCacache) merge(root string, r io.Reader) error {
	staged, err := os.MkdirTemp(root, ".incoming-")
	if err != nil {
		return err
	}

	defer func() { _ = os.RemoveAll(staged) }()

	if err := importInto(staged, r); err != nil {
		return err
	}

	// Blobs first and by the ordinary rule: content-v2 is content-addressed, so
	// a held copy really is as good as the sender's.
	if err := copyTree(staged, root, func(rel string) bool {
		return strings.HasPrefix(rel, "content-v2/")
	}); err != nil {
		return err
	}

	incoming, err := n.entries(staged)
	if err != nil {
		return err
	}

	return n.unionBuckets(root, incoming)
}

func (n npmCacache) unionBuckets(root string, incoming []entry) error {
	byBucket := map[string][]entry{}
	for _, e := range incoming {
		byBucket[e.bucket] = append(byBucket[e.bucket], e)
	}

	for bucket, es := range byBucket {
		at := filepath.Join(root, filepath.FromSlash(bucket))

		held := map[string]bool{}

		var lines []string

		if b, err := os.ReadFile(at); err == nil { //nolint:gosec // under the cache root
			for _, line := range strings.Split(string(b), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}

				if tab := strings.Index(line, "\t"); tab >= 0 {
					held[line[:tab]] = true
				}

				lines = append(lines, line)
			}
		}

		added := 0

		for _, e := range es {
			if held[e.digest] {
				continue
			}

			held[e.digest] = true
			lines = append(lines, e.line)
			added++
		}

		if added == 0 {
			continue
		}

		if err := writeAtomic(root, at, strings.Join(lines, "\n")+"\n"); err != nil {
			return err
		}
	}

	return nil
}

// writeAtomic replaces a file by rename, which is the one place this helper
// *does* replace: a unioned bucket is strictly a superset of what was there.
func writeAtomic(root, at, body string) error {
	if err := mkdirNoFollow(root, filepath.Dir(at)); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(at), ".bucket-")
	if err != nil {
		return err
	}

	name := tmp.Name()

	defer func() {
		_ = tmp.Close()
		_ = os.Remove(name)
	}()

	if _, err := tmp.WriteString(body); err != nil {
		return err
	}

	if err := tmp.Chmod(0o644); err != nil {
		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(name, at)
}

func copyTree(from, to string, want func(rel string) bool) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable staged entry is not fatal
		}

		rel, err := filepath.Rel(from, p)
		if err != nil {
			return nil //nolint:nilerr // outside is not ours
		}

		slash := filepath.ToSlash(rel)
		if !want(slash) {
			return nil
		}

		at := filepath.Join(to, rel)
		if _, err := os.Lstat(at); err == nil {
			return nil
		}

		if err := mkdirNoFollow(to, filepath.Dir(at)); err != nil {
			return err
		}

		if err := os.Link(p, at); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}

		return nil
	})
}

// cargoRegistry is Cargo's downloaded crates, and the simplest cache here.
//
// One `.crate` file is one unit: a gzip tarball of a published crate at a
// published version, which cannot be rewritten because crates.io does not permit
// it. No index beside it, no bookkeeping, nothing to merge - which is what makes
// it **union-complete** where npm's is not.
//
// `registry/cache` and not `registry/src`. The extracted sources are derived
// from these and Cargo re-extracts on demand, so shipping the trees moves
// several times the bytes to save an unpack; and Cargo performs no content
// verification when reusing an extracted tree, where a `.crate` is checked
// against the lockfile.
type cargoRegistry struct{}

func (cargoRegistry) ident() string { return "earthbuild/cargo-registry/1" }

// cargoCacheIn finds the crate directory, wherever the author mounted.
//
// **Two mount points are both sensible and a helper must take either.**
// `$CARGO_HOME` is what an author reaches for; `$CARGO_HOME/registry` is what
// they should mount, because mounting the home masks the toolchain living in it
// - `cargo: not found` is what that looks like, and it cost a build here.
//
// So the layout is found rather than assumed. Empty where this is neither.
func cargoCacheIn(root string) string {
	for _, at := range []string{
		filepath.Join(root, "registry", "cache"), // CARGO_HOME
		filepath.Join(root, "cache"),             // CARGO_HOME/registry
	} {
		if fi, err := os.Lstat(at); err == nil && fi.IsDir() {
			return at
		}
	}

	return ""
}

func (cargoRegistry) probe(root string) error {
	if cargoCacheIn(root) == "" {
		return fmt.Errorf("no cache of crates under %s: not a Cargo registry", root)
	}

	return nil
}

func (cargoRegistry) units(root string) ([]unit, error) {
	var out []unit

	cache := cargoCacheIn(root)
	if cache == "" {
		return nil, nil
	}

	err := filepath.WalkDir(cache, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".crate") {
			return nil //nolint:nilerr // an unreadable entry is one fewer unit
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil //nolint:nilerr // outside the root is not ours
		}

		slash := filepath.ToSlash(rel)

		// Keyed by registry and crate rather than by crate alone: two registries
		// may both publish `serde-1.0.0`, and a key that could not tell them
		// apart would import one machine's private crate over another's public
		// one. The registry directory is a hash of its URL, so it is the same
		// name on every machine.
		// Relative to the crate directory, so the key is the same whichever of
		// the two mount points the author chose - a key that moved with the
		// mount would make one machine's units unreadable by another's.
		under, relErr2 := filepath.Rel(cache, p)
		if relErr2 != nil {
			return nil //nolint:nilerr // outside the cache is not ours
		}

		key := strings.TrimSuffix(filepath.ToSlash(under), ".crate")

		out = append(out, unit{key: key, files: []string{slash}, bytes: sizeOf(p)})

		return nil
	})

	return out, err
}
