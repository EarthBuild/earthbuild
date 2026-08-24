package interp

import (
	"fmt"
	"maps"
	"strings"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/util/flagutil"
)

// forStatement unrolls a loop into the graph.
//
// Unrolled rather than represented, for the reason a condition is decided
// rather than deferred (green paper §3.4a): the graph stays known before the
// build. A loop in the graph is a graph whose shape depends on something that
// has not run yet, and every key, schedule and diagnostic in this engine rests
// on the shape being settled first. Unrolling also makes each iteration a step
// in its own right, so one changed item invalidates one iteration rather than
// the whole loop.
func (p *Plan) forStatement(st *earthfile.ForStatement, prev *ir.Node, rs *state) (*ir.Node, error) {
	where := loc(st.SourceLocation)

	name, items, err := p.loopItems(st.Args, prev, rs, where)
	if err != nil {
		return nil, err
	}

	// The loop variable is scoped to the loop. Restoring the outer value rather
	// than deleting it, because the name may well have been an ARG before the
	// loop borrowed it, and a loop that quietly unset it would change the
	// meaning of every line after END.
	outer, had := rs.args[name]

	defer func() {
		if had {
			rs.args[name] = outer

			return
		}

		delete(rs.args, name)
	}()

	// Each iteration stands on the one before it: a loop body normally builds on
	// itself, and the order written is the order meant.
	for _, item := range items {
		rs.args[name] = item

		next, err := p.block(st.Body, prev, rs)
		if err != nil {
			return nil, err
		}

		prev = next
	}

	return prev, nil
}

// loopItems reads `[--sep=x] name IN item...` and returns the name and the
// items.
func (p *Plan) loopItems(args []string, prev *ir.Node, rs *state, where string) (string, []string, error) {
	var opts cmdopts.For

	rest, err := flagutil.ParseArgsCleaned("FOR", &opts, args)
	if err != nil {
		return "", nil, flagFault("FOR", where, err)
	}

	if len(rest) < 2 || !strings.EqualFold(rest[1], "IN") {
		return "", nil, fmt.Errorf(
			"FOR at %s: expected `FOR <name> IN <items>`, found %q",
			where, strings.Join(rest, " "))
	}

	name := rest[0]

	// Default separators are the shell's, which is what `IN $list` means when
	// the list came from a variable holding a line or a sentence.
	seps := opts.Separators
	if seps == "" {
		seps = "\n\t "
	}

	var items []string

	for _, tok := range rest[2:] {
		// expandWord: a `$(...)` here is a command line, and its quoting belongs
		// to the shell that will read it (E65).
		expanded := rs.args.expandWord(tok)

		// A list that has to be *computed* is run on the filesystem the recipe
		// has built up to that line, through the same seam a condition uses.
		expanded, err = p.expandCommands(expanded, prev, rs.dir, "FOR", where)
		if err != nil {
			return "", nil, err
		}

		items = append(items, splitAny(expanded, seps)...)
	}

	return name, items, nil
}

// splitAny splits on any of the separator characters, dropping empties.
//
// Empty items are dropped rather than iterated over: `FOR x IN $list` with an
// unset list means no iterations, not one iteration with x empty.
func splitAny(s, seps string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return strings.ContainsRune(seps, r)
	})

	out := make([]string, 0, len(fields))

	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}

	return out
}

// waitStatement runs a block and makes everything after it wait.
//
// The block's own steps are ordinary steps of the target. What WAIT adds is an
// ordering edge from whatever is built *next* to everything the block caused -
// which matters only for the work that is not already sequential: a `BUILD`
// inside the block is a dependency edge rather than a base, so without this
// nothing would make the following step wait for it.
//
// The edges are left pending rather than attached here, because the node they
// belong on does not exist yet. Attaching them to the block's exit would put
// them on a node that *precedes* the block whenever the block built nothing of
// its own, and making a step wait for work that stands on it is a cycle.
func (p *Plan) waitStatement(st *earthfile.WaitStatement, prev *ir.Node, rs *state) (*ir.Node, error) {
	before := len(p.also)

	last, err := p.block(st.Body, prev, rs)
	if err != nil {
		return nil, err
	}

	// Everything the block added as a dependency rather than a base: exactly
	// the steps nothing downstream would otherwise wait for.
	p.pending = append(p.pending, p.also[before:]...)

	return last, nil
}

// tryStatement runs a block whose failure does not stop the build, then a
// FINALLY that reads what it left behind.
//
// The TRY step is marked tolerant, which is what makes the rest possible: it
// still fails the build, but only once everything that had to run has run.
// FINALLY then stands on it in the ordinary way - as the *next* step - because
// what it saves was written by the step that failed. `RUN test > report &&
// false` followed by `SAVE ARTIFACT report` is the whole point, and nothing
// about it works if the failed filesystem is discarded.
//
// CATCH is a side branch rather than part of the chain. It runs commands
// *because* the try failed, so every step in it carries OnFailure naming the
// guarded step, and the build after END carries on from the TRY - threading it
// through the handler would make every later step wait for commands that
// usually do not run at all.
func (p *Plan) tryStatement(st *earthfile.TryStatement, prev *ir.Node, rs *state) (*ir.Node, error) {
	// Gated on the file's own VERSION line. The reference refuses TRY outright
	// without `--try`, so accepting it here produced Earthfiles that build on
	// this engine and nowhere else - which is the quiet way a compatible
	// implementation stops being one (E36).
	err := p.here.features.needs(p.here.features.try, "TRY", "--try", loc(st.SourceLocation))
	if err != nil {
		return nil, err
	}

	before := prev

	tried, err := p.block(st.TryBody, prev, rs)
	if err != nil {
		return nil, err
	}

	// Every step the block added, and no more: tolerance is a property of what
	// TRY guards, so it must not reach anything after END.
	for n := tried; n != nil && n != before; n = firstInput(n) {
		if n.Op.Kind == ir.OpExec || n.Op.Kind == ir.OpHost {
			n.Op.Tolerate = true
		}
	}

	// The handler stands on the failed step, because that is where a failure
	// leaves what is worth inspecting. Only the first command names the guarded
	// step; the rest are skipped by standing on one that was, which is the
	// scheduler's transitive rule rather than a second mechanism here.
	if st.CatchBody != nil {
		caught, err := p.block(*st.CatchBody, tried, rs)
		if err != nil {
			return nil, err
		}

		for n := caught; n != nil && n != tried; n = firstInput(n) {
			if len(n.Inputs) > 0 && n.Inputs[0] == tried {
				n.OnFailure = tried
			}
		}

		// Nothing stands on the handler, so it needs a root of its own or it is
		// planned and never scheduled.
		if caught != tried {
			p.also = appendOnce(p.also, caught)
		}
	}

	if st.FinallyBody == nil {
		return tried, nil
	}

	return p.block(*st.FinallyBody, tried, rs)
}

// firstInput walks back along the chain a block built.
func firstInput(n *ir.Node) *ir.Node {
	if len(n.Inputs) == 0 {
		return nil
	}

	return n.Inputs[0]
}

// withStatement plans `WITH DOCKER ... END`.
//
// Every step in the body is marked as needing a daemon, which puts it in the
// step's identity: `RUN docker images` with a daemon and the same line without
// one are different requests - the first lists images, the second fails to find
// the command - and a cache that could not tell them apart would serve one for
// the other.
//
// The options are refused by name rather than accepted and ignored. `--load`
// builds another target and puts its image in the daemon, so a block that took
// the flag and did nothing would run `docker run` against an image that is not
// there, and blame the Earthfile for it.
func (p *Plan) withStatement(st *earthfile.WithStatement, prev *ir.Node, rs *state) (*ir.Node, error) {
	where := loc(st.SourceLocation)

	if st.Command.Name != earthfile.CmdDocker {
		return nil, unsupported("WITH "+string(st.Command.Name), where, "")
	}

	var opts cmdopts.WithDocker

	rest, err := flagutil.ParseArgsCleaned("WITH DOCKER", &opts, st.Command.Args)
	if err != nil {
		return nil, flagFault("WITH DOCKER", where, err)
	}

	// **WITH DOCKER takes flags and nothing else**, and what was left over was
	// discarded. `WITH DOCKER --cache-id=with space` parses as a cache called
	// `with` and a stray word, and the stray word meant the author wrote
	// something this engine did not do - which is the accepted-and-ignored
	// failure the option refusals above exist to prevent (I10, E358).
	if len(rest) > 0 {
		return nil, fmt.Errorf("WITH DOCKER (%s): %q is not an option this"+
			" construct takes, and WITH DOCKER has no arguments of its own",
			where, rest[0])
	}

	// **Every step of the block, authored or generated.** A `--pull` or a
	// `--load` writes into the same daemon storage the body reads, so a shared
	// cache is a property of the block rather than of the lines inside it - and
	// a generated step that keyed as though the daemon were empty would be
	// served from a cache that is not what it will find (E354).
	// **Restored, not cleared.** A `--load` builds another target while this
	// block is open, and that target may have a `WITH DOCKER` of its own - so
	// blocks nest even though the syntax does not. Clearing at the end emptied
	// the *outer* block's cache when an inner one closed, and every step the
	// outer generated afterwards claimed to share nothing while running against
	// a daemon that shares everything: cacheable, and reading another build's
	// images (E356).
	err = checkCacheID(opts.CacheID, where)
	if err != nil {
		return nil, err
	}

	// They contradict each other. `--isolate` says this block's daemon storage
	// dies with the step; `--cache-id` names storage that outlives it. Honouring
	// one and ignoring the other would do something the author did not ask for
	// either way (I10).
	if opts.Isolate && opts.CacheID != "" {
		return nil, fmt.Errorf(
			"WITH DOCKER (%s): --isolate and --cache-id say opposite things -"+
				" --isolate gives this block a daemon whose storage dies with the"+
				" step, and --cache-id names storage that outlives it", where)
	}

	outer := p.dockerCache
	p.dockerCache = opts.CacheID

	defer func() { p.dockerCache = outer }()

	// Saved and restored for the same reason the cache name is: a `--load`
	// builds another target while this block is open, and that target may have a
	// `WITH DOCKER` of its own, so blocks nest even though the syntax does not.
	// Clearing at the end would tell the *outer* block's remaining steps that
	// they were isolated when they are not (E356).
	outerIso := p.isolateDocker
	p.isolateDocker = opts.Isolate

	defer func() { p.isolateDocker = outerIso }()

	before := prev

	// `--pull` is a step at the top of the block rather than a property of it,
	// because that is what it is: fetching an image is work, it can fail, and
	// it has to happen before anything that uses the image. Being a step is
	// also how it reaches the key - the body stands on it, so what was pulled
	// is part of what the body is.
	// The three options that carry something into a loaded target are the three
	// FROM, BUILD and COPY already take, and they are set the same way here: a
	// construct that spelled them differently would be one people have to learn
	// twice.
	if opts.Platform != "" {
		err := checkPlatform(opts.Platform, where)
		if err != nil {
			return nil, err
		}

		p.passPlatform = opts.Platform
	}

	if opts.PassArgs || len(opts.BuildArgs) > 0 {
		pass := map[string]string{}

		// `--pass-args` hands this target's arguments down; explicit
		// `--build-arg` overrides then win, which is the order FROM and BUILD
		// use and the only order that lets a caller override one of them.
		if opts.PassArgs {
			maps.Copy(pass, rs.args)
		}

		// `--build-arg NAME=VALUE`, which is a different spelling from the
		// `--NAME=VALUE` inside a parenthesised reference - so `overrides`,
		// which reads that one, silently matched nothing here and the argument
		// never arrived.
		for _, kv := range opts.BuildArgs {
			name, value, ok := strings.Cut(kv, "=")
			if !ok || name == "" {
				return nil, fmt.Errorf(
					"WITH DOCKER --build-arg %q (%s): expected NAME=VALUE", kv, where)
			}

			pass[name] = value
		}

		p.passTo = pass
	}

	for _, spec := range opts.Loads {
		next, err := p.dockerLoad(spec, prev, rs, where)
		if err != nil {
			return nil, err
		}

		prev = next
	}

	for _, ref := range opts.Pulls {
		prev = p.dockerPull(ref, prev, rs, where)
	}

	if len(opts.ComposeServices) > 0 && len(opts.ComposeFiles) == 0 {
		return nil, fmt.Errorf(
			"WITH DOCKER --service (%s): there is no --compose file to find those services in"+
				"\n  name one: WITH DOCKER --compose docker-compose.yml --service %s",
			where, opts.ComposeServices[0])
	}

	if len(opts.ComposeServices) > 0 && len(opts.ComposeFiles) == 0 {
		return nil, fmt.Errorf(
			"WITH DOCKER --service (%s): there is no --compose file to find those services in"+
				"\n  name one: WITH DOCKER --compose docker-compose.yml --service %s",
			where, opts.ComposeServices[0])
	}

	if len(opts.ComposeFiles) > 0 {
		// The block's own commands run `docker compose ps` and the like, and
		// compose takes its project from the working directory's basename -
		// which for a step whose WORKDIR is the image root is empty, reported
		// as "project name must not be empty". Naming it in the environment
		// gives the body the same project this block brought up, so a bare
		// `docker compose ps` means what the author obviously intended.
		//
		// In ε, so it is in the key: a body that sees a different project is
		// looking at different containers.
		restore := rs.env
		rs.env = withEnv(rs.env,
			"COMPOSE_PROJECT_NAME", composeProject(opts.ComposeFiles),
			"COMPOSE_FILE", strings.Join(opts.ComposeFiles, ":"))

		defer func() { rs.env = restore }()

		prev = p.composeUp(opts.ComposeFiles, opts.ComposeServices, prev, rs, where)
	}

	// What was already running, before the body starts anything. Recorded
	// rather than assumed empty, because the VM outlives the build and another
	// build's containers are none of this block's business to remove.
	prev = p.dockerKnown(prev, rs, where)

	last, err := p.block(st.Body, prev, rs)
	if err != nil {
		return nil, err
	}

	// Down after the block, because the daemon outlives the build: a service
	// left running is still there for the next build, and every one after it.
	//
	// On the way out only. A block whose commands fail stops the build there,
	// and its services stay up - which is a real hole, and a smaller one than
	// it looks, because the next build with the same compose file brings them
	// up again over the top. Closing it properly means tolerating the body's
	// failure so the teardown still runs, which is TRY's machinery and TRY's
	// error reporting, and is a change to make deliberately rather than as part
	// of this.
	if len(opts.ComposeFiles) > 0 {
		last = p.composeDown(opts.ComposeFiles, last, rs, where)
	}

	// And anything else the block started. `compose down` takes away a
	// project's services; a bare `docker run -d` is the commoner case in real
	// Earthfiles and was taken away by nothing - so it stayed running, holding
	// its ports, for every build that followed on this machine.
	//
	// Twice: once for the ordinary path, and once guarded on the body failing.
	// Exactly one of the two ever runs.
	//
	// The guarded copy was impossible until the scheduler learnt to unwind. An
	// OnFailure edge says "run only if that step failed", which is right, but
	// the build abandoned itself at the failure and nothing guarded by it was
	// reached; the only way to get a handler to run was TRY's tolerance, which
	// has no end. Now handlers run during unwind, the build still fails with
	// the error it had, and nothing after END runs.
	//
	// The two differ in the *identity*, not only the guard: OnFailure is
	// deliberately absent from a node's key - it decides whether a step runs,
	// never what it computes - so two teardowns alike in everything else are
	// one node, and Graph.Nodes() folds them into a single step.
	failed := p.dockerClean(last, rs, where, true)
	failed.OnFailure = last
	p.also = appendOnce(p.also, failed)

	last = p.dockerClean(last, rs, where, false)

	// Every step the block added, and no more: the daemon is a property of what
	// the block wraps and must not reach anything after END.
	for n := last; n != nil && n != before; n = firstInput(n) {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		n.Op.Docker = true

		// **A shared daemon cache makes the block uncacheable, and that is not
		// a limitation - it is what sharing means.** The daemon is given
		// storage an earlier build wrote, so what these steps produce is not a
		// function of their inputs, which is the condition `--no-cache` exists
		// for (I3).
		//
		// A block naming no cache starts with an empty daemon and stays
		// cacheable, which is the mode that wants no flag: reproducible, and
		// what a test looking for cache misses in this engine needs.
		if opts.CacheID != "" {
			n.Op.DockerCache = opts.CacheID
		}

		// **Sharing is the default, so not-isolated is the uncacheable case.**
		//
		// This also covers `--cache-id`, which used to set `NoCache` itself: the
		// two options are refused together above, so a block naming a cache is
		// never isolated and is caught here. The mutation sweep found the second
		// assignment surviving deletion and it was removed rather than kept as
		// defence in depth - two mechanisms for one rule is one mechanism and one
		// thing that can drift (E371, E382).
		// A block that said nothing may be handed a daemon an outer step has
		// been using (E381), and what it produces then depends on what that
		// other build left behind rather than on this step's inputs (I3).
		//
		// `--isolate` is the only mode whose result can be reused: its daemon
		// starts empty and dies with the step, because nothing is mounted (E365).
		n.Op.IsolateDocker = opts.Isolate
		if !opts.Isolate {
			n.Op.NoCache = true
		}
	}

	return last, nil
}

// dockerPull is `--pull <ref>`: an ordinary step that fetches an image into the
// daemon.
//
// It writes nothing to the step's own filesystem - the image goes into the
// daemon, which is outside it - so its layer is empty and standing on it costs
// nothing. That is what makes expressing it as a step rather than as a flag on
// the block work: the ordering, the failure and the identity all come from
// machinery that already exists.
func (p *Plan) dockerPull(ref string, prev *ir.Node, rs *state, where string) *ir.Node {
	return &ir.Node{
		Platform: platformOf(rs.platform),
		Op: ir.Op{
			Kind: ir.OpExec,
			// Shell form, as every other step in the block is: the client is
			// mounted where the sandbox image keeps it and found on PATH, which
			// is the image's business rather than this engine's. Handing the
			// reference to a shell grants nothing an author does not already
			// have - the next line of the block is a RUN.
			Args:   shell("docker pull " + ref),
			Dir:    rs.dir,
			User:   rs.user,
			Env:    rs.env,
			Docker: true,
		},
		Inputs: []*ir.Node{prev},
		Meta:   ir.Meta{Source: where, Description: "docker pull " + ref},
	}
}

// dockerLoad is `--load [name=]+target`: build that target, write its image,
// and put it in the daemon.
//
// Two steps, because two things happen in two places. The layout is written on
// the machine running the build, from layer directories and a configuration it
// holds; the load happens inside the sandbox, against a daemon. Splitting them
// is not ceremony - a single step would have to be half host and half guest,
// which is the one thing this engine's step model does not express.
//
// The body stands on the load, so what was loaded is part of what the body is.
func (p *Plan) dockerLoad(spec string, prev *ir.Node, rs *state, where string) (*ir.Node, error) {
	name, ref := splitLoad(spec)

	from, err := p.loadSource(ref, where)
	if err != nil {
		return nil, fmt.Errorf("WITH DOCKER --load %s (%s): %w", spec, where, err)
	}

	if name == "" {
		name = p.imageOf(from)
		if name == "" {
			return nil, fmt.Errorf(
				"WITH DOCKER --load %s (%s): %s saves no image, so there is nothing to load"+
					"\n  give it a SAVE IMAGE, or name one here: --load myimage:latest=%s",
				spec, where, ref, ref)
		}
	}

	pack := &ir.Node{
		Platform: platformOf(rs.platform),
		Op: ir.Op{
			Kind: ir.OpPackImage, Args: []string{name},
			// What the target declared about how the image runs. Without it
			// the layers were loaded and `docker run` had no command.
			Image: p.configOf(from),
		},
		Inputs: []*ir.Node{from},
		Meta:   ir.Meta{Source: where, Description: "pack image " + name},
	}

	// The archive's path comes from the packing step's identity, which both
	// sides can compute: the host writes it into the store, the guest reads it
	// from the same store at its own path.
	archive := exec.PackedImagePath(pack.ID())

	load := &ir.Node{
		Platform: platformOf(rs.platform),
		Op: ir.Op{
			Kind:   ir.OpExec,
			Args:   shell("docker load -i " + archive),
			Dir:    rs.dir,
			User:   rs.user,
			Env:    rs.env,
			Docker: true,
			// The step runs chrooted into its own overlay, so the archive has
			// to be *in* it. Visible to the sandbox is not the same as
			// reachable from the step, and the difference showed up as a
			// missing file that was demonstrably there.
			Mounts: []ir.Mount{{Sandbox: archive, Target: archive, ReadOnly: true}},
		},
		// prev is what the step stands on; the packed image is a *source* - read,
		// keyed, and never stacked. Making it an input instead merged the
		// target's whole layer stack into this step, and since both share a
		// base the stack then named one layer twice, which overlayfs refuses
		// outright. The distinction between standing on something and reading
		// it is exactly this.
		Inputs:  []*ir.Node{prev},
		Sources: []*ir.Node{pack},
		Meta:    ir.Meta{Source: where, Description: "docker load " + name},
	}

	return load, nil
}

// imageOf is the reference a target saves, if it saves one.
// configOf is what the image produced by a step declared about running.
//
// Nil when the step saves no image, which is a legitimate case: `--load
// name=+target` may name an image the target never declared, and packing its
// layers under a name of the caller's choosing is exactly what was asked for.
func (p *Plan) configOf(n *ir.Node) *ir.ImageConfig {
	for _, img := range p.Images {
		if img.From == nil || img.From.ID() != n.ID() {
			continue
		}

		return img.Config.ToIR()
	}

	return nil
}

func (p *Plan) imageOf(n *ir.Node) string {
	for _, img := range p.Images {
		if img.From != nil && img.From.ID() == n.ID() {
			return img.Ref
		}
	}

	return ""
}

// splitLoad separates `name=` from the target reference.
//
// Only an `=` before any bracket separates them, because a parenthesised
// reference carries build arguments of its own: `--load (+image --INDEX=1)` has
// an `=` in it that has nothing to do with naming the image, and cutting at the
// first one produced a reference of `1)` and a diagnosis about a target nobody
// wrote.
// Each half is unquoted, because quotes are syntax and the halves are values.
// `--load=name="(+t --a=1)"` quotes only the reference, and the quote then
// travelled into it: loadSource decides between the two forms a reference can
// take by asking whether it starts with `(`, this one started with `"`, and the
// whole string went to the target resolver - which reported `"(` as an import
// alias and advised declaring one. `--load="name=(+t --a=1)"`, one line above
// it in the same fixture, has always worked because the parser strips quotes
// that wrap a whole flag value. Two spellings of one thing, one of them broken.
//
// Green paper A6 is in the specification because of this mistake made elsewhere
// (see unquote): a quoted token is a value with delimiters, not a value that
// begins with a quote.
func splitLoad(spec string) (name, ref string) {
	open := strings.IndexByte(spec, '(')

	eq := strings.IndexByte(spec, '=')
	if eq < 0 || (open >= 0 && open < eq) {
		return "", unquote(spec)
	}

	return unquote(spec[:eq]), unquote(spec[eq+1:])
}

// loadSource resolves the target whose image is loaded, in either form a
// reference can take.
func (p *Plan) loadSource(ref, where string) (*ir.Node, error) {
	if !strings.HasPrefix(ref, "(") {
		n, _, err := p.targetRef(ref, where)

		return n, err
	}

	// `(+target --arg=value ...)`: the parser has already merged this into one
	// token, so it arrives whole rather than as flags on the WITH DOCKER.
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(ref, "("), ")"))
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty reference in parentheses (%s)", where)
	}

	args, err := overrides(fields[1:], where)
	if err != nil {
		return nil, err
	}

	p.passTo = args

	n, _, err := p.targetRef(fields[0], where)

	return n, err
}

// composeFlags is the `-p name -f file...` sequence shared by up and down.
//
// Every file the block named, in the order written, because compose merges them
// in that order and a different order is a different configuration.
//
// The project name is explicit and derived from those files. Compose otherwise
// takes it from the working directory's basename, and a step whose WORKDIR is
// the image root has no basename - which it reports as "project name must not
// be empty", a message with nothing in it to connect to an Earthfile. Deriving
// it from the files is also what makes `down` find what `up` started: the two
// commands agree because they compute the same name from the same input.
func composeFlags(files []string) string {
	var b strings.Builder

	fmt.Fprintf(&b, " -p %s", composeProject(files))

	for _, f := range files {
		b.WriteString(" -f ")
		b.WriteString(f)
	}

	return b.String()
}

// dockerKnown records the containers that exist before a block runs.
//
// The list is taken inside the sandbox and read there again, so it never
// reaches a layer or a key: what is running on a machine is exactly the sort of
// thing a key must not depend on.
func (p *Plan) dockerKnown(prev *ir.Node, rs *state, where string) *ir.Node {
	// Seeded with a line that is not a container id, so the file is never
	// empty. busybox `grep -vxF -f` on an empty pattern file matches *nothing*
	// - where GNU grep matches everything - so on a clean machine, which is the
	// ordinary case, the cleanup below removed nothing at all and this fix
	// would have shipped doing nothing. Container ids are hex, so no id can
	// ever equal the sentinel under -x.
	cmd := "{ echo __none__; docker ps -aq; } > " + containerList(where)

	return p.dockerStep(cmd, "docker containers before", prev, rs, where)
}

// dockerClean removes the containers the block started, and only those.
//
// The difference against the list taken before it, so a container another build
// is using is left alone - the daemon is shared, and removing something that is
// not ours would be a worse fault than the one this fixes.
//
// Written as a loop rather than `xargs -r`, which busybox does not reliably
// have, and ending in `true` because a failed cleanup must not fail a build
// that has already produced its result.
func (p *Plan) dockerClean(prev *ir.Node, rs *state, where string, onFailure bool) *ir.Node {
	list := containerList(where)

	cmd := "docker ps -aq | grep -vxF -f " + list +
		" | while read id; do docker rm -f \"$id\" >/dev/null 2>&1; done; true"

	desc := "docker containers after"

	// The failure-path copy has to differ *in the identity*, not merely in when
	// it runs. OnFailure is deliberately absent from a node's key - it decides
	// whether a step runs, never what it computes - so two teardowns alike in
	// every other way are one node, and Graph.Nodes() folds them into a single
	// step that keeps whichever guard it was built with.
	//
	// A shell comment is the smallest honest difference: it changes the argv,
	// so the two are different steps, and it says which is which in any log
	// that prints the command.
	if onFailure {
		cmd += " # after a failed block"
		desc = "docker containers after a failure"
	}

	return p.dockerStep(cmd, desc, prev, rs, where)
}

// containerList names the file a block keeps its "before" list in.
//
// Named after the block, so two blocks in one build do not read each other's
// list. In /tmp inside the sandbox, which is the machine's own space rather
// than the step's filesystem.
func containerList(where string) string {
	h := ir.NewHasher()
	h.Str(where)

	return "/tmp/earthbuild-containers-" + h.Sum().String()[:8]
}

// composeUp brings a block's services up before its commands run.
//
// `--wait` is not optional. `docker compose up -d` returns when containers have
// started rather than when they are ready, and the first line of a block like
// this is usually something that connects to one - so without it the failure is
// a connection refused that succeeds on a retry, which is the least actionable
// kind of flake there is.
func (p *Plan) composeUp(files, services []string, prev *ir.Node, rs *state, where string) *ir.Node {
	cmd := "docker compose" + composeFlags(files) + " up -d --wait"
	if len(services) > 0 {
		cmd += " " + strings.Join(services, " ")
	}

	return p.dockerStep(cmd, "compose up", prev, rs, where)
}

// composeDown takes them away again.
func (p *Plan) composeDown(files []string, prev *ir.Node, rs *state, where string) *ir.Node {
	return p.dockerStep("docker compose"+composeFlags(files)+" down", "compose down", prev, rs, where)
}

// dockerStep is a command run against the block's daemon.
func (p *Plan) dockerStep(cmd, desc string, prev *ir.Node, rs *state, where string) *ir.Node {
	return &ir.Node{
		Platform: platformOf(rs.platform),
		Op: ir.Op{
			Kind:   ir.OpExec,
			Args:   shell(cmd),
			Dir:    rs.dir,
			User:   rs.user,
			Env:    rs.env,
			Docker: true,
			// The block's shared cache, if it has one: a generated step writes
			// into the same daemon storage the body reads (E354).
			DockerCache: p.dockerCache,
			// And the block's isolation, for the same reason: a `--pull` puts an
			// image into the daemon the body will use, so it is the same daemon
			// and the same question about whether anything else has been in it.
			//
			// Uncacheable unless isolated, which is the block's rule applied to
			// the steps the block generates - a generated step keying as though
			// its daemon were empty is served a result from a cache that will
			// not match what it finds (E381).
			IsolateDocker: p.isolateDocker,
			NoCache:       p.dockerCache != "" || !p.isolateDocker,
		},
		Inputs: []*ir.Node{prev},
		Meta:   ir.Meta{Source: where, Description: desc + ": " + cmd},
	}
}

// composeProject names the compose project this block brings up.
//
// `default`, and not a name of our choosing, because **the project name is
// visible to the Earthfile**. Compose prefixes every network it creates with
// it, so a compose file declaring `java/part6_default` produces a network
// called `<project>_java/part6_default` - and the Earthfile beside it writes
// `docker run --network=default_java/part6_default` by hand, in a RUN.
//
// It was a hash of the compose files, which is better isolation and breaks
// every Earthfile that names a network: the container came up on
// `earthbuild-9f86d081_java/part6_default` while the RUN two lines later asked
// for a network nobody had created. Three of the three tutorials that name a
// network expect `default`.
//
// What the isolation was protecting against is narrower than it looks. `up` and
// `down` agree because both compute the same name, and a daemon belongs to a
// sandbox rather than to the machine - so two blocks collide only if they share
// a store *and* run at the same time, which no build currently does. If that
// changes, the name has to be negotiated with whatever Earthfiles expect rather
// than chosen freely.
func composeProject(_ []string) string { return "default" }

// withEnv returns the environment with some names set, leaving the original
// alone - a block's additions must not outlive it.
func withEnv(env map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(env)+len(kv)/2)
	maps.Copy(out, env)

	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}

	return out
}

// cacheIDLimit is how long a shared cache's name may be.
//
// A name, not a sentence: it ends up as a directory this engine composes, and
// every filesystem has a limit on a component. Sixty-four is comfortably under
// the smallest of them and is more than anybody needs to tell two caches apart.
const cacheIDLimit = 64

// checkCacheID refuses a shared cache's name that would not be one.
//
// **It became an input when it stopped being refused** (E354), and it is the
// kind that ends up in a path: a shared daemon's storage has to live somewhere,
// and where is derived from the name. `--cache-id=../../etc` is a traversal in a
// mount that does not exist yet, which is the best moment to refuse it - here,
// at the line that wrote it, where a refusal names the file and the flag rather
// than a path this engine composed (I10, E358).
//
// An empty name is not a name: `--cache-id=` shares nothing, which is the
// isolated default said out loud.
func checkCacheID(id, where string) error {
	if id == "" {
		return nil
	}

	if len(id) > cacheIDLimit {
		return fmt.Errorf("WITH DOCKER --cache-id (%s): %d characters, and a"+
			" cache name may be %d - it names a directory", where, len(id),
			cacheIDLimit)
	}

	if id == "." || id == ".." {
		return fmt.Errorf("WITH DOCKER --cache-id=%s (%s): that names a"+
			" directory rather than a cache", id, where)
	}

	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("WITH DOCKER --cache-id=%s (%s): %q is not"+
				" allowed in a cache name - letters, digits, dot, dash and"+
				" underscore are, because the name becomes a directory",
				id, where, r)
		}
	}

	return nil
}
