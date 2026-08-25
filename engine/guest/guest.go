package guest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"github.com/EarthBuild/earthbuild/engine/fstime"
	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
	"github.com/EarthBuild/earthbuild/engine/timing"
)

// Server is the guest half: it runs inside the VM and serves a real
// materialiser - overlayfs over the CAS shared in at boot.
//
// It wraps any core.Materialiser, which is what lets the whole protocol be
// tested on a machine with no VM and no overlayfs: put the simulator behind it
// and the wire is still exercised end to end.
type Server struct {
	Mat core.Materialiser

	// Idle stops the sandbox when nobody has used it for a while. Nil means it
	// stays up until something else stops it, which is what a sandbox did
	// before this existed. See idle.
	Idle *idle

	// Fills faults in paths a lazily materialised base does not yet have, and
	// remembers what arrived so the capture can leave it out (E293, E295).
	//
	// Nil for every build today: nothing lazily materialises yet, and a nil one
	// makes the capture exactly what it was.
	Fills *Fills
	// fillsMu guards Fills, which arrives when a host dials rather than when
	// this server is built. See SetFills.
	fillsMu sync.Mutex

	// obs records what each handle's step looked at in its base, which is the
	// engine's first real observation source. See observe.go.
	obsMu sync.Mutex
	obs   map[core.Handle]*watcher

	// Unconfined runs steps without isolation.
	//
	// Off by default, and it must stay that way: a step that escapes its
	// sandbox invalidates every cache claim in the specification (green paper
	// A3), because ε no longer bounds what it observed. Set only by tests, and
	// by tests that are not making claims about caching.
	Unconfined bool

	// Limits bound what a step may consume. Unset means unbounded.
	//
	// An unavailable cgroup degrades to unbounded rather than refusing: a step
	// with no memory ceiling still produces a correct result, so the rule that
	// protects ε does not apply here.
	Limits Limits

	// LayerDir is where captured layers are committed. Empty means a capture is
	// digested and discarded, which is honest but useless.
	LayerDir string

	// DropNet puts the step in an empty network namespace.
	//
	// Off by default because most builds fetch dependencies, and cutting the
	// network breaks them. It is a policy with a large blast radius, so it is
	// opt-in rather than a side effect of confinement.
	DropNet bool

	mu      sync.Mutex
	handles map[string]core.Handle
	lockMu  sync.Mutex
	locks   map[string]*sync.Mutex

	// Terminals carries a caller's terminal to an interactive step.
	//
	// Separate from the request connection because that one is a framed byte
	// stream and a terminal is a *descriptor*: relayed bytes give a step
	// something that passes `test -t 0` and has no job control, no window size
	// and no signal from Ctrl-C (E190). Nil means no interactive step can run
	// here, which is the honest answer for any arrangement that is not one host.
	Terminals *net.UnixConn

	// termMu is held for the length of an interactive step. One terminal, one
	// prompt: a second step reading the same descriptor takes some of the
	// keystrokes, which is a wrong session rather than a degraded one.
	termMu   sync.Mutex
	n        int
	degraded string
	// running is how to abandon each exec in flight, by request id.
	//
	// A function rather than the *exec.Cmd it came from. Holding the command
	// meant `cancel` read `cmd.Process` while `Start` wrote it, which the race
	// detector reported the first time the gate ran - and which os/exec already
	// solves, since it invokes Cmd.Cancel only after the process exists.
	running map[uint64]context.CancelFunc

	// own answers, once, whether the layer store can carry uid and gid.
	own storeOwnership

	// mounts serialises steps that share a cache, which is what
	// `CACHE --sharing=locked` means and what was not being provided (E427).
	mounts mountLocks
}

// began records how to abandon a running step so a later cancel can find it.
// prepared makes a handle over a base somebody else assembled.
//
// The delta is a directory of its own, as it is for a stacked base: a step's
// writes must not land where it reads, or the layer it produces would contain
// its own base. `TakeExcluding` prevents that afterwards for faulted-in files
// (E293); keeping them apart here costs nothing and prevents it for everything
// else.
func (s *Server) prepared(root string) (core.Handle, error) {
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("the prepared base %s is not a directory this"+
			" guest can use: %w", root, err)
	}

	delta, err := os.MkdirTemp(s.LayerDir, "delta-")
	if err != nil {
		return nil, fmt.Errorf("make room for a step's writes: %w", err)
	}

	return &preparedHandle{root: root, delta: delta}, nil
}

// preparedHandle is a base that arrived assembled.
type preparedHandle struct {
	root  string
	delta string
}

func (h *preparedHandle) Root() string  { return h.root }
func (h *preparedHandle) Delta() string { return h.delta }

func (h *preparedHandle) Observations() core.Observation { return core.Observation{} }

// Release removes the delta and leaves the base alone.
//
// Whoever prepared it owns it: this guest did not assemble it and must not
// decide when it stops existing.
func (h *preparedHandle) Release() error {
	err := os.RemoveAll(h.delta)
	if err != nil {
		return fmt.Errorf("release a prepared base: %w", err)
	}

	return nil
}

// filler is how this guest faults a path in, or nothing.
//
// Nil when no fills channel was given, which is every build today - and a nil
// one leaves the tracer watching rather than filling, which is what it has
// always done.
func (s *Server) filler(handle string) func(string) error {
	if s.Fills == nil {
		return nil
	}

	// The step's root bounds which directories count as base (E307): everything
	// between it and a faulted-in file was placed by this engine, and nothing
	// above it is this step's business at all.
	root := ""

	if h, ok := s.get(handle); ok {
		root = h.Root()
	}

	// Bound to this step's handle, so the host knows which base to fetch from
	// and this guest knows which capture must exclude what arrives (E303).
	return s.Fills.For(handle, root)
}

// placedIn is what this guest faulted into a delta, keyed as the capture sees
// paths.
//
// Relative, because a capture walks a tree and names entries relative to its
// root, while a fault-in names an absolute path the step opened. Two spellings
// of one path is how an exclusion silently excludes nothing.
func (s *Server) placedIn(handle, root string) map[string]ir.NodeID {
	if s.Fills == nil {
		return nil
	}

	out := map[string]ir.NodeID{}

	for p, id := range s.Fills.FilledFor(handle) {
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			// Faulted in somewhere other than this delta. Another handle's, or
			// outside the step's filesystem entirely.
			continue
		}

		out[rel] = id
	}

	return out
}

func (s *Server) began(id uint64, kill context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running == nil {
		s.running = map[uint64]context.CancelFunc{}
	}

	s.running[id] = kill
}

// ended forgets a command that has finished.
func (s *Server) ended(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.running, id)
}

// cancel kills the step a request is running, if it is still running.
//
// Best effort by design: a step that finished a moment ago is not an error to
// cancel, because the caller cannot know it had. Reporting one would make every
// race at the end of a step look like a fault.
func (s *Server) cancel(id uint64) {
	s.mu.Lock()
	kill := s.running[id]
	s.mu.Unlock()

	if kill == nil {
		return
	}

	kill()
}

// Serve handles requests until the connection closes.
func (s *Server) Serve(ctx context.Context, rw io.ReadWriter) error {
	c := newConn(rw)

	for {
		var req Request

		err := c.recv(&req)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("receive: %w", err)
		}

		// Something arrived, so this sandbox is wanted.
		s.Idle.touched()

		// Handled concurrently: a slow materialise must not hold up an exec
		// queued behind it, which is the whole reason requests carry ids.
		go func() {
			// A cancellable context per *request*, not per step.
			//
			// `cancel` used to reach only an exec, because `began` was called
			// from the exec path alone: a KindCancel naming a materialise found
			// nothing registered, and the guest ran it to completion with its
			// reply dropped. The caller had stopped waiting, so that work was
			// being paid for and could not be used (E178).
			//
			// The exec path still calls `began` with its own kill, which
			// replaces this one for the life of that step - killing the process
			// is stronger than cancelling the context it was started with, and
			// it is what a step needs.
			reqCtx, cancel := context.WithCancel(ctx)

			s.began(req.ID, cancel)

			// Held open for as long as this request runs, however long that is:
			// a RUN that compiles for an hour sends nothing while it works, and
			// a sandbox that stopped on silence would stop in the middle of it.
			s.Idle.working()

			defer func() {
				// **After the work, at every request, in one place.** What
				// costs the host a descriptor is a name this guest has looked
				// up, and every request that touches the store looks some up -
				// so the check belongs where they all end rather than in each
				// of them, where the one that was forgotten is the one that
				// fails a build (E560).
				//
				// A file read of a few bytes, and a drop only when there is
				// something worth dropping.
				relieveDentries()

				s.Idle.done()
				s.ended(req.ID)
				cancel()
			}()

			resp := s.handle(reqCtx, req, c)
			resp.ID = req.ID

			// A send failure means the connection is gone, which the read loop
			// will discover too. Reporting it from here would race with that -
			// except for a reply too large to write, where the connection is
			// fine and only this answer is impossible, and dropping it hangs
			// the caller for ever (E617). `reply` answers that one.
			_ = reply(c, req.Kind, resp)
		}()
	}
}

func (s *Server) handle(ctx context.Context, req Request, c *conn) Response {
	switch req.Kind {
	case KindHello:
		// Version is checked on the first exchange because the guest ships
		// inside a VM image and is updated on a different cadence from the
		// host. A mismatch discovered mid-build is a mismatch discovered late.
		if req.Version != Version {
			return Response{Err: fmt.Sprintf(
				"protocol version mismatch: host speaks %d, guest speaks %d", req.Version, Version)}
		}

		return Response{Version: Version}

	case KindMaterialise:
		// A base somebody already assembled, used as it is (E300). Refused
		// together with a stack rather than resolved by precedence: the two say
		// different things about where a step's filesystem comes from, and a
		// caller that sent both does not know which it wants (I10).
		if req.Prepared != "" && len(req.Stack) > 0 {
			return Response{Err: "a materialise names both a stack and a" +
				" prepared root, and they say different things about where this" +
				" step's filesystem comes from"}
		}

		var (
			h   core.Handle
			err error
		)

		if req.Prepared != "" {
			h, err = s.prepared(req.Prepared)
		} else {
			var stack []ir.NodeID

			stack, err = decodeStack(req.Stack)
			if err != nil {
				return Response{Err: err.Error()}
			}

			h, err = s.Mat.Materialise(ctx, stack)
		}

		if err != nil {
			return Response{Err: err.Error()}
		}

		s.mu.Lock()
		s.n++
		id := fmt.Sprintf("h%d", s.n)

		if s.handles == nil {
			s.handles = map[string]core.Handle{}
		}

		s.handles[id] = h
		s.mu.Unlock()

		// The declaration travels with the handle it belongs to. A caller that
		// asked for a base has been given what that base declares, without a
		// second question and without reading the store (E554).
		resp := Response{Handle: id, Root: h.Root()}

		if d, ok := h.(interface{ Declaration() decl.Declaration }); ok {
			said := d.Declaration()
			resp.Declares = &said
		}

		return resp

	case KindObserve:
		h, ok := s.get(req.Handle)
		if !ok {
			return Response{Err: "unknown handle " + req.Handle}
		}

		// Two sources, merged: what the materialiser saw (nothing yet - S5) and
		// what this server saw doing the step's own work. The copy path is the
		// second, and is the engine's first real observation source (E119).
		obs := merge(h.Observations(), s.observationOf(h))

		// **One page.** The whole observation used to go in one frame, and a
		// step whose observation exceeded it was reported as having been watched
		// by nothing at all (E620). The host asks for the rest.
		resp, _, more := observationPage(obs, req.FromEntry)
		resp.More = more

		return resp

	case KindExec:
		return s.execRequest(ctx, req, c)

	case KindCancel:
		s.cancel(req.Cancel)

		return Response{}

	case KindExport:
		h, ok := s.get(req.Handle)
		if !ok {
			return Response{Err: "unknown handle " + req.Handle}
		}

		// The store is a disk the host can read too, so an export whose bytes
		// are already on it is a sentence rather than 45 MB (E568). The guest
		// answers only when it can prove the merged file is the store's file
		// unchanged; otherwise the bytes go the ordinary way.
		if r, ok := h.(core.SharedResolver); ok && req.MayShare {
			if rel, ok := r.SharedFile(req.Path); ok {
				return Response{Shared: rel}
			}
		}

		err := s.export(h, req.Path, req.Dest, clampAt(req.Clamp))
		if err != nil {
			return Response{Err: err.Error()}
		}

		return Response{}

	case KindCopy:
		h, ok := s.get(req.Handle)
		if !ok {
			return Response{Err: "unknown handle " + req.Handle}
		}

		// One copy into a filesystem at a time.
		//
		// Requests are handled concurrently on purpose - a slow materialise must
		// not hold up an exec - and for two *different* handles that is exactly
		// right. For one handle it buys nothing: both copies write the same
		// filesystem, so they contend rather than overlap.
		//
		// It costs something, though. The copy clears a symlink at a directory
		// it is about to create, and the argument for that being sound is that
		// the walk is top-down. That argument covers a link planted *before* the
		// copy; it does not cover one planted by a second copy running at the
		// same moment, which is a symlink TOCTOU (gosec G122) of exactly the
		// shape E162 found in the mount preparation.
		//
		// This engine never does it - `engine/exec` issues a step's copies in
		// order and each step has its own handle - so this is not a hole being
		// closed but a hole being kept shut for clients that are not this one.
		// The server is a protocol, and a protocol's guarantees should not rest
		// on the habits of the caller that happens to be shipped with it.
		unlock := s.lockHandle(req.Handle)

		err := s.copyIn(h, req.From, req.Path, req.Dest,
			copyOpts{
				AsDir: req.DirCopy, NoFollow: req.NoFollow, KeepOwn: req.KeepOwn,
				Chown: req.Chown, Clamp: clampAt(req.Clamp),
			})

		unlock()

		if err != nil {
			return Response{Err: err.Error()}
		}

		return Response{}

	case KindCapture:
		h, ok := s.get(req.Handle)
		if !ok {
			return Response{Err: "unknown handle " + req.Handle}
		}

		// The step's *delta*, not the filesystem it saw. Digesting the merged
		// view would make a one-line change over a 200 MB base produce a 200 MB
		// layer sharing nothing with its predecessor.
		// In store terms, not namespace terms: this digest is what the layer
		// is filed under and what a host recomputes to verify it (E135).
		uids, gids := OwnIDMaps()

		// Before the digest, so the identity this layer gets is the one any
		// other machine computes for the same work (E549). Only when the build
		// asked: unset means keep what each file has.
		if req.Clamp != nil {
			err := clampTree(h.Delta(), time.Unix(*req.Clamp, 0))
			if err != nil {
				return Response{Err: err.Error()}
			}
		}

		// Whatever this guest faulted in is base, not delta (E293). Nil when
		// nothing lazily materialised, and then this is exactly `TakeIn` - which
		// is every build today.
		c, err := layer.TakeExcludingIn(h.Delta(), s.placedIn(req.Handle, h.Delta()), uids, gids)
		if err != nil {
			return Response{Err: err.Error()}
		}

		// Committed into the layer store under its own digest, because the
		// handle's directories are removed on release: a layer that is digested
		// and not persisted is a cache entry pointing at nothing.
		endCommit := timing.Phase("guest:commit", c.ID.String())
		err = s.commit(h.Delta(), c.ID)

		endCommit()
		if err != nil {
			return Response{Err: err.Error()}
		}

		// **The capture already knows.** Materialising a layer has to find out
		// whether it carries deletion markers, and the only way to find out is
		// to look at every entry - so a layer that has just been walked is a
		// layer whose answer is in hand. Written down here, it is never asked
		// again; not written down, it was asked on every materialise, and cost
		// 7.1 seconds of an 8 second build in 36 scans of trees that had each
		// been walked once already (E561).
		//
		// Only the negative is recorded. A layer *with* markers has to be
		// translated whatever anybody noted, and the translation is what leaves
		// the durable evidence.
		if !c.Marked && s.LayerDir != "" {
			store.DirStore(s.LayerDir).NoteUnmarked(c.ID)
		}

		return Response{Layer: c.ID.String(), Content: c.Content.String(), Bytes: c.Bytes}

	case KindPackImage:
		if s.LayerDir == "" {
			return Response{Err: "pack-image: this guest was started without a" +
				" layer directory, so it has no store to pack from"}
		}

		if req.Image == nil {
			return Response{Err: "pack-image: no image was described"}
		}

		into, err := ir.ParseNodeID(req.Into)
		if err != nil {
			return Response{Err: "pack-image: " + err.Error()}
		}

		ids, err := decodeStack(req.Stack)
		if err != nil {
			return Response{Err: "pack-image: " + err.Error()}
		}

		err = packImageInto(s.LayerDir, into, ids, req.Image.Spec())
		if err != nil {
			return Response{Err: err.Error()}
		}

		return Response{}

	case KindSquash:
		if s.LayerDir == "" {
			return Response{Err: "squash: this guest was started without a layer" +
				" directory, so it has no store to merge into"}
		}

		into, err := ir.ParseNodeID(req.Into)
		if err != nil {
			return Response{Err: "squash: " + err.Error()}
		}

		rng, err := decodeStack(req.Stack)
		if err != nil {
			return Response{Err: "squash: " + err.Error()}
		}

		err = store.DirStore(s.LayerDir).Squash(ctx, into, rng)
		if err != nil {
			return Response{Err: err.Error()}
		}

		return Response{}

	case KindStoreHas:
		ids, err := decodeStack(req.Stack)
		if err != nil {
			return Response{Err: "store-has: " + err.Error()}
		}

		// An unset store is not an empty store. `DirStore("")` joins to a
		// *relative* path, so the answer would come from whatever directory
		// this process happens to be in - and "no" from the wrong place is
		// indistinguishable from "no", so the build would quietly rebuild
		// everything it already had.
		if s.LayerDir == "" {
			return Response{Err: "store-has: this guest was started without a" +
				" layer directory, so it cannot say what the store holds" +
				" (set EARTH_GUEST_ROOT, or Server.LayerDir)"}
		}

		st := store.DirStore(s.LayerDir)

		held := make([]string, 0, len(ids))

		for _, id := range ids {
			if st.Has(id) {
				held = append(held, id.String())
			}
		}

		return Response{Held: held}

	case KindUnpackLayer:
		return s.unpackLayer(req)

	case KindFileConfig:
		return s.fileConfig(req)

	case KindViewDigests:
		return s.viewDigests(ctx, req)

	case KindRelease:
		s.mu.Lock()
		h, ok := s.handles[req.Handle]
		delete(s.handles, req.Handle)
		s.mu.Unlock()

		// Releasing an unknown handle is not an error. Cleanup paths run more
		// than once, and a second release must not fail in a way that masks the
		// first error.
		if !ok {
			return Response{}
		}

		err := h.Release()
		if err != nil {
			return Response{Err: err.Error()}
		}

		return Response{}

	default:
		return Response{Err: "unknown request " + string(req.Kind)}
	}
}

// isDir reports whether a path is an existing directory.
func isDir(p string) bool {
	fi, err := os.Stat(p)

	return err == nil && fi.IsDir()
}

// CopyOpts is how a COPY differs from the plain one, across the wire.
//
// Exported because the executor builds one: it is the only way to add the
// second flag without giving Copy two adjacent bools, where transposing them
// compiles and produces a build that copies the wrong thing.
type CopyOpts = copyOpts

// copyOpts is how a COPY differs from the plain one.
//
// A struct rather than a second trailing bool, and the zero value is what the
// engine already did: a call site converted without thinking keeps following
// links, which is the direction that cannot silently change a build's meaning.
// `Follow bool` would have inverted that - every unconverted caller would have
// stopped following, and nothing would have said so.
type copyOpts struct {
	// AsDir is `--dir`: the directory itself rather than its contents.
	AsDir bool
	// NoFollow is `--symlink-no-follow`: a symlink the copy names arrives as a
	// link rather than as what it points at.
	NoFollow bool
	// KeepOwn is `--keep-own`: uid and gid travel with the copy.
	//
	// Off by default because that is what both engines already did - a file
	// owned by 65534 arrives as root unless the flag asks otherwise (E34).
	// Copying ownership always would put uids from the building machine into
	// images that run somewhere else.
	KeepOwn bool
	// Chown is `--chown=user[:group]`: what the copy belongs to, resolved
	// against the destination image rather than this machine (E419).
	//
	// Distinct from KeepOwn, which takes the source's ownership. The two are
	// refused together by the interpreter: one says "whatever it was" and the
	// other names something else.
	Chown string
	// chownIDs is the resolved pair, filled once per copy so the passwd file is
	// read once rather than per file.
	chownUID, chownGID int
	// Clamp is the time everything this copy writes should carry, or nil to
	// keep what each file has.
	//
	// Carried per operation rather than read from the environment. The guest
	// *was* given `SOURCE_DATE_EPOCH` at boot and it worked, until a second
	// build reused the sandbox: one machine serves builds that may each want a
	// different epoch, so the value has to arrive with the work (E549).
	Clamp *time.Time
}

// stamp is the time to write on a file this copy places.
func (o copyOpts) stamp(actual time.Time) time.Time {
	return fstime.Stamp(o.Clamp, actual)
}

// copyIn implements COPY: it takes a path out of the stack an artifact came
// from and places it in the step's filesystem.
//
// A *stack*, not one layer: an artifact need not be made by its target's last
// step. Clojure's build runs `lein uberjar`, extracts a version from the jar,
// and then saves the jar - so the jar is two layers down, and reading the
// producing node's own layer said the pattern matched nothing, which was true
// of that layer and false of the target.
func (s *Server) copyIn(h core.Handle, from []string, src, dest string, opts copyOpts) error {
	if s.LayerDir == "" {
		return errors.New("no layer store configured, so there is nothing to copy from")
	}

	// Asked before anything is copied, because the answer is about the store
	// and not about this path: a flag that is going to be lost should say so
	// instead of producing an image whose files belong to the wrong user (A2,
	// I10). Once per process - it is a filesystem property.
	if opts.KeepOwn {
		err := s.own.check(s.LayerDir)
		if err != nil {
			return err
		}
	}

	// Resolved once, against the image the copy lands in, before anything is
	// written: a name the image does not have must fail the copy rather than
	// leave half of it owned by the wrong user (E419).
	if opts.Chown != "" {
		uid, gid, err := chownIDs(h.Root(), opts.Chown)
		if err != nil {
			return err
		}

		opts.chownUID, opts.chownGID = uid, gid
	}

	srcPaths, err := s.findInStack(from, src)
	if err != nil {
		return err
	}

	dstPath, err := within(h.Root(), dest)
	if err != nil {
		return err
	}

	// The newest layer decides what the source *is*, as a mount would: a file
	// written over a directory of the same name replaces it entirely, and only
	// a directory is merged with what is underneath.
	srcPath := srcPaths[len(srcPaths)-1]

	// What the source *is* is asked of the thing the link names, and the name
	// it lands under still comes from the link: `COPY --dir link /placed` gives
	// /placed/link holding the tree, not /placed/real. Resolving one line
	// earlier would have been correct and unfindable.
	resolved := srcPath.path

	if !opts.NoFollow {
		followed, followErr := resolveLast(srcPath.root, srcPath.path)
		if followErr != nil {
			return fmt.Errorf("COPY %s: %w", src, followErr)
		}

		resolved = followed
	}

	fi, err := os.Lstat(resolved)
	if err != nil {
		return fmt.Errorf("COPY %s: %w", src, err)
	}

	if !fi.IsDir() {
		srcPaths = srcPaths[len(srcPaths)-1:]
	}

	// Where the source lands, and the source's own kind decides it.
	//
	// A *file* goes inside a destination that is a directory: `COPY x /app/`
	// places, `COPY x config.json` renames. A *directory* contributes its
	// contents instead, unless `--dir` asked for the directory itself -
	// `COPY src .` under WORKDIR /code puts main.cpp at /code/main.cpp, and the
	// next line of that Earthfile says `gcc -c main.cpp`.
	//
	// A trailing separator alone cannot express this, and was being asked to:
	// right for a file, wrong for a directory, and `COPY src .` put the whole
	// tree at /code/src where nothing could find it.
	// Nothing is placed *inside* a destination that is not already a directory,
	// and that single condition decides both flavours: `cp -r`, exactly.
	//
	// `COPY --dir tree /placed` gives /placed/tree when /placed exists and
	// /placed itself when it does not. A file goes inside on the same terms;
	// otherwise it takes the destination's name. A directory without `--dir`
	// contributes its contents and is never joined.
	//
	// The engine had this wrong in both directions at once, which is why it
	// took a four-case matrix against the reference to see: `--dir` joined even
	// with no destination to join to, and an artifact never joined at all
	// because the interpreter cancelled the flag to compensate. Each error hid
	// the other, and every test agreed with both because they were written from
	// the same misreading (E48).
	// What the copy looked at in its base, recorded before it is acted on: the
	// destination's own kind decides where the source lands, and nothing else
	// about the base is consulted. This is the engine's first real observation
	// source (E119) and it needs no tracing mechanism, because the guest does
	// these reads itself.
	s.observeDest(h, dstPath, dest)

	intoDir := strings.HasSuffix(dest, "/") || isDir(dstPath)
	if intoDir && (opts.AsDir || !fi.IsDir()) {
		dstPath = filepath.Join(dstPath, filepath.Base(srcPath.path))
	}

	// 0755 deliberately: this becomes part of the image, and a directory a
	// non-root user in that image cannot traverse is a build that works here
	// and fails wherever the image is run.
	err = mkdirAllStamped(filepath.Dir(dstPath), 0o755, opts.Clamp)
	if err != nil {
		return fmt.Errorf("create the destination directory for %s: %w", dest, err)
	}

	// Oldest first, so a newer layer's version of an entry lands last and wins.
	//
	// mtimes carry over, because a COPY that stamped every file with the current
	// time would defeat every downstream tool that compares timestamps - which is
	// most build systems, and the reason I8 exists. copyPath does that for a file
	// and for a tree by the same rule, including the clamp.
	for _, p := range srcPaths {
		err = copyPath(p.root, p.path, dstPath, opts)
		if err != nil {
			return err
		}
	}

	return nil
}

// export copies an artifact out of a step's filesystem into the shared store,
// where the host can reach it.
//
// Taken from the merged view rather than from the delta: `SAVE ARTIFACT` names a
// path in the filesystem the step *saw*, which may come from the base image and
// never have been written by this step at all.
//
// The host cannot read the guest's filesystem - that is the whole reason the
// guest exists (experiment E1b) - so the copy happens here and the host collects
// it from the mount they share.
func (s *Server) export(h core.Handle, path, dest string, clamp *time.Time) error {
	// Timed on this side of the boundary, because the host's `export:stage`
	// covers a round trip as well as the work and 0.35s of a 1.16s build was
	// being attributed to a copy without anybody having measured the copy
	// (E567, E568).
	defer timing.Phase("guest:export", path)()

	if s.LayerDir == "" {
		return errors.New("no shared store configured, so an artifact cannot be handed back")
	}

	// The path is resolved inside the step's root and must stay there. It comes
	// from the Earthfile, and an Earthfile is not necessarily written by the
	// person running it.
	src, err := within(h.Root(), path)
	if err != nil {
		return err
	}

	dst, err := within(filepath.Join(s.LayerDir, "exports"), dest)
	if err != nil {
		return err
	}

	// copyPath resolves and stats too, and this one is kept for its wording
	// alone: it names the path the Earthfile asked for, where the copy would
	// name the resolved one inside a sandbox root the author never wrote down.
	//
	// It resolves by the same rule as the copy rather than by os.Stat's, or a
	// link the copy would follow correctly is refused here first, and the
	// engine's answer to `SAVE ARTIFACT link` depends on which of two checks
	// happens to run.
	resolved, err := resolveLast(h.Root(), src)
	if err != nil {
		return fmt.Errorf("SAVE ARTIFACT %s: %w", path, err)
	}

	_, err = os.Lstat(resolved)
	if err != nil {
		return fmt.Errorf("SAVE ARTIFACT %s: %w", path, err)
	}

	// Inside the store, so private (gosec G301): the mode of a directory this
	// engine makes to hold its own files is not part of what a build produced.
	err = os.MkdirAll(filepath.Dir(dst), 0o750)
	if err != nil {
		return fmt.Errorf("prepare the export directory: %w", err)
	}

	// The time as well as the contents, for a file exactly as for a tree. When
	// these were two pieces of code, `SAVE ARTIFACT` of a directory kept its
	// timestamps and `SAVE ARTIFACT` of a file quietly stamped it with the
	// moment the build ran (I8). The asymmetry is what made it invisible:
	// whichever one you looked at, the other was the broken one.
	done := timing.Phase("guest:export:copy", path)
	err = copyPath(h.Root(), src, dst, copyOpts{Clamp: clamp})
	done()
	return err
}

// within joins a path onto a root and refuses anything that leaves it.
func within(root, p string) (string, error) {
	clean := filepath.Clean("/" + p)

	joined := filepath.Join(root, clean)
	if joined != root && !strings.HasPrefix(joined, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q leaves %s", p, root)
	}

	return joined, nil
}

// ExportCommit is commit, reachable from tests in this package's test binary.
// The behaviour it exposes - that an already-present layer is left alone - is
// the deduplication property, and it is worth testing directly rather than
// through a whole build.
func ExportCommit(_ context.Context, store, delta string, id ir.NodeID) error {
	s := &Server{LayerDir: store}

	return s.commit(delta, id)
}

// commit moves a captured delta into the layer store under its digest.
//
// A rename where possible, a copy otherwise: the delta and the store may be on
// different filesystems, since scratch is deliberately local while the store is
// shared (green paper §3.3b).
func (s *Server) commit(delta string, id ir.NodeID) error {
	if s.LayerDir == "" {
		return nil // no store configured; the caller keeps the digest and nothing else
	}

	dst := store.LayerStore(s.LayerDir).Path(id)

	_, err := os.Stat(dst)
	if err == nil {
		// Already present. Two steps producing identical output is the good case,
		// not a collision - the digest says they are the same layer.
		return nil
	}

	// As above: the layer store is the engine's, and its directories are not
	// the build's output.
	err = os.MkdirAll(filepath.Dir(dst), 0o750)
	if err != nil {
		return fmt.Errorf("prepare the layer store: %w", err)
	}

	// Copied, never renamed *from* the delta. The obvious optimisation - rename
	// when both are on one filesystem - is wrong here: the delta is the upper
	// directory of a live overlay mount, and moving it out from under the mount
	// leaves the merged view referring to a directory that is no longer there.
	// The symptom is ELOOP on the next mount that stacks it, naming nothing
	// about the cause.
	//
	// The copy goes to a temporary name and is then renamed into place, so a
	// layer is either wholly there or not there at all. Without that, a crash
	// mid-copy leaves a directory that looks like a complete layer and is
	// missing half of it - which a later build would cheerfully use as a base.
	// **The staging name is asked for, not derived.** `<id>.partial` is the
	// same path for every builder of that layer, so two builds committing one
	// layer race: the second's `RemoveAll` deletes the first's half-copied
	// tree, and the first then renames whatever survived - or fails on a file
	// that has gone. Third instance of a derived name that had to be unique,
	// after the mount directories (E140) and the whiteout translations (E142).
	//
	// A derived name is unique among the things the deriver knows about, and a
	// second process is not one of them.
	tmp, err := os.MkdirTemp(filepath.Dir(dst), "."+id.String()+".partial-")
	if err != nil {
		return fmt.Errorf("stage a commit of layer %s: %w", id, err)
	}

	// **Ownership travels with it.** The copy is the layer store's own, so the
	// files it writes belong to whoever ran it - and that flattened every uid a
	// step had set to the invoking user, which inside a step's namespace reads
	// as root. `COPY --keep-own` then reported 0 for a file the build had
	// deliberately chowned, and the copy that lost it is here rather than
	// anywhere near the option (E446).
	//
	// The guest can restore any id its namespace maps, which is the delegated
	// range - the same reason a step's own `chown testuser:testuser` works at
	// all. Where an id cannot be restored, `copyTree` says so rather than
	// carrying on: a layer whose ownership is not what its digest says is a
	// layer two machines cannot agree about (E313).
	err = copyTree(delta, tmp, copyOpts{KeepOwn: true})
	if err != nil {
		_ = os.RemoveAll(tmp)

		return err
	}

	// Losing to another build committing the same layer is a race worth losing,
	// and `Publish` is where that is said once for every caller that files one.
	err = store.Publish(s.LayerDir, id, tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)

		return fmt.Errorf("commit layer %s: %w", id, err)
	}

	return nil
}

// noteDegraded records why limits could not be applied. Visible to the host so
// an unbounded build is a known state rather than an assumed one.
func (s *Server) noteDegraded(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.degraded = reason
}

// Degraded reports why resource limits were not applied, if they were not.
func (s *Server) Degraded() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.degraded
}

// lockHandle serialises filesystem work against one handle, and returns the
// release.
//
// Keyed by handle id rather than held on the handle, because core.Handle is an
// interface implemented by four types and only one of them has a filesystem to
// contend over. The mutex is never removed: one per handle for the life of a
// guest is a few dozen words, and reclaiming them would need a second lock to
// decide when nobody is waiting.
func (s *Server) lockHandle(id string) func() {
	s.lockMu.Lock()

	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}

	mu, ok := s.locks[id]
	if !ok {
		mu = &sync.Mutex{}
		s.locks[id] = mu
	}

	s.lockMu.Unlock()

	mu.Lock()

	return mu.Unlock
}

func (s *Server) get(id string) (core.Handle, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.handles[id]

	return h, ok
}

// Client is the host half: a core.Materialiser that does its work in the guest.
//
// The scheduler cannot tell it apart from a local one, which is the point of
// the port - and is why the same conformance suite applies.
type Client struct {
	c *conn

	// Terminals is the other end of the server's descriptor channel. Nil means
	// this client cannot ask for an interactive step.
	Terminals *net.UnixConn

	mu      sync.Mutex
	next    uint64
	pending map[uint64]chan Response
	sinks   map[uint64]func(string)
	dead    error
	// degraded is why the guest could not apply a step's resource limits. The
	// first reason is kept: they are all the same reason in practice, and a
	// build reporting it once is read while a build reporting it per step is
	// not (E123).
	degraded string
}

// Degraded reports why steps ran without the resource limits they were given,
// or empty if they did not.
//
// Asked of the client rather than announced, so the caller decides when a
// build-level warning belongs in its output - the same shape as the
// case-insensitive store warning.
func (c *Client) Degraded() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.degraded
}

// noteDegraded records the first reason a step could not be limited.
func (c *Client) noteDegraded(reason string) {
	if reason == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.degraded == "" {
		c.degraded = reason
	}
}

// Dial performs the version handshake and returns a client.
func Dial(rw io.ReadWriter) (*Client, error) {
	return dialWithin(rw, greetingAtMost)
}

// dialWithin is Dial with the handshake bound named rather than assumed.
//
// The bound is a parameter so the test that proves the handshake gives up does
// not have to sit out the production thirty seconds to watch it: what is on
// trial is that a guest which never speaks is reported rather than waited for,
// and that is the same fact at fifty milliseconds.
func dialWithin(rw io.ReadWriter, within time.Duration) (*Client, error) {
	cl := &Client{c: newConn(rw), pending: map[uint64]chan Response{}, sinks: map[uint64]func(string){}}

	// The handshake is exchanged *synchronously*, before the demultiplexer
	// starts, and must stay expressible in the oldest dialect the protocol has
	// ever spoken.
	//
	// Version negotiation that depends on the newest feature cannot negotiate.
	// Doing this through the multiplexed path made a version-1 guest - which
	// replies without an id - leave the host waiting forever for a reply it
	// would never match, so the mismatch check could not run and a stale guest
	// hung the build instead of being refused.
	err := cl.c.send(Request{Kind: KindHello, Version: Version})
	if err != nil {
		return nil, fmt.Errorf("greet the guest: %w", err)
	}

	// Bounded, because a guest that connects and never greets is otherwise a
	// build that never starts and cannot be interrupted - the same unbounded
	// wait the release had, one step earlier and before there is anything to
	// interrupt (E442).
	//
	// A goroutine and a select rather than a read deadline: what arrives here is
	// an `io.ReadWriter`, which may be a pipe, a socket or a test double, and
	// only some of those can be given one. The reader is left blocked on a
	// connection the caller is about to close.
	type greeting struct {
		resp Response
		err  error
	}

	said := make(chan greeting, 1)

	go func() {
		var resp Response

		said <- greeting{resp, cl.c.recv(&resp)}
	}()

	var resp Response

	select {
	case g := <-said:
		resp, err = g.resp, g.err
	case <-time.After(within):
		return nil, fmt.Errorf(
			"the guest did not answer the handshake within %s"+
				"\n  it is running and not speaking: check the sandbox agent is"+
				" the one this build produced", within)
	}

	if err != nil {
		return nil, fmt.Errorf("the guest did not answer the handshake: %w", err)
	}

	if resp.Err != "" {
		return nil, errors.New(resp.Err)
	}

	if resp.Version != Version {
		return nil, fmt.Errorf(
			"guest speaks protocol %d, host speaks %d"+
				"\n  the sandbox agent is from a different build of earthbuild"+
				"\n  rebuild earth-guestd, or point $EARTH_GUESTD at a matching one",
			resp.Version, Version)
	}

	go cl.read()

	return cl, nil
}

// read demultiplexes replies onto the callers waiting for them.
//
// One goroutine owns the read side for the connection's lifetime, which is what
// makes concurrent requests safe: nothing else ever reads a frame, so no two
// callers can take each other's reply.
func (c *Client) read() {
	for {
		var resp Response

		err := c.c.recv(&resp)
		if err != nil {
			c.fail(fmt.Errorf("guest connection lost: %w", err))

			return
		}

		// A streaming frame is progress, not a reply: the request stays in
		// flight and the caller keeps waiting.
		if resp.Streaming {
			c.mu.Lock()
			sink := c.sinks[resp.ID]
			c.mu.Unlock()

			if sink != nil {
				sink(resp.Chunk)
			}

			continue
		}

		c.mu.Lock()
		ch, waiting := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		delete(c.sinks, resp.ID)
		c.mu.Unlock()

		if !waiting {
			// A reply to a request nobody is waiting for: a duplicate, or one
			// whose caller gave up. Dropped rather than treated as fatal - the
			// connection is still good for everyone else.
			continue
		}

		ch <- resp
	}
}

// fail wakes every outstanding caller when the connection dies.
//
// Without it they wait for a reply that can never arrive, and a build that has
// lost its sandbox hangs instead of reporting why.
func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.dead = err

	for id, ch := range c.pending {
		ch <- Response{ID: id, Err: err.Error()}
		delete(c.pending, id)
	}
}

// do is doStream with nowhere for output to go.
//
// It took no context and passed `context.Background()`, so every request that
// was not an exec - materialise, capture, export, copy, release - waited on
// something nobody could cancel. Four of the methods calling it accept a
// `context.Context` and named the parameter `_`, which is the defect written
// down: a signature promising cancellation over a body discarding it (E177).
func (c *Client) do(ctx context.Context, req Request) (Response, error) {
	return c.doStream(ctx, req, nil)
}

// doStream is do, with somewhere for a running step's output to go, and a
// context that can abandon the wait.
func (c *Client) doStream(ctx context.Context, req Request, sink func(string)) (Response, error) {
	ch := make(chan Response, 1)

	c.mu.Lock()

	if c.dead != nil {
		err := c.dead
		c.mu.Unlock()

		return Response{}, err
	}

	c.next++
	req.ID = c.next
	c.pending[req.ID] = ch

	if sink != nil {
		c.sinks[req.ID] = sink
		req.Stream = true
	}

	c.mu.Unlock()

	err := c.c.send(req)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, req.ID)
		delete(c.sinks, req.ID)
		c.mu.Unlock()

		return Response{}, err
	}

	// Waited for, or abandoned. A step is the one request here that can take
	// minutes, so the caller's context has to reach it: without this a build
	// could not be interrupted while a step ran, and Ctrl-C during a long
	// compile was a wait for the compile (E56).
	var resp Response

	select {
	case resp = <-ch:
	case <-ctx.Done():
		// Tell the guest before returning. Returning first would leave a step
		// running in a sandbox the host has stopped tracking and is about to
		// release the handle of - which is a worse answer than the wait, and
		// the reason this needed a protocol message rather than a `select`.
		//
		// Sent on a fresh request of its own, and its reply is not waited for:
		// the caller has already stopped waiting, and the cancel is the last
		// thing it wants to block on.
		c.mu.Lock()
		delete(c.pending, req.ID)
		delete(c.sinks, req.ID)
		c.next++
		cancelID := c.next
		c.mu.Unlock()

		_ = c.c.send(Request{ID: cancelID, Kind: KindCancel, Cancel: req.ID})

		return Response{}, ctx.Err()
	}

	if resp.Err != "" {
		return Response{}, errors.New(resp.Err)
	}

	return resp, nil
}

// MaterialisePrepared uses a base somebody has already assembled.
//
// The lazy path (E300, E302): a directory primed with the paths a step was
// predicted to read, sent as a fact rather than as a stack of layer ids - which
// it is not, because a fragment is never a layer (E281).
func (c *Client) MaterialisePrepared(ctx context.Context, root string) (core.Handle, error) {
	resp, err := c.do(ctx, Request{Kind: KindMaterialise, Prepared: root})
	if err != nil {
		return nil, err
	}

	return &remoteHandle{c: c, id: resp.Handle, root: resp.Root, declares: declaresIn(resp)}, nil
}

// Materialise implements core.Materialiser.
func (c *Client) Materialise(ctx context.Context, stack []ir.NodeID) (core.Handle, error) {
	resp, err := c.do(ctx, Request{Kind: KindMaterialise, Stack: encodeStack(stack)})
	if err != nil {
		return nil, err
	}

	return &remoteHandle{c: c, id: resp.Handle, root: resp.Root, declares: declaresIn(resp)}, nil
}

// HandleID is the name the guest knows this handle by.
//
// Exported because a fault-in says which base it is for (E303), and whoever
// primed that base has to be able to record it under the same name the guest
// will use.
func (h *remoteHandle) HandleID() string { return h.id }

type remoteHandle struct {
	c    *Client
	id   string
	root string

	// declares is what the guest's materialiser said this stack declares,
	// carried back with the handle rather than read out of the store (E554).
	declares decl.Declaration

	mu       sync.Mutex
	released bool
}

func (h *remoteHandle) Root() string { return h.root }

// Declaration is what the materialised stack declares.
func (h *remoteHandle) Declaration() decl.Declaration { return h.declares }

// Declared is the environment half of it, which is what the step's environment
// is folded onto.
func (h *remoteHandle) Declared() []string { return h.declares.Env }

// Delta is not meaningful across the wire: the host cannot see the guest's
// filesystem, which is why committing happens in the guest.
func (h *remoteHandle) Delta() string { return "" }

// errNoProgress is a guest that promises more entries and sends none.
var errNoProgress = errors.New("the guest asked for another page and sent no entries")

// absorb folds one page of an observe reply into the observation being built.
//
// The guest's admissions are carried through rather than recomputed: a host that
// decoded the paths and dropped `Incomplete` would turn a careful source into a
// lying one. They travel on every page, so the last page cannot quietly clear
// what an earlier one admitted - `Incomplete` only ever goes one way.
func absorb(obs *core.Observation, resp Response) {
	for p, d := range resp.Reads {
		ids, err := decodeStack([]string{d})
		if err == nil {
			obs.Reads[p] = ids[0]
		}
	}

	for p, d := range resp.Listings {
		ids, err := decodeStack([]string{d})
		if err == nil {
			obs.Listings[p] = ids[0]
		}
	}

	obs.Negative = append(obs.Negative, resp.Negative...)

	if resp.Incomplete {
		obs.Incomplete = true
	}

	obs.Why = append(obs.Why, resp.Why...)
}

// unfetchedObservation is what a step observed when the answer never arrived.
//
// **Incomplete, and with the reason attached.** The error used to be discarded
// and an empty observation returned, so a step whose observation was too large
// to send - around 16 MB, which this repository's own `+unit-test` reaches -
// was reported as "nothing observed this step" (E620). That is false: something
// was watching and it saw a great deal, and what failed was delivering it. I11
// asks for a degradation that says so, and this was doing only the first half.
//
// Incomplete matters as much as the reason. An empty observation offered as fact
// agrees with every base there is, which is exactly the false hit I3 forbids.
func unfetchedObservation(err error) core.Observation {
	return core.Observation{
		Reads:      map[string]ir.NodeID{},
		Listings:   map[string]ir.NodeID{},
		Incomplete: true,
		Why:        []string{"the step's observation never arrived: " + err.Error()},
	}
}

func (h *remoteHandle) Observations() core.Observation {
	obs := core.Observation{
		Reads:    map[string]ir.NodeID{},
		Listings: map[string]ir.NodeID{},
	}

	// **Fetched in pages, because a large one does not fit in a frame.** A step
	// whose observation exceeded 16 MiB used to be reported as watched by
	// nothing at all, losing it the second cache tier - and the steps that
	// exceed it are the expensive ones (E620, E621).
	//
	// No caller context: an observation is asked for while the step's
	// result is being assembled, and there is nobody left to cancel it.
	from := 0

	for page := 0; ; page++ {
		resp, err := h.c.do(context.Background(),
			Request{Kind: KindObserve, Handle: h.id, FromEntry: from})
		if err != nil {
			// An observation that cannot be fetched is an absent observation,
			// never an empty one presented as fact: silence must not be
			// recorded as "read nothing", or every later step would falsely
			// satisfy it. A page that fails discards what came before it for
			// the same reason - half an observation names fewer paths than the
			// step read, which is precisely the false hit I3 forbids.
			return unfetchedObservation(err)
		}

		absorb(&obs, resp)

		if !resp.More {
			break
		}

		next := from + len(resp.Reads) + len(resp.Listings) + len(resp.Negative)
		if next <= from {
			// A guest that says "more" and sends nothing would spin here. It
			// cannot happen while both sides agree about paging, which is why
			// it is checked rather than assumed.
			return unfetchedObservation(errNoProgress)
		}

		from = next
	}

	// A gap with no reason is still a gap, but it is one nobody can act on -
	// and the whole point of carrying reasons is that "this step never earns an
	// L2 hit" should be a sentence rather than a mystery. An unnamed one says so.
	if obs.Incomplete && len(obs.Why) == 0 {
		obs.Why = []string{"the guest did not say why"}
	}

	return obs
}

func (h *remoteHandle) Release() error {
	return h.releaseWithin(releaseAtMost)
}

// releaseWithin is Release with the deadline named rather than assumed, so the
// test that proves a deaf guest is given up on need not wait the minute a real
// one is given.
func (h *remoteHandle) releaseWithin(atMost time.Duration) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.released {
		return nil
	}

	h.released = true

	// Release runs from a cleanup, after whatever context the caller had is
	// gone, so it makes one of its own rather than borrowing a cancelled one.
	//
	// **With a deadline.** It used `context.Background()`, which is not a
	// context of its own but no bound at all: a guest that was alive and not
	// answering stopped the build for ever, in a deferred call during teardown
	// where nothing is left to interrupt it. The execution gate sat on one
	// target for thirteen minutes under a sixty-second deadline, and the
	// goroutine dump put the wait here (E442).
	//
	// Long enough that a busy guest finishes - unmounting an overlay under load
	// is seconds, not minutes - and short enough that a stuck one is reported
	// while somebody is still watching.
	ctx, stop := context.WithTimeout(context.Background(), atMost)
	defer stop()

	_, err := h.c.do(ctx, Request{Kind: KindRelease, Handle: h.id})
	if err != nil {
		return fmt.Errorf("release the step's filesystem: %w", err)
	}

	return nil
}

// greetingAtMost bounds the handshake.
//
// Generous: the guest may be starting inside a fresh namespace with a cold page
// cache. Bounded all the same - "slow to start" and "never going to answer" look
// identical from here, and only one of them is worth waiting for.
const greetingAtMost = 30 * time.Second

// releaseAtMost bounds the wait for a guest to let go of a handle.
//
// A number rather than a caller's choice: every caller of Release is a cleanup,
// and a cleanup that took a deadline from the code around it would inherit the
// cancellation that just fired.
const releaseAtMost = 60 * time.Second

// outputFor is the output a reply carries.
//
// **Nothing, when the host asked for a stream.** Every byte has already crossed
// as chunks, and `core.StepError` prints the reply's copy only `if !e.Streamed` -
// so repeating it transfers megabytes to be discarded. This repository's own
// `+unit-test` produces about nineteen of them, which is past the frame limit:
// the reply could not be written, and the build failed and then hung on it
// (E617).
//
// A step the host did not ask to stream still gets its output here, because
// nothing else delivered it.
func outputFor(req Request, out []byte) string {
	if req.Stream {
		return ""
	}

	return string(out)
}

// streamer returns a sink that forwards a step's output to the host as it
// appears, or nil when the host did not ask for it.
func streamer(c *conn, req Request) func([]byte) {
	if !req.Stream || c == nil {
		return nil
	}

	return func(b []byte) {
		// A send failure means the connection is gone; the read loop will find
		// out. Failing the step because its *progress* could not be delivered
		// would turn a cosmetic problem into a build failure.
		_ = c.send(Response{ID: req.ID, Streaming: true, Chunk: string(b)})
	}
}

// run executes a command, returning its combined output and feeding sink as it
// arrives.
//
// cmd.CombinedOutput would be shorter and would hold everything until the step
// ends, which is exactly the silence this exists to remove.
func run(cmd *osexec.Cmd, sink func([]byte)) ([]byte, error) {
	// Already attached to something: an interactive step owns a terminal, and
	// its output is going there. Capturing as well is not possible - Output and
	// CombinedOutput refuse a command whose Stdout is set, which is how this was
	// found - and streaming as well would take the terminal away from the step
	// by overwriting the field.
	//
	// Nothing is returned because nothing was collected. The caller watching the
	// terminal has already seen it, and a second copy of an interactive session
	// is not something anybody asked for.
	if cmd.Stdout != nil {
		return nil, cmd.Run() //nolint:wrapcheck // the caller classifies this
	}

	if sink == nil {
		return cmd.CombinedOutput() //nolint:wrapcheck // the caller classifies this
	}

	var (
		mu  sync.Mutex
		buf []byte
	)

	// One writer for both streams, so interleaving is preserved as the step
	// produced it rather than reordered by which pipe drained first.
	w := writerFunc(func(b []byte) (int, error) {
		mu.Lock()
		buf = append(buf, b...)
		mu.Unlock()

		sink(b)

		return len(b), nil
	})

	cmd.Stdout, cmd.Stderr = w, w

	// **`Wait` waits for the copying, not just the child.** `Stdout` here is not
	// an `*os.File`, so `os/exec` makes an OS pipe and a goroutine to drain it,
	// and `Wait` returns only once that goroutine sees EOF - which needs every
	// holder of the write end to close it. A process the step left running in
	// the background inherited that end, so a step that exited promptly could
	// leave this guest blocked for ever, and the host waiting for a reply that
	// was never coming (E519).
	//
	// The bound starts when the child exits, so an ordinary step pays nothing.
	cmd.WaitDelay = stepWaitDelay

	err := cmd.Run()

	// **A step that left something behind still succeeded.** When the delay
	// elapses, `os/exec` closes the pipes and reports `ErrWaitDelay` - which is
	// news about the plumbing, not about the command. Reporting it as a failure
	// would fail builds that worked, and for a reason nobody could act on.
	if errors.Is(err, osexec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
		err = nil
	}

	mu.Lock()
	defer mu.Unlock()

	return buf, err //nolint:wrapcheck // the caller classifies this
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

// maxOutput bounds what a step's output can cost the host. A step that prints a
// gigabyte must not be able to exhaust the engine's memory through the
// diagnostic channel.
const maxOutput = 64 << 10

// stepMounts is everything bound into a step, in the order it is applied.
//
// Gathered in one function so that *what a step is given* can be asserted
// without running one. The composition was inline, and the mutation sweep found
// it unguarded: the only test that exercised it was behind the `integration`
// tag, and a sweep that does not build with tags cannot see a mechanism only a
// tagged test guards (E415).
//
// Devices first and unconditionally, because every step is entitled to them;
// then the resolver; then what the request asked for; then the step's own
// `/etc/hosts` where it declared entries - a mount rather than a file written
// into the step's root, because what a step writes into its own filesystem is
// captured and a resolver file is this engine's doing rather than the step's
// output.
func stepMounts(req Request) []Mount {
	out := append(deviceMounts(), resolverMount()...)
	out = append(out, req.Mounts...)

	return append(out, hostsMount(req.Hosts)...)
}

// execRequest runs a command against a materialised handle.
//
// Confinement is applied unless the server is explicitly Unconfined: a chroot
// into the handle's root plus mount, PID, UTS and IPC namespaces, which is what
// makes green paper A3 true rather than assumed. A guest that cannot isolate
// refuses the step.
//
// Resource limits are applied where available and reported where not: an
// unbounded step runs, but Degraded says why it was unbounded rather than
// leaving the caller to assume a ceiling that was never enforced.
func (s *Server) execRequest(ctx context.Context, req Request, c *conn) Response {
	// Everything between here and the command starting: resolving views,
	// holding cache mounts, binding them, building the argv. The host's `run`
	// phase covers the whole round trip and could not tell that apart from the
	// command's own time, which is the difference between an engine that is
	// slow and a step that is.
	// The whole request, so that what is left when prepare and exec are taken
	// off it is the teardown - which runs in defers and is otherwise invisible.
	defer timing.Phase("guest:request", req.Handle)()

	endPrepare := timing.Phase("guest:prepare", req.Handle)

	h, ok := s.get(req.Handle)
	if !ok {
		return Response{Err: "unknown handle " + req.Handle}
	}

	if len(req.Argv) == 0 {
		return Response{Err: "exec with no argv"}
	}

	// Before anything is started, because the alternative is a step running with
	// a socket and nothing behind it - which reads to the author as Docker being
	// broken rather than as this engine declining the request (I10).
	err := checkDaemon(req.Daemon)
	if err != nil {
		return Response{Err: err.Error()}
	}

	// A bare command name is resolved against the *step's* PATH, inside the
	// step's filesystem. Go resolves it against this process's PATH when the
	// command is built, which is the guest's and not the step's - so
	// `docker-entrypoint.sh`, which every RUN --entrypoint produces, was
	// reported as not found while sitting in the image's own /usr/local/bin.
	//
	// It has not come up before because every other step runs `/bin/sh`, and an
	// absolute path needs no resolving.
	// **What the base declares comes from the base**, not from whoever asked for
	// the step. The materialiser walked the stack and folded its declarations
	// (green paper §3.2a), so a delegate derives this exactly as the machine
	// that sent it would - which is the difference between a worker that runs a
	// step correctly and one that runs `go build` without the PATH its image
	// sets.
	declared := declaredBy(h, req)

	argv0 := lookIn(h.Root(), req.Argv[0], stepEnv(declared, req.Env))

	// A context of this step's own, so a cancel naming this request abandons
	// this step and nothing else.
	ctx, kill := context.WithCancel(ctx)
	defer kill()

	endArgv := timing.Phase("guest:argv", req.Handle)

	cmd := osexec.CommandContext(ctx, argv0, req.Argv[1:]...) //nolint:gosec // the argv is the step

	endArgv()
	cmd.Dir = h.Root()

	// The process group, not the process. A confined step is pid 1 of its own
	// pid namespace and takes its children with it; an unconfined one - which
	// is what LOCALLY and the tests use - would leave a shell's children
	// running, still writing into a handle the host is about to release.
	//
	// os/exec calls this only after the process exists, which is why the kill
	// lives here rather than beside the map: reaching for cmd.Process from
	// another goroutine races with Start.
	cmd.Cancel = func() error {
		killErr := killGroup(-cmd.Process.Pid, syscall.SIGKILL)
		if killErr != nil {
			return cmd.Process.Kill() //nolint:wrapcheck // os/exec reports this verbatim
		}

		return nil
	}

	// WORKDIR is relative to the step's own root, and must be created: a
	// WORKDIR naming a directory the base image does not have is ordinary, and
	// failing there would be failing for a reason the author cannot see.
	//
	// The directory is created through the *host* path, because that is the only
	// name the guest can use before the chroot. What the command runs in is set
	// after isolation, below, and must be the path *inside* the new root.
	if req.Dir != "" && req.Dir != "/" {
		dir, withinErr := within(h.Root(), req.Dir)
		if withinErr != nil {
			return Response{Err: withinErr.Error()}
		}

		// The step's WORKDIR, inside the step's own filesystem, so this is a
		// directory the build will be judged on rather than one the engine
		// keeps: it gets the mode the reference gives it.
		//
		// **And a deterministic time, because this is the root of the cascade.**
		// `WORKDIR /earthly` creates a directory that lands in the step's delta,
		// and taking the wall clock gave that layer a new identity on every run.
		// Κ₁ hashes the identities of a step's base (4.5), so every step in the
		// build re-keyed from this one line: 47 of 91 steps showed a moved base
		// between two builds of one commit, while their op, env, platform and
		// node were identical (E577).
		withinErr = mkdirAllStamped(dir, 0o755, clampAt(req.Clamp))
		if withinErr != nil {
			return Response{Err: fmt.Sprintf("create the working directory %s: %v", req.Dir, withinErr)}
		}

		cmd.Dir = dir
	}

	// Bound before the chroot: the source is a path only the guest can name, and
	// afterwards there is no way to reach it.
	//
	// The devices come first and unconditionally, because every step is
	// entitled to them: an image ships an empty /dev and expects the runtime to
	// populate it, and nothing did.
	// A proc filesystem, because the loader resolves $ORIGIN through
	// /proc/self/exe and a step without one cannot run a JDK.
	endProc := timing.Phase("guest:proc", req.Handle)
	undoProc, err := mountProc(h.Root())

	endProc()
	if err != nil {
		return Response{Err: err.Error()}
	}

	defer undoProc()

	mounts := stepMounts(req)

	// A view of an earlier result is a stack and has to be assembled before
	// anything can be bound to it (§3.3d, ν ∈ 𝕂). Released after the step, not
	// after the binding: the mount reads through the handle.
	endViews := timing.Phase("guest:views", req.Handle)
	mounts, releaseViews, err := s.resolveStacks(ctx, mounts)

	endViews()
	if err != nil {
		return Response{Err: err.Error()}
	}

	defer releaseViews()

	// `--sharing=locked`, the default, which was accepted and not provided: two
	// steps naming one cache used it at once (E427). Held for the whole step,
	// because that is what the mode means - a cache is in use until the command
	// holding it finishes, not until its files are opened.
	endHold := timing.Phase("guest:hold", req.Handle)
	releaseMounts := s.mounts.hold(mounts)

	endHold()
	defer releaseMounts()
	if len(mounts) > 0 {
		// Setting mounts up and taking them down are each serialised per
		// handle, and the step between them is not.
		//
		// Two steps on one root bind the same targets. A bind needs a file to
		// land on, so it creates one; the teardown removes the ones it created.
		// Between another step's `ensureFile` and its `mount` that removal is
		// fatal - measured, not supposed:
		//
		//	mount /etc/resolv.conf at /etc/resolv.conf: no such file or directory
		//
		// three times in twelve runs of TestTwoStepsOnOneRootDoNotFightOverTheirMounts,
		// which is 14,400 steps (E173). Holding the lock across the *step* would
		// stop two steps sharing a root from running at once, which is the
		// concurrency the design wants; holding it across these two short
		// sections costs nothing and closes the window.
		unlock := s.lockHandle(req.Handle)
		endBind := timing.Phase("guest:bind", fmt.Sprintf("%d mounts", len(mounts)))
		undo, bindErr := bindMounts(h.Root(), s.mountStore(), s.LayerDir, h.Delta(), mounts)

		endBind()
		unlock()

		if bindErr != nil {
			return Response{Err: bindErr.Error()}
		}

		defer func() {
			unlock := s.lockHandle(req.Handle)
			undo()
			unlock()
		}()
	}

	// An interactive step takes the caller's terminal as its own.
	//
	// Received here rather than relayed: the step owns the descriptor, so
	// `isatty`, window size, raw mode and job control all work, and none of them
	// survives a byte-stream copy (E190).
	if req.Interactive {
		if s.Terminals == nil {
			return Response{Err: "this guest has no terminal channel, so an" +
				" interactive step cannot run here"}
		}

		// One terminal, one prompt. Two steps reading the same descriptor each
		// take some of the keystrokes, which is a wrong session rather than a
		// degraded one.
		if !s.termMu.TryLock() {
			return Response{Err: "another interactive step already holds the terminal"}
		}

		defer s.termMu.Unlock()

		tty, recvErr := fdpass.RecvFile(s.Terminals)
		if recvErr != nil {
			return Response{Err: fmt.Sprintf("take the terminal: %v", recvErr)}
		}

		recvErr = AttachTerminal(cmd, tty)
		if recvErr != nil {
			_ = tty.Close()

			return Response{Err: recvErr.Error()}
		}

		// The step owns it after Start. A copy left open here means the caller's
		// reader never sees the end of the session.
		defer func() { _ = tty.Close() }()
	}

	if !s.Unconfined {
		// Refuse rather than approximate. An unconfined step produces a result
		// that looks cacheable and is not, which is worse than no result: the
		// build appears to have succeeded and the cache is now wrong.
		// Either says yes. A server told to run every step hermetically does
		// not stop being hermetic because this step did not ask, and a step
		// that asked is not overridden by a server that did not.
		endIsolate := timing.Phase("guest:isolate", req.Handle)
		isolateErr := isolate(cmd, h.Root(), s.DropNet || req.NoNet)

		endIsolate()

		if isolateErr != nil {
			return Response{Err: isolateErr.Error()}
		}

		// isolate chroots, so the working directory has to be named from inside
		// the new root - and it sets cmd.Dir itself, which silently discarded
		// the one set above. A step with WORKDIR sub then ran in the root and
		// wrote its output one directory from where every later command looked,
		// while reporting success.
		cmd.Dir = "/"
		if req.Dir != "" {
			cmd.Dir = filepath.Clean("/" + req.Dir)
		}
	}

	endCgroup := timing.Phase("guest:cgroup", req.Handle)
	cg, err := newCgroup(req.Handle, s.Limits)

	endCgroup()

	var (
		deg         *DegradedError
		degradedNow string
	)

	switch {
	case errors.As(err, &deg):
		// Resource limits are not a correctness property, so an unbounded step
		// still runs - but the reason is carried back rather than swallowed,
		// and back *now*: it used to be printed when Serve returned, which is
		// after the build, on a stream nobody reads (E123).
		s.noteDegraded(deg.Reason)

		degradedNow = deg.Reason
	case err != nil:
		return Response{Err: err.Error()}
	}

	defer func() {
		// A cgroup that outlives its step leaks, and thousands of them make a
		// machine unhappy in ways that are hard to trace back here.
		_ = cg.remove()
	}()

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cg.apply(cmd.SysProcAttr)

	// Its own process group, so a cancel takes the whole step and not just the
	// shell at the top of it.
	//
	// Only when unconfined. A confined step is already pid 1 of its own pid
	// namespace and takes its children with it, so this would buy nothing there
	// - and it is not free: the confined path already asks the kernel for a
	// chroot, four namespaces and a cgroup at clone time, and adding a fifth
	// thing to that sequence is a change to the most delicate call in the
	// engine for no gain. LOCALLY and the tests are unconfined, and they are
	// exactly the steps that would otherwise leak children.
	if s.Unconfined {
		ownGroup(cmd)
	}

	// Only ε reaches the step, over a *declared* baseline. Inheriting the
	// guest's environment would let a step observe ambient state that never
	// entered its cache key, which is invariant I3 violated by omission rather
	// than by error - but an empty environment is not the alternative, and
	// pretending otherwise cost a real bug.
	//
	// `cmd.Env = req.Env` inherits the parent's environment when the slice is
	// nil and replaces it entirely when it is not. So an Earthfile with no ENV
	// got the guest's PATH by accident, and one with a single ENV lost it and
	// fell back to the shell's own narrower default - which omits
	// /usr/local/bin, where pip, npm, cargo and docker all put things. The
	// symptom was `sh: docker: not found` on a line whose only crime was
	// following an ENV.
	//
	// The baseline is fixed and written down here, so it is the same on every
	// machine and in every build: constant, not observed, and therefore not
	// something I3 has anything to say about.
	cmd.Env = stepEnv(declared, req.Env)

	// Registered for exactly as long as it runs, so a cancel arriving in the
	// middle finds it and one arriving after finds nothing.
	s.began(req.ID, kill)
	defer s.ended(req.ID)

	// The body, and around it the daemon if this step asked for one.
	//
	// **After the mounts and inside them.** `bindMounts` above has already put
	// the cache where the step will see it, in this process's mount namespace,
	// so a daemon started here writes its storage through that bind (E370). A
	// daemon started *before* the mounts would write into the step's overlay
	// instead, and a named cache would be silently empty on every build - a
	// cache that misses forever and reports nothing.
	var out []byte

	endPrepare()

	body := func() error {
		var rerr error

		defer timing.Phase("guest:exec", req.Handle)()

		out, rerr = runStep(cmd, streamer(c, req), req, s, h, mountPoints(mounts))

		return rerr
	}

	if req.Daemon == nil {
		err = body()
	} else {
		err = withDaemon(ctx, h.Root(), req.Daemon, launchDockerd, publishSocket, body)
	}

	if len(out) > maxOutput {
		out = out[:maxOutput]
	}

	if exitErr, ok := errors.AsType[*osexec.ExitError](err); ok {
		// The step ran and failed. That is a result.
		// A step that ran and failed spent time too, and a build asked for its
		// stats wants the expensive failure counted (E467).
		cpu, rss := usageOf(cmd.ProcessState)

		return Response{
			Exit: exitErr.ExitCode(), Output: outputFor(req, out), Degraded: degradedNow,
			CPUNanos: cpu.Nanoseconds(), MaxRSS: rss,
		}
	}

	if err != nil {
		// The step could not be started at all - a missing binary, a bad
		// working directory, a call the kernel refused. That is a protocol
		// error, not a build result.
		//
		// The facts are gathered here rather than left to the reader, because
		// the message names the binary and the binary is usually not the
		// problem: `fork/exec /bin/sh: operation not permitted` has been seen
		// twice, in different targets, and produced three hypotheses and no
		// evidence (E53).
		hint := startHint(err, collectStartFacts(req.Argv, h.Root(), !s.Unconfined))

		return Response{Err: fmt.Sprintf("exec %v: %v%s", req.Argv, err, hint)}
	}

	cpu, rss := usageOf(cmd.ProcessState)

	return Response{
		Exit: 0, Output: outputFor(req, out), Degraded: degradedNow,
		CPUNanos: cpu.Nanoseconds(), MaxRSS: rss,
	}
}

// Capture digests what a handle's filesystem holds.
//
// Taken in the guest rather than the host because on a real backend the host
// cannot see the VM's filesystem at all - which is the same constraint (E1b)
// that put layer assembly in the guest to begin with.
func (c *Client) Capture(ctx context.Context, h core.Handle) (ir.NodeID, ir.NodeID, int64, error) {
	rh, ok := h.(*remoteHandle)
	if !ok {
		return ir.NodeID{}, ir.NodeID{}, 0, errors.New("handle did not come from this guest")
	}

	resp, err := c.do(ctx, Request{Kind: KindCapture, Handle: rh.id, Clamp: hostClamp()})
	if err != nil {
		return ir.NodeID{}, ir.NodeID{}, 0, err
	}

	ids, err := decodeStack([]string{resp.Layer, resp.Content})
	if err != nil {
		return ir.NodeID{}, ir.NodeID{}, 0, fmt.Errorf("decode capture digests: %w", err)
	}

	return ids[0], ids[1], resp.Bytes, nil
}

// Export copies an artifact out of a step's filesystem into the shared store.
// Export places an artifact where the host can pick it up.
//
// The returned path, when not empty, is where the bytes already sit in the
// store, relative to its root: nothing was copied and the host reads them off
// its own disk. See Response.Shared.
func (c *Client) Export(
	ctx context.Context, h core.Handle, path, dest string,
) (string, error) {
	rh, ok := h.(*remoteHandle)
	if !ok {
		return "", errors.New("handle did not come from this guest")
	}

	resp, err := c.do(ctx, Request{
		Kind: KindExport, Handle: rh.id, Path: path, Dest: dest, Clamp: hostClamp(),
		// **Not when the store is the guest's own.** The fast path answers with
		// a path in the store so the host can take the bytes off its own disk,
		// which is precisely what a store on a block device inside the VM is
		// not. Asking anyway would send the host to read a file that is not
		// there.
		MayShare: ShareExports() && !StoreInVM(),
	})
	if err != nil {
		return "", err
	}

	return resp.Shared, nil
}

// Copy places a path from a stored layer into a step's filesystem.
func (c *Client) Copy(
	ctx context.Context, h core.Handle, from []ir.NodeID, src, dest string, opts CopyOpts,
) error {
	rh, ok := h.(*remoteHandle)
	if !ok {
		return errors.New("handle did not come from this guest")
	}

	_, err := c.do(ctx, Request{
		Kind: KindCopy, Handle: rh.id, From: layerIDs(from), Clamp: hostClamp(),
		Path: src, Dest: dest,
		DirCopy: opts.AsDir, NoFollow: opts.NoFollow, KeepOwn: opts.KeepOwn,
		Chown: opts.Chown,
	})

	return err
}

// Exec runs a step in the guest against a materialised handle.
func (c *Client) Exec(ctx context.Context, h core.Handle, argv, env []string) (int, string, error) {
	return c.ExecStream(ctx, h, argv, env, nil)
}

// ExecStream runs a step and delivers its output as it appears.
//
// sink is called from the connection's reader goroutine, so it must not block
// for long: everything else on this connection waits behind it. It may be nil,
// which is Exec.
//
// The complete output is still returned, because a failing step's message is
// what its error is made of - streaming is in addition to that, not instead.
func (c *Client) ExecStream(
	ctx context.Context, h core.Handle, argv, env []string, sink func(string),
) (int, string, error) {
	return c.ExecIn(ctx, h, "", argv, env, sink)
}

// Step is what to run and what it may see.
//
// A struct rather than more parameters, because the list had reached six and
// the next two - mounts, and whatever follows - would be positional booleans
// nobody could read at a call site.
type Step struct {
	// Dir is the working directory inside the step's filesystem.
	Dir string
	// Argv is the command.
	Argv []string
	// Env is ε, and only ε.
	Env []string
	// BaseEnv is what the base image declared, which ε overlays.
	BaseEnv []string
	// Mounts are directories bound into the step's filesystem: they outlive it,
	// and are not part of what it produces.
	Mounts []Mount
	// Daemon asks for a container daemon running inside this step, for as long
	// as the step lasts. Nil for everything that is not a WITH DOCKER.
	//
	// What is mounted at its root decides whether its storage survives the step,
	// and that is the executor's decision rather than the guest's (E365).
	Daemon *Daemon
	// Hosts are name-to-address entries the step resolves by. See Request.Hosts.
	Hosts []string
	// Terminal is the caller's terminal, for an interactive step. It is sent as
	// a descriptor rather than relayed, so the step owns it.
	Terminal *os.File

	// NoNet is `RUN --network=none`: the step runs in an empty network
	// namespace.
	//
	// Per step, unlike Server.DropNet, which cuts every step off. Both are
	// honoured, and either one is enough - a server told to run hermetically
	// does not stop being hermetic because a step did not ask.
	// Trace asks for this step's reads to be observed. See Request.Trace.
	Trace bool
	NoNet bool
}

// ExecIn runs a step in a working directory.
//
// Keeps the three-value shape its callers use: they run a command and read what
// it said, and none of them wants the step's resource usage.
func (c *Client) ExecIn(
	ctx context.Context, h core.Handle, dir string, argv, env []string, sink func(string),
) (int, string, error) {
	got, err := c.RunStep(ctx, h, Step{Dir: dir, Argv: argv, Env: env}, sink)

	return got.Exit, got.Output, err
}

// StepOutcome is what running a step came to.
//
// A value rather than a widening tuple: this returned three things and needed a
// fourth, and a signature that grows a return per fact is one nobody can read -
// the same lesson `passable` learned one increment earlier (E467).
type StepOutcome struct {
	// Exit is the command's exit code.
	Exit int
	// Output is what it printed, bounded by the guest.
	Output string
	// CPU and MaxRSS are what its process spent, for `--exec-stats`. Zero where
	// the platform cannot state one honestly.
	CPU    time.Duration
	MaxRSS uint64
}

// RunStep runs a step, including anything mounted into it.
//
// Named apart from Exec because that one is the old three-argument form the
// rest of the package still uses; one method taking a Step and another taking a
// loose argv would be two ways to say the same thing, and the mounts would be
// missing from whichever a caller happened to pick.
func (c *Client) RunStep(
	ctx context.Context, h core.Handle, step Step, sink func(string),
) (StepOutcome, error) {
	rh, ok := h.(*remoteHandle)
	if !ok {
		return StepOutcome{}, errors.New("handle did not come from this guest")
	}

	req := Request{
		Kind:    KindExec,
		Handle:  rh.id,
		Argv:    step.Argv,
		Env:     step.Env,
		BaseEnv: step.BaseEnv,
		Dir:     step.Dir,
		Mounts:  step.Mounts,
		NoNet:   step.NoNet,
		Trace:   step.Trace,
		Daemon:  step.Daemon,
		Hosts:   step.Hosts,
	}

	if step.Terminal != nil {
		if c.Terminals == nil {
			return StepOutcome{}, fdpass.ErrNoDescriptorChannel
		}

		// Sent before the request, so the guest never has to wait for a
		// descriptor it has already been told to expect. The socket buffers it.
		err := fdpass.SendFile(c.Terminals, step.Terminal)
		if err != nil {
			return StepOutcome{}, fmt.Errorf("hand over the terminal: %w", err)
		}

		req.Interactive = true
	}

	resp, err := c.doStream(ctx, req, sink)
	if err != nil {
		return StepOutcome{}, err
	}

	// Why a step ran unbounded, recorded as the step reports it rather than
	// when the guest exits (E123).
	c.noteDegraded(resp.Degraded)

	return StepOutcome{
		Exit: resp.Exit, Output: resp.Output,
		CPU: time.Duration(resp.CPUNanos), MaxRSS: resp.MaxRSS,
	}, nil
}

// EnvFast names storage the guest owns, if the sandbox was given any.
//
// A block device attached to the VM, so its filesystem lives in the guest kernel
// rather than being a view of a host directory. Empty where the sandbox has no
// such device - a Linux worker confining with namespaces has none - and then
// everything is as it was.
const EnvFast = "EARTH_GUEST_FAST"

// mountStore is where this guest keeps the directories a CACHE names.
//
// **On storage the guest owns, where there is any.** These were beside the
// layers, in the store shared from the host, because a cache mount has to
// outlive the step that used it. That requirement is right and the conclusion
// was wrong: outliving the build does not mean the *host* must see it.
//
// The difference is not marginal. In one guest, 4,000 files: untarring into a
// block-device volume takes 0.09s where the shared store takes 2.31s, and
// removing the tree 0.00s against 0.62s - because every metadata operation over
// a share is a round trip across the VM boundary and none of them is on a block
// device (E511, and the prior art in the plan).
//
// Resolved here rather than sent by the host, since the host and the guest see
// the store at different paths.
func (s *Server) mountStore() string {
	// Checked for rather than trusted: the environment says a volume was
	// attached and the filesystem is the authority on whether it arrived. A
	// sandbox that started without its mount would otherwise put a build's
	// caches somewhere that is not there, and the failure would name a cache
	// rather than a missing volume.
	if fast := os.Getenv(EnvFast); fast != "" {
		// An operator's environment variable, not a path a build named: whoever
		// sets it already runs this engine (gosec G703).
		fi, err := os.Stat(fast) //nolint:gosec // an operator's own setting
		if err == nil && fi.IsDir() {
			return filepath.Join(fast, "mounts")
		}
	}

	if s.LayerDir == "" {
		return filepath.Join(os.TempDir(), "earthbuild-mounts")
	}

	// LayerDir is the store *root* - the materialiser puts layers at
	// <root>/layers - so mounts sit beside those rather than one level further
	// up, which is outside the directory shared into this machine and vanishes
	// with it.
	return filepath.Join(s.LayerDir, "mounts")
}

// stepEnv puts ε over the baseline every step is entitled to.
//
// PATH is the one that matters and the reason this exists; HOME is here because
// a great deal of software writes to it and would otherwise choose "/" or fail.
// ε wins on a collision, because an Earthfile that sets PATH means it.
func stepEnv(base, env []string) []string {
	// Three layers, weakest first: a floor this engine guarantees, then what the
	// base image declared, then ε. Each wins over the one before, because each
	// is more specific about this step - and an Earthfile that sets PATH means
	// it.
	//
	// The floor exists because an image may declare nothing at all, and a step
	// with no PATH falls back to whatever the shell compiled in - which omits
	// /usr/local/bin, where pip, npm, cargo and docker put things.
	floor := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
	}

	// **One fold, in `engine/decl`.** What an image declares and what an
	// Earthfile declares are the same kind of thing said about the same step
	// (green paper §3.2a), and this had two rules for them: the base was
	// overlaid without expansion and ε was expanded one entry at a time. Two
	// rules for one question is how the two came to disagree - and the version
	// here also wrote a bare name into the environment as a malformed entry
	// rather than removing it.
	//
	// The floor and the base arrive already expanded, so they say so: their
	// values mean the characters they contain, not whatever this step's
	// environment would substitute.
	return decl.Fold(nil, decl.Literal(floor), decl.Literal(base), decl.Declaration{Env: env})
}

// lookIn resolves a bare command name against a filesystem that is about to
// become the root.
//
// Returns the name unchanged when it is already a path, or when nothing in PATH
// matches - the second so the failure is the kernel's "no such file", naming
// what was asked for, rather than a message this function invented.
func lookIn(root, name string, env []string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name
	}

	var path string

	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == "PATH" {
			path = v
		}
	}

	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}

		// The name the *step* will use, checked against where it lives now.
		inStep := filepath.Join(dir, name)

		fi, err := os.Stat(filepath.Join(root, inStep))
		if err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return inStep
		}
	}

	return name
}

// matchOne resolves a pattern against what is actually there.
//
// One match, because a copy has one destination: several would each have to
// land somewhere, and choosing between them is the author's business rather
// than this engine's guess. A pattern that matches nothing names itself, so the
// message is about the pattern rather than about a file with a star in its name.
func matchOne(path, as string) (string, error) {
	if !strings.ContainsAny(filepath.Base(path), "*?[") {
		return path, nil
	}

	matches, err := filepath.Glob(path)
	if err != nil {
		return "", fmt.Errorf("COPY %s: %q is not a usable pattern: %w", as, as, err)
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("COPY %s: nothing matches %q", as, as)

	case 1:
		return matches[0], nil

	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, filepath.Base(m))
		}

		sort.Strings(names)

		return "", fmt.Errorf(
			"COPY %s: %q matches %d files, and a copy has one destination"+
				"\n  they are: %s", as, as, len(matches), strings.Join(names, ", "))
	}
}

// layerPath is a path in the store together with the layer it belongs to.
//
// The root travels with the path because a symlink's text is meaningless
// without it: `/opt/app` inside a layer names that layer's /opt/app, and the
// only way to say so is to carry the place it is relative to. Returning bare
// strings meant every caller resolved links against the guest's own
// filesystem - which is the machine the link was *not* written on.
type layerPath struct {
	root string
	path string
}

// findInStack locates a path in every layer an artifact came from, oldest first.
//
// Every layer, not the first one that has it: a *file* belongs to the newest
// layer that wrote it, and a *directory* belongs to all of them at once. A
// target that makes a directory over three steps - which is what a build is -
// has its contents spread across three layers, and taking the newest gives a
// plausible subset of the artifact with no sign that anything is missing.
//
// The repository's own `+code` is exactly that shape: fourteen source
// directories copied in three steps, saved as one artifact, and what arrived
// downstream was the third step's share. It surfaced two targets later as
// `find . -name go.mod` finding nothing (E48).
//
// A pattern is matched here too: `SAVE ARTIFACT target/*-standalone.jar` names
// a file whose version the build decides, so it cannot be resolved when the
// plan is made - only against a filesystem that has it.
func (s *Server) findInStack(from []string, src string) ([]layerPath, error) {
	if s.LayerDir == "" {
		return nil, errors.New("no layer store configured, so there is nothing to copy from")
	}

	if len(from) == 0 {
		return nil, fmt.Errorf("COPY %s: no layers to take it from", src)
	}

	var (
		found []layerPath
		last  error
	)

	for _, v := range from {
		root := filepath.Join(s.LayerDir, "layers", v)

		path, err := within(root, src)
		if err != nil {
			return nil, err
		}

		match, err := matchOne(path, src)
		if err != nil {
			last = err

			continue
		}

		_, err = os.Lstat(match)
		if err == nil {
			found = append(found, layerPath{root: root, path: match})
		}
	}

	if len(found) > 0 {
		return found, nil
	}

	if last != nil {
		return nil, last
	}

	return nil, fmt.Errorf("COPY %s: nothing in that target has it", src)
}

// layerIDs is a stack in the form the protocol carries.
func layerIDs(ids []ir.NodeID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}

	return out
}

// stepWaitDelay bounds how long this guest waits for a step's pipes to close
// after the step itself has exited.
//
// Generous, because the cost of the two mistakes is not symmetric: too short
// truncates the tail of a step's output, and too long is a build that hangs.
// Output already written is already copied - the delay only covers the gap
// between the child exiting and its inherited pipe ends being released.
const stepWaitDelay = 5 * time.Second

// runStep runs a step, observed if it asked to be.
//
// The branch is here rather than inside runObserved so that an untraced step
// pays nothing at all - not a goroutine, not a thread, not a locked one. Tracing
// is what earns a step an L2 hit, and a build that does not want the tier should
// not be paying a round trip for every path its steps open.
//
// A traced step's sightings are recorded against the handle, where the rest of
// its observation already lives: what a copy looked at, and now what a command
// did (green paper §3.4).
func runStep(
	cmd *osexec.Cmd, sink func([]byte), req Request, s *Server, h core.Handle,
	provided []string,
) ([]byte, error) {
	if !req.Trace {
		return run(cmd, sink)
	}

	// The step's own cancel, which kills its process group - see where
	// cmd.Cancel is set. Passed rather than reached for, and guarded on
	// cmd.Process, because os/exec fills that in only once the process exists
	// and a tracer can stop before it does.
	release := func() {
		if cmd.Process == nil {
			return
		}

		_ = cmd.Cancel()
	}

	out, seen, err := runObserved(
		func() ([]byte, error) { return run(cmd, sink) }, s.filler(req.Handle), release)

	s.recordSightings(h, h.Root(), seen, provided)

	return out, err
}

// mountPoints is where this engine put things inside a step's filesystem.
//
// Taken from the mounts the guest is about to make rather than written down
// beside the tracer, so a mount added later is excluded from observations
// without anybody remembering to come back - which is how the list in question
// would otherwise rot (E222).
//
// `/proc` is here because `mountProc` makes it separately and is not in this
// list; it is the runtime's, and a step reading `/proc/self/status` has read
// nothing the base carries.
func mountPoints(mounts []Mount) []string {
	out := make([]string, 0, len(mounts)+1)
	for _, m := range mounts {
		out = append(out, m.Target)
	}

	return append(out, "/proc")
}

// declaredBy is what this step's base says about how it should run.
//
// From the stack the materialiser walked, when it can say - and from the request
// otherwise, which is what a caller with no declaration in its stack sends and
// what every build did before declarations existed.
func declaredBy(h core.Handle, req Request) []string {
	d, ok := h.(interface{ Declared() []string })
	if !ok {
		return req.BaseEnv
	}

	from := d.Declared()
	if len(from) == 0 {
		return req.BaseEnv
	}

	return from
}

// EnvShareExports turns off taking an export straight from the store.
//
// An escape hatch and an A/B switch: the fast path is only sound because the
// guest refuses everything it cannot prove (see overlay's SharedFile), and a
// way to run the same build both ways is what shows the artifact is identical
// rather than merely plausible.
const EnvShareExports = "EARTH_SHARE_EXPORTS"

// EnvStoreInVM puts the layer store on the block device the guest owns.
//
// **A shared directory is reached over virtiofs, and every metadata operation
// on it is a round trip across the VM boundary.** Measured from inside the
// guest on one layer of `golang:1.26-alpine`: unpacking into the shared store
// 4.67s against 2.18s into the volume, and reading it all back 6.04s against
// 1.47s - about 0.31ms per file a step opens, which is half a second on a cold
// `go build` and invisible in every phase, because it is spread through the
// step's own execution.
//
// E511 established the principle and moved CACHE mounts for it: "outliving the
// build does not mean the *host* must see it". The layer store is the rest.
//
// What it costs is the cache's lifetime. The volume belongs to the sandbox and
// goes when the sandbox does, so layers live as long as the machine rather than
// as long as a directory the user owns.
const EnvStoreInVM = "EARTH_STORE_IN_VM"

// EnvTracePin puts a traced step and the thread answering its syscalls on one
// CPU.
//
// **A traced path call costs 2.2µs when they share a CPU and 45µs when they do
// not** - the same 4-vCPU guest, the same tracer, measured both ways (E681).
// Each half of the wakeup is a vmexit under a hypervisor, which is why bare
// metal pays 8.3µs against 7.2µs for the same choice and barely notices.
//
// Behind a switch because the trade is real and this cannot yet tell which side
// of it a step is on: `find /usr/local/go` makes 45k path calls on one thread
// and wants this, and `go build -p 4` makes few and wants four CPUs. The steps
// that flood the tracer are the single-threaded ones, but "usually" is not a
// policy and choosing one needs a corpus rather than an argument.
const EnvTracePin = "EARTH_TRACE_PIN"

// StoreInVM reports whether the layer store is on the guest's own device.
func StoreInVM() bool { return os.Getenv(EnvStoreInVM) != "" }

// ShareExports reports whether an export may come from the store directly.
//
// On unless switched off: the slow path is always correct, so the failure this
// guards against is a wrong answer, not a missing one.
func ShareExports() bool { return os.Getenv(EnvShareExports) != "0" }

// hostClamp is the timestamp this build asked every file it writes to carry.
//
// Read here, on the host, and sent with each request that writes something.
// The guest was given `SOURCE_DATE_EPOCH` at boot for a while, which is right
// until the sandbox outlives the build that started it: it is named by its
// image, store and memory, so the next build finds a machine already holding
// somebody else's instruction (E549).
func hostClamp() *int64 {
	at, ok := fstime.Clamp()
	if !ok {
		return nil
	}

	secs := at.Unix()

	return &secs
}

// clampAt turns a request's clamp into the time to write, or nil for "keep".
func clampAt(secs *int64) *time.Time {
	if secs == nil {
		return nil
	}

	at := time.Unix(*secs, 0)

	return &at
}

// declaresIn is what a materialise reply said the stack declares.
//
// Absent is empty rather than an error: a guest too old to send one materialised
// the same stack and knows the same things, and the caller's own defaults are
// what it had before this field existed.
func declaresIn(resp Response) decl.Declaration {
	if resp.Declares == nil {
		return decl.Declaration{}
	}

	return *resp.Declares
}

// unpackLayer unpacks a compressed blob into this guest's store.
//
// **The guest does it because the guest owns the store.** See KindUnpackLayer
// for why the store is moving onto the block device, and `mountStore` for the
// same argument made about CACHE mounts before it.
//
// Root here, which the host is not: an unprivileged unpack cannot grant the
// ownership an archive declares, cannot create a device node, and cannot set an
// attribute in the `security.` namespace - so a layer unpacked on the host is a
// different object from the one the image describes, and three separate
// mechanisms exist to paper over the difference.
func (s *Server) unpackLayer(req Request) Response {
	// An unset store is not an empty store: `DirStore("")` joins to a relative
	// path, so a layer would be placed wherever this process happens to be. The
	// same refusal store-has makes, for the same reason.
	if s.LayerDir == "" {
		return Response{Err: "unpack-layer: this guest was started without a" +
			" layer directory, so it has nowhere to put one" +
			" (set EARTH_GUEST_ROOT, or Server.LayerDir)"}
	}

	if req.Blob == "" || req.Media == "" {
		return Response{Err: "unpack-layer: a blob path and a media type are" +
			" both required; a blob whose compression nobody stated cannot be read"}
	}

	// Per layer, because a stack's unpack is only ever as quick as its largest
	// member and the aggregate hides which one that is.
	defer timing.Phase("layer:unpack:guest", req.Blob)()

	blob, err := s.openBlob(req)
	if err != nil {
		return Response{Err: "unpack-layer: " + err.Error()}
	}

	defer blob.Close()

	zr, err := image.DecompressFrom(blob, req.Media)
	if err != nil {
		return Response{Err: "unpack-layer: " + err.Error()}
	}

	defer zr.Close()

	st := store.DirStore(s.LayerDir)

	staging, err := st.Staging(".unpack-")
	if err != nil {
		return Response{Err: "unpack-layer: " + err.Error()}
	}

	defer func() { _ = os.RemoveAll(staging) }()

	// **Not hashed here.** Hashing on the way in is serial inside this
	// goroutine at 330 MB/s on entries the size a layer holds, where the
	// read-back it saves runs across every core - and the largest layer is the
	// critical path of a cold FROM. Measured at 8% of one (E682).
	got, err := image.UnpackApartUnhashed(zr, staging)
	if err != nil {
		return Response{Err: "unpack-layer: " + err.Error()}
	}

	// **Told rather than rediscovered.** The unpacker read every header to
	// write the layer at all, so the digests are free - and the ownership is not
	// recoverable from the tree at all if any chown was refused.
	id, err := st.PlaceAs(staging, store.Placement{
		Digests: knownDigests(got.Digests), Owners: declaredOwners(got.Owners),
	})
	if err != nil {
		return Response{Err: "unpack-layer: " + err.Error()}
	}

	if !got.Marked {
		st.NoteUnmarked(id)
	}

	// Beside the layer, under the name the store looks for. Best effort, as
	// `AdoptConfig` is: a configuration that cannot be filed costs the
	// environment an image asked for, which is a build that behaves as it did
	// before declarations existed, where failing the FROM would be no build.
	declares := ""

	if len(req.Config) > 0 {
		err = os.WriteFile(st.LayerPath(id)+store.ConfigSuffix, req.Config, 0o600)
		if err == nil {
			// **Written, not merely named.** A declaration is a stack element,
			// so a caller handed only its identity would build a stack on
			// something the store does not hold - which fails at materialise
			// with "holds neither a layer nor a declaration for it". Only this
			// side can write it, because only this side has the store.
			if d := st.Declaration(id); d != (ir.NodeID{}) {
				declares = d.String()
			}
		}
	}

	return Response{Layer: id.String(), Declaration: declares}
}

// knownDigests and declaredOwners are conversions and nothing else: `ir` imports
// `engine/image`, so `engine/image` cannot name `ir` or `layer`.
func knownDigests(from map[string]image.Digest) map[string]ir.NodeID {
	if len(from) == 0 {
		return nil
	}

	out := make(map[string]ir.NodeID, len(from))
	for at, d := range from {
		out[at] = ir.NodeID(d)
	}

	return out
}

func declaredOwners(from map[string]image.Owner) map[string]layer.Owner {
	if len(from) == 0 {
		return nil
	}

	out := make(map[string]layer.Owner, len(from))
	for at, o := range from {
		out[at] = layer.Owner{UID: o.UID, GID: o.GID}
	}

	return out
}

// fileConfig puts an image's configuration beside a layer already in the store
// and reports the declaration it produced.
//
// See KindFileConfig for why this is not part of the unpack. Writing rather than
// naming, for `unpackLayer`'s reason: a declaration is a stack element, and only
// the side holding the store can put one there.
func (s *Server) fileConfig(req Request) Response {
	if s.LayerDir == "" {
		return Response{Err: "file-config: this guest was started without a" +
			" layer directory (set EARTH_GUEST_ROOT, or Server.LayerDir)"}
	}

	id, err := ir.ParseNodeID(req.Layer)
	if err != nil {
		return Response{Err: "file-config: " + err.Error()}
	}

	st := store.DirStore(s.LayerDir)

	if !st.Has(id) {
		return Response{Err: fmt.Sprintf("file-config: this store does not hold %v,"+
			" so there is nothing to file a configuration beside", id)}
	}

	// Nothing to say is the ordinary case, and must not leave an empty sidecar:
	// "declares nothing" is the absence of a declaration rather than a
	// declaration of emptiness.
	if len(req.Config) == 0 {
		return Response{}
	}

	err = os.WriteFile(st.LayerPath(id)+store.ConfigSuffix, req.Config, 0o600)
	if err != nil {
		return Response{Err: "file-config: " + err.Error()}
	}

	if d := st.Declaration(id); d != (ir.NodeID{}) {
		return Response{Declaration: d.String()}
	}

	return Response{}
}

// viewDigests answers what a base holds at each of a set of paths.
//
// See KindViewDigests: the observed-input tier reads a base to check a
// prediction against it, and a base on this guest's own device is not somewhere
// the host can read.
//
// Both questions are asked of every path because the caller does not know which
// are directories - a prediction records what a step looked at, and a step looks
// at both. An absent answer is left absent: "not there" and "there and empty"
// are different, and a prediction turns on which it gets.
func (s *Server) viewDigests(ctx context.Context, req Request) Response {
	if s.LayerDir == "" {
		return Response{Err: "view-digests: this guest was started without a" +
			" layer directory (set EARTH_GUEST_ROOT, or Server.LayerDir)"}
	}

	ids, err := decodeStack(req.Stack)
	if err != nil {
		return Response{Err: "view-digests: " + err.Error()}
	}

	view, err := store.LayerStore(s.LayerDir).View(ctx, ids)
	if err != nil {
		return Response{Err: "view-digests: " + err.Error()}
	}

	digests := map[string]string{}
	listings := map[string]string{}

	for _, at := range req.Paths {
		if d, ok := view.Digest(at); ok {
			digests[at] = d.String()
		}

		if d, ok := view.ListingDigest(at); ok {
			listings[at] = d.String()
		}
	}

	return Response{Reads: digests, Listings: listings}
}

// blobPatience is how long the guest waits for a growing blob to move.
//
// **Short, because a living fetch is never quiet.** Progress is reported every
// megabyte, which at the 56 MB/s one connection manages is about 18ms; silence
// for forty-five seconds is not a slow registry, it is something broken.
//
// It was five minutes, and that number cost a build that sat doing nothing and
// then said `context canceled` - the failure this file's comments keep warning
// about, shipped in the file that warns about it. A wait that ends is worth
// more than a wait that is generous.
const blobPatience = 45 * time.Second

// openBlob is a layer's compressed bytes, whether or not the host has finished
// writing them.
//
// **A blob that has landed is opened and read**, which is how it always was. One
// still arriving is read as it grows: the host says how long it will be, and the
// guest never reads past what the blob's progress marker reports - pages beyond
// the writer are zeros, and a cached zero is a zero kept (E683).
//
// The digest is not checked here and does not need to be. The host stops the
// marker one byte short of the end until the bytes verify, so an unpack of a bad
// layer cannot finish - and a layer that never finishes is never placed, which
// is the whole of the guarantee.
func (s *Server) openBlob(req Request) (io.ReadCloser, error) {
	if req.Growing <= 0 {
		f, err := os.Open(req.Blob)
		if err != nil {
			return nil, err
		}

		return f, nil
	}

	g, err := image.OpenGrowing(req.Blob, req.Growing, s.blobProgress(req.Blob))
	if err != nil {
		return nil, err
	}

	// **Buffered, because the reader gave up readahead to be correct.** A
	// growing file is read with readahead off - it is the only way a page the
	// writer has not reached is not cached as zeros - and the decompressor asks
	// in small pieces, so without this a 64MB blob becomes thousands of round
	// trips across the shared mount. One megabyte at a time makes it dozens.
	return buffered{Reader: bufio.NewReaderSize(g, growingBuffer), close: g.Close}, nil
}

// growingBuffer is how much of a growing blob is read at once. See openBlob.
const growingBuffer = 1 << 20

// buffered is a bufio.Reader that closes what it was reading from.
type buffered struct {
	*bufio.Reader

	close func() error
}

func (b buffered) Close() error { return b.close() }

// blobProgress is how this guest asks where a fetch has got to.
//
// **Over the socket when there is one.** A file on the shared mount answers
// about 460ms late, which is long enough that a guest reading a blob as it
// arrives spends the fetch waiting rather than unpacking - the head start and
// the waiting cancel, and streaming buys nothing (E688). The fault-in channel
// is already guest-to-host and has no filesystem in it.
//
// The blob is named by its file name rather than its path, because the host and
// the guest see the same file at different ones.
//
// Falls back to the file for a guest with no channel - slower, and the only
// alternative is refusing to stream at all.
func (s *Server) blobProgress(blob string) func(int64) (int64, error) {
	s.fillsMu.Lock()
	f := s.Fills
	s.fillsMu.Unlock()

	if f == nil {
		return func(have int64) (int64, error) {
			return image.AwaitProgress(blob, have, blobPatience)
		}
	}

	name := filepath.Base(blob)

	return func(have int64) (int64, error) { return f.Progress(name, have) }
}
