package interp

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// fromDockerfile builds a Dockerfile as this target's base.
//
// Translated into the commands this interpreter already runs, rather than
// handed to another builder. A Dockerfile's FROM, RUN, COPY, ENV and WORKDIR
// mean what the Earthfile spellings of them mean, so they can be the same
// steps - which is the whole argument for doing it this way: the Dockerfile's
// contents decide the keys, its steps land in the same layer store, and a build
// that changes one line of it re-runs one step rather than everything.
//
// Delegating to `docker build` would have been less code and would have put the
// result outside every guarantee this engine makes: a daemon's cache is not
// keyed by anything here, so the result could not be cached, shared or
// reproduced.
func (p *Plan) fromDockerfile(c earthfile.Command, prev *ir.Node, rs *state) (*ir.Node, error) {
	where := loc(c.SourceLocation)

	// Args are what follows `FROM DOCKERFILE`, the two words being one token in
	// the grammar.
	opt, err := dockerfileArgs(c.Args, where)
	if err != nil {
		return nil, err
	}

	// The context is where the Dockerfile's own COPY reads from. A target's
	// output can be it: `FROM DOCKERFILE -f ./Dockerfile +context/*` means
	// build that target and let the Dockerfile read what it produced, which is
	// how a Dockerfile is fed something this build made rather than something
	// on disk.
	var fromTarget *ir.Node

	dir := p.here.dir

	if strings.Contains(opt.context, "+") {
		// `+context/*` names the target and everything it produced; the target
		// alone is what has to be resolved, and its whole output is the
		// context, so the trailing pattern says nothing this needs.
		ref := opt.context
		if i := strings.LastIndex(ref, "/"); i > strings.Index(ref, "+") {
			ref = ref[:i]
		}

		n, _, targetErr := p.targetRef(ref, where)
		if targetErr != nil {
			return nil, fmt.Errorf("FROM DOCKERFILE %s (%s): %w", opt.context, where, targetErr)
		}

		fromTarget = n
	} else {
		dir = filepath.Join(p.here.dir, opt.context)
	}

	// The Dockerfile itself always comes from this machine, beside the
	// Earthfile: the context is what the *build* reads and the Dockerfile is
	// what says how to read it, so looking for it in the target's output would
	// need that target built before anything could be parsed.
	//
	// Which is a real limit, and it is said here rather than discovered as a
	// missing file. Two shapes ask for a Dockerfile that does not exist yet:
	// `-f` naming an artifact, and a target as the context with nothing saying
	// where the Dockerfile is - the reference looks in the context, so that is
	// the target's output too. The engine used to read `Dockerfile` beside the
	// Earthfile instead, and on a case-insensitive filesystem that found the
	// corpus's `tests/dockerfile/` *directory*: a diagnosis about the wrong
	// file, in a directory the author never named (E478).
	file := filepath.Join(p.here.dir, opt.path)
	if fromTarget == nil {
		file = filepath.Join(dir, opt.path)
	}

	// A Dockerfile that does not exist yet, and the caller may know how to make
	// it exist.
	//
	// Two shapes ask for one: `-f` naming an artifact, and a target as the
	// context with nothing saying where the Dockerfile is - the reference looks
	// in the context, so that is the target's output too.
	//
	// **The interpreter cannot build it and does not try.** It asks whoever
	// called it, exactly as it does for a condition it cannot decide and a
	// repository it cannot fetch, and a caller who supplied nothing gets a
	// refusal saying so rather than one saying the engine cannot do this
	// (E478, E487).
	if from := dockerfileFromTarget(opt, fromTarget); from != "" {
		if p.opt.artifacts == nil {
			return nil, fmt.Errorf(
				"FROM DOCKERFILE at %s: %s is produced by %s, and this plan was"+
					" made without anywhere to build it"+
					"\n  the Dockerfile is parsed while planning, so it has to"+
					" exist before the plan does"+
					"\n  name one that is already on disk -"+
					" `-f ./Dockerfile %s` - if this plan cannot run anything:"+
					" %w",
				where, opt.path, from, opt.context, ErrNotProvided)
		}

		made, artifactsErr := p.opt.artifacts(from, where)
		if artifactsErr != nil {
			return nil, fmt.Errorf(
				"FROM DOCKERFILE at %s: %s produces the Dockerfile, and: %w",
				where, from, artifactsErr)
		}

		// The name inside what the target produced. `-f +gen/other.Dockerfile`
		// names the artifact; a context-only reference means the usual name.
		file = filepath.Join(made, filepath.Base(opt.path))
	}

	src, err := os.ReadFile(file) //nolint:gosec // a path the Earthfile named
	if err != nil {
		return nil, fmt.Errorf(
			"FROM DOCKERFILE at %s: cannot read %s: %w"+
				"\n  the path is relative to the build context, and -f names a different file",
			where, opt.path, err)
	}

	stages, meta, err := dockerfileStages(src, where)
	if err != nil {
		return nil, err
	}

	// `--build-arg` supplies values for the Dockerfile's own ARGs, exactly as a
	// build argument does for a target. Restored afterwards, because they belong
	// to this Dockerfile and not to the rest of the Earthfile.
	if len(opt.args) > 0 {
		restore := rs.supplied
		rs.supplied = withEnv(rs.supplied, flatten(opt.args)...)

		defer func() { rs.supplied = restore }()
	}

	sel, err := selectStage(stages, opt.target, where)
	if err != nil {
		return nil, err
	}

	b := &dockerfileBuild{
		plan: p, stages: stages, built: map[string]*ir.Node{},
		where: where, context: fromTarget,
		globals: globalArgs(meta, opt.args),
	}

	return b.stage(sel, prev, rs, nil)
}

// dockerfileBuild builds a Dockerfile's stages, and remembers what each one
// ended at.
//
// Stages are built on demand rather than in order, because only the ones the
// selected stage depends on should run at all: building the rest would do work
// the Earthfile never asked for, and on a file with a `test` stage that is
// precisely the work somebody excluded on purpose.
type dockerfileBuild struct {
	// globals are the Dockerfile's own arguments declared before the first
	// stage, which Docker makes visible to FROM lines.
	globals map[string]string

	plan   *Plan
	stages []instructions.Stage
	built  map[string]*ir.Node
	where  string
	// context is the target whose output the Dockerfile's COPY reads from, when
	// a target was named instead of a directory. Nil means the ordinary case:
	// files on this machine.
	context *ir.Node
}

// stage builds one stage and returns the node it ends at.
//
// `pending` is the chain of stage names currently being built, which is what
// makes a loop between stages a diagnosis rather than a stack overflow.
func (b *dockerfileBuild) stage(
	st instructions.Stage, prev *ir.Node, rs *state, pending []string,
) (*ir.Node, error) {
	if st.Name != "" {
		if n, done := b.built[strings.ToLower(st.Name)]; done {
			return n, nil
		}

		for _, name := range pending {
			if strings.EqualFold(name, st.Name) {
				return nil, fmt.Errorf(
					"FROM DOCKERFILE at %s: the stages %s form a loop",
					b.where, strings.Join(append(pending, st.Name), " -> "))
			}
		}

		pending = append(pending, st.Name)
	}

	// A stage's base is either another stage or an image. Resolved here rather
	// than translated to a FROM, because an Earthfile has no way to say "stand
	// on that node" - and a stage name handed to FROM as an image reference
	// would be pulled from a registry.
	//
	// Declared without a value because both branches below set one. It read
	// `base := prev`, which is never what is used and tells a reader the base
	// falls back to the previous stage - which is exactly the thing this engine
	// must not do, since a Dockerfile stage inherits nothing from the one
	// before it but the files it is given.
	var base *ir.Node

	// Docker's predefined arguments reach a stage reference without being
	// declared, and a multi-platform Dockerfile is written around that:
	// `FROM binaries-$TARGETOS`. Left unexpanded it is not a stage name, so the
	// lookup misses and the engine tries to pull it from a registry (E64).
	baseName := expandWith(
		expandPredefined(st.BaseName, b.plan.targetPlatform(rs), b.plan.opt.nativePlatform()),
		b.globals)

	if other, ok := b.find(baseName); ok {
		n, err := b.stage(other, prev, rs, pending)
		if err != nil {
			return nil, err
		}

		base = n
	} else {
		n, err := b.plan.command(earthfile.Command{
			Name: earthfile.CmdFrom, Args: []string{baseName},
			SourceLocation: c(b.where),
		}, prev, rs)
		if err != nil {
			return nil, err
		}

		base = n
	}

	// Each stage gets its own environment and working directory: they are the
	// stage's, and a later stage inherits nothing from an earlier one but the
	// files it is given.
	//
	// The configuration is its own too, and starts empty for the same reason -
	// a stage's VOLUME is the stage's, not the caller's.
	sub := *rs
	sub.env = map[string]string{}
	sub.dir = ""
	sub.user = ""
	sub.cfg = Config{Labels: map[string]string{}, Env: map[string]string{}}

	// **And its own record of what it declared.** A Dockerfile's stages are
	// separate scopes for ARG, so the same name may be declared in every one of
	// them - which a multi-platform Dockerfile does as a matter of course, once
	// per stage, because that is the only way a stage can see it.
	//
	// `sub := *rs` copies a map *header*, so every stage shared one map and the
	// second stage to declare a name was refused for redeclaring it. The rule
	// doing the refusing is an Earthfile rule and a good one - within a recipe a
	// second ARG really does nothing (E438) - and it does not reach across
	// stages. The line above already resets `cfg` for the same reason; this one
	// was missed, and nothing exercised two stages declaring one name until
	// `FROM DOCKERFILE` met a Dockerfile with eight of them (E584).
	sub.declared = map[string]bool{}

	for _, instr := range st.Commands {
		n, err := b.instruction(instr, base, &sub, pending)
		if err != nil {
			return nil, err
		}

		base = n
	}

	// What the stage declared about the image goes back to the caller, because
	// `FROM DOCKERFILE` makes that stage the target's base and a base image's
	// configuration is part of what it is - the same rule `FROM +target`
	// follows (E32).
	//
	// It had to be copied back explicitly: the stage runs against `*rs`, a
	// *copy*, so `VOLUME` and `EXPOSE` - which append to slices - were lost the
	// moment the stage returned, while `LABEL` survived because a map header is
	// shared. A Dockerfile's ports and volumes silently did not reach the image
	// it built, and the asymmetry between them and labels is what gave it away.
	rs.cfg = sub.cfg

	if st.Name != "" {
		b.built[strings.ToLower(st.Name)] = base
	}

	return base, nil
}

// instruction applies one Dockerfile instruction to a stage's chain.
func (b *dockerfileBuild) instruction(
	instr instructions.Command, prev *ir.Node, rs *state, pending []string,
) (*ir.Node, error) {
	// `COPY --from=<stage>` is the only instruction that cannot be said as an
	// Earthfile command: it reads another stage's filesystem, and the reference
	// is a node rather than a name anything can resolve. Built directly, as a
	// *source* - read and never stacked, which is the same distinction
	// `COPY +target/artifact` rests on, and the difference between carrying one
	// file out of a builder and carrying the whole builder.
	if cp, ok := instr.(*instructions.CopyCommand); ok && cp.From != "" {
		// The same expansion as a stage's base: `COPY --from=tools-$TARGETOS`
		// names a stage the same way, and substituting at one and not the other
		// would build the stages correctly and copy out of a registry.
		fromName := expandWith(
			expandPredefined(cp.From, b.plan.targetPlatform(rs), b.plan.opt.nativePlatform()),
			b.globals)

		// A name that matches no stage is an image reference, which Docker
		// allows and which buildkit's own Dockerfile uses to take the qemu
		// binaries out of `tonistiigi/binfmt@sha256:...`. The two are not
		// distinguishable by syntax, so the stage lookup decides (E64).
		var src *ir.Node

		if from, found := b.find(fromName); found {
			n, err := b.stage(from, nil, rs, pending)
			if err != nil {
				return nil, err
			}

			src = n
		} else {
			n, err := b.plan.command(earthfile.Command{
				Name: earthfile.CmdFrom, Args: []string{fromName},
				SourceLocation: c(b.where),
			}, nil, rs)
			if err != nil {
				return nil, err
			}

			src = n
		}

		if len(cp.SourcePaths) != 1 {
			return nil, fmt.Errorf(
				"COPY --from at %s takes one source here, and was given %d",
				b.where, len(cp.SourcePaths))
		}

		return &ir.Node{
			Platform: platformOf(rs.platform),
			Op: ir.Op{
				Kind: ir.OpFile,
				Args: []string{cp.SourcePaths[0], resolveDest(cp.DestPath, rs.dir)},
				Dir:  rs.dir, User: rs.user,
			},
			Inputs:  []*ir.Node{prev},
			Sources: []*ir.Node{src},
			Meta: ir.Meta{
				Source:      b.where,
				Description: "COPY --from=" + cp.From + " " + cp.SourcePaths[0] + " " + cp.DestPath,
			},
		}, nil
	}

	// An ordinary COPY when the context is a target reads from that target's
	// output rather than from this machine - which no Earthfile command can
	// say, for the same reason `--from` cannot.
	if cp, ok := instr.(*instructions.CopyCommand); ok && b.context != nil {
		if len(cp.SourcePaths) != 1 {
			return nil, fmt.Errorf(
				"COPY at %s takes one source when the context is a target, and was given %d",
				b.where, len(cp.SourcePaths))
		}

		return &ir.Node{
			Platform: platformOf(rs.platform),
			Op: ir.Op{
				Kind: ir.OpFile,
				Args: []string{cp.SourcePaths[0], resolveDest(cp.DestPath, rs.dir)},
				Dir:  rs.dir, User: rs.user,
			},
			Inputs:  []*ir.Node{prev},
			Sources: []*ir.Node{b.context},
			Meta: ir.Meta{
				Source:      b.where,
				Description: "COPY " + cp.SourcePaths[0] + " " + cp.DestPath,
			},
		}, nil
	}

	cmd, err := translate(instr, b.where)
	if err != nil {
		return nil, err
	}

	// Everything else goes through the ordinary path, so every rule this
	// interpreter has learnt - quoting, keys, mounts, working directories -
	// applies without being restated here.
	return b.plan.command(cmd, prev, rs)
}

// find locates a stage by name.
func (b *dockerfileBuild) find(name string) (instructions.Stage, bool) {
	for _, st := range b.stages {
		if st.Name != "" && strings.EqualFold(st.Name, name) {
			return st, true
		}
	}

	return instructions.Stage{}, false
}

// dockerfileOptions is what `FROM DOCKERFILE` was given.
type dockerfileOptions struct {
	// pathGiven says `-f` was written. Distinct from `path` being non-empty,
	// which it always is: without the flag the Dockerfile is looked for in the
	// build context under its usual name, and *where* that context is decides
	// whether this engine can read it at all (E478).
	pathGiven bool
	path      string
	context   string
	target    string
	args      map[string]string
}

// dockerfileArgs reads `[-f <path>] [--target <stage>] [--build-arg N=V] <context>`.
func dockerfileArgs(args []string, where string) (dockerfileOptions, error) {
	opt := dockerfileOptions{path: "Dockerfile", args: map[string]string{}}

	value := func(i *int, flag string) (string, bool) {
		if v, ok := strings.CutPrefix(args[*i], flag+"="); ok {
			return v, true
		}

		if args[*i] == flag && *i+1 < len(args) {
			*i++

			return args[*i], true
		}

		return "", false
	}

	for i := range len(args) {
		switch {
		case strings.HasPrefix(args[i], "-f"):
			v, ok := value(&i, "-f")
			if !ok {
				return opt, fmt.Errorf("FROM DOCKERFILE at %s: -f needs a path", where)
			}

			opt.path, opt.pathGiven = v, true

		case strings.HasPrefix(args[i], "--target"):
			v, ok := value(&i, "--target")
			if !ok {
				return opt, fmt.Errorf("FROM DOCKERFILE at %s: --target needs a stage name", where)
			}

			opt.target = v

		case strings.HasPrefix(args[i], "--build-arg"):
			v, ok := value(&i, "--build-arg")
			if !ok {
				return opt, fmt.Errorf("FROM DOCKERFILE at %s: --build-arg needs NAME=VALUE", where)
			}

			name, val, joined := strings.Cut(v, "=")
			if !joined || name == "" {
				return opt, fmt.Errorf(
					"FROM DOCKERFILE --build-arg %q (%s): expected NAME=VALUE", v, where)
			}

			opt.args[name] = val

		case args[i] == allowPrivilegedFlag:
			// Permits the referenced target to use `RUN --privileged`, which
			// this engine refuses wherever it appears - so the permission has
			// nothing to act on. Accepted rather than refused because the only
			// way it can be wrong is by refusing a build the shipping engine
			// would run, and refusing the flag did exactly that over a
			// permission nobody could have used.

		case strings.HasPrefix(args[i], "-"):
			return opt, unsupported("FROM DOCKERFILE "+args[i], where, "")

		default:
			opt.context = args[i]
		}
	}

	context := opt.context

	if context == "" {
		return opt, fmt.Errorf(
			"FROM DOCKERFILE at %s needs a build context"+
				"\n  write the directory the Dockerfile's COPY reads from, as in `FROM DOCKERFILE .`",
			where)
	}

	return opt, nil
}

// dockerfileStages parses a Dockerfile into its stages.
func dockerfileStages(
	src []byte, where string,
) ([]instructions.Stage, []instructions.ArgCommand, error) {
	ast, err := parser.Parse(strings.NewReader(string(src)))
	if err != nil {
		return nil, nil, fmt.Errorf("FROM DOCKERFILE at %s: %w", where, err)
	}

	// The second return is the *meta*-arguments: the ARGs before the first
	// stage, which Docker makes available to FROM lines. Discarded here until a
	// Dockerfile pinning its base image that way was sent to a registry with
	// `${XX_VERSION}` still in the reference (E64).
	stages, meta, err := instructions.Parse(ast.AST)
	if err != nil {
		return nil, nil, fmt.Errorf("FROM DOCKERFILE at %s: %w", where, err)
	}

	if len(stages) == 0 {
		return nil, nil, fmt.Errorf("FROM DOCKERFILE at %s: the Dockerfile has no FROM", where)
	}

	return stages, meta, nil
}

// globalArgs are the values a Dockerfile's own meta-arguments carry into its
// FROM lines: each one's default, and whatever `--build-arg` supplied instead.
//
// Earlier declarations are visible to later ones - `ARG A=1` then `ARG B=$A-x`
// is ordinary - so they are resolved in order rather than in one pass.
func globalArgs(meta []instructions.ArgCommand, supplied map[string]string) map[string]string {
	out := map[string]string{}

	for _, arg := range meta {
		for _, a := range arg.Args {
			if v, given := supplied[a.Key]; given {
				out[a.Key] = v

				continue
			}

			if a.Value == nil {
				out[a.Key] = ""

				continue
			}

			out[a.Key] = expandWith(*a.Value, out)
		}
	}

	return out
}

// expandWith substitutes `$name` and `${name}` from a map, leaving the rest.
//
// It is `expandWord`, which already scans left to right and reads the whole
// name at each `$`. The first version of this walked the map instead, replacing
// one name at a time, and had both of the defects that arrangement always has:
// `$DIR` substituted before `$DIRECTORY` leaves `shortECTORY`, and *which* goes
// first is Go's map order - so the same Earthfile planned two ways and
// `TestPlanningIsDeterministic` caught it about half the time (E66).
//
// A second expander was never needed. This one is a name for the right one.
func expandWith(in string, vals map[string]string) string {
	return scope(vals).expandWord(in)
}

// selectStage picks the stage to build, naming what exists when the one asked
// for does not.
func selectStage(stages []instructions.Stage, target, where string) (instructions.Stage, error) {
	if target == "" {
		return stages[len(stages)-1], nil
	}

	names := make([]string, 0, len(stages))

	for _, st := range stages {
		if strings.EqualFold(st.Name, target) {
			return st, nil
		}

		if st.Name != "" {
			names = append(names, st.Name)
		}
	}

	have := "it names no stages"
	if len(names) > 0 {
		have = "it defines: " + strings.Join(names, ", ")
	}

	return instructions.Stage{}, fmt.Errorf(
		"FROM DOCKERFILE --target %s at %s: no such stage\n  %s", target, where, have)
}

// translate turns one Dockerfile instruction into one command.
//
// Anything not here is refused by name rather than skipped: an instruction
// silently dropped produces an image that is not what the Dockerfile describes,
// and nothing downstream can tell.
func translate(instr instructions.Command, where string) (earthfile.Command, error) {
	loc := c(where)

	switch v := instr.(type) {
	case *instructions.RunCommand:
		// **Its mounts are part of the instruction.** Keeping the command and
		// dropping them is the failure `translate`'s own note describes, one
		// level down: the step runs, without the source bound at `.`, without
		// the cache, without the file another stage wrote - and fails somewhere
		// inside itself with an error about none of that.
		//
		// buildkit's own Dockerfile is the case that found this. Its buildkitd
		// stage binds the context at `.`, mounts two caches, and mounts
		// `/tmp/.ldflags` from an earlier stage; run without them it reports
		// `cat: can't open '/tmp/.ldflags'` and `go: go.mod file not found`,
		// which names neither the mounts nor the engine.
		//
		// The same rule as the Earthfile side, where a flag that changes what a
		// step can *do* is refused rather than stripped (runflags.go): a step
		// that quietly loses one does not fail, it produces the wrong thing.
		if len(instructions.GetMounts(v)) > 0 {
			return earthfile.Command{}, unsupported("RUN --mount", where, "")
		}

		return earthfile.Command{
			Name: earthfile.CmdRun, Args: v.CmdLine,
			ExecMode: !v.PrependShell, SourceLocation: loc,
		}, nil

	case *instructions.CopyCommand:
		// --from is handled before this, because it names a node rather than a
		// path and no Earthfile command can say that.
		args := append(append([]string{}, v.SourcePaths...), v.DestPath)

		return earthfile.Command{Name: earthfile.CmdCopy, Args: args, SourceLocation: loc}, nil

	case *instructions.AddCommand:
		return addCommand(v, loc, where)

	case *instructions.EnvCommand:
		if len(v.Env) != 1 {
			// One per command keeps the translation honest: several would have
			// to be several commands, and the interpreter's ENV takes one.
			return multiEnv(v, loc)
		}

		return earthfile.Command{
			Name: earthfile.CmdEnv, Args: []string{v.Env[0].Key, v.Env[0].Value},
			SourceLocation: loc,
		}, nil

	case *instructions.WorkdirCommand:
		return earthfile.Command{Name: earthfile.CmdWorkdir, Args: []string{v.Path}, SourceLocation: loc}, nil

	case *instructions.UserCommand:
		return earthfile.Command{Name: earthfile.CmdUser, Args: []string{v.User}, SourceLocation: loc}, nil

	case *instructions.ArgCommand:
		if len(v.Args) != 1 {
			return earthfile.Command{}, unsupported("ARG with several names in a Dockerfile", where, "")
		}

		args := []string{v.Args[0].Key}
		if v.Args[0].Value != nil {
			args = append(args, "=", *v.Args[0].Value)
		}

		return earthfile.Command{Name: earthfile.CmdArg, Args: args, SourceLocation: loc}, nil

	case *instructions.CmdCommand:
		return earthfile.Command{Name: earthfile.CmdCmd, Args: v.CmdLine, SourceLocation: loc}, nil

	case *instructions.EntrypointCommand:
		return earthfile.Command{Name: earthfile.CmdEntrypoint, Args: v.CmdLine, SourceLocation: loc}, nil

	// The three below configure the image and produce no step, and the
	// Earthfile spellings of them are already implemented here. Refusing them
	// turned an ordinary Dockerfile into `VOLUME is not supported by the native
	// engine` - a construct this engine supports, named as one it does not.
	case *instructions.VolumeCommand:
		return earthfile.Command{Name: earthfile.CmdVolume, Args: v.Volumes, SourceLocation: loc}, nil

	case *instructions.ExposeCommand:
		return earthfile.Command{Name: earthfile.CmdExpose, Args: v.Ports, SourceLocation: loc}, nil

	case *instructions.LabelCommand:
		return labelCommand(v, loc)

	default:
		return earthfile.Command{}, unsupported(instructionName(instr), where, "")
	}
}

// addCommand translates a Dockerfile ADD, when it is a COPY and only then.
//
// ADD does two things COPY does not: it extracts a local tar archive into the
// destination, and it fetches a URL. Translating it to COPY regardless would
// not fail - it would succeed, with the archive where its contents were meant
// to be, which is the shape of wrong this engine is arranged against.
//
// Decided on how the source *looks*, where docker decides by reading it. That
// is deliberately the conservative direction: a file named `.tar.gz` that is
// not one gets refused where it would have worked, and the alternative is a
// file that is one being copied whole where it should have been unpacked.
func addCommand(v *instructions.AddCommand, loc *earthfile.SourceLocation, where string) (earthfile.Command, error) {
	for _, src := range v.SourcePaths {
		if strings.Contains(src, "://") {
			return earthfile.Command{}, fmt.Errorf(
				"ADD %s at %s would fetch it, which this engine does not do"+
					"\n  fetch it in a RUN, or vendor the file and COPY it",
				src, where)
		}

		if archiveLike(src) {
			return earthfile.Command{}, fmt.Errorf(
				"ADD %s at %s would extract it, and this engine would copy it whole"+
					"\n  COPY it and unpack it in a RUN, which says what happens",
				src, where)
		}
	}

	args := append(append([]string{}, v.SourcePaths...), v.DestPath)

	return earthfile.Command{Name: earthfile.CmdCopy, Args: args, SourceLocation: loc}, nil
}

// archiveLike reports whether a name is one docker would unpack.
//
// The list docker itself unpacks: tar, and tar compressed the four ways it
// recognises. A bare `.gz` is not on it - docker only extracts *archives* - so
// `ADD thing.gz` is an ordinary copy.
func archiveLike(name string) bool {
	lower := strings.ToLower(name)

	for _, ext := range []string{".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".tar.zst"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}

	return false
}

// labelCommand translates a Dockerfile LABEL.
//
// One at a time, like ENV and for the same reason: the Earthfile spelling takes
// a single `key=value`, and a Dockerfile may set several in one instruction.
// Refused rather than silently taking the first, because a label quietly
// dropped is an image that does not say what it was built from.
func labelCommand(v *instructions.LabelCommand, loc *earthfile.SourceLocation) (earthfile.Command, error) {
	if len(v.Labels) != 1 {
		names := make([]string, 0, len(v.Labels))
		for _, kv := range v.Labels {
			names = append(names, kv.Key)
		}

		return earthfile.Command{}, fmt.Errorf(
			"LABEL at %s sets %s in one instruction, which this engine reads one at a time"+
				"\n  write them as separate LABEL lines",
			loc.File, strings.Join(names, ", "))
	}

	return earthfile.Command{
		Name:           earthfile.CmdLabel,
		Args:           []string{v.Labels[0].Key + "=" + v.Labels[0].Value},
		SourceLocation: loc,
	}, nil
}

// multiEnv refuses an ENV setting several names at once, naming them.
func multiEnv(v *instructions.EnvCommand, loc *earthfile.SourceLocation) (earthfile.Command, error) {
	names := make([]string, 0, len(v.Env))
	for _, kv := range v.Env {
		names = append(names, kv.Key)
	}

	return earthfile.Command{}, fmt.Errorf(
		"ENV at %s sets %s in one instruction, which this engine reads one at a time"+
			"\n  write them as separate ENV lines",
		loc.File, strings.Join(names, ", "))
}

// instructionName is what to call an instruction in a refusal.
func instructionName(instr instructions.Command) string {
	if named, ok := instr.(interface{ Name() string }); ok {
		return strings.ToUpper(named.Name())
	}

	return fmt.Sprintf("%T", instr)
}

// c makes a source location out of the FROM DOCKERFILE line, because every step
// a Dockerfile contributes belongs to that line as far as the Earthfile's
// reader is concerned.
func c(where string) *earthfile.SourceLocation {
	return &earthfile.SourceLocation{File: where}
}

// flatten turns a map into the alternating name/value form withEnv takes,
// ordered so a build reading it twice reads the same thing.
func flatten(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}

	sort.Strings(names)

	out := make([]string, 0, 2*len(names))
	for _, k := range names {
		out = append(out, k, m[k])
	}

	return out
}

// dockerfileFromTarget names the target a Dockerfile would have to come out of,
// empty when it is a file on this machine.
//
// Two shapes: `-f` naming an artifact, and a context that is a target with no
// `-f` at all - because the reference looks for the Dockerfile *in the context*,
// and a target's context is its output.
func dockerfileFromTarget(opt dockerfileOptions, fromTarget *ir.Node) string {
	if strings.Contains(opt.path, "+") {
		return opt.path
	}

	if fromTarget != nil && !opt.pathGiven {
		return opt.context
	}

	return ""
}
