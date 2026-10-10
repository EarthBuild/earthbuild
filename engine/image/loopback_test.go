package image

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Only a registry on this machine may be spoken to in plain HTTP.
//
// Docker's own local registry, `registry:2`, serves HTTP unless it is handed a
// certificate, and containerd's answer is to try HTTPS and fall back for a
// loopback host. Anywhere else a plaintext pull is one an intermediary can
// rewrite, so the fallback must stop at exactly these hosts - a name that
// merely starts with "localhost" is somebody else's machine.
func TestOnlyALoopbackRegistryMayFallBackToHTTP(t *testing.T) {
	t.Parallel()

	for host, want := range map[string]bool{
		"localhost:5055":             true,
		"localhost":                  true,
		"127.0.0.1:5000":             true,
		"127.1.2.3:5000":             true,
		"[::1]:5000":                 true,
		"localhost.example.com:5000": false,
		"10.0.0.1:5000":              false,
		"docker.io":                  false,
		"ghcr.io":                    false,
	} {
		if got := loopbackRegistry(host); got != want {
			t.Errorf("loopbackRegistry(%q) = %v, want %v", host, got, want)
		}
	}
}

// A push to a plain-HTTP registry on this machine works without being told.
//
// **What `registry:2 -p 5055:5000` is**, and what the midnight-node warm path
// pushed to: rth spoke HTTPS, the registry answered in HTTP, and the push
// failed with nothing in rth able to set `Plain`.
func TestAPushToAPlainLoopbackRegistryFallsBackToHTTP(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{blobs: map[string][]byte{}, manifests: map[string]string{}}
	srv := httptest.NewServer(reg.handler(t))

	defer srv.Close()

	reg.realm = srv.URL

	dir := writeATinyLayout(t, "app:latest")

	_, err := Push(context.Background(), dir,
		strings.TrimPrefix(srv.URL, "http://")+"/app:latest",
		PushOptions{Client: srv.Client()})
	if err != nil {
		t.Fatalf("push to a plain registry on 127.0.0.1 without Plain: %v", err)
	}

	if _, ok := reg.manifests["latest"]; !ok {
		t.Errorf("no manifest was put under the tag: %v", reg.manifests)
	}
}

// And a registry that does speak TLS is never downgraded, whatever goes wrong.
//
// **The fallback is for a server that answered in HTTP, and nothing else.** A
// certificate this machine does not trust is the case TLS exists to refuse;
// retrying in plaintext would turn "someone is in the middle" into "carry on".
func TestACertificateFailureNeverDowngrades(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	defer srv.Close()

	// A client that does not trust the test server's certificate, and records
	// every scheme it is asked to use.
	seen := &schemeLog{next: http.DefaultTransport}
	client := &http.Client{Transport: seen}

	dir := writeATinyLayout(t, "app:latest")

	_, err := Push(context.Background(), dir,
		strings.TrimPrefix(srv.URL, "https://")+"/app:latest",
		PushOptions{Client: client})
	if err == nil {
		t.Fatal("a push to an untrusted certificate succeeded")
	}

	if !strings.Contains(err.Error(), "certificate") {
		t.Errorf("the error does not say the certificate was refused: %v", err)
	}

	if seen.plain() {
		t.Error("a certificate failure was retried over plain HTTP" +
			"\n  only a server that answers in HTTP may be spoken to in HTTP")
	}
}

// schemeLog is a transport that remembers whether it was ever asked for http.
type schemeLog struct {
	next http.RoundTripper

	mu   sync.Mutex
	http bool
}

func (s *schemeLog) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	if r.URL.Scheme == "http" {
		s.http = true
	}
	s.mu.Unlock()

	return s.next.RoundTrip(r)
}

func (s *schemeLog) plain() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.http
}
