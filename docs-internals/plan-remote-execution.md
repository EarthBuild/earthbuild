# Plan: speaking the remote execution API

Companion to [the Green Paper](green-paper.md), whose (4.5a) and (4.5b) define 𝜏 and 𝜈 - the
objects this plan puts on a wire. The *why* is that a content-named tree is the thing two machines
that never shared a build graph can agree about, and REAPI is where that agreement is already
standardised.

Durations are working weeks for one developer, and are estimates.

## What made this reachable

Three things landed on 2026-09-13/14 and are not part of this plan's cost:

* 𝜏 is a Merkle tree of directories rather than one digest over a flat list (4.5b). A subtree has a
  name independent of where it sits, so two bases holding one directory hold one node.
* Nodes are addressable and self-verifying, and `KindTreeMissing` asks a peer which it lacks.
* The fold is carried across layers, so a step names only the directories it moved. On a 48-step
  ladder over a 20k-entry base: 949ms to 47ms, about 1ms a fold, flat in stack depth.

## Decisions taken (2026-09-14)

* **ℋ stays BLAKE3-256; SHA-256 is selected per store, and the two co-exist.** Different functions
  give different keys, so a store may hold both generations and collection removes whichever stops
  being used. No stamp, no migration, no mixing hazard: the failure mode of getting it wrong is a
  miss, never a false hit (I3).
* **BLAKE3 is negotiable with Bazel and not with Buck2.** REAPI has `BLAKE3 = 9` in
  `DigestFunction.Value`; Bazel has `--digest_function=BLAKE3` since 6.4 and BuildBuddy serves it.
  Buck2's own RFC says "publicly available RE providers use SHA256", declines to make output
  digests configurable, and wants *keyed* BLAKE3 internally - which would not match ours. So the
  switch is needed for Buck2 and unnecessary for a Bazel-family server.
* **Our extra metadata goes in `NodeProperties.properties`, omitted when empty.** Not a second
  digest beside a REAPI one. For every tree either tool would construct the properties are empty,
  protobuf emits nothing for an unset message, and our bytes are theirs. Measured on a real
  repository: a 3,131-file source tree and a 20,000-file output tree each have exactly **two**
  distinct (mode, uid, gid) triples, and 644-vs-755 *is* `is_executable`. The genuine extras are
  uid, gid and hardlinks.
* **A node kind REAPI cannot express is refused, not encoded.** A character device is not a
  `FileNode` with an unusual property - it has no content digest and no node type at all, and
  emitting one would have a conforming consumer materialise an empty regular file where a device
  belongs. Both measured trees held zero such entries; the real sources are rootfs-building steps.
* **Hardlink identity stays in the digest.** Relinking identical-but-unlinked files at
  materialisation would match REAPI and save disk, and is wrong: a later step may write to one and
  not expect the other to change. Preserving an existing link is safe because the step that made it
  expected sharing; creating one is not, because nothing did.
* **Sockets are not layer members** (green paper §3.3, landed). A socket is a live process's
  address and no process crosses a step.
* **The hardlink path costs nothing measurable.** `e.hardlink` is relative to the layer root, so a
  directory holding one has a node digest that depends on a path outside it - which is the
  position-independence subtree sharing rests on. Measured, and it collapses at three removes:

  * **A release build has no hardlinks at all** - 10,638 files, zero. Every hardlink in a Rust
    output tree is rustc's incremental machinery, and cargo disables incremental for release.
    CI builds `--release`, as does `examples/rust-layered`, so the tree this engine caches is
    unaffected entirely.
  * In a *debug* tree, 58 of 8,041 directories hold one, 118 counting the ancestors whose digests
    then depend on them - 98.5% unaffected. All 2,697 inode groups share the common ancestor
    `target/debug`, pairing `deps/*.rcgu.o` with `incremental/<crate>/s-<session>/*.o`.
  * Those `incremental/` directories carry a per-session id in their name, so they could never
    match across two builds whatever the hardlinks did. The only genuine loss is `deps/`, one node,
    whose pointers name those unique paths.

  And position-dependence only bites on *relocation* in any case: two bases holding a directory at
  one path record the same string and share the node normally. No encoding change.

## What is not decided

| decision                   | what it needs                                                                                                                                                                                                                                                                                                   |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| hand-roll the protobuf     | Proto3 defines no canonical form. REAPI works because every implementation emits ascending field number and omits defaults, and the ecosystem would collapse otherwise - but it is *de facto*. Four messages is ~100 lines to own and test; a dependency is less code and less control.                         |
| accepting an RE cache hit  | RE's equivalence is coarser than ours: two trees differing only in uid, xattrs or non-executable mode are one action input to them. Shipping by `re(d)` is safe; *trusting* a hit keyed on it imports their equivalence, and we hash uid because a step can observe it. An I3 question on our side, not theirs. |
| symlinks as their own list | REAPI has three typed lists; 𝜈 has two, with symlinks folded in under a kind byte. Splitting them makes "one name, two kinds" unrepresentable, and costs a cache generation - cheap only while another 𝜈 change is being spent.                                                                                 |
| uid/gid as a tree default  | Both are constant across every tree measured. Hoisting them to a per-tree default makes `extra(d)` empty for a source tree, so 𝜈 becomes a function of the REAPI digest alone.                                                                                                                                  |

## Phase R0 - emit a REAPI `Directory` (2 weeks)

A tree we already hold, serialised as REAPI and digested as REAPI. No protocol, no network.

* `Directory`, `FileNode`, `DirectoryNode`, `SymlinkNode`, `NodeProperties` encoded to bytes.
* Extras into `properties`, the field omitted when there is nothing to say.
* A tree holding a device, fifo or anything else without a node type is refused, naming the path
  and saying the protocol cannot carry it.

**Exit**: a tree of known content digests to a constant checked against a value produced by Bazel,
not by us. Without an external vector this phase proves only self-consistency.

## Phase R1 - SHA-256 as a store's digest function (2 weeks)

* `EARTH_RE` (or equivalent) selects ℋ at store creation; both generations co-exist.
* `ir.DigestOf`, `NewHasher` and `NewStreamHasher` take it from one place, so nothing can be hashed
  with the wrong function by omission.

**Exit**: one repository built twice under the two functions, both green, sharing a store, and
`earth prune` reclaiming the abandoned generation. Note that the measured cost is *negative* on
hardware with SHA-2 instructions - concurrently over many small files SHA-256 ran 6766 MB/s against
BLAKE3's 532 - and unverified on x86 without SHA-NI, where it may invert.

## Phase R2 - a cache-only REAPI front end (6 weeks)

The first thing another tool can use. Serves, over our store, without executing anything:

* `Capabilities` - advertising the digest function the store was made with
* `ContentAddressableStorage`: `FindMissingBlobs`, `BatchReadBlobs`, `GetTree`
* `ActionCache`: `GetActionResult` only. Writing is Phase R3's question.

**Exit**: `bazel build --remote_cache=` against an EarthBuild store gets hits for actions an
EarthBuild build populated. This is the milestone that is useful to somebody else without changing
what EarthBuild means.

## Phase R3 - delegate a step to an RE service (unscoped)

The endgame, and the one with a design question rather than a work list. A result EarthBuild did
not capture has no `Capture.ID`, so I8 has nothing to hash and Κ₁ keys on something it was not
built for. That wants settling in the Green Paper before any code.

Not scoped here deliberately: the decomposition that makes it worth doing - one action per compiler
invocation rather than per RUN - is a separate argument, and the reason it pays is that it isolates
nondeterminism to the action that has it rather than poisoning twenty minutes.

## Not in this plan

* A transport that ships subtrees. `layer.PackPaths` and `fleet.Blobs.Fragment` already ship a
  subset of a layer by *path*; nodes name content by *digest*. Making those speak one language is
  the prerequisite, and it is a fleet change rather than an RE one.
* `SHA256TREE` (function 8) - chunk-level verification of large blobs. BLAKE3 has this natively via
  Bao, and `lukechampine.com/blake3/bao` is already in the module graph. Only needed if a server
  demands SHA-256 *and* we want verified partial reads.
