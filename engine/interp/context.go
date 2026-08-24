package interp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ignore"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/timing"
)

// Option configures a build.
type Option func(*options)

type options struct {
	context string
	// versionFlags are features the *caller* turns on, whatever the file's
	// VERSION line says: `--version-flag-overrides`. Seven of the corpus's
	// invocations pass it, and it is how a tree drives one file through two
	// dialects without keeping two copies of it (E473).
	versionFlags []string
	args         map[string]string
	// terminal says the invocation has one, so an interactive step can run.
	terminal bool
	// commands runs what the plan cannot work out: a condition it cannot
	// decide, a `$(...)` it cannot expand. Nil means both are refused.
	commands Commands
	// remotes checks out a reference to another repository. Nil means such a
	// reference is refused rather than fetched.
	remotes Remotes
	// artifacts builds a target and gives back where its output landed, so a
	// plan that depends on the *content* of a produced file can be made. Nil
	// means such a plan is refused as something this call did not provide,
	// which is what a plan-only caller wants (E487).
	artifacts Artifacts
	// gitClone fetches a repository named by GIT CLONE. Nil means the construct
	// is refused rather than fetched.
	gitClone GitClone
	// resolveImage pins a mutable reference to a digest (§3.4d). Nil means a
	// reference is left as written - not refused, unlike the seams above it:
	// see WithImageResolver for why FROM is the exception.
	resolveImage ResolveImage
	// secrets are the names the invocation supplied. Only the *names* are kept
	// here: the interpreter needs to know a secret exists so it can refuse one
	// that does not, and needs the value for nothing at all.
	secrets map[string]bool
	// platform is what the build runs on when no `--platform` says otherwise:
	// the sandbox's own, which is also what NATIVE* reports. Empty means the
	// invoking machine's, which is right for a plan resolved without a sandbox.
	platform string
}

// WithPlatform sets the platform the build runs on.
//
// It is what `ARG NATIVEARCH` and its siblings answer, and the default for
// `ARG TARGETARCH` where no `--platform` overrides it. Without it those
// arguments declare as empty - which is what they did, silently, until a
// binary was written to a directory named `$TARGETOS` (E49).
func WithPlatform(p string) Option {
	return func(o *options) { o.platform = p }
}

// nativePlatform is the platform the work runs on.
func (o options) nativePlatform() string {
	if o.platform == "" {
		return UserPlatform()
	}

	return o.platform
}

// WithContext sets the local build context: the directory COPY reads from.
func WithContext(dir string) Option {
	return func(o *options) { o.context = dir }
}

// resolveContext turns a COPY source into a node whose identity covers the
// bytes it names.
//
// The digest is taken here, at graph construction, rather than at execution.
// That is the whole point: a cache key is derived from the graph, so anything
// the result depends on has to be in the graph before the key is computed.
// Resolving it later would mean keying on a path and hitting on stale content.
func resolveContext(root, src, where string) (*ir.Node, error) {
	if root == "" {
		return nil, fmt.Errorf(
			"COPY at %s needs a build context, and none was given"+
				"\n  the context is the directory COPY reads from; without it there is nothing to copy",
			where)
	}

	// Normalise the root before comparing anything against it. `--dir .` is the
	// ordinary invocation, and a path joined onto "." does not have "." as a
	// textual prefix - so a relative context refused every file in it. Resolving
	// symlinks too, for the same reason the unpacker does: on macOS a directory
	// under /var resolves to /private/var, and comparing one form against the
	// other rejects everything.
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve the build context %s: %w", root, err)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	}

	root = abs

	// Checked before normalising, not after. `filepath.Clean("/" + "../a.txt")`
	// is `/a.txt`, so joining it to the root produces a path *inside* the
	// context - and `COPY ../a.txt` would quietly copy the wrong file rather
	// than say it cannot. The test is on the source as written.
	if rel := filepath.Clean(src); rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("COPY at %s: %q leaves the build context", where, src)
	}

	clean := filepath.Clean("/" + strings.TrimPrefix(src, "./"))

	abs = filepath.Join(root, clean)
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return nil, fmt.Errorf("COPY at %s: %q leaves the build context", where, src)
	}

	_, err = os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf(
			"COPY at %s: %s is not in the build context"+
				"\n  looked in %s", where, src, root)
	}

	// Digest the named path and nothing else. Digesting the whole context would
	// make an unrelated edit invalidate every COPY in the Earthfile, which is how
	// a cache stops being worth having.
	// Timed because it is the largest thing planning does and nothing said so.
	// A warm build of this repository spends most of its wall clock here, and
	// the phase list did not mention it - which is how a cost gets attributed
	// to whatever *is* instrumented next to it (E562).
	endDigest := timing.Phase("context:digest", src)
	c, err := layer.TakeIgnoring(abs, excluderFor(root, abs))
	endDigest()

	if err != nil {
		return nil, fmt.Errorf("read %s from the build context: %w", src, err)
	}

	// The *content* digest, not the identity: ℓ_con excludes mtimes (green paper
	// §3.3a) and that is what a build context needs. Two checkouts of one commit
	// have different mtimes everywhere, so keying on ℓ_id would mean a fresh
	// clone never hits the cache and CI rebuilds the world every time. It is the
	// same reason git records content and not timestamps.
	//
	// Timestamps still reach the *image*, because COPY writes files with them;
	// they simply do not decide whether the copy has to happen again.
	return &ir.Node{
		Op:   ir.Op{Kind: ir.OpLocal, Args: []string{strings.TrimPrefix(clean, "/")}, Content: c.Content},
		Meta: ir.Meta{Source: where, Description: "context " + src, ContextRoot: root},
	}, nil
}

// hasPattern reports whether a source is a pattern rather than a path.
func hasPattern(src string) bool { return strings.ContainsAny(src, "*?[") }

// expandContextPatterns turns `scripts/*.sh` into the files it names.
//
// Expanded here, at graph construction, for the same reason the digest is taken
// here: what a COPY reads has to be in the graph before the key is computed.
// Expanding at execution would key the build on the pattern, so adding a file
// that the pattern matches would not change the key, and the build would hit a
// cache entry that predates the file.
//
// Sorted, because a directory listing is not ordered and the order reaches the
// key. Two machines expanding one pattern differently would key the same build
// two ways, and neither would ever hit the other's cache.
func expandContextPatterns(root string, sources []string, where string) ([]string, error) {
	out := make([]string, 0, len(sources))

	for _, src := range sources {
		// An artifact reference names another target's output, which no
		// directory holds yet.
		if !hasPattern(src) || strings.Contains(src, "+") {
			out = append(out, src)

			continue
		}

		if rel := filepath.Clean(src); rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("COPY at %s: %q leaves the build context", where, src)
		}

		abs, err := realDir(root)
		if err != nil {
			return nil, err
		}

		matches, err := filepath.Glob(filepath.Join(abs, filepath.Clean("/"+strings.TrimPrefix(src, "./"))))
		if err != nil {
			return nil, fmt.Errorf("COPY at %s: %q is not a valid pattern: %w", where, src, err)
		}

		if len(matches) == 0 {
			return nil, fmt.Errorf(
				"COPY at %s: %q matches nothing in the build context"+
					"\n  looked in %s", where, src, abs)
		}

		sort.Strings(matches)

		for _, m := range matches {
			rel, err := filepath.Rel(abs, m)
			if err != nil {
				return nil, fmt.Errorf("COPY at %s: %q: %w", where, src, err)
			}

			out = append(out, filepath.ToSlash(rel))
		}
	}

	return out, nil
}

// onlyPresent keeps the sources that are in the build context.
//
// For `COPY --if-exists`, where an absent source is not an error. Artifact
// references are kept regardless: they name another target's output, which no
// directory holds yet, so their presence is not a question this can answer.
func onlyPresent(root string, sources []string) []string {
	out := make([]string, 0, len(sources))

	for _, src := range sources {
		if strings.Contains(src, "+") {
			out = append(out, src)

			continue
		}

		abs, err := realDir(root)
		if err != nil {
			continue
		}

		_, err = os.Stat(filepath.Join(abs, filepath.Clean("/"+strings.TrimPrefix(src, "./"))))
		if err != nil {
			continue
		}

		out = append(out, src)
	}

	return out
}

// WithSecrets declares which secrets the invocation supplied.
//
// Names, not values. The interpreter needs to know a secret exists so it can
// refuse a step asking for one nobody supplied; it needs the value for nothing,
// and not having it is what makes a value in the graph impossible rather than
// merely avoided.
// WithTerminal says the invocation has a terminal an interactive step could run
// on.
//
// A prompt needs one, and a terminal is a descriptor the caller supplies - so a
// build with none refuses `RUN --interactive` the way it refuses a secret nobody
// passed: `ErrNotProvided`, a valid Earthfile and an incomplete invocation
// (E151's family). A CI job and a piped stdin are exactly that case, and they
// are the common one.
//
// A *fleet* is a different question - a descriptor cannot cross a machine, so
// with workers elsewhere this is not withheld but impossible - and that refusal
// is deliberately not written yet: S6 does not exist, so it could never fire,
// and a refusal nothing can reach is the shape this work keeps finding by
// accident rather than adding on purpose (E195).
func WithTerminal(has bool) Option {
	return func(o *options) { o.terminal = has }
}

func WithSecrets(secrets map[string]string) Option {
	names := make(map[string]bool, len(secrets))
	for k := range secrets {
		names[k] = true
	}

	return func(o *options) { o.secrets = names }
}

// GitClone fetches a repository and returns the directory it landed in.
//
// A seam of its own rather than the one Earthfile references use: that takes a
// repository path this engine builds a URL from, and GIT CLONE is handed a URL
// as written - `ssh://git@host/x.git` among them. Reusing it would mean
// guessing which half of the string the caller meant.
type GitClone func(url, ref string) (dir string, err error)

// WithGitClone supplies the fetcher for GIT CLONE.
//
// Without one the construct is refused, which is what a plan-only caller needs:
// producing a graph must not reach the network.
func WithGitClone(fn GitClone) Option {
	return func(o *options) { o.gitClone = fn }
}

// WithVersionFlags turns on features for every file in the build.
//
// Each is a VERSION flag, with or without its leading dashes. A name this engine
// does not know is refused rather than ignored: a caller who asks for a dialect
// and is given another one silently has no way to find out.
func WithVersionFlags(flags []string) Option {
	return func(o *options) { o.versionFlags = flags }
}

// Artifacts builds a target and reports where its output can be read.
//
// The third capability an interpreter may be given, beside deciding a condition
// and fetching a repository - and, like both, one whose absence is a refusal
// naming what was withheld rather than a gap in the engine.
//
// `ref` is written as the Earthfile wrote it: `+gen/` for a whole output,
// `+gen/other.Dockerfile` for one artifact of it.
type Artifacts func(ref, where string) (dir string, err error)

// WithArtifacts supplies the builder for a plan that depends on what another
// target produced.
//
// Only `FROM DOCKERFILE` needs it today: the Dockerfile is parsed while
// planning, so a Dockerfile a target writes has to be built before the plan
// exists. **This is the point at which planning stops being a pure function of
// the source** - the same boundary `WithCommands` crosses for a condition, and
// worth naming for the same reason.
func WithArtifacts(fn Artifacts) Option {
	return func(o *options) { o.artifacts = fn }
}

// contextExcluder applies a context's ignore file to a path inside it.
//
// **The patterns are written against the context root and the walk is under a
// subdirectory of it**, so `examples/next-js/node_modules` in the ignore file
// has to match `next-js/node_modules` when the walk started at `examples`. The
// translation happens here rather than in the matcher, which is right to know
// only about the root.
//
// Read once per digest rather than once per build, which is a stat of a file
// that is nearly always absent - and reading it per build would mean caching an
// ignore file that a caller may have just edited.
// excluderFor reads a context's ignore file once and reuses it.
//
// **One definition, in `engine/ignore`.** This lived here, and the executor
// staged the context with no exclusions at all - so the ignore file decided the
// digest and not the bytes, and the two disagreed by about sixty thousand files
// on this repository (E623). Both sides now ask the same function.
func excluderFor(root, under string) ignore.Excluder {
	return ignore.For(root, under)
}
