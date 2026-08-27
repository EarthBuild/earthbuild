// Package interp turns an Earthfile into the engine's IR.
//
// It is the top of the four-layer architecture: it knows about Earthfile syntax
// and nothing about execution, caching or engines. Everything below it operates
// on the IR alone, which is what lets the scheduler be tested without a parser
// and the parser without a sandbox.
//
// The engine implements a subset of the language and will for some time. That
// subset is enforced *here*, before anything runs, and every construct outside
// it is refused by name (green paper I10). Silently ignoring a command would
// produce a build that is not what the Earthfile describes - which looks like a
// success.
package interp

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/util/flagutil"
	"github.com/containerd/platforms"
)

// Artifact is something the build produces.
//
// Not a step: it selects a path out of a filesystem some step already made. It
// carries the node it comes from so that a missing file can be attributed to the
// command that was supposed to create it, rather than to the target as a whole.
type Artifact struct {
	// Path inside the step's filesystem.
	Path string
	// IfExists says the build must not fail when the artifact is absent.
	//
	// An execution-time property rather than a planning one: whether the path
	// exists is not knowable until the step producing it has run.
	IfExists bool
	// LocalDest is where it is written on the host. Empty means the artifact is
	// produced but not exported - other targets may still reference it.
	LocalDest string
	// From is the step whose filesystem it is taken from.
	From *ir.Node
	// Source is where it was declared.
	Source string
	// Name is what everyone else calls it: `SAVE ARTIFACT <path> <name>`.
	//
	// The version in a filename is decided by the build - `app-*-standalone.jar`
	// - and the name is decided by the author, which is what the ENTRYPOINT two
	// lines later uses. Defaults to the file's own name.
	Name string
}

// Image is an image a target declares it produces: SAVE IMAGE.
//
// Like an artifact it is a declaration of output rather than a step - it selects
// what a target is *for* and adds nothing to the graph - and it carries the node
// whose filesystem it names, so a failure can be attributed to the command that
// was supposed to produce it.
type Image struct {
	Ref string
	// Push says the Earthfile declares this image should be published.
	//
	// A declaration, not an act: pushing happens when the *invocation* asks for
	// it, which is how the flag behaves in the tool that ships. Recording it is
	// therefore not the same as ignoring it - a build that does not push has
	// been told to push nothing.
	Push bool
	// Config is the image's configuration as it stood where the image was
	// declared.
	Config Config
	From   *ir.Node
	Source string
}

// Plan is what a target amounts to: a graph to run and the things it produces.
//
// The two are separate because they are consumed by different parts of the
// engine - the scheduler never looks at artifacts, and the exporter never looks
// at the graph - and because a build with no artifacts is a legitimate build,
// not a degenerate one.
type Plan struct {
	Graph     *ir.Graph
	Artifacts []Artifact
	// Images are the images this target declares it produces.
	Images []Image
	// Pinned is Θ's graph for this build: what each mutable reference resolved
	// to (§3.4d). Empty when nothing resolved anything, which is a build whose
	// references are as written - see WithImageResolver.
	//
	// Provenance rather than input: it is recorded so two builds can be compared
	// and a moved tag told from a changed Earthfile (B.3, B.4).
	//
	// Keyed by the reference as written. A build for two platforms resolves the
	// same tag twice, to two manifests, and records the later one here - the
	// memo behind it is keyed by the pair, so the *graph* is right either way
	// and only this record is lossy. **[GAP]** per-platform provenance.
	Pinned map[string]string

	// PinCost is how long this build spent asking registries what its mutable
	// references mean.
	//
	// Reported rather than merely measured: it is the whole of a build that has
	// nothing else to do, and the remedy is a flag the engine can name. See
	// recordPinning.
	PinCost time.Duration
	// pinned memoises Θ on (reference, platform), which is what makes it once
	// per build rather than once per use (I17).
	pinned map[string]string

	// dockerCache is the shared daemon storage the WITH DOCKER block being
	// planned right now asked for, and empty outside one. See withStatement.
	dockerCache string
	// isolateDocker is whether the WITH DOCKER block being interpreted asked for
	// a daemon of its own.
	//
	// Scoped like dockerCache and for the same reason: a `--load` opens another
	// target's blocks inside this one, so it is saved and restored rather than
	// cleared (E356).
	isolateDocker bool

	opt options
	// units are the Earthfiles this build has loaded, by directory. One build
	// spans several files, and each carries its own base recipe, functions and
	// context.
	units map[string]*unit
	// fetched memoises remote checkouts by repository and revision, so a
	// dependency named three times is cloned once.
	fetched map[string]string
	// pending are ordering edges a WAIT block left for the next step created.
	pending []*ir.Node
	// here is the Earthfile being built.
	here *unit
	// viewed memoises a bound view's object by directory and subtree.
	//
	// **A view of the whole context digests the whole context**, and a
	// Dockerfile writes `--mount=target=.` on several stages - so the corpus
	// sweep went from 39 seconds to 162 digesting one tree over and over. One
	// build sees one filesystem snapshot, which is the assumption COPY has
	// always made, so the second identical view is the first one's answer.
	viewed map[string]*ir.Node
	tree   earthfile.Tree
	// building is the chain of targets currently being resolved, used to catch
	// a cycle before it becomes a stack overflow.
	building []string
	// also collects `BUILD +other` dependencies: steps the build must run that
	// this target does not stand on.
	also []*ir.Node
	// callerGlobals are the `ARG --global` values in force where a function was
	// called. See the note where a function's state is built (E425).
	callerGlobals map[string]string
	// callerDir is the working directory in force where a function was called,
	// which the function inherits.
	callerDir string
	// callerArgs are the arguments in force where a call was made, for
	// --pass-args.
	callerArgs map[string]string
	// callerHost says the call was made from a target that runs on this machine.
	callerHost bool
	// passTo carries a BUILD --pass-args caller's arguments into the target
	// being resolved.
	passTo map[string]string
	// passPlatform carries a --platform into the target being resolved. Empty
	// means the invoking platform, which is what an unqualified reference means.
	passPlatform string
	// passPrivilege carries a reference's `--allow-privileged` into the target
	// being resolved, and `granted` is that grant while its recipe is planned.
	//
	// **The grant is per reference, which is the point of it.** A remote
	// Earthfile is not the reader's to trust, so the CLI's `--allow-privileged`
	// says "this build may use privilege", not "anything it fetches may". What
	// crosses a repository boundary is the referring line saying so:
	// `FROM --allow-privileged github.com/org/repo+privileged`.
	//
	// Hand-off rather than a parameter, for the reason passPlatform and passTo
	// are: every reference site would otherwise thread it through targetRef and
	// targetIn to reach the one line that reads it.
	passPrivilege bool
	granted       bool
	// inFunction counts how many function bodies enclose the command being
	// planned, because a function inherits the caller's build context and a
	// target does not. A count rather than a flag: a function may call one.
	inFunction int
	// resolved memoises each target's final node. Targets form a DAG: a shared
	// dependency named by three targets is one subgraph, not three.
	resolved map[string]*ir.Node
}

// Build parses an Earthfile and produces the plan for one target.
func Build(src, target string, opts ...Option) (*Plan, error) {
	var o options

	for _, f := range opts {
		f(&o)
	}

	// WithSourceMap is not optional here. Meta.Source is what correlates a step
	// across two builds, so without it every change is attributed to "graph
	// shape" rather than to the line that caused it, and the first-divergence
	// report has nothing to name.
	tree, err := earthfile.Parse("Earthfile", src, earthfile.WithSourceMap())
	if err != nil {
		return nil, fmt.Errorf("parse the Earthfile: %w", err)
	}

	p := &Plan{opt: o, tree: tree, resolved: map[string]*ir.Node{}, units: map[string]*unit{}}

	// The Earthfile handed in as text is the build's first unit, rooted at the
	// context directory: that is where its own relative references start.
	dir, err := filepath.Abs(o.context)
	if err != nil {
		return nil, fmt.Errorf("resolve the build context: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil {
		dir = resolved
	}

	here, err := newUnit(tree, dir, p.opt.versionFlags)
	if err != nil {
		return nil, err
	}

	p.here = here
	p.units[dir] = p.here

	root, err := p.target(target)
	if err != nil {
		return nil, err
	}

	if root == nil {
		// A target that only names dependencies - `all: BUILD +x BUILD +y` - has
		// no filesystem of its own, which is legitimate. So does a LOCALLY
		// target whose only line copies an artifact here: it declares an export
		// and no step, and the export is the work. It is empty only if it has
		// neither.
		if len(p.also) == 0 && len(p.Artifacts) == 0 {
			return nil, fmt.Errorf(
				"target %q has no steps"+
					"\n  a target needs a FROM, or a BUILD naming another target"+
					"\n  if its commands come from the base recipe, that recipe has no FROM either",
				target)
		}

		// The graph needs a root, and a target whose only work is an export has
		// no step of its own to be one - so the step that *produced* what is
		// exported serves. Without this the traversal starts at nothing and the
		// export names a node no scheduler ever visits.
		switch {
		case len(p.also) > 0:
			root, p.also = p.also[0], p.also[1:]

		default:
			root = p.Artifacts[0].From
		}
	}

	p.Graph = &ir.Graph{Root: root, Also: p.also}

	return p, nil
}

// target resolves a target to the node its recipe ends at.
//
// Memoised, because targets form a DAG: a dependency named by three targets is
// one subgraph, not three. Node identity would collapse the duplicates anyway -
// identical steps have identical ids - but expanding them repeatedly makes a
// deep graph exponential to build in the first place.
func (p *Plan) target(name string) (*ir.Node, error) {
	n, _, err := p.targetIn(p.here, name)

	return n, err
}

// targetIn resolves a target within a particular Earthfile.
func (p *Plan) targetIn(u *unit, name string) (*ir.Node, *state, error) {
	// Memoised on the name *and* the arguments it is being built with. A target
	// built with two different arguments is two different builds, and keying on
	// the name alone silently discarded the second - `BUILD +image --tag=one`
	// followed by `--tag=two` produced one image.
	// The grant is part of what is being asked for: the same target referenced
	// with and without `--allow-privileged` is two different requests, and
	// `reject-dedup` in the corpus exists to say so - it builds the granted one
	// first and requires the plain one to be refused afterwards.
	memo := name + "\x00" + p.passPlatform + "\x00" + canonicalArgs(p.passTo) +
		"\x00" + strconv.FormatBool(p.passPrivilege)

	if n, done := u.resolved[memo]; done {
		return n, u.ended[memo], nil
	}

	// Cycles are tracked across files: `./lib+build` depending on `..+main` is a
	// cycle even though neither Earthfile contains one on its own.
	//
	// Caught here rather than by recursing until the stack runs out, because a
	// stack overflow names nothing - least of all which two targets refer to
	// each other.
	site := u.dir + "+" + name

	for i, open := range p.building {
		if open != site {
			continue
		}

		loop := make([]string, 0, len(p.building)-i+1)
		for _, t := range append(append([]string{}, p.building[i:]...), site) {
			loop = append(loop, shortSite(t))
		}

		return nil, nil, &CycleError{Loop: loop}
	}

	// `+base` is the base recipe - the commands before the first target - and
	// not a target in the list. The name is reserved, so a reference to it can
	// only mean the implicit one.
	//
	// Compared *after* the leading `+` is stripped, because the two spellings
	// are one request everywhere else - `find` trims it for exactly that reason.
	// Matching before the trim made `FROM +base` work, where the reference
	// parser had already removed it, and `earth +base` report that no such
	// target exists while `earth ls` listed it.
	if strings.TrimPrefix(name, "+") == earthfile.TargetBase {
		n, baseState, err := p.baseRecipe(u)
		if err != nil {
			return nil, nil, err
		}

		if n == nil {
			return nil, nil, fmt.Errorf(
				"%s sets no base image before its first target, so +base names nothing"+
					"\n  a base recipe is the commands before the first target",
				filepath.Join(u.dir, "Earthfile"))
		}

		u.resolved[memo] = n

		return n, baseState, nil
	}

	t, err := find(u.tree, name)
	if err != nil {
		return nil, nil, err
	}

	p.building = append(p.building, site)

	// Every target starts from the base recipe - the commands before the first
	// target - which is what `FROM alpine` at the top of a file means. Ignoring
	// it made every target in such a file look like it had no base image, and
	// that is a hundred of the refusals across this repository's own Earthfiles.
	base, baseState, err := p.baseRecipe(u)
	if err != nil {
		return nil, nil, err
	}

	// forTarget rather than clone: a target inherits the base recipe's globals
	// and not its local arguments (E438).
	rs := baseState.forTarget()
	rs.supplied = p.opt.args
	rs.target = name

	// A platform travels with the resolution and applies to every step of the
	// target, which is what makes the same target on two architectures two
	// builds rather than one.
	if p.passPlatform != "" {
		rs.platform = p.passPlatform
		p.passPlatform = ""
	}

	// Taken here and restored below, so the grant covers this target's recipe
	// and everything it builds, and stops at the end of it.
	prevGrant := p.granted
	if p.passPrivilege {
		p.granted = true
		p.passPrivilege = false
	}

	if len(p.passTo) > 0 {
		merged := map[string]string{}
		maps.Copy(merged, p.passTo)

		maps.Copy(merged, p.opt.args)

		rs.supplied = merged
		p.passTo = nil
	}

	// Everything in a recipe resolves against its own Earthfile's directory.
	//
	// **Including the function count.** A target reached *from* a function is
	// not inside one: a function is inlined into its caller and borrows the
	// caller's context, where a target is a unit of its own and brings its own.
	// Left set, `FROM hello-world+hello` inside a fetched function had that
	// repository's target read `globe.txt` from the *invoking* project - see
	// callerContext, and tests/import.earth+test-command-import.
	prevUnit, prevFn := p.here, p.inFunction
	p.here, p.inFunction = u, 0

	root, err := p.block(t.Recipe, base, rs)

	p.here, p.inFunction = prevUnit, prevFn
	p.granted = prevGrant
	p.building = p.building[:len(p.building)-1]

	if err != nil {
		return nil, nil, err
	}

	u.resolved[memo] = root

	// The state the recipe *ended* in, which is what `FROM +target` continues
	// from. A target that sets WORKDIR and ENV and nothing else is the
	// commonest shape in the corpus and exists precisely so what builds on it
	// inherits that setup; taking the layers and dropping the rest gives a
	// filesystem that looks right and a build that runs in the wrong directory
	// with none of its variables (E32).
	if u.ended == nil {
		u.ended = map[string]*state{}
	}

	u.ended[memo] = rs

	return root, rs, nil
}

// shortSite renders a cycle entry as a reader would write it.
func shortSite(site string) string {
	if i := strings.LastIndex(site, "+"); i >= 0 {
		return "+" + site[i+1:]
	}

	return site
}

// canonicalArgs renders a set of arguments so two identical sets memoise
// together and two different ones do not. Sorted, because map order is not part
// of what was asked for.
func canonicalArgs(args map[string]string) string {
	if len(args) == 0 {
		return ""
	}

	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var b strings.Builder

	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\x00", k, args[k])
	}

	return b.String()
}

// CycleError reports targets that depend on each other.
//
// Typed so the frames above it can leave it alone: a cycle is the same fact at
// every level of the recursion, and wrapping it once per hop buries the loop
// itself under the path that found it.
type CycleError struct{ Loop []string }

func (e *CycleError) Error() string {
	return "cycle between targets: " + strings.Join(e.Loop, " -> ") +
		"\n  a target cannot depend on itself, directly or through others"
}

// targetRef resolves a reference that may name a target in another Earthfile.
func (p *Plan) targetRef(s, where string) (*ir.Node, *state, error) {
	ref, err := parseRef(s, where, p.here.imports)
	if err != nil {
		return nil, nil, err
	}

	u, err := p.resolve(p.here, ref)
	if err != nil {
		return nil, nil, err
	}

	return p.targetIn(u, ref.name)
}

// find locates a target, listing what does exist when it does not.
//
// `+main` and `main` are the same request. The leading `+` is the notation
// everywhere else - an Earthfile refers to its own targets as `+target`, the
// documentation writes `earth +build`, and so does every CI script - so
// accepting only the bare name refused the first thing anyone types, with a
// message listing `main` as though they had misspelt it.
func find(tree earthfile.Tree, name string) (earthfile.Target, error) {
	name = strings.TrimPrefix(name, "+")

	names := make([]string, 0, len(tree.Targets))

	for _, t := range tree.Targets {
		if t.Name == name {
			return t, nil
		}

		names = append(names, t.Name)
	}

	sort.Strings(names)

	// A wildcard is a feature, not a typo.
	//
	// `COPY +sub*/out.txt` asks for every target whose name matches, which is
	// what `VERSION --wildcard-copy` and `--wildcard-builds` enable. This engine
	// does not expand them - and said so by looking the name up literally and
	// reporting that no target is called `sub*`, which is true and sends an
	// author hunting for a typo in a name they wrote correctly (E412).
	if strings.ContainsAny(name, "*?[") {
		return earthfile.Target{}, fmt.Errorf(
			"%w: a wildcard target reference (%q) expands to every matching target,"+
				"\n  which is the VERSION --wildcard-copy and --wildcard-builds"+
				"\n  feature; this engine does not expand one"+
				"\n  name the targets individually, or build this with the buildkit engine",
			ErrUnimplemented, name)
	}

	if len(names) == 0 {
		return earthfile.Target{}, fmt.Errorf(
			"%w: this Earthfile defines no targets, so %q cannot be built", errNoSuchTarget, name)
	}

	return earthfile.Target{}, fmt.Errorf(
		"%w: no target named %q\n  this Earthfile defines: %s",
		errNoSuchTarget, name, strings.Join(names, ", "))
}

// block folds a recipe into a chain, each command taking the state before it.
func (p *Plan) block(b earthfile.Block, prev *ir.Node, st *state) (*ir.Node, error) {
	for _, s := range b {
		n, err := p.statement(s, prev, st)
		if err != nil {
			return nil, err
		}

		// A WAIT block leaves ordering edges for whatever comes *next*, and next
		// is here. Attaching them to the block's own exit instead would put them
		// on a node that precedes the block - for a block containing only a
		// BUILD there is no new node at all - and making the step before wait
		// for work that stands on it is a cycle, not an ordering.
		if n != nil && n != prev && len(p.pending) > 0 {
			n.After = append(n.After, p.pending...)
			p.pending = nil
		}

		prev = n
	}

	return prev, nil
}

func (p *Plan) statement(st earthfile.Statement, prev *ir.Node, rs *state) (*ir.Node, error) {
	// Control flow is refused rather than approximated. IF and FOR make the
	// graph depend on values that are not known until part of it has run, which
	// the scheduler has no representation for yet; guessing a branch would build
	// the wrong one silently.
	switch {
	case st.If != nil:
		return p.ifStatement(st.If, prev, rs)
	case st.For != nil:
		return p.forStatement(st.For, prev, rs)
	case st.With != nil:
		return p.withStatement(st.With, prev, rs)
	case st.Try != nil:
		return p.tryStatement(st.Try, prev, rs)
	case st.Wait != nil:
		return p.waitStatement(st.Wait, prev, rs)
	case st.Command == nil:
		return prev, nil
	}

	return p.command(*st.Command, prev, rs)
}

// arrival names the milestone at which a command becomes available, so a
// refusal says when rather than only that.
var arrival = map[earthfile.Cmd]string{
	earthfile.CmdSaveArtifact: "M2",
}

// withProtocol gives a port the protocol an OCI configuration expects.
//
// `EXPOSE 8080` means tcp, and the configuration spells that `8080/tcp`. Docker
// itself normalises on the way in, so an image that skips it is the odd one out
// rather than the concise one.
func withProtocol(port string) string {
	if strings.Contains(port, "/") {
		return port
	}

	return port + "/tcp"
}

// secretDigestFor maps the sources a step draws on to their fleet-keyed digests.
//
// Nil - and so an uncacheable step - unless every source has one. A partial map
// would key some of what the step depends on and not the rest, which is worse
// than not keying at all: the entry would answer for a build supplying a
// different value for the secret that was left out.
func (p *Plan) secretDigestFor(specs []string) map[string]string {
	if len(specs) == 0 || len(p.opt.secretDigest) == 0 {
		return nil
	}

	out := make(map[string]string, len(specs))

	for _, spec := range specs {
		// The same reading as the check above: `NAME=SOURCE` draws on SOURCE,
		// a bare `NAME` on a secret of that name.
		_, source, ok := strings.Cut(spec, "=")
		if !ok {
			source = spec
		}

		source = ir.SecretName(source)
		if source == "" {
			// Deliberately supplying nothing, which the check above allows.
			continue
		}

		digest, ok := p.opt.secretDigest[source]
		if !ok {
			return nil
		}

		out[source] = digest
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func (p *Plan) command(c earthfile.Command, prev *ir.Node, rs *state) (*ir.Node, error) {
	// ARG declares; everything after it sees the value. Expansion happens here,
	// before the node exists, so an argument's value is part of the operation
	// and therefore part of its key - an argument that changed what a step did
	// without changing its key would be a false hit.
	if c.Name == earthfile.CmdArg {
		where := loc(c.SourceLocation)

		expand := func(v string) (string, error) {
			// expandWord, not expandValue: what is inside a `$(...)` is read by a
			// shell, so its quoting is not this engine's to resolve (E65).
			return p.expandCommands(rs.args.expandWord(v), prev, rs.dir, string(c.Name), where)
		}

		// The platform arguments are computed here rather than seeded into the
		// scope, because they must reach a declaration and nothing else: an
		// undeclared `$TARGETARCH` expands to nothing in the reference, and an
		// engine that filled it in would change what an Earthfile means.
		builtin := builtinArgs(p.targetPlatform(rs), p.opt.nativePlatform(),
			rs.target, p.here.dir, rs.host, p.opt.push)

		// One builtin is gated on the dialect rather than always present, and is
		// added here rather than inside builtinArgs: the file this comes from
		// asserts the *absence* too, and a fifth parameter would be a signature
		// growing one field per fact (E472).
		if p.here.features.ciRunner {
			addCIRunner(builtin)
		}

		err := rs.args.declare(c.Args, rs.supplied, where, expand,
			builtin, rs.globals, rs.declared, rs.target != "")
		if err != nil {
			return nil, err
		}

		return prev, nil
	}

	c = c.Clone()

	// A command line is re-parsed by a shell, so its quoting is preserved;
	// everything else is a value this engine consumes, so its quoting is
	// resolved. Using one rule for both silently changed what RUN executed.
	expand := rs.args.expandValue
	if c.Name == earthfile.CmdRun || c.Name == earthfile.CmdEntrypoint || c.Name == earthfile.CmdCmd {
		// **A secret shadows a build argument of the same name**, for the
		// length of the command that asked for it. `ARG foo = bacon` then
		// `RUN --secret foo test "$foo" == "eggs"` must hand the shell `$foo`
		// unexpanded, because the shell is the only thing holding the secret;
		// expanded here it ran `test "bacon" == "eggs"` and the secret it was
		// given was never read.
		expand = rs.args.withoutSecrets(secretNamesIn(c.Args)).expandWord
	}

	// **By region, not by command.** An argument may be a value that *contains*
	// a command line, and `$(...)` is exactly that: what is inside goes to a
	// shell, so it keeps its quoting whatever the command around it is.
	for i, a := range c.Args {
		c.Args[i] = expandByRegion(a, expand, rs.args.expandWord)
	}

	// A `$(...)` in a value the *engine* consumes has no shell to expand it, so
	// the engine must: `SAVE IMAGE app:$(cat version)` was producing a reference
	// containing that text. A RUN command is excluded by the same reasoning
	// rather than despite it - it is handed to a shell, whose job this is, and
	// running it here would evaluate it once at plan time and bake the answer
	// in, so a step reading the clock would see the wrong moment.
	if c.Name != earthfile.CmdRun && c.Name != earthfile.CmdEntrypoint && c.Name != earthfile.CmdCmd {
		for i, a := range c.Args {
			if !strings.Contains(a, "$(") {
				continue
			}

			out, err := p.expandCommands(a, prev, rs.dir, string(c.Name), loc(c.SourceLocation))
			if err != nil {
				return nil, err
			}

			c.Args[i] = out
		}
	}

	switch c.Name {
	case earthfile.CmdFromDockerfile:
		// A separate command in the grammar, not a FROM with a first argument:
		// the lexer gives `FROM DOCKERFILE` its own token, and a check inside
		// the FROM branch was never reached.
		return p.fromDockerfile(c, prev, rs)

	case earthfile.CmdFrom:
		if rs.host {
			// Half a target on the host and half in a sandbox is two targets
			// wearing one name, and the cache rules differ between them.
			return nil, fmt.Errorf(
				"FROM at %s follows a LOCALLY"+
					"\n  a target runs either on this machine or in a sandbox, not both",
				loc(c.SourceLocation))
		}

		if len(c.Args) == 0 {
			return nil, fmt.Errorf("FROM needs an image reference (%s)", loc(c.SourceLocation))
		}

		// `FROM +other` or `FROM ./lib+base`: this target continues from another
		// target's final filesystem, so that target's steps become part of this
		// graph rather than a separate build.
		// `from-args = image-name / *( from-option WSP ) target-ref
		// *( WSP build-arg-override )`.
		from, err := fromTarget(c)
		if err != nil {
			return nil, err
		}

		if from.ref != "" {
			if from.platform != "" {
				err := checkPlatform(from.platform, loc(c.SourceLocation))
				if err != nil {
					return nil, err
				}

				p.passPlatform = from.platform
			}

			// The line granting privilege is this one, so the grant travels
			// with the reference it is written on.
			p.passPrivilege = from.allowPrivileged || p.grantedByImport(from.ref)

			pass := map[string]string{}

			if from.passArgs {
				err := p.here.features.needs(
					p.here.features.passArgs, "FROM --pass-args", "--pass-args",
					loc(c.SourceLocation))
				if err != nil {
					return nil, err
				}

				maps.Copy(pass, rs.args)
			}

			maps.Copy(pass, from.args)

			p.passTo = pass

			n, ended, err := p.targetRef(from.ref, loc(c.SourceLocation))
			if err != nil {
				return nil, wrapRef("FROM", c, err)
			}

			// Continue from the target's *state*, not only its filesystem.
			//
			// A base target that sets WORKDIR and ENV and nothing else is the
			// commonest shape in the corpus - every `part5` tutorial has one -
			// and it exists precisely so that what builds on it inherits that
			// setup. Taking the layers and dropping the rest gives a filesystem
			// that looks right and a build that runs in the wrong directory
			// with none of its variables. The reference answers `green` and
			// `/w` where this engine answered `` and `/` (E32).
			//
			// The directory, the environment, the user and the image
			// configuration - and deliberately not the arguments. An ARG
			// belongs to the recipe that declared it, and a value supplied to
			// one target was not supplied to this one.
			if ended != nil {
				rs.dir = ended.dir
				rs.user = ended.user
				rs.env = maps.Clone(ended.env)
				rs.cfg = ended.cfg.clone()
			}
			return n, nil
		}

		image := from.image
		if image == "" {
			return nil, fmt.Errorf("FROM needs an image reference (%s)", loc(c.SourceLocation))
		}

		// `FROM --platform=x <image>` sets the platform for this target, the
		// same as it does for a referenced one: it decides which image is
		// pulled and what every step after it runs on.
		if from.platform != "" {
			err := checkPlatform(from.platform, loc(c.SourceLocation))
			if err != nil {
				return nil, err
			}

			rs.platform = from.platform
		}

		// `scratch` is the reserved name for no base at all, and this engine
		// was asking a registry for it: three corpus targets failed with a 404
		// on `library/scratch`, which reads as a network problem and sends the
		// reader to the registry rather than to the line they wrote (E468).
		//
		// Bare, because that is how every reader of these files has it:
		// `registry.example/scratch` is an ordinary reference and treating it as
		// the empty base would be a rule nobody else has.
		if image == "scratch" {
			// An empty configuration, which is what `scratch` *is*: no working
			// directory, and a relative path after it has nothing to resolve
			// against (E471).
			//
			// Scratch is the one image whose configuration is known without
			// fetching it, which is what makes this a rule the interpreter can
			// apply. `FROM alpine` after a WORKDIR asks the same question and
			// needs the image's config at planning time to answer it.
			rs.dir = ""

			return &ir.Node{
				Platform: platformOf(rs.platform),
				Op:       ir.Op{Kind: ir.OpScratch},
				Meta: ir.Meta{
					Source: loc(c.SourceLocation), Description: "FROM scratch",
				},
			}, nil
		}

		// Pinned before it reaches the graph, so the digest and not the tag is
		// what every key downstream is derived from (§3.4d, I3). The description
		// keeps the reference as written: that is what the Earthfile says and
		// what a reader is looking for.
		return &ir.Node{
			Platform: platformOf(rs.platform),
			Op:       ir.Op{Kind: ir.OpImage, Args: []string{p.pin(image, rs.platform)}},
			Meta:     ir.Meta{Source: loc(c.SourceLocation), Description: "FROM " + image},
		}, nil

	case earthfile.CmdRun:
		// **The opt-in does not cross a repository boundary.** A caller saying
		// they want privilege is saying it about the build they wrote, not
		// about whatever a fetched Earthfile turns out to contain; granting it
		// there would let a remote target take privilege the operator never
		// considered. The reference engine requires it be granted again, at the
		// FROM or IMPORT that reaches out, and the corpus asserts the refusal
		// in five places.
		// Either the operator opted in for a file this build owns, or the line
		// that referred to this one granted it outright.
		allowHere := p.granted ||
			(p.opt.allowPrivileged && p.here.fetchedFrom == "")

		rf, err := runFlags(c, rs.env, rs.dir, p.opt.terminal, allowHere)
		if err != nil {
			return nil, err
		}

		// `RUN --push` runs only when the build is invoked in push mode, and
		// this engine has none - so the step is not planned, and the commands
		// after it stand on the filesystem as it was before it, which is where
		// the reference leaves them too.
		//
		// Returning the previous node rather than a node that does nothing: an
		// empty step would still be a step, with a key, a record and a line in
		// the output saying it ran.
		//
		// It was neither refused nor planned away before this, because the flag
		// was parsed and dropped - so `RUN --push ./publish.sh` ran on every
		// build, which is precisely what the option exists to prevent (E436).
		// Unless the caller said this build is a push, in which case the step
		// is an ordinary RUN and runs where it stands.
		if rf.pushOnly && !p.opt.push {
			return prev, nil
		}

		// A secret nobody supplied is refused here rather than run with an empty
		// file: the command would fail somewhere far from the line that asked
		// for the credential, usually with a message about authentication that
		// sends the reader to the wrong system entirely.
		// `--secret NAME=SOURCE` takes the value from SOURCE; `--secret NAME`
		// from a secret of the same name. Refused by the name it would have
		// come from, which is the one the caller has to supply.
		for _, spec := range rf.secrets {
			_, source, ok := strings.Cut(spec, "=")
			if !ok {
				source = spec
			}

			// **The prefix names where a secret lives, not what it is
			// called.** `+secrets/TOKEN` is a project store's TOKEN, and an
			// engine with no project store still has whatever the caller
			// passed - refusing that over the spelling is a refusal about
			// nothing. `tests/secrets.earth` supplies SECRET1 and then asks
			// for `+secrets/SECRET1`, which is the same secret twice.
			source = ir.SecretName(source)

			// **An empty source supplies nothing, and that is allowed.**
			// `ARG SECRET_ID=+secrets/SECRET1` overridden with
			// `--build-arg SECRET_ID=""` makes the source empty on purpose,
			// and `tests/secrets.earth` asserts the variable is then empty
			// *and the build carries on*. Refusing it demands a secret the
			// author deliberately removed.
			if source == "" {
				continue
			}

			if !p.opt.secrets[source] {
				// ErrNotProvided: the third place the rule holds, after a
				// probe to run and a repository to fetch. A secret arrives
				// from the invocation, so an Earthfile that declares one it
				// never receives is valid input given incompletely - see
				// TestAnUnsuppliedSecretIsAWithheldValue.
				return nil, fmt.Errorf(
					"RUN at %s needs the secret %q, which was not supplied"+
						"\n  pass it with --secret %s=<value>: %w",
					loc(c.SourceLocation), source, source, ErrNotProvided)
			}
		}

		// **A capability, so the file has to ask.** `RUN --aws` hands the
		// invoking user's credentials to a step; a file that uses it without
		// declaring the feature is refused rather than quietly given them.
		if rf.aws {
			err := p.here.features.needs(p.here.features.runWithAWS,
				"RUN --aws", "--run-with-aws", loc(c.SourceLocation))
			if err != nil {
				return nil, err
			}
		}

		for _, m := range rf.mounts {
			// The same two names for one secret, reaching the other line that
			// looks one up.
			m.ID = ir.SecretName(m.ID)

			if m.Secret && m.ID != "" && !p.opt.secrets[m.ID] {
				// Same family as the flag spelling above: one condition must
				// not classify two ways depending on how it was written.
				return nil, fmt.Errorf(
					"RUN at %s needs the secret %q, which was not supplied"+
						"\n  pass it with --secret %s=<value>: %w",
					loc(c.SourceLocation), m.ID, m.ID, ErrNotProvided)
			}
		}

		if rs.host {
			// A host step needs no base: it runs on a machine that already
			// exists. Requiring FROM would refuse an entire class of legitimate
			// target.
			//
			// NoCache is not recorded here: a host step is never cached anyway
			// (I7), so the flag asks for what it already gets.
			n := &ir.Node{
				// A LOCALLY step has no image, so it has no entrypoint to run:
				// `--entrypoint` there names something that does not exist.
				Op:   ir.Op{Kind: ir.OpHost, Args: runArgv(c, rf.rest, false), Dir: rs.dir, Env: rs.envFor()},
				Meta: ir.Meta{Source: loc(c.SourceLocation), Description: "RUN " + strings.Join(c.Args, " ")},
			}

			if prev != nil {
				n.Inputs = []*ir.Node{prev}
			}

			return n, nil
		}

		if prev == nil {
			return nil, fmt.Errorf(
				"RUN at %s has no base image\n  a target begins with FROM, which gives its commands a filesystem",
				loc(c.SourceLocation))
		}

		// A bound view's ν is resolved here, where the context root and the
		// graph are. `mounts` is copied first because the resolution writes
		// From into it, and rf's slice is the caller's (§3.3d).
		mounts := append(append([]ir.Mount{}, rs.mounts...), rf.mounts...)

		views, err := p.resolveViews(mounts[len(rs.mounts):], rf.views, rs, loc(c.SourceLocation))
		if err != nil {
			return nil, err
		}

		// **`--entrypoint` when this build declared one.** The executor reads the
		// entrypoint from the materialised base's declaration, which is right
		// for a fetched image and wrong for one this build wrote: `ENTRYPOINT`
		// lands in the interpreter's config and never reaches that declaration,
		// so `FROM DOCKERFILE` over a Dockerfile declaring an entrypoint failed
		// with "alpine declares no entrypoint to run"
		// (tests/gen-dockerfile.earth).
		//
		// Resolved here when it is known here, and the flag cleared so the
		// executor does not prepend a second copy. The argv is then in the
		// step's key, which is where an input belongs.
		argv, fromImage := runArgv(c, rf.rest, rf.entrypoint), rf.entrypoint
		if rf.entrypoint && len(rs.cfg.Entrypoint) > 0 {
			argv, fromImage = append(append([]string{}, rs.cfg.Entrypoint...), argv...), false
		}

		// Computed once, and only from names the caller already resolved: this
		// package holds no credential to derive it from.
		secretDigest := p.secretDigestFor(rf.secrets)

		return &ir.Node{
			Op: ir.Op{
				Kind: ir.OpExec, Args: argv, Entrypoint: fromImage,
				NoNetwork:   rf.noNet,
				Interactive: rf.interactive,
				SSH:         rf.ssh,
				// **A cache mount is an accelerator, and is cached.**
				//
				// It was not, on the grounds that what a step produces may
				// depend on what was in the mount, which no key describes. True,
				// and equally true of `RUN curl https://…` - which this engine
				// caches without hesitation. Refusing the local directory while
				// permitting the internet was an inconsistency rather than a
				// principle, and its cost was the opposite of what `CACHE` is
				// for: adding one made every rebuild slower (E424).
				//
				// What the construct promises is that a cold cache gives the
				// same result, slower. A step needing the contents to be
				// *correct* relies on something never promised, and relies on it
				// across builds already.
				//
				// `--persist` is the exception and is real: it copies the mount's
				// contents into the image, so they are part of what the step
				// produces rather than something beside it.
				// `rf.aws` for the reason `rf.secrets` is here: the credentials
				// are not in the key and must not be, so a step that ran with
				// one set cannot answer for a step asking with another.
				//
				// A fleet key changes that for `rf.secrets` and only for it: a
				// keyed digest of each value goes into the key, so the step
				// *can* say which credential it ran with. AWS keeps the old
				// rule - its session tokens are reissued constantly, so keying
				// on them would miss every time and fill the cache doing it.
				NoCache: rf.noCache || uncacheable(rs.mounts) || uncacheable(rf.mounts) ||
					(len(rf.secrets) > 0 && secretDigest == nil) || rf.aws,
				Mounts:       mounts,
				SecretEnv:    rf.secrets,
				SecretDigest: secretDigest,
				AWS:          rf.aws,
				// What this step resolves names by. Carried like the mounts and
				// hashed like them, because it changes what the command does
				// rather than where it runs.
				Hosts: slices.Clone(rs.hosts),
				Dir:   rs.dir, User: rs.user, Env: rs.envFor(),
			},
			Platform: platformOf(rs.platform),
			Inputs:   []*ir.Node{prev},
			// What the step reads without standing on: exactly what a source is
			// for, and what puts the object in the key and builds it first.
			Sources: views,
			Meta:    ir.Meta{Source: loc(c.SourceLocation), Description: "RUN " + strings.Join(c.Args, " ")},
		}, nil

	case earthfile.CmdCopy:
		if rs.host {
			// `COPY +target/artifact <path>` puts a built artifact on this
			// machine, which is what a LOCALLY target is for and is every use
			// of COPY inside one in this repository.
			//
			// Copying the *context* is still refused: the file is already here,
			// at the path the line names, so the copy is from a directory to
			// itself and silently doing nothing would be worse than saying so.
			return p.localCopy(c, prev, rs)
		}

		if prev == nil {
			return nil, fmt.Errorf(
				"COPY at %s has no filesystem to copy into\n  a target begins with FROM",
				loc(c.SourceLocation))
		}

		return p.copy(c, prev, rs)

	case earthfile.CmdFunction, earthfile.CmdCommand:
		// `COMMAND` is the spelling this keyword had before it was renamed, and
		// a file that asked for the new one may not use the old. The gate runs
		// the opposite way from the rest - the flag makes something *illegal* -
		// because it renames rather than adds, and accepting both everywhere is
		// what makes an Earthfile build here and nowhere else (E458).
		if c.Name == earthfile.CmdCommand && p.here.features.functionKeyword {
			return nil, fmt.Errorf(
				"COMMAND at %s is the old spelling of FUNCTION, and this file's"+
					" dialect has the new one"+
					"\n  write FUNCTION, or declare an older VERSION",
				loc(c.SourceLocation))
		}

		// And the mirror: before the rename there is no FUNCTION, so a file at
		// an older version writing one is using a keyword its dialect does not
		// have. `tests/command.earth` is that file and expects to be refused
		// (E459).
		if c.Name == earthfile.CmdFunction && !p.here.features.functionKeyword {
			return nil, fmt.Errorf(
				"FUNCTION at %s is the new spelling of COMMAND, and this file's"+
					" dialect has the old one"+
					"\n  write COMMAND, or declare VERSION 0.8",
				loc(c.SourceLocation))
		}

		// The marker that makes a block a function rather than a target. It
		// declares nothing and produces nothing.
		//
		// COMMAND is what FUNCTION was called before it was renamed, and the
		// parser deliberately keeps them apart so a diagnostic can quote the
		// word the author wrote. Here they mean the same thing, and knowing only
		// the newer one turned an Earthfile away for using the older spelling.
		return prev, nil

	case earthfile.CmdDo:
		// **Not checked here, because the function may bring one.** A function
		// is inlined into its caller, so a function beginning with `FROM` is
		// how a target beginning with `DO` gets its base -
		// `import.earth+test-command-import` is one line and does exactly that.
		// Refusing before the function is read was true at that instant and not
		// at the next.
		//
		// A host target has no base and needs none: its commands run on a
		// machine that already exists. The check below covers both, and keeps
		// the diagnostic for the case it was written for - a function that
		// establishes nothing still has no filesystem, and still says so.

		p.callerDir, p.callerArgs, p.callerHost = rs.dir, rs.args, rs.host
		p.callerGlobals = rs.globals

		out, doErr := p.do(c, prev, rs)
		if doErr != nil {
			return nil, doErr
		}

		// The check the early one became: a function that established nothing
		// leaves the target where it started, and there is still nothing for
		// its commands to run in.
		if out == nil && !rs.host {
			return nil, fmt.Errorf(
				"DO at %s has no filesystem to run in"+
					"\n  a target begins with FROM, which gives its commands a"+
					" filesystem - or calls a function that does",
				loc(c.SourceLocation))
		}

		return out, nil

	case earthfile.CmdImport:
		name, path, grant, err := importParts(c.Args)
		if err != nil {
			return nil, fmt.Errorf("%w (%s)", err, loc(c.SourceLocation))
		}

		// A remote import is recorded, not resolved: an import is only a name
		// for a reference, so `IMPORT github.com/org/repo AS lib` must mean
		// exactly what writing `github.com/org/repo+t` out in full means -
		// including being refused in the same way when there is no fetcher.
		// Fetching here instead would clone a repository for an alias nothing
		// in the file goes on to use.
		p.here.imports[name] = path

		// **The grant belongs to the name, not to one use of it.** Every
		// reference through this alias inherits it, which is why
		// `allow-privileged-import.earth` writes the flag once on the IMPORT
		// and nothing on the COPY that follows.
		if grant {
			p.here.grants[name] = true
		}

		return prev, nil

	case earthfile.CmdEntrypoint:
		rs.cfg.Entrypoint = argvOf(c)

		return prev, nil

	case earthfile.CmdCmd:
		rs.cfg.Cmd = argvOf(c)

		return prev, nil

	case earthfile.CmdExpose:
		// Normalised here so both writers get it right and the key sees one
		// spelling: `EXPOSE 8080` and `EXPOSE 8080/tcp` declare the same image,
		// and an OCI configuration says so as `8080/tcp`. Stored raw, the saved
		// image had `{"8080":{}}` where every other tool writes `{"8080/tcp":{}}`
		// - which docker accepts and nothing else recognises as a tcp port.
		for _, p := range c.Args {
			rs.cfg.Exposed = append(rs.cfg.Exposed, withProtocol(p))
		}

		return prev, nil

	case earthfile.CmdVolume:
		rs.cfg.Volumes = append(rs.cfg.Volumes, c.Args...)

		return prev, nil

	case earthfile.CmdStopSignal:
		sig, err := stopSignal(c.Args, loc(c.SourceLocation))
		if err != nil {
			return nil, err
		}

		rs.cfg.StopSignal = sig

		return prev, nil

	case earthfile.CmdHealthCheck:
		hc, err := readHealthcheck(c.Args, loc(c.SourceLocation))
		if err != nil {
			return nil, err
		}

		rs.cfg.Healthcheck = hc

		return prev, nil

	case earthfile.CmdLabel:
		k, v, err := label(c.Args)
		if err != nil {
			return nil, fmt.Errorf("%w (%s)", err, loc(c.SourceLocation))
		}

		err = refuseReservedLabel(k, loc(c.SourceLocation))
		if err != nil {
			return nil, err
		}

		rs.cfg.Labels[k] = v

		return prev, nil

	case earthfile.CmdUser:
		if len(c.Args) == 0 {
			return nil, fmt.Errorf("USER needs an account (%s)", loc(c.SourceLocation))
		}

		// Unlike the rest of the image configuration this changes what later
		// steps *do*, so it travels on the operation as well.
		rs.user = c.Args[0]

		return prev, nil

	case earthfile.CmdLocally:
		// Not from somewhere else. `LOCALLY` runs commands on the invoking
		// machine outside any sandbox, which in an Earthfile you wrote is a
		// choice you made - and in one fetched from a repository is a command
		// chosen by whoever can push there, running as you (green paper §5.3).
		//
		// The engine fetched and built these. `tests/allow-privileged.earth`
		// says so in its own words, and the RUN it says must never run was
		// reached (E439).
		if p.here.fetchedFrom != "" && p.here.reachedUnpinned &&
			!p.opt.unsafeUnpinnedRemoteLocally {
			// **A position, and it says so.** This is not a construct the engine
			// has yet to build - `LOCALLY` works, and works in the Earthfile in
			// front of you. It is one it declines for whoever wrote *this* file,
			// which is the third of the three refusals and the one that promises
			// nothing (see refusedOnPurpose).
			//
			// Said in the taxonomy's words rather than in its own, because a
			// deliberate refusal that does not declare itself reads as a defect:
			// the corpus counted this one as a gap while three sibling targets
			// refusing privileged remotes counted as decisions, the whole
			// difference being that those say "on purpose".
			return nil, refusedOnPurpose(
				"LOCALLY in an unpinned Earthfile fetched from "+p.here.fetchedFrom,
				loc(c.SourceLocation),
				"it would run that repository's commands on this machine, outside"+
					" the sandbox, as you, and nothing here is pinned to a commit -"+
					" so what runs is whatever that repository says later, chosen by"+
					" whoever can push to it"+
					"\n  name a commit (`repo:<40-hex>+target`) and this is"+
					" allowed: the commands are then fixed and you can read them"+
					" before you name them"+
					"\n  every link has to be pinned, because a pinned repository"+
					" that imports an unpinned one has moved the choice rather than"+
					" removed it"+
					"\n  --unsafe-allow-unpinned-remote-locally accepts it anyway,"+
					" for a caller who knows the repository better than this engine"+
					" does")
		}

		// Everything after this runs on the invoking machine. The specification
		// calls it `host` and distinguishes it throughout: unsandboxed,
		// non-cacheable, never retried (I7). Those are one fact - nothing bounds
		// what it observed - stated three ways.
		rs.host = true

		// **And the working directory does not follow it here.** A WORKDIR set
		// before this names a path inside the container; carried across, it had
		// a host step asked to `chdir test` for a container's `/test`
		// (tests/if.earth+test-switch-locally). The machine's build starts in
		// the directory holding the Earthfile, which is what makes a WORKDIR
		// written *after* LOCALLY relative to it.
		rs.dir = ""

		return prev, nil

	case earthfile.CmdLet, earthfile.CmdSet:
		if c.Name == earthfile.CmdSet {
			err := p.here.features.needs(p.here.features.argScopeAndSet,
				"SET", "--arg-scope-and-set", loc(c.SourceLocation))
			if err != nil {
				return nil, err
			}
		}

		name, value, err := p.assignment(c, prev, rs.dir)
		if err != nil {
			return nil, err
		}

		// LET introduces, SET updates. Treating SET as a declaration would make
		// a typo in a variable name silently create a second variable: the
		// original keeps its old value while the author believes it changed.
		if c.Name == earthfile.CmdSet {
			if _, declared := rs.args[name]; !declared {
				return nil, fmt.Errorf(
					"SET %s at %s, but it was never declared"+
						"\n  introduce it with LET, or check the spelling",
					name, loc(c.SourceLocation))
			}

			// **An ARG is the target's interface, not its state.** A caller may
			// override one, and a build reading the same Earthfile with
			// different arguments is a different build - so writing to one from
			// inside would make its value depend on where in the recipe you
			// looked. `tests/arg-set.earth` exists to be refused, and pins this
			// wording as well as the refusal.
			//
			// `declared` rather than `args`: it is the names this recipe has an
			// ARG line for, which is the case the corpus states. A name
			// inherited as an argument from elsewhere is left alone until
			// something says what the reference does with it.
			// Keyed by scope, as `declare` writes them: a name is an ARG here
			// whether it was declared local to the recipe or global to the file.
			if rs.declared["local:"+name] || rs.declared["global:"+name] {
				// A plain error, not ErrRefused: this is an Earthfile that is
				// wrong rather than a construct this engine declines, and the
				// two are counted apart.
				return nil, fmt.Errorf(
					"SET %[1]s at %[2]s cannot be done"+
						"\n  Hint: '%[1]s' is an ARG and cannot be used with SET"+
						" - try declaring `LET %[1]s = $%[1]s` first",
					name, loc(c.SourceLocation))
			}
		}

		// **LET re-introduces the name.** `ARG foo = sports` then
		// `LET foo = ${foo}` makes `foo` a variable of this recipe rather than
		// its interface, which is precisely what the refusal above tells the
		// author to write - so the ARG-ness has to go, or the rule refuses its
		// own advice. `tests/cli/testdata/let-set/Earthfile` is that shape.
		if c.Name == earthfile.CmdLet {
			delete(rs.declared, "local:"+name)
			delete(rs.declared, "global:"+name)
		}

		rs.args[name] = value

		return prev, nil

	case earthfile.CmdEnv:
		name, value, err := envPair(c)
		if err != nil {
			return nil, err
		}

		// ε, not a step: it changes what later commands observe and produces no
		// filesystem. Deliberately *not* expanded into the command text - the
		// shell expands it from the environment the step is given, which is the
		// difference between ENV and ARG.
		rs.env[name] = value

		return prev, nil

	case earthfile.CmdHost:
		entry, err := hostEntry(c)
		if err != nil {
			return nil, err
		}

		// State, not a step: it produces no filesystem and changes what every
		// later step resolves, exactly as CACHE changes what every later step
		// has mounted.
		rs.hosts = append(rs.hosts, entry)

		return prev, nil

	case earthfile.CmdWorkdir:
		if len(c.Args) == 0 {
			return nil, fmt.Errorf("WORKDIR needs a path (%s)", loc(c.SourceLocation))
		}

		// State, not a step: it changes where later commands run and produces no
		// filesystem of its own. A relative path resolves against the current
		// one, as every shell does.
		rs.dir = resolveDir(rs.dir, c.Args[0])

		return prev, nil

	case earthfile.CmdBuild:
		ref, buildArgs, buildPass, opts, err := buildTarget(c)
		if err != nil {
			return nil, err
		}

		// A target built with different arguments is a different build, so the
		// values travel with the resolution rather than being ignored.
		pass := map[string]string{}

		if buildPass {
			needsErr := p.here.features.needs(
				p.here.features.passArgs, "BUILD --pass-args", "--pass-args", loc(c.SourceLocation))
			if needsErr != nil {
				return nil, needsErr
			}

			maps.Copy(pass, rs.args)
		}

		maps.Copy(pass, buildArgs)

		p.passTo = pass

		if len(opts.Platforms) > 0 {
			checkErr := checkPlatform(opts.Platforms[0], loc(c.SourceLocation))
			if checkErr != nil {
				return nil, checkErr
			}

			p.passPlatform = opts.Platforms[0]
		}

		// As on FROM: the line granting privilege is this one.
		p.passPrivilege = opts.AllowPrivileged || p.grantedByImport(ref)

		// **One reference may name several targets.** `BUILD ./wildcard/*+test`
		// builds the target of every directory it matches, which the corpus
		// writes five ways and this engine read literally, looking for a
		// directory called `*`. A reference with no metacharacter expands to
		// itself without touching the filesystem, so an ordinary BUILD reaches
		// the resolver exactly as it did.
		refs, err := expandRef(p.here.dir, ref)
		if err != nil {
			return nil, wrapRef("BUILD", c, err)
		}

		// A pattern matched more than the directory it was aimed at when it
		// matched a directory whose Earthfile defines something else. Skipping
		// those is what makes a pattern usable; a reference that names one
		// directory still says what is wrong, because it did not expand.
		globbed := len(refs) != 1 || refs[0] != ref

		for _, one := range refs {
			dep, _, depErr := p.targetRef(one, loc(c.SourceLocation))
			if depErr != nil {
				if globbed && errors.Is(depErr, errNoSuchTarget) {
					continue
				}

				return nil, wrapRef("BUILD", c, depErr)
			}

			// A second root, not an input. BUILD makes the other target run and
			// leaves this one's filesystem alone; making it an input would stack
			// the dependency's layers into this target's base, which is what
			// FROM means.
			//
			// Through appendOnce like every other addition: it drops a nil - a
			// target whose recipe produced no step - and collapses a repeat, so
			// two BUILDs of one target are one root. Appending directly put a
			// nil in the list, which nothing noticed until something else
			// iterated it.
			p.also = appendOnce(p.also, dep)
		}

		return prev, nil

	case earthfile.CmdSaveImage:
		if prev == nil {
			return nil, fmt.Errorf(
				"%s at %s has no filesystem to name\n  a target begins with FROM",
				earthfile.CmdSaveImage, loc(c.SourceLocation))
		}

		var img cmdopts.SaveImage

		refs, err := flagutil.ParseArgsCleaned(string(earthfile.CmdSaveImage), &img, c.Args)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w",
				earthfile.CmdSaveImage, loc(c.SourceLocation), err)
		}

		// `--cache-from` and `--insecure` are accepted and ignored, and the
		// distinction they draw is worth stating. `--cache-from` names
		// somewhere to *look* for cache; `--insecure` says the push may use
		// plain HTTP, and this engine does not push - `pushNote` is what says
		// so to the operator. Neither can change the image, which is I5: a hint
		// may not change results. Refusing a flag that cannot affect the output
		// turns a working Earthfile away for nothing.
		//
		// Both are kept out of the key by not reaching the graph at all: two
		// builds differing only in where they were told to look, or in a
		// transport that is never opened, must share cache entries - which they
		// cannot do if either is part of what is keyed.
		//
		// `--no-manifest-list` is refused because it is not a hint. It says
		// what shape the artefact takes, so an engine that ignored it would
		// hand back something other than what was asked for.
		for _, u := range []struct {
			set  bool
			name string
		}{
			{img.NoManifestList, "--no-manifest-list"},
		} {
			if u.set {
				return nil, unsupported("SAVE IMAGE "+u.name, loc(c.SourceLocation), "")
			}
		}

		for _, a := range refs {
			cfg := rs.cfg.clone()
			cfg.User, cfg.WorkingDir = rs.user, rs.dir

			maps.Copy(cfg.Env, rs.env)

			p.Images = append(p.Images, Image{
				Ref: a, Push: img.Push, Config: cfg,
				From: prev, Source: loc(c.SourceLocation),
			})
		}

		// Naming an output does not produce one.
		return prev, nil

	case earthfile.CmdSaveArtifact:
		if prev == nil {
			return nil, fmt.Errorf(
				"SAVE ARTIFACT at %s has no filesystem to take from\n  a target begins with FROM",
				loc(c.SourceLocation))
		}

		a, err := artifact(c, prev, rs.dir, rs.args.expandDest)
		if err != nil {
			return nil, err
		}

		p.Artifacts = append(p.Artifacts, a)

		// The state is unchanged: selecting an output does not produce one.
		return prev, nil

	case earthfile.CmdGitClone:
		return p.gitCloneNode(c, prev, rs)

	case earthfile.CmdProject:
		// `PROJECT org/project` names who a build belongs to, which the hosted
		// service resolves secrets against. This engine resolves secrets from
		// the invocation and nowhere else, so there is nothing for the
		// declaration to act on - and refusing an Earthfile over a line that
		// only says who owns it would refuse the whole build for a fact it
		// never uses.
		//
		// Validated and not recorded. A plan field nothing reads is a feature
		// built ahead of its consumer, which this package has an architecture
		// test against - and it caught this one. When something does resolve
		// against a project, the field arrives with the code that reads it.
		// The construct arrived with `--use-project-secrets`, so a file older
		// than the feature is using a keyword its dialect does not have.
		// `tests/project-secrets-without-flag.earth` is `VERSION 0.6` and says
		// what it expects in the command it runs (E461).
		err := p.here.features.needs(p.here.features.projectSecrets,
			"PROJECT", "--use-project-secrets", loc(c.SourceLocation))
		if err != nil {
			return nil, err
		}

		if len(c.Args) != 1 || !strings.Contains(c.Args[0], "/") {
			return nil, fmt.Errorf(
				"PROJECT at %s: expected `PROJECT organisation/project`, found %q",
				loc(c.SourceLocation), strings.Join(c.Args, " "))
		}

		return prev, nil

	case earthfile.CmdCache:
		m, err := cacheMount(c, rs.dir)
		if err != nil {
			return nil, err
		}

		// Recorded on the state rather than producing a node: CACHE declares
		// something about the steps that follow, and declares nothing about the
		// filesystem on its own.
		rs.mounts = append(rs.mounts, m)

		return prev, nil

	default:
		return nil, unsupported(string(c.Name), loc(c.SourceLocation), arrival[c.Name])
	}
}

// artifact parses `SAVE ARTIFACT <path> [AS LOCAL <dest>]`.
func artifact(
	c earthfile.Command, from *ir.Node, workdir string, expandDest func(string) string,
) (Artifact, error) {
	// Read the flags off the front, or the first one becomes the path: `SAVE
	// ARTIFACT --if-exists /out /dst` was saving an artifact whose path was
	// `--if-exists` and whose destination was `/out` - the wrong file, exported
	// to the wrong place, reported as success. The third command found with its
	// options unparsed, after RUN and IF.
	var opts cmdopts.SaveArtifact

	args, err := flagutil.ParseArgsCleaned(string(earthfile.CmdSaveArtifact), &opts, c.Args)
	if err != nil {
		return Artifact{}, fmt.Errorf("%s (%s): %w",
			earthfile.CmdSaveArtifact, loc(c.SourceLocation), err)
	}

	for _, u := range []struct {
		set  bool
		name string
	}{
		// --keep-ts is accepted and does nothing, because doing nothing is
		// exactly what it asks for here.
		//
		// The reference clamps mtimes to a fixed epoch and this flag asks it
		// not to; this engine preserves them always, because I8 makes an mtime
		// part of a layer's identity and the containerd fork was patched to
		// stop truncating them. Refusing the flag rejected an Earthfile for
		// requesting the behaviour it was going to get - the least defensible
		// kind of incompatibility, because the build it refused was correct.
		//
		// If this engine ever clamps by default - an open question (E34), not a
		// settled one - the flag becomes load-bearing and this is where it
		// starts.
		// --keep-own is absent for the same reason: a captured layer records
		// uid and gid, so nothing is flattened when an artifact is saved.
		//
		// --symlink-no-follow is absent for the same reason as --keep-ts above:
		// it asks for what a capture already does. A layer holds a symlink as a
		// symlink, so nothing is dereferenced when an artifact is saved, and
		// there is nothing here for the flag to change.
		//
		// It has to be accepted rather than merely harmless, because the
		// reference requires the flag on *both* the SAVE ARTIFACT and the COPY
		// - so refusing it here makes the only spelling that works on the other
		// engine unbuildable on this one. Found by writing the differential
		// case, which could be written for neither engine until this changed.
	} {
		if u.set {
			return Artifact{}, unsupported("SAVE ARTIFACT "+u.name, loc(c.SourceLocation), "")
		}
	}

	// The only refusal here, and a decision rather than a gap: --force exists to
	// permit a save outside the Earthfile's directory, which three checks refuse
	// - this one, the CLI's, and insideProject at the point of writing, which
	// resolves symlinks so the position cannot be walked around.
	if opts.Force {
		return Artifact{}, refusedOnPurpose("SAVE ARTIFACT --force", loc(c.SourceLocation),
			"this engine never writes outside the project, and checks that again where it writes")
	}

	if len(args) == 0 {
		return Artifact{}, fmt.Errorf("SAVE ARTIFACT needs a path (%s)", loc(c.SourceLocation))
	}

	// A relative path is relative to the working directory, exactly as it is for
	// a RUN and for a COPY destination. `WORKDIR /code` then `SAVE ARTIFACT
	// main.o` means /code/main.o, and taking it from the filesystem root
	// instead produced "no such file" against a path the Earthfile never wrote -
	// reported two targets away, in whatever consumed the artifact.
	path := args[0]
	if !strings.HasPrefix(path, "/") {
		// A base with no working directory has nothing for a relative path to
		// be relative to. `FROM scratch` is that base: its configuration is
		// empty, so it names no directory to start in - and resolving against
		// the root anyway is how `tests/file-copying.earth`'s negative test
		// passed here (E471).
		if workdir == "" {
			return Artifact{}, fmt.Errorf(
				"SAVE ARTIFACT %s at %s is a relative path, and this target's"+
					" base has no working directory"+
					"\n  `FROM scratch` starts from an empty configuration:"+
					" write an absolute path, or a WORKDIR before this line",
				args[0], loc(c.SourceLocation))
		}

		path = filepath.Join("/", workdir, path)
	}

	a := Artifact{
		Path: path, Name: filepath.Base(path), From: from,
		Source: loc(c.SourceLocation), IfExists: opts.IfExists,
	}

	// `SAVE ARTIFACT <path> <name>`: a second word that is not the start of
	// `AS LOCAL` names the artifact.
	//
	// Cleaned, because every lookup compares `"/" + name`: a name written
	// `./x.txt` became `/./x.txt` and equalled nothing, so the reference passed
	// through to the guest as a path no layer has
	// (tests/escape.earth+test-copy-artifact2). The default name above goes
	// through filepath.Base and was always clean, which is why only the
	// explicit-name form was affected.
	if len(args) > 1 && !strings.EqualFold(args[1], "AS") {
		a.Name = filepath.Clean(args[1])
	}

	for i := 1; i < len(args); i++ {
		if !strings.EqualFold(args[i], "AS") {
			continue
		}

		// `AS LOCAL <dest>`: three tokens, and a truncated form is a typo worth
		// naming rather than a destination worth guessing.
		if i+2 >= len(c.Args) || !strings.EqualFold(args[i+1], "LOCAL") {
			return Artifact{}, fmt.Errorf(
				"SAVE ARTIFACT at %s: expected `AS LOCAL <destination>`, found %q",
				loc(c.SourceLocation), strings.Join(args[i:], " "))
		}

		// A destination is a place this engine makes, so a name nobody declared
		// is nothing rather than text: `AS LOCAL "build/$GOARCH$VARIANT/x"`
		// with no VARIANT declared writes build/arm64/x, as the reference does.
		dest := expandDest(args[i+2])

		err := checkLocalDest(dest, loc(c.SourceLocation))
		if err != nil {
			return Artifact{}, err
		}

		a.LocalDest = dest

		break
	}

	return a, nil
}

// ifStatement evaluates a conditional at plan time and follows the branch it
// selects.
//
// Only the taken branch enters the graph. The untaken one is not built, not
// keyed and not reported - which is what makes the condition part of the build's
// identity: a different argument selects different steps, so it produces a
// different graph and a different key.
func (p *Plan) ifStatement(st *earthfile.IfStatement, prev *ir.Node, rs *state) (*ir.Node, error) {
	where := loc(st.SourceLocation)

	taken, err := p.branch(st.Expression, prev, rs, where)
	if err != nil {
		return nil, err
	}

	if taken {
		return p.block(st.IfBody, prev, rs)
	}

	for _, e := range st.ElseIf {
		taken, err := p.branch(e.Expression, prev, rs, loc(e.SourceLocation))
		if err != nil {
			return nil, err
		}

		if taken {
			return p.block(e.Body, prev, rs)
		}
	}

	if st.ElseBody != nil {
		return p.block(*st.ElseBody, prev, rs)
	}

	// No branch taken and no ELSE: the state is unchanged, which is what an
	// unmatched conditional means.
	return prev, nil
}

// branch expands a condition's arguments and decides it, evaluating it against
// the preceding step's filesystem when it cannot be decided.
func (p *Plan) branch(expr []string, prev *ir.Node, rs *state, where string) (bool, error) {
	expr, err := condFlags(expr, where)
	if err != nil {
		return false, err
	}

	expanded := make([]string, len(expr))
	for i, tok := range expr {
		expanded[i] = rs.args.expand(tok)
	}

	taken, err := decide(expanded, rs.args, rs.env)
	if err == nil {
		return taken, nil
	}

	if !errors.Is(err, errUnsupportedTest) {
		return false, err
	}

	return p.evaluate(expanded, prev, rs.dir, where)
}

// resolveDest anchors a relative COPY destination to the working directory.
//
// `WORKDIR /app` then `COPY . .` is the most common pair of lines in container
// builds, and without this the files landed at the filesystem root - with the
// symptom arriving two steps later, as a RUN unable to find a file that had
// definitely been copied, pointing at the wrong line entirely.
//
// Resolved when the plan is made rather than inside the guest, because where a
// file lands is a static fact about the step and belongs in its identity: two
// COPYs of one file into two working directories are different operations and
// must not share a key.
//
// The trailing separator survives the join, because it is not decoration - it
// is the difference between placing a file inside a directory and renaming it.
func resolveDest(dest, workdir string) string {
	if workdir == "" || workdir == "/" || filepath.IsAbs(dest) {
		return dest
	}

	out := filepath.Join(workdir, dest)

	// `.` means "into this directory" as surely as `./` does, and Join drops
	// the distinction: the result named a *file*, so the copy created /app as a
	// regular file and the next step could not use it as a working directory -
	// reported two steps from the line that caused it.
	if strings.HasSuffix(dest, "/") || dest == "." || dest == ".." {
		if !strings.HasSuffix(out, "/") {
			out += "/"
		}
	}

	return out
}

// copy plans a COPY, from the build context or from another target's artifact.
func (p *Plan) copy(c earthfile.Command, prev *ir.Node, rs *state) (*ir.Node, error) {
	spec, err := copyArgs(c)
	if err != nil {
		return nil, err
	}

	args, dirCopy, ifExists := spec.Args, spec.Dir, spec.IfExists

	// As on FROM and BUILD: the line granting privilege is this one, and it
	// holds for every source it names.
	// The grant may also come from the IMPORT the source is named through; the
	// per-source check happens below, where each source is known.
	p.passPrivilege = spec.AllowPrivileged
	passArgs, platform, buildArgs := spec.PassArgs, spec.Platform, spec.BuildArgs

	// `copy-sources = copy-source *( WSP copy-source )`: every argument but the
	// last is a source. Taking only the first silently dropped the rest - a
	// build that succeeds and produces an image missing half of what the
	// Earthfile put in it.
	dest := resolveDest(args[len(args)-1], rs.dir)
	sources := args[:len(args)-1]

	// **Two things cannot both become the destination.** `COPY a b dest` places
	// each source inside dest whether or not dest is there; the destination was
	// resolved once and handed to every source unchanged, so with dest absent
	// the first source *became* it and only the second landed beside it
	// (tests/copy.earth+copy-art-multi-no-exist).
	//
	// A trailing separator is how "inside" is already spelled here - see
	// `copy-art-trailing-slash`, which asks for exactly this and gets it - so
	// this states the same thing rather than adding a second way to say it.
	//
	// From the sources as *written*, before any pattern is expanded: a wildcard
	// is one written source, and a rule that changed meaning according to how
	// many files happened to match would make the build depend on what is in
	// the directory.
	if len(sources) > 1 && !strings.HasSuffix(dest, "/") {
		dest += "/"
	}

	// `--dir` copies the directory itself rather than its contents, and it is
	// carried as a flag rather than as a trailing separator on the destination.
	//
	// The separator was doing both jobs and cannot: for a *file* it means "place
	// this inside that directory", and for a *directory* the default is the
	// opposite - `COPY src .` contributes what is in src. Encoding --dir as a
	// separator made `COPY src .` put the tree at ./src, where the `gcc -c
	// main.cpp` on the next line could not find it.

	// A pattern becomes the files it names before anything else happens, so
	// each match is an ordinary source from here on.
	expanded, err := expandContextPatterns(p.here.dir, sources, loc(c.SourceLocation))
	if err != nil && !ifExists {
		return nil, err
	}

	if err == nil {
		sources = expanded
	}

	// `--if-exists` drops what is not there. A pattern that matched nothing is
	// dropped by the same rule: both say "copy this if the build produced it",
	// and refusing one while allowing the other would be a distinction the
	// Earthfile never drew.
	if ifExists {
		sources = onlyPresent(p.here.dir, sources)
	}

	// One step per source, chained: each copy stands on the one before it, so
	// two sources writing the same path resolve the way the line reads.
	for _, src := range sources {
		// `--platform` builds the referenced target somewhere else, as FROM and
		// BUILD already do: a build often needs one artifact from an
		// architecture other than the one it runs on, a cross-compiled binary
		// being the ordinary case.
		if platform != "" {
			err := checkPlatform(platform, loc(c.SourceLocation))
			if err != nil {
				return nil, err
			}

			p.passPlatform = platform
		}

		// `--pass-args` hands this target's arguments to the one the artifact
		// comes from, as FROM and BUILD already do. The explicit overrides win,
		// because writing one is saying what it should be regardless of what
		// happens to be in scope.
		if passArgs {
			err := p.here.features.needs(
				p.here.features.passArgs, "COPY --pass-args", "--pass-args", loc(c.SourceLocation))
			if err != nil {
				return nil, err
			}
		}

		if passArgs || len(buildArgs) > 0 {
			pass := map[string]string{}

			if passArgs {
				maps.Copy(pass, rs.args)
			}

			maps.Copy(pass, buildArgs)

			p.passTo = pass
		}

		if p.grantedByImport(src) {
			p.passPrivilege = true
		}

		source, inSource, asked, err := p.copySource(src, loc(c.SourceLocation))
		if err != nil {
			return nil, err
		}

		// **The name the reference asked for, when the stored path does not
		// carry it.** `SAVE ARTIFACT ./file.txt ./other.txt` keeps the bytes at
		// /test/file.txt and calls them /other.txt, so `COPY +t/other.txt ./`
		// landed `file.txt` in the step and the line after it read a file that
		// was not there (tests/escape.earth+test-copy-artifact2).
		//
		// Only where they differ, so every ordinary copy carries nothing and
		// keys exactly as it did.
		landsAs := ""
		if asked != "" && path.Base(asked) != path.Base(inSource) {
			landsAs = path.Base(asked)
		}

		// `--dir` means the same for an artifact as for a path in the project,
		// and the destination decides. See the rule in the guest, which is
		// where the destination can actually be looked at.
		//
		// It was cancelled here for artifacts, on the evidence of E32:
		// `COPY --dir +build/sub /here` produced /here/sub/b.txt where the
		// reference produces /here/b.txt. That reading was right about the case
		// and wrong about the rule - /here did not exist, and the reference
		// joins nothing to a destination that is not there. Cancelling the flag
		// reproduced the reference for that case and broke the one the
		// repository's own Earthfile uses, `COPY --dir +code/earthly /`, where
		// the destination is the root and could not exist more.
		//
		// The general lesson is worth the sentence: a single differential case
		// tells you what the reference *did*, and it takes the matrix to tell
		// you what it *means*.
		dir := dirCopy

		// `+target/dist` where the target saved `/dist/index.js`: a directory
		// in the artifact namespace, holding everything saved below it. Each
		// entry keeps its path below the name that was asked for, so
		// `COPY +build/dist out` puts /dist/index.js at out/index.js.
		//
		// Before the glob, and for the same reason: neither is a path in any
		// layer, so passing either through asks the guest to find something no
		// filesystem contains.
		if entries := p.savedUnder(source, inSource); len(entries) > 0 {
			for _, e := range entries {
				prev = &ir.Node{
					Platform: platformOf(rs.platform),
					Op: ir.Op{
						Kind: ir.OpFile,
						Args: []string{
							e.artifact.Path,
							filepath.Join(strings.TrimSuffix(dest, "/"), e.rel),
						},
						// Not `dir`: each entry's destination is already
						// resolved here, and asking the guest to place it
						// inside one more directory would apply the rule twice.
						Dir: rs.dir, User: rs.user, DirCopy: false,
						NoFollow: spec.NoFollow, KeepOwn: spec.KeepOwn, Chown: spec.Chown,
						Chmod:    spec.Chmod,
						IfExists: ifExists,
					},
					Inputs:  []*ir.Node{prev},
					Sources: []*ir.Node{source},
					Meta: ir.Meta{
						Source:      loc(c.SourceLocation),
						Description: "COPY " + src + " " + dest,
					},
				}
			}

			continue
		}

		// `+target/*` names everything that target saved, not a file called
		// `*`. Expanded here rather than in the guest, so each artifact is its
		// own copy in the plan and the key covers exactly what was taken: a
		// producer that starts saving a second artifact is a different build
		// and should look like one.
		if artifactPattern(inSource) {
			taken, err := p.savedMatching(source, inSource, src,
				loc(c.SourceLocation), ifExists)
			if err != nil {
				return nil, err
			}

			for _, a := range taken {
				// Each lands under the name it was given, because that is what
				// the rest of the Earthfile calls it: a pattern's match carries
				// a version the author did not write, and the ENTRYPOINT two
				// lines later names the artifact.
				to := dest
				if strings.HasSuffix(to, "/") || to == "." {
					to = filepath.Join(strings.TrimSuffix(dest, "/"), a.Name)
				}

				prev = &ir.Node{
					Platform: platformOf(rs.platform),
					Op: ir.Op{
						Kind: ir.OpFile, Args: []string{a.Path, to},
						// As above: `to` already carries the name each match
						// lands under, so the guest has nothing left to decide.
						Dir: rs.dir, User: rs.user, DirCopy: false,
						NoFollow: spec.NoFollow, KeepOwn: spec.KeepOwn, Chown: spec.Chown,
						Chmod:    spec.Chmod,
						IfExists: ifExists,
					},
					Inputs:  []*ir.Node{prev},
					Sources: []*ir.Node{source},
					Meta: ir.Meta{
						Source:      loc(c.SourceLocation),
						Description: "COPY " + src + " " + dest,
					},
				}
			}

			continue
		}

		prev = &ir.Node{
			Platform: platformOf(rs.platform),
			Op: ir.Op{
				Kind: ir.OpFile, Args: []string{inSource, dest},
				Dir: rs.dir, User: rs.user, DirCopy: dir,
				NoFollow: spec.NoFollow, KeepOwn: spec.KeepOwn, Chown: spec.Chown,
				Chmod:    spec.Chmod,
				IfExists: ifExists,
				As:       landsAs,
			},
			Inputs:  []*ir.Node{prev},
			Sources: []*ir.Node{source},
			Meta: ir.Meta{
				Source:      loc(c.SourceLocation),
				Description: "COPY " + src + " " + dest,
			},
		}
	}

	return prev, nil
}

// callerContext is the directory a COPY of a context path reads from.
//
// The invocation's own context, which is what the top-level build has and what
// every function called from it inherits. A unit fetched from a repository
// brings its Earthfile and no context, so `here.dir` is the wrong answer inside
// a remote function - see E716 and Plan.contextNode.
func (p *Plan) callerContext() string {
	// **Only inside a function.** A remote *target* brings its own context -
	// `BUILD github.com/org/repo+build` copying `src/` means that repository's
	// `src/`, which `wildcard-copy.earth+wildcard-remote` builds - so this is
	// not a rule about fetched units, it is a rule about functions.
	if p.inFunction > 0 && p.here.fetchedFrom != "" && p.opt.context != "" {
		return p.opt.context
	}

	return p.here.dir
}

// grantedByImport reports whether a reference goes through an alias that was
// imported with `--allow-privileged`.
//
// The grant belongs to the name: `IMPORT --allow-privileged <repo> AS priv`
// then `COPY priv+privileged/out .` carries no flag of its own, and the whole
// point of naming a repository once is that it does not have to.
func (p *Plan) grantedByImport(ref string) bool {
	at := strings.Index(ref, "+")
	if at <= 0 || p.here == nil {
		return false
	}

	return p.here.grants[ref[:at]]
}

// copySource resolves what a COPY reads from.
// asked is the artifact path as the reference wrote it, empty for a copy from
// the build context. It is what the file must land under: the *stored* path may
// carry a different name, because `SAVE ARTIFACT ./file.txt ./other.txt` keeps
// the bytes at /test/file.txt and calls them /other.txt.
func (p *Plan) copySource(src, where string) (*ir.Node, string, string, error) {
	// `artifact-with-args = "(" WSP artifact-ref *( WSP build-arg-override )
	// WSP ")"`. ProcessParamsAndQuotes has already merged this into one token,
	// so it arrives whole rather than as flags on the COPY itself.
	if strings.HasPrefix(src, "(") && strings.HasSuffix(src, ")") {
		fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(src, "("), ")"))
		if len(fields) == 0 {
			return nil, "", "", fmt.Errorf("empty reference in parentheses (%s)", where)
		}

		args, err := overrides(fields[1:], where)
		if err != nil {
			return nil, "", "", err
		}

		p.passTo = args

		return p.copySource(fields[0], where)
	}

	if !strings.Contains(src, "+") {
		// The build context is *this* Earthfile's directory, not the one that
		// referred to it. A COPY in lib/Earthfile names a file beside that file;
		// resolving it against the calling Earthfile would silently copy
		// something else, or report a file missing that is sitting exactly where
		// its own Earthfile says it is.
		n, err := p.contextNode("COPY", src, where)

		return n, src, "", err
	}

	// `+target/path` or `./lib+target/path`: the target, and the path within its
	// output.
	// The first plus, and the *last* one is what divides path from target - but
	// this index only decides where the artifact path is cut, and every plus
	// before the first `/` gives the same answer there. `parseRef` applies the
	// rule, on a string this reconstructs unchanged (E444).
	i := strings.Index(src, "+")

	ref, path, ok := strings.Cut(src[i:], "/")
	if !ok {
		// A string that cannot be a reference is not one.
		//
		// `+` starts a target reference, and a filename may contain one:
		// `COPY file-with-\+.txt ./` is how an Earthfile says so, and this
		// repository's own `tests/escape.earth` is written that way. The escape
		// does not survive the lexer, so by here the two spellings are one
		// string - and the engine refused the file, naming a target nobody
		// wrote (E441).
		//
		// Decided by *shape first*: with no `/` after the `+` there is no
		// artifact, so this cannot be the reference form whatever the author
		// meant. Then by whether the build context has such a file, which is the
		// only remaining evidence of which spelling it was. A COPY already
		// depends on what the context holds, so this asks a question the command
		// was going to ask anyway.
		n, cerr := p.contextNode("COPY", src, where)
		if cerr == nil {
			return n, src, "", nil
		}

		// Which of two findings it is depends on what sits before the plus.
		//
		// Nothing, a path, or an IMPORT alias means the author was writing a
		// reference and left the artifact off: `COPY +dep .` is a forgotten
		// `/path` far more often than it is a file called `+dep`, and
		// `COPY ../+base .` is the same thing with a directory in front. A
		// *filename* before the plus is not that - nobody writes `file-with-` as
		// a target - so the missing file is the finding (E479).
		if referenceShaped(src, p.here.imports) {
			return nil, "", "", fmt.Errorf(
				"%q names a target but no artifact (%s)\n  write it as +target/path"+
					"\n  a file of that name would be copied instead, and the"+
					" context has none",
				src, where)
		}

		// And where the file is absent, the finding is the *file*.
		//
		// This said "names a target but no artifact" and told the reader to
		// write `+target/path` - sending them after a target the two lines
		// above have already worked out cannot exist. **A diagnosis about the
		// thing that was ruled out**, which is E478's Dockerfile in a different
		// command (E479).
		//
		// The other reading still gets a line, because a `+` in a source is
		// worth a second thought even where the shape rules it out - but as the
		// aside it is, under the claim rather than instead of it.
		return nil, "", "", fmt.Errorf(
			"%q is not in the build context (%s)"+
				"\n  looked in %s"+
				"\n  the `+` here starts no target reference: there is no"+
				" /path after it, so it was read as part of the filename"+
				"\n  write `+target/path` if a target was meant",
			src, where, p.here.dir)
	}

	n, _, err := p.targetRef(src[:i]+ref, where)
	if err != nil {
		return nil, "", "", fmt.Errorf("COPY %s (%s): %w", src, where, err)
	}

	// `+build/main.o` names the artifact that target saved, and where it saved
	// it is that target's business: `SAVE ARTIFACT main.o` under `WORKDIR /code`
	// puts it at /code/main.o. Reading the name as a path took /main.o - a file
	// the Earthfile never mentions - and reported it in the *consuming* target,
	// two steps from the line that decided it.
	return n, p.savedAt(n, path), unescape(path), nil
}

// allSavedBy is every artifact a target produced, for `+target/*`.
//
// A target that saved nothing is refused rather than copied from: a glob over
// no artifacts is a reference to something that does not exist, and copying
// nothing would produce an image quietly missing whatever the author meant.
func (p *Plan) allSavedBy(from *ir.Node, src, where string) ([]Artifact, error) {
	var out []Artifact

	for _, a := range p.Artifacts {
		if a.From != nil && a.From.ID() == from.ID() {
			out = append(out, a)
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf(
			"COPY %s at %s: that target saves no artifacts, so there is nothing to copy"+
				"\n  give it a SAVE ARTIFACT, or name the file you meant",
			src, where)
	}

	// Ordered, so a build reading this twice reads the same plan.
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })

	return out, nil
}

// artifactPattern reports whether an artifact path selects among what a target
// saved rather than naming one file of it.
//
// The last segment decides, because that is the only part a pattern may live
// in: `+build/dist/*` globs within `dist`, and a `*` earlier in the path would
// be a target reference this never sees.
func artifactPattern(inSource string) bool {
	return strings.ContainsAny(path.Base(inSource), "*?[")
}

// savedMatching is every artifact of a target whose name the pattern selects.
//
// **The match is against what the producer declared, not against a tree.** A
// target's artifacts are known at plan time, so `+build/main.*` resolves where
// `+build/*` already does, and each match is its own copy in the key - a
// producer that starts saving a second matching artifact is a different build.
// Passed through instead, the guest looked for a file called `main.*` in the
// producing layer and reported it missing in the *consuming* target.
func (p *Plan) savedMatching(
	from *ir.Node, pattern, src, where string, ifExists bool,
) ([]Artifact, error) {
	all, err := p.allSavedBy(from, src, where)
	if err != nil {
		if ifExists {
			return nil, nil
		}

		return nil, err
	}

	// **The bare star keeps its own meaning**, which is wider than a match:
	// `path.Match` stops at a separator, where `+build/*` has always taken
	// artifacts saved into directories of the namespace too.
	if pattern == "/*" || strings.HasSuffix(pattern, "/*") {
		return all, nil
	}

	want := "/" + strings.TrimPrefix(pattern, "/")

	var out []Artifact

	for _, a := range all {
		ok, merr := path.Match(want, "/"+strings.TrimPrefix(a.Name, "/"))
		if merr != nil {
			return nil, fmt.Errorf("COPY %s at %s: %q is not a valid pattern: %w",
				src, where, pattern, merr)
		}

		if ok {
			out = append(out, a)
		}
	}

	// **Nothing matched is refused, not copied as nothing.** The artifacts are
	// listed, because the author is choosing among names they wrote and the
	// mismatch is usually visible the moment both are on the screen.
	if len(out) == 0 {
		names := make([]string, 0, len(all))
		for _, a := range all {
			names = append(names, a.Name)
		}

		// **Unless the author said it might match nothing.** `--if-exists`
		// means for a pattern what it means for a path, and
		// `COPY --if-exists +save/*_ok .` from a target saving `ok` is a
		// corpus target asserting exactly that.
		if ifExists {
			return nil, nil
		}

		return nil, fmt.Errorf(
			"COPY %s at %s: no artifact of that target matches %q"+
				"\n  it saves %s",
			src, where, pattern, strings.Join(names, ", "))
	}

	return out, nil
}

// savedUnder is every artifact a target saved inside a directory of its
// artifact namespace, and where each goes below the destination.
//
// `SAVE ARTIFACT index.js /dist/index.js` names the file /dist/index.js in a
// namespace of the target's own making; `/dist` is a directory in that
// namespace holding it. Nothing of either name exists in any layer, which is
// why this is resolved here and not in the guest - passed through, `/dist` was
// looked for in the producing target's filesystem and reported missing in the
// *consuming* target, two steps from the line that decided it.
//
// Only strictly below, so an artifact actually named `/dist` is a file and is
// matched by savedAt instead.
func (p *Plan) savedUnder(from *ir.Node, name string) []savedEntry {
	dir := "/" + strings.Trim(name, "/") + "/"

	var out []savedEntry

	for _, a := range p.Artifacts {
		if a.From == nil || a.From.ID() != from.ID() {
			continue
		}

		full := "/" + strings.TrimPrefix(a.Name, "/")
		if !strings.HasPrefix(full, dir) {
			continue
		}

		out = append(out, savedEntry{artifact: a, rel: strings.TrimPrefix(full, dir)})
	}

	// Ordered, so a build reading this twice reads the same plan.
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })

	return out
}

// savedEntry is one artifact found inside an artifact directory.
type savedEntry struct {
	artifact Artifact
	// rel is its path below the directory that was named, which is where it
	// lands below the destination: `COPY +build/dist out` puts /dist/index.js
	// at out/index.js.
	rel string
}

// savedAt is where a target put the artifact of this name.
//
// The name as written when nothing matches, so a reference to something that
// was never saved fails saying what it looked for rather than what this
// function guessed.
func (p *Plan) savedAt(from *ir.Node, name string) string {
	want := "/" + strings.TrimPrefix(name, "/")

	for _, a := range p.Artifacts {
		if a.From == nil || a.From.ID() != from.ID() {
			continue
		}

		// The name the target gave it, which is what everyone else calls it:
		// `SAVE ARTIFACT index.js /dist/index.js` is named /dist/index.js and
		// lives at /js-example/index.js, and `+build/dist/index.js` means the
		// file - so the answer is where the file is, not what it is called.
		if "/"+strings.TrimPrefix(a.Name, "/") == want {
			return a.Path
		}

		// The declared path, matched by what it ends with: `SAVE ARTIFACT
		// main.o` in /code is `/code/main.o`, and `+build/main.o` names it.
		if a.Path == want || strings.HasSuffix(a.Path, want) {
			return a.Path
		}

		// A pattern, matched against what the consumer asked for. `SAVE
		// ARTIFACT ./*` in /s is recorded as `/s/*`, because the files it will
		// match do not exist until the step has run - so the name is checked
		// against the pattern rather than the other way round, and `+saver/one`
		// is /s/one.
		//
		// A star does not cross a separator, here as it does not in the guest's
		// own matcher: `./*` publishes what is beside it, not what is under it.
		if at, ok := underPattern(a.Path, want); ok {
			return at
		}
	}

	// **A reference may descend into a saved artifact.** `SAVE ARTIFACT in`
	// names one artifact, `/in`, and `+artifact/in/sub/1` names something inside
	// it - which nothing above resolves, because the name is not equal to `/in`
	// and `/in` is not a directory *of* artifacts the way savedUnder means. The
	// reference was passed through as written, so the guest was asked for
	// `/in/sub/1`, a path no layer has (tests/copy.earth+copy-art-multi-*).
	//
	// The longest matching name wins, so a target that saves both a directory
	// and something inside it resolves through the more specific one.
	best, under := "", ""

	for _, a := range p.Artifacts {
		if a.From == nil || a.From.ID() != from.ID() {
			continue
		}

		named := "/" + strings.TrimPrefix(a.Name, "/")
		if named == "/" || !strings.HasPrefix(want, strings.TrimSuffix(named, "/")+"/") {
			continue
		}

		if len(named) > len(best) {
			best, under = named, a.Path
		}
	}

	if best != "" {
		return filepath.Join(under, strings.TrimPrefix(want, strings.TrimSuffix(best, "/")))
	}

	return want
}

// underPattern resolves a name against a pattern a target saved under.
//
// The pattern's own directory plus the name asked for, accepted only if the
// pattern matches it: that keeps `/s/*` answering for `one` and refusing
// `nested/deep`, without this having to know what the step produced.
func underPattern(pattern, want string) (string, bool) {
	if !strings.ContainsAny(pattern, "*?[") {
		return "", false
	}

	at := filepath.Join(filepath.Dir(pattern), strings.TrimPrefix(want, "/"))

	// An unparseable pattern is not a match and not an error: the reference
	// fails below naming what it looked for, which is more use than a message
	// about syntax in a line the author may not have written.
	ok, err := filepath.Match(pattern, at)
	if err != nil || !ok {
		return "", false
	}

	return at, true
}

// copyArgs strips COPY's flags, refusing any that change what is copied.
//
// `COPY --dir src dest` copies directories as directories rather than their
// contents, which is a different result. Reading the flag as a path produced
// "--dir is not in the build context" - a diagnosis of the wrong thing entirely,
// forty times over in this repository's own Earthfiles.
// copyArgs reads COPY's options and positional arguments.
//
// Uses the repository's own option layer - `cmdopts.Copy` with
// `flagutil.ParseArgsCleaned` - rather than reading the tokens by hand.
// ParseArgsCleaned runs `stringutil.ProcessParamsAndQuotes` first, which merges
// tokens across quotes *and* across `( ... )`, so quoting and the parenthesised
// reference form come free and cannot drift from what the rest of the engine
// accepts.
// copySpec is what a COPY's flags amount to.
//
// A struct rather than a seventh and eighth return value: the tuple had reached
// six, and a caller that transposes two adjacent bools gets a build that copies
// the wrong thing and compiles perfectly. Naming them makes that a typo the
// compiler catches.
type copySpec struct {
	// Args are the sources followed by the destination.
	Args []string
	// Dir is `--dir`: the directory itself rather than its contents.
	Dir bool
	// NoFollow is `--symlink-no-follow`: a link arrives as a link.
	NoFollow bool
	// KeepOwn is `--keep-own`: uid and gid travel with the copy.
	KeepOwn bool
	// Chown is `--chown=user[:group]`: what the copy belongs to, resolved
	// against the destination image.
	Chown string
	// IfExists tolerates a source that is not there.
	IfExists bool
	// Chmod is `COPY --chmod=777`: the mode the copied files get.
	Chmod string
	// PassArgs forwards this target's arguments to the one the artifact comes
	// from.
	PassArgs bool
	// AllowPrivileged is `COPY --allow-privileged`: this line grants the
	// referenced target privilege, across a repository boundary if need be.
	AllowPrivileged bool
	// Platform builds the source target for a platform of its own.
	Platform string
	// BuildArgs are `--build-arg` values for the source target.
	BuildArgs map[string]string
}

func copyArgs(c earthfile.Command) (copySpec, error) {
	var opts cmdopts.Copy

	rest, err := flagutil.ParseArgsCleaned("COPY", &opts, c.Args)
	if err != nil {
		return copySpec{}, flagFault("COPY", loc(c.SourceLocation), err)
	}

	// `--from` is refused separately, because it is not this engine's gap: the
	// Earthfile language does not have the flag at all, so "use another engine"
	// would be a remedy that fails the same way. Dockerfile syntax does have it
	// and this engine implements it there.
	if opts.From != "" {
		return copySpec{}, notInLanguage("COPY --from", loc(c.SourceLocation),
			"use SAVE ARTIFACT in the other target and COPY its artifact form")
	}

	// Options that change *what* is copied are refused rather than ignored.
	// --dir is honoured, because it is expressible as a destination.
	// --chmod is honoured: a mode is part of a layer and this engine already
	// keeps modes through SAVE ARTIFACT, so there is nothing here a store can
	// fail to carry - unlike --chown, which asks for an owner a shared mount
	// has no room for.
	for _, u := range []struct {
		set  bool
		name string
	}{
		// --keep-ts is absent on purpose: it asks for what this engine already
		// does. See the note on SAVE ARTIFACT below.
		// --keep-own is absent: implemented, and measured first (E34, E84).
		// --allow-privileged is absent: it grants a *referenced* target
		// permission to run privileged, and this engine refuses privileged
		// execution by name wherever it appears - so the permission grants
		// nothing that can happen, and refusing the flag rejected a file over a
		// feature it could not exercise (E420). The refusal at the point of use
		// is asserted rather than assumed.
	} {
		if u.set {
			return copySpec{}, unsupported("COPY "+u.name, loc(c.SourceLocation), "")
		}
	}

	// One says "whatever the source was" and the other names something else, so
	// a copy asking for both has not said what it wants (I10).
	if opts.Chown != "" && opts.KeepOwn {
		return copySpec{}, fmt.Errorf(
			"COPY --chown=%s and --keep-own say different things about the owner:"+
				"\n  one names it and the other takes the source's (%s)",
			opts.Chown, loc(c.SourceLocation))
	}

	if len(rest) < 2 {
		return copySpec{}, fmt.Errorf("COPY needs a source and a destination (%s)", loc(c.SourceLocation))
	}

	// `--build-arg k=v` passes an argument to the target the artifact comes
	// from, which makes it a different build - so it travels with the
	// resolution rather than being dropped.
	args := map[string]string{}

	for _, a := range opts.BuildArgs {
		if k, v, ok := strings.Cut(a, "="); ok {
			args[k] = v
		}
	}

	return copySpec{
		Args: rest, Dir: opts.IsDirCopy,
		NoFollow: opts.SymlinkNoFollow, KeepOwn: opts.KeepOwn, Chown: opts.Chown,
		IfExists: opts.IfExists, PassArgs: opts.PassArgs, Chmod: opts.Chmod,
		AllowPrivileged: opts.AllowPrivileged,
		Platform:        opts.Platform, BuildArgs: args,
	}, nil
}

// do inlines a function call.
//
// Unlike BUILD, which runs another target beside this one, DO continues *this*
// target's filesystem: a function is a way of writing the same steps in one
// place, not a way of running a different build. So its recipe is evaluated with
// the caller's current node as its base.
func (p *Plan) do(c earthfile.Command, prev *ir.Node, caller *state) (*ir.Node, error) {
	ref, args, passArgs, err := doTarget(c)
	if err != nil {
		return nil, err
	}

	where := loc(c.SourceLocation)

	fnRef, err := parseRef(ref, where, p.here.imports)
	if err != nil {
		return nil, err
	}

	u, err := p.resolve(p.here, fnRef)
	if err != nil {
		return nil, err
	}

	fn, err := p.function(u, fnRef.name, where)
	if err != nil {
		return nil, err
	}

	// A fresh state, seeded only with what the call passed.
	//
	// The caller's arguments are deliberately *not* inherited: a function is a
	// unit with its own interface, and one that silently saw its caller's
	// variables would do different things depending on where it was called from.
	// The working directory *is* inherited, because the function runs in the
	// caller's filesystem and a WORKDIR set before the call still applies.
	rs := newState()
	rs.dir = p.callerDir
	rs.supplied = args

	// **The caller's target, because a function is inlined into it.** The
	// language reference puts the build *environment* in the same sentence as
	// the build context, and `EARTHLY_TARGET_NAME` is part of that
	// environment: `function.earth`'s `TEST_BUILTIN` declares it and asserts
	// the name of the target that called it. Built fresh, the state had no
	// target and the builtin answered the empty string - four lines from the
	// assertion, saying nothing about where the name went.
	rs.target = caller.target

	// The globals travel in, and the call's own values beat them.
	//
	// `ARG --global` is the author saying "this one, everywhere", which is a
	// different statement from the caller's locals - those stay outside, because
	// a function that silently saw them would do different things depending on
	// where it was called from (E425).
	rs.globals = p.callerGlobals

	for name, value := range p.callerGlobals {
		if given, ok := rs.supplied[name]; ok {
			// `DO +FN --name=value` beats the global, and does so without the
			// function declaring anything: a global is already declared, so the
			// call is overriding a value rather than supplying an argument.
			value = given
		}

		rs.args[name] = value
	}

	// The environment travels *in* as well as out, and for the same reason it
	// travels out: a function is inlined, so it runs in the caller's build
	// environment and reads what is there. Only arguments are scoped.
	//
	// Without this, `earthly-lib`'s rust library told users their build was
	// misconfigured - `+INIT has not been called yet in this build environment`
	// - about a variable its own `+INIT` had set one call earlier.
	maps.Copy(rs.env, caller.env)
	rs.user = caller.user

	// A function called from a LOCALLY target runs on the machine too: it is
	// inlined into the caller, so it inherits where the caller runs. Without
	// this every RUN inside such a function asked for a base image the caller
	// deliberately does not have.
	rs.host = p.callerHost

	// --pass-args: the caller's values are available, and an explicit argument
	// on the call still beats them - the nearer statement wins.
	if passArgs {
		merged := map[string]string{}
		maps.Copy(merged, p.callerArgs)

		maps.Copy(merged, args)

		rs.supplied = merged
	}

	// **The arguments are part of the site.** A function calling itself with a
	// *different* argument is bounded recursion, which the language has and the
	// corpus uses: `command.earth`'s `RECURSIVE` counts down from 5, touching a
	// file per level, and asserts that `./0` is never made. Keyed on the name
	// alone, the second call read as a loop and the build was refused.
	//
	// The target memo learned this already - it keys on the reference and its
	// arguments, because the same target with different arguments is a
	// different build. The same is true of a function, and for the same reason.
	//
	// Unchanged arguments are still a cycle, because that one does not
	// terminate.
	site := "fn:" + u.dir + "+" + fnRef.name + "\x00" + canonicalArgs(rs.supplied)

	if slices.Contains(p.building, site) {
		return nil, &CycleError{Loop: []string{"+" + fnRef.name, "+" + fnRef.name}}
	}

	p.building = append(p.building, site)

	prevUnit := p.here
	p.here = u

	// The unit changes so `+other` resolves against the file the function was
	// written in; the *context* stays the caller's, which is what
	// callerContext reads this for.
	p.inFunction++

	out, err := p.block(fn.Recipe, prev, rs)

	p.inFunction--
	p.here = prevUnit
	p.building = p.building[:len(p.building)-1]

	if err != nil {
		return nil, err
	}

	// What the function *set* stays set. A function is inlined into the caller -
	// this file says so a few lines up, "a way of writing the same steps in one
	// place, not a way of running a different build" - so an ENV, a WORKDIR or a
	// USER inside it applies to what follows the call, exactly as if the lines
	// had been written there.
	//
	// Arguments deliberately do not travel, and the asymmetry is the point: an
	// ARG is a function's *interface* and is scoped to it, while ENV, WORKDIR
	// and USER are properties of the filesystem the function is building.
	//
	// Discarding them made `earthly-lib`'s caching idiom fail three hops from
	// its cause: the rust, python and node libraries all set their cache mounts
	// with `ENV` inside a function, so `RUN --mount=$EARTHLY_RUST_TARGET_CACHE`
	// expanded to nothing and the engine reported `--mount type=(none) is not
	// supported` - naming something the author never wrote, about a construct
	// that is supported (E101).
	maps.Copy(caller.env, rs.env)
	caller.dir, caller.user = rs.dir, rs.user

	return out, nil
}

// function finds a function by name, listing what exists when it does not.
func (p *Plan) function(u *unit, name, where string) (earthfile.Function, error) {
	names := make([]string, 0, len(u.tree.Functions))

	for _, f := range u.tree.Functions {
		if f.Name == name {
			return f, nil
		}

		names = append(names, f.Name)
	}

	sort.Strings(names)

	if len(names) == 0 {
		return earthfile.Function{}, fmt.Errorf(
			"no function named %q, and this Earthfile defines none (%s)", name, where)
	}

	return earthfile.Function{}, fmt.Errorf(
		"no function named %q (%s)\n  this Earthfile defines: %s",
		name, where, strings.Join(names, ", "))
}

// doTarget picks the function and its arguments out of a DO.
//
// `DO --pass-args +FN --k=v`: flags before the reference are options of the call
// itself, and are refused rather than ignored - --pass-args changes which
// variables the function sees, so honouring the line without it would run a
// different function than the one written.
// doTarget reads DO's options, its function reference and its arguments.
func doTarget(c earthfile.Command) (string, map[string]string, bool, error) {
	var opts cmdopts.Do

	rest, err := flagutil.ParseArgsCleaned("DO", &opts, c.Args)
	if err != nil {
		return "", nil, false, flagFault("DO", loc(c.SourceLocation), err)
	}

	if len(rest) == 0 {
		return "", nil, false, fmt.Errorf("DO needs a function (%s)", loc(c.SourceLocation))
	}

	args, err := overrides(rest[1:], loc(c.SourceLocation))
	if err != nil {
		return "", nil, false, err
	}

	return rest[0], args, opts.PassArgs, nil
}

// overrides reads `--key=value` arguments to a target or function.
//
// `build-arg-override = "--" build-arg-key "=" build-arg-value` in the grammar,
// so the value is joined with `=`. The space-separated form is also accepted,
// because this repository's own Earthfiles write it and rejecting a line the
// parser accepts helps nobody.
func overrides(args []string, where string) (map[string]string, error) {
	out := map[string]string{}

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}

		name, value, joined := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if joined {
			// **A value this engine consumes has its quoting resolved**, the
			// rule the rest of the interpreter follows. `escape.earth` passes
			// `FILE="file-with-\+.txt"` - the backslash is what stops the `+`
			// being read as a target reference, and it is punctuation, not part
			// of the name. Passed through, the target looked for a file called
			// `file-with-\+.txt` and reported it missing, naming a file nobody
			// has.
			out[name] = unquote(value)

			continue
		}

		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			out[name] = trueWord

			continue
		}

		i++
		out[name] = unquote(args[i])
	}

	// A caller may not pass a value for a name the engine answers.
	//
	// The dangerous half of E457's rule: unlike a default, which could never
	// apply, a passed value *can* - so a target would be built against a
	// version string, a target name or a platform that its caller invented, and
	// every assertion that target makes about the engine would be about the
	// caller instead.
	//
	// Sorted, because a build passing two of them must refuse the same one every
	// time: map order is random, and a diagnostic that varies between runs of
	// one build is one nobody can act on (I12).
	names := make([]string, 0, len(out))
	for name := range out {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		err := refuseBuiltinArgument(name, where, "BUILD")
		if err != nil {
			return nil, err
		}
	}

	return out, nil
}

// assignment parses `LET name=value` and `SET name=value`.
//
// A value that must be computed by running something - `$(cat version.txt)` -
// is run on the filesystem the recipe has built up to that line, through the
// seam a condition uses. Without a runner it is refused rather than guessed at:
// a build using a value nobody chose is worse than one that stops and says why.
func (p *Plan) assignment(c earthfile.Command, prev *ir.Node, dir string) (string, string, error) {
	if len(c.Args) == 0 {
		return "", "", fmt.Errorf("%s needs a name (%s)", c.Name, loc(c.SourceLocation))
	}

	name, value, ok := strings.Cut(c.Args[0], "=")

	switch {
	case len(c.Args) >= 3 && c.Args[1] == "=":
		name, value = c.Args[0], strings.Join(c.Args[2:], " ")
	case !ok && len(c.Args) >= 2:
		name, value = c.Args[0], strings.Join(c.Args[1:], " ")
	case !ok:
		return "", "", fmt.Errorf("%s %s needs a value (%s)", c.Name, name, loc(c.SourceLocation))
	}

	if strings.Contains(value, "$(") {
		out, err := p.expandCommands(value, prev, dir, string(c.Name), loc(c.SourceLocation))
		if err != nil {
			return "", "", err
		}

		value = out
	}

	return name, unquote(value), nil
}

// envPair parses `ENV NAME=VALUE`, which the parser may hand over as one token
// or as three.
func envPair(c earthfile.Command) (string, string, error) {
	if len(c.Args) == 0 {
		return "", "", fmt.Errorf("ENV needs a name (%s)", loc(c.SourceLocation))
	}

	if len(c.Args) >= 3 && c.Args[1] == "=" {
		return c.Args[0], strings.Join(c.Args[2:], " "), nil
	}

	if name, value, ok := strings.Cut(c.Args[0], "="); ok {
		return name, value, nil
	}

	if len(c.Args) >= 2 {
		// `ENV NAME value`, the space-separated form.
		return c.Args[0], strings.Join(c.Args[1:], " "), nil
	}

	return c.Args[0], "", nil
}

// buildTarget picks the target out of a BUILD, refusing flags this engine
// cannot honour.
//
// `BUILD --platform=linux/amd64 +image` is ordinary in real Earthfiles. Reading
// past the flag and building anyway would produce the wrong architecture and
// report success; reading the flag *as* the target produces a baffling message
// about a malformed reference. Both were observed against this repository's own
// Earthfiles.
// fromTarget reads FROM's options, its reference and its arguments.
//
// Returns an empty reference for `FROM alpine:3.22`, which is an image name
// rather than a target.
// fromSpec is what a FROM line says, after its flags have been read.
//
// A struct rather than a fifth and sixth return value: the image and the target
// reference are alternatives, and a shape that says so is harder to misuse than
// a row of strings whose meaning depends on which are empty. That misuse is
// exactly what went wrong here - the image was taken from the *unparsed*
// arguments while everything else came from the parsed ones.
type fromSpec struct {
	// ref is a target reference; image is a registry image. Exactly one is set.
	ref   string
	image string

	args     map[string]string
	passArgs bool
	platform string
	// allowPrivileged is `FROM --allow-privileged`: this line grants the
	// referenced target privilege, across a repository boundary if need be.
	allowPrivileged bool
}

func fromTarget(c earthfile.Command) (fromSpec, error) {
	var opts cmdopts.From

	rest, err := flagutil.ParseArgsCleaned("FROM", &opts, c.Args)
	if err != nil {
		return fromSpec{}, flagFault("FROM", loc(c.SourceLocation), err)
	}

	allow := opts.AllowPrivileged

	// An image rather than a target. The *parsed* first argument, not the raw
	// one: `FROM --platform=linux/amd64 alpine` names alpine, and reading
	// c.Args[0] here made the reference `--platform=linux/amd64` - an image no
	// registry has - while dropping the platform it was asking for.
	if len(rest) == 0 || !strings.Contains(rest[0], "+") {
		image := ""
		if len(rest) > 0 {
			image = rest[0]
		}

		// **One image, and one only.** `FROM alpine extra` was accepted and the
		// second word dropped, so an author who wrote two images by mistake got
		// the first and no indication (E359).
		//
		// Only for an image: `FROM +target --ARG=value` passes arguments down,
		// and those arrive here as further words. The first version of this
		// check refused them and took five targets out of the corpus, which the
		// ratchet reported before anything else did (E353).
		if len(rest) > 1 {
			return fromSpec{}, fmt.Errorf("FROM (%s): %q is a second image and"+
				" FROM takes one - a build stands on a single base",
				loc(c.SourceLocation), rest[1])
		}

		return fromSpec{image: image, platform: opts.Platform}, nil
	}

	args, err := overrides(rest[1:], loc(c.SourceLocation))
	if err != nil {
		return fromSpec{}, err
	}

	for _, a := range opts.BuildArgs {
		if k, v, ok := strings.Cut(a, "="); ok {
			args[k] = v
		}
	}

	return fromSpec{
		allowPrivileged: allow,
		ref:             rest[0],
		args:            args,
		passArgs:        opts.PassArgs,
		platform:        opts.Platform,
	}, nil
}

// buildTarget reads BUILD's options, its target reference and its arguments.
func buildTarget(c earthfile.Command) (string, map[string]string, bool, cmdopts.Build, error) {
	var opts cmdopts.Build

	rest, err := flagutil.ParseArgsCleaned("BUILD", &opts, c.Args)
	if err != nil {
		return "", nil, false, opts, flagFault("BUILD", loc(c.SourceLocation), err)
	}

	// `--auto-skip` asks for what this engine's cache already does.
	//
	// It skips a target whose dependencies have not changed since a successful
	// build, and that is what a chain key is: a step with identical inputs is
	// served and does not run. **I5 is what makes ignoring it safe** - a cache
	// hint may not change results - which is the reasoning already written
	// beside `SAVE IMAGE --cache-hint`, and this is the same kind of flag under
	// a different name (E484).
	//
	// Accepted rather than refused, for E34's asymmetry: refusing something
	// already implemented costs a working build. `tests/wildcard-build.earth`
	// drives it expecting one.

	if len(rest) == 0 {
		return "", nil, false, opts, fmt.Errorf("BUILD needs a target (%s)", loc(c.SourceLocation))
	}

	args, err := overrides(rest[1:], loc(c.SourceLocation))
	if err != nil {
		return "", nil, false, opts, err
	}

	// --build-arg k=v is another way to write an override, and means the same -
	// including the quoting, which `overrides` resolves and this did not.
	// `escape.earth` passes `FILE="file-with-\+.txt"`, where the backslash is
	// what stops the `+` being read as a target reference; passed through, the
	// target looked for a file called `file-with-\+.txt` and reported it
	// missing, naming a file nobody has.
	for _, a := range opts.BuildArgs {
		if k, v, ok := strings.Cut(a, "="); ok {
			args[k] = unquote(v)
		}
	}

	return rest[0], args, opts.PassArgs, opts, nil
}

// checkPlatform refuses something that is not a platform.
//
// Parsed rather than accepted: `--platform=nonsense/` would otherwise become a
// platform with an empty architecture, which pulls an image for nothing and
// fails much later with a message about a manifest.
func checkPlatform(s, where string) error {
	_, err := platforms.Parse(resolveNative(s))
	if err != nil {
		return fmt.Errorf("%q is not a platform (%s): %w", s, where, err)
	}

	return nil
}

// resolveNative turns the word `native` into the platform this build is
// running on.
//
// Resolved to something concrete rather than left unset, because unset means
// "inherit" and the entire use of the word is to escape an inherited foreign
// platform: `FROM --platform=linux/amd64` followed by `COPY --platform=native`
// wants this machine, and giving it the inherited amd64 would cross-compile
// while reading as if it had not.
func resolveNative(s string) string {
	if s == "native" {
		return runtime.GOOS + "/" + runtime.GOARCH
	}

	return s
}

// targetPlatform is what this target is being built for: a `--platform` when one
// was given, and what the build runs on otherwise.
func (p *Plan) targetPlatform(rs *state) string {
	if rs.platform != "" {
		return resolveNative(rs.platform)
	}

	return p.opt.nativePlatform()
}

// platformOf turns a platform string into the IR's form.
// allowPrivilegedFlag is spelled once, because it is spelled in six places and
// a flag this engine refuses by name is a flag whose name has to match.
const allowPrivilegedFlag = "--allow-privileged"

func platformOf(s string) ir.Platform {
	p, err := platforms.Parse(resolveNative(s))
	if err != nil {
		return ir.Platform{}
	}

	return ir.Platform{OS: p.OS, Arch: p.Architecture, Variant: p.Variant}
}

// wrapRef adds the location of a target reference, unless the error already
// says everything worth saying.
func wrapRef(cmd string, c earthfile.Command, err error) error {
	if _, ok := errors.AsType[*CycleError](err); ok {
		// The loop is the diagnosis. Prefixing it with every hop that led here
		// buries it under a path the reader can already see in the loop.
		return err
	}

	return fmt.Errorf("%s %s (%s): %w", cmd, c.Args[0], loc(c.SourceLocation), err)
}

// unsupported is the I10 refusal: what, where, and what to do instead.
// flagMeanings says what a refused flag asks for, in one clause.
//
// Every entry is taken from `docs/earthfile/earthfile.md` in this repository,
// which is the reference for the language this engine implements. **A flag not
// in there has no entry**, deliberately: a description nobody checked is worse
// than none, because a wrong one sends the reader somewhere there is nothing to
// find and they believe it on the way.
//
// Every entry describes a flag that is refused somewhere. `--symlink-no-follow`
// and `--keep-own` had entries and are *honoured* - measured against the
// shipping engine first (E74, E34) - so their descriptions could never be
// printed, and unreachable text is the one kind that never gets corrected.
// TestNoMeaningDescribesAFlagThatIsNotRefused keeps it that way.
//
// `--keep-ts` is the worked example and no longer has an entry: it was refused
// while this engine did exactly what it asks, the last such refusal has gone,
// and its description went with it. That is the rule working, not an omission.
//
// The refusals were "X is not supported by the native engine" and nothing else,
// which tells a reader the door is shut and nothing about whether they wanted
// to go through it. That is the E68 shape: the refusal named the refusal and
// not the thing refused. It matters because this list has already been wrong in
// the expensive direction - `--keep-ts` was refused while this engine did
// exactly what it asks - and a refusal that explains itself is a refusal
// somebody can contradict.
var flagMeanings = map[string]string{
	"--sharing": "decides whether concurrent builds wait for the cache mount, share it, or each get their own",
	"--network": "isolates the command from the networking stack and the internet",
	"--oidc":    "obtains temporary AWS credentials for the command through a federated session",
	// Documented as *absent from the language*, so this describes what it does
	// in the syntax that has it. The refusal for it is notInLanguage, not
	// unsupported: see TestARefusalForSomethingTheLanguageLacksOffersNoEngineSwitch.
	"--from":             "takes files out of an earlier build stage, as classical Dockerfile syntax does",
	"--privileged":       "lets the command use privileged capabilities",
	"--allow-privileged": "lets a remotely-referenced target request privileged capabilities",
	"--ssh":              "gives the command the host's ssh authentication client",
	"--with-docker":      "starts a Docker daemon for the duration of the command",
	"--interactive-keep": "opens a prompt in the container and keeps what the session changed",
	// Refusing this one is a position rather than a gap: it exists to permit a
	// save outside the directory holding the Earthfile, which is the thing
	// insideProject was written to stop. "Not supported" invites somebody to
	// implement it.
	"--force": "permits a save that writes outside the directory containing the Earthfile",
}

// flagMeaning finds the description for the flag a construct ends with.
//
// Keyed off the construct string because that is what every call site already
// builds - "SAVE ARTIFACT --keep-own", "RUN --ssh" - so a new refusal gets its
// explanation without the refusing code knowing this exists. Constructs that
// are not about a flag at all, like HEALTHCHECK, fall through to "".
func flagMeaning(construct string) (flag, meaning string) {
	fields := strings.Fields(construct)
	if len(fields) == 0 {
		return "", ""
	}

	flag = fields[len(fields)-1]

	return flag, flagMeanings[flag]
}

// ErrRefused marks any construct this engine will not build, of whatever kind.
//
// Three kinds share it - a gap, something the language lacks, a decision - and
// a caller asking only "was this refused?" should not have to know which. The
// question used to be answered by matching "not supported by the native
// engine", which is one kind's wording, so introducing the other two made
// still-refused constructs look supported (E153).
var ErrRefused = errors.New("this construct will not be built by the native engine")

// ErrNotInLanguage is the subset the Earthfile language does not have.
//
// The third kind, and the one that had no sentinel until I10 was written to say
// there are three. Distinct from a gap, because there is no engine to switch to,
// and from a decision, because the way out is a different construct rather than
// nothing (E181).
var ErrNotInLanguage = fmt.Errorf("not part of the Earthfile language: %w", ErrRefused)

// ErrOnPurpose is the subset that is a decision: refused, and arriving nowhere.
//
// Disjoint from ErrUnimplemented, and both distinctions carry weight. The
// corpus report ranks what to build next, and a decision has no place in that
// list; it also has no place under "refused as invalid input", where it landed
// by default and where nobody reads it - the same mistake E151 fixed for
// `--required` ARGs.
var ErrOnPurpose = fmt.Errorf("refused by design: %w", ErrRefused)

// ErrUnimplemented is the subset that is work: a gap that arrives later.
//
// Distinct from ErrRefused because the corpus report ranks what to build next,
// and a decision that arrives nowhere has no place in that list. It wraps
// ErrRefused, so a caller asking the broader question still gets yes.
var ErrUnimplemented = fmt.Errorf("not yet built: %w", ErrRefused)

// refusedOnPurpose declines a construct this engine has decided not to do.
//
// The third kind, and the one that most needed its own words. `unsupported`
// promises "later, and meanwhile elsewhere"; `notInLanguage` promises another
// construct. This promises nothing, because there is nothing coming: the
// construct works, and the engine will not do it.
//
// "Not supported" reads as unfinished, and an unfinished thing is an invitation
// to finish it - which for `SAVE ARTIFACT --force` means deleting a safety
// property because the engine looked incomplete. The table recording flag
// meanings has said so all along: *"Refusing this one is a position rather than
// a gap ... 'Not supported' invites somebody to implement it."*
//
// `position` states what is being defended, so a reader who disagrees knows what
// they would be switching off. The other engine is still named: it does permit
// this, and concealing that is a lie by omission (I10). Disclosure, not advice -
// hence the wording, which does not begin "to build this now".
func refusedOnPurpose(construct, where, position string) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%s is refused by the native engine on purpose", construct)

	if where != "" {
		fmt.Fprintf(&b, " (%s)", where)
	}

	if flag, why := flagMeaning(construct); why != "" {
		fmt.Fprintf(&b, "\n  %s %s", flag, why)
	}

	fmt.Fprintf(&b, "\n  %s", position)
	b.WriteString("\n  the other engine permits it: --engine=buildkit")

	return fmt.Errorf("%s: %w", b.String(), ErrOnPurpose)
}

// notInLanguage refuses a construct Earthfiles do not have, and says what does.
//
// Separate from unsupported because the way out is different, and a wrong way
// out is the expensive kind of wrong. `unsupported` ends "use
// --engine=buildkit", which is right for a native-engine gap; for `COPY --from`
// the other engine refuses identically - docs/earthfile/earthfile.md: *"Although
// this option is present in classical Dockerfile syntax, it is not supported by
// Earthfiles"* - so that line sends the reader to run the build again and get
// the same answer, believing the engine on the way.
//
// `instead` is what does work, and is not optional: this refusal exists to carry
// it. Without one there is nothing here that `unsupported` does not do better.
func notInLanguage(construct, where, instead string) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%s is not part of the Earthfile language", construct)

	if where != "" {
		fmt.Fprintf(&b, " (%s)", where)
	}

	if flag, why := flagMeaning(construct); why != "" {
		fmt.Fprintf(&b, "\n  %s %s", flag, why)
	}

	fmt.Fprintf(&b, "\n  %s", instead)

	return fmt.Errorf("%s: %w", b.String(), ErrNotInLanguage)
}

func unsupported(construct, where, milestone string) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%s is not supported by the native engine", construct)

	if where != "" {
		fmt.Fprintf(&b, " (%s)", where)
	}

	// Before the milestone and the way out, because it is the line that decides
	// whether the reader needs either of them.
	if flag, why := flagMeaning(construct); why != "" {
		fmt.Fprintf(&b, "\n  %s %s", flag, why)
	}

	if milestone != "" {
		fmt.Fprintf(&b, "\n  it arrives at %s", milestone)
	}

	b.WriteString("\n  to build this now, use --engine=buildkit")

	return fmt.Errorf("%s: %w", b.String(), ErrUnimplemented)
}

func loc(s *earthfile.SourceLocation) string {
	if s == nil {
		return ""
	}

	file := s.File
	if file == "" {
		file = "Earthfile"
	}

	return fmt.Sprintf("%s:%d", file, s.StartLine)
}

// uncacheable reports whether any mount puts something in the step's result that
// no key over its inputs describes.
//
// Two kinds, and a cache is neither:
//
//   - **persisted**: `CACHE --persist` copies the mount's contents into the
//     image, so they are part of what the step produces rather than something
//     beside it;
//   - **a secret**: the value is deliberately outside the graph - it has no id
//     in any key and must not be - so a result derived from one cannot be keyed
//     honestly, and caching it would publish the derivation to every later build.
//
// A plain cache mount is an accelerator and is cached (E424). Written as a
// property of each mount rather than "any mount at all", which was the rule that
// made `CACHE` slow down every rebuild.
func uncacheable(mounts []ir.Mount) bool {
	for _, m := range mounts {
		if m.Persist || m.Secret {
			return true
		}
	}

	return false
}

// referenceShaped reports whether a COPY source reads as a target reference that
// forgot its artifact path.
//
// What precedes the last plus decides it: nothing (`+dep`), a path (`../+base`,
// `./sub+dep`) or a name this file imported (`tests+build`). Anything else is a
// filename that contains a plus, which is a thing Earthfiles write and escape
// (E441, E479).
func referenceShaped(src string, imports map[string]string) bool {
	i := strings.LastIndex(src, "+")
	if i < 0 {
		return false
	}

	before := src[:i]

	switch {
	case before == "":
		return true

	case strings.HasPrefix(before, "."), strings.HasPrefix(before, "/"):
		return true

	default:
		_, imported := imports[before]

		return imported
	}
}
