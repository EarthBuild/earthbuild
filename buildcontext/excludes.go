package buildcontext

import (
	"os"
	"path/filepath"

	"github.com/earthbuild/earthbuild/util/fileutil"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/pkg/errors"
)

const earthIgnoreFile = ".earthignore"
const earthbuildIgnoreFile = ".earthbuildignore"
const dockerIgnoreFile = ".dockerignore"

var errDuplicateIgnoreFile = errors.New("both .earthignore and .earthbuildignore exist - please remove one")

// ImplicitExcludes is a list of implicit patterns to exclude.
var ImplicitExcludes = []string{
	".tmp-earthbuild-out/",
	"build.earth",
	"Earthfile",
	earthIgnoreFile,
	earthbuildIgnoreFile,
}

func readExcludes(dir string, noImplicitIgnore bool, useDockerIgnore bool) ([]string, error) {
	var ignoreFile = earthIgnoreFile

	//earthIgnoreFile
	var earthIgnoreFilePath = filepath.Join(dir, earthIgnoreFile)
	earthExists, err := fileutil.FileExists(earthIgnoreFilePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to check if %s exists", earthIgnoreFilePath)
	}

	//earthbuildIgnoreFile
	var earthbuildIgnoreFilePath = filepath.Join(dir, earthbuildIgnoreFile)
	earthbuildExists, err := fileutil.FileExists(earthbuildIgnoreFilePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to check if %s exists", earthbuildIgnoreFilePath)
	}

	//dockerIgnoreFile
	var dockerIgnoreFilePath = filepath.Join(dir, dockerIgnoreFile)
	dockerExists := false
	if useDockerIgnore {
		dockerExists, err = fileutil.FileExists(dockerIgnoreFilePath)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to check if %s exists", dockerIgnoreFilePath)
		}
	}

	defaultExcludes := ImplicitExcludes
	if noImplicitIgnore {
		defaultExcludes = []string{}
	}

	// Check which ones exists and which don't
	if earthExists && earthbuildExists {
		// if both exist then throw an error
		return defaultExcludes, errDuplicateIgnoreFile
	}
	if earthExists == earthbuildExists {
		if !dockerExists {
			// return just ImplicitExcludes if neither of them exist
			return defaultExcludes, nil
		}
		ignoreFile = dockerIgnoreFile
	} else if earthbuildExists {
		ignoreFile = earthbuildIgnoreFile
	}

	filePath := filepath.Join(dir, ignoreFile)
	f, err := os.Open(filePath)
	if err != nil {
		return nil, errors.Wrapf(err, "read %s", filePath)
	}
	defer f.Close()
	excludes, err := ignorefile.ReadAll(f)
	if err != nil {
		return nil, errors.Wrapf(err, "parse %s", filePath)
	}
	return append(excludes, defaultExcludes...), nil
}
