//go:build linux

package fleet_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/trace"
)

// A real step runs on a lazily materialised base and produces the right layer.
//
// **The whole thesis in one test.** A base is primed with what the step was
// predicted to read; a real command runs against it, under the real tracer;
// the one path nobody predicted faults in through the real filler; the step
// writes; and the capture leaves out everything this engine placed.
//
// The assertion is the only one that matters: **the layer is the same one the
// step would have produced against a whole base** (I1, E306). Everything else -
// how little moved, how many faults - is a measurement rather than a promise.
func TestARealStepOnALazyBaseProducesTheRightLayer(t *testing.T) {
	store := t.TempDir()
	id := aBiggerLayer(t, store)

	from := []fleet.Fragmenter{&fromStore{layers: &fleet.Layers{Root: store}}}

	// One directory: what the step reads and where it writes, which is the
	// arrangement a lazy base forces (E293).
	work := t.TempDir()

	f := &fleet.Filler{
		Into:  work,
		Stack: []ir.NodeID{id},
		From:  from,
		Store: &fleet.Fragments{Root: t.TempDir()},
	}

	// What the driver predicted.
	predicted := []string{"etc/hosts"}

	err := f.Prime(context.Background(), predicted)
	if err != nil {
		t.Fatalf("priming: %v", err)
	}

	placed := placedUnder(t, work)

	// The step: reads what was predicted, reads one thing that was not, writes
	// its own output.
	script := "cat etc/hosts > /dev/null && " +
		"cat usr/lib/lib7.so > /dev/null && " +
		"printf 'made by the step\\n' > out"

	faulted := runTraced(t, work, script, f)

	if len(faulted) == 0 {
		t.Fatal("nothing faulted in; the step read a path nobody predicted")
	}

	for p, id := range faulted {
		placed[p] = id
	}

	got, err := layer.TakeExcluding(work, placed)
	if err != nil {
		t.Fatalf("capturing: %v", err)
	}

	// What the same step produces with the whole base: only its own writes.
	whole := t.TempDir()

	// **0o644, because the step's shell writes it.** This file exists to be the
	// same file the script produced, and `printf > out` gets the default umask.
	// Tightening it for gosec made the eager side a file the lazy side could
	// never be, and the comparison then failed for the fixture's reason rather
	// than the engine's (E632).
	//nolint:gosec // mirrors what the step wrote
	err = os.WriteFile(filepath.Join(whole, "out"), []byte("made by the step\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	stampLike(t, filepath.Join(whole, "out"), filepath.Join(work, "out"))
	stampLike(t, whole, work)

	want, err := layer.Take(whole)
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != want.ID {
		// **Which half differs decides where to look.** Content is the identity
		// with mtimes excluded (§3.3, §6), so equal content and differing ids
		// means the two bases disagree about *when*, and differing content means
		// they disagree about what is there at all - a leaked directory, a mode,
		// a byte. Saying which turns one failing digest into a direction.
		same := "and their content digests differ too, so the trees are not the same tree"
		if got.Content == want.Content {
			same = "though their content digests match, so the difference is mtimes alone"
		}

		t.Errorf("a lazily materialised step produced %v and an eagerly"+
			" materialised one produces %v"+
			"\n  %s"+
			"\n  the same step must produce the same layer, or the cache is a"+
			" lottery", got.ID, want.ID, same)
	}

	t.Logf("predicted %d path(s), faulted in %d, produced %v",
		len(predicted), len(faulted), got.ID)
}

// placedUnder is everything in a directory, as the engine put it there.
func placedUnder(t *testing.T, root string) map[string]ir.NodeID {
	t.Helper()

	out := map[string]ir.NodeID{}

	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == root {
			return nil //nolint:nilerr // the root is not something placed in it
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil //nolint:nilerr // not ours
		}

		// A directory priming made is placed too, and says so with a zero
		// digest: in an overlay it would not exist in the delta at all (E306).
		if d.IsDir() {
			out[rel] = ir.NodeID{}

			return nil
		}

		body, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil //nolint:nilerr // gone between walk and read
		}

		out[rel] = layer.ContentID(body)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// runTraced runs a shell script in a directory, faulting in what it reads.
func runTraced(
	t *testing.T, dir, script string, f *fleet.Filler,
) map[string]ir.NodeID {
	t.Helper()

	type result struct {
		faulted map[string]ir.NodeID
		err     error
	}

	got := make(chan result, 1)

	go func() {
		runtime.LockOSThread() // never unlocked: the thread ends with this goroutine

		tr, err := trace.StartOnSelf()
		if err != nil {
			got <- result{err: err}

			return
		}

		faulted := map[string]ir.NodeID{}

		tr.Fill = func(path string) error {
			ferr := f.Fill(context.Background(), path)
			if ferr != nil {
				return ferr
			}

			body, rerr := os.ReadFile(path)
			if rerr != nil {
				// Genuinely absent, which is an answer (E289).
				return nil
			}

			rel, rerr := filepath.Rel(dir, path)
			if rerr == nil {
				faulted[rel] = layer.ContentID(body)

				// And the directories it needed, which priming or the fault-in
				// created and an overlay would never have shown.
				for d := filepath.Dir(rel); d != "." && d != "/"; d = filepath.Dir(d) {
					faulted[d] = ir.NodeID{}
				}
			}

			return nil
		}

		done := make(chan struct{})

		go func() { tr.Run(); close(done) }()

		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Dir = dir
		runErr := cmd.Run()

		_ = tr.Close()
		<-done

		got <- result{faulted: faulted, err: runErr}
	}()

	select {
	case r := <-got:
		if r.err != nil {
			t.Skipf("the traced step did not run here: %v", r.err)
		}

		return r.faulted

	case <-time.After(60 * time.Second):
		t.Fatal("the traced step never finished")
	}

	return nil
}

// stampLike gives one path the other's modification time.
func stampLike(t *testing.T, to, from string) {
	t.Helper()

	fi, err := os.Stat(from)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Chtimes(to, fi.ModTime(), fi.ModTime())
	if err != nil {
		t.Fatal(err)
	}
}
