package interp

import (
	"fmt"
	"strings"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/util/flagutil"
)

// runFlags reads RUN's options off the front of its command.
//
// Without this the flags were part of the command: `RUN --no-cache fetch`
// became `sh -c "--no-cache fetch"`, a command nobody wrote, which fails saying
// `--no-cache` is not a program. A hundred and eleven RUN lines in this
// repository carry a flag, so the failure was not obscure - it was simply never
// reached, because the corpus measures planning and this defect is in what gets
// run.
//
// The flags split cleanly in two, and the division is the same one that decides
// `SAVE IMAGE --cache-from`:
//
//   - `--no-cache` changes whether the step may be cached, which is a property
//     of the step and reaches its key;
//   - everything else changes what the step can *do* - reach the network, hold
//     a secret, mount a directory, run privileged - and is refused rather than
//     stripped, because a step that quietly loses its secret does not fail, it
//     produces the wrong thing.
//
// env is consulted for a flag's value and never for the command's.
//
// A flag has no later reader: nothing downstream expands `--mount=$SPEC`, so an
// engine that leaves it alone leaves it broken. A command does have one - the
// shell - which is why the same substitution must not be applied to it (E65,
// E66).
// runOpts is what a RUN's flags said, once the refused ones are out of the way.
//
// A struct rather than the six return values this had: adding `--network=none`
// would have made seven, and a caller unpacking seven positional results is one
// transposition away from marking the wrong step uncacheable.
type runOpts struct {
	// ssh is `RUN --ssh`: the step may talk to the invoking user's agent.
	ssh bool
	// pushOnly is `RUN --push`: the step belongs to a push, and this engine has
	// no push mode, so it contributes nothing to the build (E436).
	pushOnly   bool
	rest       []string
	noCache    bool
	entrypoint bool
	// noNet is `--network=none`: the step runs with no network at all.
	noNet bool
	// interactive is `--interactive`: the step runs on the caller's terminal.
	interactive bool
	mounts      []ir.Mount
	secrets     []string
}

func runFlags(c earthfile.Command, env map[string]string, hasTerminal bool) (runOpts, error) {
	// The exec form takes no flags: `RUN ["a", "--b"]` is an argv, and reading
	// `--b` as an option would eat an argument the author wrote deliberately.
	if c.ExecMode {
		return runOpts{rest: c.Args}, nil
	}

	var opts cmdopts.Run

	rest, err := flagutil.ParseArgsCleaned("RUN", &opts, c.Args)
	if err != nil {
		return runOpts{}, flagFault("RUN", loc(c.SourceLocation), err)
	}

	for _, u := range []struct {
		set  bool
		name string
	}{
		{opts.WithAWS, "--aws"},
		{opts.OIDC != "", "--oidc"},
		{opts.WithDocker, "--with-docker"},
		{opts.InteractiveKeep, "--interactive-keep"},
	} {
		if u.set {
			return runOpts{}, unsupported("RUN "+u.name, loc(c.SourceLocation), "")
		}
	}

	// Refused on purpose, and measured before deciding (E157). A step here
	// already holds `CapEff 000001ffffffffff` - every capability - and can
	// mount a tmpfs; what it cannot do is reach past its namespace, which
	// `mknod` of a device refuses with EPERM. Capabilities are namespaced, so
	// root in a user namespace is not root.
	//
	// Neither half of "not supported by the native engine" was true: there is
	// nothing to implement, and switching engines is the wrong advice for the
	// common case. The corpus's own instance is
	// `RUN --privileged echo "hello …" > a.txt`, which needs no privilege at
	// all - so the refusal leads with the fix that works.
	if opts.Privileged {
		return runOpts{}, refusedOnPurpose("RUN --privileged", loc(c.SourceLocation),
			"a step here already has every capability, and cannot reach past its"+
				" namespace whatever the flag says - no device nodes, no host mounts"+
				"\n  if the command does not need those, remove the flag")
	}

	// A prompt needs a terminal, and a terminal is the caller's to supply. With
	// one the step runs on it (E189-E193); without, this is the same shape as a
	// secret nobody passed - a valid Earthfile and an incomplete invocation.
	if opts.Interactive && !hasTerminal {
		return runOpts{}, fmt.Errorf(
			"RUN --interactive at %s needs a terminal and this invocation has none"+
				"\n  run it from a terminal, or drop --interactive: %w",
			loc(c.SourceLocation), ErrNotProvided)
	}

	// `none` is the only value earthfile.md gives this flag - its heading is
	// literally `--network=none` - so any other is refused by name rather than
	// guessed at. The dangerous direction is the guess: a step asking for host
	// networking and silently getting an empty namespace fails looking like a
	// broken mirror, a long way from the line that caused it.
	if opts.Network != "" && opts.Network != "none" {
		return runOpts{}, unsupported(
			"RUN --network="+opts.Network, loc(c.SourceLocation), "")
	}

	// `--push` is refused, and this comment used to say it was recorded
	// elsewhere. It was not: the flag appeared in the parser and in no other
	// file in the engine, so `RUN --push ./publish.sh` ran on every build - the
	// one thing the option exists to prevent (E436). A claim about a mechanism,
	// outliving the mechanism, in the comment that explained why no test was
	// needed.
	//
	// Not refused, because refusing costs seven of this repository's own targets
	// and refusing is not what the reference does either: without push mode, a
	// push command does not run and its filesystem changes are not part of the
	// image. So the step is planned away rather than planned - the faithful
	// answer, and the one that keeps the Earthfiles building.

	// `--raw-output` is about how output is printed and not about what the step
	// produces, so it is accepted and has no effect on the plan by design.
	out := runOpts{
		// An interactive step is never cached: what a person typed is not a
		// function of the inputs, so neither is the result. The same reasoning
		// `--no-cache` rests on, and with no argument on the other side.
		noCache:     opts.NoCache || opts.Interactive,
		interactive: opts.Interactive,
		entrypoint:  opts.WithEntrypoint,
		noNet:       opts.Network == "none",
		secrets:     opts.Secrets,
		// The invoking user's ssh agent, which is how a build reaches a private
		// dependency without a key ever being written into an image (E466).
		ssh:      opts.WithSSH,
		pushOnly: opts.Push,
	}

	if len(rest) == 0 {
		// `--entrypoint` alone is complete: the command is the image's own, and
		// an image whose entrypoint is a whole program needs no arguments to
		// run it. The corpus writes exactly that.
		if opts.WithEntrypoint {
			return out, nil
		}

		return runOpts{}, fmt.Errorf("RUN needs a command (%s)", loc(c.SourceLocation))
	}

	for _, spec := range opts.Mounts {
		m, err := parseMount(expandWith(spec, env), loc(c.SourceLocation))
		if err != nil {
			return runOpts{}, err
		}

		out.mounts = append(out.mounts, m)
	}

	out.rest = rest

	return out, nil
}

// runArgv turns the command left after the flags into an argv.
func runArgv(c earthfile.Command, rest []string, entrypoint bool) []string {
	// `--entrypoint` hands its arguments to the image's own entrypoint, which is
	// a program and not a shell - so they must not be re-split, exactly as in
	// exec form. `RUN --entrypoint -- -f api.proto` means protoc gets three
	// arguments, not a sentence.
	if entrypoint {
		return rest
	}

	if c.ExecMode {
		// Exec form: the author asked for no shell, which is exactly what it is
		// for - a command whose arguments must not be re-split.
		return rest
	}

	return shell(strings.Join(rest, " "))
}
