package image_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// serveAfter answers the first `fail` requests with `code` and then serves a
// manifest, counting what it was asked.
func serveAfter(t *testing.T, fail int32, code int) (string, *atomic.Int32) {
	t.Helper()

	var seen atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// **Only the real read is counted or failed.** `token` probes the same
		// URL first, to draw the challenge, and does so without an Accept
		// header; `get` always sets one. Without this split the probe absorbs
		// the first failure and the test measures the challenge dance rather
		// than the retry.
		if r.Header.Get("Accept") == "" {
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = w.Write([]byte(`{"schemaVersion":2,"config":{},"layers":[]}`))

			return
		}

		if seen.Add(1) <= fail {
			w.WriteHeader(code)

			return
		}

		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte(`{"schemaVersion":2,"config":{},"layers":[]}`))
	}))
	t.Cleanup(srv.Close)

	return strings.TrimPrefix(srv.URL, "http://"), &seen
}

// A registry that is briefly unavailable is waited for.
//
// 503 is the registry saying "not me, not yet". Before this the engine asked
// once and failed the build - the same shape as the local-registry pull that
// fails about one CI job-run in a hundred.
func TestAResolveSurvivesATransientFiveHundred(t *testing.T) {
	t.Parallel()

	host, seen := serveAfter(t, 1, http.StatusServiceUnavailable)

	_, err := image.Resolve(context.Background(), host+"/library/test:1", image.Options{Plain: true})
	if err != nil {
		t.Fatalf("a 503 followed by success should resolve, got: %v", err)
	}

	if got := seen.Load(); got != 2 {
		t.Errorf("registry saw %d requests, want 2 (one refusal, one retry)", got)
	}
}

// A 404 is an answer, not a wait.
//
// Retrying it would spend the policy's whole budget re-asking a question the
// registry has already answered, and would slow every genuine "no such image"
// by the length of the backoff.
func TestAResolveDoesNotRetryANotFound(t *testing.T) {
	t.Parallel()

	host, seen := serveAfter(t, 99, http.StatusNotFound)

	_, err := image.Resolve(context.Background(), host+"/library/test:1", image.Options{Plain: true})
	if err == nil {
		t.Fatal("want an error for a missing manifest")
	}

	if got := seen.Load(); got != 1 {
		t.Errorf("registry saw %d requests, want 1 - a 404 is not retried", got)
	}
}

// Rate limiting is worth waiting for, unlike the rest of the 4xx family.
func TestAResolveRetriesRateLimiting(t *testing.T) {
	t.Parallel()

	host, seen := serveAfter(t, 1, http.StatusTooManyRequests)

	_, err := image.Resolve(context.Background(), host+"/library/test:1", image.Options{Plain: true})
	if err != nil {
		t.Fatalf("a 429 followed by success should resolve, got: %v", err)
	}

	if got := seen.Load(); got != 2 {
		t.Errorf("registry saw %d requests, want 2", got)
	}
}
