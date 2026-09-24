package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ErrInputsChanged is `--check-inputs` saying the build must run.
//
// A sentinel rather than a message, because the caller acts on it: CI skips a
// job on its absence, and a front end turns it into an exit code distinct from
// a real failure. "Changed" and "the Earthfile does not parse" must not arrive
// as the same thing, or a broken build reads as a full one.
var ErrInputsChanged = errors.New("the build's inputs have changed")

// inputsVersion is the file format. Bumped when a field's meaning changes, so
// a fingerprint written by an older engine is refused rather than compared to
// one that was computed differently.
const inputsVersion = 1

// Inputs is what a build's plan depends on, written down.
//
// **The fingerprint is the whole of the comparison; everything else is for the
// reader.** It is derived from the graph's node identities, which are recursive
// over their inputs (`ir.Node.ID`) - so it covers the Earthfile's text, every
// build argument and environment value, the platform, the resolved digest of
// every base image, and the content digest of every path a COPY reads. A file
// listing context hashes alone goes green on an edited command or a moved tag;
// this does not.
//
// What it does *not* cover is the world outside the plan: what a `RUN` fetches
// from the network, what a `LOCALLY` step reads from the machine, the value
// behind a secret where no fleet key is configured. Those are Caveats, and a
// build carrying one is never certified unchanged.
type Inputs struct {
	Version int    `json:"version"`
	Target  string `json:"target"`
	// Platform the plan was made for. Two platforms are two fingerprints.
	Platform string `json:"platform"`
	// Fingerprint is the value a later build compares against.
	Fingerprint string `json:"fingerprint"`
	// Context is every path the build reads from the host, with the digest of
	// what it held. Present so a "changed" verdict can name the file.
	Context []ContextInput `json:"context,omitempty"`
	// Images is each image reference as written, and what it resolved to.
	// Provenance, not input - the resolved reference is already in the
	// fingerprint - so that a moved tag is legible rather than merely detected.
	Images []ImageInput `json:"images,omitempty"`
	// Caveats are the reasons this fingerprint under-claims. Any at all means
	// the build cannot be certified unchanged.
	Caveats []string `json:"caveats,omitempty"`
}

// ContextInput is one path read from the host and the digest of its contents.
//
// The digest is ℓ_con, which excludes mtimes (§3.3a) - two checkouts of one
// commit agree, which is the whole point on a CI runner that clones fresh.
type ContextInput struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// ImageInput is a reference as the Earthfile wrote it and what it resolved to.
type ImageInput struct {
	Ref      string `json:"ref"`
	Resolved string `json:"resolved,omitempty"`
}

// inputsOf reads the fingerprint out of a plan.
func inputsOf(plan *interp.Plan, target, platform string) Inputs {
	in := Inputs{
		Version:     inputsVersion,
		Target:      target,
		Platform:    platform,
		Fingerprint: fingerprintOf(plan).String(),
		Caveats:     caveatsOf(plan),
	}

	seen := map[string]string{}

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind != ir.OpLocal || len(n.Op.Args) == 0 {
			continue
		}

		seen[n.Op.Args[0]] = n.Op.Content.String()
	}

	for path, digest := range seen {
		in.Context = append(in.Context, ContextInput{Path: path, Digest: digest})
	}

	// Sorted, because a map walk is not an order and this file is compared byte
	// for byte by a CI cache.
	sort.Slice(in.Context, func(i, j int) bool { return in.Context[i].Path < in.Context[j].Path })

	for ref, to := range plan.Pinned {
		in.Images = append(in.Images, ImageInput{Ref: ref, Resolved: to})
	}

	sort.Slice(in.Images, func(i, j int) bool { return in.Images[i].Ref < in.Images[j].Ref })

	return in
}

// fingerprintOf is one value over the whole plan.
//
// **Every root, not only the first.** `BUILD +other` puts a target in `Also`
// rather than in the root's inputs - it is a thing the build must run, not a
// filesystem the root stands on - so a fingerprint over `Root` alone goes green
// when a BUILD-only dependency changes.
//
// **And what the build produces, not only what it runs.** A destination and an
// image name live in the plan beside the graph rather than in it, so two builds
// writing `out-one.txt` and `out-two.txt` have the same graph exactly - and a
// fingerprint over the graph alone certifies the second unchanged, skips it, and
// leaves the file it was asked for unwritten. The layers really are identical;
// what the job was asked to produce is not, and that is what a green tick is a
// claim about.
func fingerprintOf(plan *interp.Plan) ir.NodeID {
	h := ir.NewHasher()

	g := plan.Graph

	roots := append([]*ir.Node{g.Root}, g.Also...)

	ids := make([]ir.NodeID, 0, len(roots))

	for _, n := range roots {
		if n != nil {
			ids = append(ids, n.ID())
		}
	}

	// Sorted, so the order `Also` happens to be in does not reach the value.
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	h.Count(len(ids))

	for _, id := range ids {
		h.Fixed(id[:])
	}

	hashProduces(h, plan)

	return h.Sum()
}

// hashProduces writes what the build was asked to leave behind.
//
// Sorted and counted, as everything else here is: the order the interpreter
// happened to collect them in is not an input, and without a count two entries
// and one concatenation hash alike (§1.4).
func hashProduces(h *ir.Hasher, plan *interp.Plan) {
	saved := make([]string, 0, len(plan.Artifacts))

	for _, a := range plan.Artifacts {
		// The local destination is the field that carries an argument, and the
		// path is what identifies the artifact. Both, because `SAVE ARTIFACT a`
		// and `SAVE ARTIFACT b` to one destination are different builds too.
		saved = append(saved, a.Path+"\x00"+a.LocalDest)
	}

	sort.Strings(saved)
	h.Count(len(saved))

	for _, one := range saved {
		h.Str(one)
	}

	declared := make([]string, 0, len(plan.Images))

	for _, i := range plan.Images {
		// Push beside the reference: a build told to publish an image and one
		// told to keep it are not the same job, whatever the layers say.
		declared = append(declared, fmt.Sprintf("%s\x00%t", i.Ref, i.Push))
	}

	sort.Strings(declared)
	h.Count(len(declared))

	for _, one := range declared {
		h.Str(one)
	}
}

// caveatsOf is every reason this plan's fingerprint promises less than it looks
// like it promises.
//
// **Each one is a thing the engine knows it cannot key.** Reporting them is what
// keeps "unchanged" honest: a caller skipping a job on this file is told when
// the answer is a guess rather than being handed a guess that looks like an
// answer.
func caveatsOf(plan *interp.Plan) []string {
	var (
		out  []string
		once = map[string]bool{}
	)

	say := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if !once[msg] {
			once[msg] = true

			out = append(out, msg)
		}
	}

	for _, n := range plan.Graph.Nodes() {
		switch {
		case n.Op.NoCache:
			say("%s is --no-cache, so it runs whatever the inputs say", n.Meta.Source)

		case n.Op.Kind == ir.OpHost:
			say("%s is LOCALLY, and reads this machine rather than the build context", n.Meta.Source)

		case n.Op.Kind == ir.OpImage && len(n.Op.Args) > 0 && !strings.Contains(n.Op.Args[0], "@sha256:"):
			say("%s is not pinned to a digest, so the tag may have moved", n.Op.Args[0])

		case len(n.Op.SecretEnv) > 0 && len(n.Op.SecretDigest) == 0:
			say("%s reads a secret whose value is outside the fingerprint"+
				" (set %s to key on it)", n.Meta.Source, EnvSecretHMAC)
		}
	}

	sort.Strings(out)

	return out
}

// writeInputs writes the fingerprint where the caller asked for it.
//
// Indented and newline-terminated, because a human reads it in a PR and `git
// diff` wants the newline. Deterministic: every list above is sorted, and the
// encoder writes struct fields in declaration order.
func writeInputs(at string, in Inputs) error {
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return fmt.Errorf("write the input fingerprint: %w", err)
	}

	//nolint:gosec // the caller named this path, as every output flag's does
	err = os.WriteFile(at, append(b, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("write the input fingerprint to %s: %w", at, err)
	}

	return nil
}

// checkInputs compares this plan against a fingerprint written earlier.
//
// Nil means the build need not run. Everything else is ErrInputsChanged with
// what differed, or a plain error where the file itself is unusable - a missing
// one included, because "no fingerprint" is not "nothing changed" and a CI cache
// misses on its first run for every project.
func checkInputs(at string, now Inputs) error {
	b, err := os.ReadFile(at) //nolint:gosec // the caller named this path
	if err != nil {
		return fmt.Errorf("read the input fingerprint at %s: %w", at, err)
	}

	var was Inputs

	err = json.Unmarshal(b, &was)
	if err != nil {
		return fmt.Errorf("%s is not an input fingerprint: %w", at, err)
	}

	if was.Version != inputsVersion {
		return fmt.Errorf("%w: %s was written by a different engine"+
			" (format %d, this engine writes %d)", ErrInputsChanged, at, was.Version, inputsVersion)
	}

	// **Before the comparison, because a caveat is not about what changed.** A
	// build the engine cannot key is one it cannot certify, however equal the
	// two fingerprints are.
	if len(now.Caveats) > 0 {
		return fmt.Errorf("%w: this build cannot be certified unchanged\n  %s",
			ErrInputsChanged, strings.Join(now.Caveats, "\n  "))
	}

	if was.Target != now.Target || was.Platform != now.Platform {
		return fmt.Errorf("%w: %s is about %s on %s, this build is %s on %s",
			ErrInputsChanged, at, was.Target, was.Platform, now.Target, now.Platform)
	}

	if was.Fingerprint == now.Fingerprint {
		return nil
	}

	return fmt.Errorf("%w:\n  %s", ErrInputsChanged, strings.Join(differences(was, now), "\n  "))
}

// differences is what to tell a reader who has been told the build must run.
//
// The fingerprint has already decided; this only explains. Where nothing
// legible differs - an edited command, a build argument - it says so rather
// than listing nothing, because an empty explanation reads as a bug in the
// comparison.
func differences(was, now Inputs) []string {
	before := map[string]string{}
	for _, c := range was.Context {
		before[c.Path] = c.Digest
	}

	var out []string

	for _, c := range now.Context {
		got, had := before[c.Path]

		switch {
		case !had:
			out = append(out, "context "+c.Path+" is new")
		case got != c.Digest:
			out = append(out, "context "+c.Path+" changed")
		}

		delete(before, c.Path)
	}

	for path := range before {
		out = append(out, "context "+path+" is gone")
	}

	wasImage := map[string]string{}
	for _, i := range was.Images {
		wasImage[i.Ref] = i.Resolved
	}

	for _, i := range now.Images {
		if to, had := wasImage[i.Ref]; had && to != i.Resolved {
			out = append(out, fmt.Sprintf("%s moved from %s to %s", i.Ref, to, i.Resolved))
		}
	}

	sort.Strings(out)

	if len(out) == 0 {
		// The Earthfile, a build argument, an environment value: all of them
		// reach the fingerprint and none of them is listed above.
		return []string{"the Earthfile or a build argument changed"}
	}

	return out
}
