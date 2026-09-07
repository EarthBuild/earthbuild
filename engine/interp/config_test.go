package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ENTRYPOINT, CMD, EXPOSE and the rest configure the *image*, not the
// filesystem. They add nothing to the graph and everything to what the image is.
func TestImageConfigIsCollected(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    RUN make
    ENTRYPOINT ["/usr/bin/tool"]
    CMD ["--serve"]
    EXPOSE 8080 9090
    LABEL org.opencontainers.image.source=https://example.invalid
    SAVE IMAGE tool:latest
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	// Two steps: FROM and RUN. The configuration commands are not steps.
	if got := len(p.Graph.Nodes()); got != 2 {
		t.Errorf("graph has %d nodes, want 2:\n%s", got, describe(p.Graph.Nodes()))
	}

	if len(p.Images) != 1 {
		t.Fatalf("collected %d images, want 1", len(p.Images))
	}

	cfg := p.Images[0].Config

	if got := strings.Join(cfg.Entrypoint, " "); got != "/usr/bin/tool" {
		t.Errorf("entrypoint is %q", got)
	}

	if got := strings.Join(cfg.Cmd, " "); got != "--serve" {
		t.Errorf("cmd is %q", got)
	}

	// `8080/tcp` and not `8080`: an OCI configuration names the protocol, and
	// the saved image had a key nothing but docker recognised until this was
	// normalised (E44).
	if got := strings.Join(cfg.Exposed, ","); got != "8080/tcp,9090/tcp" {
		t.Errorf("exposed ports are %q", got)
	}

	if cfg.Labels["org.opencontainers.image.source"] != "https://example.invalid" {
		t.Errorf("labels are %v", cfg.Labels)
	}
}

// The configuration recorded is the one in force where SAVE IMAGE appears.
//
// A command after it belongs to whatever is saved next, if anything. Taking the
// end-of-recipe state instead would let a later line silently change an image
// that was already declared.
func TestConfigIsSnapshotWhereTheImageIsSaved(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    CMD ["first"]
    SAVE IMAGE one:latest
    CMD ["second"]
    SAVE IMAGE two:latest
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Images) != 2 {
		t.Fatalf("collected %d images, want 2", len(p.Images))
	}

	for i, want := range []string{"first", "second"} {
		if got := strings.Join(p.Images[i].Config.Cmd, " "); got != want {
			t.Errorf("image %d has cmd %q, want %q", i, got, want)
		}
	}
}

// USER is different from the rest: it changes what a RUN *does*, not only what
// the image says. Running as root and running as nobody are different steps, so
// it belongs to the operation and reaches the key.
func TestUserReachesTheStep(t *testing.T) {
	t.Parallel()

	mk := func(user string) *ir.Node {
		p, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    USER "+user+"\n    RUN make\n", "build")
		if err != nil {
			t.Fatal(err)
		}

		return p.Graph.Root
	}

	if mk("root").ID() == mk("nobody").ID() {
		t.Error("the same command as two different users produced one step")
	}

	if got := mk("nobody").Op.User; got != "nobody" {
		t.Errorf("the step runs as %q, want nobody", got)
	}
}

// Shell form is accepted as well as exec form, because both are written.
func TestEntrypointShellForm(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
build:
    FROM alpine:3.22
    ENTRYPOINT /usr/bin/tool --serve
    SAVE IMAGE tool:latest
`, "build")
	if err != nil {
		t.Fatal(err)
	}

	// Shell form runs through a shell, which is what distinguishes it.
	got := p.Images[0].Config.Entrypoint
	if len(got) == 0 || got[0] != testShell {
		t.Errorf("shell-form entrypoint is %q, want it wrapped in a shell", got)
	}
}

// A target that configures an image but never saves one is not an error - the
// configuration simply applies to nothing.
func TestConfigWithoutSaveImageIsNotAnError(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+"\nbuild:\n    FROM alpine\n    CMD [\"x\"]\n    RUN make\n", "build")
	if err != nil {
		t.Fatal(err)
	}
}
