package cli

import (
	"context"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

// imageEnv reads what a base image declares, for a Dockerfile whose WORKDIR
// names a variable the image sets.
//
// **Memoised per reference and platform.** A Dockerfile is a chain of stages and
// several of them name the same base; asking the registry once per mention would
// pay for the same answer repeatedly, and a build resolves its references
// concurrently, so the memo settles the resolution and not merely the storing of
// it - the same shape, and the same reason, as the credential memo.
func (g *engine) imageEnv(ctx context.Context) interp.ImageEnv {
	var known sync.Map

	type held struct {
		once     sync.Once
		declared interp.ImageDeclares
		err      error
	}

	challenges, err := imageCacheDir()
	if err != nil {
		challenges = ""
	}

	return func(ref, platform string) (interp.ImageDeclares, error) {
		key := ref + "\x00" + platform

		slot, _ := known.LoadOrStore(key, &held{})

		h, ok := slot.(*held)
		if !ok {
			return interp.ImageDeclares{}, nil
		}

		h.once.Do(func() {
			cfg, err := image.Config(ctx, ref, image.Options{
				Platform: resolveFor(platform), Challenges: challenges,
			})
			if err != nil {
				h.err = err

				return
			}

			// An image that declares nothing is ordinary, and is not an error.
			h.declared.WorkingDir = cfg.WorkingDir

			if len(cfg.Env) == 0 {
				return
			}

			h.declared.Env = make(map[string]string, len(cfg.Env))

			for _, kv := range cfg.Env {
				name, value, found := cutEnv(kv)
				if found {
					h.declared.Env[name] = value
				}
			}
		})

		return h.declared, h.err
	}
}

// cutEnv splits an image's `NAME=value`, which is the form a config uses.
func cutEnv(kv string) (string, string, bool) {
	for i := range len(kv) {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}

	return "", "", false
}
