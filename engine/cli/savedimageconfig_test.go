package cli

import (
	"slices"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/decl"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

// A SAVE IMAGE layout keeps what its base declared, as a packed one does.
//
// E771 fixed this for `WITH DOCKER --load`, which packs through
// `engine/exec`. `SAVE IMAGE` writes its layout here instead, from
// `img.Config` alone - so the same image written the other way declared no
// PATH. Two paths to one format that disagree about what the image says are
// worse than either being wrong (E773).
func TestASavedImageKeepsWhatItsBaseDeclared(t *testing.T) {
	t.Parallel()

	base := decl.Declaration{
		Env:        []string{"PATH=/usr/bin:/bin"},
		WorkingDir: "/base",
	}
	when := time.Unix(1700000000, 0).UTC()

	spec := specFor(interp.Image{
		Ref:    "thing:latest",
		Config: interp.Config{Env: map[string]string{"GREETING": "hello"}},
	}, "linux/amd64", nil, base, when)

	if !slices.Contains(spec.Config.Env, "PATH=/usr/bin:/bin") {
		t.Errorf("the base's PATH is gone: %v", spec.Config.Env)
	}

	if !slices.Contains(spec.Config.Env, "GREETING=hello") {
		t.Errorf("the target's own env is gone: %v", spec.Config.Env)
	}

	if spec.Config.WorkingDir != "/base" {
		t.Errorf("WorkingDir = %q, want the base's /base", spec.Config.WorkingDir)
	}

	if !spec.Created.Equal(when) {
		t.Errorf("Created = %v, want %v", spec.Created, when)
	}
}
