// Package ir is the EarthBuild engine's intermediate representation: a
// content-addressed graph of build steps.
//
// It implements green paper §3 (objects). A node's identity is derived from its
// operation, its resolved inputs and its platform, so two nodes that would
// produce the same result share an identity and are scheduled once.
//
// The IR is a native Go graph, not a wire format. Nodes hold direct references
// to their inputs rather than digests standing in for unevaluated subgraphs;
// see docs-internals/plan-native-engine.md §2a-pre for why that distinction
// matters. What crosses a wire is a step assignment (green paper C.3), which is
// a deliberately poorer type.
package ir

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// OpKind is the vocabulary of operations a node may carry. It deliberately
// mirrors LLB's op set - green paper §3.4 - so that importing LLB is mechanical,
// with the additions LLB cannot express: Host, and typed metadata on every node.
type OpKind uint8

// The operation kinds.
const (
	// OpImage materialises a registry reference.
	OpImage OpKind = iota + 1
	// OpLocal materialises host build context.
	OpLocal
	// OpExec runs a command in the sandbox.
	OpExec
	// OpFile applies pure filesystem operations.
	OpFile
	// OpMerge combines stacks.
	OpMerge
	// OpHost runs on the invoking machine, unsandboxed. This is LOCALLY.
	//
	// OpHost is constrained everywhere it appears: never scheduled to a remote
	// worker (green paper §4.7.1), never retried (I7), never speculated
	// (§4.7.4), and absent from the wire vocabulary entirely (C.3).
	OpHost
	// OpBuild delegates a whole target to a worker, which schedules it itself.
	// Unused until the fleet exists; present from the start because a wire
	// vocabulary that cannot express delegation is expensive to retrofit.
	OpBuild
	// OpPackImage writes its input's layer stack as an OCI image in the layer
	// store, ready for something inside the sandbox to load.
	//
	// A separate operation rather than part of the step that loads it, because
	// the two happen in different places: the layout is written on the machine
	// running the build, from layers and a configuration it holds, while the
	// load happens inside the sandbox against a daemon. Args[0] is the
	// reference the image is written under.
	OpPackImage
	// OpScratch is the empty base: `FROM scratch`.
	//
	// **Appended, not inserted.** An opcode's number is hashed into every key
	// that mentions it, so putting a new one in the middle renumbers the ones
	// after it and quietly changes what every existing entry was filed under.
	// The guard that counts them caught this within a minute of it being
	// written, which is the only reason it is written down here rather than
	// found later (E468).
	//
	// A base that materialises nothing, which is where a build starts when it
	// brings its own filesystem. Its own kind rather than a merge of no inputs,
	// which would mean the same and would have to be decoded by every reader
	// (E468).
	//
	// Distinct from a target with no FROM at all, which this engine refuses by
	// name: a file that says `FROM scratch` has said where it starts.
	OpScratch
)

func (k OpKind) String() string {
	switch k {
	case OpImage:
		return "image"
	case OpLocal:
		return "local"
	case OpExec:
		return "exec"
	case OpFile:
		return "file"
	case OpMerge:
		return "merge"
	case OpHost:
		return "host"
	case OpBuild:
		return "build"
	case OpScratch:
		return "scratch"
	case OpPackImage:
		return "pack-image"
	default:
		return fmt.Sprintf("op(%d)", uint8(k))
	}
}

// Platform is a step's target platform. Fixed-width by construction so that it
// needs no length prefix when encoded (green paper §1.4).
type Platform struct {
	OS      string
	Arch    string
	Variant string
}

func (p Platform) String() string {
	if p.Variant == "" {
		return p.OS + "/" + p.Arch
	}

	return p.OS + "/" + p.Arch + "/" + p.Variant
}

// Mount is a directory bound into a step's filesystem.
//
// Not a layer: a layer is stacked and becomes part of what the step produces,
// while a mount is a hole in that filesystem onto something that outlives it.
type Mount struct {
	// Target is where it appears inside the step's filesystem.
	Target string
	// ID names what the mount comes from: a shared cache directory, or - when
	// Secret is set - the secret the invocation supplied.
	ID string
	// Sandbox names a path in the sandbox's own filesystem rather than
	// something in the layer store.
	//
	// A step runs chrooted into its own overlay, so a file sitting in the
	// sandbox is not reachable from inside it however visible it is to the
	// machine - which is how `docker load -i /var/lib/earthbuild/store/...`
	// came to report a missing file that was demonstrably there. WITH DOCKER
	// --load is what needs it: the archive is written by the host into the
	// store both sides share, and mounted into the step that loads it.
	Sandbox string
	// Mode is the permission bits the mount is created with, or zero for the
	// default. `RUN --mount=...,mode=0400`.
	//
	// In the key like every other field: a secret staged 0400 and one staged
	// 0644 are different inputs to the same command, and the corpus has
	// Earthfiles that write three modes for one secret in three targets - which
	// keyed identically until now (E435).
	// The strings first and the flags last, so the pointer-bearing fields sit
	// together (govet fieldalignment). A mount is built per step, so the
	// layout is worth more here than the reading order was.
	Mode uint32
	// From is the object a bound view shows, by identity: an earlier step's
	// result, or the node that materialises the local context. ν of §3.3d.
	//
	// Zero for a cache mount and a secret, which show nothing this build made.
	// A bound view's From is also one of the node's Sources, which is what puts
	// the object in the key and what makes the scheduler build it first; this
	// field says *which* of them, since a step may bind more than one.
	From NodeID
	// Sub is the subtree of From that appears at Target. 𝑢 of §3.3d.
	//
	// Empty means the whole of it. In the key beside From, because two steps
	// binding different subtrees of one object read different bytes.
	Sub string
	// View says the mount is a bound view rather than a cache or a credential.
	//
	// Distinguishable from the fields alone only by accident - a view of the
	// whole context has an empty Sub, and From is filled in later - so it is
	// said rather than inferred. §3.3d.
	View bool
	// Secret says the mount carries a credential rather than a cache.
	//
	// The *value* is deliberately absent from this struct and from every other
	// part of the graph. It is supplied at execution from the invocation, so
	// there is no field a credential could reach and no key it could change.
	// Carrying it and excluding it from the key would work until someone added
	// a hasher, and that failure is a credential in a cache key.
	Secret bool
	// ReadOnly binds it so the step cannot write through it.
	ReadOnly bool
	// Ephemeral is `CACHE --sharing=private`: a directory made for this step and
	// removed with it.
	//
	// A cache nothing else can see. It still keeps what the step writes out of
	// the image - that is what any mount does - and keeps it out of every other
	// step as well, which is what `private` means (E432).
	Ephemeral bool
	// Exclusive is `CACHE --sharing=locked`, the default: one step in this
	// directory at a time.
	//
	// The alternative is `shared`, where several steps use it at once and the
	// tools inside are trusted to cope - which npm and cargo do with their own
	// locks, and which a build declares deliberately.
	Exclusive bool
	// Persist puts the cache's contents into the image as well as keeping them:
	// `CACHE --persist`.
	//
	// Which side of the layer the contents land on, and not a detail. An
	// ordinary cache is *bound over* the step's filesystem, so what goes into it
	// is excluded from the layer by construction; --persist asks for the
	// opposite, so it cannot be a bind at all - the contents have to be written
	// into the step's own root to be captured.
	//
	// In the key, because the two produce different images from the same
	// command: one carries the cache's contents and one does not.
	Persist bool
}

// Op is a node's operation: green paper's ω.
type Op struct {
	Kind OpKind
	// Args is the command for OpExec and OpHost, the reference for OpImage,
	// the path for OpLocal, the target for OpBuild.
	Args []string
	// User is the account the operation runs as: USER.
	//
	// Unlike the rest of the image configuration - entrypoint, exposed ports,
	// labels - this changes what the operation *does*, not only what the image
	// says about itself. Running as root and running as nobody can produce
	// different filesystems, so it belongs to the operation and reaches the key.
	User string
	// NoCache says the author declared this step is not a function of its
	// inputs: `RUN --no-cache`.
	//
	// It reaches the key rather than sitting beside it, because the same
	// command with and without the flag are different requests and keying them
	// alike would let a cached run serve one that must not be cached. Honouring
	// it is a correctness matter and not a preference: a step that fetches the
	// latest of something, or reads the clock, produces a result the key cannot
	// bound - the same reasoning I7 applies to a host step.
	NoCache bool
	// NoNetwork says the step runs with no network at all: `RUN --network=none`.
	//
	// In the key for the reason NoCache is: the same command with and without a
	// network is a different request - one resolves a dependency, the other
	// fails to - and a cache that could not tell them apart would serve the
	// connected result for the isolated one, which is the false hit I3 exists
	// to prevent.
	//
	// It is a *reduction* in what the step may do, so honouring it late is
	// safe and honouring it wrongly is not: a step that asked to be cut off and
	// was not may reach the network and produce a result nobody can reproduce.
	NoNetwork bool
	// Interactive says the step runs on the caller's terminal:
	// `RUN --interactive`.
	//
	// In the key because a step a human typed into is not the step the same
	// command would have been without one, and because the arrangement decides
	// whether it can run at all - a terminal is a descriptor and does not cross
	// a machine.
	//
	// It does not make the step cacheable or not by itself; the interpreter
	// marks an interactive step uncacheable for the reason `--no-cache` exists,
	// because what a person typed is not a function of the inputs.
	Interactive bool
	// Docker says the step runs inside a WITH DOCKER block and is given a
	// docker daemon: the client on its PATH and a socket to talk to.
	//
	// In the key, because `RUN docker images` with a daemon and the same line
	// without one are different requests - the first lists images, the second
	// fails to find the command - and a cache that could not tell them apart
	// would serve one for the other.
	Docker bool
	// DockerCache names storage the inner daemon keeps between blocks, when the
	// author asked for one: `WITH DOCKER --cache-id=<name>`.
	//
	// **Sharing and cacheability are one axis.** A block naming a cache is given
	// a daemon holding whatever an earlier build left there, so what its steps
	// produce is not a function of their inputs - the interpreter marks them
	// `NoCache` for the reason `--no-cache` exists (I3). A block naming none
	// starts empty, is reproducible, and is cached; that is the mode a test of
	// this engine's own cache behaviour wants, and it is the default rather than
	// a flag to remember.
	//
	// In the key for the reason `Docker` is: a daemon holding one project's
	// images and a daemon holding another's answer `docker images` differently.
	DockerCache string
	// Hosts are name-to-address entries a step resolves by, as "name address".
	//
	// `HOST api.test 10.0.0.1`. Part of the operation rather than of the base,
	// because it changes what the operation *does*: `curl api.test` with an entry
	// and without one are two different commands wearing the same words, and a
	// key that did not describe them would serve one build's download to another
	// (I3).
	//
	// Ordered as written, and not sorted: the file is written in this order and a
	// later entry for a name the resolver already has is the author's business,
	// not this engine's to normalise away.
	Hosts []string
	// IsolateDocker is `WITH DOCKER --isolate`: start a daemon of this step's
	// own, whatever is already around it.
	//
	// **The flag is the opt-out, because sharing is the default.** A build
	// running inside a WITH DOCKER step - `earth` invoked in a container - has a
	// daemon around it already, and a nested block uses it: that is what an
	// author almost always wants, and making them ask for it would be a default
	// chosen for the minority.
	//
	// What the flag buys is the case that cannot be got right by sharing: a test
	// of this engine's own caching, which is looking for cache *misses* and is
	// silently wrong if it is handed hits. An isolated block's daemon writes into
	// the step's own overlay and dies with it, so the isolation is structural
	// rather than configured (E365) - and it is the only mode that can be cached,
	// because a shared daemon's contents are not a function of this step's
	// inputs (I3).
	IsolateDocker bool
	// Entrypoint says the step runs the base image's own entrypoint with Args
	// as its arguments: `RUN --entrypoint -- -f api.proto`.
	//
	// In the key, because running an image's entrypoint with some words and
	// running those words as a command are different operations - and the
	// entrypoint itself is in the key already, through the image the step
	// stands on.
	Entrypoint bool
	// DirCopy is `COPY --dir`: the directory itself rather than its contents.
	//
	// In the key, because the two put different trees in the image from the
	// same words - `COPY src .` contributes what is in src, and `COPY --dir src
	// .` contributes src.
	DirCopy bool
	// NoFollow is `COPY --symlink-no-follow`: a symlink the copy names arrives
	// as a link rather than as what it points at.
	//
	// In the key for the same reason as DirCopy, and more sharply: the two put
	// genuinely different filesystems in the image - one a link, one a tree -
	// from identical words. A flag that changes a result and not a key is a
	// false cache hit, which is the one failure I3 exists to forbid.
	//
	// Measured before implemented (E75): varying the flag one side at a time
	// against the reference showed the COPY decides and the SAVE ARTIFACT does
	// not, which is why only this side carries it.
	NoFollow bool
	// KeepOwn is `COPY --keep-own`: uid and gid travel with the copy.
	//
	// In the key with the rest: the same words produce files owned by different
	// users, and a service in the image that drops privileges to the user its
	// files belong to fails at runtime rather than at build time.
	KeepOwn bool
	// SSH is `RUN --ssh`: the invoking user's ssh agent is reachable from this
	// step.
	//
	// A bool rather than the socket's path, and that is the whole design: a path
	// like `/tmp/ssh-XXXX/agent.1234` is per-invocation, so keying on it would
	// make one build key differently in every session. The operation says an
	// agent is wanted; the executor finds it, the way the docker socket is
	// resolved (E466).
	SSH bool
	// Chown is `COPY --chown=user[:group]`: what the copied files belong to.
	//
	// The names are resolved against the *destination image*, not this machine
	// (A3), so this carries the specification rather than a pair of numbers -
	// which also keeps the key honest: two images resolving `www-data`
	// differently are two different results from one Earthfile, and the
	// Earthfile is what the key describes.
	Chown string
	// Image is the configuration an image carries when this operation writes
	// one: OpPackImage, and nothing else.
	//
	// It is here rather than looked up at execution because the executor has no
	// plan to look in - it is handed nodes. Written as layers alone, a loaded
	// image had no entrypoint and no command, and `docker run` answered
	// `no command specified` from inside a WITH DOCKER block, two targets away
	// from the ENTRYPOINT that had been dropped.
	//
	// In the key, because the configuration decides what the image *does*: two
	// loads of the same layers under different entrypoints are different images,
	// and a cache that could not tell them apart would run the wrong command -
	// which is the worst shape of wrong available.
	Image *ImageConfig

	// SecretEnv are credentials the step is given as environment variables:
	// `RUN --secret NAME[=SOURCE]`, held as written.
	//
	// Names only. `Env` is hashed, so putting a value there would put a
	// credential in the cache key - which is written to disk and shared between
	// machines. The names are keyed because asking for a different secret is a
	// different step; the values are supplied at execution and exist nowhere in
	// the graph.
	SecretEnv []string
	// Mounts are directories bound into the step's filesystem that outlive it:
	// CACHE.
	//
	// The *paths* reach the key; the contents cannot. That is the whole
	// difficulty with a cache mount and the reason a step carrying one is not
	// soundly cacheable: what it produces may depend on what was in the mount,
	// which no key can bound (I3). Mounting somewhere else is a different step,
	// so the paths belong in the key; trusting a result that depended on the
	// contents would be the false hit this engine exists to prevent.
	Mounts []Mount
	// Tolerate says a non-zero exit is a result rather than the end of the
	// build: TRY.
	//
	// The step still failed and the build still fails, at the end - but what
	// stands on it runs first, which is the entire reason TRY exists. `TRY / RUN
	// test > report; FINALLY / SAVE ARTIFACT report` only means anything if the
	// failed step's filesystem survives to be read.
	//
	// In the key, and the reason is not obvious: the *command* is the same
	// either way, but the outcomes differ where it matters. A tolerated failure
	// yields a filesystem that later steps use; an untolerated one yields
	// nothing at all. Two requests with different results are different
	// requests.
	Tolerate bool
	// Dir is the working directory the operation runs in: WORKDIR.
	//
	// Part of the operation because it changes what the operation does - `make`
	// in two directories is two different steps - which means it must reach the
	// key, and the reflective key-coverage guard enforces that without anyone
	// having to remember.
	Dir string
	// Content is a digest of external bytes this operation depends on: the
	// files a local context names, and nothing else so far.
	//
	// It exists because identity must cover everything an operation's result
	// depends on. A local context identified by its *path* would leave the graph
	// unchanged when a source file is edited, so every key would still match and
	// the build would hit the cache and reproduce the previous output - the most
	// damaging false hit available to a build tool, because it looks like a fast
	// build.
	Content NodeID
	// Env is the ambient state the operation may observe - green paper's ε.
	// Only variables named here are visible to the step, and every one of them
	// enters the cache key. Anything observable but absent from ε is a false
	// cache hit waiting to happen (I3).
	Env map[string]string
}

// Node is a step in the graph.
type Node struct {
	Op       Op
	Platform Platform
	// Inputs are direct references, in order. Order is significant: swapping
	// two inputs of a Copy changes the result.
	// Inputs are what this step stands on: their layers form its base.
	Inputs []*Node
	// Sources are what it reads without standing on.
	//
	// A build context, or another target whose artifact is copied. Their layers
	// are *not* stacked - `COPY +compile/binary /usr/bin/` takes one file, and
	// stacking compile's whole filesystem underneath would merge an entire image
	// in - but they decide the result, so they reach the key.
	//
	// Structural rather than inferred from an input's kind, which is what it was:
	// the scheduler special-cased OpLocal, so an artifact source - an ordinary
	// node - would have been stacked.
	Sources []*Node
	// After are steps that must finish first, whose results this one does not
	// use: WAIT.
	//
	// Neither Inputs nor Sources can say this. An input stacks a layer and a
	// source puts one in the key; an ordering edge does neither, because what is
	// being waited for is a *side effect* - an image pushed, a file written on
	// this machine - with no layer to take. Expressing it as an input would
	// stack a filesystem nobody asked for.
	//
	// Deliberately absent from the identity. Ordering changes when work happens
	// and not what it produces, so two builds differing only in a WAIT do the
	// same work and must share cache entries; keying on it would make a WAIT
	// invalidate everything after it, punishing the one construct people reach
	// for when they need correctness.
	After []*Node
	// OnFailure names a step whose failure is this step's reason to run: the
	// CATCH body of a TRY, which exists to inspect what went wrong. When that
	// step succeeds this one is skipped, along with anything standing on it.
	//
	// Absent from the identity for the reason After is, and the reason is
	// sharper here: it decides *whether* the step runs, never what the step
	// computes. A handler keyed differently from the identical command written
	// outside a TRY would miss a cache entry it is entitled to.
	//
	// The step is also an input, because a handler runs in the build
	// environment the failure left behind - the only place worth inspecting
	// after one - so ordering comes for free and needs no After edge.
	OnFailure *Node
	// Meta is typed metadata - the thing LLB has no room for, which is why
	// util/vertexmeta exists today, smuggling base64 JSON through a display
	// name. Here it is a struct field and never enters the identity.
	Meta Meta

	// id is the memoised identity. Atomic because a node is reachable from two
	// graphs at once - a shared subgraph is the normal case, not a corner - and
	// the first caller to want its identity may not be the only one. Two
	// callers racing compute the same digest, so the store is idempotent and
	// no lock is needed to make it correct; the atomic is there to make it
	// legal. See ID.
	id atomic.Pointer[NodeID]
}

// ImageConfig is what an image declares about how it runs.
//
// A deliberately smaller thing than the OCI configuration: only the fields an
// Earthfile can set and a daemon acts on. Growing it means growing the key, so
// a field arrives here when something needs it rather than because the format
// has one.
type ImageConfig struct {
	Entrypoint []string
	Cmd        []string
	Env        []string // "K=V", ordered, because a map has none and the key needs one
	WorkingDir string
	User       string
	Labels     map[string]string
	Exposed    []string
	Volumes    []string
	// Healthcheck is how a running container reports its own health, nil when
	// the image says nothing about it.
	//
	// In the key, because an image that declares one is a different image from
	// the same layers without it - which is the whole reason for recording it
	// rather than noting it beside the plan (E486).
	Healthcheck *Healthcheck
}

// Healthcheck is a HEALTHCHECK, in the form an image config carries it.
//
// `Test` is `["NONE"]` or `["CMD-SHELL", "<command>"]`: a daemon's shape rather
// than a tidier one of this engine's, so that writing an image is a copy and not
// a conversion.
type Healthcheck struct {
	Test          []string
	Interval      time.Duration
	Timeout       time.Duration
	StartPeriod   time.Duration
	StartInterval time.Duration
	Retries       int
}

// Meta is diagnostic and scheduling information that does not affect a result
// and therefore never enters a node's identity.
type Meta struct {
	// Description is what a human is shown for this step.
	Description string
	// Source locates the Earthfile line that produced this node.
	Source string
	// Target names the Earthfile target this node belongs to.
	Target string
	// ContextRoot is the directory a build-context entry was read from.
	//
	// A build has one `-dir`, and an Earthfile referred to across directories
	// has its own: `../js+build` copies index.js from beside *that* Earthfile.
	// Joining every context path to the invocation's directory made a
	// referenced target read the caller's tree, and the failure arrived at
	// execution after a plan that was entirely correct.
	//
	// Here rather than in the operation because identity is the file's
	// *content*: two identical files in different directories are the same
	// layer, and should stay one.
	ContextRoot string
	// ReadsPredicted is what a step of this class read last time.
	//
	// Advice, and it travels here because the executor materialises a step's
	// base and only the node reaches it. **Not in the identity** - `Meta` is not
	// hashed, and there is a test that says so, because "this field is not in
	// the key" is the kind of thing that is true when written and quietly false
	// two refactors later (E301).
	//
	// A worker fills it from the assignment's hints; everywhere else it is
	// empty, and empty means materialise the whole base.
	// Last, so the string fields above keep their pointers together and the
	// collector stops scanning sooner (govet fieldalignment).
	ReadsPredicted []string
}

// NodeID is a node's content-derived identity.
type NodeID [HashSize]byte

// String renders an ID as hex, which is what appears in diagnostics and what
// ties are broken on.
func (n NodeID) String() string {
	const hexit = "0123456789abcdef"

	var sb strings.Builder

	sb.Grow(HashSize * 2)

	for _, b := range n {
		sb.WriteByte(hexit[b>>4])
		sb.WriteByte(hexit[b&0x0f])
	}

	return sb.String()
}

// Less orders IDs. Scheduling ties are broken by this, so that a schedule is
// reproducible across runs and across machines (green paper §4.7.3).
func (n NodeID) Less(o NodeID) bool {
	for i := range n {
		if n[i] != o[i] {
			return n[i] < o[i]
		}
	}

	return false
}

// ID returns the node's identity, computing it once.
//
// Identity covers the operation, the platform and the identities of the inputs
// in order. It deliberately excludes Meta: two nodes differing only in their
// description are the same step and must share a cache entry.
func (n *Node) ID() NodeID {
	if id := n.id.Load(); id != nil {
		return *id
	}

	h := NewHasher()

	h.Byte(byte(n.Op.Kind))
	h.Str(n.Platform.OS)
	h.Str(n.Platform.Arch)
	h.Str(n.Platform.Variant)

	h.Count(len(n.Op.Args))

	for _, a := range n.Op.Args {
		h.Str(a)
	}

	// Fixed width by §3.1, so no prefix. The zero value is written for
	// operations with no external content, which keeps the encoding injective
	// rather than making the field optional.
	h.Str(n.Op.Dir)
	h.Str(n.Op.User)
	h.Bool(n.Op.NoCache)
	h.Bool(n.Op.NoNetwork)
	h.Bool(n.Op.Interactive)
	h.Bool(n.Op.Docker)
	h.Str(n.Op.DockerCache)
	h.Bool(n.Op.IsolateDocker)
	// Counted before they are written, like every other list here: without a
	// count, one entry "a b" and two entries "a" and "b" hash the same.
	h.Count(len(n.Op.Hosts))

	for _, entry := range n.Op.Hosts {
		h.Str(entry)
	}
	h.Bool(n.Op.SSH)
	h.Bool(n.Op.Entrypoint)
	h.Bool(n.Op.DirCopy)
	h.Bool(n.Op.NoFollow)
	h.Bool(n.Op.KeepOwn)
	h.Str(n.Op.Chown)
	h.Bool(n.Op.Tolerate)

	h.Count(len(n.Op.SecretEnv))

	for _, name := range n.Op.SecretEnv {
		h.Str(name)
	}

	// The image's own configuration, when this operation writes one. Two loads
	// of the same layers under different entrypoints are different images, so
	// they are different steps.
	HashImage(h, n.Op.Image)

	// Mount paths, in order: mounting the same directory somewhere else is a
	// different step. A *cache* mount's contents are deliberately absent - they
	// are exactly what a key cannot bound. A bound view's are not: they are
	// bounded by From, which is a key over them (§3.3d).
	h.Count(len(n.Op.Mounts))

	for _, m := range n.Op.Mounts {
		h.Str(m.Target)
		h.Str(m.ID)
		h.Bool(m.ReadOnly)
		// Whether the mount is a credential, not the credential: the value is
		// deliberately outside the graph and this is a bool. A mount named
		// "token" carrying a secret and one carrying a cache are different
		// things, and until now they keyed the same (E432).
		h.Bool(m.Secret)
		h.Bool(m.Ephemeral)
		h.Bool(m.Exclusive)
		h.Bool(m.Persist)
		h.Count(int(m.Mode))
		h.Str(m.Sandbox)
		// A bound view's object and subtree. **Its contents are keyed**, unlike
		// a cache mount's - and they are keyed by this, because From is already
		// a key over them (I20, §3.3d). A cache mount is a function of history
		// and a step may find one empty; a bound view is a function of the
		// graph, the step reads it, and it decides the result.
		h.Fixed(m.From[:])
		h.Str(m.Sub)
		// Whether it is a view at all. A cache mount at the same target with
		// the same (zero) From is a different thing entirely: one is emptiable
		// and outside the key's reach, the other is content this build made.
		h.Bool(m.View)
	}
	h.Fixed(n.Op.Content[:])

	// Env is sorted: map iteration order must not reach the identity, or the
	// same step hashes differently between runs.
	keys := make([]string, 0, len(n.Op.Env))
	for k := range n.Op.Env {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	h.Count(len(keys))

	for _, k := range keys {
		h.Str(k)
		h.Str(n.Op.Env[k])
	}

	h.Count(len(n.Inputs))

	for _, in := range n.Inputs {
		id := in.ID()
		h.Fixed(id[:])
	}

	h.Count(len(n.Sources))

	for _, src := range n.Sources {
		id := src.ID()
		h.Fixed(id[:])
	}

	id := h.Sum()
	n.id.Store(&id)

	return id
}

// HashImage writes an image configuration into the encoding of §1.4.
//
// Exported because the chain key in engine/core encodes an operation too, and
// the two must agree: a field that reaches one and not the other is a step that
// is a different step by identity and the same one by cache key.
//
// A presence byte first, because "no configuration" and "an empty one" are
// different claims: a node that carries none is one that writes no image.
func HashImage(h *Hasher, c *ImageConfig) {
	if c == nil {
		h.Bool(false)

		return
	}

	h.Bool(true)

	for _, list := range [][]string{c.Entrypoint, c.Cmd, c.Env, c.Exposed, c.Volumes} {
		h.Count(len(list))

		for _, v := range list {
			h.Str(v)
		}
	}

	h.Str(c.WorkingDir)
	h.Str(c.User)

	// Sorted, because a map has no order and an image's identity must not
	// depend on one: the same labels would otherwise digest differently on
	// every run.
	keys := make([]string, 0, len(c.Labels))
	for k := range c.Labels {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	h.Count(len(keys))

	for _, k := range keys {
		h.Str(k)
		h.Str(c.Labels[k])
	}

	hashHealthcheck(h, c.Healthcheck)
}

// hashHealthcheck adds a healthcheck to an image's identity.
//
// A presence byte first, for the reason HashImage has one: an image that says
// *nothing* about its health and one that says `NONE` are different claims -
// NONE overrides whatever the base declared - and hashing them alike would let
// one be served for the other (E486).
//
// **No mutant guards this byte, and that is deliberate.** Removing it survives
// every test, because this is the last field hashed: nothing follows, so an
// absent healthcheck writes nothing and a present one writes at least a count,
// and the two cannot be confused. The byte is here for the field that gets
// appended after it - the moment one is, dropping it is a collision between
// "no healthcheck, then X" and "a healthcheck that begins like X". Written down
// rather than guarded, because a mutant that cannot fail is worse than none.
func hashHealthcheck(h *Hasher, hc *Healthcheck) {
	if hc == nil {
		h.Bool(false)

		return
	}

	h.Bool(true)
	h.Count(len(hc.Test))

	for _, v := range hc.Test {
		h.Str(v)
	}

	// As nanoseconds, so the key does not depend on how a duration prints.
	for _, d := range []time.Duration{
		hc.Interval, hc.Timeout, hc.StartPeriod, hc.StartInterval,
	} {
		h.Count(int(d.Nanoseconds()))
	}

	h.Count(hc.Retries)
}

// Graph is a build's node set with a designated root.
type Graph struct {
	Root *Node
	// Also are steps the build must run that the root does not stand on: what
	// `BUILD +other` means.
	//
	// A second root rather than an operation, because that is the semantics. An
	// operation taking the dependency as an input would put its layers in this
	// target's base - which is what FROM means, not BUILD - and the difference
	// is a target quietly inheriting a filesystem it never asked for.
	Also []*Node
}

// Nodes returns every node reachable from the root, in a deterministic order:
// depth-first post-order, so a node always appears after its inputs, with ties
// broken by identity. Duplicate nodes - the same step reached by two paths -
// appear once.
//
// This is the toposort shape from the ticktock prototype
// (buildkit solver/simple.go exploreVertices), which was the part of it worth
// keeping.
func (g *Graph) Nodes() []*Node {
	var (
		out  []*Node
		seen = map[NodeID]bool{}
	)

	var visit func(*Node)

	visit = func(n *Node) {
		if n == nil || seen[n.ID()] {
			return
		}

		seen[n.ID()] = true

		// Sort inputs by identity before descending, so the traversal order
		// does not depend on how the graph was built.
		// Ordering edges are traversed too: a step that is only waited for is
		// still part of the build, and leaving it out of the traversal would
		// mean it is never scheduled - a WAIT block whose contents silently do
		// not happen.
		ins := make([]*Node, 0, len(n.Inputs)+len(n.Sources)+len(n.After))
		ins = append(ins, n.Inputs...)
		ins = append(ins, n.Sources...)
		ins = append(ins, n.After...)
		sort.Slice(ins, func(i, j int) bool { return ins[i].ID().Less(ins[j].ID()) })

		for _, in := range ins {
			visit(in)
		}

		out = append(out, n)
	}

	visit(g.Root)

	// After the root, so a shared subgraph is ordered by the main chain's needs
	// and the traversal stays deterministic.
	for _, n := range g.Also {
		visit(n)
	}

	return out
}
