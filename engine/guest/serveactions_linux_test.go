package guest_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// An action asked for over a step's socket runs, and the socket is not in it.
//
// **Linux-only because the mount is.** A WITH RE step gets an ephemeral tmpfs
// where its socket lives, so the socket never reaches the step's delta and
// never reaches a layer - a socket has no contents to digest, and committing a
// delta holding one fails outright. Off Linux nothing is mounted, the socket
// really is in the delta, and this cannot be true; inside a sandbox, which is
// the only place a delta is ever committed, it is.
//
// The cross-platform sibling checks what can hold anywhere: that the socket is
// bound while the step runs, is answered by a service holding that step's base,
// and is gone afterwards.
func TestAnActionOverAStepsSocketProducesALayer(t *testing.T) {
	t.Parallel()

	root := shortStepRoot(t)
	layerDir := t.TempDir()
	st := store.DirStore(layerDir)

	c := pairWith(t, &guest.Server{
		Mat:        linkedMat{root: root, delta: realOf(t, root)},
		LayerDir:   layerDir,
		Unconfined: true,
	})

	src := put(t, st, []byte("the input\n"))
	inputRoot := put(t, st, dirOf(member{name: "in.txt", digest: src}))

	cmd := layer.EncodeCommand(layer.Command{
		Arguments:        []string{"/bin/sh", "-c", "cat in.txt > out"},
		WorkingDirectory: "/",
		OutputPaths:      []string{"out"},
	})

	action := layer.EncodeAction(layer.Action{
		Command:     put(t, st, cmd),
		CommandSize: int64(len(cmd)),
		InputRoot:   inputRoot,
		InputSize:   1,
	})

	id := put(t, st, action)

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	const sock = "/run/earthbuild/actions.sock"

	at := filepath.Join(root, sock)
	marker := filepath.Join(root, "go")

	done := make(chan struct{})

	go func() {
		defer close(done)

		_, runErr := c.RunStep(context.Background(), h, guest.Step{
			Argv: []string{
				"/bin/sh", "-c",
				"while [ ! -e " + marker + " ]; do sleep 0.02; done",
			},
			Actions: &guest.Actions{Socket: sock},
		}, nil)
		if runErr != nil {
			t.Error(runErr)
		}
	}()

	waitFor(t, at)

	res := resultOverSocket(t, at, id)

	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	<-done

	if res.ExitCode != 0 {
		t.Errorf("the action exited %d", res.ExitCode)
	}

	if res.Root == (ir.NodeID{}) {
		t.Fatal("the action produced no tree")
	}

	// The layer holds what the action declared, and neither the input it read
	// nor the socket it was asked over.
	held := namesIn(t, st, res.Root)
	if len(held) != 1 || held[0] != "out" {
		t.Errorf("the action produced %v, and it declared out", held)
	}
}

// resultOverSocket is runOverSocket where the action is expected to run.
func resultOverSocket(t *testing.T, at string, action ir.NodeID) layer.Result {
	t.Helper()

	conn, err := grpc.NewClient("unix://"+at,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			var d net.Dialer

			return d.DialContext(ctx, "unix", strings.TrimPrefix(s, "unix://"))
		}),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(remote.Codec())))
	if err != nil {
		t.Fatal(err)
	}

	defer conn.Close()

	stream, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{ServerStreams: true},
		"/build.bazel.remote.execution.v2.Execution/Execute")
	if err != nil {
		t.Fatal(err)
	}

	req := layer.EncodeExecuteForTest(action, false)
	if sendErr := stream.SendMsg(&req); sendErr != nil {
		t.Fatal(sendErr)
	}

	_ = stream.CloseSend()

	var out []byte
	if err := stream.RecvMsg(&out); err != nil {
		t.Fatalf("Execute over the step's socket: %v", err)
	}

	op, err := layer.FinishedIn(out)
	if err != nil {
		t.Fatal(err)
	}

	res, err := layer.ResultIn(op.Result)
	if err != nil {
		t.Fatal(err)
	}

	return res
}
