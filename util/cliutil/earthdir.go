package cliutil

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sync"

	"github.com/EarthBuild/earthbuild/util/fileutil"
)

var (
	earthDir         string
	earthDirOnce     sync.Once
	earthDirSudoUser *user.User
)

var (
	earthDirCreateOnce sync.Once
	errEarthDirCreate  error
)

// ErrBothEarthDirsExist is returned when both ~/.earth and the deprecated ~/.earthly exist.
var ErrBothEarthDirsExist = errors.New("both ~/.earth and ~/.earthly paths exist; " +
	"the ~/.earthly path is deprecated, delete it")

// GetEarthDir returns the global configuration dir. (Usually ~/.earth).
// If only the deprecated ~/.earthly dir exists, it is returned instead so that
// existing configuration keeps working; see [LegacyEarthDirWarning].
// This function will not attempt to create the directory if missing,
// for that functionality use to the [GetOrCreateEarthDir] function.
func GetEarthDir(installName string) string {
	earthDirOnce.Do(func() {
		earthDir, earthDirSudoUser = getEarthDirAndUser(installName)
	})

	return earthDir
}

func getEarthDirAndUser(installName string) (string, *user.User) {
	dir, legacyDir, u := earthDirs(installName)

	if legacyDir != "" && !dirExists(dir) {
		return legacyDir, u
	}

	return dir, u
}

// earthDirs returns the current dir and, for the default installation only,
// the deprecated ~/.earthly dir if it exists.
func earthDirs(installName string) (string, string, *user.User) {
	homeDir, u := fileutil.HomeDir()
	installName = cmp.Or(installName, "earth") // may be unset when called by the autocomplete code

	legacyDir := filepath.Join(homeDir, ".earthly")
	if installName != "earth" || !dirExists(legacyDir) {
		legacyDir = ""
	}

	return filepath.Join(homeDir, "."+installName), legacyDir, u
}

func dirExists(dir string) bool {
	exists, _ := fileutil.DirExists(dir)
	return exists
}

// LegacyEarthDirWarning returns a warning to show the user when only the
// deprecated ~/.earthly dir exists, and [ErrBothEarthDirsExist] when both it
// and ~/.earth exist.
func LegacyEarthDirWarning(installName string) (string, error) {
	dir, legacyDir, _ := earthDirs(installName)

	switch {
	case legacyDir == "":
		return "", nil
	case dirExists(dir):
		return "", ErrBothEarthDirsExist
	default:
		return "The ~/.earthly path is deprecated. Move your configuration to ~/.earth.", nil
	}
}

// GetOrCreateEarthDir returns the global configuration dir. (Usually ~/.earth).
// if the directory does not exist, it will attempt to create it.
func GetOrCreateEarthDir(installName string) (string, error) {
	_ = GetEarthDir(installName) // ensure global vars get created so we can reference them below.

	earthDirCreateOnce.Do(func() {
		earthDirExists, err := fileutil.DirExists(earthDir)
		if err != nil {
			errEarthDirCreate = fmt.Errorf("unable to create dir %s: %w", earthDir, err)
			return
		}

		if !earthDirExists {
			err := os.MkdirAll(earthDir, 0o755) // #nosec G301
			if err != nil {
				errEarthDirCreate = fmt.Errorf("unable to create dir %s: %w", earthDir, err)
				return
			}

			if earthDirSudoUser != nil {
				err := fileutil.EnsureUserOwned(earthDir, earthDirSudoUser)
				if err != nil {
					errEarthDirCreate = fmt.Errorf("failed to ensure %s is owned by %s: %w", earthDir, earthDirSudoUser, err)
				}
			}
		}
	})

	return earthDir, errEarthDirCreate
}

// IsBootstrapped provides a tentatively correct guess about the state of our bootstrapping.
func IsBootstrapped(installName string) bool {
	exists, _ := fileutil.DirExists(GetEarthDir(installName))
	return exists
}

// EnsurePermissions changes the permissions of all earthbuild files to be owned by the user and their group.
func EnsurePermissions(installName string) error {
	earthDir, sudoUser := getEarthDirAndUser(installName)
	if sudoUser != nil {
		err := fileutil.EnsureUserOwned(earthDir, sudoUser)
		if err != nil {
			return fmt.Errorf("failed to ensure %s is owned by %s: %w", earthDir, sudoUser, err)
		}
	}

	return nil
}
