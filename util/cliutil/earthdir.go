package cliutil

import (
	"cmp"
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

var (
	earthDirCreateOnce sync.Once
	errEarthDirCreate  error
)

// DefaultInstallationName is the installation name used when none is set: when
// the binary is built without -X main.DefaultInstallationName (go build, go
// install), and on the autocomplete path, which runs before flags are parsed.
const DefaultInstallationName = "earth"

// GetEarthDir returns the .<installation name> dir. (Usually ~/.earth).
// This function will not attempt to create the directory if missing,
// for that functionality use to the [GetOrCreateEarthDir] function.
func GetEarthDir(installName string) string {
	earthDir, _ := getEarthDirAndUser(installName)

	return earthDir
}

func getEarthDirAndUser(installName string) (string, *user.User) {
	// if GetEarthDir is called by the autocomplete code, this may not be set
	installName = cmp.Or(installName, DefaultInstallationName)

	homeDirOnce.Do(func() {
		homeDir, sudoUser = fileutil.HomeDir()
	})

	return filepath.Join(homeDir, "."+installName), sudoUser
}

// GetOrCreateEarthDir returns the .<installation name> dir. (Usually ~/.earth).
// if the directory does not exist, it will attempt to create it.
func GetOrCreateEarthDir(installName string) (string, error) {
	earthDir, u := getEarthDirAndUser(installName)

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

			if u != nil {
				err := fileutil.EnsureUserOwned(earthDir, u)
				if err != nil {
					errEarthDirCreate = fmt.Errorf("failed to ensure %s is owned by %s: %w", earthDir, u, err)
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
	earthDir, u := getEarthDirAndUser(installName)
	if u != nil {
		err := fileutil.EnsureUserOwned(earthDir, u)
		if err != nil {
			return fmt.Errorf("failed to ensure %s is owned by %s: %w", earthDir, u, err)
		}
	}

	return nil
}
