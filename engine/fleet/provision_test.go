package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// mapStore is a blob store in memory, which also records what it was asked to
// keep - the point of provisioning being that it fetches once, not every step.
// Guarded, because the real thing is: `Layers` is a directory, and `os.Stat`
// beside `os.Rename` needs no lock. A fake without one reports a data race for a
// property the production type has (E266).
type mapStore struct {
	mu    sync.Mutex
	blobs map[ir.NodeID][]byte
	puts  int
}

func newMapStore() *mapStore { return &mapStore{blobs: map[ir.NodeID][]byte{}} }

func (m *mapStore) Has(id ir.NodeID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.blobs[id]

	return ok
}

func (m *mapStore) Get(id ir.NodeID) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.blobs[id]
	if !ok {
		return nil, errors.New("no such blob")
	}

	return b, nil
}

// Put files a body under its own digest, which is the shape blob.Store has: the
// caller does not get to choose the name, so a store cannot file a blob under
// the id it was hoping for rather than the one it received.
func (m *mapStore) Put(r io.Reader) (ir.NodeID, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return ir.NodeID{}, 0, err
	}

	id := fleet.BlobID(b)

	m.mu.Lock()
	m.blobs[id] = b
	m.puts++
	m.mu.Unlock()

	return id, int64(len(b)), nil
}

// countingSource serves from a store and counts the batches it was asked for.
type countingSource struct {
	*fleet.LayerSource

	mu      sync.Mutex
	batches int
	asked   []ir.NodeID
}

func (c *countingSource) Fetch(
	ctx context.Context, ids []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	c.mu.Lock()
	c.batches++
	c.asked = append(c.asked, ids...)
	c.mu.Unlock()

	return c.LayerSource.Fetch(ctx, ids)
}

// A worker fetches the inputs it does not have, and only those.
//
// This is the mechanism the fleet has been missing: `Runner` handed an
// assignment's digests straight to the executor, which materialises from **this
// machine's** store. A worker that had never seen the base could not run the
// step, and the end-to-end test only passed because its blob source claimed to
// hold everything (E258).
//
// "Only those" is not an optimisation here, it is the difference between a fleet
// that helps and one that does not: a base layer is measured in hundreds of
// megabytes, and refetching it per step spends more time on the network than the
// step spends building.
func TestAWorkerFetchesTheInputsItLacksAndNoOthers(t *testing.T) {
	t.Parallel()

	base := []byte("a base layer's bytes")
	source := []byte("an artifact from another target")

	remote := newMapStore()
	baseID := putBlob(t, remote, base)
	srcID := putBlob(t, remote, source)

	// This worker already has the base - it ran a step on it a moment ago - and
	// has never seen the artifact.
	mine := newMapStore()
	put(t, mine, base)

	from := &countingSource{LayerSource: &fleet.LayerSource{Label: "driver", Held: remote}}

	a := fleet.Assignment{
		Version: fleet.Version,
		Base:    []ir.NodeID{baseID},
		Sources: [][]ir.NodeID{{srcID}},
	}

	_, err := fleet.Provision(t.Context(), mine, a, from)
	if err != nil {
		t.Fatalf("provisioning: %v", err)
	}

	if !mine.Has(srcID) {
		t.Error("the artifact the step needs was not fetched")
	}

	if from.batches != 1 {
		t.Errorf("asked the source %d times for two blobs"+
			"\n  C.4 batches: one stream per blob does not survive a"+
			" thousand-blob synchronisation", from.batches)
	}

	for _, id := range from.asked {
		if id == baseID {
			t.Error("refetched the base this machine already held" +
				"\n  a base layer is hundreds of megabytes; per step, that is" +
				" more network than the build is compute")
		}
	}
}

// Nothing missing is no fetch at all.
//
// The common case once a fleet is warm, and the one that decides whether a
// second machine is worth having: a worker that has everything should not open a
// connection to be told so.
func TestAWorkerWithEverythingAsksForNothing(t *testing.T) {
	t.Parallel()

	body := []byte("already here")

	mine := newMapStore()
	id := putBlob(t, mine, body)

	from := &countingSource{LayerSource: &fleet.LayerSource{Held: newMapStore()}}

	_, err := fleet.Provision(t.Context(), mine,
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{id}}, from)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if from.batches != 0 {
		t.Errorf("opened %d fetch(es) for a machine that had everything", from.batches)
	}
}

// A blob nobody can supply is a refusal, not a step that runs without it.
//
// Running a step whose base is missing does not produce a wrong answer by luck -
// it produces a *different* answer, keyed as though it had the base. That is the
// false hit I3 exists to prevent, and it would be cached and served to everybody
// else.
func TestAnInputNobodyHasIsRefusedRatherThanSkipped(t *testing.T) {
	t.Parallel()

	mine := newMapStore()
	from := &countingSource{LayerSource: &fleet.LayerSource{Held: newMapStore()}}

	_, err := fleet.Provision(t.Context(), mine,
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{{9}}}, from)
	if err == nil {
		t.Fatal("a step whose base nobody has was allowed to proceed" +
			"\n  it would be keyed as though it had one (I3)")
	}
}

// putBlob files a body under its own digest, as a store does.
func putBlob(t *testing.T, m *mapStore, body []byte) ir.NodeID {
	t.Helper()

	return put(t, m, body)
}

func put(t *testing.T, m *mapStore, body []byte) ir.NodeID {
	t.Helper()

	id, _, err := m.Put(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	return id
}

// The worker path provisions, not just the function.
//
// `Provision` being correct and never called is the state this engine was
// actually in (E258), so the property worth asserting is that an assignment
// arriving at a worker causes the fetch - the wiring, not the mechanism.
func TestAnAssignmentArrivingAtAWorkerFetchesItsInputs(t *testing.T) {
	t.Parallel()

	body := []byte("the base this worker has never seen")

	driver := newMapStore()
	id := putBlob(t, driver, body)

	mine := newMapStore()

	run := fleet.Runner(&countingLocal{}, core.Worker{ID: "w1"},
		fleet.WithBlobs(mine, &fleet.LayerSource{Label: "driver", Held: driver}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused != "" {
		t.Fatalf("the worker refused: %s", reply.Refused)
	}

	if !mine.Has(id) {
		t.Error("the step ran without its base ever reaching this machine" +
			"\n  it would be keyed as though it had one (I3)")
	}
}

// A worker that cannot get an input refuses rather than builds.
//
// The driver then has the step back and may run it somewhere that can (I11).
// Building without the base would produce a *different* answer keyed as the
// right one, and cache it for everybody.
func TestAWorkerThatCannotGetAnInputRefuses(t *testing.T) {
	t.Parallel()

	local := &countingLocal{}

	run := fleet.Runner(local, core.Worker{ID: "w1"},
		fleet.WithBlobs(newMapStore(), &fleet.LayerSource{Held: newMapStore()}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{{9}},
	})
	if err != nil {
		t.Fatalf("a worker that cannot fetch must refuse, not fail: %v", err)
	}

	if reply.Refused == "" {
		t.Error("the worker accepted a step whose base it could not get")
	}

	if local.runs != 0 {
		t.Error("the step ran anyway, without its base")
	}
}

// renamingStore files every blob under a name of its own choosing.
type renamingStore struct{ *mapStore }

func (r renamingStore) Put(body io.Reader) (ir.NodeID, int64, error) {
	_, n, err := r.mapStore.Put(body)

	return ir.NodeID{0xff}, n, err
}

// A store that files a blob under a different name is caught, not trusted.
//
// The wire and the store have to agree about what a blob is called or the whole
// scheme comes apart: the fetch verified these bytes against `id`, and a step is
// about to be keyed on `id`, so a store that filed them as something else leaves
// the step reading a blob nobody checked. Belt and braces - the fetch's own
// verification should make it impossible - which is exactly the kind of check
// that rots unexercised, and it survived a mutation before this test existed.
func TestAStoreThatRenamesABlobIsCaught(t *testing.T) {
	t.Parallel()

	body := []byte("bytes with a name")

	driver := newMapStore()
	id := putBlob(t, driver, body)

	_, err := fleet.Provision(t.Context(), renamingStore{newMapStore()},
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{id}},
		&fleet.LayerSource{Held: driver})
	if err == nil {
		t.Fatal("a store that filed a blob under another name was believed")
	}

	if !strings.Contains(err.Error(), id.String()) {
		t.Errorf("%v\n  the message must name the blob that went astray", err)
	}
}

// Provisioning says what it moved, and a worker passes that back.
//
// The bytes are the number that decides whether a fleet is worth having: a step
// that computes for two seconds and moves four hundred megabytes to get there is
// not a step worth delegating, and nothing else in the system can tell that from
// a step that computed for two seconds (E259).
func TestProvisioningReportsWhatItMoved(t *testing.T) {
	t.Parallel()

	body := make([]byte, 64<<10)
	for i := range body {
		body[i] = byte(i)
	}

	driver := newMapStore()
	id := putBlob(t, driver, body)

	moved, err := fleet.Provision(t.Context(), newMapStore(),
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{id}},
		&fleet.LayerSource{Held: driver})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if moved.Bytes != int64(len(body)) {
		t.Errorf("reported %d bytes moved, want %d", moved.Bytes, len(body))
	}

	// Nothing moved is nothing reported, which is what makes a warm worker
	// distinguishable from a cold one in the account.
	warm := newMapStore()
	put(t, warm, body)

	none, err := fleet.Provision(t.Context(), warm,
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{id}},
		&fleet.LayerSource{Held: driver})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if none.Bytes != 0 {
		t.Errorf("a worker that already held everything reported %d bytes moved",
			none.Bytes)
	}
}

// A worker's reply carries what it had to move.
//
// The wiring, again: an accounting nothing fills in reads as a fleet with no
// transfer cost, which is the most flattering possible lie about a distributed
// build.
func TestAWorkersReplySaysWhatItHadToMove(t *testing.T) {
	t.Parallel()

	body := make([]byte, 32<<10)

	driver := newMapStore()
	id := putBlob(t, driver, body)

	run := fleet.Runner(&countingLocal{}, core.Worker{ID: "w1"},
		fleet.WithBlobs(newMapStore(), &fleet.LayerSource{Held: driver}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.FetchedBytes != int64(len(body)) {
		t.Errorf("the reply says %d bytes were fetched, want %d"+
			"\n  an account nothing fills in reads as a fleet with no transfer"+
			" cost", reply.FetchedBytes, len(body))
	}
}

// A fetch that failed everywhere says what the last source said.
//
// **The same discard as E308, one level down.** `Provision` tries each source and
// moves on when one cannot answer - which is right (I6) - and then reports "some
// blobs could not be fetched", which is a count without a cause. Every source
// had a reason and all of them were thrown away.
//
// The last one is kept. Not all of them: a fleet of twenty workers would produce
// twenty lines of the same timeout, and the useful case is one or two sources
// with one real reason between them (E309).
func TestAFetchThatFailedEverywhereSaysWhy(t *testing.T) {
	t.Parallel()

	boom := errors.New("no route to that machine")

	_, err := fleet.Provision(t.Context(), newMapStore(),
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{{9}}},
		&failingSource{err: boom})
	if err == nil {
		t.Fatal("a fetch with no working source succeeded")
	}

	if !strings.Contains(err.Error(), "no route") {
		t.Errorf("%v\n  a count without a cause is what an afternoon of"+
			" two-machine runs produced", err)
	}
}

// failingSource cannot answer, and says why.
type failingSource struct{ err error }

func (f *failingSource) Name() string { return "unreachable" }

func (f *failingSource) Fetch(
	context.Context, []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	return nil, f.err
}

// A source that answered "I have none" is not a source that was never asked.
//
// **E312, and the third time this project has met the same class.** Five
// two-machine experiments narrowed a fleet that fetched nothing to one line -
// "no source had it" - which turns out to be what `Provision` says whether a
// peer was consulted and had none, or was never reached at all. The two states
// need different fixes and the message cannot tell them apart, so each round of
// instrumenting told us only where to instrument next.
//
// *Failure class: a mechanism that is not running and one that found nothing
// produce the same output.* Named in E246, met again here.
//
// So a source that answered is named as having answered. The reason a failing
// source gives is still the more interesting one and still leads (E309); this is
// what fills the silence when nothing failed.
func TestASourceThatAnsweredEmptyIsNamed(t *testing.T) {
	t.Parallel()

	_, err := fleet.Provision(t.Context(), newMapStore(),
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{{9}}},
		&emptySource{name: "driver"}, &emptySource{name: "peer-2"})
	if err == nil {
		t.Fatal("a fetch no source could answer succeeded")
	}

	for _, want := range []string{"driver", "peer-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v\n  does not name %q, which answered and had nothing"+
				"\n  a peer that was asked and a peer that was never reached"+
				" need different fixes (E312)", err, want)
		}
	}
}

// A source that answers, and has nothing.
type emptySource struct{ name string }

func (e *emptySource) Name() string { return e.name }

func (e *emptySource) Fetch(
	context.Context, []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	return nil, nil
}

// A fetch with no sources at all says that, in those words.
//
// The other half of E312, and the state the two-machine probe was actually in:
// nothing failed, nothing was consulted, and the message said "no source had
// it" - which reads as a fleet that looked and came back empty-handed. It had
// not looked.
func TestAFetchWithNoSourcesSaysNobodyWasAsked(t *testing.T) {
	t.Parallel()

	_, err := fleet.Provision(t.Context(), newMapStore(),
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{{9}}})
	if err == nil {
		t.Fatal("a fetch with no sources succeeded")
	}

	if !strings.Contains(err.Error(), "no source was consulted") {
		t.Errorf("%v\n  a fleet that looked and a fleet that did not look are"+
			" the same sentence (E312)", err)
	}
}

// A blob that arrived and was rejected is not a blob that never arrived.
//
// **The end of E312, and the fifth boundary in one path to throw its reason
// away.** The driver held the layer, packed it, and sent it; this end stored it,
// found it captured under a different digest, put it back on the wanted list and
// said nothing. The worker then reported that the driver did not hold it - which
// is the opposite of what happened.
//
// `keep` already produces the one sentence that explains the whole afternoon:
// *asked for X and got Y*. It was being discarded one line after being made.
func TestABlobRejectedOnArrivalSaysWhy(t *testing.T) {
	t.Parallel()

	_, err := fleet.Provision(t.Context(), newMapStore(),
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{{9}}},
		&wrongSource{body: []byte("something else entirely")})
	if err == nil {
		t.Fatal("a fetch that stored nothing succeeded")
	}

	if !strings.Contains(err.Error(), "asked for") {
		t.Errorf("%v\n  a peer that sent the wrong bytes reads as a peer that"+
			" sent none (E312)", err)
	}
}

// wrongSource answers every request, with the wrong thing.
type wrongSource struct{ body []byte }

func (w *wrongSource) Name() string { return "liar" }

func (w *wrongSource) Fetch(
	_ context.Context, ids []ir.NodeID,
) (map[ir.NodeID]io.Reader, error) {
	out := map[ir.NodeID]io.Reader{}
	for _, id := range ids {
		out[id] = bytes.NewReader(w.body)
	}

	return out, nil
}
