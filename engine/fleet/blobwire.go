package fleet

import (
	"bytes"
	"compress/flate"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// PeerSource fetches blobs from one peer over C.2's `earth/blob/1`.
//
// It **dials**, which is right for exactly one direction: a worker reaching the
// driver, or anything reaching a machine that is listening. The driver reaching
// a worker cannot dial - a worker is behind whatever NAT its operator has - and
// must ask over the connection the worker opened (E249, E250).
//
// One stream for the whole batch, not one per blob: "one stream per blob does
// not survive a thousand-blob synchronisation" (C.4). The request is the ids;
// the answer is each blob in the **order they were asked for**, so the receiver
// knows what it is reading without a lookup, and an absent blob is a flag rather
// than a gap.
type PeerSource struct {
	// Label names this peer in diagnostics.
	Label string
	// Endpoint is this participant's own.
	Endpoint *iroh.Endpoint
	// Peer is who to ask.
	Peer netaddr.EndpointAddr

	// held is the connection to this peer, kept between requests.
	//
	// **A connection costs 25ms on loopback before it moves anything**, and
	// loopback has no network to blame; between machines it is most of what a
	// small fetch costs (E337). One stream per request on one connection is what
	// QUIC is for.
	//
	// Dropped on any failure rather than retried here: a caller that gets an
	// error asks the next source (I6), and the next request to this one dials
	// again. Retrying inside would turn one slow peer into two waits.
	heldMu sync.Mutex
	held   *iroh.Conn
}

// connect is this peer's connection, opened if it is not already.
func (s *PeerSource) connect(ctx context.Context) (*iroh.Conn, error) {
	s.heldMu.Lock()
	defer s.heldMu.Unlock()

	if s.held != nil {
		return s.held, nil
	}

	c, err := s.Endpoint.Connect(ctx, s.Peer, ALPNBlob)
	if err != nil {
		return nil, fmt.Errorf("connect for blobs: %w", err)
	}

	s.held = c

	return c, nil
}

// forget drops a connection that failed, so the next request opens a new one.
func (s *PeerSource) forget(c *iroh.Conn) {
	s.heldMu.Lock()
	defer s.heldMu.Unlock()

	if s.held == c {
		s.held = nil
	}

	_ = c.CloseWithError(0, "")
}

// Name is this source's label.
func (s *PeerSource) Name() string {
	if s.Label == "" {
		return "peer"
	}

	return s.Label
}

// Fetch asks one peer for these blobs.
//
// The readers are over buffers rather than the live stream, and that is a
// deliberate limitation with a stated cost: a peer serving a gigabyte of rubbish
// is detected after the gigabyte has crossed the network, not after a chunk. The
// *verification* is still per-chunk - `Fetch.Get` runs `VerifiedCopy` over what
// arrives - so nothing wrong is ever handed to a caller; what is lost is the
// early hang-up.
//
// Streaming it properly needs the readers to outlive this call and be consumed
// in order, which makes the source's contract "read these in sequence before the
// connection closes" - a contract every future source would have to honour. It
// is worth doing and it is not free, so it is written down rather than assumed.
func (s *PeerSource) Fetch(
	ctx context.Context, ids []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	conn, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}

	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		s.forget(conn)

		return nil, fmt.Errorf("open a blob stream: %w", err)
	}

	defer func() { _ = st.Close() }()

	// The context reaches as far as opening the stream; the reads below take
	// none. A peer that is *alive and silent* - wedged, or serving a blob it
	// cannot find - therefore never times out at all, because QUIC sees a
	// healthy connection and waits for a message that is not coming. Unbounded,
	// not merely slow, and a fetch tries its sources in order, so one such peer
	// stops the fallback that exists to survive it (E256).
	if dl, ok := ctx.Deadline(); ok {
		_ = st.SetDeadline(dl)
	}

	err = writeRequest(st, ids, nil, true)
	if err != nil {
		return nil, err
	}

	out := make(map[ir.NodeID]io.Reader, len(ids))

	for _, id := range ids {
		body, present, err := readBlob(st)
		if err != nil {
			// What arrived is still useful and is returned - the caller asks
			// somebody else for the rest. **The error comes with it**, because
			// a connection that stopped answering and a peer that genuinely
			// lacks a blob are different things, and reporting them alike makes
			// a network that went away look like a peer with nothing (E311).
			return out, fmt.Errorf("%s stopped answering: %w", s.Name(), err)
		}

		if present {
			out[id] = bytes.NewReader(body)
		}
	}

	return out, nil
}

// writeRequest asks for a batch of blobs, or for part of them.
//
// **One request format, not two.** An empty path list means the whole of each
// blob, which is what every caller wanted until fragments existed; a non-empty
// one means those paths of each layer, with the manifest that authenticates them
// (E286). A second request type would be a second thing to keep in step with the
// first, and the difference between them is one list.
func writeRequest(w io.Writer, ids []ir.NodeID, want []string, proof bool) error {
	var buf bytes.Buffer

	e := ir.NewEncoder(&buf)
	e.Count(len(ids))

	for _, id := range ids {
		e.Fixed(id[:])
	}

	e.Count(len(want))

	for _, p := range want {
		e.Str(p)
	}

	e.Bool(proof)

	return WriteMessage(w, buf.Bytes())
}

// readRequest reads one, refusing a count a peer invented.
func readRequest(r io.Reader) ([]ir.NodeID, []string, bool, error) {
	body, err := ReadMessage(r)
	if err != nil {
		return nil, nil, false, err
	}

	d := &decoder{b: body}

	ids := d.ids()
	want := d.strs()
	proof := d.boolean()

	if d.err != nil {
		return nil, nil, false, d.err
	}

	return ids, want, proof, nil
}

// readBlob reads one answer: a flag, the sender's root, then the encoding.
//
// Returns the **plain bytes**, verified against the root as they are decoded.
// That is the `Source` contract - a source hands back what was asked for, having
// checked it survived the journey - and it is what lets a caller treat a peer's
// answer and a local store's answer the same way. Identity is the caller's
// business and is checked where the thing is stored (E264).
func readBlob(r io.Reader) (body []byte, present bool, err error) {
	flag, err := ReadMessage(r)
	if err != nil {
		return nil, false, err
	}

	if len(flag) != 1 || flag[0] == 0 {
		return nil, false, nil
	}

	raw, err := ReadMessage(r)
	if err != nil {
		return nil, false, err
	}

	var root ir.NodeID

	if len(raw) != len(root) {
		return nil, false, fmt.Errorf("%w: a root of %d bytes, want %d",
			ErrMalformed, len(raw), len(root))
	}

	copy(root[:], raw)

	stream, err := ReadBlobMessage(r)
	if err != nil {
		return nil, false, err
	}

	var out bytes.Buffer

	err = VerifiedCopy(&out, bytes.NewReader(stream), root)
	if err != nil {
		return nil, false, err
	}

	return out.Bytes(), true, nil
}

// ServeBlobs answers `earth/blob/1` from what this machine holds.
//
// Verified encodings, which is the sender's obligation: a receiver cannot check
// chunks against a tree nobody sent. A blob this store believes is corrupt is
// answered as **absent** rather than served - the sender's own check, which is
// one of the two on this path and catches an honest peer with a bad disk (E240).
func ServeBlobs(ctx context.Context, e *iroh.Endpoint, held Held, onError func(error)) error {
	if onError == nil {
		onError = func(error) {}
	}

	for {
		conn, err := e.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return fmt.Errorf("accept for blobs: %w", err)
		}

		go serveBlobConn(ctx, conn, held, onError)
	}
}

// serveBlobConn answers every request on one connection.
//
// **A stream per request, not a connection per request.** The first version
// served one stream and hung up, which forced the other end to dial again for
// every fetch - 25ms of handshake on loopback, where there is no network to
// blame, and most of what a small fetch costs between machines (E337).
//
// Streams are served concurrently: a fetch of a large layer must not delay a
// question about a small one, and QUIC's whole shape is that they need not.
func serveBlobConn(ctx context.Context, conn *iroh.Conn, held Held, onError func(error)) {
	defer func() { _ = conn.CloseWithError(0, "") }()

	for {
		st, err := conn.AcceptStream(ctx)
		if err != nil {
			// The peer is done with this connection, or has gone. Neither is
			// worth reporting: a caller that finished asking closes, and that
			// is what finishing looks like from here.
			return
		}

		go serveBlobStream(ctx, st, held, onError)
	}
}

// serveBlobStream answers one request.
func serveBlobStream(_ context.Context, st io.ReadWriteCloser, held Held, onError func(error)) {
	defer func() { _ = st.Close() }()

	ids, want, proof, err := readRequest(st)
	if err != nil {
		onError(fmt.Errorf("read a blob request: %w", err))

		return
	}

	for _, id := range ids {
		err := serveOneBlob(st, held, id, want, proof)
		if err != nil {
			onError(fmt.Errorf("serve %v: %w", id, err))

			return
		}
	}

	// Wait for the client to close **this stream** before tearing it down.
	//
	// Justified on its own terms and **not** as a fix for anything observed:
	// closing a QUIC connection can discard what has not been acknowledged, so
	// a server that returns the moment it has written is racing its own last
	// write. This waits for the client's own close, which means it has read
	// everything.
	//
	// It was added while chasing a "1 of 3 blobs arrived" failure and claimed as
	// the cure. Deleting it again passes five runs in five, so it was not - the
	// failure has not reproduced since and its cause is unknown (E248). The line
	// stays because the reasoning above is sound; the claim that it fixed
	// something did not survive being checked.
	//
	// Per stream since E337: the connection now outlives the request, so this
	// waits for the end of an answer rather than the end of a conversation.
	_, _ = io.Copy(io.Discard, st)
}

// fragmenting is a store that can send part of a layer with its proof.
type fragmenting interface {
	Fragment(id ir.NodeID, want []string) (manifest, packed []byte, err error)
}

func serveOneBlob(w io.Writer, held Held, id ir.NodeID, want []string, proof bool) error {
	// Part of a layer, when that is what was asked for and this store can do it.
	// The manifest travels with it, because a fragment whose proof arrives
	// separately has a state in which it is here and unverifiable - and the only
	// safe thing to do in that state is throw it away (E286).
	// **Not gated on `Has`.** That answers about the *whole* layer, and a worker
	// holding exactly the bytes the asker wants and nothing else would never be
	// asked for them - so fragments came only from whoever held everything, and
	// a fleet was a star on the one path that is supposed to be cheap (E325).
	// A store that cannot answer says so, and the whole-blob path below runs.
	if f, ok := held.(fragmenting); ok && len(want) > 0 && held != nil {
		manifest, packed, err := f.Fragment(id, want)
		if err == nil {
			if !proof {
				// The caller has it. Sending it again is the dominant cost of a
				// small read set (E299).
				manifest = nil
			}

			return writeFragment(w, manifest, packed)
		}
	}

	var b []byte

	if held != nil && held.Has(id) {
		got, err := held.Get(id)
		if err != nil {
			// **Held and unreadable is not absent.** The two send the caller in
			// opposite directions - an absence to another peer, a store that
			// cannot read what it holds to a person - and this returned the
			// same byte for both. It cost five two-machine experiments to tell
			// them apart by hand (E312).
			//
			// An error rather than a flag: there is no room on the wire for a
			// third answer, and `serveBlobConn` reports it and drops the
			// connection, which the caller reads as a source that failed. That
			// is the state it is in.
			return fmt.Errorf("held but unreadable: %w", err)
		}

		b = got
	}

	if b == nil {
		return WriteMessage(w, []byte{0})
	}

	err := WriteMessage(w, []byte{1})
	if err != nil {
		return err
	}

	stream, root := EncodeBlob(b)

	// The root first, because the receiver cannot check chunks against a tree
	// nobody sent.
	//
	// **It is the sender's claim, and it is still worth having.** For a blob the
	// receiver knows the digest already and could ignore this; for a **layer** it
	// does not - a layer is named by the digest of its tree and the bytes
	// carrying it bear no relation to that name (E263). Checking the stream
	// against the root the sender declared catches corruption on the way, within
	// a group rather than after a gigabyte, and identity is established
	// afterwards by unpacking and capturing. Two checks answering two questions:
	// "did this arrive intact" and "is this the thing I asked for".
	err = WriteMessage(w, root[:])
	if err != nil {
		return err
	}

	return WriteBlobMessage(w, stream)
}

var _ Source = (*PeerSource)(nil)

// writeFragment answers with part of a layer and the proof it belongs.
func writeFragment(w io.Writer, manifest, packed []byte) error {
	err := WriteMessage(w, []byte{2})
	if err != nil {
		return err
	}

	small, err := squeeze(manifest)
	if err != nil {
		return err
	}

	err = WriteBlobMessage(w, small)
	if err != nil {
		return err
	}

	return WriteBlobMessage(w, packed)
}

// squeeze compresses a proof for the wire.
//
// **The most regular thing this engine sends.** A manifest is a few thousand
// entries differing in little: paths sharing prefixes, mode and ownership and
// device bytes repeating exactly. It is 2.6x the fragment it authenticates and
// crosses once per worker per layer, so a fleet of ten pays for ten copies of
// the same bytes (E339, E340).
//
// This does not remove the O(n): only making a layer's identity a Merkle root
// over its entries does that, and that is a change to what a layer *is* rather
// than to a message (§3.2, E339). It removes the constant, which is large.
//
// **The proof only.** A fragment's payload is file contents, and compressing an
// archive of already-compressed files is how a transfer gets slower for the
// trouble.
func squeeze(b []byte) ([]byte, error) {
	var out bytes.Buffer

	w, err := flate.NewWriter(&out, flate.BestSpeed)
	if err != nil {
		return nil, fmt.Errorf("compress a proof: %w", err)
	}

	_, err = w.Write(b)
	if err != nil {
		return nil, fmt.Errorf("compress a proof: %w", err)
	}

	err = w.Close()
	if err != nil {
		return nil, fmt.Errorf("compress a proof: %w", err)
	}

	return out.Bytes(), nil
}

// unsqueeze reads a compressed proof, refusing one that does not decompress.
//
// Bounded by `maxBlob`, as every length on this wire is: the compressed size a
// peer sends says nothing about what it expands to, and a few kilobytes that
// become a terabyte is a denial of service that costs the sender nothing.
func unsqueeze(b []byte, limit int64) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(b))
	defer func() { _ = r.Close() }()

	// **One byte past the limit**, so passing it is detectable. `io.LimitReader`
	// alone truncates in silence, which turns a bomb into a proof that is merely
	// wrong - refused later, by a check that would report corruption rather than
	// an attack.
	out, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: a proof that does not decompress: %w",
			ErrMalformed, err)
	}

	if int64(len(out)) > limit {
		return nil, fmt.Errorf("%w: a proof expanding past %d bytes",
			ErrMalformed, limit)
	}

	return out, nil
}

// Fragment asks a peer for part of a layer, and for the manifest that
// authenticates it.
//
// Separate from Fetch because the answers are different shapes: a blob is bytes
// the caller already knows the digest of, and a fragment is bytes plus the proof
// that they belong to a layer whose digest says nothing about any subset (E284).
func (s *PeerSource) Fragment(
	ctx context.Context, id ir.NodeID, want []string, proof bool,
) (manifest, packed []byte, err error) {
	conn, err := s.connect(ctx)
	if err != nil {
		return nil, nil, err
	}

	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		s.forget(conn)

		return nil, nil, fmt.Errorf("open a fragment stream: %w", err)
	}

	defer func() { _ = st.Close() }()

	bound(st, ctx)

	err = writeRequest(st, []ir.NodeID{id}, want, proof)
	if err != nil {
		return nil, nil, err
	}

	return readFragment(st, id)
}

// readFragment reads an answer that should be a fragment.
func readFragment(r io.Reader, id ir.NodeID) (manifest, packed []byte, err error) {
	flag, err := ReadMessage(r)
	if err != nil {
		return nil, nil, err
	}

	if len(flag) != 1 || flag[0] != 2 {
		// Absent, or a whole blob from a peer that cannot fragment. Either way
		// this caller asked for part and did not get it, and saying so is better
		// than quietly returning something else (I10).
		return nil, nil, fmt.Errorf("%w: no fragment of %v here", ErrMalformed, id)
	}

	small, err := ReadBlobMessage(r)
	if err != nil {
		return nil, nil, err
	}

	manifest, err = unsqueeze(small, maxBlob)
	if err != nil {
		return nil, nil, err
	}

	packed, err = ReadBlobMessage(r)
	if err != nil {
		return nil, nil, err
	}

	return manifest, packed, nil
}

// bound applies a context's deadline to a stream.
//
// The context covers opening the stream and **nothing after it**: the reads take
// no context, so a peer whose machine vanished after the stream was opened would
// block until QUIC gave up on the connection - tens of seconds, once per step
// (E256). A deadline on the stream is what actually applies it.
func bound(st *iroh.Stream, ctx context.Context) {
	if dl, ok := ctx.Deadline(); ok {
		_ = st.SetDeadline(dl)
	}
}
