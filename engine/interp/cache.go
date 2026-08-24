package interp

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/util/flagutil"
)

// cacheMount reads `CACHE [--id=name] [--sharing=mode] <path>`.
//
// The mount applies to the steps *after* the line, which is how the command
// reads: a cache declared halfway down a recipe is not something the commands
// above it were built with.
//
// `--persist` puts the cache's contents into the image, which is a different
// operation with a different result: an ordinary cache is bound over the step's
// filesystem and so is excluded from the layer by construction, and this asks
// for the opposite. It is carried on the mount and honoured by the guest, which
// copies rather than binds.
// workdir is the directory in force at the CACHE line, which is what a relative
// path is relative to.
func cacheMount(c earthfile.Command, workdir string) (ir.Mount, error) {
	var opts cmdopts.Cache

	rest, err := flagutil.ParseArgsCleaned("CACHE", &opts, c.Args)
	if err != nil {
		return ir.Mount{}, flagFault("CACHE", loc(c.SourceLocation), err)
	}

	// The three modes, each with a mechanism behind it.
	//
	// They were all refused but `locked`, on the honest grounds that accepting a
	// mode while providing a different one answers a question about concurrency
	// with a guess. Both missing mechanisms now exist - a lock per cache id
	// (E427) and an ephemeral mount (E398) - so the guess is not required:
	//
	//   locked   the shared directory, one step in it at a time (the default)
	//   shared   the shared directory, several steps at once
	//   private  a directory of its own, thrown away with the step
	//
	// A word nobody has heard of is still refused, for the reason all three
	// were: it would be a guess (E432).
	exclusive, private, known := sharingMode(opts.Sharing, "locked")
	if !known {
		return ir.Mount{}, unsupported("CACHE --sharing="+opts.Sharing, loc(c.SourceLocation), "")
	}

	if len(rest) == 0 {
		return ir.Mount{}, fmt.Errorf("CACHE needs a path (%s)", loc(c.SourceLocation))
	}

	// **One path, and one only.** `CACHE /one /two` was accepted and the second
	// dropped, so a step that asked for two caches got one and cached writes to
	// the other into its own layer (E359).
	if len(rest) > 1 {
		return ir.Mount{}, fmt.Errorf("CACHE (%s): %q is a second path and"+
			" CACHE takes one - write a CACHE line for each",
			loc(c.SourceLocation), rest[1])
	}

	// Relative to the *working directory*, which is what the reference does and
	// what an author writing `CACHE ./node_modules` under `WORKDIR /app` means.
	//
	// Resolved against `/` it mounted `/node_modules`: a directory nothing
	// touches, so the cache cached nothing and everything it was meant to hold
	// went into the step's own layer. Nothing failed - a cache that misses is a
	// slower build - and the tell was in a profile, where 2382 reads under
	// `/app/node_modules` proved the path had not been mounted, because a path
	// inside a mount is filtered out of an observation before it is recorded
	// (E222, E498).
	target := rest[0]
	if !strings.HasPrefix(target, "/") {
		target = filepath.Join("/", workdir, target)
	}

	target = filepath.Clean("/" + strings.TrimPrefix(target, "./"))

	mode, err := mountMode(map[string]string{mountFieldChmod: opts.Mode}, loc(c.SourceLocation))
	if err != nil {
		return ir.Mount{}, err
	}

	if mode == cacheChmodDefault {
		mode = 0
	}

	// The id defaults to the path, so two targets naming one cache get one
	// directory - a cache that is private per step never warms, which is the
	// opposite of what the line asks for.
	id := opts.ID
	if id == "" {
		id = cacheID(target)
	}

	// A private cache names no shared directory: there is nothing for an id to
	// point at, and leaving one would let a later `--sharing=locked` line with
	// the same id believe it shares with a step that shared with nobody.
	if private {
		return ir.Mount{Target: target, Ephemeral: true, Persist: opts.Persist}, nil
	}

	return ir.Mount{
		Target: target, ID: id, Exclusive: exclusive, Persist: opts.Persist, Mode: mode,
	}, nil
}

// cacheChmodDefault is what the parser fills in when `--chmod` is not written.
//
// Indistinguishable from an author writing it, which would matter if it were a
// usable mode. It is not: 0644 on a *directory* has no execute bit, so nothing
// can enter it - so the default is treated as unwritten, and so is the same
// value written by hand. The kind answer of the two: the alternative is a cache
// nobody can cd into, produced by a flag they did not know they had (E436).
const cacheChmodDefault = 0o644

// sharingMode reads one of the three modes, or reports that it is not one.
//
// One function because `CACHE --sharing` and `RUN --mount=...,sharing=` are the
// same three words with the same three meanings, and were two switches until one
// of them turned out to be no switch at all (E435).
//
// The default differs and is the caller's: `CACHE` locks and `RUN --mount`
// shares, which is what each does in the engine this one has to agree with. Not
// an inconsistency to tidy - changing either would make an Earthfile mean
// something here that it does not mean anywhere else.
func sharingMode(spec, whenEmpty string) (exclusive, private, known bool) {
	mode := strings.ToLower(spec)
	if mode == "" {
		mode = whenEmpty
	}

	switch mode {
	case "locked":
		return true, false, true

	case "shared":
		return false, false, true

	case "private":
		return false, true, true

	default:
		return false, false, false
	}
}

// cacheID turns a path into a directory name.
func cacheID(target string) string {
	out := []rune(strings.TrimPrefix(target, "/"))
	for i, r := range out {
		if r == '/' || r == filepath.Separator {
			out[i] = '_'
		}
	}

	if len(out) == 0 {
		return "root"
	}

	return string(out)
}

// parseMount reads one `--mount=type=cache,target=/x,id=name` specification.
//
// Comma-separated key=value, which is the form the shipping engine and
// Dockerfiles both use, so an Earthfile written for either reads the same here.
//
// Only `type=cache` is provided. A `secret` hands a credential to a step and a
// `tmpfs` gives it memory that disappears; neither is a cache, and providing a
// cache instead would run the step with something other than what it asked for.
// A silently absent secret is the worst of them, because the command that needed
// it fails somewhere else entirely.
// mountFieldTarget is where a mount appears, named because two files spell it:
// this one reads it and dockerfile.go writes it.
const mountFieldTarget = "target"

// mountKindBind is a bound view's spelling. Named because four places test for
// it and because the *other* bind - `bind-experimental`, an Earthfile's window
// onto the host - differs from it by a suffix, which is the kind of difference
// a reader skims past.
const mountKindBind = "bind"

// mountFieldDst is `target` under its other name, which both languages accept.
const mountFieldDst = "dst"

// The remaining field and kind names this file both reads and lists.
const (
	mountFieldMode  = "mode"
	mountFieldChmod = "chmod"
	mountKindSecret = "secret"
	mountFieldRO    = "readonly"
	mountFieldType  = "type"
)

// parseMount also reports a bound view's `from`, which it cannot resolve.
//
// ν is a node, and this function has no graph: the caller has the plan, the
// context root and - inside a FROM DOCKERFILE - the stages. Returning the raw
// reference keeps the parsing here and the resolution where the answers are.
func parseMount(spec, where string) (ir.Mount, string, error) {
	fields := map[string]string{}

	for part := range strings.SplitSeq(spec, ",") {
		if part == "" {
			continue
		}

		k, v, _ := strings.Cut(part, "=")
		fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	kind := fields[mountFieldType]
	if kind == "" {
		kind = "(none)"
	}

	// A bind from the host is a decision, not a gap.
	//
	// It gives a step a **writable window onto the machine running the build** -
	// `tests/host-bind.earth` writes through one - and that is the thing this
	// engine has already decided about twice in other words: a step's writes are
	// held to its own layer (A3), and `SAVE ARTIFACT --force` is refused because
	// this engine never writes outside the project. The same hazard by a
	// different door (E485).
	//
	// Marked as such rather than as unimplemented, because the sentinel is what
	// both sweeps read: filed as a gap it was work somebody should do, and the
	// work would be reversing a position.
	if kind == "bind-experimental" {
		return ir.Mount{}, "", refusedOnPurpose("RUN --mount type="+kind, where,
			"a step's writes are held to its own layer, and a bind is a window"+
				" out of it\n  COPY what the step needs in, and SAVE ARTIFACT"+
				" what it produces out")
	}

	// **A plain `bind` is a different thing wearing the same word**, and gets a
	// different answer. It can only have come from a Dockerfile - the shipping
	// engine accepts `bind-experimental` and nothing else in an Earthfile
	// (earthfile2llb/runmount.go) - where it means a read-only view of the build
	// context, or of an earlier stage. Content this build already has and
	// already digests. Nothing about it is a window onto the machine, so the
	// decision above does not reach it.
	//
	// So: unbuilt, not declined. The distinction is the sentinel both corpus
	// sweeps count, and it decides whether 371 targets read as a settled
	// question or as the largest piece of work left. They are the latter.
	if kind != "cache" && kind != mountKindSecret && kind != mountKindBind {
		return ir.Mount{}, "", unsupported("RUN --mount type="+kind, where, "")
	}

	err := onlyKnownFields(fields, kind, where)
	if err != nil {
		return ir.Mount{}, "", err
	}

	target := fields[mountFieldTarget]
	if target == "" {
		target = fields[mountFieldDst]
	}

	if target == "" {
		return ir.Mount{}, "", fmt.Errorf(
			"RUN --mount at %s has no target"+
				"\n  a mount needs somewhere to appear: --mount=type=cache,target=/path",
			where)
	}

	target = filepath.Clean("/" + strings.TrimPrefix(target, "./"))

	id := fields["id"]
	if id == "" {
		id = cacheID(target)
	}

	mode, err := mountMode(fields, where)
	if err != nil {
		return ir.Mount{}, "", err
	}

	// A secret is read, never written: a step that could write through the
	// mount would be writing into wherever the invocation keeps its
	// credentials.
	if kind == mountKindSecret {
		return ir.Mount{Target: target, ID: id, Secret: true, ReadOnly: true, Mode: mode}, "", nil
	}

	// **A bound view is read-only whatever was written.** `readonly=false` on
	// one would be a step editing another step's input, which §3.3b forbids
	// outright - so the field is read and the answer does not depend on it.
	// From is filled in by the caller, which is what can resolve ν.
	if kind == mountKindBind {
		sub := fields["source"]
		if sub == "" {
			sub = fields["src"]
		}

		return ir.Mount{Target: target, Sub: sub, ReadOnly: true, View: true}, fields["from"], nil
	}

	// `shared` by default here and `locked` for CACHE - see sharingMode.
	exclusive, private, known := sharingMode(fields["sharing"], "shared")
	if !known {
		return ir.Mount{}, "", unsupported(
			"RUN --mount sharing="+fields["sharing"], where, "")
	}

	if private {
		return ir.Mount{
			Target: target, Ephemeral: true, ReadOnly: readOnly(fields), Mode: mode,
		}, "", nil
	}

	return ir.Mount{
		Target: target, ID: id, Exclusive: exclusive,
		ReadOnly: readOnly(fields), Mode: mode,
	}, "", nil
}

// mountMode reads `mode=` or `chmod=`, which are one field with two spellings.
//
// Base 8 explicitly, so `0644` and `644` mean the same thing. Left to Go to
// infer, `644` would be read as decimal and mounted as 0o1204 - a permission
// nobody asked for and nobody would look for, which is the silent-wrong failure
// this engine is arranged against (E435).
func mountMode(fields map[string]string, where string) (uint32, error) {
	for _, k := range []string{mountFieldMode, mountFieldChmod} {
		raw, set := fields[k]

		// An empty value is not a bad mode. The spec is expanded before it is
		// parsed, so `mode=$mode` with the argument unsupplied arrives here as
		// `mode=` - and refusing that would refuse the Earthfile for something
		// the expansion did rather than something its author wrote. This
		// repository's own `tests/cache-mount-mode.earth` is exactly that file.
		if !set || raw == "" {
			continue
		}

		mode, err := strconv.ParseUint(raw, 8, 32)
		if err != nil || mode > 0o7777 {
			return 0, fmt.Errorf(
				"RUN --mount at %s has %s=%q, which is not a permission"+
					"\n  write it in octal, as `mode=0400` or `mode=400`",
				where, k, raw)
		}

		return uint32(mode), nil
	}

	return 0, nil
}

// mountFields are the keys this engine reads, per mount type.
//
// A list rather than a set of `if`s, because the failure was structural: fields
// went into a map, five were consulted and the rest were neither used nor
// refused. Parsing a field is not providing it, and nothing in a map can tell
// the two apart (E435).
var mountFields = map[string][]string{
	"cache": {
		mountFieldType, mountFieldTarget, mountFieldDst, "id",
		mountFieldRO, "ro", "sharing", mountFieldMode, mountFieldChmod,
	},
	mountKindSecret: {
		mountFieldType, mountFieldTarget, mountFieldDst, "id",
		mountFieldRO, "ro", mountFieldMode, mountFieldChmod,
	},
	// A bound view (§3.3d). `from` names an earlier stage and is read here so
	// that it can be refused by name at the point of use rather than dropped:
	// a view of a stage needs that stage's assembled stack, which a view of the
	// context does not, so one of the two is built and the other is not.
	mountKindBind: {
		mountFieldType, mountFieldTarget, mountFieldDst,
		"source", "src", "from", mountFieldRO, "ro",
	},
}

// onlyKnownFields refuses a field this engine would have dropped.
//
// The safe direction of E34's asymmetry: refusing a field we could have honoured
// costs a build that says exactly what is missing, and honouring the mount
// without it costs a step that ran with something other than what it asked for
// and reported success.
func onlyKnownFields(fields map[string]string, kind, where string) error {
	// Sorted, so a mount with two unknown fields refuses the same one every
	// time: map order is random, and a diagnostic that varies between runs of
	// the same build is one nobody can act on (I12).
	unknown := make([]string, 0, len(fields))

	for k := range fields {
		if !slices.Contains(mountFields[kind], k) {
			unknown = append(unknown, k)
		}
	}

	if len(unknown) == 0 {
		return nil
	}

	slices.Sort(unknown)

	return unsupported("RUN --mount "+unknown[0], where,
		"")
}

// readOnly reads the two spellings and the bare form.
//
// `ro` and `readonly` are both Docker's, and `readonly` on its own - no value -
// is how it is usually written. Compared against "true", the bare form was
// false, so a mount the author asked to be read-only was writable.
func readOnly(fields map[string]string) bool {
	for _, k := range []string{mountFieldRO, "ro"} {
		v, set := fields[k]
		if set && (v == "" || v == trueWord) {
			return true
		}
	}

	return false
}

// gitClone plans `GIT CLONE [--branch ref] <url> <dest>`.
//
// The checkout becomes an ordinary copy source, so it is content-addressed like
// every other: digested at graph construction, so a build whose dependency moved
// gets a different key. Keyed on the URL instead would leave the graph unchanged
// when the branch advanced, and the build would hit the cache and reproduce the
// previous checkout - the most damaging false hit available, because it looks
// like a fast build.
func (p *Plan) gitCloneNode(c earthfile.Command, prev *ir.Node, rs *state) (*ir.Node, error) {
	where := loc(c.SourceLocation)

	var opts cmdopts.GitClone

	rest, err := flagutil.ParseArgsCleaned("GIT CLONE", &opts, c.Args)
	if err != nil {
		return nil, flagFault("GIT CLONE", where, err)
	}

	// --keep-ts is absent on purpose: it asks for what this engine already
	// does. A capture records timestamps to the nanosecond (I8), so a checkout
	// with the flag and one without produce the same tree - which is the same
	// reasoning that already accepts `COPY --keep-ts` and
	// `SAVE ARTIFACT --keep-ts`, and which this one was left out of.
	//
	// Refusing a flag while doing what it asks is the expensive direction: it
	// turns away a working Earthfile and tells its author the opposite of the
	// truth. See flagMeanings, which records that this exact mistake has been
	// made here once before.

	if len(rest) != 2 {
		return nil, fmt.Errorf(
			"GIT CLONE at %s: expected `GIT CLONE <url> <destination>`, found %q",
			where, strings.Join(rest, " "))
	}

	url, dest := rest[0], rest[1]

	// Implemented, and wired by the CLI; a plan-only caller simply has nowhere
	// to put a checkout. That is a withheld capability rather than a missing
	// feature, and the difference is what keeps the work list honest.
	if p.opt.gitClone == nil {
		return nil, fmt.Errorf(
			"GIT CLONE %s (%s) needs somewhere to check the repository out: %w"+
				"\n  this caller resolves plans without fetching anything",
			url, where, ErrNoRunner)
	}

	dir, err := p.opt.gitClone(url, opts.Branch)
	if err != nil {
		return nil, fmt.Errorf("GIT CLONE %s (%s): %w", url, where, err)
	}

	src, err := resolveContext("COPY", dir, ".", where)
	if err != nil {
		return nil, err
	}

	src.Meta.Description = "GIT CLONE " + url

	return &ir.Node{
		Platform: platformOf(rs.platform),
		Op:       ir.Op{Kind: ir.OpFile, Args: []string{".", dest}, Dir: rs.dir, User: rs.user},
		Inputs:   []*ir.Node{prev},
		Sources:  []*ir.Node{src},
		Meta:     ir.Meta{Source: where, Description: "GIT CLONE " + url + " " + dest},
	}, nil
}

// localCopy plans a COPY inside a LOCALLY target.
//
// Recorded as an artifact export rather than a step, because that is what it is:
// the file is produced by another target and lands on this machine, which is
// exactly what `SAVE ARTIFACT ... AS LOCAL` already does. Reusing that path
// means one implementation of "put this where the user asked", and one place
// for it to be wrong.
func (p *Plan) localCopy(c earthfile.Command, prev *ir.Node, _ *state) (*ir.Node, error) {
	where := loc(c.SourceLocation)

	spec, err := copyArgs(c)
	if err != nil {
		return nil, err
	}

	// Only the sources, the destination and the source target's arguments mean
	// anything here: a LOCALLY copy writes onto the machine running the build,
	// where there is no image for --dir or --symlink-no-follow to shape. The
	// struct makes that visible - the fields simply are not read - where six
	// discarded return values needed a line saying so.
	sources, buildArgs := spec.Args, spec.BuildArgs

	if len(sources) < 2 {
		return nil, fmt.Errorf("COPY needs a source and a destination (%s)", where)
	}

	dest := sources[len(sources)-1]

	for _, src := range sources[:len(sources)-1] {
		if !strings.Contains(src, "+") {
			return nil, fmt.Errorf(
				"COPY at %s is inside a LOCALLY target"+
					"\n  %q is already on this machine, so there is nothing to copy it into"+
					"\n  a COPY here takes an artifact from another target, as in `COPY +target/file .`",
				where, src)
		}

		if len(buildArgs) > 0 {
			p.passTo = buildArgs
		}

		from, path, err := p.copySource(src, where)
		if err != nil {
			return nil, err
		}

		// A destination outside the project is allowed, unlike `SAVE ARTIFACT AS
		// LOCAL`. That rule exists because an Earthfile - possibly fetched from
		// elsewhere - must not choose where to write on someone's machine; a
		// LOCALLY target is already running arbitrary commands there, so
		// refusing the copy while allowing `RUN cp` would be theatre.
		p.Artifacts = append(p.Artifacts, Artifact{
			Path: path, From: from, LocalDest: dest, Source: where,
		})

		// The producing step has to be *in* the graph, or it is never scheduled
		// and the export names a node nobody built. A dependency rather than a
		// base: this target does not stand on the artifact's filesystem, it
		// takes one file out of it.
		//
		// Caught by the corpus invariant that every artifact is produced by the
		// graph, on a target exporting several artifacts from different
		// producers - where only the first was reachable.
		p.also = appendOnce(p.also, from)
	}

	return prev, nil
}

// appendOnce adds a node the build must run, unless it is already there.
func appendOnce(nodes []*ir.Node, n *ir.Node) []*ir.Node {
	if n == nil {
		return nodes
	}

	for _, have := range nodes {
		// A nil already in the list is not this call's problem to report, and
		// dereferencing it is how the one that got in was found.
		if have == nil {
			continue
		}

		if have.ID() == n.ID() {
			return nodes
		}
	}

	return append(nodes, n)
}

// resolveViews fills in each bound view's ν and returns the objects it shows.
//
// Called where the context root and the graph are, which parseMount is not:
// ν is a node, and a node is not something a string parser can produce.
//
// A view of the local context is one layer - the context node this engine
// already builds for COPY - so it drops straight in. A view of an earlier stage
// is not: a stage's filesystem is an assembled stack, and showing one needs
// machinery that does not exist yet (§3.3d, ν ∈ 𝕂).
func (p *Plan) resolveViews(mounts []ir.Mount, views []view, rs *state, where string) ([]*ir.Node, error) {
	if len(views) == 0 {
		return nil, nil
	}

	// **Every refusal first, then the expensive part.** Resolving a view of the
	// context digests it, and a step binding both the context and a stage was
	// digesting a whole tree before refusing the stage - work thrown away, and
	// on this repository's own corpus it was two minutes of it per sweep.
	//
	// The ordering is also the better behaviour: a build told it cannot do
	// something should be told before it waits.
	for _, v := range views {
		if v.from != "" && rs.stage == nil {
			return nil, notInLanguage("RUN --mount type=bind,from="+v.from, where,
				"`from` names a Dockerfile stage and an Earthfile has none"+
					"\n  COPY from the other target instead")
		}
	}

	out := make([]*ir.Node, 0, len(views))

	for _, v := range views {
		// A stage is built on demand, so binding one may be the only reason it
		// is built at all - and it has to be, because a view that named an
		// unbuilt object would key against something nothing produces.
		if v.from != "" {
			n, err := rs.stage(v.from)
			if err != nil {
				return nil, err
			}

			mounts[v.at].From = n.ID()
			out = append(out, n)

			continue
		}

		// The subtree names a path in the context, and the context node this
		// engine builds for COPY holds exactly that path - digested, so what it
		// shows reaches the key (I20). Empty means the whole of it.
		at := mounts[v.at].Sub
		if at == "" {
			at = "."
		}

		key := p.here.dir + "\x00" + at

		n, seen := p.viewed[key]
		if !seen {
			var err error

			n, err = resolveContext("RUN --mount", p.here.dir, at, where)
			if err != nil {
				return nil, err
			}

			if p.viewed == nil {
				p.viewed = map[string]*ir.Node{}
			}

			p.viewed[key] = n
		}

		mounts[v.at].From = n.ID()
		out = append(out, n)
	}

	return out, nil
}
