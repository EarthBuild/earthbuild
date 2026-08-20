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

	var up, body, down *ir.Node

	for _, n := range p.Graph.Nodes() {
		switch {
		case strings.Contains(n.Meta.Description, "compose up"):
			up = n
		case n.Meta.Description == "RUN run-the-tests":
			body = n
		case strings.Contains(n.Meta.Description, "compose down"):
			down = n
		}
	}

	for name, n := range map[string]*ir.Node{"up": up, "body": body, "down": down} {
		if n == nil {
			t.Fatalf("no %s step:\n%s", name, describe(p.Graph.Nodes()))
		}
	}

	if !reaches(body, up.Meta.Description) {
		t.Error("the block's commands do not stand on the services being up")
	}

	if !reaches(down, "RUN run-the-tests") {
		t.Error("the services come down before the commands that use them have run")
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
		if strings.Contains(n.Meta.Description, "compose up") {
			if !strings.Contains(strings.Join(n.Op.Args, " "), "--wait") {
				t.Errorf("the block starts before its services are ready: %v", n.Op.Args)
			}

			return
		}
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
		if !strings.Contains(n.Meta.Description, "compose up") {
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
