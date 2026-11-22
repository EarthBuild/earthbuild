package remote_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// ran records what a runner was asked to do, and answers with a fixed result.
type ran struct {
	asked []ir.NodeID
	res   layer.Result
	err   error
}

func (r *ran) RunAction(_ context.Context, id ir.NodeID) (layer.Result, error) {
	r.asked = append(r.asked, id)

	return r.res, r.err
}

// An action this store has no result for is run, and the answer says so.
//
// **The one thing a cache cannot do.** Until there is a runner the honest reply
// to a miss is that this service cannot execute, because an empty result would
// be taken for an action that produced nothing. With one, a miss is the case
// this whole phase exists for.
func TestExecuteRunsWhatTheCacheDoesNotHold(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	produced := ir.DigestOf([]byte("a tree the action made"))
	r := &ran{res: layer.Result{Root: produced, RootSize: 22, ExitCode: 0, Stdout: []byte("ran")}}

	id := ir.DigestOf([]byte("an action nobody has run"))
	conn := dialRunning(t, &remote.Cache{Store: store.DirStore(t.TempDir())}, r)

	op, err := layer.FinishedIn(executeOnce(t, conn, layer.EncodeExecuteForTest(id, false)))
	if err != nil {
		t.Fatal(err)
	}

	if !op.Done {
		t.Error("the operation is not done, so a client waits for a second one")
	}

	// **Reported as executed, because it was.** `cached_result` is how this API
	// says which, and a service that claimed a cache hit for work it had just
	// done would make every measurement of the cache a lie.
	if op.Cached {
		t.Error("an action that was run was reported as answered from the cache")
	}

	if len(r.asked) != 1 || r.asked[0] != id {
		t.Errorf("the runner was asked for %v, and the client asked about %v", r.asked, id)
	}

	if !bytes.Contains(op.Result, []byte(produced.String())) {
		t.Errorf("the result does not name the tree the action produced: %x", op.Result)
	}
}

// A cache hit is still answered from the cache, runner or no runner.
//
// The runner is the fallback and not the path: an engine that ran every action
// it was asked about would have a cache nothing consults.
func TestExecutePrefersTheCacheToTheRunner(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	r := &ran{}
	c, key, content := cachedAction(t)

	op, err := layer.FinishedIn(executeOnce(t,
		dialRunning(t, c, r), layer.EncodeExecuteForTest(key, false)))
	if err != nil {
		t.Fatal(err)
	}

	if !op.Cached {
		t.Error("a result that was in the cache was reported as executed")
	}

	if len(r.asked) != 0 {
		t.Errorf("the runner ran %v, and the answer was already in the cache", r.asked)
	}

	if !bytes.Contains(op.Result, []byte(content.String())) {
		t.Errorf("the result does not name the cached tree: %x", op.Result)
	}
}

// skip_cache_lookup runs the action even where the cache holds one.
//
// Asked for on purpose, usually to reproduce something, and answering from the
// cache would answer a question the client did not ask.
func TestSkipCacheLookupRunsAnyway(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	r := &ran{res: layer.Result{Root: ir.DigestOf([]byte("fresh"))}}
	c, key, _ := cachedAction(t)

	op, err := layer.FinishedIn(executeOnce(t,
		dialRunning(t, c, r), layer.EncodeExecuteForTest(key, true)))
	if err != nil {
		t.Fatal(err)
	}

	if op.Cached {
		t.Error("skip_cache_lookup was answered from the cache")
	}

	if len(r.asked) != 1 {
		t.Errorf("the runner ran %v, and skip_cache_lookup asked for exactly one run", r.asked)
	}
}

// A runner that cannot run says why, and the client is not told UNIMPLEMENTED.
//
// **The difference matters to a client.** UNIMPLEMENTED means "ask somebody
// else"; a failure to materialise an input root means "this action, as sent,
// cannot run here" - and a client that retried it elsewhere would get the same
// answer having paid twice for it.
func TestARunnerThatFailsSaysWhy(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	r := &ran{err: errors.New("the input root names a blob nobody sent")}

	stream, err := dialRunning(t, &remote.Cache{Store: store.DirStore(t.TempDir())}, r).
		NewStream(context.Background(), &grpc.StreamDesc{ServerStreams: true},
			"/build.bazel.remote.execution.v2.Execution/Execute")
	if err != nil {
		t.Fatal(err)
	}

	req := layer.EncodeExecuteForTest(ir.DigestOf([]byte("an action")), false)
	if sendErr := stream.SendMsg(&req); sendErr != nil {
		t.Fatal(sendErr)
	}

	_ = stream.CloseSend()

	var out []byte

	err = stream.RecvMsg(&out)
	if err == nil {
		t.Fatal("an action that could not run was reported as having run")
	}

	if got := status.Code(err); got == codes.Unimplemented {
		t.Error("a runner's failure was reported as UNIMPLEMENTED, which tells a" +
			" client to ask somebody else about an action that cannot run anywhere")
	}

	if !bytes.Contains([]byte(status.Convert(err).Message()), []byte("nobody sent")) {
		t.Errorf("the failure does not say why: %v", err)
	}
}

// cachedAction is a cache holding one result, and the key and tree it is under.
func cachedAction(t *testing.T) (*remote.Cache, core.Key, ir.NodeID) {
	t.Helper()

	root := t.TempDir()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte("built"), 0o600); err != nil {
		t.Fatal(err)
	}

	took, err := layer.Take(dir)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(store.LayerStore(root).Path(took.ID), 0o750); err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(root, took.ID, m)

	key := core.Key{0x5a}

	return &remote.Cache{
		Store:   store.DirStore(root),
		Actions: oneEntry{key: key, e: core.Entry{Layer: took.ID, Content: took.Content}},
	}, key, took.Content
}

// dialRunning is dialWith with a runner behind the service.
func dialRunning(t *testing.T, c *remote.Cache, r remote.Runner) *grpc.ClientConn {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	g := grpc.NewServer(grpc.ForceServerCodec(remote.Codec()))
	(&remote.Service{Cache: c, Runner: r}).Register(g)

	go func() { _ = g.Serve(ln) }()
	t.Cleanup(g.Stop)

	conn, err := grpc.NewClient(ln.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(remote.Codec())))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}
