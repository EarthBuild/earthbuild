# The Green Paper

*The specification of the EarthBuild engine.*

**Status: incomplete, not provisional.** What is written here is asserted. The engine conforms to
this document; where the code and this document disagree, one of them is a defect and the
disagreement is resolved rather than tolerated. Sections still to be written are marked
**[GAP]** in place, so that absence is deliberate and visible.

Green for earth. The form is borrowed from the Ethereum Yellow Paper by way of the JAM Gray
Paper: define the state, define the transition over it, define every symbol before use, number the
equations, and keep assumptions separate from mechanism.

## 0. Purpose

The RFC argues *why*, the plan describes *how* and *when*, the experiments record *what was
measured*. None of them define *what the engine is*.

**The justification for formality is that independent implementations must agree.** Two engines,
BuildKit and native, must agree on Earthfile semantics. N fleet workers must agree on what a step
produces. A cache entry written by one machine is consumed by another, on another platform, weeks
later. Wherever independent implementations must agree, ambiguity in prose becomes divergence in
practice - and divergence in a cache is a wrong artefact, delivered silently.

That is the standard this document is held to: can two people implement §4 and get the same
answer, and does §5 forbid the failures that matter.

### 0.0 Work not done

**The cheapest computation is the one that does not happen, and it is also the cleanest.** A build
system's output is an artefact; its by-product is heat. Every cache hit is a compilation that did not
run, a CPU that did not spin and a watt that was not drawn - which is the same statement whether it
is read as latency, as cost, or as carbon. This is the *earth* the name refers to, and it is a design
principle rather than a sentiment: where two designs produce the same artefact, the one that computes
less is the better one, even where the wall clock cannot tell them apart.

Three consequences run through the rest of this document.

**Once, not once per machine.** N workers each materialising the same base do N times the download,
N times the unpack and N times the hashing to reach a byte-identical result. One worker doing it and
the others fetching what it produced does it once. This is why a layer is content-addressed (§3.2)
and why a fleet exchanges layers rather than instructions: not to be quick, but so that the same
work is not paid for N times. A fleet that cannot share a base is not merely slower than one that
can - it is N times more expensive for an identical answer, and the difference grows with the fleet.

**Move the data less.** Moving a byte is work, and moving it twice is work done twice for a result
that was already correct after the first. The costs compound quietly because each handling is
defensible on its own: a pull writes an image, a materialise places it, a capture reads it back to
learn the name it will be filed under, a step reads it again through whatever it was placed on, and a
transfer packs it, sends it, unpacks it and hashes it once more to learn a name the sender already
knew. Six handlings, each with a good reason, of bytes that never changed.

The rule that follows is not "copy less often" but **let the bytes land once, where they will be
used, named as they arrive**. A name computed while the bytes are already passing costs nothing; the
same name computed later costs a full read. Content addressing is what makes this possible - a digest
taken at the moment of writing is as good as one taken afterwards - and the specification therefore
requires it to be cheap to preserve rather than expensive to recover (§3.2).

**A resource nobody is using should stop.** An idle sandbox draws power to serve nobody; an idle
worker holds a machine that could be off. Nothing that persists for the convenience of a later build
may persist indefinitely without something deciding it is still wanted, and that decision belongs
where it survives the death of whatever was using it (§C.5).

**Speculation is the exception, and is bounded because of this.** §4.7.4 permits work that may be
discarded, which is the one mechanism here that deliberately spends energy on an answer nobody may
need. It is not forbidden - a build that finishes sooner may leave the machine idle sooner - but it
is the only place where computing *more* is allowed, so the burden of showing it pays falls on it
rather than on the alternative.

The tension with §6 is real and stated rather than resolved: determinism screening re-runs steps that
are believed deterministic, spending energy to learn something no single build needs. It buys the
right to share results at all, which saves incomparably more - but a screening rate is a cost, not a
free check, and §6's floor above zero is where that trade is set.

### 0.1 Assumptions

Stated apart from the mechanism, because each is a place where the specification can be true and
the system still wrong.

* **A1.** ℋ is collision-resistant. Every identity claim in this document reduces to this.
* **A2.** The host filesystem preserves the metadata enumerated in §3.3. Where it does not - a
  filesystem without nanosecond timestamps, or without xattrs - results remain correct but I8 is
  unenforceable and the engine must say so rather than silently degrade.
* **A3.** The executor isolates a step's writes to its own upper layer. A step that escapes its
  sandbox invalidates every cache claim in this document, because ε (§4.4) no longer bounds what
  it observed.
* **A4.** Clocks are used for scheduling and diagnostics only. No cache decision depends on wall
  time.
* **A5.** Within a trust domain (§5.3) the writer of a cache entry is authorised. Cross-domain
  entries are unauthenticated data until verified.
* **A6.** The Earthfile language is defined by its grammar, `internal/earthfile/earthfile.abnf`.
  This document specifies what an engine *does* with an Earthfile; the grammar specifies what an
  Earthfile *is*, and where the two disagree about syntax the grammar governs.

  Stated as an assumption because it is a place where this specification can be entirely true and
  the engine still wrong. An interpreter that infers syntax from examples rather than from the
  grammar produces a build that is correct about everything except what the author wrote - and
  reports the difference as the author's mistake. Quoting is the instance that proved it: the
  grammar defines `path` as excluding quote characters unquoted and permitting `QUOTED-STRING`
  otherwise, so quotes delimit a value and are not part of it. Treating them as part of it produced
  "\"wildcard-copy.earth\" is not in the build context" - a file nobody has - 226 times across one
  repository.

## 1. Notational conventions

### 1.1 Typography

* **Sets** in double-struck: 𝔹 byte strings, 𝔻 digests, 𝕂 keys, 𝔸 attestations, 𝕊 steps,
  𝕃 layers, ℙ paths, ℕ non-negative integers.
* **Components of state** in fraktur: 𝔅 blobs, 𝔄 action cache, 𝔐 masks, 𝔇 beliefs, ℜ records.
  Fraktur denotes a store; double-struck denotes the set its members are drawn from.
* **Persistent values** in lower-case Greek: σ engine state, ℓ a layer, κ a key, ρ a result.
* **Functions introduced here** in upper-case Greek: Υ build, Σ step, Κ key derivation, Δ capture,
  Φ flattening, Λ lookup, Ω observation, Μ mask consultation.
* **Imported functions** in calligraphic: ℋ the hash, 𝒮 the canonical serialisation (Appendix B.1).
* **Documents** in script: ℰ an Earthfile.
* **Aggregates** in bold: 𝐀 the artefacts a build yields.
* **Cryptographic keys** in italic roman: 𝑘. Distinct from κ, a cache key.
* **Local values** in lower-case roman: 𝑖, 𝑗 indices; 𝑥, 𝑦 members.

### 1.2 Operators

| Notation       | Meaning                                                    |
| -------------- | ---------------------------------------------------------- |
| 𝑎 ‖ 𝑏          | injective concatenation of byte strings - see §1.4         |
| ⟨𝑥₀, 𝑥₁, …⟩    | a sequence                                                 |
| {𝑥 ∈ 𝕏 : 𝑃(𝑥)} | set comprehension                                          |
| sort(𝑆)        | the sequence of 𝑆 in ascending lexicographic order of 𝒮(𝑥) |
| 𝑓[𝑥]           | application of a partial map; ⊥ where undefined            |
| 𝑓 ⊕ {𝑥 ↦ 𝑦}    | the map 𝑓 updated at 𝑥                                     |
| ⊥              | absent, undefined, or "no answer"                          |
| ω(s), ε(s)     | accessors: the named component of a tuple                  |

### 1.3 Subscripts

Subscripts are written with the Unicode subscript characters, never with an underscore. Unicode
provides digits ₀-₉ and a partial Latin set - ₐ ₑ ₕ ᵢ ⱼ ₖ ₗ ₘ ₙ ₒ ₚ ᵣ ₛ ₜ ᵤ ᵥ ₓ - with no `c`,
no `d`, no uppercase and almost no Greek.

**Where a subscript cannot be expressed, change the symbol rather than fake it.** A mixture of
real subscripts and underscored ones reads as a typographical error and invites transcription
mistakes. In practice this means:

* number things when the number is meaningful - Κ₁ and Κ₂ are the keys consulted at lookup levels
  L1 and L2, which is more informative than "chain" and "observed" abbreviated to letters that do
  not exist as subscripts;
* use **accessor functions** for tuple components - ω(s) not s_ω - which is more precise anyway,
  since it states that the component is a function of the tuple;
* leave field names of serialised structures in `code font`, where they are identifiers rather
  than mathematical symbols.

### 1.4 Concatenation must be injective

Naive concatenation admits collisions between distinct inputs - ⟨"ab", "c"⟩ and ⟨"a", "bc"⟩ -
which under §4.4 is a false cache hit. The requirement on ‖ is therefore **injectivity**: distinct
input sequences must produce distinct byte strings.

Length prefixing is one way to achieve that, not the requirement itself. It is required only where
the length can vary:

| Field shape                             | Encoding                                | Prefix?                   |
| --------------------------------------- | --------------------------------------- | ------------------------- |
| fixed width, at a schema-fixed position | the bytes                               | **no**                    |
| variable width                          | `u32` length, then the bytes            | yes                       |
| sequence of fixed-width elements        | `u32` count, then the raw elements      | **once**, not per element |
| sequence of variable-width elements     | `u32` count, then each element prefixed | yes                       |

**The trap is "fixed in practice".** A field is fixed-width only if the *schema* fixes it, not if
it merely happens to be constant. An internal digest is 32 bytes because §3.1 fixes ℋ to
BLAKE3-256 for the life of this specification - not because it currently happens to be that hash.
Where a field's width depends on anything this document does not fix, it is variable and must be
prefixed.

The saving is real but modest: for a step with 50,000 inputs, prefixing each digest individually
would add roughly 200 KB of prefix bytes to about 1.6 MB of digests, some 12% of the hashing work.
Worth taking. Not a reason to compromise injectivity anywhere.

## 2. State

```text
(2.1)    σ ≡ (𝔅, 𝔄, 𝔐, 𝔇, ℜ)
```

| Symbol | Name                | Type        | Verifiable               |
| ------ | ------------------- | ----------- | ------------------------ |
| 𝔅      | blob store          | 𝔻 ⇀ 𝔹       | **yes** - rehash on read |
| 𝔄      | action cache        | 𝕂 ⇀ 𝔸       | **no** - a claim (§5.2)  |
| 𝔐      | masks               | 𝕂ₘ ⇀ bitmap | hint only                |
| 𝔇      | determinism beliefs | 𝕂ₛ ⇀ ℕ × ℕ  | hint only                |
| ℜ      | build records       | 𝔻 ⇀ record  | evidence                 |

### 2.1 The blob store

```text
(2.2)    ∀ 𝑑 ∈ dom(𝔅) :  ℋ(𝔅[𝑑]) = 𝑑
```

Equation 2.2 is the reason 𝔅 cannot be poisoned. A store that returns wrong bytes is detected on
read and the read becomes a miss. **An attacker with total control of 𝔅 can deny service and
nothing else.**

### 2.2 The action cache

```text
(2.3)    𝔄 : 𝕂 ⇀ 𝔸,   𝔸 ≡ (𝑑, 𝑤, 𝑠, 𝑝)
```

A result digest, the writer identity 𝑤, an attestation 𝑠 binding (κ ‖ 𝑑) to 𝑤, and provenance 𝑝
(Appendix B.3). **No equation analogous to 2.2 exists for 𝔄**, and none can: verifying that κ maps
to 𝑑 requires performing the computation. §5.2 and §5.3 exist because of this asymmetry.

**The signature scheme is a field of 𝑤, not a constant.** Unlike ℋ (§3.1), it is baked into no
encoding: 𝑠 sits outside the hashed material, so schemes coexist at no structural cost and
verification is per-writer.

**𝑠 is a batch attestation, not a per-entry signature.** A publication round signs one Merkle root
over its entries; 𝑠 is that signature plus this entry's inclusion proof, costing ⌈log₂ n⌉ digests
rather than a whole signature. This is what makes a post-quantum scheme affordable:

| Scheme                      | Per entry |
| --------------------------- | --------- |
| ed25519, per entry          | 64 B      |
| ML-DSA-44, per entry        | 2,420 B   |
| ML-DSA-44, batched over 10⁴ | ~450 B    |

Current scheme: ed25519. The cost of PQ is size, not speed - ML-DSA verification is competitive
with ed25519. Migration touches verification and Appendix C, not the encodings.

### 2.3 Derived state

𝔐, 𝔇 and ℜ are derived. Deleting them costs latency and diagnosis, never correctness (I5). This is
a constraint on all future work: no mechanism may make a result depend on them.

### 2.4 Mutation

σ evolves by **insertion and removal only**. No entry is ever modified in place.

```text
(2.4)    σ′ = σ ⊕ insertions ∖ removals
```

Garbage collection removes entries; it never rewrites them. A consumer holding a digest therefore
either finds the same bytes it expected or finds nothing (I9). This is what makes concurrent
access safe without locking the store.

## 3. Objects

### 3.1 Digests

A digest 𝑑 ∈ 𝔻 is the output of ℋ over a byte string.

**ℋ ≡ BLAKE3-256**, fixed for the life of this specification: not negotiated, not tagged, not
configurable. Every digest in a §4.4 encoding is exactly 32 bytes, with no length prefix and no
algorithm identifier (§1.4).

SHA-256 appears only in OCI-facing structures, which carry their own encoding. Registry digests
never appear inside a key. The two namespaces are disjoint and never compared.

Changing ℋ revises this specification and invalidates σ. It is not a runtime choice: a broken ℋ
makes every key in 𝔄 untrustworthy, so the response is to discard the cache, not to re-key it.

#### 3.1.1 Quantum resistance

256 bits suffices. Grover halves preimage resistance to 2¹²⁸; quantum collision search gains
nothing practical over classical birthday search once its memory cost is counted. Collision
resistance is the property depended on (A1).

The larger exposure is classical cryptanalysis - BLAKE3 is younger than SHA-256 and runs a smaller
round margin. The remedy is the revision path above.

Signatures, not digests, are the post-quantum weakness: ed25519 (§2.2, C.1) falls to Shor. §2.2
makes the scheme a per-writer field and batches attestations, so migration is a configuration
change rather than a format change.

| Break   | Effect                                                                 |
| ------- | ---------------------------------------------------------------------- |
| ℋ       | retroactive - content addressed today becomes substitutable            |
| ed25519 | prospective - forges future entries; cannot alter one already verified |

Cache entries are short-lived and re-derivable, so migrating signatures is contained to §2.2 and
Appendix C. No decision is required now.

### 3.2 Layers

A layer ℓ ∈ 𝕃 is a content-addressed filesystem delta. Its identity is the **uncompressed** digest:

```text
(3.1)    id(ℓ) ≡ ℋ(uncompressed canonical tar of ℓ)
```

Not the compressed digest. Every lazy-pull encoding - eStargz, zstd:chunked, nydus - re-encodes a
layer and changes its compressed digest while the content is identical. Keying on the compressed
form makes re-encoding invisible to the cache and stores identical content twice. **Compression is
a transport encoding, never identity.**

A **stack** is a sequence ⟨ℓ₀ … ℓₙ⟩. Its materialisation is the left fold of layer application.
Stacks are subject to the flattening operator Φ (§4.6).

### 3.3 Metadata

A layer records, per path: mode, uid, gid, symlink target, xattrs, device numbers, hardlink
identity, and mtime **to nanosecond precision**. It does not record atime or ctime: reading a file
alters its atime, so including it would make a layer's identity depend on who last read the source
tree.

### 3.4 Steps

```text
(3.2)    s ≡ (𝑏, ω, ε, π)
```

𝑏 the base stack, ω the operation, ε the ambient state the operation may observe (§4.4), π the
platform. ω is one of:

| ω             | Meaning                                  |
| ------------- | ---------------------------------------- |
| `exec(argv)`  | run a command in the sandbox             |
| `file(ops)`   | a sequence of pure filesystem operations |
| `image(ref)`  | materialise a registry reference         |
| `local(path)` | materialise host context                 |
| `host(argv)`  | run on the host, unsandboxed - `LOCALLY` |
| `merge(⟨s⟩)`  | combine stacks                           |

`host` is distinguished throughout: it is unsandboxed, non-cacheable by default, and never
retried (I7).

ω additionally carries the **working directory** the operation runs in. It is part of the
operation rather than of the base, because it changes what the operation does - `make` in two
directories is two steps - and therefore enters Κ₁ by (4.5) like any other component of ω.

### 3.2a Declarations

A stack element is not always a filesystem delta. A **declaration** γ ∈ 𝔾 is what an image says about
how a step should run - its environment, working directory, user, entrypoint and command - and it
contributes no paths.

```text
(3.8)    id(γ) ≡ ℋ(𝒮(γ))
(3.9)    𝑏 ∈ ⟨𝕃 ∪ 𝔾⟩*
```

Materialisation is the left fold of §3.2 either way: a layer applies to the filesystem, a declaration
applies to the environment, and later wins over earlier in both. ε overlays what the declarations
leave, which is why `ENV` in a derived image overrides the base it was written on.

**This is what an image already is.** OCI records a metadata-only build step as a `history` entry with
`empty_layer: true` and folds its effect into one accumulating configuration document.
`golang:1.26.5-alpine3.24` carries ten history entries against five filesystem layers, so half of
what built it declared rather than wrote. What changes here is that the document is split per element
instead of carried whole, and the three consequences are the argument for it:

* a declaration travels by the mechanism that already moves stack elements, so a machine that can
  materialise a base has what that base declares. It is not a second thing to remember to send, and a
  worker that received the filesystem and not the declaration ran steps without the `PATH` their
  image sets;
* a declaration is in ids(𝑏) and therefore in every key derived from it (§4.4) by construction, rather
  than by an exception to what a key covers;
* two images whose filesystems are identical and whose declarations differ share the filesystem and
  stay distinct. Carried as one document beside a layer they collide, and whichever was placed second
  answers for both.

**An Earthfile's own `ENV` is a declaration, by the same rule.** There is one mechanism, not one for
what an image declares and another for what a build declares - they say the same kind of thing about
the same step and are composed by the same fold in the same order. `ENV` between two `RUN`s is an
element between two layers, and it applies to what follows it for the reason a layer does.

This is the reduction §4.4 asks for. Environment variables were the first item ε had to enumerate,
and ε is that section's stated weak point: what is ambient must be listed correctly or a key is
wrong, and nothing detects the omission. A declaration is not ambient. It is an input, named by its
content, and it reaches every key derived from the stack by (4.5) whether or not anybody remembered
it. **Every reduction in ε is worth more than every addition to it**, and this is the largest one
available.

```text
(3.10)   𝒮(γ) is the declaration as written, before expansion
```

Equation 3.10 is what lets a declaration be shared. `ENV MYPATH=hello:$PATH` names its own base if it
is expanded when it is written down, so the same line on two bases would be two elements; expanded in
the fold instead, it is one element that means what it should on both. The fold is also the only
place where the value of `$PATH` is known, since it is whatever the elements before it left.

**A secret is never a declaration.** ε keeps declared secrets by identity and never by value (§4.4),
and a declaration is stored, content-addressed and shared by construction - so a secret value placed
in one would be published to every machine that materialises the stack. The two mechanisms are
distinguished by what may be written down, and that is the whole of the distinction (I19).

### 3.3a Layer identity

A layer carries two digests over the same metadata:

```text
(3.1a)   ℓ_id  ≡ ℋ(⟨𝒮(entry) : entry ∈ sort(layer)⟩)  including mtimes
(3.1b)   ℓ_con ≡ ℋ(⟨𝒮(entry) : entry ∈ sort(layer)⟩)  excluding mtimes
```

ℓ_id is the identity: it is stored in 𝔄, transferred between workers, and reproduced by a
restore. ℓ_con answers only whether two captures hold the same bytes and structure.

Both are required because creating a directory stamps it with the wall clock. Two executions of an
identical, deterministic step therefore differ in ℓ_id. Determinism screening (§6) compares ℓ_con;
comparing ℓ_id would report every step that creates a directory as non-deterministic, and a screen
that fires on everything is switched off.

Entries are ordered by byte-wise comparison of their paths. A collation-aware ordering would make
a layer's identity depend on the capturing machine's locale.

### 3.3b The layer store is read-only to a step

A step reads layers and writes only its own upper layer. The two are on different filesystems,
and the separation is structural rather than enforced by policy:

* the layer store is shared into the sandbox and is not writable by the step;
* the upper and work directories are local to the sandbox.

This makes A3 cheaper to believe - a step cannot corrupt the cache it is reading, because it has
no writable path to it. It is also forced: a shared filesystem (virtiofs) does not support the
extended attributes overlayfs requires of an upper layer, and the kernel responds by mounting
read-only rather than by refusing, so a step's first write fails with an error naming neither
cause nor cure.

### 3.3c Cache mounts

A step may mount a directory that is not part of its base stack and is not captured into its
result. Each such mount carries a **sharing mode** μ:

```text
(3.6)    μ ∈ { locked, shared, private }
```

`locked` gives one step at a time the named directory. `shared` gives several steps the named
directory at once. `private` gives the step a directory of its own, made for it and removed with
it.

μ is a component of ω and enters Κ₁ by (4.5), because it changes what the step sees: the same
command over a directory another step is concurrently writing is not the same step as one over a
directory nobody else holds.

**A mode is provided or the step is refused (I15).** The three are not degrees of one behaviour
that an engine may round between. Serving `shared` where `locked` was asked for lets two commands
into a directory the author said held one; serving `locked` where `shared` was asked for makes a
build with an npm cache serial, which is a performance claim rather than a correctness one, and
still not what was asked. `private` names no shared directory at all, which is what makes it the
only mode whose contents are a function of the step (I3).

μ constrains the schedule and is therefore visible to it: the set of steps that may run
concurrently is the set whose `locked` mounts are disjoint. This is a constraint on order and not
on result - a build with the constraint and a build without it produce the same artefacts, and
only one of them is entitled to say so.

The constraint is enforced **before** a step is given a share of the build's parallelism, and not
when its directory is bound. A step waiting for a cache while holding a share is a share spent
waiting, and the steps that would have used it are the ones needing no cache at all. The two
acquisitions are ordered - cache, then share - which is also what makes them safe: a share is held
only by a step that already holds its caches, so no share ever waits for one.

### 3.4a Conditions

A conditional selects a branch. Where the condition is a function of ε alone - a comparison of
build arguments - it is decided when the graph is built, and only the selected branch enters it.
The graph therefore stays known before the build, which every key, schedule and diagnostic depends
on: a graph discovered while it runs has no stable identity to key on.

Conditions joined by `&&` and `||` are a function of ε when their operands are, and are decided
the same way. They are left-associative with equal precedence, as the shell has them, and they
short-circuit: an operand the engine cannot decide alone is not decided when the left side settles
the answer. `[ "$v" = "no" ] && command -v unbuffer` is therefore static, because a condition that
is never evaluated needs no decision - which is the shell's rule and not a liberty taken to widen
what counts as decidable.

Where the condition requires evaluation in a sandbox, the graph is not fully known in advance. The
engine may then **predict** the branch from that site's history and speculate on it.

A prediction is a hint and is governed by I5: the branch a build takes is whatever evaluating the
condition yields, never what was predicted. A misprediction costs the work spent on the untaken
branch and nothing else, exactly as a missing cache entry costs time and nothing else. Prediction
disabled and prediction enabled produce the same artefacts.

A site with no history is not predicted. Speculating on no evidence spends the build's parallelism
on a coin toss, and that cost falls on the builds with nothing to learn from.

### 3.4b Container daemons

An operation may require a container daemon running for its duration. The daemon is not part of the
step's filesystem and is not captured; what it holds is state that exists before the step and may
survive it.

A step so marked carries the daemon's **provenance** δ:

```text
(3.5)    δ ∈ { own, own(c), shared }
```

`own` starts a daemon whose storage is inside the step's own filesystem and is discarded with it.
`own(c)` starts one whose storage is the named area c and outlives the step. `shared` reaches a
daemon the engine did not start.

δ enters ω and therefore Κ₁ by (4.5), because it changes what the operation does.

**Cacheability follows from δ and from nothing else:**

| δ        | daemon's contents at the start | Λ may serve a result |
| -------- | ------------------------------ | -------------------- |
| `own`    | empty, by construction         | yes                  |
| `own(c)` | whatever c holds               | no                   |
| `shared` | whatever that daemon holds     | no                   |

Only `own` is cacheable, and the reason is (I3): the result of a step whose daemon already held
images is not a function of the step's inputs, and no key over those inputs describes it. `own`'s
emptiness is structural rather than declared - nothing outside the step is mounted into the
daemon's storage, so there is nothing for a previous build to have left.

An engine that cannot provide δ refuses the step (I10). It does not substitute a different δ: a
step asking for `own` and given `shared` is a step whose key claims an empty daemon and whose
execution saw a full one.

### 3.4c Descriptions the build produces

A construct may expand into steps read from a *file another target produces*. `FROM DOCKERFILE`
naming a target's output is the one Earthfiles have.

The graph is then not known until that artifact exists, as in §3.4a and for a different reason: not
a branch whose condition needs deciding, but a description that has to be built before it can be
read. The engine builds the producing target, reads the description, and expands it - so **the order
is fixed by the language rather than chosen**: nothing downstream of the expansion can be planned
first.

**No term is added to Κ.** The description's content becomes the nodes it describes, so every
derived node's key covers it by (4.5) already: a different description is a different graph, not the
same graph with a different provenance. This is stronger than keying on the producing target's
identity would be, and it holds without §4.4 changing.

An engine planning without the means to build - resolving a graph and running nothing - refuses the
construct as a capability the caller withheld, not as one the engine lacks (I10). The distinction is
the caller's to act on: the first is answered by supplying it, the second by nobody.

A produced description is data. It is parsed, and its steps run where any step runs (I16); nothing
about having been generated by this build lets it reach the host.

### 3.4d Mutable references

A reference that is not a digest - `alpine:latest`, `alpine:3.22`, a floating branch - names
different content at different moments. Resolving one is an **observation of the outside world**,
and it is the only observation a build makes that no key can be closed over: ε enumerates what a
step may observe (§4.4), and a registry's answer at an instant is not a property of the machine.

Θ resolves a reference to a digest:

```text
(3.7)    Θ : ref ⇀ 𝔻,   fixed per invocation of Υ
```

**Once per reference per build.** Θ is fixed at each reference's first use and every later use of
that reference in the same build yields the same digest. Not "resolved at t₀": §3.4a and §3.4c both
describe graphs that are not fully known until something has run, so a reference may be discovered
late. It is resolved when discovered, once, and never again.

**Only the digest reaches a key.** A reference never appears in Κ₁ or Κ₂. It reaches them as the
identity of the layer it resolved to, through `ids(𝑏)` in (4.5), so a tag that moves is a different
base and therefore a different key. A key derived from the reference instead would be stable while
the thing it names changed, which is a false hit - the one failure that must never occur (I3).

**Θ is recorded.** Its graph for a build is provenance (B.3): the references used and what they
resolved to. A build that cannot say which image it used cannot be compared with the one before it,
and comparing them is how a moved tag is told from a changed Earthfile (B.4).

A retry re-resolves. A re-run is a fresh invocation of Υ, and carrying the previous attempt's
resolution forward would make provenance report an image the retry did not use. The cost is that a
re-run may not reproduce the failure it was meant to reproduce; the record says why, which is the
part that matters.

### 3.5 Results

```text
(3.3)    ρ ≡ (ℓ, 𝑒, 𝑟)
```

the output layer, the exit code 𝑒 ∈ ℕ, and the observation set 𝑟.

### 3.6 Observation sets

```text
(3.4)    𝑟 ≡ (𝑅, 𝑁, 𝐷)
```

* **𝑅** - paths read, with the content digest of each.
* **𝑁** - negative lookups: failed opens, stats of absent paths.
* **𝐷** - directories listed, with the digest of each listing.

𝑁 and 𝐷 are not refinements. A step evaluating `if [ -f /x ]` reads nothing; a specification
recording only 𝑅 admits a false hit against a base where `/x` exists. **𝐷 subsumes 𝑁 within a
listed directory**: if the listing digest is unchanged, every absent path in it is still absent.
This is what keeps 𝑁 small when a compiler probes twenty include directories.

𝑅, 𝑁 and 𝐷 are **sets**. A source that records one path twice - a compiler stating the same absent
header once per `-I` directory, a shell walking `PATH` - observed it once, and Κ₂ (4.6) derives from
the set. `sort` in (4.6) fixes order; multiplicity carries no meaning to fix, so a derivation
normalises rather than assuming its caller did. Two implementations disagreeing here derive different
keys for one observation and share no cache entry, which at the fleet (Appendix C) is indistinguishable
from a cold cache.

**An observation set is either closed or absent.** 𝑟 may be used to derive Κ₂ (4.6) only if it
records everything the step observed. A partial set asserts that the step reads exactly what was
recorded, about a step that read more; the first base differing in an unrecorded path is then a
false hit, which is I3 violated by omission.

An observation source that cannot see some access path therefore reports 𝑟 as incomplete, and an
incomplete 𝑟 yields no Κ₂ entry. This costs a cache hit. Recording it as complete costs
correctness, and the two are not traded against each other.

## 4. Transitions

### 4.1 The build

```text
(4.1)    (σ′, 𝐀) ≡ Υ(σ, ℰ, 𝑐, 𝑡)
```

Prior state, an Earthfile, a local context, a target; yielding posterior state and artefacts. Υ is
the composition of Σ over the step graph in any order consistent with §4.7.

### 4.2 The step

Σ is the primitive. Scheduling, distribution and prefetch change *when and where* Σ runs, never
*what it returns*.

```text
(4.2)    (σ′, ρ) ≡ Σ(σ, s)
```

Normative algorithm:

```text
(4.3)  Σ(σ, s):
         κ₁ ← Κ₁(s)                                  chain key, computable in advance
         if Λ(σ, κ₁) = ρ  then return (σ, ρ)          L1: verified hit
         if 𝑟̂ ← predicted observation set for s        L2, only when a prediction exists
            and Λ(σ, Κ₂(s, 𝑟̂)) = ρ
            and 𝑟̂ is consistent with the current base  see 4.5
         then return (σ, ρ)
         materialise base 𝑏, prefetching under Μ(s)    hints only; failure is not an error
         (ℓ, 𝑒, 𝑟) ← Ω(ω, ε, π, base)                    execute under observation
         σ′ ← publish(σ, κ₁, Κ₂(s, 𝑟), ℓ, 𝑟)
         return (σ′, (ℓ, 𝑒, 𝑟))
```

Ω is execution under observation: it runs ω with ambient state ε on platform π over a
materialised base, returning the output layer, the exit code and the observation set (3.4). It is
the only function in this document with side effects.

Μ(s) consults the masks 𝔐 for s and returns a prefetch set (Appendix A). It is advisory: Μ may
return the empty set, a stale set, or a wrong set, and only latency changes (I5).

Two properties of 4.3 are normative. **The L2 consultation is optional**: an implementation that
performs only L1 is conforming, slower, and never wrong. And **prefetching cannot fail the step** -
a mask that is absent, stale or wrong changes only how long materialisation takes (I5).

### 4.3 Lookup

```text
(4.4)  Λ(σ, κ):
         𝑎 ← 𝔄[κ];              if 𝑎 = ⊥ → miss
         if writer 𝑤(𝑎) not authorised in this domain → miss
         if signature 𝑠(𝑎) invalid → miss
         𝑏 ← 𝔅[𝑑(𝑎)];           if 𝑏 = ⊥ → miss
         if ℋ(𝑏) ≠ 𝑑(𝑎)  → miss
         return the result
```

**Λ has exactly two outcomes: a verified result, or a miss.** There is no third. A corrupt entry,
an unknown writer, an invalid signature, a missing blob, a malformed record - every one returns
miss, meaning "do the work" (I4). Λ never returns an error and never returns an unverified result.
This single property converts every failure of the caching system, malicious or accidental, into a
performance cost.

### 4.4 Key derivation

```text
(4.5)    Κ₁(s)     ≡ ℋ("c" ‖ ids(𝑏) ‖ 𝒮(ω) ‖ 𝒮(ε) ‖ 𝒮(π))
(4.6)    Κ₂(s, 𝑟)  ≡ ℋ("o" ‖ sort(𝑅) ‖ sort(𝑁) ‖ sort(𝐷) ‖ 𝒮(ω) ‖ 𝒮(ε) ‖ 𝒮(π))
```

The domain-separating tag is a single fixed byte - `0x01` for Κ₁, `0x02` for Κ₂ - and prevents a
chain key from ever colliding with an observed-input key. Sorting is required: unordered input
makes the key depend on traversal order, which is not reproducible. Sorting is over the encoded
bytes, so fixed-width elements sort without decoding.

Per §1.4, prefixes appear only where a length varies:

| Component      | Encoding                                                | Prefixed   |
| -------------- | ------------------------------------------------------- | ---------- |
| domain tag     | one byte                                                | no         |
| `ids(𝑏)`       | `u32` count, then 32 bytes per layer id                 | count only |
| `sort(𝑅)`      | `u32` count, then per entry: digest (32) ‖ `u16` ‖ path | per path   |
| `sort(𝑁)`      | `u32` count, then per entry: `u16` ‖ path               | per path   |
| `sort(𝐷)`      | `u32` count, then per entry: digest (32) ‖ `u16` ‖ path | per path   |
| `𝒮(ω)`, `𝒮(ε)` | `u32` length, then the serialisation                    | yes        |
| `𝒮(π)`         | four bytes: os, arch, variant, reserved                 | no         |

Path lengths are `u16`, which bounds a path at 65,535 bytes - an order of magnitude above any
filesystem's limit. **A path exceeding it is rejected, not truncated.** Truncation would map two
distinct paths to one encoding, which is exactly the collision §1.4 exists to prevent.

Κ₁ is computable before execution. Κ₂ is computable only after, which is why L2 in 4.3 requires
a *predicted* 𝑟̂ and a consistency check (§4.5).

**ε is the weakest point in this specification and is treated as such.** It must enumerate
everything ambient a step may observe: argv, uid and gid, umask, locale, timezone, hostname, CPU
feature flags exposed to the sandbox, and the set of declared secrets (by identity, never by value).
Environment variables were the first item on that list and are no longer on it: they are declarations
(§3.2a), which are inputs rather than ambient state and reach a key through ids(𝑏). Anything observable but omitted is a false hit, undetectable
by any signature because nothing was forged. **The mitigation is to shrink what is observable** -
to make steps hermetic - rather than to enumerate ever harder. Every reduction in ε is worth more
than every addition to it.

### 4.5 Prediction consistency

A predicted observation set 𝑟̂ may be used for an L2 lookup only if it is *consistent* with the
current base: every path in 𝑅̂ resolves to the digest recorded, every path in 𝑁̂ is still absent,
and every listing in 𝐷̂ still hashes as recorded. Verification touches only the paths named, so its
cost is proportional to the prediction, not to the tree.

```text
(4.7)    consistent(𝑟̂, 𝑏) ⟹ Κ₂(s, 𝑟̂) is the key this step would produce
```

If 4.7 does not hold, the observation set was incomplete and I3 is violated. This is the precise
statement that E5b tests.

### 4.6 Capture and flattening

Δ captures a step's output as a layer. It is defined over the *changed set*: the paths the
executor reports as written, removed or altered. Where the executor cannot report a changed set, Δ
falls back to comparing trees - correct, and measured at 14x the cost (E4).

Timestamps follow I8: nanoseconds preserved. Where a layer is destined for publication rather than
cache, a clamping operator applies `SOURCE_DATE_EPOCH`.

```text
(4.8)    Φ(⟨ℓ₀ … ℓₙ⟩) ≡ ⟨ℓ₀ … ℓₖ, flatten(ℓₖ₊₁ … ℓₙ)⟩   when 𝑛 > 𝑛ₘₐₓ
```

Stacks have a hard upper bound. overlayfs refuses more than 500 lower layers, and a 501-step target
fails with `invalid argument` and no explanation (E11). It is not the only bound and not usually the
binding one: `mount(2)` reads its options from a single page, so 𝑛ₘₐₓ also depends on the *length*
of the layer paths, which stops this engine's guest an order of magnitude sooner (E49). **𝑛ₘₐₓ is
the smallest bound the materialiser is subject to, and the materialiser is what knows it.** A
scheduler that assumes either limit is the only one flattens too late.

Φ commits and squashes when the stack approaches 𝑛ₘₐₓ. Φ is *observable*: it trades per-step cache
granularity across the squashed range for the ability to build at all, so the choice of 𝑛ₘₐₓ and of
which range to squash is a policy that must be recorded in the build record, not an implementation
detail.

**flatten(ℓₖ₊₁ … ℓₙ) is a layer, not a name.** It denotes the range merged - oldest first, so a
later layer's version of a path wins, which is what the mount it replaces would have produced - and
that layer exists in 𝔅 before any step stands on it. An identity handed to an executor that has
never been built is not a flattened stack; it is a build whose base has been silently discarded
(E50). Because flatten is derived from the range, two builds collapsing the same layers name and
share one result.

### 4.7 Scheduling

A schedule is an assignment of steps to workers and to an order. Σ's result does not depend on it
(4.2), so a scheduler may choose freely within the constraints below and never otherwise.

#### 4.7.1 Schedules, and which are legal

A **schedule** 𝑔 assigns each step of a build to a worker and to a position in that worker's
order:

```text
(4.9)    𝑔 : 𝕊 ⇀ (worker, ℕ)
```

𝑔 is partial because the graph is discovered progressively: a schedule covers the steps known so
far and is extended as more are revealed.

`legal(𝑔)` holds exactly when all five constraints hold:

| Constraint        | legal(𝑔) requires                                                 |
| ----------------- | ----------------------------------------------------------------- |
| dependency order  | for every step, its base is materialised before its position      |
| platform affinity | every step is assigned to a worker whose platform satisfies π     |
| barriers          | no step ordered after a `WAIT` precedes the block being satisfied |
| host locality     | every `host` step is assigned to the invoking machine             |
| stack depth       | no materialised stack exceeds 𝑛ₘₐₓ; Φ (4.8) is applied first      |

An implementation MUST produce only legal schedules. The property that matters is then:

```text
(4.10)   ∀ 𝑔₁, 𝑔₂ : legal(𝑔₁) ∧ legal(𝑔₂) ⟹ 𝐀(𝑔₁) = 𝐀(𝑔₂)
```

Any two legal schedules yield the same artefacts. This is what makes distribution sound; it
follows from I1, and E7 tests it.

#### 4.7.2 Free choices

Concurrency, placement within the eligible set, work stealing, batching, prefetch timing,
speculation and re-ordering of independent steps are unconstrained.

#### 4.7.3 Stability

Given the same graph, the same worker inventory and the same cost estimates, an implementation
MUST produce the same schedule. Determinism here is not tidiness: stable placement means a worker
already holds the data, so stability is a caching property.

This forbids the ordinary sources of schedule noise - iteration over an unordered map, ties broken
by goroutine arrival, identity taken from a pointer. Ties are broken by step digest, which is
content-derived and therefore stable across runs and across machines.

#### 4.7.4 Speculation

A scheduler MAY evaluate a step before knowing whether the build requires it. Speculation is
sound because Σ is pure (I1): a step that turns out to be unnecessary has still produced a valid,
content-addressed result.

Two rules are normative:

* **A speculative step MUST NOT be `host`, and MUST NOT push.** Both have effects outside the
  sandbox, and both are excluded from automatic retry for the same reason (I7).
* **Speculation MUST NOT change what a build produces.** A speculatively computed result is
  admissible only through the ordinary lookup path Λ, which verifies it (I4) exactly as it would
  any other entry.

Mispredicted speculative work is deposited in σ, not discarded: should that branch ever be taken,
the result is already present.

## 5. Invariants

Normative. An implementation that violates any of these is defective, not merely suboptimal.

* **I1 (Purity).** Σ is a function of (σ, s) up to declared nondeterminism. Two evaluations with
  equal keys yield equal results, or the step's class is classified non-deterministic (§6).
* **I2 (Blob integrity).** Every blob is verified against its digest before use, including partial,
  resumed and peer-sourced transfers.
* **I3 (Key completeness).** If anything the step could observe differs, the key differs. Violation
  is a false cache hit: the one failure that must never occur.
* **I4 (Two outcomes).** Λ yields a verified result or a miss. Never an error; never an unverified
  result.
* **I5 (Hint safety).** 𝔐, 𝔇, prefetch and placement never affect a result. They may be absent,
  stale, or wrong in either direction.
* **I6 (Transient tolerance).** Infrastructure failure affects duration, never outcome.
* **I7 (Retry safety).** Only effect-free operations are retried automatically:

| Operation                                      | Automatic retry                                             |
| ---------------------------------------------- | ----------------------------------------------------------- |
| blob fetch, registry pull, pinned clone        | yes - content-verified, so a retry cannot yield wrong bytes |
| a pure step                                    | yes, wholesale                                              |
| layer push                                     | yes - content-addressed, so re-push is a no-op              |
| manifest or tag update                         | yes, last-writer-wins between concurrent builds             |
| **`host` steps, and pushes with side effects** | **never** - attempted exactly once                          |

  Retries are bounded by a budget expressed as a fraction of operations, so a systemically broken
  dependency fails fast rather than consuming the whole build.

* **I8 (Timestamp policy).** Nanoseconds preserved in cache layers and fleet transfers; clamped to
  `SOURCE_DATE_EPOCH` in published images. Never the reverse.
* **I9 (Monotonicity).** State entries are inserted or removed, never modified. A held digest
  yields the expected bytes or nothing.
* **I10 (Honest refusal).** An engine that cannot evaluate a construct refuses it, naming the
  construct and the alternative. It never approximates.

  A refusal states **where** - a source location, a quoted name, or a target - and **what to do**.
  This is a property of the message rather than a set of approved wordings, and is checked as one
  against a corpus of Earthfiles written without knowledge of this engine.

  What to do is one of three, and which one is itself part of the claim:

  1. a **gap**: the construct arrives later, and meanwhile another engine builds it;
  2. a construct **the language does not have**: there is no engine to switch to, and the way out
     is a different construct;
  3. a **decision**: the engine will not do it, and nothing is coming.

  The way out must be one that works. A refusal offering an engine that refuses the same construct
  is not a remedy but a second failure, and it is believed on the way because the engine said it;
  a refusal reading as unfinished when it is a position invites somebody to finish it, which for a
  safety property means removing one. Naming the kind is therefore not presentation - it is the
  difference between a reader who tries the other engine, one who rewrites the line, and one who
  stops.

  A construct accepted with a flag silently ignored is an approximation, not a refusal:
  `BUILD --platform=… +x` evaluated without the platform builds the wrong architecture and reports
  success.
* **I12 (Reporting is deterministic).** Two executions of one build produce identical records and
  attribute a failure identically, whatever order steps completed in.

  (4.10) requires legal schedules to agree on artefacts. That is not sufficient: a build whose
  *record* varies makes every tool that diffs two builds report noise, and a build that blames a
  different command each time cannot be acted on. Records are therefore ordered by position in the
  deterministic traversal, not by completion, and where several steps fail the one earliest in that
  traversal is reported.

  Concurrency is a legal schedule, so this is the same requirement extended from what a build
  *produces* to what it *says*.
* **I16 (A fetched description does not run on the host).** An operation whose ω is `host` is
  refused where its description came from a fetched repository (§5.3). The engine does not run a
  command on the invoking machine, outside the sandbox, on the say-so of a description it did not
  fetch from the builder's own filesystem.
* **I15 (A sharing mode is provided or the step is refused).** Each cache mount's μ (§3.3c) is
  honoured as written: `locked` admits one step at a time, `shared` admits several, `private`
  names no shared directory. An engine that cannot provide a mode refuses the step rather than
  substituting another, and μ is a component of ω, so two steps differing only in it are
  different steps.
* **I14 (A daemon's provenance is in the key or the step is not cached).** A step requiring a
  container daemon (§3.4b) is served from cache only where δ = `own`, whose daemon is empty by
  construction. Where the daemon may hold what another build put there, no key over the step's
  inputs describes its result, and Λ neither reads nor writes an entry for it. δ is a component of
  ω, so two steps differing only in it are different steps.
* **I13 (A part is as authenticated as the whole).** A fragment of a layer is accepted only when
  every entry it carries seals against that layer's manifest (C.4.1). Content alone is not enough: a
  peer that sends the right bytes with the wrong mode has sent a file the layer does not describe.
  The two fields outside the seal are outside it because the receiver cannot reproduce them, and both
  are named in C.4.1 rather than left to be discovered.

* **I11 (Refuse or degrade, never silently).** An unavailable facility is refused when it bears on
  correctness and degraded otherwise, and a degradation is always reported with its cause.

  Confinement bears on correctness: A3 states that a step's writes are confined to its own upper
  layer, so an escaped step makes ε an unsound bound on what it observed and every key derived
  from it a false claim. A step that cannot be confined is therefore not run.

  Resource bounds do not: a step with no memory ceiling computes the same result, more
  dangerously. It runs, bounded by whatever the host allows.

  The two are separated because conflating them fails in both directions - refusing to build where
  cgroups are unavailable, or caching the output of a step that escaped. Silence is excluded from
  both branches: an unenforced limit that reports nothing is indistinguishable from an enforced
  one, which is how a ceiling written to `memory.max` and evaded through swap survived its own
  test.

  The same rule governs results. A step whose output layer was not captured yields no entry in 𝔄:
  the absent digest is well-formed, so publishing it would assert that the step produces the empty
  layer and every later build sharing its key would hit that assertion. An executor that cannot
  capture degrades to an uncacheable result and records that it did.

* **I17 (Reference stability).** Within one build a mutable reference resolves exactly once (§3.4d).
* **I18 (A declaration is not an absence).** A stack element that contributes no paths is materialised
  as contributing none; an element the store does not hold is refused. A materialiser that answers
  both with an empty directory cannot tell an image that declares from a base that never arrived
  (§3.2a).
* **I19 (A secret is never written down).** A declared secret enters ε by identity and never by
  value, and never becomes a declaration: declarations are stored, content-addressed and shared, so a
  secret in one is a secret published to every machine that materialises the stack (§3.2a).
  Every key that depends on it, and every worker that acts on it, sees the same digest. A reference
  is never itself a term of a key.

### 5.1 How each invariant is enforced

An invariant with no experiment is an aspiration. An invariant with no *assertion* is worse: an
experiment checks chosen inputs at CI time, an assertion checks every real execution on real
inputs.

Enforcement is preferred in this order, and **earlier is strictly better** - the numbering runs
from the strongest form to the weakest, so a lower number is a stronger guarantee:

| Level | Mechanism              | Why better                                                |
| ----- | ---------------------- | --------------------------------------------------------- |
| 1     | **unrepresentable**    | the type admits no violation, so nothing can be forgotten |
| 2     | **always-on check**    | part of the mechanism, not an optional extra              |
| 3     | **debug assertion**    | catches it in development on real inputs                  |
| 4     | **sampled at runtime** | probabilistic, for checks too dear to run always          |
| 5     | **experiment only**    | chosen inputs, CI time - the weakest form                 |

| Invariant | Enforced by                                                                                                                                          | Level | Tested by                                                     |
| --------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | ----- | ------------------------------------------------------------- |
| I1        | sampled determinism screening (§6)                                                                                                                   | 4     | E14; E7 fleet equivalence                                     |
| I2        | digest verified on every read - it *is* the mechanism                                                                                                | 2     | E5c, E15                                                      |
| I3        | observation set closed at key time; every field of ω reaches Κ₁                                                                                      | 3     | **E5b**; core key-coverage tests                              |
| I4        | Λ's return type has no error variant (4.4)                                                                                                           | **1** | E5c                                                           |
| I5        | results identical with all hints disabled                                                                                                            | 3     | E12                                                           |
| I6        | -                                                                                                                                                    | 5     | E15                                                           |
| I7        | attempt counter; `host` absent from the wire vocabulary (C.3)                                                                                        | **1** | E15                                                           |
| I8        | assert nanoseconds survive layer write unless clamping                                                                                               | 3     | E3, containerd fork tests                                     |
| I9        | insert-only stores: an existing entry is never rewritten                                                                                             | **2** | E76; crash-safety, c4 in this engine's terms                  |
| I10       | capability list consulted before evaluation; three kinds of refusal, each with a way out that works                                                  | 2     | core capability tests; interp refusal tests; E152, E153, E157 |
| I11       | isolation returns an error; limits return a stated reason                                                                                            | 2     | guest isolation and cgroup tests                              |
| I12       | records sorted by traversal position; earliest failure wins                                                                                          | 2     | core concurrency tests                                        |
| I13       | every entry of a fragment sealed against its manifest (C.4.1)                                                                                        | 2     | E324; layer fragment-seal tests                               |
| I14       | δ hashed into Κ₁ at both mirrors; scheduler refuses the cache for δ ≠ `own`                                                                          | 2     | E381, E384; key-coverage guards; interp isolation tests       |
| I15       | μ hashed into Κ₁ at both mirrors; the guest queues only `locked` mounts                                                                              | 2     | E427, E432; mount-coverage guards; sharing-mode tests         |
| I16       | LOCALLY refused in a fetched Earthfile, its functions and its checkout                                                                               | 2     | E439; interp remote-trust tests                               |
| I17       | Θ memoised on (reference, platform); the digest reaches Op.Args before the key. An unpinned build says so                                            | 2     | E508; interp pinning tests                                    |
| I18       | the materialiser distinguishes a declaration from a layer the store does not hold, rather than creating an empty directory for whatever is absent    | 2     | **[GAP]**                                                     |
| I19       | a secret reaches a step through the secret mechanism and has no path into a declaration; the type that carries a declaration carries no secret value | 1     | **[GAP]**                                                     |

An invariant with two mechanisms takes the **weaker** level, not the better one: I3 needs both the
observation set to be closed and every field of ω to reach the key, so it is enforced only as well as
whichever of those is weakest. Recording the stronger would describe a guarantee the invariant does
not have.

Two invariants are already at level 1 and should stay there: Λ cannot return an error, and a step
assignment cannot express a `host` op. Both were reached by choosing a type rather than adding a
check. I6 is the weakest, at level 5, because tolerating transient failure cannot be observed from
inside a single execution.

### 5.2 What "unpoisonable" means, precisely

The claim is bounded, and the bound should be stated rather than implied:

> A poisoned cache may make a build slower. It may never make it wrong.

This follows from 2.2, 4.4 and I4 **for 𝔅 unconditionally**, and for 𝔄 **conditionally on A5** -
that the writer is authorised within the trust domain. It does not follow for an unsound key: I3
violated produces wrong builds from an honest cache, and no signature detects it because nothing
was forged.

### 5.3 Trust domains

A domain is the set of writers whose entries are honoured. Untrusted builds - a pull request from
a fork - read the shared cache and write only to an isolated namespace. Write-scoping is load
bearing: signing does not help when the attacker is a legitimate writer.

**A fetched description is in the writers' domain, not the builder's.** An operation whose ω is
`host` runs outside the sandbox, as the person who started the build (§3.4). Where the description
of that operation came from a repository rather than from the machine building it, the command was
chosen by whoever may write to that repository - so it is refused (I16), and the refusal names the
repository so that the reader can see whose command it was.

The boundary is *provenance and not path*: it follows the file the operation is written in, through
functions it invokes and through other files of the same checkout, and it does not attach to a local
file merely for referring to a remote one. A rule that spread the other way would refuse ordinary
builds and be disabled within a week, which is a security property in name only.

## 6. Classification

A step class is *deterministic* if repeated evaluation yields identical results across the
perturbation matrix: clock, hostname, pid, build path, `TMPDIR`, CPU count, locale, `TZ`, umask,
uid.

Determinism is never proven, only bounded. N clean observations put the 95% upper bound on the
failure rate near 3/N, so 𝔇 holds bounds, not verdicts, and the observation rate has a floor above
zero. Beliefs generalise by class - a command shape over a base - because a verdict about one exact
key rarely recurs.

Only deterministic classes are eligible for quorum verification and cross-worker result sharing.

---

## Appendix A. Masks

### A.1 Structure

A mask is a bitmap over a layer's index: bit 𝑖 set means chunk or file 𝑖 was accessed. Masks live
with the layer, which is content-addressed, so a mask learned by one project applies to every
project using the same base.

### A.2 Key hierarchy

| Level | Key                           | Available                 |
| ----- | ----------------------------- | ------------------------- |
| L0    | exact step key                | this step ran before      |
| L1    | command class ‖ base layer id | anything similar ran      |
| L2    | base layer id                 | the image was used before |
| L3    | structural, directory-level   | always                    |

L1-L3 are computable from the Earthfile alone, before inputs are resolved - which is what lets a
cold worker begin fetching while the graph is still being built.

### A.3 Maintenance

Masks are unioned on consultation and **extended on miss**: an unpredicted access demand-faults
normally and is added. Extension alone is a ratchet that converges on the whole layer, so each
entry carries a use count and is dropped after 𝑁 unused consultations.

Quality is measured as precision - fraction prefetched that was used - and recall - fraction used
that was prefetched. Recall buys latency; precision prevents degeneration into eager transfer.

### A.4 Safety

Per I5, a mask never affects a result. It is a superset hint: wrong inclusively costs bandwidth,
wrong exclusively costs a demand fault. **Correctness never depends on a mask being right.**

## Appendix B. Build records

### B.1 Canonical serialisation

𝒮 is deterministic: maps serialised in ascending key order, no floating point, integers
fixed-width big-endian, strings length-prefixed UTF-8. Two implementations serialising equal values
produce equal bytes. Without this, keys differ across implementations and the entire cache is
per-implementation.

### B.2 Record

Per build: an identifier, and per step - the step's identity (𝑏, ω, ε, π digests), κ₁, κ₂ where
computed, the result digest, exit code, the observation set digest, whether Φ was applied and over
what range, and the outcome (L1 hit, L2 hit, miss, refused).

Plus the **cost measurements**, which are what makes ℜ a cost oracle rather than an audit log:

| Measurement              | Used for                                                                             |
| ------------------------ | ------------------------------------------------------------------------------------ |
| duration                 | critical-path estimation; how long this step takes                                   |
| output layer size        | transfer-cost estimation; how much moves if it is scheduled elsewhere                |
| input closure size       | placement; how much must arrive before it can start                                  |
| queue depth at execution | normalising duration, so a measurement taken under load does not poison the estimate |

A scheduler that estimates time but not bytes will place work badly on a fleet, where transfer is
on the critical path. Both are required.

Records hold digests and structure, never content. They are retained for the last 𝑁 builds.

### B.3 Provenance

The 𝑝 component of an action-cache entry: writer identity, timestamp, engine version, executor
backend, and the domain. Provenance is evidence, never an input to Λ beyond the writer check.

### B.4 First divergence

```text
(B.1)  divergence(ℜ_A, ℜ_B):
         for s in topological order of the shared graph:
           if step absent from one record → report graph-shape divergence, halt
           if result digests equal → continue
           classify:
             inputs differ        → name the differing paths (B.5)
             ω differs            → report the command change
             ε differs            → name the differing ambient value
             nothing in key differs → report NON-DETERMINISM
           halt
```

The final classification is the valuable one: *nothing in the key changed and the output did* is
the strongest diagnostic a build tool can emit, and no chain-keyed system can produce it, because
it does not know what the step depended on.

### B.5 Naming the differing files

Records store a Merkle tree over the sorted input list, so diffing descends only where subtree
hashes differ - cost proportional to the number of differences, not the number of inputs. Paths are
recovered by resolving the observation bitmap against the layer index, which is already stored.

Reports show ranked examples, never the full list: paths in the step's own 𝑅 above paths inherited
from the base; paths the Earthfile names above those it does not; and suspicious paths - `.git`
entries, editor swap files, timestamp files - promoted, because those are usually the actual bug.
Metadata-only differences are called out as such.

## Appendix C. Fleet wire protocol

### C.1 Identity and rendezvous

Each participant has an ed25519 identity. The driver's key 𝑘 is derived from the session:

```text
(C.1)    𝑘 ≡ HKDF(`session` ‖ `run_id` ‖ `attempt` ‖ `repo` ‖ `secret`)
```

The `secret` term is normative. Deriving from public metadata alone - a run identifier visible on
a public repository - permits any observer to derive the driver key, join the mesh and serve
results. The driver additionally publishes an allowlist of worker identities and refuses others.

### C.2 Protocols

| ALPN           | Purpose                                        |
| -------------- | ---------------------------------------------- |
| `earth/ctl/1`  | claim, heartbeat, result, cancel               |
| `earth/blob/1` | content-addressed transfer, verified per chunk |
| `earth/mask/1` | mask and profile exchange                      |

### C.3 Assignments

A worker is sent a **step assignment**, never a graph:

```text
(C.2)    assignment ≡ (𝑏, ω, ε, π, deadline, hints)
```

which is s (3.2) plus scheduling advice. 𝑏 is a sequence of layer ids, not the subgraph that
produced them: the base is content-addressed and materialisable from 𝔅, so **content addressing
collapses the graph into digests at the boundary**. A worker never learns how its inputs were
derived and never needs to.

This is also how I17 is kept without the fleet needing a rule of its own: an assignment carries
digests, so a worker has no reference to resolve and cannot disagree with the driver about what one
means (§3.4d). Resolution happens in one place and travels as data.

An assignment with **no** ω is a **prime**: the same base and hints, nothing to run. A worker
provisions and answers; the build does not wait for it. It carries no authority a step does not and
can change no result (I5) - what it changes is *when* a transfer happens, and a worker too old to
know it refuses as it refuses any operation it does not implement (I10), which costs the build a
fetch it would have made anyway.

ω may be `build(target, args)`, delegating a whole target to the worker, which then schedules the
target's steps itself and resolves that region's unknowns. Delegation transfers the *authority to
evaluate*; unevaluated graph structure still never crosses the wire. A scheduler is therefore a
tree, not a single point, and the depth is bounded only by the target graph.

**A delegate is an engine.** Every invariant in §5 binds it as it binds the parent: its schedules
must be legal (4.7.1), its lookups have two outcomes (I4), its `host` steps are refused rather
than executed - a delegate is not the invoking machine, so it cannot satisfy host locality and
must return the sub-build unevaluated at that point. Delegation adds no exemptions.

The assignment format is a **distinct, poorer type than the IR**, deliberately:

* It is flat. No unevaluated references, no laziness, no recursion.
* It is versioned and canonically serialised (B.1); the IR is neither.
* `hints` are advisory and may be dropped by any participant without affecting the result (I5). The
  vocabulary is closed, and each field says what a worker may do differently rather than what it
  must:

| Hint               | Says                                                                    |
| ------------------ | ----------------------------------------------------------------------- |
| `images`           | base images worth fetching before the step needs them                   |
| `readsPredicted`   | paths the step is expected to read, so a base may cross in part (C.4.1) |
| `estimatedSeconds` | how long this step took last time                                       |
| `holders`          | peers said to hold this step's inputs, nearest first                    |
| `bytes`            | how large those inputs are, when the sender knows                       |

  A worker that ignores every one of them fetches whole layers from the first source it can reach
  and produces the same result more slowly, which is what makes an unverified address safe to pass
  on (A5).

* **`host` is not in the wire vocabulary.** A `host` op cannot be expressed in an assignment, so a
  malicious peer cannot request one. This is a property of the type, not a check that could be
  forgotten.

The reply carries the result digest, exit code, observation set and measured duration.

### C.3.1 Replies

A worker answers with a **reply**, whose vocabulary is closed for the same reason the assignment's
is: a second implementation has to know what it may act on.

| Field                         | Says                                                                              |
| ----------------------------- | --------------------------------------------------------------------------------- |
| `version`                     | which version of this protocol the worker speaks                                  |
| `layer`, `content`, `bytes`   | what the step produced (§3.3)                                                     |
| `exit`                        | the step's own exit status, which is a **result** and not a failure of the worker |
| `observation`                 | ω as the worker saw it (§3.4), from which Κ₂ is derived                           |
| `refused`                     | the worker declined, and why (I10, I11)                                           |
| `heldAt`                      | where the produced layer can now be fetched                                       |
| `platform`, `capacity`        | what this machine is and how many steps it runs at once                           |
| `durationMillis`              | how long the step itself took                                                     |
| `queueMillis`                 | how long the step waited for a slot on this worker                                |
| `fetchedBytes`, `fetchMillis` | what the worker had to move to be able to run it                                  |

The last six are the only measurements a driver has of a machine it does not own, and placement is
computed from them. They are also what makes the account decomposable: a driver knows the round trip
and subtracts what the worker reports, so **anything a worker does not report becomes network time**.
A queue is not waste - a worker with more steps than slots is a worker being used - and an account
that cannot separate the two cannot say whether adding machines would help.

**None of them can change a result**: a worker that reports nothing is placed
badly and produces the same layers (I5).

A **refusal is not a failure.** A worker that cannot take a step - the wrong platform, an opcode it
does not implement, inputs it could not obtain - says so, and the driver runs the step somewhere that
can, or here (I11). A non-zero `exit` is the opposite: the step ran and said no, and the build fails
with its output rather than trying elsewhere.

### C.4 Transfer

Blobs are requested in batches. One stream per blob does not survive a thousand-blob
synchronisation. Every chunk is verified on receipt (I2); a peer serving wrong bytes is detected
within one chunk, not at the end of a transfer.

Fetch order: peers holding the blob, then other peers, then the registry. Multi-source fallback is
what makes registry availability non-load-bearing (I6).

### C.4.1 Partial transfer

A worker need not hold a whole layer to run a step over it. Given a set of paths 𝑤, a holder answers
with a **fragment** - those paths of the layer, packed - and a **manifest**, which is the layer's own
per-entry encoding (§3.3) over every path it contains.

```text
         seal(𝑒) ≡ ℋ(𝑒 with uid, gid and hardlink zeroed)
```

Unnumbered, for the reason C.5.1 gives: every number left in this appendix names a section as well,
and one that names two things is worse than none.

A fragment is accepted when every entry it carries seals equal to the manifest's entry for the same
path (I13). Two fields are outside the seal, and each because the receiver cannot reproduce
it rather than because it does not matter: **ownership**, which restoring requires privilege a worker
does not have, and **hardlinks**, whose partner may lie outside the fragment. Ownership is therefore
taken from the sender's declaration wherever a layer's identity is recomputed after an unprivileged
unpack (§3.3, §5.3).

The manifest crosses **compressed**, and is the only part of a fragment that does: it is a few
thousand entries differing in little, while a fragment's payload is file contents that are already
whatever they are. This changes no digest - it is a property of the message, not of the layer - and
it does not remove the O(n): a proof is linear in the layer because a layer's identity is a flat hash
of every entry, and only a Merkle identity admits a subset proof (§3.3).

The manifest crosses once per layer. A caller holding one says so, and the holder omits it: for the
case partial transfer exists for - a small read set from a large base - the proof is otherwise the
dominant cost.

A path a step reads that 𝑤 did not name is **faulted in**: the step's executor names the missing path,
the worker fetches that path as a further fragment, and the step is resumed. A prediction that proves
wrong repeatedly degrades to the whole layer rather than to a failure (I11).

**What a prediction may not do is change a result** (I5). It selects what crosses the network and
nothing else; a wrong one costs bytes and time, and a build with every hint disabled produces the
same layers.

### C.5 Failure

A worker that disappears mid-step causes the step to be re-queued elsewhere. This is sound because
steps are pure (I1), and it is the same property that makes retry safe (I7).

#### C.5.1 Concurrent claims

Two workers may claim one step. This is **not arbitrated**: both evaluate it, both publish, and the
second publication is refused by the insert-only rule (I9) or is byte-identical to the first. Steps
are pure (I1), so the two results agree; the loser discards its own and continues.

No lock, no leader, no claim registry. A protocol that prevented the duplicate would cost a
round-trip on every step to save the work of a race that is rare and whose cost is bounded by one
step's duration - and it would introduce the one thing a fleet of independent machines cannot have
cheaply, which is agreement about who is doing what.

```text
claim(s, w₁) ∧ claim(s, w₂) ⟹ result(w₁) = result(w₂)
```

Unnumbered deliberately: `(C.3)` would be the next equation in this appendix and **C.3 is already a
section**, which the engine cites as `(C.3)` in three comments. A number that names two things is a
citation that reads plausibly as either.

This is I1 restated at the fleet, and is the same rule the single-machine store already follows:
a layer, a translation and an image are each staged under a name the filesystem chose and renamed
into place, and a writer that loses the rename keeps the winner's copy because the identity names
the content. **A race worth losing is not a race worth preventing.**

#### C.5.2 Saturation

A worker that cannot start a step **refuses the assignment**; it does not queue it.

The scheduler places on load (4.7.1), and load is the count of assignments it has made. A worker
holding a private queue makes that number describe something that is no longer true: the scheduler
believes it has balanced work it has in fact piled on one machine, and the machine it is protecting
is the one it will next choose. Refusal keeps the scheduler's model and the fleet's state the same
object.

A refusal is not a failure. The step is re-placed among the remaining eligible workers, exactly as
one whose worker disappeared is (C.5), and the same purity that makes that sound makes this sound.
An invoker with no eligible worker left refuses the build with the diagnosis §4.7.1 requires, rather
than waiting for one to free: **a build that cannot be placed should say so, not hang.**

**[GAP]** What a worker uses to decide it is saturated - a fixed concurrency, a memory watermark, a
measured queue delay - is deliberately unspecified. It is a policy of the worker and observable only
as a refusal, so a fleet of machines running different policies is well-formed.

## Appendix D. Oracle exclusions

Differential comparison against the BuildKit engine treats divergence as a defect except for the
entries below. Each requires a stated reason; the table is reviewed as a whole rather than grown
one exception at a time, because that is how "equivalent" becomes "similar".

| Excluded                             | Reason                                                                         |
| ------------------------------------ | ------------------------------------------------------------------------------ |
| sub-second mtimes                    | intentional divergence per I8; compare truncated to seconds                    |
| layer and image digests              | different writer and timestamps; compare *contents*                            |
| `created` timestamps in image config | wall clock                                                                     |
| tar entry order                      | normalise by sorting                                                           |
| `/etc/hosts`, `/etc/resolv.conf`     | injected by the runtime during exec; runtimes differ                           |
| builds beyond 𝑛ₘₐₓ layers            | BuildKit cannot perform them at all (E11); the goal is to be better, not equal |

Corpus entries must be self-consistent: a candidate is built twice under the reference engine and
discarded if it differs from itself. A non-deterministic oracle case teaches developers to ignore
failures.

## Appendix E. Index of notation

Every symbol, once, with the equation or section that introduces it. Generated by extracting each
mathematical character from the document and requiring a definition for each; six defects were
found and fixed in the process, listed at the end.

### E.1 Sets

| Symbol | Meaning               | Introduced  |
| ------ | --------------------- | ----------- |
| 𝔹      | byte strings          | §1.1        |
| 𝔻      | digests               | §3.1        |
| 𝕂      | cache keys            | §4.4        |
| 𝕂ₘ     | mask keys             | §2, App A.2 |
| 𝕂ₛ     | step-class keys       | §2, §6      |
| 𝔸      | attestations          | (2.3)       |
| 𝕊      | steps                 | (3.2)       |
| 𝕃      | layers                | §3.2        |
| 𝔾      | declarations          | §3.2a       |
| ℙ      | paths                 | §1.1        |
| ℕ      | non-negative integers | §1.1        |

### E.2 State

| Symbol | Meaning             | Introduced |
| ------ | ------------------- | ---------- |
| σ      | engine state        | (2.1)      |
| 𝔅      | blob store          | (2.2)      |
| 𝔄      | action cache        | (2.3)      |
| 𝔐      | masks               | §2, App A  |
| 𝔇      | determinism beliefs | §2, §6     |
| ℜ      | build records       | §2, App B  |

### E.3 Values

| Symbol | Meaning                                  | Introduced     |
| ------ | ---------------------------------------- | -------------- |
| ℓ      | a layer                                  | (3.1)          |
| γ      | a declaration                            | (3.8), (3.10)  |
| s      | a step                                   | (3.2)          |
| 𝑏      | a base stack                             | (3.2)          |
| ω      | an operation                             | (3.2)          |
| ε      | ambient state a step may observe         | (3.2), §4.4    |
| π      | a platform                               | (3.2)          |
| ρ      | a result                                 | (3.3)          |
| δ      | a container daemon's provenance          | (3.5), §3.4b   |
| μ      | a cache mount's sharing mode             | (3.6), §3.3c   |
| 𝑒      | an exit code                             | (3.3)          |
| 𝑟      | an observation set                       | (3.4)          |
| 𝑅      | paths read, with digests                 | (3.4)          |
| 𝑁      | negative lookups                         | (3.4)          |
| 𝐷      | directories listed, with listing digests | (3.4)          |
| 𝑟̂      | a *predicted* observation set            | §4.5           |
| κ      | a cache key                              | (4.5), (4.6)   |
| 𝑑      | a digest                                 | §3.1           |
| 𝑤      | a writer identity                        | (2.3)          |
| 𝑝      | provenance                               | (2.3), App B.3 |
| 𝑘      | a cryptographic key                      | (C.1)          |
| ℰ      | an Earthfile                             | (4.1)          |
| 𝐀      | the artefacts a build yields             | (4.1)          |
| 𝑡      | a target                                 | (4.1)          |
| 𝑐      | a local context                          | (4.1)          |
| 𝑛ₘₐₓ   | the maximum stack depth                  | (4.8)          |

### E.4 Functions

| Symbol | Meaning                       | Introduced   |
| ------ | ----------------------------- | ------------ |
| Υ      | the build transition          | (4.1)        |
| Σ      | the step transition           | (4.2)        |
| Λ      | cache lookup                  | (4.4)        |
| Κ₁     | chain key derivation          | (4.5)        |
| Κ₂     | observed-input key derivation | (4.6)        |
| Ω      | execution under observation   | §4.2         |
| Μ      | mask consultation             | §4.2, App A  |
| Δ      | layer capture                 | §4.6         |
| Φ      | stack flattening              | (4.8)        |
| ℋ      | the hash, BLAKE3-256          | §3.1         |
| 𝒮      | canonical serialisation       | App B.1      |
| Θ      | reference resolution          | (3.7), §3.4d |
| id(ℓ)  | a layer's identity            | (3.1)        |

### E.5 Operators

Defined in §1.2: ‖ injective concatenation, ⟨…⟩ sequence, sort, 𝑓[𝑥], 𝑓 ⊕ {𝑥 ↦ 𝑦}, ⊥, and the
accessors ω(s), ε(s).

Named predicates and helpers, each defined where it is introduced:

| Name             | Meaning                                    | Introduced |
| ---------------- | ------------------------------------------ | ---------- |
| id(ℓ)            | a layer's identity                         | (3.1)      |
| id(γ)            | a declaration's identity                   | (3.8)      |
| sort(𝑆)          | canonical ordering                         | §1.2       |
| consistent(𝑟̂, 𝑏) | a prediction still matches the base        | §4.5       |
| legal(𝑔)         | a schedule satisfies every hard constraint | §4.7.1     |

Set notation ∈, ∀, ∖ and the partial-map arrow ⇀ carry their usual meanings.

### E.6 What writing this appendix found

The exercise is the test, and it failed six times:

| Defect                                                                      | Resolution                                                          |
| --------------------------------------------------------------------------- | ------------------------------------------------------------------- |
| 𝑐 meant both *exit code* (3.3) and *local context* (4.1)                    | exit code became 𝑒                                                  |
| Μ was used in (4.3) and defined nowhere                                     | defined in §4.2, listed in §1.1                                     |
| Ω was listed in §1.1 and defined nowhere                                    | defined in §4.2                                                     |
| ℰ and 𝐀 appeared in (4.1) unannounced                                       | added to §1.1 as script and bold                                    |
| fraktur (stores) versus double-struck (sets) was an undocumented convention | stated in §1.1                                                      |
| 𝑘 in (C.1) broke the lower-case-Greek rule for persistent values            | §1.1 now names italic roman for cryptographic keys, distinct from κ |

A symbol that cannot be given a one-line definition pointing at its introducing equation was never
properly defined. Regenerate this appendix whenever §§1-4 change.
