package image_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// Concurrent callers share one token exchange.
//
// **Found on the x86 box, not here.** `image.Warm` fetches a token beside the
// sandbox boot so the pull finds it cached (E907). On macOS the boot is 1.4s,
// so the warm always wins the race and the pull's exchange costs 0.09s. On
// Linux there is no VM to boot: the pull starts at once, both miss the cache,
// and the build makes *two* full exchanges where it used to make one - an extra
// request against a rate limit, for nothing (E915).
//
// The cache had no single-flight, so "warm it early" only ever helped when
// something else happened to be slow.
func TestConcurrentTokenFetchesShareOneExchange(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "f", "one")}, auth: true}
	host := reg.start(t)
	ref := host + "/library/test:1"

	const callers = 8

	var wg sync.WaitGroup

	start := make(chan struct{})

	for range callers {
		wg.Go(func() {
			<-start

			_, _ = image.Resolve(context.Background(), ref, image.Options{Plain: true})
		})
	}

	close(start)
	wg.Wait()

	if got := reg.seen(&reg.tokens); got != 1 {
		t.Errorf("%d concurrent resolutions made %d token exchanges, want 1"+
			"\n  a bearer token is good for minutes and they all wanted the same one;"+
			"\n  without single-flight, warming one early only helps when the other"+
			"\n  caller happens to be slower (E915)", callers, got)
	}
}
