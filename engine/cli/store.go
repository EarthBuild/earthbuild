package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// The variables that move the two directories images are unpacked into.
//
// Named once because each is written twice - where it is read, and in the note
// that tells someone to set it - and those two must be the same string. They
// were not: the note said EARTH_CACHE_DIR while warning about the image cache,
// so following it moved a directory that was not at fault and changed nothing.
const (
	envCacheDir      = "EARTH_CACHE_DIR"
	envImageCacheDir = "EARTH_IMAGE_CACHE_DIR"
)

// storeDir is where layers and cache entries live between builds.
//
// A stable location is the whole point: the store defaulted to a fresh
// temporary directory, which meant every build was a first build. Chosen in the
// usual order - an explicit override, then XDG, then the conventional fallback -
// so it lands where a user's other tools already put things and can be deleted
// with one `rm -rf`.
func storeDir() (string, error) {
	if p := os.Getenv(envCacheDir); p != "" {
		return p, nil
	}

	if p := os.Getenv("XDG_CACHE_HOME"); p != "" {
		return filepath.Join(p, "earthbuild"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find a home directory for the build cache: %w", err)
	}

	return filepath.Join(home, ".cache", "earthbuild"), nil
}

// imageCacheDir is where pulled images live.
//
// Beside the build cache by default, and separable by EARTH_IMAGE_CACHE_DIR
// because the two answer different questions: a layer store belongs to a build
// cache and dies with it, while an image is content-addressed by reference and
// platform and is identical for every project on the machine. Pointing several
// build caches at one image cache is how a machine stops fetching alpine once
// per project.
func imageCacheDir() (string, error) {
	if p := os.Getenv(envImageCacheDir); p != "" {
		return p, nil
	}

	return storeDir()
}
