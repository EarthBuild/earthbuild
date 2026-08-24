package image_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// Pulling an image reads its configuration, not only its layers.
//
// An image is a filesystem *and* a declaration about how to run it -
// ENTRYPOINT, ENV, WORKDIR, USER - and this engine fetched only the first. So
// `FROM node:20-alpine` gave `NODE_VERSION=[]`, `RUN --entrypoint` had nothing
// to prepend, and a derived SAVE IMAGE lost everything it inherited.
func TestAPullReadsTheImageConfiguration(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{
		layers: [][]byte{gzipTar(t, "greeting", "hi\n")},
		config: []byte(`{
			"config": {
				"Env": ["PATH=/opt/bin:/usr/bin", "NODE_VERSION=20.20.2"],
				"Entrypoint": ["/usr/local/bin/docker-entrypoint.sh"],
				"Cmd": ["node"],
				"WorkingDir": "/app",
				"User": "node"
			}
		}`),
	}

	cfg, err := image.Pull(context.Background(), reg.start(t)+"/library/thing:1",
		t.TempDir(), image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if got := len(cfg.Env); got != 2 {
		t.Fatalf("the image declares %d environment entries, want 2: %v", got, cfg.Env)
	}

	for i, want := range []string{"PATH=/opt/bin:/usr/bin", "NODE_VERSION=20.20.2"} {
		if cfg.Env[i] != want {
			t.Errorf("Env[%d] is %q, want %q", i, cfg.Env[i], want)
		}
	}

	if len(cfg.Entrypoint) != 1 || cfg.Entrypoint[0] != "/usr/local/bin/docker-entrypoint.sh" {
		t.Errorf("the entrypoint is %v", cfg.Entrypoint)
	}

	if cfg.WorkingDir != testWorkdir {
		t.Errorf("the working directory is %q", cfg.WorkingDir)
	}

	if cfg.User != "node" {
		t.Errorf("the user is %q", cfg.User)
	}
}

// An image that declares nothing pulls as happily as one that declares
// everything - most base images are the first kind.
func TestAnImageMayDeclareNothing(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "greeting", "hi\n")}}

	cfg, err := image.Pull(context.Background(), reg.start(t)+"/library/thing:1",
		t.TempDir(), image.Options{Plain: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Env) != 0 || len(cfg.Entrypoint) != 0 {
		t.Errorf("an empty configuration produced %+v", cfg)
	}
}

// An image built for another architecture is refused, saying which.
//
// A single-manifest image has no index to choose from, so nothing checked it:
// `hashicorp/terraform` and `namely/protoc-all` are linux/amd64 only, were
// pulled onto an arm64 machine, and failed inside the sandbox with
// `fork/exec /bin/sh: exec format error` - a message with nothing in it to
// connect to an image, an Earthfile, or a platform.
//
// The configuration says what the image is, and it is fetched now, so the
// mismatch can be named where it happens.
func TestAnImageForAnotherArchitectureIsRefused(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{
		layers: [][]byte{gzipTar(t, "bin/sh", "#!/bin/sh\n")},
		config: []byte(`{"architecture":"amd64","os":"linux","config":{}}`),
	}

	_, err := image.Pull(context.Background(), reg.start(t)+"/library/thing:1",
		t.TempDir(), image.Options{Plain: true, Platform: testPlatform})
	if err == nil {
		t.Fatal("an image for another architecture was accepted")
	}

	for _, want := range []string{"linux/amd64", testPlatform} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// One that matches is pulled, and one that says nothing about itself is trusted.
//
// An image with no architecture in its configuration is old or unusual rather
// than wrong, and refusing it would refuse something that works.
func TestAMatchingOrSilentImageIsPulled(t *testing.T) {
	t.Parallel()

	for _, cfg := range []string{
		`{"architecture":"arm64","os":"linux","config":{}}`,
		`{"config":{}}`,
	} {
		reg := &fakeRegistry{
			layers: [][]byte{gzipTar(t, "bin/sh", "#!/bin/sh\n")},
			config: []byte(cfg),
		}

		_, err := image.Pull(context.Background(), reg.start(t)+"/library/thing:1",
			t.TempDir(), image.Options{Plain: true, Platform: testPlatform})
		if err != nil {
			t.Errorf("an image that should have been pulled was refused: %v", err)
		}
	}
}
