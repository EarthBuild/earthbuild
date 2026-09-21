// Package fleet is the wire between a driver and its workers.
//
// Green paper Appendix C. The one idea the whole appendix rests on is that a
// worker is sent a **step assignment, never a graph**: the base is a sequence of
// layer ids rather than the subgraph that produced them, because the base is
// content-addressed and materialisable from 𝔅. Content addressing collapses the
// graph into digests at the boundary, and a worker never learns how its inputs
// were derived because it never needs to.
package fleet

import (
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Version is the wire vocabulary this package speaks.
//
// C.3 requires the assignment format to be versioned, and the IR is not - which
// is the point of them being different types. A peer speaking a version this one
// does not know is refused rather than guessed at.
// Version 2 added Op.Scratch. A version-1 worker would ignore the field and run
// the step without the directory, capturing into its result what the step meant
// to throw away - so the version is what stops a peer answering a question it
// cannot hear.
// Version 3 added Hints.Bytes to the encoding - it was set, documented and
// tagged, and carried by nothing - and Hints.CacheMaps beside it.
const Version = 3

// Op is what a worker is asked to do.
//
// **A poorer vocabulary than `ir.Op`, deliberately.** `host` is not in it, so a
// malicious peer cannot request one - and that is a property of the *type*
// rather than a check somebody could forget to write (C.3). A delegate is not
// the invoking machine, so it cannot satisfy host locality; expressing the
// request at all would be the first mistake.
//
// Flat, for the same reason: no unevaluated references, no laziness, no
// recursion. Everything here is a value, and a value cannot smuggle a graph.
type Op struct {
	// Kind is which of the wire's operations this is. The set is closed and is
	// not `ir.OpKind`: see Kind's own documentation.
	Kind Kind `json:"kind"`
	// Args is the operation's arguments, as the language wrote them.
	Args []string `json:"args,omitempty"`
	// Env is the environment the step runs with, sorted by key when serialised.
	Env map[string]string `json:"env,omitempty"`
	// Dir is the working directory inside the step's filesystem.
	Dir string `json:"dir,omitempty"`
	// User is who the step runs as.
	User string `json:"user,omitempty"`
	// NoNetwork is `RUN --network=none`.
	NoNetwork bool `json:"noNetwork,omitzero"`
	// Scratch are directories the worker makes for the step and removes after
	// it - `CACHE --sharing=private` (§3.3c).
	//
	// Targets and nothing else, because there is nothing else: a private cache
	// names no directory on the invoking machine, so every worker produces the
	// same one. This is the only kind of mount an assignment can carry, and it
	// carries it because the alternative is worse than refusing - a step run
	// without a mount it declared writes into its output layer what it would
	// otherwise have discarded, which is one key over two results (E433).
	Scratch []string `json:"scratch,omitempty"`
	// Caches are the shared cache mounts the step declared - `CACHE` without
	// `--persist` (§3.3c).
	//
	// **A cache mount cannot change what a step produces, and this engine has
	// always said so.** It is bound *over* the step's filesystem, so what goes
	// into it is excluded from the layer by construction, and the key hashes
	// the mount's declaration and never its contents. Every cache hit ever
	// served asserts it. A worker therefore runs the step against its own
	// directory of the same name and produces the same layer, more slowly the
	// first time.
	//
	// Carried rather than assumed, for `Scratch`'s reason: a step run without a
	// mount it declared writes into its layer what it would have discarded, and
	// files it under the invoker's key (E433). What is *not* carried is the
	// contents - those are the worker's, and the point.
	Caches []Cache `json:"caches,omitempty"`
}

// Cache is a shared cache mount, by name and place.
//
// No contents and no host path: the name is what two machines agree on, and the
// directory behind it belongs to whichever machine is running the step. That is
// the whole difference between this and a bound view, which names an object
// both ends can fetch.
type Cache struct {
	// ID is what the build calls this cache. Two steps naming the same ID share
	// a directory, on whichever machine they run.
	ID string `json:"id"`
	// Target is where it appears inside the step's filesystem.
	Target string `json:"target"`
	// Mode is the permission bits the directory is made with, or zero for the
	// default.
	Mode uint32 `json:"mode,omitzero"`
	// Exclusive is `--sharing=locked`: one step at a time in this directory.
	// Per machine, because the directory is.
	Exclusive bool `json:"exclusive,omitzero"`
	// Portable is whether the author claimed that another machine's copy of a
	// path under this cache is as good as this machine's own.
	//
	// Separate from the list because the list cannot carry it: a claim with no
	// exceptions is the strongest form and the commonest useful one, and it has
	// the same empty list as a cache nobody claimed anything about.
	Portable bool `json:"portable,omitzero"`
	// PortableExcept names the paths under this cache where that is not true -
	// as written, not parsed.
	//
	// **Carried because both ends must agree.** It is already in the step's key,
	// so a worker sent a different claim would be running a different step; this
	// is the same fact said on the wire, so a worker can act on it rather than
	// infer it. Empty means no claim was made and nothing is shared, which is
	// every cache by default.
	PortableExcept string `json:"portableExcept,omitempty"`
	// Helper names the program that understands this cache's format, as the
	// author wrote it. Carried because both ends must run the same one: a
	// helper decides what a unit is, so two that differ share nothing and may
	// import each other's units wrongly.
	Helper string `json:"helper,omitempty"`
	// HelperID is the digest of that helper's module, or empty where the
	// driver pinned none.
	//
	// **The field that makes `Helper` mean anything here.** A worker sent
	// `./go.wasm` has no such file: the path names a module on the machine that
	// read the Earthfile and nothing on this one. A digest names the bytes, so
	// a worker can fetch exactly the module the driver ran out of 𝔅 - which is
	// what "both ends must run the same one" requires and what comparing two
	// spellings of a path never gave.
	HelperID string `json:"helperID,omitempty"`
}

// Kind is the closed set of operations expressible on the wire.
//
// A distinct type from `ir.OpKind`, which is what keeps `host` off the wire. An
// engine converting an IR operation into one of these has to decide, for each
// kind, whether it can be delegated - and there is nowhere to put the answer
// "yes, host" because the constant does not exist.
type Kind string

// The wire vocabulary. Extending it is a protocol change and a version bump.
const (
	// KindExec is a command run inside the step's filesystem.
	KindExec Kind = "exec"
	// KindFile is a copy between layer stacks.
	KindFile Kind = "file"
	// KindImage is a base image to materialise.
	KindImage Kind = "image"
	// KindBuild delegates a whole target: the worker schedules the target's
	// steps itself and resolves that region's unknowns (C.3).
	//
	// **A delegate is an engine.** Every invariant in §5 binds it as it binds
	// the parent, and delegation adds no exemptions - which is why it can be
	// expressed here while `host` cannot: a delegate can honour the first and
	// cannot honour the second.
	KindBuild Kind = "build"
)

// Assignment is one step, as (C.2): 𝑏, ω, ε, π, a deadline and hints.
type Assignment struct {
	// Version is the wire vocabulary. First, so a peer can refuse a message it
	// does not understand before interpreting any of it.
	Version int `json:"version"`
	// Base is 𝑏 - a sequence of layer ids, **not the subgraph that produced
	// them**. The worker materialises it from the blob store and never learns
	// how it was built.
	Base []ir.NodeID `json:"base,omitempty"`
	// Op is ω, in the wire's own vocabulary.
	Op Op `json:"op"`
	// Sources are the layer stacks a copy reads from, each a sequence of ids for
	// the same reason as Base.
	Sources [][]ir.NodeID `json:"sources,omitempty"`
	// Platform is π.
	Platform string `json:"platform,omitempty"`
	// DeadlineUnix is when the driver stops waiting, in seconds since the epoch.
	//
	// An absolute instant rather than a duration: a duration would start when
	// the message was written, was read or was queued depending on who was
	// asked, and the three differ by exactly the amount that matters.
	DeadlineUnix int64 `json:"deadlineUnix,omitzero"`
	// Hints are advisory and **may be dropped by any participant without
	// affecting the result** (I5) - masks, a predicted read set, an estimated
	// duration. A worker that ignores every hint produces the same answer more
	// slowly, which is what makes them safe to accept from a peer.
	Hints Hints `json:"hints,omitzero"`
}

// Hints are advice. Nothing here may change what a step produces (I5).
type Hints struct {
	// Images are base images worth fetching before the step needs them.
	Images []string `json:"images,omitempty"`
	// ReadsPredicted are paths the step is expected to look at.
	ReadsPredicted []string `json:"readsPredicted,omitempty"`
	// EstimatedSeconds is how long this took last time.
	EstimatedSeconds int64 `json:"estimatedSeconds,omitzero"`
	// Bytes is how large this step's inputs are, when the driver knows.
	//
	// Placement's only measure of what delegating would *cost*: a base worth
	// three hundred steps of compute should not be shipped to save one, and
	// without a size every base is priced the same (E317).
	//
	// Zero means "not stated", never "free" - a fleet that priced an unknown
	// base at nothing would prefer whichever machine had the most to fetch.
	Bytes int64 `json:"bytes,omitzero"`
	// Holders are peers said to hold this step's inputs, nearest first.
	//
	// The mechanism that keeps a fleet from being a star: a worker that just
	// produced a layer is the closest copy of it, and without being told, every
	// worker fetches every input from the driver - whose uplink then *is* the
	// fleet's bandwidth, so adding machines adds queueing rather than throughput
	// (E260).
	//
	// Advice, like the rest of this struct. A worker that ignores every holder
	// fetches from the driver and produces the same answer more slowly, which is
	// what makes an unverified address safe to pass on.
	Holders []string `json:"holders,omitempty"`
	// CacheMaps says which map describes each portable cache this assignment
	// declares, keyed `<id>/<scope>` and valued by the digest of the map blob.
	//
	// **The join a worker cannot compute.** A map names a cache's units by ℋ
	// and is itself a blob; the *pointer* from a cache to its latest map is a
	// mutable file beside a store, deliberately not content-addressed, and a
	// worker has no way to derive it. So the machine that filed one says so.
	//
	// Keyed by the scope as well as the id, which is what keeps a trust domain
	// load-bearing (§5.3): two machines whose domains differ compute different
	// scopes, so the key does not match and nothing is stocked - without either
	// end having to compare domains, or even know the other has one.
	//
	// Advice, like the rest of this struct (I5). A worker that ignores it fills
	// the cache by doing the work, which is what every worker did before.
	CacheMaps map[string]string `json:"cacheMaps,omitempty"`
}
