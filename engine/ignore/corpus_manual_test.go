package ignore_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ignore"
)

// TestAgainstARealModuleCache walks an actual /go/pkg/mod, if one has been left
// where E-F4 put it, and reports what the recommended setting excludes.
//
// Skipped everywhere else: a guard that needs 1.4 GiB of somebody's disk is not
// a guard. It exists so that the numbers in E-F4 can be re-derived rather than
// trusted, which is the whole argument of that experiment applied to itself.
func TestAgainstARealModuleCache(t *testing.T) {
	t.Parallel()

	root := os.Getenv("EARTH_MODULE_CACHE_CORPUS")
	if root == "" {
		t.Skip("set EARTH_MODULE_CACHE_CORPUS to a populated GOMODCACHE")
	}

	m, err := ignore.Patterns("cache/lock,cache/download/**/*.lock," +
		"cache/download/**/*.partial,cache/download/sumdb/*/lookup")
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}

	err = filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil || rel == "." {
			return nil //nolint:nilerr // a path we cannot place is not ours to judge
		}

		counts["total"]++

		if !m.Excludes(filepath.ToSlash(rel)) {
			return nil
		}

		counts["excluded"]++

		switch {
		case strings.Contains(rel, "sumdb"):
			counts["sumdb"]++
		case strings.HasSuffix(rel, ".lock"), rel == "cache/lock":
			counts["lock"]++
		default:
			counts["other"]++
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("total=%d excluded=%d (sumdb=%d lock=%d other=%d)",
		counts["total"], counts["excluded"], counts["sumdb"],
		counts["lock"], counts["other"])

	if counts["other"] != 0 {
		t.Errorf("%d excluded paths are neither sumdb nor a lock, so the"+
			" recommended setting refuses to share something unexplained",
			counts["other"])
	}
}
