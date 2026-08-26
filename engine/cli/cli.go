// Package cli is the front end: a directory and a target name in, a built
// artifact and a readable account of what happened out.
//
// It is a library rather than a main package so that the whole path - parse,
// plan, schedule, export, report - is testable without a process boundary. What
// remains in main is argument parsing and an exit code.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/pin"
	"github.com/EarthBuild/earthbuild/engine/store"
	"github.com/EarthBuild/earthbuild/engine/timing"
)

// Options configure a build.
// writerName is what this engine calls itself in a build record.
//
// One place, because a record's writer is how a later reader knows which engine
// produced it - a build recorded under two spellings is two engines as far as
// anything reading the record is concerned.
const writerName = "earthbuild"

// Options is a build, as a caller describes it.
//
// Everything the engine needs to run one and nothing about how: the fields say
// what to build and where to report, and the choices - which engine, how many
// workers, what to trace - are read from the environment or decided by the
// engine itself. A caller that had to answer those would be a caller that has
// to be updated whenever the engine learns a new one.
type Options struct {
	// NoOutput leaves `SAVE ARTIFACT ... AS LOCAL` unwritten.
	//
	// What `--ci` is for: a build machine wants the steps run and the cache
	// filled, not the working tree changed. `--ci` means `--no-output --strict`,
	// and strict is what this engine already is - it refuses what it cannot
	// reproduce (I10) - so this is the half that needed building.
	NoOutput bool
	// Dir holds the Earthfile and is the build context.
	Dir string
	// Target to build.
	Target string
	// Platform is "os/arch". The guest's own when empty.
	Platform string
	// Out receives progress. Diagnostics go to the returned error, not here.
	Out io.Writer
	// Args are build argument values, overriding the Earthfile's defaults.
	Args map[string]string
	// Secrets are credentials a step may mount, by name.
	//
	// Never written to the graph, the key, or a printed plan: the interpreter is
	// told only which names exist so it can refuse a step asking for one that
	// does not, and the executor is the single place a value is read.
	Secrets map[string]string
	// DryRun resolves the plan and prints it without running anything.
	//
	// Useful on a machine with no sandbox, and useful as a check: it does all
	// the work that can fail for reasons in the Earthfile - parsing, target
	// resolution, context digests, capability refusals - and none of the work
	// that can fail for reasons in the environment.
	DryRun bool
	// AllowPrivileged accepts `RUN --privileged` rather than refusing it. The
	// flag buys nothing here - a step already holds every capability inside its
	// namespace - and a caller who asks for it anyway is taken at their word
	// (interp.WithAllowPrivileged).
	AllowPrivileged bool
	// Push says this build is a push, so `RUN --push` steps run rather than
	// being planned away (interp.WithPush).
	Push bool
	// NoCache builds every step, reading no cache entry that is already there.
	//
	// Two of the corpus's own invocations pass `--no-cache` and the gate could
	// not, because the engine had no such option (E462).
	NoCache bool
	// Env is this invocation's own environment, consulted before the process's.
	//
	// A build reads a few variables - which file its build arguments live in,
	// for one - and a caller that drives several builds at once cannot say so
	// with `os.Setenv` without deciding it for all of them. Nil means the
	// process's environment alone, which is what a terminal gives (E475).
	Env map[string]string
	// Long asks the reading commands for everything they have rather than a
	// summary: `doc --long` adds what a target needs and what it produces.
	Long bool
	// VersionFlags are features turned on for every file in the build, whatever
	// its VERSION line says: `--version-flag-overrides`.
	//
	// Seven of the corpus's invocations pass it, and the gate could not attempt
	// any of them because the engine had nowhere to put the answer (E473).
	VersionFlags []string
	// ArgFile and SecretFile name the files a project keeps its build arguments
	// and secrets in, empty for the usual `.arg` and `.secret` beside the
	// Earthfile.
	//
	// Named explicitly, a missing file is an error: the author asked for that
	// path (E465).
	ArgFile    string
	SecretFile string
	// SecretFiles are `NAME=path` entries: one secret whose value is a file's
	// contents.
	//
	// Distinct from SecretFile, which is where the project keeps *many* - the
	// two were conflated once and the engine looked for a file called
	// `SECRET3=~/my-secret-file` (E469).
	SecretFiles []string
	// ExecStats asks the build to say what it spent: total CPU across its steps
	// and the largest peak any one of them reached (E467).
	ExecStats bool
}

// platformOrDefault is the platform the build runs on.
//
// The sandbox's own when the invocation named none, which is what `ARG
// NATIVEARCH` answers and what an unqualified target is built for.
func (o Options) platformOrDefault() string {
	if o.Platform != "" {
		return o.Platform
	}

	return exec.DefaultPlatform()
}

// Run builds a target.
// The result is named because a deferred check reads it: see the case note
// below, which belongs to a *failed* build and cannot know that from a local
// variable. `return build(...)` assigns the named result before defers run, so
// every exit is covered by the one check - which was worth confirming rather
// than assuming, and a mutant that survived is what asked the question (E491).
func Run(ctx context.Context, o Options) (err error) { //nolint:nonamedreturns // the deferred case note reads it
	if o.Out == nil {
		o.Out = io.Discard
	}

	// **A target may name the directory it lives in.** `./dir+target` is how the
	// language refers to a target elsewhere and the interpreter has always
	// resolved it; only the command line refused it, which put this
	// repository's own corpus out of reach of its own engine. See splitTargetRef.
	o.Dir, o.Target = splitTargetRef(o.Dir, o.Target)

	path := filepath.Join(o.Dir, "Earthfile")

	src, err := os.ReadFile(path) //nolint:gosec // the user named this directory
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no Earthfile in %s\n  looked for %s", o.Dir, path)
		}

		return fmt.Errorf("read %s: %w", path, err)
	}

	// The engine is created before the plan because making the plan may need
	// it: a condition the interpreter cannot decide is answered by running it,
	// which needs a sandbox. It builds one lazily, so a plan that decides all
	// its conditions - which is nearly all of them - still boots nothing.
	g := &engine{o: o, contexts: &interp.ContextCache{}}

	defer g.close()

	// What earlier builds observed about each condition. A hint, so a machine
	// with no history and a history that cannot be read are the same case:
	// build anyway.
	dir, err := storeDir()
	if err == nil {
		// Said once, at the start, because it explains failures that arrive much
		// later and look like something else entirely.
		// Both, because they are the same directory by default and
		// EARTH_IMAGE_CACHE_DIR separates them - and an image is unpacked into
		// whichever one it lands in.
		images, imageErr := imageCacheDir()
		if imageErr != nil {
			images = ""
		}

		// Kept, not printed. See explainCase: this note belongs to a failure,
		// and printing it before anything has happened is what made it read as
		// the diagnosis of whatever failed next (E491).
		g.caseNote = caseNoteFor(
			cacheDir{path: dir, env: envCacheDir},
			cacheDir{path: images, env: envImageCacheDir})

		learned, imageErr := loadPredictions(dir)
		if imageErr == nil {
			g.learned = learned

			// What confidently-predicted branches needed last time, fetched
			// beside the interpretation rather than in front of it - so the
			// pull overlaps the work that leads to the condition selecting the
			// branch that wants it. A hint throughout: it cannot fail the
			// build, and an image it did not fetch is pulled normally by
			// whatever needs it.
			platform := o.Platform
			if platform == "" {
				platform = exec.DefaultPlatform()
			}

			// Waited for on the way out, so no pull outlives the build that
			// speculated on it.
			defer prefetch(ctx, learned, intoImageCache(dir, platform))()

			// Started for every build, not only one whose history says a
			// condition will need it. A build that runs *any* step needs the
			// machine, which is nearly all of them, and the boot then overlaps
			// parsing, digesting the build context and resolving what `FROM`
			// means - about half a second of registry round trip that the
			// machine has no reason to wait behind.
			//
			// Nothing waits for it, so a build that turns out to need no machine
			// is not slowed: it finishes and exits while the boot is in flight,
			// and leaves behind the VM the next build would have had to boot
			// anyway (E537).
			g.warm(ctx)

			defer func() {
				recordNeeds(learned, g.decided, g.images)

				_ = savePredictions(dir, learned)
			}()
		}
	}

	// The terminal an interactive step would run on, if this invocation has one.
	//
	// Found before planning, because whether it exists decides whether
	// `RUN --interactive` is accepted at all - and refusing at plan time is the
	// difference between a build that says so and one that fails halfway
	// through with a prompt nobody can answer.
	tty := callersTerminal()
	if tty != nil {
		defer func() { _ = tty.Close() }()
	}

	// The project's own defaults, under whatever this invocation was given.
	//
	// A `.arg` beside the Earthfile is how a project keeps values out of its
	// source without typing them every time, and `.secret` the same for
	// credentials. Read before planning, because an argument decides what the
	// graph *is* (E465).
	args, secrets, err := o.withProjectFiles()
	if err != nil {
		return err
	}

	// From here on a failure is the caller's news, and the store's case
	// behaviour may be part of why (E491).
	defer func() {
		if err != nil && g.caseNote != "" && o.Out != nil {
			fmt.Fprint(o.Out, g.caseNote)
		}
	}()

	// **The three stages a build has, timed at the top.** Every phase inside
	// them was instrumented and their sum came to about half the wall clock of
	// a fully cached build - so the rest was being attributed to whichever
	// mechanism happened to be measured next to it, which is how a scan that
	// ran concurrently with the real work looked like the answer for a while
	// (E561, E565).
	endPlan := timing.Phase("plan", o.Target)

	// **Started before the walk, because the walk is what makes them serial.**
	// The interpreter resolves each `FROM` as it reaches it, so two distinct
	// images cost the sum of two round trips - 0.336s measured against 0.197s
	// for one, on a build whose every step was already cached. Nothing about
	// resolving one image depends on another.
	//
	// The scan is the same one `--pin` uses, so a reference it misses resolves
	// inline exactly as it did before; this changes when the lookups happen and
	// not how many (`Plan.pin`'s memo is still what makes it one per reference).
	resolver := newPrefetchResolver(g.imageResolver(ctx))
	resolver.start(pin.References(src), o.platformOrDefault())

	plan, err := interp.Build(string(src), o.Target,
		interp.WithContextCache(g.contexts),
		interp.WithTerminal(tty != nil),
		interp.WithContext(o.Dir), interp.WithArgs(args),
		interp.WithCommands(g.commands(ctx)),
		interp.WithRemotes(g.remotes(ctx)),
		interp.WithSecrets(secrets),
		interp.WithVersionFlags(o.VersionFlags),
		interp.WithAllowPrivileged(o.AllowPrivileged),
		interp.WithPush(o.Push),
		interp.WithPlatform(o.platformOrDefault()),
		interp.WithGitClone(g.gitClone(ctx)),
		interp.WithImageResolver(resolver.Resolve),
		// Withheld from a dry run, which promises to run nothing: a plan that
		// needs a target built to exist is refused there, saying so (E488).
		interp.WithArtifacts(artifactsFor(ctx, o, g, string(src))))

	endPlan()

	if err != nil {
		return err
	}

	// Kept for the deferred record above: what this build needed is attributed
	// to the conditions it decided along the way.
	g.images = imageRefs(plan)

	if o.DryRun {
		return report(o.Out, plan)
	}

	return build(ctx, o, plan, g, tty)
}

// needsSandbox reports whether any step must run somewhere other than here.
func needsSandbox(plan *interp.Plan) bool {
	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind != ir.OpHost {
			return true
		}
	}

	return false
}

// report prints what would happen, in Earthfile order.
func report(w io.Writer, plan *interp.Plan) error {
	fmt.Fprintf(w, "plan:\n")

	for _, n := range plan.Graph.Nodes() {
		desc := n.Meta.Description
		if desc == "" {
			desc = n.Op.Kind.String()
		}

		fmt.Fprintf(w, "  %-14s %s\n", n.Meta.Source, desc)
	}

	if len(plan.Artifacts) == 0 {
		return nil
	}

	fmt.Fprintf(w, "produces:\n")

	for _, a := range plan.Artifacts {
		if a.LocalDest == "" {
			fmt.Fprintf(w, "  %s\n", a.Path)

			continue
		}

		fmt.Fprintf(w, "  %s -> %s\n", a.Path, a.LocalDest)
	}

	return nil
}

func build(ctx context.Context, o Options, plan *interp.Plan, g *engine, tty *os.File) error {
	e, s, err := runPlan(ctx, o, plan, g, tty)
	if err != nil {
		return err
	}

	// **`runPlan` has already exported.** There were two calls, with identical
	// arguments, one here and one at the end of the run - so every artifact was
	// written out twice and the second write was invisible because it produced
	// the same bytes as the first.
	//
	// Found by timing, not by reading: the export phase logged its whole
	// sequence twice in one build, 0.37s a time for a 45MB binary, on a build
	// whose total was 1.6s (E566). Two calls that agree are the hardest kind of
	// duplicate to see, because nothing about the result is wrong.

	// After the artifacts, because a build that produced both should keep the
	// artifacts even if writing an image fails.
	return writeImages(ctx, o, e, s.StackFor, plan.Images)
}

// runPlan runs a plan and gives back what ran it.
//
// Separated from `build` so a *second* caller can read what a plan produced
// without also exporting it where the invocation asked: planning a
// `FROM DOCKERFILE +gen/` needs the file `+gen` writes, which means running that
// target and reading one file out of it - not exporting its artifacts into the
// project (E488).
//
// The reporting stays here rather than in `build`, so a sub-build's steps appear
// in the output like any others. A target that ran and printed nothing is one
// the reader cannot account for.
func runPlan(
	ctx context.Context, o Options, plan *interp.Plan, g *engine, tty *os.File,
) (*exec.Executor, *core.Scheduler, error) {
	// Everything between a plan and the first step: the executor, the action
	// cache, the profile store, the blob question. Timed because it is the span
	// the stage timings left out, and a span nobody has measured is one that
	// gets blamed on its neighbours (E566).
	endSetup := timing.Phase("setup", o.Target)

	// A build whose every step runs on this machine needs no sandbox, and must
	// not require one: a LOCALLY target is precisely what someone without a
	// container runtime can run, so demanding one to run it is backwards. It
	// also booted a VM, used it for nothing, and tore it down.
	e, err := g.executorFor(plan)
	if err != nil {
		return nil, nil, err
	}

	// The same terminal the plan was built against. An interactive step reaches
	// the executor only if the interpreter accepted it, and it accepted it only
	// because this was not nil - so the two must be the same decision or a step
	// would be planned to prompt and given nowhere to do it.
	e.Terminal = tty

	// Printed as it happens. A build that goes quiet for four minutes and then
	// prints everything is indistinguishable from one that has hung.
	e.Progress = func(step, line string) {
		fmt.Fprintf(o.Out, "  %-14s | %s\n", step, line)
	}

	sb := e.Sandbox()

	// The same cache the conditions were answered against, not a second one over
	// the same directory: see actionCache.
	ac, err := g.actionCache(sb.StoreDir())
	if err != nil {
		return nil, nil, err
	}

	rec := &core.Record{Identity: core.LayerRule}

	// A profile store that cannot be opened is reported rather than skipped: a
	// build quietly running without a cache tier is a build whose speed nobody
	// can account for (I11).
	profiles, err := g.profileStore(sb.StoreDir())
	if err != nil {
		return nil, nil, err
	}

	// The executor and the workers the build schedules over.
	//
	// Both, together, and from one place: a fleet reaches a build through the
	// executor *and* the worker list, and a scheduler that does not know a
	// worker exists never places a step on it whatever executor it holds
	// (E500).
	over, workers := g.scheduling(e, o.Platform)

	// What the L2 tier verifies its hits against: the store's index, with the
	// store itself as the fallback that says when the index lagged (E542).
	//
	// A store that cannot be asked is reported and the build carries on against
	// the store alone - the tier this feeds turns every one of its own failures
	// into a rebuild rather than a wrong answer (I4), so a missing index costs
	// time and nothing else.
	blobs, err := store.OpenBlobs(sb.StoreDir())
	if err != nil {
		fmt.Fprintf(o.Out, "earth: the layer store's index could not be opened,"+
			" so this build verifies its cache against the store directly: %v\n", err)
	}

	// **A store on the guest's device is answered for by the guest.** Stat'ing
	// the host's own root reads an empty answer, `Lookup` turns that into a
	// miss, and the build rebuilds everything it already had - which is what
	// `KindStoreHas` was written for.
	//
	// A separate variable because the index below is still the host's - it
	// closes gaps in a directory the host owns, which is not where the layers
	// are, and only the *lookup* needs to move.
	var (
		present core.BlobStore = blobs
		views                  = viewsFor(sb)
	)

	if guest.StoreInVM() {
		asker, ok := over.(interface {
			StoreHas(context.Context, []ir.NodeID) ([]ir.NodeID, error)
			ViewDigests(context.Context, []ir.NodeID, []string) (map[string]ir.NodeID, map[string]ir.NodeID, error)
		})
		if ok {
			present = &guestBlobs{ask: func(ids []ir.NodeID) ([]ir.NodeID, error) {
				return asker.StoreHas(ctx, ids)
			}}
			views = &guestViews{ask: asker.ViewDigests}
		} else {
			fmt.Fprintln(o.Out, "earth: the layer store is inside the sandbox and"+
				" this executor cannot be asked what it holds, so this build"+
				" caches nothing")
		}
	}

	blobs.Gap = func(id ir.NodeID) {
		fmt.Fprintf(o.Out, "earth: layer %s is in the store and was not in its"+
			" index, which means something filed it without recording it;"+
			" the index has been corrected\n", id)
	}

	s := &core.Scheduler{
		Workers:  workers,
		Executor: over,
		Cache:    ac,
		// The invocation saying "redo it all": reads nothing already there and
		// writes everything it produces, so the *next* build is warm (E462).
		NoCache: o.NoCache,
		Blobs:   present,
		Writer:  writerName,
		Record:  rec,
		// What the mount can take, not what overlayfs allows: the option page
		// runs out an order of magnitude sooner (E49), and Φ exists for exactly
		// this.
		MaxStack: store.MountableStackDepth,

		// The L2 tier, switched on now that a real observation source exists
		// (E119) and the empty-observation trap is closed on the base rather
		// than on the opcode (E125). A COPY over a bumped base image is reused
		// when its destination is unchanged, which is the common expensive miss
		// this tier was designed for.
		//
		// Steps with no source still report nothing, so they publish no profile
		// and every lookup for them misses: the tier costs one absent file read
		// per step and applies only where something actually watched.
		Profiles: profiles,
		Views:    views,
	}

	endSetup()

	endSchedule := timing.Phase("schedule", o.Target)
	_, runErr := s.Run(ctx, plan.Graph)
	endSchedule()

	// Said while it can still be acted on, and once: the guest carries the
	// reason back with each step and the first one is kept (E123).
	warnUnbounded(o.Out, e.Degraded())

	// Said before the reader meets `docker: not found` from a step, rather than
	// after (E146).
	warnNoDockerClient(o.Out, e.DockerNote())

	// And before they conclude a change they made is not working (E499).
	if note := e.GuestNote(); note != "" && o.Out != nil {
		fmt.Fprint(o.Out, note)
	}

	// A tolerated failure has already let everything downstream run, and what a
	// FINALLY declared still has to be exported - which is the entire point of
	// TRY. Returning here would fail the build correctly and throw away the one
	// thing it exists to keep.
	var tolerated *core.ToleratedFailureError

	if runErr != nil && !errors.As(runErr, &tolerated) {
		// The step's own diagnostic is the useful part and already names the
		// line; wrapping it in "build failed" would only push it further from
		// the top of the message.
		return nil, nil, runErr
	}

	for _, r := range rec.Steps {
		// Ten wide because "uncaptured" is ten: an outcome column narrower than
		// its widest outcome shunts the description out of line on exactly the
		// steps whose outcome most needs reading.
		fmt.Fprint(o.Out, stepRow(r.Meta.Source, r.Outcome.String(), r.Meta.Description))
	}

	// After the steps, because these are about the build as a whole and belong
	// where a reader has finished reading the per-step lines.
	fmt.Fprint(o.Out, cacheSummary(s.Stats))

	// What each mutable reference resolved to (§3.4d): the one input a key
	// cannot be closed over, so the one worth naming.
	recordPinning(o.Out, plan.Pinned, plan.PinCost)

	// Whether the fleet this build waited for did anything (E505).
	if d, ok := g.fleetExec().(*fleet.Delegating); ok {
		fmt.Fprint(o.Out, fleetSummary(d.Spend()))
	}

	// What the build spent, where the invocation asked for it (E467).
	if o.ExecStats {
		fmt.Fprint(o.Out, usageSummary(s.Stats))
	}
	fmt.Fprint(o.Out, whyItReran(sb.StoreDir(), o.Target, rec))
	fmt.Fprint(o.Out, conflictWarning(ac.Conflicts(), ac.ConflictCount()))

	// Written after it has been compared against, and best-effort: a record
	// that could not be saved costs the *next* build its explanation and this
	// one nothing, so failing here would trade a working build for a
	// diagnostic.
	_ = saveRecord(sb.StoreDir(), o.Target, rec)

	endExport := timing.Phase("export", o.Target)
	err = exportAll(ctx, o, e, s, plan)
	endExport()

	if err != nil {
		return nil, nil, err
	}

	// After the artifacts, because a build that produced both should keep the
	// artifacts even if writing an image fails.
	err = writeImages(ctx, o, e, s.StackFor, plan.Images)
	if err != nil {
		return nil, nil, err
	}

	// The run's own failure comes back with what ran it, not instead of it: a
	// tolerated failure has already let a FINALLY run, and the caller still has
	// to export what the build produced.
	return e, s, runErr
}

func exportAll(ctx context.Context, o Options, e *exec.Executor, s *core.Scheduler, plan *interp.Plan) error {
	// **Before anything is looked up, not per artifact.** The steps still ran
	// and the cache is still filled; the only thing withheld is the write to
	// somebody's working tree.
	if o.NoOutput {
		return nil
	}

	for _, a := range plan.Artifacts {
		if a.LocalDest == "" {
			continue
		}

		stack := s.StackFor(a.From)
		if len(stack) == 0 {
			return fmt.Errorf("%s: the step producing %s did not run", a.Source, a.Path)
		}

		// Relative to the project directory, not to wherever the process happens
		// to have been started.
		dest := filepath.Join(o.Dir, localPath(a.LocalDest, a.Name))

		// The interpreter already refuses a destination that leaves the
		// project, and this is the layer that does the writing. A check here
		// does not depend on that one having been right.
		if !within(o.Dir, dest) {
			return fmt.Errorf("%s: %q is not inside the project", a.Source, a.LocalDest)
		}

		err := e.Export(ctx, stack, a.Path, dest, a.IfExists)
		if err != nil {
			return err
		}

		fmt.Fprintf(o.Out, "  %-14s %s -> %s\n", a.Source, a.Path, dest)
	}

	return nil
}

var _ = ir.NodeID{}

// localPath is where an artifact lands on this machine.
//
// A destination that ends in a separator, or is already a directory, names
// somewhere to *put* the artifact rather than the artifact's new name:
// `SAVE ARTIFACT ./package.json package.json AS LOCAL ./` means "put it here",
// and writing it as `./` failed with "is a directory". The same rule COPY
// needed, arriving from the other end.
func localPath(dest, name string) string {
	if name == "" {
		return dest
	}

	if strings.HasSuffix(dest, "/") || dest == "." || dest == ".." {
		return filepath.Join(dest, name)
	}

	fi, err := os.Stat(dest)
	if err == nil && fi.IsDir() {
		return filepath.Join(dest, name)
	}

	return dest
}

// artifactsFor is the build capability, or nothing where the caller must not
// have it.
func artifactsFor(ctx context.Context, o Options, g *engine, src string) interp.Artifacts {
	if o.DryRun {
		return nil
	}

	return g.artifacts(ctx, o, src)
}

// viewsFor reads the layer store the way this sandbox presents it.
//
// A sandbox that shares the store into a VM shows it owned by root, and the
// guest's observations are digested that way; a view reading the store's own
// ownership can then never match one, which is why Κ₂ served no RUN on darwin
// (E494).
//
// An optional interface rather than a method on `Sandbox`: three
// implementations and every test double would have to answer a question only
// one of them has an interesting answer to. `TestTheDarwinSandboxSaysHowItShares`
// is what stops that being a rule nobody notices going missing.
func viewsFor(sb exec.Sandbox) core.ViewSource {
	store := store.LayerStore(sb.StoreDir())

	shared, ok := sb.(interface{ SharesStoreAsRoot() bool })
	if !ok || !shared.SharesStoreAsRoot() {
		return store
	}

	return store.SeenAsRoot(uint32(os.Getuid()), uint32(os.Getgid())) //nolint:gosec // ids are small
}

// scheduling is what a build schedules over: the fleet if one was joined, this
// machine otherwise.
//
// `EARTH_FLEET_WORKERS` made the driver wait for workers, announce them, and
// hand back a `fleet.Delegating` whose `Remote()` names them - and that reached
// the scheduler used to answer *conditions* and not the one that runs the build.
// A fleet was joined, printed, and never used (E500).
//
// The invoker is always in the list. It runs steps too, and a build that placed
// nothing locally would be slower on a one-worker fleet than with no fleet.
// A method rather than a function taking the fleet: a free function let the
// *call site* pass nil and no test noticed, which is the seam E465 named -
// something set and then not read is indistinguishable from something never set.
// fleetExec is the fleet executor the sandbox built, or nil.
//
// Behind the lock because it is written on the prewarm goroutine and read here,
// and the `sync.Once` that writes it only synchronises with callers of `Do` -
// which this is not (E610).
func (g *engine) fleetExec() core.Executor {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.fleetEx
}

func (g *engine) scheduling(local core.Executor, platform string) (core.Executor, []core.Worker) {
	workers := []core.Worker{localWorker(platform)}

	fleetEx := g.fleetExec()
	if fleetEx == nil {
		return local, workers
	}

	if d, ok := fleetEx.(*fleet.Delegating); ok {
		workers = append(workers, d.Remote()...)
	}

	return fleetEx, workers
}
