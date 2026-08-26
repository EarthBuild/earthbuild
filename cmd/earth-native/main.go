// Command earth-native builds an Earthfile with the native engine.
//
// A thin front end over engine/cli, which holds everything worth testing. This
// binary exists so the engine is reachable from a terminal; it will become
// `earthly --engine=native` once the flag is wired through the existing CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/guestd"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// secretList collects repeated -secret flags.
//
// **Same spelling as the other front end takes**, because the two put the same
// argument in front of the same engine: `--secret NAME=VALUE`, or a bare
// `--secret NAME` meaning "take it from the environment", which is how a
// credential reaches a build without appearing in anybody's shell history.
type secretList map[string]string

func (l *secretList) String() string { return "" }

func (l *secretList) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	if name == "" {
		return fmt.Errorf("expected NAME=VALUE or NAME, found %q", v)
	}

	if !ok {
		got, present := os.LookupEnv(name)
		if !present {
			return fmt.Errorf("--secret %s takes its value from the environment,"+
				" and %s is not set", name, name)
		}

		value = got
	}

	(*l)[name] = value

	return nil
}

// secretFiles collects repeated -secret-file flags, as `NAME=path`.
type secretFiles []string

func (f *secretFiles) String() string { return "" }

func (f *secretFiles) Set(v string) error {
	if !strings.Contains(v, "=") {
		return fmt.Errorf("expected NAME=PATH, found %q", v)
	}

	*f = append(*f, v)

	return nil
}

// buildArgs collects repeated -build-arg flags.
type buildArgs map[string]string

// Pointer receiver to match Set, because a type whose methods disagree about
// it satisfies `flag.Value` only by accident of which one is addressable
// (recvcheck).
func (a *buildArgs) String() string { return "" }

func (a *buildArgs) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	if name == "" {
		return fmt.Errorf("expected NAME=VALUE, found %q", v)
	}

	if !ok {
		// **A bare name means the environment**, which is the spelling
		// `earthly --build-arg NAME` accepts and what `variables` has always
		// done for the other backend. The two front ends put the same argument
		// in front of the same engine, so a flag one takes and the other refuses
		// is a script that works until somebody changes which binary they call.
		//
		// What the refusal here was right about was *guessing an empty value* -
		// `-build-arg version` being a typo that built something nobody asked
		// for and said nothing. Looking the name up does not guess, and a name
		// nobody exported is still refused below.
		value, ok = os.LookupEnv(name)
		if !ok {
			return fmt.Errorf(
				"%q has no value and %s is not set in the environment"+
					"\n  write it as %s=<value>, or export %s before building",
				v, name, v, name)
		}
	}

	if *a == nil {
		*a = buildArgs{}
	}

	(*a)[name] = value

	return nil
}

func main() {
	// **This binary is also the sandbox agent.** `earth guestd ...` runs it, and
	// that is how the agent reaches places the CLI is copied into - a nested
	// build inside a step runs a copy of this binary and has nowhere beside it
	// to put a second file. Before flag parsing, because the agent's arguments
	// are its own.
	if len(os.Args) > 1 && os.Args[1] == guestd.Command {
		guestd.Main(os.Args[2:])

		return
	}

	// **And it is also the step shim**, which matters here and not in the
	// macOS arrangement: there the guest is a separate binary and the shim
	// re-executes that, which dispatches it. On Linux this binary *is* the
	// guest, so the shim re-executes this - and without this line the flags it
	// was launched with are read as the CLI's own, which prints a usage message
	// and fails the step. A nested build is exactly where that happens, and
	// nested builds are most of this project's own test suite.
	guest.RunStepShimIfAsked()
	guest.RunDaemonShimIfAsked()

	// Having got past that, this binary demonstrably dispatches the agent - so
	// the engine may run it as one rather than hunting for a separate file.
	exec.SelfServesAsGuest()

	var (
		dir      = flag.String("dir", ".", "directory holding the Earthfile; also the build context")
		platform = flag.String("platform", "", "os/arch to build for; the sandbox's own when empty")
		dryRun   = flag.Bool("dry-run", false, "resolve the plan and print it without running anything")
		stopSb   = flag.Bool("stop-sandbox", false, "remove the persistent sandbox VM and exit")
		doPin    = flag.Bool("pin", false, "write each image reference's digest into the Earthfile and exit")
		long     = flag.Bool("long", false, "with `doc`, also list what each target needs and produces")
		prune    = flag.String("prune", "", "remove least-recently-used layers until the store fits in this size, and exit")
		// Wiring, not mechanism: engine/cli already reads all three and had no
		// way to be told. The names are earthly's, because a flag that does the
		// same thing under a different spelling is a compatibility gap wearing
		// a disguise.
		argFile = flag.String("arg-file-path", "",
			"read build arguments from this file (default \".arg\")")
		secretFile = flag.String("secret-file-path", "",
			"read secrets from this file (default \".secret\")")
		// Comma-separated, as earthly takes it. The seven corpus invocations
		// that pass one name a feature this engine implements unconditionally,
		// so what they need is for the flag to be understood rather than for
		// anything to change (E473).
		versionFlags = flag.String("version-flag-overrides", "",
			"turn on these VERSION features for every file, comma-separated")
		push = flag.Bool("push", false,
			"this build is a push: `RUN --push` steps run rather than being"+
				" planned away")
		allowPriv = flag.Bool("allow-privileged", false,
			"accept RUN --privileged, which this engine otherwise refuses")
		noCache = flag.Bool("no-cache", false,
			"build every step, reading no cache entry that is already there")
		noOutput = flag.Bool("no-output", false,
			"do not write SAVE ARTIFACT AS LOCAL artifacts to the working tree")
		ci = flag.Bool("ci", false,
			"execute in CI mode; implies -no-output (this engine is already strict)")
		// Made here rather than on first use, for the reason the two below are:
		// a nil map takes no assignment, and the arguments written after the
		// target are merged into this one whether or not -build-arg was used.
		args = buildArgs{}
		// Maps are made here rather than on first use: `flag.Var` hands the
		// value a pointer and calls Set on it, and Set on a nil map panics.
		secrets         = secretList{}
		secretFilePaths secretFiles
	)

	flag.Var(&args, "build-arg", "set a build argument as NAME=VALUE; repeatable")
	flag.Var(&secrets, "secret",
		"a secret as NAME=VALUE, or NAME to take it from the environment; repeatable")
	flag.Var(&secretFilePaths, "secret-file",
		"a secret whose value is a file's contents, as NAME=PATH; repeatable")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: earth-native [flags] <target|ls|doc>\n\n")
		flag.PrintDefaults()
	}

	// **Before parsing**, because Go's flag package stops at the first non-flag
	// argument: `doc --long` reaches it as a subcommand with an argument, and
	// the flag is reported as a build argument that is not one.
	flag.CommandLine.Parse(hoistSubcommandFlags(os.Args[1:])) //nolint:errcheck // ExitOnError

	// The sandbox outlives a build on purpose. This is how it is taken away,
	// and it takes no target because it is not a build.
	if *stopSb {
		err := cli.RemoveSandbox()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		return
	}

	// Not a build either, and it takes no target: it resolves what the file
	// names and edits the file. The one thing here that changes a file the user
	// wrote, so it happens only when asked for by name.
	if *doPin {
		report(cli.Pin(cli.Options{Dir: *dir, Out: os.Stdout, Platform: *platform}))

		return
	}

	// Not a build: it takes no target and removes things, so like -pin it
	// happens only when asked for by name. See cli.Prune for why it is never
	// something a build does on its way past.
	if *prune != "" {
		keep, err := store.ParseSize(*prune)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}

		report(cli.Prune(cli.Options{Dir: *dir, Out: os.Stdout}, keep))

		return
	}

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	// **Everything after the target is a build argument.** `+target --ARG=value`
	// is the form the language uses and a person types; `-build-arg NAME=value`
	// before the target keeps working and the two are merged, with what follows
	// the target winning - it is the more specific of the two and the one
	// written closest to what it applies to.
	after, argErr := argsAfterTarget(flag.Args()[1:])
	if argErr != nil {
		fmt.Fprintln(os.Stderr, argErr)
		os.Exit(2)
	}

	maps.Copy(args, after)

	// Two words that are not targets. Both read the Earthfile and neither plans
	// or runs anything, so they are answered before a sandbox is thought about
	// (E474).
	switch flag.Arg(0) {
	case "ls":
		report(cli.List(cli.Options{Dir: *dir, Out: os.Stdout}))

		return

	case "doc":
		report(cli.Doc(cli.Options{Dir: *dir, Out: os.Stdout, Long: *long}))

		return
	}

	// Ctrl-C cancels the build rather than killing this process where it
	// stands: the guest holds mounts and handles, and a mount left behind keeps
	// a root busy until the machine is restarted. A second interrupt is not
	// caught, so a wedged build can still be stopped (E179).
	ctx, stop := cli.InterruptContext(context.Background())
	defer stop()

	err := cli.Run(ctx, cli.Options{
		Dir:             *dir,
		Target:          flag.Arg(0),
		Platform:        *platform,
		Args:            args,
		Secrets:         secrets,
		SecretFiles:     secretFilePaths,
		DryRun:          *dryRun,
		ArgFile:         *argFile,
		SecretFile:      *secretFile,
		NoCache:         *noCache,
		AllowPrivileged: *allowPriv,
		Push:            *push,
		VersionFlags:    splitList(*versionFlags),
		// **`--ci` means `--no-output --strict`.** Strict is what this engine
		// already is: it refuses what it cannot reproduce rather than offering
		// the choice (I10), so there is nothing for the flag to switch on. What
		// remains is leaving the working tree alone, which is the half a build
		// machine actually wants.
		NoOutput: *noOutput || *ci,
		Out:      os.Stdout,
	})
	if err != nil {
		// Bare, with no "error:" prefix: these diagnostics are written to be read
		// as prose and already name the construct, the line and the remedy.
		fmt.Fprintln(os.Stderr, err)

		// **Before the exit, because `defer` does not survive it.** `stop()`
		// releases the signal handler this installed; leaving it to a deferred
		// call that `os.Exit` skips means the tidy-up is written down and never
		// performed (gocritic exitAfterDefer).
		stop()
		os.Exit(1) //nolint:gocritic // stop() is called above, which is the point
	}
}

// report ends the process on an error, and says nothing otherwise.
//
// Bare, with no "error:" prefix: these diagnostics are written to be read as
// prose and already name the construct, the line and the remedy.
func report(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// splitList is a comma-separated flag value as a list, with the empty value as
// no entries rather than one empty one.
func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}

	out := strings.Split(v, ",")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}

	return out
}
