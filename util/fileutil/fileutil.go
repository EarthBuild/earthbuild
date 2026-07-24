// Package fileutil contains robust cross-platform utilities for file and directory checks, path expansion,\
// and globbing.
package fileutil

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// FileExists returns true if the file exists.
func FileExists(filename string) (bool, error) {
	info, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("unable to stat %s: %w", filename, err)
	}

	return !info.IsDir(), nil
}

// FileExistsBestEffort returns true if the directory exists and ignores errors.
func FileExistsBestEffort(filename string) bool {
	ok, _ := FileExists(filename)
	return ok
}

// DirExists returns true if the directory exists.
func DirExists(filename string) (bool, error) {
	info, err := os.Stat(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, fmt.Errorf("unable to stat %s: %w", filename, err)
	}

	return info.IsDir(), nil
}

// DirExistsBestEffort returns true if the directory exists and ignores errors.
func DirExistsBestEffort(filename string) bool {
	ok, _ := DirExists(filename)
	return ok
}

// EnsureUserOwned changes the files in the directory to be owned by the use and their group,
// as specified by the provided user.
func EnsureUserOwned(dir string, owner *user.User) error {
	uid, err := strconv.Atoi(owner.Uid)
	if err != nil {
		return fmt.Errorf("convert uid %s to int: %w", owner.Uid, err)
	}

	gid := 0
	if owner.Gid != "" {
		// If cannot convert will use gid 0.
		gid, _ = strconv.Atoi(owner.Gid)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open root %s: %w", dir, err)
	}
	defer root.Close()

	return filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("get relative path for %s to %s: %w", path, dir, err)
		}

		return root.Chown(rel, uid, gid)
	})
}

// GlobDirs will return any sub-directories which match the provided glob
// pattern. Example: "/tmp/*" will return all sub-directories under "/tmp/".
func GlobDirs(pattern string) ([]string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to expand glob path %q: %w", pattern, err)
	}

	ret := make([]string, 0, len(matches))
	for _, match := range matches {
		st, err := os.Stat(match)
		if err != nil {
			return nil, fmt.Errorf("failed to stat expanded path %q: %w", match, err)
		}

		if !st.IsDir() {
			continue
		}

		ret = append(ret, match)
	}

	return ret, nil
}
