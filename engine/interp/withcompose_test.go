package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `--compose` brings services up before the block's commands and takes them
// down after.
//
// Both halves are the feature. Bringing them up is what the block is for; taking
// them down matters because the daemon outlives the build, so a service left
// running is still there for the next one - and for every build after that.
func TestComposeBringsServicesUpAndDown(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --compose docker-compose.yml
        RUN run-the-tests
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	// **In the body's own command, not around it.** They were separate steps
	// until E970, and a step gets a daemon of its own - so the services came up
	// in one daemon and died with it before the body ran. The property is
	// unchanged; where it is expressed is not.
	var body *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == "RUN run-the-tests" {
			body = n
		}
	}

	if body == nil {
		t.Fatalf("no body step:\n%s", describe(p.Graph.Nodes()))
	}

	cmd := strings.Join(body.Op.Args, " ")

	if !strings.Contains(cmd, "up -d") {
		t.Errorf("nothing brings the services up: %s", cmd)
	}

	if !strings.Contains(cmd, "down") {
		t.Errorf("nothing takes the services down: %s", cmd)
	}

	// Order, which is the whole point: up, then the author's command, then down.
	upAt, bodyAt, downAt := strings.Index(cmd, "up -d"),
		strings.Index(cmd, "run-the-tests"), strings.LastIndex(cmd, "down")
	if upAt >= bodyAt || bodyAt >= downAt {
		t.Errorf("the services do not surround the command: %s", cmd)
	}
}

// Waiting is part of bringing them up.
//
// `docker compose up -d` returns when containers have started, not when they
// are ready, and the first line of the block is usually something that connects
// to one. Without the wait the failure is a connection refused that succeeds on
// a retry, which is the least actionable kind of flake there is.
func TestComposeWaitsForServicesToBeReady(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --compose docker-compose.yml
        RUN run-the-tests
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description != "RUN run-the-tests" {
			continue
		}

		if !strings.Contains(strings.Join(n.Op.Args, " "), "--wait") {
			t.Errorf("the block starts before its services are ready: %v", n.Op.Args)
		}

		return
	}

	t.Error("nothing brings the services up")
}

// `--service` narrows what comes up; without it, everything in the file does.
func TestNamedServicesAreTheOnesBroughtUp(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --compose docker-compose.yml --service db --service cache
        RUN run-the-tests
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description != "RUN run-the-tests" {
			continue
		}

		cmd := strings.Join(n.Op.Args, " ")
		for _, want := range []string{"db", "cache"} {
			if !strings.Contains(cmd, want) {
				t.Errorf("%q is not brought up: %s", want, cmd)
			}
		}

		return
	}

	t.Error("nothing brings the services up")
}

// A service asked for with no compose file to find it in is refused.
func TestAServiceWithoutAComposeFileIsRefused(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --service db
        RUN run-the-tests
    END
`, testMain)
	if err == nil {
		t.Fatal("a service was brought up from no compose file")
	}

	if !strings.Contains(err.Error(), "--compose") {
		t.Errorf("the refusal does not say what is missing:\n%s", err)
	}
}

// The services and the body share one step, because they must share one daemon.
//
// `WITH DOCKER` permits exactly one `RUN`, and the daemon's whole lifetime is
// that command: earthfile.md says the daemon is stopped and its data deleted
// once the RUN completes. The engine planned the compose up, the body and the
// compose down as *separate steps*, and a step gets a daemon of its own - so the
// services came up in one daemon, that daemon was torn down when its step ended,
// and the body ran against a fresh one with nothing in it.
//
// The symptom was a build that hung for the six hours GitHub allows, waiting for
// a port nothing was listening on, while `docker compose` had reported the
// container healthy seconds earlier (E970).
//
// Shared storage is not a shared daemon: `--load` works across steps because an
// image written to disk survives the daemon that wrote it, and a running
// container does not.
func TestComposeSharesTheBodysStepAndSoItsDaemon(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
    WITH DOCKER --compose docker-compose.yml --service db
        RUN run-the-tests
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var body *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == "RUN run-the-tests" {
			body = n
		}

		// Nothing may run `docker compose up` as a step of its own: whatever it
		// starts dies with that step's daemon.
		if n != body && strings.Contains(strings.Join(n.Op.Args, " "), "compose up") {
			t.Errorf("`compose up` is a step of its own (%s), so its services die"+
				" with that step's daemon", n.Meta.Source)
		}
	}

	if body == nil {
		t.Fatal("the block's command was not planned")
	}

	// The body's own command brings them up, so it is the same step and
	// therefore the same daemon - which is how the reference does it:
	// `dockerd-wrapper.sh execute --compose ... -- <command>`.
	cmd := strings.Join(body.Op.Args, " ")
	if !strings.Contains(cmd, "compose") || !strings.Contains(cmd, "up") {
		t.Errorf("the body's command does not bring the services up, so nothing"+
			" starts them in its daemon: %s", cmd)
	}

	if !strings.Contains(cmd, "db") {
		t.Errorf("the named service is not in the body's command: %s", cmd)
	}

	if !strings.Contains(cmd, "run-the-tests") {
		t.Errorf("the author's own command was lost: %s", cmd)
	}
}
