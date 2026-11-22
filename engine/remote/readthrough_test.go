package remote_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// asked records what was wanted of a machine that is not this one.
type asked struct {
	give map[ir.NodeID][]byte
	for_ []ir.NodeID
}

func (a *asked) Node(id ir.NodeID) ([]byte, error) {
	a.for_ = append(a.for_, id)

	b, ok := a.give[id]
	if !ok {
		return nil, errors.New("not here")
	}

	return b, nil
}

// A blob this machine holds is answered without asking anybody.
//
// **The property that keeps a read-through free.** A fleet worker's store holds
// most of what its steps ask for, and a hit that consulted a peer first would
// pay a round trip for every one of them - turning the common case into the
// expensive one to make the rare case cheap.
func TestAHeldBlobIsNotSoughtElsewhere(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	st := store.DirStore(t.TempDir())
	id := nodeIn(t, st, []byte("held here"))

	away := &asked{}
	srv := httptest.NewServer(&remote.Cache{Store: st, Elsewhere: away})

	defer srv.Close()

	if body := get(t, srv.URL+"/cas/"+id.String(), http.StatusOK); string(body) != "held here" {
		t.Errorf("served %q, want %q", body, "held here")
	}

	if len(away.for_) != 0 {
		t.Errorf("asked elsewhere for %v, which this machine already had", away.for_)
	}
}

// A blob this machine lacks comes from elsewhere.
//
// **The seam the whole cache-sharing design now rests on.** Everything else was
// already built: 𝔅 names blobs by their digests, `fleet.Blobs` already makes a
// blob store a place a step's faults are answered from, and the agent already
// speaks a protocol other tools use. What was missing was one machine's cache
// being able to say "not here, but I know who".
func TestABlobThisMachineLacksComesFromElsewhere(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	body := []byte("held by a peer")
	id := ir.DigestOf(body)

	srv := httptest.NewServer(&remote.Cache{
		Store:     store.DirStore(t.TempDir()),
		Elsewhere: &asked{give: map[ir.NodeID][]byte{id: body}},
	})

	defer srv.Close()

	if got := get(t, srv.URL+"/cas/"+id.String(), http.StatusOK); string(got) != string(body) {
		t.Errorf("served %q, want %q", got, body)
	}
}

// Elsewhere is not trusted, and that is what makes this safe to build at all.
//
// A blob is named by its digest, so bytes that do not hash to the name asked for
// are not that blob - whoever sent them and whatever they meant by it. §5.3's
// position exactly: cross-domain entries are unauthenticated data until
// verified, and A5 is an assumption about this engine's scepticism rather than
// about a peer's good faith.
//
// A miss rather than an error, because 𝔅's own rule is that a store returning
// wrong bytes is detected on read and the read becomes a miss (I4). An attacker
// with total control of a peer can deny service and nothing else.
func TestElsewhereIsVerifiedLikeAnythingElse(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	wanted := ir.DigestOf([]byte("what was asked for"))

	srv := httptest.NewServer(&remote.Cache{
		Store: store.DirStore(t.TempDir()),
		Elsewhere: &asked{give: map[ir.NodeID][]byte{
			wanted: []byte("something else entirely"),
		}},
	})

	defer srv.Close()

	get(t, srv.URL+"/cas/"+wanted.String(), http.StatusNotFound)
}

// With nobody to ask, a miss is the miss it always was.
func TestWithoutAnElsewhereAMissIsAMiss(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	srv := httptest.NewServer(&remote.Cache{Store: store.DirStore(t.TempDir())})
	defer srv.Close()

	get(t, srv.URL+"/cas/"+ir.DigestOf([]byte("nowhere")).String(), http.StatusNotFound)
}

// nodeIn puts a blob in a store and returns the digest naming it.
func nodeIn(t *testing.T, st store.DirStore, b []byte) ir.NodeID {
	t.Helper()

	id := ir.DigestOf(b)
	at := store.NodePath(string(st), id)

	if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(at, b, 0o600); err != nil {
		t.Fatal(err)
	}

	return id
}

func get(t *testing.T, at string, want int) []byte {
	t.Helper()

	resp, err := http.Get(at) //nolint:noctx // a test server on this machine
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != want {
		t.Fatalf("GET %s: %d, want %d", at, resp.StatusCode, want)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return b
}
