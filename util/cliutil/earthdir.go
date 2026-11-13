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
	homeDir     string
	homeDirOnce sync.Once
	sudoUser    *user.User
)

var createMu sync.Mutex

// DefaultInstallationName is the installation name used when none is set: when
// the binary is built without -X main.DefaultInstallationName (go build, go
// install), and on the autocomplete path, which runs before flags are parsed.
const DefaultInstallationName = "earth"

// ErrBothEarthDirsExist is returned when both ~/.earth and the deprecated ~/.earthly exist.
var ErrBothEarthDirsExist = errors.New("both ~/.earth and ~/.earthly paths exist; " +
	"the ~/.earthly path is deprecated, delete it")

// GetEarthDir returns the global configuration dir. (Usually ~/.earth).
// If only the deprecated ~/.earthly dir exists, it is returned instead so that
// existing configuration keeps working; see [LegacyEarthDirWarning].
// This function will not attempt to create the directory if missing,
// for that functionality use the [GetOrCreateEarthDir] function.
func GetEarthDir(installName string) string {
	earthDir, _ := getEarthDirAndUser(installName)

	return earthDir
}

func getEarthDirAndUser(installName string) (string, *user.User) {
	dir, legacyDir, u := earthDirs(installName)
	return selectEarthDir(dir, legacyDir), u
}

func selectEarthDir(dir, legacyDir string) string {
	if legacyDir != "" && !dirExists(dir) {
		return legacyDir
	}

	return dir
}

func earthDirs(installName string) (string, string, *user.User) {
	homeDirOnce.Do(func() {
		homeDir, sudoUser = fileutil.HomeDir()
	})

	return earthDirsAt(homeDir, installName, sudoUser)
}

func earthDirsAt(home, installName string, u *user.User) (string, string, *user.User) {
	installName = cmp.Or(installName, DefaultInstallationName)
	dir := filepath.Join(home, "."+installName)
	legacyDir := filepath.Join(home, ".earthly")

	if installName != DefaultInstallationName || !dirExists(legacyDir) {
		legacyDir = ""
	}

	return dir, legacyDir, u
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

	return legacyEarthDirWarning(dir, legacyDir)
}

func legacyEarthDirWarning(dir, legacyDir string) (string, error) {
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
// If the directory does not exist, it will attempt to create it.
func GetOrCreateEarthDir(installName string) (string, error) {
	earthDir, u := getEarthDirAndUser(installName)

	earthDirExists, err := fileutil.DirExists(earthDir)
	if err != nil {
		return earthDir, fmt.Errorf("check earth dir %s: %w", earthDir, err)
	}

	if earthDirExists {
		return earthDir, nil
	}

	createMu.Lock()
	defer createMu.Unlock()

	earthDirExists, err = fileutil.DirExists(earthDir)
	if err != nil {
		return earthDir, fmt.Errorf("check earth dir %s: %w", earthDir, err)
	}

	if !earthDirExists {
		err = os.MkdirAll(earthDir, 0o755) // #nosec G301
		if err != nil {
			return earthDir, fmt.Errorf("create earth dir %s: %w", earthDir, err)
		}

		if u != nil {
			err = fileutil.EnsureUserOwned(earthDir, u)
			if err != nil {
				return earthDir, fmt.Errorf("failed to ensure %s is owned by %s: %w", earthDir, userName(u), err)
			}
		}
	}

	return earthDir, nil
}

// userName returns the human-readable username of u, falling back to Uid,
// or "<unknown>" if u is nil or empty.
func userName(u *user.User) string {
	if u == nil {
		return "<nil>"
	}

	if u.Username != "" {
		return u.Username
	}

	if u.Uid != "" {
		return u.Uid
	}

	return "<unknown>"
}

// IsBootstrapped provides a tentatively correct guess about the state of our bootstrapping.
func IsBootstrapped(installName string) bool {
	exists, _ := fileutil.DirExists(GetEarthDir(installName))
	return exists
}

// EnsurePermissions changes the permissions of all earthbuild files to be owned by the user and their group.
func EnsurePermissions(installName string) error {
	earthDir, u := getEarthDirAndUser(installName)
	if u != nil {
		err := fileutil.EnsureUserOwned(earthDir, u)
		if err != nil {
			return fmt.Errorf("failed to ensure %s is owned by %s: %w", earthDir, userName(u), err)
		}
	}

	return nil
}
