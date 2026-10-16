package exec

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// primedBase is a step's lazily materialised filesystem, and where it came from.
type primedBase struct {
	stack []ir.NodeID
	into  string
	// complete says the base was assembled whole rather than primed, so nothing
	// is missing from it that a fetch could supply. See FillFor.
	complete bool
}

// remember records a primed base against the handle the guest gave it.
//
// **The host end of E303.** A worker runs several steps at once, each with a
// primed base of its own, and a fault-in says which - so this is what turns that
// name back into a stack and a directory. Guessing would fetch from a base
// chosen by accident (E304).
func (e *Executor) remember(handle string, b primedBase) {
	e.primedMu.Lock()
	defer e.primedMu.Unlock()

	if e.primed == nil {
		e.primed = map[string]primedBase{}
	}

	e.primed[handle] = b
}

// forget drops a base whose step has finished.
//
// Its directory is removed when the step ends, so a fault-in arriving afterwards
// is for a filesystem that no longer exists - and a map that never forgot would
// grow for the life of the process.
func (e *Executor) forget(handle string) {
	e.primedMu.Lock()
	defer e.primedMu.Unlock()

	delete(e.primed, handle)
}

// FillFor answers a fault-in against the base it named.
//
// Refused for a handle this executor did not prime: **not answered from some
// other base**, and not silently succeeded. A step told "no such file" about one
// this engine could have obtained takes the other branch and succeeds (E289), and
// a step handed a file out of somebody else's base is worse still.
func (e *Executor) FillFor(ctx context.Context, handle, path string) error {
	e.primedMu.Lock()
	b, ok := e.primed[handle]
	e.primedMu.Unlock()

	if !ok {
		return fmt.Errorf("a fault-in named base %q, which this engine did not"+
			" prime\n  it cannot be answered from another step's base", handle)
	}

	// **Not every miss is a fault.** A step resolving a command walks `PATH`:
	// `go version` opens `/go/bin/go`, which no Go image has, before finding
	// `/usr/local/go/bin/go`, which every one does. The tracer stops the syscall
	// on any path that is not there, so a base with nothing missing still
	// produces faults - one per probe.
	//
	// A base assembled whole has nothing to fetch and nothing absent that ought
	// to be present, so the answer is the one this protocol already has: no
	// error, meaning the host looked and the file is genuinely not there, and
	// the step gets its honest ENOENT (E289).
	//
	// Reported as a failure instead, this cost a real fleet build: a worker
	// fetched a 267MB base, ran `go version`, faulted on a PATH probe, and
	// refused the step.
	if b.complete {
		return nil
	}

	if e.Fetch == nil {
		return fmt.Errorf("this engine has nowhere to fetch %s from", path)
	}

	// The guest names a path inside its own root; this engine knows that root as
	// the directory it primed. Both are handed on, because whoever fetches has
	// to know which base a path is *relative to* - two spellings of one path is
	// how a fault-in silently fetches nothing (E295, E305).
	return e.Fetch(ctx, b.stack, b.into,
		filepath.Join(b.into, strings.TrimPrefix(path, "/")))
}
