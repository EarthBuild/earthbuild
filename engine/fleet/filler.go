package fleet

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Filler turns a path a step is about to open into a fetch.
//
// The two ends of lazy transfer joined: the tracer stops a step *before* an open
// (E289), this works out which layer the path is in, asks a peer for that one
// path (E288), and puts it where the step will look (E290).
//
// **Its contract is the tracer's**, and the three outcomes are the whole of the
// safety:
//
//   - the file is placed, and the syscall finds it;
//   - no layer in the stack has it, so nothing is created and the syscall gets
//     its honest ENOENT - and this returns **nil**, because that is not a
//     failure;
//   - nobody could be asked, so this returns an **error** and the step is
//     failed. A step told "no such file" about a file this engine could not
//     reach takes the other branch and succeeds, producing a layer keyed on a
//     lie (E289).
//
// The protocol tells the second and third apart already: a fragment that arrives
// without the path is a layer that does not have it, and no fragment at all is a
// peer that could not answer.
type Filler struct {
	// Into is where the step's base is materialised.
	Into string
	// Stack is the base, bottom first, as a stack is written.
	Stack []ir.NodeID
	// From is where to ask, nearest first.
	From []Fragmenter
	// Store keeps what arrives, so a second step reading the same path pays
	// nothing.
	Store *Fragments
}

// Prime materialises the paths a step was predicted to read, before it starts.
//
// The other half of a lazy base. `Fill` handles what a step reads that nobody
// expected; this handles what it was expected to read, in one batch rather than
// a fault at a time - which matters, because a fault is a round trip and a
// prediction that is any good names most of what the step will open (E292).
//
// **Nothing predicted materialises nothing**, and that is not an empty base: a
// worker with no prediction fetches the whole layer the ordinary way, and an
// empty prime that looked like a base would leave a step faulting on every path
// it opened.
func (f *Filler) Prime(ctx context.Context, want []string) error {
	if len(want) == 0 {
		return nil
	}

	// Bottom up here, unlike Fill: a layer higher in the stack overwrites what
	// is below it, so writing in stack order leaves the top copy in place - the
	// same result Fill reaches by searching downwards and stopping.
	for _, id := range f.Stack {
		err := f.primeLayer(ctx, id, want)
		if err != nil {
			return err
		}
	}

	return nil
}

// primeLayer places whatever one layer has of a predicted set.
func (f *Filler) primeLayer(ctx context.Context, id ir.NodeID, want []string) error {
	if !f.Store.Has(id, want) {
		_, err := ProvisionFragments(ctx, f.Store,
			Assignment{Base: []ir.NodeID{id}, Hints: Hints{ReadsPredicted: want}},
			f.From...)
		if err != nil {
			return fmt.Errorf("prime from %v: %w", id, err)
		}
	}

	from := f.Store.Dir(id, want)

	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == from {
			return nil //nolint:nilerr // a fragment that is not there primes nothing
		}

		rel, err := filepath.Rel(from, p)
		if err != nil {
			return nil //nolint:nilerr // not ours to place
		}

		fi, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // gone between walk and stat
		}

		return place(p, filepath.Join(f.Into, rel), fi)
	})
}

// Fill places a path if some layer in the stack has it.
func (f *Filler) Fill(ctx context.Context, path string) error {
	rel, ok := f.inside(path)
	if !ok {
		// `/proc`, `/dev`, the step's own working directory. Asking a peer for
		// those would be a fetch that cannot succeed and a step failed for
		// reading something perfectly ordinary.
		return nil
	}

	// Top down: the layer nearest the top wins, because that is what the step
	// would see if the whole stack were materialised. A filler that took the
	// first answer it got would hand the step a file the base has overwritten.
	for i := range slices.Backward(f.Stack) {
		got, err := f.fromLayer(ctx, f.Stack[i], rel)
		if err != nil {
			return err
		}

		if got {
			return nil
		}
	}

	// Every layer answered, and none had it.
	return nil
}

// fromLayer fetches one path out of one layer, and says whether it was there.
func (f *Filler) fromLayer(ctx context.Context, id ir.NodeID, rel string) (bool, error) {
	want := []string{rel}

	if !f.Store.Has(id, want) {
		_, err := ProvisionFragments(ctx,
			f.Store, Assignment{Base: []ir.NodeID{id}, Hints: Hints{ReadsPredicted: want}},
			f.From...)
		if err != nil {
			// Nobody could be asked. **Not the same as the layer not having
			// it**, and the difference is a wrong build.
			return false, fmt.Errorf("could not ask about %s in %v: %w", rel, id, err)
		}
	}

	from := filepath.Join(f.Store.Dir(id, want), rel)

	fi, err := os.Lstat(from)
	if err != nil {
		// The fragment arrived and does not contain it: this layer does not have
		// this path. An ordinary answer, and the caller tries the one below.
		return false, nil //nolint:nilerr // absence is an answer
	}

	if fi.IsDir() {
		// A directory is scaffolding here; the step asked for it and will read
		// what is inside, which arrives the same way one path at a time.
		return true, place(from, filepath.Join(f.Into, rel), fi)
	}

	return true, place(from, filepath.Join(f.Into, rel), fi)
}

// inside says whether a path is in the materialised base, and where.
func (f *Filler) inside(path string) (string, bool) {
	root := filepath.Clean(f.Into)

	p := filepath.Clean(path)
	if p == root {
		return "", false
	}

	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return rel, true
}

// makeAncestors creates the directories above an entry, each with the mode and
// times it has in the source.
//
// Walks down rather than up, so a parent exists before its child is stamped, and
// stamps only what it creates: a directory already placed has been given its own
// mode by `place` and must not be rewritten by a guess.
//
// A source ancestor that cannot be stated is created with the restrictive
// default. That is a base which will differ from an eager one, and it is the
// honest outcome for a source that has gone: better a wrong mode than a
// fabricated one that claims to be right.
func makeAncestors(from, to string) error {
	dir := filepath.Dir(to)

	missing := []string{}
	for at := dir; at != "" && at != string(filepath.Separator); at = filepath.Dir(at) {
		_, err := os.Lstat(at)
		if err == nil {
			break
		}

		missing = append(missing, at)
	}

	// Deepest last, so each is made after its parent.
	for i := range slices.Backward(missing) {
		at := missing[i]

		rel, err := filepath.Rel(dir, at)
		if err != nil {
			return fmt.Errorf("locate %s under %s: %w", at, dir, err)
		}

		src := filepath.Clean(filepath.Join(filepath.Dir(from), rel))

		mode := os.FileMode(0o750)

		fi, err := os.Lstat(src)
		if err == nil {
			mode = fi.Mode().Perm()
		}

		err = os.Mkdir(at, mode)
		if err != nil && !os.IsExist(err) {
			return fmt.Errorf("create %s: %w", at, err)
		}

		if fi != nil {
			err = stampLike(at, fi)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// place copies one entry into the materialised base.
//
// Modes and times come with it: a step that reads a file also stats it, and a
// base assembled with the wrong modes is a base the step behaves differently
// against.
func place(from, to string, fi os.FileInfo) error {
	// **The ancestors get the mode they have in the source, not 0o755.**
	// `MkdirAll` invented every directory between the base root and this entry
	// with a fixed mode, and only a directory that is *itself* faulted in ever
	// got the right one.
	//
	// **This does not reach a layer, and the first version of this comment said
	// it did.** A capture excludes what the engine placed, ancestors included,
	// so their modes never enter an identity. What they do reach is the step:
	// the sentence above this function is "a base assembled with the wrong modes
	// is a base the step behaves differently against", and a directory invented
	// at 0o755 where the source had 0o700 is a base the step can walk into and
	// should not (E631, corrected in E632).
	err := makeAncestors(from, to)
	if err != nil {
		return fmt.Errorf("make room for %s: %w", to, err)
	}

	if fi.IsDir() {
		err = os.MkdirAll(to, fi.Mode().Perm())
		if err != nil {
			return fmt.Errorf("place %s: %w", to, err)
		}

		return stampLike(to, fi)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		target, linkErr := os.Readlink(from)
		if linkErr != nil {
			return fmt.Errorf("read %s: %w", from, linkErr)
		}

		_ = os.Remove(to)

		linkErr = os.Symlink(target, to)
		if linkErr != nil {
			return fmt.Errorf("place %s: %w", to, linkErr)
		}

		return nil
	}

	src, err := os.Open(from) //nolint:gosec // a path this engine wrote
	if err != nil {
		return fmt.Errorf("read %s: %w", from, err)
	}

	defer func() { _ = src.Close() }()

	//nolint:gosec // a path this engine composed
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return fmt.Errorf("place %s: %w", to, err)
	}

	_, err = io.Copy(dst, src)
	if err != nil {
		_ = dst.Close()

		return fmt.Errorf("place %s: %w", to, err)
	}

	err = dst.Close()
	if err != nil {
		return fmt.Errorf("place %s: %w", to, err)
	}

	return stampLike(to, fi)
}

// stampLike gives a placed entry the time the layer says it has.
func stampLike(to string, fi os.FileInfo) error {
	err := os.Chtimes(to, fi.ModTime(), fi.ModTime())
	if err != nil {
		return fmt.Errorf("times of %s: %w", to, err)
	}

	return nil
}
