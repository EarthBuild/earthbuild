package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// shapeInput is everything that decides what a build does, short of what its
// copied files contain.
//
// **No plan and no graph.** An Earthfile, what was asked of it, and the values
// handed in determine every step there will be - so the shape can be had from
// those directly, in milliseconds, without interpreting anything, without
// running an `IF` to decide a branch and without digesting a byte of the build
// context. That is what makes a pre-flight check cheap enough to be worth
// making. See docs-internals/job-skipping.md.
type shapeInput struct {
	// Source is the Earthfile.
	Source []byte
	// Target is what was asked for, and Platform what it was asked for on.
	Target   string
	Platform string
	// Args are the build arguments after the project's own files were merged
	// in: what the build will actually see.
	Args map[string]string
	// SecretDigests are the keyed digests of the secrets this build carries -
	// what the fleet HMAC produces. **A changed secret is a changed build**,
	// and with a key configured that is what the shape sees.
	SecretDigests map[string]string
	// SecretNames is what is left when no key is configured: which secrets the
	// build carries, not what they are.
	//
	// **A weaker claim, made deliberately and marked.** Without a key there is
	// nothing to fold a value into, so the choice is between covering the names
	// and covering nothing - and refusing outright would leave `--auto-skip`
	// doing nothing at all for anyone who has not configured an HMAC, which is
	// most people. What it costs: a rotated credential does not move the shape,
	// so a job whose result depends on *which* credential it had could skip.
	// Usually a secret fetches something rather than changing what is built;
	// where that is not true, configure the key.
	//
	// Which of the two was used is folded into the shape itself, so a name-keyed
	// shape and a digest-keyed one for the same build are different values and
	// cannot be compared by accident.
	SecretNames []string
	// The flags that change what a build does rather than how it reports.
	Push            bool
	Strict          bool
	NoOutput        bool
	AllowPrivileged bool
	VersionFlags    []string
}

// readsASecret matches the two spellings of a step taking a credential.
//
// Eager, like the one above: a `RUN` whose text merely mentions `--secret`
// refuses a key it could have had, which costs a build rather than a wrong one.
var readsASecret = regexp.MustCompile(`--secret\b|type=secret\b`)

// shapeOf is the invocation: everything asked of a build that is not a file.
//
// **The Earthfiles are not here.** They were, and so was an apparatus of
// refusals that came with them - a build reaching another file, a reference
// built from an argument, a reference nobody pinned - because one file cannot
// describe a build spanning several. It does not have to. The interpreter reads
// every Earthfile a build needs and says which (`interp.Plan.Earthfiles`), so
// they are ordinary inputs like the files a step reads: recorded by the build
// that read them, re-read when it is asked whether to run again. A build across
// six Earthfiles is keyed exactly, with nothing followed and nothing refused.
func shapeOf(in shapeInput) (ir.NodeID, error) {
	h := ir.NewHasher()

	h.Str(in.Target)
	h.Str(in.Platform)
	hashSorted(h, in.Args)
	hashSecrets(h, in)

	h.Bool(in.Push)
	h.Bool(in.Strict)
	h.Bool(in.NoOutput)
	h.Bool(in.AllowPrivileged)

	flags := append([]string(nil), in.VersionFlags...)
	sort.Strings(flags)
	h.Count(len(flags))

	for _, f := range flags {
		h.Str(f)
	}

	return h.Sum(), nil
}

// canonicalTree is what the parser made of an Earthfile, with where it was
// removed.
//
// **The meaning, not the bytes.** A comment, a blank line or a reformat changes
// the file and not the build, and a key that moved for them is the coarseness
// that makes people turn a cache off.
//
// The parser's own output rather than a second walk over it: `Tree` is tagged
// for JSON throughout, so marshalling it is a faithful account of everything the
// parser found, and it cannot fall behind the grammar the way a hand-written
// walker would. Only the source locations come out, because they are line
// numbers and a comment moves every one below it.
//
// Deterministic: `encoding/json` writes a map's keys in sorted order and a
// struct's in declaration order, and the tree is structs and slices.
func canonicalOf(tree earthfile.Tree) ([]byte, error) {
	b, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotDerivable, err)
	}

	var held any

	err = json.Unmarshal(b, &held)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotDerivable, err)
	}

	return json.Marshal(withoutLocations(held))
}

// notTheBuild are the tree's fields that say where something is written or
// what it is documented as, rather than what it does.
//
// `sourceLocation` is line numbers, and a comment moves every one below it.
// `docs` is the comment itself: the parser attaches the lines above a command
// to that command, so a note explaining a `RUN` would otherwise rebuild it. Both
// are read by `earth doc` and by diagnostics; neither is read by the build.
var notTheBuild = []string{"sourceLocation", "docs"}

// withoutLocations strips from a decoded tree everything that is not the build.
func withoutLocations(v any) any {
	switch held := v.(type) {
	case map[string]any:
		for _, key := range notTheBuild {
			delete(held, key)
		}

		for k, inner := range held {
			held[k] = withoutLocations(inner)
		}

		return held

	case []any:
		for i, inner := range held {
			held[i] = withoutLocations(inner)
		}

		return held

	default:
		return v
	}
}

// hashSecrets folds in what the shape can say about this build's credentials,
// and which of the two things that is.
//
// The marker first and always, so the two key spaces cannot meet: a build keyed
// on names and the same build keyed on digests are different shapes, and a
// record made before an HMAC was configured is simply not found afterwards
// rather than being trusted.
func hashSecrets(h *ir.Hasher, in shapeInput) {
	if len(in.SecretDigests) > 0 {
		h.Str("secrets:by-digest")
		hashSorted(h, in.SecretDigests)

		return
	}

	h.Str("secrets:by-name")

	names := append([]string(nil), in.SecretNames...)
	sort.Strings(names)
	h.Count(len(names))

	for _, name := range names {
		h.Str(name)
	}
}

// secretsAreKeyedByName says this shape covers which secrets a build carries
// and not what they are, so a caller can say so once.
func secretsAreKeyedByName(in shapeInput) bool {
	return len(in.SecretDigests) == 0 && readsASecret.Match(in.Source)
}

// hashSorted writes a map into a digest, in an order a map walk does not have.
func hashSorted(h *ir.Hasher, m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	h.Count(len(keys))

	for _, k := range keys {
		h.Str(k)
		h.Str(m[k])
	}
}
