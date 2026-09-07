package image_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/timing"
)

// One round trip is reported as one phase.
//
// **A phase log is read as a list of costs, so a call timed twice is counted
// twice.** `Resolve` wrapped its call to `token` in a `pin:token` phase, and
// `token` opens a `registry:token` phase of its own - so a single exchange
// printed two lines, agreeing to the millisecond:
//
//	registry:token 0.319s  registry-1.docker.io/library/python
//	pin:token      0.319s  docker.io
//
// Adding them sized the registry work at 570ms when it was 285ms, and that
// arithmetic is exactly how a prefetch bug was nearly mis-sized by a factor of
// three (E732, E733).
//
// The inner phase is the one kept: its key names the host and the repository,
// where the outer one named only the registry.
func TestOneTokenExchangeIsOnePhase(t *testing.T) {
	// Not parallel: timing.To is a package-level writer.
	var out bytes.Buffer

	restore := timing.To
	timing.To = &out

	defer func() { timing.To = restore }()

	f := &fakeRegistry{auth: true}
	host := f.start(t)

	_, err := image.Resolve(context.Background(), host+"/thing:latest", image.Options{Plain: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var tokenPhases []string

	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, ":token") {
			tokenPhases = append(tokenPhases, strings.TrimSpace(line))
		}
	}

	if len(tokenPhases) != 1 {
		t.Errorf("one token exchange reported %d phases, want 1\n  %s"+
			"\n  a phase that contains another is counted twice by anybody"+
			" adding the log up, and the log says nothing about which contains"+
			" which (E733)",
			len(tokenPhases), strings.Join(tokenPhases, "\n  "))
	}
}
