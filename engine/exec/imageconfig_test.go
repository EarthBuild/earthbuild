package exec

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/decl"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// A packed image keeps what its base declared.
//
// `SAVE IMAGE` wrote only what the Earthfile said, so an image built `FROM
// alpine` declared no PATH at all - alpine declares one and the packed image
// dropped it. `docker run` hid that, because the daemon substitutes a default
// PATH for an image that declares none; `docker inspect` did not, and neither
// would `FROM openjdk` losing JAVA_HOME (E771).
func TestAPackedImageKeepsWhatItsBaseDeclared(t *testing.T) {
	t.Parallel()

	base := decl.Declaration{
		Env:        []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"},
		WorkingDir: "/base",
		User:       "nobody",
		Cmd:        []string{"/bin/base"},
	}

	got := ConfigWithBase(base, ocispec.ImageConfig{
		Env:        []string{"GREETING=hello", "LANG=en_GB.UTF-8"},
		WorkingDir: "/w",
	})

	// The base's, kept.
	if !slices.Contains(got.Env, "PATH=/usr/bin:/bin") {
		t.Errorf("the base's PATH is gone: %v", got.Env)
	}

	// The Earthfile's, added.
	if !slices.Contains(got.Env, "GREETING=hello") {
		t.Errorf("the target's own env is gone: %v", got.Env)
	}

	// The Earthfile's, winning: a target that sets a variable the base also set
	// means to change it, and an image carrying both is an image whose
	// environment depends on which one a reader takes.
	if slices.Contains(got.Env, "LANG=C.UTF-8") || !slices.Contains(got.Env, "LANG=en_GB.UTF-8") {
		t.Errorf("the target did not override the base: %v", got.Env)
	}

	if got.WorkingDir != "/w" {
		t.Errorf("WorkingDir = %q, want the target's /w", got.WorkingDir)
	}

	// Not set by the target, so the base's stands - the same rule the runtime
	// already applies to a step.
	if got.User != "nobody" {
		t.Errorf("User = %q, want the base's nobody", got.User)
	}

	if !slices.Equal(got.Cmd, []string{"/bin/base"}) {
		t.Errorf("Cmd = %v, want the base's", got.Cmd)
	}
}
