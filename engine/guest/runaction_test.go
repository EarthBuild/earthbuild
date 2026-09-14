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
			"cat in.txt > out && echo debris > debris && echo ran",
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

	got, err := srv.RunAction(context.Background(), put(t, st, action), nil)
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

	_, err := srv.RunAction(context.Background(), put(t, st, action), nil)
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
