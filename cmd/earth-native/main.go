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
	"os"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/guestd"
	"github.com/EarthBuild/earthbuild/engine/store"
)

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
		args     buildArgs
	)

	flag.Var(&args, "build-arg", "set a build argument as NAME=VALUE; repeatable")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: earth-native [flags] <target|ls|doc>\n\n")
		flag.PrintDefaults()
	}

	flag.Parse()

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

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

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
		Dir:      *dir,
		Target:   flag.Arg(0),
		Platform: *platform,
		Args:     args,
		DryRun:   *dryRun,
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
