package guest_test

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// An action runs, and what it declared is what comes back.
//
// **The same three things a step is.** An Action is a base, a small tree over
// it, and a command - so this materialises the stack, writes the input root
// into it, runs the argv and captures the delta, which are the requests this
// guest already answers. Nothing about an action needs a second way to run
// something, and a second way is how the two drift.
//
// `output_paths` is R4's narrowing under another name: the step writes `out`
// and `debris`, declares only `out`, and the layer holds one file.
func TestAnActionRunsAndReturnsWhatItDeclared(t *testing.T) {
	// **A step mounts /proc, which an unprivileged process cannot.** Re-executed
	// into a user namespace where it can, which is what every other test that
	// runs a step here does - and without it this is green on macOS, where the
	// tests run as root inside a VM, and red on Linux for a reason that is
	// about the machine rather than the code.
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Parallel()

	root := stepRoot(t)

	layerDir := t.TempDir()
	st := store.DirStore(layerDir)

	srv := &guest.Server{
		Mat:        &fixedRootMat{root: root},
		LayerDir:   layerDir,
		Unconfined: true,
	}

	// What a client uploads before it asks for anything to be run: the command,
	// the tree it runs over, and the action naming both.
	src := put(t, st, []byte("the input\n"))
	inputRoot := put(t, st, dirOf(member{name: "in.txt", digest: src}))

	cmd := layer.EncodeCommand(layer.Command{
		Arguments: []string{
			"/bin/sh", "-c",
			// Shell builtins only: `cat` is not on the default PATH
			// everywhere this runs, and a test that needed one would be
			// testing the fixture's machine.
			"read l < in.txt; echo \"$l\" > out; echo debris > debris; echo ran",
		},
		WorkingDirectory: "/",
		OutputPaths:      []string{"out"},
	})

	action := layer.EncodeAction(layer.Action{
		Command:     put(t, st, cmd),
		CommandSize: int64(len(cmd)),
		InputRoot:   inputRoot,
		InputSize:   1,
	})

	got, err := srv.RunAction(context.Background(), put(t, st, action), nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if got.ExitCode != 0 {
		t.Errorf("the action exited %d, having printed %q", got.ExitCode, got.Stdout)
	}

	if !strings.Contains(string(got.Stdout), "ran") {
		t.Errorf("stdout is %q, and the action printed \"ran\"", got.Stdout)
	}

	// The layer holds what was declared, and not the debris beside it.
	held := namesIn(t, st, got.Root)
	if len(held) != 1 || held[0] != "out" {
		t.Errorf("the result holds %v, and the action declared out", held)
	}
}

// An action naming a command this store does not hold is refused.
//
// **Refused, not run as an action with no argv.** REAPI's contract is that a
// client sends its blobs and then asks; one that asked too early is entitled to
// be told which blob is missing rather than to have an empty command succeed
// and be cached as this action's answer.
func TestAnActionWithNoCommandBlobIsRefused(t *testing.T) {
	t.Parallel()

	root := stepRoot(t)

	layerDir := t.TempDir()
	st := store.DirStore(layerDir)

	srv := &guest.Server{
		Mat:        &fixedRootMat{root: root},
		LayerDir:   layerDir,
		Unconfined: true,
	}

	absent := ir.DigestOf([]byte("a command nobody sent"))
	action := layer.EncodeAction(layer.Action{
		Command:   absent,
		InputRoot: put(t, st, dirOf()),
	})

	_, err := srv.RunAction(context.Background(), put(t, st, action), nil, "")
	if err == nil {
		t.Fatal("an action whose command is not in the store was run")
	}

	if !strings.Contains(err.Error(), absent.String()) {
		t.Errorf("the refusal does not name the missing blob: %v", err)
	}
}

// put files a blob under its own name and hands the name back.
func put(t *testing.T, st store.DirStore, b []byte) ir.NodeID {
	t.Helper()

	id := ir.DigestOf(b)
	if err := st.Accept(id, b); err != nil {
		t.Fatal(err)
	}

	return id
}

// namesIn is the top-level entries of a stored Directory.
func namesIn(t *testing.T, st store.DirStore, root ir.NodeID) []string {
	t.Helper()

	b, err := st.Node(root)
	if err != nil {
		t.Fatal(err)
	}

	d, err := layer.DirectoryIn(b)
	if err != nil {
		t.Fatal(err)
	}

	out := make([]string, 0, len(d.Files)+len(d.Dirs))
	for _, m := range d.Files {
		out = append(out, m.Name)
	}

	for _, m := range d.Dirs {
		out = append(out, m.Name)
	}

	return out
}

type member struct {
	name   string
	digest ir.NodeID
}

// dirOf encodes a Directory of files by hand, which is what a client sends.
func dirOf(files ...member) []byte {
	var out []byte

	for _, f := range files {
		node := pbField(nil, 1, []byte(f.name))
		node = pbField(node, 2, pbField(nil, 1, []byte(hex.EncodeToString(f.digest[:]))))
		out = pbField(out, 1, node)
	}

	return out
}

func pbField(b []byte, num int, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(num)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(v)))

	return append(b, v...)
}

// An action may name the image the step it is asking from stands on.
//
// **Which is just the FROM line.** The engine already resolved that reference
// to a stack in order to run the step at all - that is what FROM does, memoised
// on (reference, platform), pinned before it reaches the key (I17). So there is
// nothing to look up here and no table to keep: the host says which image the
// stack it handed over came from, exactly as it says where the socket is, and
// this compares.
//
// An action naming a *different* image is refused. Its image is part of its
// Platform, which is part of its Action, which is its key - so running it over
// the base to hand and filing the result under the image it named produces a
// cache entry describing an environment the work never ran in, which every
// later build legitimately using that image would be served (I3).
func TestAnActionMayNameTheImageItsStepStandsOn(t *testing.T) {
	t.Parallel()

	// Two of these cases actually run the action, which mounts /proc, and a
	// machine that will not let this process do that is not a machine where
	// this test failed. Asked of the engine's own probe rather than by matching
	// the kernel's wording: "operation not permitted" is what this kernel says
	// today, and a harness that recognises one phrasing turns every other into a
	// false failure - the argument `nstest.Unstartable` already makes.
	if err := guest.CanIsolate(); err != nil {
		t.Skipf("this machine will not isolate a step, so nothing ran: %v", err)
	}

	const (
		ours   = "alpine@sha256:" + zeros + "01"
		theirs = "ubuntu@sha256:" + zeros + "ff"
	)

	for name, tc := range map[string]struct {
		asks    string
		refused bool
	}{
		"the same image":             {asks: ours},
		"the same image, prefixed":   {asks: "docker://" + ours},
		"a different image":          {asks: theirs, refused: true},
		"the same name, unpinned":    {asks: "alpine", refused: true},
		"an image, but we know none": {asks: ours, refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			layerDir := t.TempDir()
			st := store.DirStore(layerDir)

			srv := &guest.Server{
				Mat:        &fixedRootMat{root: t.TempDir()},
				LayerDir:   layerDir,
				Unconfined: true,
			}

			cmd := layer.EncodeCommand(layer.Command{Arguments: []string{"/bin/sh", "-c", "true"}})

			action := layer.EncodeAction(layer.Action{
				Command:     put(t, st, cmd),
				CommandSize: int64(len(cmd)),
				InputRoot:   put(t, st, dirOf()),
				Platform:    []layer.Property{{Name: "container-image", Value: tc.asks}},
			})

			// The last case is the one where the engine could not say what the
			// step stands on: an unknown environment can confirm nothing.
			know := ours
			if name == "an image, but we know none" {
				know = ""
			}

			_, err := srv.RunAction(context.Background(), put(t, st, action), nil, know)

			switch {
			case tc.refused && err == nil:
				t.Errorf("an action asking for %q ran on %q", tc.asks, know)
			case !tc.refused && err != nil:
				t.Errorf("an action asking for the image it is running on was refused: %v", err)
			case tc.refused && !strings.Contains(err.Error(), tc.asks):
				t.Errorf("the refusal does not name the image asked for: %v", err)
			}
		})
	}
}

// An action naming no image runs over the base it was given.
//
// A refusal that refuses everything is not a check, and the test above cannot
// tell the difference on its own.
func TestAnActionNamingNoImageStillRuns(t *testing.T) {
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Parallel()

	root := stepRoot(t)
	layerDir := t.TempDir()
	st := store.DirStore(layerDir)

	srv := &guest.Server{
		Mat:        &fixedRootMat{root: root},
		LayerDir:   layerDir,
		Unconfined: true,
	}

	cmd := layer.EncodeCommand(layer.Command{
		Arguments: []string{"/bin/sh", "-c", "echo ran > out"},
		// A platform property that is not an image says nothing about the
		// environment, and must not be mistaken for one that does.
		OutputPaths: []string{"out"},
	})

	action := layer.EncodeAction(layer.Action{
		Command:     put(t, st, cmd),
		CommandSize: int64(len(cmd)),
		InputRoot:   put(t, st, dirOf()),
		Platform:    []layer.Property{{Name: "OSFamily", Value: "linux"}},
	})

	if _, err := srv.RunAction(context.Background(), put(t, st, action), nil, ""); err != nil {
		t.Fatalf("an action naming no image was refused: %v", err)
	}
}

// zeros is the dull part of a digest, so a table of them fits on a line.
const zeros = "000000000000000000000000000000000000000000000000000000000000"

// The directories an output needs are there before the action runs.
//
// **The worker's job, and REAPI says so:** "Directories leading up to the
// output directories (but not the output directories themselves) are created by
// the worker prior to execution, even if they are not explicitly part of the
// input root."
//
// Bazel relies on it. A genrule writes to `bazel-out/k8-fastbuild/bin/...`,
// which is in no input root and which bazel never creates, so an action that
// merely materialises what it was sent fails with `No such file or directory`
// from the shell - a message about the output that says nothing about whose job
// the directory was.
func TestAnOutputsParentDirectoriesExistBeforeItRuns(t *testing.T) {
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Parallel()

	root := stepRoot(t)
	layerDir := t.TempDir()
	st := store.DirStore(layerDir)

	srv := &guest.Server{
		Mat:        &fixedRootMat{root: root},
		LayerDir:   layerDir,
		Unconfined: true,
	}

	// Writes where nothing has been created, exactly as a genrule does.
	cmd := layer.EncodeCommand(layer.Command{
		Arguments: []string{"/bin/sh", "-c", "echo made it > out/deep/nested/greeting.txt"},
		// Declared but never created by the client, and not in the input root.
		OutputPaths: []string{"out/deep/nested/greeting.txt"},
	})

	action := layer.EncodeAction(layer.Action{
		Command:     put(t, st, cmd),
		CommandSize: int64(len(cmd)),
		InputRoot:   put(t, st, dirOf()),
	})

	got, err := srv.RunAction(context.Background(), put(t, st, action), nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if got.ExitCode != 0 {
		t.Fatalf("the action exited %d: %s", got.ExitCode, got.Stdout)
	}

	// And the file it wrote is named back, which is the point of having made
	// somewhere to put it.
	if len(got.Declared.Files) != 1 ||
		got.Declared.Files[0].Path != "out/deep/nested/greeting.txt" {
		t.Errorf("the action produced %v", got.Declared.Files)
	}
}
