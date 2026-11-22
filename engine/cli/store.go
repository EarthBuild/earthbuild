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
		return private(filepath.Join(p, "earthbuild"))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find a home directory for the build cache: %w", err)
	}

	return private(filepath.Join(home, ".cache", "earthbuild"))
}

// private makes the default store readable by its owner alone, and returns it.
//
// It holds build contexts and layers: source code, and whatever a build fetched
// with credentials. `0750` read as private and was not on macOS, where every
// local user is in group `staff` and `staff` may enter a home directory. A store
// made before this is tightened. EARTH_CACHE_DIR is not passed through here: a
// shared cache is a choice somebody made.
func private(dir string) (string, error) {
	err := os.MkdirAll(dir, 0o700) //nolint:gosec // the user's own cache location
	if err != nil {
		return "", fmt.Errorf("create the build cache %s: %w", dir, err)
	}

	err = os.Chmod(dir, 0o700) //nolint:gosec // a directory: 0700 is its owner alone
	if err != nil {
		return "", fmt.Errorf("make the build cache %s private to this user: %w"+
			"\n  it holds build contexts and fetched layers; set %s to choose another place",
			dir, err, envCacheDir)
	}

	return dir, nil
}

// imageCacheDir is where pulled images live.
//
// Beside the build cache by default, and separable by EARTH_IMAGE_CACHE_DIR
// because the two answer different questions: a layer store belongs to a build
// cache and dies with it, while an image is content-addressed by reference and
// platform and is identical for every project on the machine. Pointing several
// build caches at one image cache is how a machine stops fetching alpine once
// per project.
// savedImagesDir is where SAVE IMAGE writes, or "" where there is no store.
// See image.SaveLocal.
func savedImagesDir() string {
	root, err := storeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(root, "images")
}

func imageCacheDir() (string, error) {
	if p := os.Getenv(envImageCacheDir); p != "" {
		return p, nil
	}

	return storeDir()
}
