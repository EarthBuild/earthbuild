package remote_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/remote"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A tree node this engine wrote is a blob Bazel can fetch by its digest.
//
// **The first thing another tool can use.** 𝜏 is an REAPI input-root digest
// rather than a translation of one, so a Directory this engine named is a
// Directory another tool asks for under the same number - and this is where
// that stops being a claim about encodings and becomes a cache hit.
func TestANodeIsServedByItsDigest(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	root := t.TempDir()
	st := store.DirStore(root)

	f := layer.NewFold()
	if !f.Add(manifestOf(t, map[string]string{"a.txt": "one", "sub/b.txt": "two"})) {
		t.Fatal("the manifest did not fold")
	}

	tree := f.Tree()
	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(&remote.Cache{Store: st})
	defer srv.Close()

	for id, want := range tree.Nodes() {
		resp, err := http.Get(srv.URL + "/cas/" + id.String())
		if err != nil {
			t.Fatal(err)
		}

		body := readAll(t, resp)

		if resp.StatusCode != http.StatusOK {
			t.Errorf("node %v: %s", id, resp.Status)

			continue
		}

		if string(body) != string(want) {
			t.Errorf("node %v served %d bytes, the store holds %d", id, len(body), len(want))
		}

		// And the bytes name the digest they were asked for, which is the only
		// thing that makes this a content-addressed store rather than a map.
		if got := ir.DigestOf(body); got != id {
			t.Errorf("asked for %v and was served bytes naming %v", id, got)
		}
	}
}

// A digest the store does not hold is a miss, not an error.
func TestAnAbsentBlobIsNotFound(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	srv := httptest.NewServer(&remote.Cache{Store: store.DirStore(t.TempDir())})
	defer srv.Close()

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, err := http.NewRequest(method, srv.URL+"/cas/"+ir.NodeID{7}.String(), nil)
		if err != nil {
			t.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s of an absent blob: %s, want 404", method, resp.Status)
		}
	}
}

// The action cache is not served, and says so rather than answering.
//
// **A 404 here would be a lie by omission.** Bazel reads "not found" as "run the
// action", which is correct - but it is also what it reads from a cache that is
// working and empty, so a front end that has not implemented /ac at all is
// indistinguishable from one that has and holds nothing. 501 says which.
func TestTheActionCacheSaysItIsNotServedYet(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	srv := httptest.NewServer(&remote.Cache{Store: store.DirStore(t.TempDir())})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ac/" + ir.NodeID{1}.String())
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("/ac answered %s, want 501 while it is unimplemented", resp.Status)
	}
}

// A store hashed with BLAKE3 cannot answer a protocol that asks in SHA-256.
//
// **Refused at the door, because the failure is otherwise invisible.** Bazel
// asks for a SHA-256 digest; a BLAKE3 store simply never holds one, so every
// request is a miss and the cache appears to work and do nothing. Serving it at
// all would be answering a question about a function this store does not use.
func TestABlake3StoreRefusesToServe(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashBLAKE3)
	defer restore()

	srv := httptest.NewServer(&remote.Cache{Store: store.DirStore(t.TempDir())})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/cas/" + ir.NodeID{1}.String())
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusOK {
		t.Errorf("a BLAKE3 store answered a SHA-256 protocol with %s, so every"+
			"\n  request is a silent miss and the cache looks empty rather than"+
			"\n  misconfigured", resp.Status)
	}
}

// Writing is refused, and names why.
func TestUploadsAreRefused(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	srv := httptest.NewServer(&remote.Cache{Store: store.DirStore(t.TempDir())})
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/cas/"+ir.NodeID{1}.String(), nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("a PUT answered %s, want 405", resp.Status)
	}
}

// A blob corrupted on disk is not served.
//
// **The promise that makes this a content-addressed store.** A client asks by
// digest and trusts what comes back to be those bytes - it is entitled to,
// because that is the whole contract - so serving something else is worse than
// serving nothing. Reading the file and writing it out would pass every other
// test here; only a corrupted store distinguishes them.
func TestACorruptBlobIsNotServed(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	root := t.TempDir()
	st := store.DirStore(root)

	f := layer.NewFold()
	if !f.Add(manifestOf(t, map[string]string{"a.txt": "one"})) {
		t.Fatal("the manifest did not fold")
	}

	tree := f.Tree()
	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	id := tree.Root()
	if err := os.WriteFile(store.NodePath(root, id), []byte("not the node"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(&remote.Cache{Store: st})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/cas/" + id.String())
	if err != nil {
		t.Fatal(err)
	}

	body := readAll(t, resp)

	if resp.StatusCode == http.StatusOK {
		t.Errorf("bytes naming %v were served under the name %v"+
			"\n  a client asks by digest and is entitled to get those bytes;"+
			"\n  serving something else is worse than serving nothing",
			ir.DigestOf(body), id)
	}
}

// Serving a request holds the machine open.
//
// **A machine with work in flight is not idle.** A sandbox stops itself when
// nobody has wanted it for a while, and idleness is measured by when a host
// last spoke - which a client inside a step is not. Without the hold the
// service has its own machine stopped underneath it mid-request, and the client
// sees a connection close with nothing saying why.
//
// Held across the whole request, released after: the release is what lets the
// countdown start, and starting it while bytes are still going out is the same
// bug one beat later.
func TestServingHoldsTheMachineOpen(t *testing.T) {
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	var held, released int

	srv := httptest.NewServer(&remote.Cache{
		Store: store.DirStore(t.TempDir()),
		Hold: func() func() {
			held++

			return func() { released++ }
		},
	})

	defer srv.Close()

	// Even a miss: a request that finds nothing still occupied the machine.
	resp, err := http.Get(srv.URL + "/cas/" + ir.NodeID{9}.String())
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if held != 1 {
		t.Errorf("the machine was held %d times for one request", held)
	}

	if released != 1 {
		t.Errorf("the hold was released %d times, so the machine never becomes"+
			" idle again and the sandbox outlives every use of it", released)
	}
}
