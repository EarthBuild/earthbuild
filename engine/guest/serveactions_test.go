package guest_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A step that asked for one can reach an execution service, and only it can.
//
// **The socket is the identity.** It is bound inside the step's own filesystem,
// so the service a client reaches is the one holding that step's base - no
// token to pass, nothing for a client to get wrong, and no way for a step that
// did not ask to find it. That is the whole of the authorisation model, and it
// is the sandbox boundary rather than anything invented here.
//
// The path is said by the caller and not derived at both ends, which is
// `Daemon.Socket`'s lesson: two implementations of one rule disagree eventually,
// and present as a client that cannot reach a service running perfectly well.
func TestAStepThatAskedCanReachTheActionService(t *testing.T) {
	t.Parallel()

	// **Short on purpose.** A step's root is part of a unix socket path, and
	// `sun_path` is 104 bytes - which the macOS temp directory alone very
	// nearly is. In a sandbox the root is a few tens of characters and this
	// never bites; here it would, and a test skipped over its fixture's path
	// length teaches nothing.
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

	// **The step waits for a marker rather than for a while.** What is being
	// tested is that the service is up *during* the step, and a step that slept
	// would test that only as often as the machine was fast enough.
	//
	// The step's root is the host's here because `Unconfined` skips the chroot
	// a real sandbox has; under confinement these are the step's own paths.
	at := filepath.Join(root, sock)
	marker := filepath.Join(root, "go")

	done := make(chan guest.StepOutcome, 1)

	go func() {
		got, runErr := c.RunStep(context.Background(), h, guest.Step{
			Argv: []string{
				"/bin/sh", "-c",
				"while [ ! -e " + marker + " ]; do sleep 0.02; done; echo ended",
			},
			Actions: &guest.Actions{Socket: sock},
		}, nil)
		if runErr != nil {
			t.Error(runErr)
		}

		done <- got
	}()

	waitFor(t, at)

	// The service behind the socket answers this step, over this step's base.
	//
	// **Asked about an action it holds no result for, not asked to run one.**
	// Running it ends in a capture, and a capture sees the socket that this
	// service bound - which off Linux is really in the step's delta, because
	// `actionsRoomMount` mounts nothing there. Inside a sandbox it is on an
	// ephemeral tmpfs and never reaches a layer, which is what the Linux-only
	// sibling of this test checks.
	if code := runOverSocket(t, at, id); code == codes.Unimplemented {
		t.Error("the step's socket answers UNIMPLEMENTED, so it was given no" +
			" runner and the step's base reaches nothing")
	}

	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	<-done

	// **Gone with the step that asked for it.** A service outliving its step
	// would be answering for a filesystem that has been released.
	if _, err := os.Stat(at); err == nil {
		t.Error("the action socket is still bound after the step ended")
	}
}

// waitFor blocks until a path exists, or the test has waited long enough to
// call it a failure rather than a slow machine.
func waitFor(t *testing.T, at string) {
	t.Helper()

	for range 500 {
		if _, err := os.Stat(at); err == nil {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("%s never appeared, so the step never got an action service", at)
}

// A step that did not ask gets no socket.
//
// **Not a security boundary and still worth keeping.** The sandbox is the
// boundary; this is legibility. A step that can reach the engine is a fact
// about a build that its author should have written down, and a service that
// appeared everywhere would make `WITH RE` decorative.
func TestAStepThatDidNotAskGetsNoSocket(t *testing.T) {
	t.Parallel()

	root := stepRoot(t)

	c := pairWith(t, &guest.Server{
		Mat:        &fixedRootMat{root: root},
		LayerDir:   t.TempDir(),
		Unconfined: true,
	})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.RunStep(context.Background(), h, guest.Step{
		Argv: []string{"/bin/sh", "-c", "test -e /run/earthbuild/actions.sock && echo found || echo absent"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got.Output, "absent") {
		t.Errorf("a step that did not ask found an action service: %q", got.Output)
	}
}

// runOverSocket asks the service at a unix socket to execute an action, and
// reports the status it answered with.
func runOverSocket(t *testing.T, at string, action ir.NodeID) codes.Code {
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

	return status.Code(stream.RecvMsg(&out))
}

// linkedMat is fixedRootMat where the step sees the tree by one name and its
// writes are committed from another.
//
// **Which is what a real handle is.** Root is where the filesystem appears and
// Delta is where the writes land, and in an overlay those are always two
// different paths. Here they are the same directory reached two ways, so the
// socket path can be short while the commit walks a tree that is not itself a
// symlink.
type linkedMat struct{ root, delta string }

func (m linkedMat) Materialise(context.Context, []ir.NodeID) (core.Handle, error) {
	return linkedHandle{fixedHandle{m.root}, m.delta}, nil
}

type linkedHandle struct {
	fixedHandle

	delta string
}

func (h linkedHandle) Delta() string { return h.delta }

// realOf is a path with its symlinks resolved.
func realOf(t *testing.T, at string) string {
	t.Helper()

	full, err := filepath.EvalSymlinks(at)
	if err != nil {
		t.Fatal(err)
	}

	return full
}

// shortStepRoot is stepRoot reached by a path short enough to hold a socket.
//
// **A symlink, because the limit is on the string and not on the location.**
// `sun_path` bounds the path handed to bind(2), which the kernel then resolves -
// so the files stay in the test's own temp directory while the name used to
// reach them is short. Putting them under /tmp instead would be shorter and
// wrong on macOS, where /tmp is group `wheel`: files inherit that gid, and the
// keep-own lchown that commits a layer cannot restore it as an ordinary user.
//
// None of this is a product concern - a step's root inside a sandbox is a few
// tens of characters - but a test skipped over its fixture's path length would
// be testing nothing.
func shortStepRoot(t *testing.T) string {
	t.Helper()

	full := stepRoot(t)

	// t.TempDir() is the usual answer and is the thing being avoided: on this
	// machine it is ninety characters before the fixture adds any.
	short, err := os.MkdirTemp("/tmp", "eb") //nolint:usetesting // short on purpose
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(short) })

	at := filepath.Join(short, "r")
	if err := os.Symlink(full, at); err != nil {
		t.Fatal(err)
	}

	return at
}
