package interp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ignore"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/timing"
)

// Option configures a build.
type Option func(*options)

type options struct {
	// contextCache shares digested context paths between builds, when the
	// caller supplies one. Nil means no sharing, which is the default.
	contextCache *ContextCache
	context      string
	// versionFlags are features the *caller* turns on, whatever the file's
	// VERSION line says: `--version-flag-overrides`. Seven of the corpus's
	// invocations pass it, and it is how a tree drives one file through two
	// dialects without keeping two copies of it (E473).
	versionFlags []string
	// allowPrivileged accepts `RUN --privileged` rather than refusing it. See
	// WithAllowPrivileged.
	allowPrivileged bool
	// unsafeUnpinnedRemoteLocally accepts a `LOCALLY` reached through a
	// reference that is not pinned to a commit. See
	// WithUnsafeUnpinnedRemoteLocally.
	unsafeUnpinnedRemoteLocally bool
	// push says this build is a push, so `RUN --push` steps run.
	push bool
	args map[string]string
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
	// secretDigest maps a secret's source name to a fleet-keyed digest of its
	// value. Empty unless a fleet key is configured, and the only route by
	// which anything derived from a secret's value reaches the graph.
	secretDigest map[string]string
	// imageEnv reads what a base image declares. See WithImageEnv.
	imageEnv ImageEnv
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
// resolveContext is given the construct's name because it serves more than one.
//
// It said "COPY" whatever asked, and a `RUN --mount=type=bind` naming a path
// outside the context was told a COPY had failed - a message that sends the
// reader to a line that has no COPY on it.
func resolveContext(what, root, src, where string) (*ir.Node, error) {
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
			"%s at %s: %s is not in the build context"+
				"\n  looked in %s", what, where, src, root)
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
		// **An artifact reference names another target's output**, which no
		// directory holds yet - so a pattern in the artifact path is not this
		// function's to expand. A pattern in the *directory* before the `+` is
		// a different thing: it names which targets, and those are directories
		// that exist now.
		if strings.Contains(src, "+") {
			refs, refErr := expandArtifactRef(root, src)
			if refErr != nil {
				return nil, refErr
			}

			out = append(out, refs...)

			continue
		}

		if !hasPattern(src) {
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

// WithSecrets declares which secrets the invocation supplied.
//
// Names, not values. The interpreter needs to know a secret exists so it can
// refuse a step asking for one nobody supplied; it needs the value for nothing,
// and not having it is what makes a value in the graph impossible rather than
// merely avoided.
func WithSecrets(secrets map[string]string) Option {
	names := make(map[string]bool, len(secrets))
	for k := range secrets {
		names[k] = true
	}

	return func(o *options) { o.secrets = names }
}

// ImageEnv is the environment an image carries, for a reference this build is
// about to start a stage from.
//
// A Dockerfile's `WORKDIR $GOPATH/src/x` reads what the *base image* set, and
// until this existed the engine knew a base image's digest and nothing else
// about it - so the variable stayed as written and the step ran in a directory
// named `$GOPATH` (E747).
//
// Returning an error is not the same as returning nothing: nothing means the
// image sets no environment, an error means this machine could not find out,
// and only the second is a reason to refuse a stage that needs it.
type ImageEnv func(ref, platform string) (map[string]string, error)

// WithImageEnv supplies Θ's neighbour: what an image declares, rather than which
// image it is.
//
// Separate from WithImageResolver because the two answer different questions and
// fail differently - a reference that cannot be resolved leaves a build unpinned
// and running, while an environment that cannot be read leaves a `WORKDIR` this
// engine cannot honour.
func WithImageEnv(fn ImageEnv) Option {
	return func(o *options) { o.imageEnv = fn }
}

// WithSecretDigests supplies a fleet-keyed digest per secret, which is what
// lets a step holding one be cached.
//
// Digests, not values - computed by the caller, which has the fleet key and the
// credentials, so this package needs neither. The rule WithSecrets states holds
// unchanged: a value cannot appear in the graph because nothing here is ever
// given one.
//
// Absent, every secret step stays uncacheable, which is the default and the
// behaviour this engine has always had.
func WithSecretDigests(digests map[string]string) Option {
	return func(o *options) { o.secretDigest = digests }
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

// WithAllowPrivileged lets a step ask for privilege it already has.
//
// `RUN --privileged` is refused by default, and rightly: a step here holds every
// capability inside its namespace and cannot reach past it, so the flag promises
// something it cannot deliver and the refusal says so.
//
// By default, though, and not for ever. This is the caller saying they know what
// the flag means here and want it accepted anyway - which is what
// `--allow-privileged` says in the reference engine, and what sixteen of this
// repository's corpus invocations pass. An engine that refuses a construct the
// operator has explicitly opted into is refusing to be used rather than refusing
// to be wrong.
func WithAllowPrivileged(on bool) Option {
	return func(o *options) { o.allowPrivileged = on }
}

// WithUnsafeUnpinnedRemoteLocally accepts a `LOCALLY` reached through a
// reference nobody pinned.
//
// **The refusal it lifts is about mutability, not about remoteness.** A
// `LOCALLY` in a fetched Earthfile runs that repository's commands on this
// machine, outside the sandbox, as you. Behind a commit hash that is a decision
// you can make once and check: the commands are fixed and you can read them
// before you name them. Behind a branch or a tag it is a decision somebody else
// can revisit after you made it, which is what the engine declines by default.
//
// Named `unsafe` because it is, and offered anyway because the caller knows
// things this engine does not - a repository they control, a network they
// trust, a build that is already running as them. An engine that refuses a
// construct the operator has explicitly opted into is refusing to be used
// rather than refusing to be wrong.
func WithUnsafeUnpinnedRemoteLocally(on bool) Option {
	return func(o *options) { o.unsafeUnpinnedRemoteLocally = on }
}

// WithPush says this build is a push, so `RUN --push` steps run.
//
// **The flag is the caller's statement about the build, not about the step.**
// `RUN --push` marks work that belongs to publishing - tagging a registry,
// posting a release - and planning it away is right for an ordinary build.
// Once the caller says this is a push, nothing about the step is special: it is
// a RUN, and it runs, in the place it was written.
//
// Not the same question as pushing an *image*: `SAVE IMAGE --push` needs a
// registry, credentials and a network, where this needs a shell. Conflating
// the two is why `tests/push.earth` ran nothing for as long as it did.
func WithPush(on bool) Option {
	return func(o *options) { o.push = on }
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

// ContextCache lets a caller planning several targets digest one tree once.
//
// **A build sees one snapshot of its context**, which is what every COPY here
// already assumes: the path is digested at graph construction, and a tree
// changing under a running build is a build whose answer was never defined.
// Sharing that snapshot between targets is the same assumption held one level
// out, and it is the caller's to make - the cache is theirs, so its lifetime is
// theirs, and a caller who does not create one gets no sharing at all.
//
// It exists because the cost is not small. Planning every target of a large
// Earthfile whose steps bind the build context digested that context once per
// target: 68% of the corpus sweep's time, and 213 seconds of it, over a tree
// that had not changed between the first target and the last.
//
// Not a package-level cache, deliberately. A long-lived process planning two
// builds minutes apart must digest twice, and a cache with no owner cannot know
// that.
type ContextCache struct {
	mu sync.Mutex
	m  map[string]*ir.Node
}

// WithContextCache shares digested context paths across builds.
//
// Safe for concurrent use, because planning several targets at once is the
// obvious reason to want it.
func WithContextCache(c *ContextCache) Option {
	return func(o *options) { o.contextCache = c }
}

// node returns the cached node for a path, and whether there was one.
func (c *ContextCache) node(key string) (*ir.Node, bool) {
	if c == nil {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	n, ok := c.m[key]

	return n, ok
}

// put records a digested path.
func (c *ContextCache) put(key string, n *ir.Node) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.m == nil {
		c.m = map[string]*ir.Node{}
	}

	c.m[key] = n
}
