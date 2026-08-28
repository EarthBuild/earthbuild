package guest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// kindProgress asks how far a blob is written rather than for a path.
//
// Named once: the guest writes it and the host matches on it, and a typo in one
// of them would read as an ordinary fault-in for a file called `sha256-...`,
// which the host would look for and not find.
const kindProgress = "progress"

// faultIn is a request in the direction nothing else in this protocol travels.
//
// The tracer runs **inside** the guest and the peers live outside it: the guest
// is confined on purpose, and the fetcher holds addresses, connections and a
// store it must not have. So a fault-in is the guest asking the host, where
// every other message is the host asking the guest (E291).
type faultIn struct {
	ID uint64 `json:"id"`
	// Handle is the base this path belongs to.
	//
	// **A worker runs several steps at once**, each with its own base. A request
	// naming only a path would leave the host guessing which stack to fetch
	// from, and guessing wrong serves one step a file out of another's base -
	// a wrong build that reports success (E303).
	Handle string `json:"handle"`
	Path   string `json:"path"`
	// Kind is empty for a fault-in, which is what every one of these was.
	//
	// "progress" asks how far a blob the host is fetching has been written,
	// given that the guest has already read `Have` of it - a second question in
	// the one direction this protocol travels, and asked here because the
	// alternative was a file on a shared mount whose answer is 460ms old
	// (E688).
	Kind string `json:"kind,omitempty"`
	// Have is how much the asker has already read. The host answers when there
	// is more than this, so the guest waits on a wakeup rather than a poll.
	Have int64 `json:"have,omitempty"`
}

// filled is the answer, and the empty Error is load-bearing.
//
// **"Absent" and "unreachable" must not flatten into each other** on the way
// across (E289). An empty Error means the host looked and the file is genuinely
// not in the base, so the step gets its honest ENOENT; a non-empty one means the
// host could not find out, and the step is failed rather than told a file it may
// well need does not exist.
type filled struct {
	ID    uint64 `json:"id"`
	Error string `json:"error,omitempty"`
	// Bytes answers a progress question: how much of the blob is written.
	Bytes int64 `json:"bytes,omitempty"`
}

// Fills asks the host to fault a path in.
type Fills struct {
	mu   sync.Mutex
	enc  *json.Encoder
	next uint64

	waiting sync.Map // uint64 -> chan filled

	once   sync.Once
	closed chan struct{}
	dead   error

	// placed is what this guest faulted in, per handle, and what it was when it
	// arrived.
	//
	// Per handle because a capture excludes what was faulted into *its* delta
	// (E295), and two steps running at once each have one. A single list would
	// have each step excluding the other's files.
	//
	// The capture has to leave these out (E293), and **the guest is the only
	// party that knows all of them**: it asked for each one. The digest comes
	// with the path because the exclusion is by name and by content both - a
	// file the step then edits is the step's after all.
	placed map[string]map[string]ir.NodeID
}

// NewFills reads answers from rw and writes requests to it.
func NewFills(rw io.ReadWriter) *Fills {
	f := &Fills{enc: json.NewEncoder(rw), closed: make(chan struct{})}

	go f.read(rw)

	return f
}

// For is how a step faults paths into its own base.
//
// The handle rides on every request, so the host knows which stack to fetch from
// and this guest knows which capture must exclude what arrived (E303).
func (f *Fills) For(handle, root string) func(path string) error {
	return func(path string) error { return f.fill(handle, root, path) }
}

// Fill asks for one path, without saying which base it is for.
//
// Kept for a caller with one base and no handles, which is what a test has. A
// step always has a handle and always uses For.
func (f *Fills) Fill(path string) error { return f.fill("", "", path) }

// fill asks for one path and waits for the verdict.
//
// **A channel that breaks is a failure, never a silent absence.** If the host
// goes away and this reported "no such file", the step would take the other
// branch and produce a layer keyed on a lie, with nothing anywhere reporting a
// problem (E291).
func (f *Fills) fill(handle, root, path string) error {
	f.mu.Lock()
	f.next++
	id := f.next
	f.mu.Unlock()

	answer := make(chan filled, 1)
	f.waiting.Store(id, answer)

	defer f.waiting.Delete(id)

	f.mu.Lock()
	err := f.enc.Encode(faultIn{ID: id, Handle: handle, Path: path})
	f.mu.Unlock()

	if err != nil {
		return fmt.Errorf("ask the host for %s: %w", path, err)
	}

	select {
	case got := <-answer:
		if got.Error != "" {
			return errors.New(got.Error)
		}

		f.remember(handle, root, path)

		return nil

	case <-f.closed:
		return fmt.Errorf("asking the host for %s: %w", path, f.why())
	}
}

// Progress asks the host how far a blob it is fetching has been written.
//
// Blocks until there is more than `have`, until the fetch fails, or until the
// host says it cannot answer. **Never until nothing**: a reader with no answer
// coming is a build that hangs with nothing to say, so a host with no answerer
// refuses rather than ignores.
func (f *Fills) Progress(blob string, have int64) (int64, error) {
	f.mu.Lock()
	f.next++
	id := f.next
	f.mu.Unlock()

	answer := make(chan filled, 1)
	f.waiting.Store(id, answer)

	defer f.waiting.Delete(id)

	f.mu.Lock()
	err := f.enc.Encode(faultIn{ID: id, Kind: kindProgress, Path: blob, Have: have})
	f.mu.Unlock()

	if err != nil {
		return 0, fmt.Errorf("ask the host about %s: %w", blob, err)
	}

	select {
	case got := <-answer:
		if got.Error != "" {
			return 0, errors.New(got.Error)
		}

		return got.Bytes, nil

	case <-f.closed:
		return 0, fmt.Errorf("asking the host about %s: %w", blob, f.why())
	}
}

// read matches answers to the requests that asked for them.
//
// By id, because a step opens files from several threads at once and the answers
// come back in whatever order the host produced them. Matching by arrival would
// hand one fault-in another's verdict, which - when one succeeded and one did
// not - is the same lie by another route.
func (f *Fills) read(r io.Reader) {
	dec := json.NewDecoder(bufio.NewReader(r))

	for {
		var got filled

		err := dec.Decode(&got)
		if err != nil {
			f.die(err)

			return
		}

		if ch, ok := f.waiting.Load(got.ID); ok {
			ch.(chan filled) <- got //nolint:forcetypeassert // the only thing stored
		}
	}
}

func (f *Fills) die(err error) {
	f.once.Do(func() {
		f.mu.Lock()
		f.dead = err
		f.mu.Unlock()

		close(f.closed)
	})
}

// anyWaiterForTest hands back some waiter, whichever one.
//
// Exists so the mutation catalogue can express "match answers by arrival rather
// than by id" - the failure that hands one fault-in another's verdict. Not used
// by anything else, and deliberately absurd, because a mutant has to be able to
// name the mistake it is checking for.
// Called only by a mutant the catalogue injects; see above.
func (f *Fills) anyWaiterForTest() (any, bool) { //nolint:unused
	var (
		found any
		ok    bool
	)

	f.waiting.Range(func(_, v any) bool {
		found, ok = v, true

		return false
	})

	return found, ok
}

// remember records a file that actually arrived.
//
// Read back and hashed here rather than reported by the host, because the digest
// that matters is of what is **on this filesystem**: the host says what it sent,
// and the capture will compare against what is there.
//
// A path the base did not have is not remembered. The host says so by succeeding
// without creating anything, and excluding a file that is not there would
// exclude nothing while looking like bookkeeping.
func (f *Fills) remember(handle, root, path string) {
	fi, err := os.Lstat(path)
	if err != nil {
		// Nothing arrived: the base does not have it, and the host said so by
		// succeeding without creating anything (E289).
		return
	}

	var id ir.NodeID

	if !fi.IsDir() {
		body, rerr := os.ReadFile(path) //nolint:gosec // a path the step named
		if rerr != nil {
			return
		}

		id = layer.ContentID(body)
	}

	// A directory keeps the zero digest, which is how a capture is told "the
	// engine made this to hold something it placed": in an overlay it would not
	// exist in the delta at all (E306).
	//
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.placed == nil {
		f.placed = map[string]map[string]ir.NodeID{}
	}

	if f.placed[handle] == nil {
		f.placed[handle] = map[string]ir.NodeID{}
	}

	f.placed[handle][path] = id

	// **And every directory between it and the step's root**, which is base
	// whoever made it: priming created some and the fault-in created the rest,
	// and neither belongs in the step's delta (E307).
	//
	// It cannot be a directory the *step* made. If the step made it, the base
	// did not have it, and a fault-in for a path underneath would have found
	// nothing to fetch.
	//
	// The root is what bounds the walk. An earlier version had none and reached
	// `/var` and `/tmp`, excluding directories the step genuinely made - and its
	// comment said "only up to the root of what this guest can see" while the
	// code walked to `/` (E306).
	if root == "" {
		return
	}

	for d := filepath.Dir(path); len(d) > len(root) && d != "." && d != "/"; d = filepath.Dir(d) {
		if _, seen := f.placed[handle][d]; seen {
			break
		}

		f.placed[handle][d] = ir.NodeID{}
	}
}

// FilledFor is what one step faulted into its base, and what each was.
func (f *Fills) FilledFor(handle string) map[string]ir.NodeID {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]ir.NodeID, len(f.placed[handle]))
	maps.Copy(out, f.placed[handle])

	return out
}

// why is what went wrong with the channel.
func (f *Fills) why() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.dead == nil {
		return errors.New("the host went away")
	}

	return f.dead
}

// ServeFills answers fault-in requests using whatever can obtain a path.
//
// The host side. `fill` returning nil means the file is now there *or* is
// genuinely not in the base; returning an error means it could not be found out,
// and the guest fails the step.
func ServeFills(rw io.ReadWriter, fill func(handle, path string) error) error {
	return ServeFillsAnd(rw, fill, nil)
}

// ServeFillsAnd is ServeFills, able also to say how far a blob has been
// written.
//
// A nil `progress` is a host that does not stream blobs, and it *refuses* such
// a question rather than dropping it: a guest waiting for an answer nobody will
// give is the one outcome worth more than any saving.
func ServeFillsAnd(rw io.ReadWriter, fill func(handle, path string) error,
	progress func(blob string, have int64) (int64, error),
) error {
	dec := json.NewDecoder(bufio.NewReader(rw))
	enc := json.NewEncoder(rw)

	var mu sync.Mutex

	for {
		var req struct {
			ID     uint64 `json:"id"`
			Handle string `json:"handle"`
			Path   string `json:"path"`
			Kind   string `json:"kind"`
			Have   int64  `json:"have"`
		}

		err := dec.Decode(&req)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("read a fault-in request: %w", err)
		}

		go func() {
			out := filled{ID: req.ID}

			switch {
			case req.Kind == kindProgress && progress == nil:
				out.Error = "this host does not stream blobs, so it cannot say" +
					" how far " + req.Path + " has been written"

			case req.Kind == kindProgress:
				n, err := progress(req.Path, req.Have)
				if err != nil {
					out.Error = err.Error()
				} else {
					out.Bytes = n
				}

			// **Started for the streaming, asked about a path.** The relay
			// runs whenever either question might be asked, and streaming a
			// blob is reason enough on its own - so a build with nothing to
			// fault paths in from still has one, and this is the arm that
			// request lands in. Saying so costs a line; calling through a nil
			// `fill` cost the whole process, and said neither which path nor
			// which host (E811).
			case fill == nil:
				out.Error = "this host does not fault paths in, so it cannot" +
					" provide " + req.Path

			default:
				err := fill(req.Handle, req.Path)
				if err != nil {
					out.Error = err.Error()
				}
			}

			// One writer at a time: answers are produced concurrently and a
			// half-written one would corrupt the next.
			mu.Lock()
			_ = enc.Encode(out)
			mu.Unlock()
		}()
	}
}
