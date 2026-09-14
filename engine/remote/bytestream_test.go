package remote_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A blob too big for a batch goes both ways over a stream.
//
// **Bigger than the limit on purpose.** The batch this service advertises is
// 4 MiB and a client splits its requests by what it is told, so a blob past
// that has no way through BatchUpdateBlobs at all - it is not slower, it is
// impossible. A compiler's output is routinely past it, which is the difference
// between a service that works on an example and one that works on a build.
//
// Random bytes, so a chunking bug cannot be hidden by a blob that compresses or
// repeats: an off-by-one at a boundary changes the digest.
func TestABlobPastTheBatchLimitStreamsBothWays(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	blob := make([]byte, 5<<20)
	if _, err := rand.Read(blob); err != nil {
		t.Fatal(err)
	}

	id := ir.DigestOf(blob)
	st := store.DirStore(t.TempDir())
	conn := dialService(t, st)

	// Up.
	up, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{ClientStreams: true},
		"/google.bytestream.ByteStream/Write")
	if err != nil {
		t.Fatal(err)
	}

	res := "uploads/a-uuid/blobs/" + id.String() + "/" + itoa(len(blob))

	for off := 0; off < len(blob); off += 1 << 19 {
		end := min(off+(1<<19), len(blob))

		msg := layer.EncodeWriteRequestForTest(res, int64(off), blob[off:end], end == len(blob))
		if err := up.SendMsg(&msg); err != nil {
			t.Fatal(err)
		}

		res = "" // only the first message carries the name
	}

	_ = up.CloseSend()

	var wrote []byte
	if err := up.RecvMsg(&wrote); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// It is in the store, under the name it was sent as, verified on the way in.
	got, err := st.Node(id)
	if err != nil {
		t.Fatalf("the uploaded blob is not in the store: %v", err)
	}

	if !bytes.Equal(got, blob) {
		t.Fatal("the stored blob is not what was sent")
	}

	// And down again.
	down, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{ServerStreams: true},
		"/google.bytestream.ByteStream/Read")
	if err != nil {
		t.Fatal(err)
	}

	ask := layer.EncodeReadRequestForTest("blobs/"+id.String()+"/"+itoa(len(blob)), 0, 0)
	if err := down.SendMsg(&ask); err != nil {
		t.Fatal(err)
	}

	_ = down.CloseSend()

	var back []byte

	for {
		var chunk []byte

		err := down.RecvMsg(&chunk)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("Read: %v", err)
		}

		data, err := layer.ReadResponseIn(chunk)
		if err != nil {
			t.Fatal(err)
		}

		back = append(back, data...)
	}

	if !bytes.Equal(back, blob) {
		t.Errorf("read back %d bytes of %d, and they are not the same blob",
			len(back), len(blob))
	}
}

// A blob whose bytes are not what it was called is refused.
//
// The same rule the batch path follows, and the reason accepting an upload is
// safe rather than trusting.
func TestAStreamedBlobIsCheckedAgainstItsName(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	st := store.DirStore(t.TempDir())
	conn := dialService(t, st)

	up, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{ClientStreams: true},
		"/google.bytestream.ByteStream/Write")
	if err != nil {
		t.Fatal(err)
	}

	lie := ir.DigestOf([]byte("what it claims"))
	sent := []byte("what it is")

	msg := layer.EncodeWriteRequestForTest(
		"uploads/u/blobs/"+lie.String()+"/"+itoa(len(sent)), 0, sent, true)
	if err := up.SendMsg(&msg); err != nil {
		t.Fatal(err)
	}

	_ = up.CloseSend()

	var out []byte

	err = up.RecvMsg(&out)
	if err == nil {
		t.Fatal("a blob was filed under a name its bytes do not produce")
	}

	if got := status.Code(err); got != codes.InvalidArgument {
		t.Errorf("refused with %v, and the client sent something wrong", got)
	}

	if !strings.Contains(status.Convert(err).Message(), lie.String()) {
		t.Errorf("the refusal does not name the blob: %v", err)
	}
}

// A read can start part-way in and stop early.
//
// **Which the round trip above never exercises**, because it asks for the whole
// blob from nothing - so the offset could have been ignored entirely and the
// test would still have passed. A client resuming an interrupted download sends
// an offset, and a client that wants a header sends a limit.
func TestAReadHonoursOffsetAndLimit(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	blob := []byte("0123456789abcdefghij")
	id := ir.DigestOf(blob)

	st := store.DirStore(t.TempDir())
	if err := st.Accept(id, blob); err != nil {
		t.Fatal(err)
	}

	conn := dialService(t, st)

	for name, tc := range map[string]struct {
		offset, limit int64
		want          string
	}{
		"from the start":  {want: string(blob)},
		"part-way in":     {offset: 10, want: "abcdefghij"},
		"a limit":         {limit: 4, want: "0123"},
		"both":            {offset: 4, limit: 3, want: "456"},
		"a limit past it": {offset: 18, limit: 99, want: "ij"},
		"the whole of it": {offset: 0, limit: int64(len(blob)), want: string(blob)},
	} {
		t.Run(name, func(t *testing.T) {
			got := readOver(t, conn, id, len(blob), tc.offset, tc.limit)
			if got != tc.want {
				t.Errorf("read %q, want %q", got, tc.want)
			}
		})
	}

	// **An offset past the end is out of range, not an empty read.** A client
	// that resumed from somewhere impossible has lost track of the blob, and
	// telling it "here is nothing" would have it conclude the blob is empty.
	down, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{ServerStreams: true},
		"/google.bytestream.ByteStream/Read")
	if err != nil {
		t.Fatal(err)
	}

	ask := layer.EncodeReadRequestForTest("blobs/"+id.String()+"/"+itoa(len(blob)), 999, 0)
	if err := down.SendMsg(&ask); err != nil {
		t.Fatal(err)
	}

	_ = down.CloseSend()

	var out []byte
	if err := down.RecvMsg(&out); status.Code(err) != codes.OutOfRange {
		t.Errorf("a read past the end answered %v", status.Code(err))
	}
}

// readOver asks for a blob and reassembles what comes back.
func readOver(t *testing.T, conn *grpc.ClientConn, id ir.NodeID, size int, offset, limit int64) string {
	t.Helper()

	down, err := conn.NewStream(context.Background(),
		&grpc.StreamDesc{ServerStreams: true},
		"/google.bytestream.ByteStream/Read")
	if err != nil {
		t.Fatal(err)
	}

	ask := layer.EncodeReadRequestForTest("blobs/"+id.String()+"/"+itoa(size), offset, limit)
	if err := down.SendMsg(&ask); err != nil {
		t.Fatal(err)
	}

	_ = down.CloseSend()

	var back []byte

	for {
		var chunk []byte

		err := down.RecvMsg(&chunk)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("Read: %v", err)
		}

		data, err := layer.ReadResponseIn(chunk)
		if err != nil {
			t.Fatal(err)
		}

		back = append(back, data...)
	}

	return string(back)
}

func itoa(n int) string { return strconv.Itoa(n) }
