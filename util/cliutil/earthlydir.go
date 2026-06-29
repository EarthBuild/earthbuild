package cliutil

import (
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

// GetEarthDir returns the .earthly dir. (Usually ~/.earthly).
// This function will not attempt to create the directory if missing,
// for that functionality use to the [GetOrCreateEarthDir] function.
func GetEarthDir(installName string) string {
	if installName == "" {
		// if GetEarthDir is called by the autocomplete code, this may not be set
		installName = "earthly"
	}

	earthDirOnce.Do(func() {
		earthDir, earthDirSudoUser = getEarthDirAndUser(installName)
	})

	return earthDir
}

func getEarthDirAndUser(installName string) (string, *user.User) {
	homeDir, u := fileutil.HomeDir()

	return filepath.Join(homeDir, "."+installName), u
}

// GetOrCreateEarthDir returns the .earthly dir. (Usually ~/.earthly).
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

// EnsurePermissions changes the permissions of all earthly files to be owned by the user and their group.
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
