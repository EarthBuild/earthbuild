package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `CACHE` takes a path relative to the working directory.
//
// `examples/cache-command/npm` is the case: `WORKDIR /app` and
// `CACHE ./node_modules`, which is where `npm install` writes. This engine
// resolved the path against `/` and mounted `/node_modules` - a directory
// nothing touches - so the cache cached nothing and everything it was meant to
// hold went into the step's own layer instead (E498).
//
// Found by reading a profile rather than by a failing build: the step recorded
// **2382 reads and 1627 negative lookups** under `/app/node_modules`, and a path
// inside a cache mount is filtered out of an observation before it is recorded
// (E222). Files that appear in a profile are files that were not mounted.
func TestACacheIsRelativeToTheWorkingDirectory(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		recipe string
		want   string
	}{
		"under a WORKDIR": {
			recipe: "    WORKDIR /app\n    CACHE ./node_modules\n",
			want:   "/app/node_modules",
		},
		"an absolute path is itself": {
			recipe: "    WORKDIR /app\n    CACHE /var/cache/apt\n",
			want:   "/var/cache/apt",
		},
		"with no WORKDIR at all": {
			recipe: "    CACHE ./out\n",
			want:   "/out",
		},
		// The working directory at the CACHE line, not the last one in the
		// recipe: a WORKDIR after it belongs to the steps after it.
		"the WORKDIR in force": {
			recipe: "    WORKDIR /app\n    CACHE ./node_modules\n    WORKDIR /elsewhere\n",
			want:   "/app/node_modules",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build(versioned+
				"\nmain:\n    FROM alpine:3.22\n"+tc.recipe+"    RUN make\n", testMain)
			if err != nil {
				t.Fatalf("planning: %v", err)
			}

			var got []string

			for _, n := range p.Graph.Nodes() {
				if n.Op.Kind != ir.OpExec {
					continue
				}

				for _, m := range n.Op.Mounts {
					got = append(got, m.Target)
				}
			}

			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("the step mounts %v, want [%s]"+
					"\n  a cache at the wrong path caches nothing, and the"+
					" directory it was meant to hold goes into the layer",
					got, tc.want)
			}
		})
	}
}
