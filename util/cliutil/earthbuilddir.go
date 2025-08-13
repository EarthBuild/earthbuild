package cliutil

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sync"

	"github.com/earthbuild/earthbuild/util/fileutil"
	"github.com/pkg/errors"
)

var earthbuildDir string
var earthbuildDirOnce sync.Once
var earthbuildDirSudoUser *user.User

var earthbuildDirCreateOnce sync.Once
var earthbuildDirCreateErr error

// GetearthbuildDir returns the .earthbuild dir. (Usually ~/.earthbuild).
// This function will not attempt to create the directory if missing, for that functionality use to the GetOrCreateearthbuildDir function.
func GetearthbuildDir(installName string) string {
	if installName == "" {
		// if GetearthbuildDir is called by the autocomplete code, this may not be set
		installName = "earthbuild"
	}
	earthbuildDirOnce.Do(func() {
		earthbuildDir, earthbuildDirSudoUser = getearthbuildDirAndUser(installName)
	})
	return earthbuildDir
}

func getearthbuildDirAndUser(installName string) (string, *user.User) {
	homeDir, u := fileutil.HomeDir()
	earthbuildDir := filepath.Join(homeDir, fmt.Sprintf(".%s", installName))
	return earthbuildDir, u
}

// GetOrCreateearthbuildDir returns the .earthbuild dir. (Usually ~/.earthbuild).
// if the directory does not exist, it will attempt to create it.
func GetOrCreateearthbuildDir(installName string) (string, error) {
	_ = GetearthbuildDir(installName) // ensure global vars get created so we can reference them below.

	earthbuildDirCreateOnce.Do(func() {
		earthbuildDirExists, err := fileutil.DirExists(earthbuildDir)
		if err != nil {
			earthbuildDirCreateErr = errors.Wrapf(err, "unable to create dir %s", earthbuildDir)
			return
		}
		if !earthbuildDirExists {
			err := os.MkdirAll(earthbuildDir, 0755)
			if err != nil {
				earthbuildDirCreateErr = errors.Wrapf(err, "unable to create dir %s", earthbuildDir)
				return
			}
			if earthbuildDirSudoUser != nil {
				err := fileutil.EnsureUserOwned(earthbuildDir, earthbuildDirSudoUser)
				if err != nil {
					earthbuildDirCreateErr = errors.Wrapf(err, "failed to ensure %s is owned by %s", earthbuildDir, earthbuildDirSudoUser)
				}
			}
		}
	})

	return earthbuildDir, earthbuildDirCreateErr
}

// IsBootstrapped provides a tentatively correct guess about the state of our bootstrapping.
func IsBootstrapped(installName string) bool {
	exists, _ := fileutil.DirExists(GetearthbuildDir(installName))
	return exists
}

// EnsurePermissions changes the permissions of all earthbuild files to be owned by the user and their group.
func EnsurePermissions(installName string) error {
	earthbuildDir, sudoUser := getearthbuildDirAndUser(installName)
	if sudoUser != nil {
		err := fileutil.EnsureUserOwned(earthbuildDir, sudoUser)
		if err != nil {
			return errors.Wrapf(err, "failed to ensure %s is owned by %s", earthbuildDir, sudoUser)
		}
	}
	return nil
}
