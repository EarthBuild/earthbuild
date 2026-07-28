package buildcontext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/util/fileutil"
	"github.com/moby/patternmatcher/ignorefile"
)

const (
	earthIgnoreFile   = ".earthignore"
	earthlyIgnoreFile = ".earthlyignore"
	dockerIgnoreFile  = ".dockerignore"
)

const (
	tmpOutputDir          = ".tmp-earth-out"
	tmpOutputDirWithSlash = tmpOutputDir + "/"
)

const (
	buildEarthFile = "build.earth"

	// Earthfile is the default project configuration file.
	Earthfile = "Earthfile"
)

var errDuplicateIgnoreFile = errors.New("both .earthignore and .earthlyignore exist - please remove one")

// ImplicitExcludes is a list of implicit patterns to exclude.
var ImplicitExcludes = []string{
	tmpOutputDirWithSlash,
	buildEarthFile,
	Earthfile,
	earthIgnoreFile,
	earthlyIgnoreFile,
}

func readExcludes(dir string, noImplicitIgnore bool, useDockerIgnore bool) ([]string, error) {
	ignoreFile := earthIgnoreFile

	// earthIgnoreFile
	earthIgnoreFilePath := filepath.Join(dir, earthIgnoreFile)

	earthExists, err := fileutil.FileExists(earthIgnoreFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to check if %s exists: %w", earthIgnoreFilePath, err)
	}

	// earthlyIgnoreFile
	earthlyIgnoreFilePath := filepath.Join(dir, earthlyIgnoreFile)

	earthlyExists, err := fileutil.FileExists(earthlyIgnoreFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to check if %s exists: %w", earthlyIgnoreFilePath, err)
	}

	// dockerIgnoreFile
	dockerIgnoreFilePath := filepath.Join(dir, dockerIgnoreFile)

	dockerExists := false
	if useDockerIgnore {
		dockerExists, err = fileutil.FileExists(dockerIgnoreFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to check if %s exists: %w", dockerIgnoreFilePath, err)
		}
	}

	defaultExcludes := ImplicitExcludes
	if noImplicitIgnore {
		defaultExcludes = []string{}
	}

	// Check which ones exists and which don't
	if earthExists && earthlyExists {
		// if both exist then throw an error
		return defaultExcludes, errDuplicateIgnoreFile
	}

	if earthExists == earthlyExists {
		if !dockerExists {
			// return just ImplicitExcludes if neither of them exist
			return defaultExcludes, nil
		}

		ignoreFile = dockerIgnoreFile
	} else if earthlyExists {
		ignoreFile = earthlyIgnoreFile
	}

	filePath := filepath.Join(dir, ignoreFile)

	f, err := os.Open(filePath) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filePath, err)
	}
	defer f.Close()

	excludes, err := ignorefile.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}

	return append(excludes, defaultExcludes...), nil
}
