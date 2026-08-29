package core

import (
	"sort"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Domain tags separate key spaces so that a chain key can never collide with an
// observed-input key (green paper §4.4). One fixed byte, no prefix needed.
const (
	domainChain     = 0x01 // Κ₁
	domainObserved  = 0x02 // Κ₂ - stage S5
	domainClass     = 0x04 // step class, for profile lookup
	domainComponent = 0x05 // key components, for divergence attribution
)

// Key is a cache key: green paper's κ.
type Key = ir.NodeID

// DeriveChainKey computes Κ₁, green paper (4.5):
//
//	Κ₁(s) ≡ ℋ(0x01 ‖ ids(𝑏) ‖ 𝒮(ω) ‖ 𝒮(ε) ‖ 𝒮(π))
//
// The base 𝑏 is the sequence of *resolved* layer identities the step will run
// over, not the identities of the nodes that produced them. That distinction is
// the whole point of a chain key: it is a claim about a step over concrete
// inputs, which is what a cache entry can be about.
//
// The ticktock prototype learned this the expensive way - keying its dedup lock
// on the LLB digest rather than the computed key, discovering the bug, and
// regressing it once before it stuck. The same effective operation reached
// through different ancestry has different node identities and the same chain
// key, and it is the chain key that must govern.
func DeriveChainKey(n *ir.Node, base, refs []ir.NodeID) Key {
	return deriveChainKeyAtEpoch(n, base, refs, cacheEpoch)
}

func deriveChainKeyAtEpoch(n *ir.Node, base, refs []ir.NodeID, epoch int) Key {
	h := ir.NewHasher()

	h.Byte(domainChain)
	// The generation this entry belongs to. A false L2 hit is recorded under
	// this key, over a base that is correct, so poison reaches Κ₁ and only an
	// epoch here can retire it. See cacheEpoch.
	h.Count(epoch)

	// ids(𝑏): a sequence of fixed-width digests, so one count and then raw
	// bytes - no per-element prefix (§1.4).
	h.Count(len(base))

	for _, id := range base {
		h.Fixed(id[:])
	}

	// 𝒮(ω), including refs where this derivation has them - see hashOperation.
	hashOperation(h, n, refs)

	// 𝒮(ε) ‖ 𝒮(π)
	hashEnvAndPlatform(h, n)

	return h.Sum()
}

// hashOperation writes 𝒮(ω): everything about the operation itself.
//
// One function because three derivations need it - Κ₁ (4.5), Κ₂ (4.6) and the
// step class - and both of the latter had grown their own, covering the kind,
// the arguments and nothing else. Nine fields including `Dir`, `User`,
// `NoFollow` and `KeepOwn` were absent from them, so:
//
//	RUN --user root  install …     derived one class and one Κ₂ with
//	RUN --user build install …
//
// which at S5 serves the root build's layer for the unprivileged one - silently,
// since the build succeeds and only the ownership in the image is wrong (I3).
//
// The green paper already settled this: (4.5) and (4.6) both name 𝒮(ω), the
// same serialisation of the same operation. Two implementations of one symbol is
// a defect in the code and not a design question.
//
// `refs` is the exception and is a parameter rather than a field of the
// operation. Κ₁ and Κ₂ pass them - a COPY that reads an edited context must not
// hit - and the class passes nil deliberately: a class is a *prediction* key,
// and one that changed whenever a source file changed would predict for nothing.
// Safety does not rest on it, because `tryL2` derives the exact Κ₂ before it
// serves anything.
//
// Field order is Κ₁'s existing order, and refs sit where Κ₁ put them, so this
// refactoring does not invalidate a single cached entry.
func hashOperation(h *ir.Hasher, n *ir.Node, refs []ir.NodeID) {
	h.Byte(byte(n.Op.Kind))
	h.Count(len(n.Op.Args))

	for _, a := range n.Op.Args {
		h.Str(a)
	}

	// refs: inputs the step reads but does not stand on - a local context, which
	// COPY reads from and which is deliberately absent from the base (a context
	// is a source, not a base layer). They still decide the result, so they still
	// decide the key. Omitting them left COPY hitting the cache after its source
	// was edited.
	h.Count(len(refs))

	for _, id := range refs {
		h.Fixed(id[:])
	}

	h.Str(n.Op.Dir)
	h.Str(n.Op.User)
	h.Bool(n.Op.AWS)
	h.Bool(n.Op.NoCache)
	h.Bool(n.Op.IfExists)
	h.Str(n.Op.As)
	h.Str(n.Op.Chmod)
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

	// The value-derived half, where a fleet key is configured (I19).
	ir.HashSecretDigest(h, n.Op.SecretDigest)

	// The image's own configuration, when this step writes one: two loads of
	// the same layers under different entrypoints run different commands, so
	// they cannot share a cache entry.
	ir.HashImage(h, n.Op.Image)

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
		h.Bool(m.Tmpfs)
		h.Bool(m.Exclusive)
		h.Bool(m.Persist)
		h.Count(int(m.Mode))
		h.Str(m.Sandbox)
		// A bound view's object and subtree. **Its contents are keyed**, unlike
		// a cache mount's - and they are keyed by this, because From is already
		// a key over them (I20, §3.3d). A cache mount is a function of history
		// and a step may find one empty; a bound view is a function of the
		// graph, the step reads it, and it decides the result.
		//
		// Not redundant with `refs`, which is the reasonable objection: refs
		// carry the sources' result *layers* and so already bring the bytes
		// into Κ₁. What they cannot say is *which* source a mount shows. A step
		// binding one of its two sources and a step binding the other read
		// different files and would otherwise key identically.
		h.Fixed(m.From[:])
		h.Str(m.Sub)
		// Whether it is a view at all. A cache mount at the same target with
		// the same (zero) From is a different thing entirely: one is emptiable
		// and outside the key's reach, the other is content this build made.
		h.Bool(m.View)
	}

	// The operation's external content - the bytes a local context names. Fixed
	// width by §3.1, so no prefix, and the zero value is written for operations
	// that have none, which keeps the encoding injective.
	//
	// Omitting this produced a false hit that reached a real build: editing a
	// copied source file changed the node's *identity* but not its key, so four
	// steps reported L1 hits and the previous output was written over an edited
	// source. Identity and key are derived by different functions over the same
	// operation; anything the result depends on belongs in both.
	h.Fixed(n.Op.Content[:])
}

// hashEnvAndPlatform writes 𝒮(ε) ‖ 𝒮(π), which all three derivations end with.
func hashEnvAndPlatform(h *ir.Hasher, n *ir.Node) {
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

	h.Str(n.Platform.OS)
	h.Str(n.Platform.Arch)
	h.Str(n.Platform.Variant)
}

// Entry is an action-cache record: green paper (2.3), 𝔸 ≡ (𝑑, 𝑤, 𝑠, 𝑝).
//
// Attestation and provenance are declared but unused until there is a fleet to
// distrust. Their absence is why Lookup currently verifies only what it can.
type Entry struct {
	// Layer is the result digest 𝑑.
	Layer ir.NodeID

	// Layers is the stack this result names, when it names more than one.
	//
	// **An image is many layers**, and with the store on the guest's device
	// that is how one arrives - `Result.Layers`, oldest first, with no single
	// delta to stand for it. An entry that could hold only `Layer` therefore
	// could not hold an image at all: every `FROM` was dropped by the
	// zero-layer guard below and missed on every subsequent build, which then
	// started a sandbox to re-materialise an image the store already held
	// (E872).
	//
	// Empty for almost every step, exactly as it is on Result: a RUN produces
	// one delta and Layer carries it.
	Layers []ir.NodeID
	// Content is the same delta with timestamps excluded, and it is what two
	// claims are compared on.
	//
	// A layer's identity includes its timestamps (I8), so two runs of one
	// deterministic step produce two Layers: creating a directory stamps it
	// with the wall clock. Measured - `RUN mkdir -p /out/dir && echo fixed >
	// /out/dir/a.txt` built twice from a cold store gives two different layer
	// digests with the base image identical both times.
	//
	// Comparing Layer therefore reads every re-run after eviction as a step
	// that produced two different results, which is most steps and would train
	// a reader out of the one diagnostic this engine has for non-determinism.
	//
	// Zero where nobody computed one - a host step, an entry from before this
	// field - and the comparison falls back to Layer there rather than treating
	// absence as agreement.
	Content ir.NodeID
	// Exit is the recorded exit code.
	Exit int
	// Bytes is the recorded output size, which the cost model reads.
	Bytes int64
	// Writer identifies who published this entry, 𝑤.
	Writer string
	// Declares is the declaration this step's result carries, and Declared says
	// whether anybody looked.
	//
	// Two fields for the reason Content and Captured are two things: a zero
	// identity means "declares nothing", which is a fact about the image, and an
	// entry written before declarations existed means "nobody recorded what it
	// says", which is a fact about the entry. Read as the same, a cached FROM
	// serves a stack with no declaration and the step above it runs without the
	// environment its image sets (§3.2a).
	Declares ir.NodeID
	Declared bool
}

// usableDeclaration reports whether an entry's declaration may be believed.
//
// Only an image is expected to carry one, so a step's entry is not refused for
// lacking what it never had - which matters because every entry written before
// this field existed says nothing, and refusing all of them would empty the
// cache for one kind of node's benefit.
func usableDeclaration(kind ir.OpKind, e Entry) bool {
	return kind != ir.OpImage || e.Declared
}

// ActionCache is the 𝔄 port: key ↦ claim. Unlike the blob store it is not
// self-verifying, and every security property in green paper §5.2 exists
// because of that asymmetry.
//
// **Get and Put are called concurrently.** Independent steps are evaluated at
// the same time, so an implementation with shared state needs its own lock -
// the same obligation `Executor.Run` states, and for the same reason. The real
// store satisfies it by writing one file per entry and renaming it into place;
// the in-memory fake did not, and a bare map is a `fatal error: concurrent map
// writes` rather than a wrong answer.
//
// Unstated until a test finally ran six independent steps at once (E139). It
// had been true of the scheduler since it stopped being serial, and nothing had
// asked for enough concurrency to find out.
type ActionCache interface {
	Get(k Key) (Entry, bool)
	Put(k Key, e Entry)
}

// BlobStore is the 𝔅 port: digest ↦ bytes. Self-verifying by construction -
// ask for a digest, hash what arrives, reject a mismatch - so an attacker with
// total control of it can deny service and nothing else (green paper §2.1).
//
// At stage S1 it exists only so Lookup can check that an entry's result is
// actually present. Real bytes arrive at S2.
type BlobStore interface {
	Has(id ir.NodeID) bool
}

// Lookup is Λ, green paper (4.4).
//
// It has exactly two outcomes: a verified entry, or a miss. There is no third.
// A malformed entry, an unknown writer, a result whose blob is absent - every
// one returns a miss, meaning "do the work". Λ never returns an error and never
// returns an unverified entry (invariant I4).
//
// That single property is what converts every failure of the caching system,
// malicious or accidental, into a performance cost rather than a wrong answer.
// It is expressed as a type with no error variant so that a third outcome
// cannot be added by accident.
func Lookup(ac ActionCache, bs BlobStore, allowed map[string]bool, k Key) (Entry, bool) {
	if ac == nil {
		return Entry{}, false
	}

	e, ok := ac.Get(k)
	if !ok {
		return Entry{}, false
	}

	// An entry from a writer outside the trust domain is data, not a result
	// (green paper §5.3, A5).
	if allowed != nil && !allowed[e.Writer] {
		return Entry{}, false
	}

	// A claim whose result is not present is not usable, however well signed.
	// **Every layer of a stack**, not just the first: a partial stack
	// materialises a filesystem missing an element, and nothing downstream
	// could tell that from a complete one.
	if bs != nil && !held(bs, e) {
		return Entry{}, false
	}

	var zero ir.NodeID
	if e.Layer == zero && len(e.Layers) == 0 {
		return Entry{}, false
	}

	return e, true
}

// held reports whether the blob store holds everything this entry names.
//
// A stack is all-or-nothing: a hit that materialised some of an image's layers
// would produce a filesystem missing an element, and the build above it could
// not tell that from a complete one. Checking the first layer and trusting the
// rest is the same mistake as checking none.
func held(bs BlobStore, e Entry) bool {
	var zero ir.NodeID
	if e.Layer != zero && !bs.Has(e.Layer) {
		return false
	}

	for _, l := range e.Layers {
		if l != zero && !bs.Has(l) {
			return false
		}
	}

	return true
}
