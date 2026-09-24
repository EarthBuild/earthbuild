package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// defaultBuildkitImage is the daemon a comparison needs.
//
// **Pinned, because the branch's own tag does not exist.** Without this the
// buildkit side dies with `manifest unknown` from `docker run`, which reads as a
// broken build rather than a missing image, and the comparison silently becomes
// "native fails, buildkit fails" for every target.
const defaultBuildkitImage = "ghcr.io/earthbuild/earthbuild:buildkitd-v0.8.17-fix.1"

func main() {
	var (
		file     = flag.String("f", "", "the Earthfile to build (copied in as `Earthfile`)")
		context  = flag.String("C", "", "a directory to copy as the build context, if the target needs one")
		image    = flag.String("buildkit-image", defaultBuildkitImage, "the buildkit daemon image")
		timeout  = flag.Duration("timeout", 10*time.Minute, "per-engine limit")
		earthBin = flag.String("earth", "earth", "the earth binary to run")
	)

	flag.Parse()

	if *file == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: earth-diff -f <earthfile> [-C <context>] +target [build args...]")
		os.Exit(2)
	}

	target, args := flag.Arg(0), flag.Args()[1:]

	native, err := run(*earthBin, *file, *context, target, args, false, *image, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "native: %v\n", err)
		os.Exit(3)
	}

	buildkit, err := run(*earthBin, *file, *context, target, args, true, *image, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "buildkit: %v\n", err)
		os.Exit(3)
	}

	v := Compare(native, buildkit)
	fmt.Printf("%s\t%s\tnative=%d\tbuildkit=%d\n", v, target, native, buildkit)

	// Only a gap is a failure of this engine. Agreement is the expected answer
	// and "ahead" is good news, so neither is worth a non-zero exit.
	if v == NativeGap {
		os.Exit(1)
	}
}

// run builds the target under one engine in a directory of its own, and returns
// the exit code.
//
// **A fresh directory per engine, every time.** The two engines write into the
// tree they build - `SAVE ARTIFACT ... AS LOCAL` lands beside the Earthfile - so
// sharing one directory lets whichever ran first decide what the second one
// finds. That is the confound this tool exists to remove, and it is easy to
// reintroduce by being tidy about temporary directories.
func run(
	bin, file, contextDir, target string, args []string,
	buildkit bool, image string, timeout time.Duration,
) (int, error) {
	dir, err := os.MkdirTemp("", "earth-diff-")
	if err != nil {
		return 0, err
	}

	defer func() { _ = os.RemoveAll(dir) }()

	work := dir
	if contextDir != "" {
		work = filepath.Join(dir, "ctx")

		err = os.CopyFS(work, os.DirFS(contextDir))
		if err != nil {
			return 0, fmt.Errorf("copy the context: %w", err)
		}
	}

	src, err := os.ReadFile(file) //nolint:gosec // the Earthfile to compare is the caller's choice
	if err != nil {
		return 0, err
	}

	//nolint:gosec // work is a temporary directory this function made
	err = os.WriteFile(filepath.Join(work, "Earthfile"), src, 0o600)
	if err != nil {
		return 0, err
	}

	argv := append(append([]string{}, args...), target)
	if buildkit {
		argv = append([]string{"--engine", "buildkit"}, argv...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, argv...) //nolint:gosec // the binary is the caller's choice
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"XDG_CACHE_HOME="+filepath.Join(dir, "cache"),
		"EARTH_BUILDKIT_IMAGE="+image,
	)

	if !buildkit {
		cmd.Env = append(cmd.Env, "EARTH_ENGINE=native")
	}

	werr := cmd.Run()

	if code := cmd.ProcessState.ExitCode(); code >= 0 && ctx.Err() == nil {
		return code, nil
	}

	if ctx.Err() != nil {
		return 0, fmt.Errorf("%s did not finish within %s", engineName(buildkit), timeout)
	}

	return 0, werr
}

func engineName(buildkit bool) string {
	if buildkit {
		return "buildkit"
	}

	return "native"
}
