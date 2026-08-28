package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

// EnvParallelExport writes several artifacts at once.
//
// **Because an export is an unmount, and unmounts are the whole cost.** Staging
// an artifact and copying it out are free - 0.06ms to materialise, 0.00ms to
// stage, 0.00ms to copy - and releasing the handle afterwards is 18.19ms of an
// 18.25ms export. That release is `unix.Unmount` then `os.RemoveAll`, measured
// at 15.8ms and 3.5ms on Linux, where making the mount costs 5us (E817).
//
// `exportAll` does them one at a time, so a build with N artifacts pays N of
// those in a row. Thirty-two of them is 582ms of a 1425ms build, and taking the
// `SAVE ARTIFACT` out of that build entirely brings it to 751ms.
//
// **The kernel only half-allows it.** Thirty-two overlay unmounts take 87ms
// serially and 36ms sixteen at a time - 2.4x, not 32x, because `namespace_sem`
// is held for write by each one. So this is worth having and is not a rewrite of
// the cost; the cost is that an overlay is three thousand times dearer to
// destroy than to create.
//
// Off by default: it changes when a failing build stops writing. Serially, an
// artifact after a failure is never written; concurrently, one already in flight
// may land. Nothing reads those files but the person who ran the build, and the
// error is the same error - but it is a behaviour change and it should be asked
// for until it has been lived with.
const EnvParallelExport = "EARTH_PARALLEL_EXPORT"

// exportWidth is how many artifacts are written at once. Zero, and anything
// unparseable, means off.
//
// A number sets the width directly; anything else that is not an off spelling
// takes NumCPU, bounded at 8 - past that the unmounts are queueing on the
// kernel's mount lock rather than making progress, and the extra goroutines only
// make the queue longer.
func exportWidth() int {
	raw := os.Getenv(EnvParallelExport)

	switch raw {
	case "", "0", "false", "no":
		return 0
	}

	n, err := strconv.Atoi(raw)
	if err == nil {
		if n < 1 {
			return 0
		}

		return n
	}

	return min(runtime.NumCPU(), 8)
}

// exportConcurrently writes the artifacts several at a time.
//
// **Order is kept where order is observable.** Two artifacts naming the same
// destination are written in the order the Earthfile gives, because the second
// is meant to win; artifacts naming different destinations cannot see each
// other and go in any order. So the work is grouped by destination, the groups
// run concurrently, and each group runs in sequence.
//
// What the caller sees is ordered whatever happened: the lines are printed after
// the wait, in the Earthfile's order, and the error returned is the earliest by
// that order rather than the first to arrive. A build that fails must fail the
// same way twice.
func exportConcurrently(
	ctx context.Context, o Options, e *exec.Executor, s *core.Scheduler,
	plan *interp.Plan, width int,
) error {
	type job struct {
		at   int
		dest string
		a    interp.Artifact
	}

	var (
		jobs   []job
		groups = map[string][]job{}
		order  []string
	)

	for i, a := range plan.Artifacts {
		if a.LocalDest == "" {
			continue
		}

		stack := s.StackFor(a.From)
		if len(stack) == 0 {
			return fmt.Errorf("%s: the step producing %s did not run", a.Source, a.Path)
		}

		dest := filepath.Join(o.Dir, localPath(a.LocalDest, a.Name))

		if !a.Force && !within(o.Dir, dest) {
			return fmt.Errorf("%s: %q is not inside the project", a.Source, a.LocalDest)
		}

		j := job{at: i, dest: dest, a: a}
		jobs = append(jobs, j)

		if _, seen := groups[dest]; !seen {
			order = append(order, dest)
		}

		groups[dest] = append(groups[dest], j)
	}

	// Indexed by the artifact's position, so a result can be reported in the
	// Earthfile's order however it was produced.
	errs := make([]error, len(plan.Artifacts))

	// Cancelled on the first failure, so the artifacts still queued behind it
	// stop rather than carrying on writing into a tree the build has already
	// given up on. Not a guarantee that none of them lands - one already in
	// flight will finish - which is the behaviour change this is gated for.
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	var (
		wg   sync.WaitGroup
		slot = make(chan struct{}, width)
		mu   sync.Mutex
		bad  bool
	)

	for _, dest := range order {
		wg.Add(1)

		go func(group []job) {
			defer wg.Done()

			slot <- struct{}{}
			defer func() { <-slot }()

			for _, j := range group {
				mu.Lock()
				give := bad
				mu.Unlock()

				if give {
					return
				}

				err := e.Export(ctx, s.StackFor(j.a.From), j.a.Path, j.dest,
					j.a.IfExists, j.a.Force)
				if err != nil {
					mu.Lock()
					errs[j.at] = err
					bad = true
					mu.Unlock()
					stop()

					return
				}
			}
		}(groups[dest])
	}

	wg.Wait()

	for _, j := range jobs {
		if errs[j.at] != nil {
			return errs[j.at]
		}

		fmt.Fprintf(o.Out, "  %-14s %s -> %s\n", j.a.Source, j.a.Path, j.dest)
	}

	return nil
}
