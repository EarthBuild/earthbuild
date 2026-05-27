package inputgraph

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/stretchr/testify/require"
)

// newTestConsole returns a no-op console suitable for unit tests.
func newTestConsole() conslogging.ConsoleLogger {
	return conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)
}

// testdataCacheMissReason is the path to the testdata fixture used by the hash
// log tests.
const testdataCacheMissReason = "./testdata/cache-miss-reason"

// labelSet returns the set of distinct Label values in a HashLog.
func labelSet(log []HashInput) map[string]struct{} {
	s := make(map[string]struct{}, len(log))
	for _, e := range log {
		s[e.Label] = struct{}{}
	}

	return s
}

// detailsForLabel returns all Detail values for entries with the given label.
func detailsForLabel(log []HashInput, label string) []string {
	var out []string

	for _, e := range log {
		if e.Label == label {
			out = append(out, e.Detail)
		}
	}

	return out
}

// TestHashLogPopulatedForARG verifies that ARG inputs are recorded in the
// hash log with the expanded value.
func TestHashLogPopulatedForARG(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	target := domain.Target{
		LocalPath: testdataCacheMissReason,
		Target:    "simple-arg",
	}

	hash, stats, err := HashTarget(context.Background(), HashOpt{
		Console: newTestConsole(),
		Target:  target,
	})

	r.NoError(err)
	r.NotEmpty(hash)
	r.NotEmpty(stats.HashLog, "HashLog should be populated")

	labels := labelSet(stats.HashLog)
	r.Contains(labels, "ARG", "expected an ARG entry in the hash log")

	argDetails := detailsForLabel(stats.HashLog, "ARG")
	r.NotEmpty(argDetails)

	// The default value "hello" should appear in the detail.
	r.True(slices.Contains(argDetails, "MESSAGE=hello"),
		"expected ARG entry 'MESSAGE=hello' in hash log, got: %v", argDetails)
}

// TestHashLogPopulatedForLETandSET verifies that LET and SET inputs are
// recorded in the hash log.
func TestHashLogPopulatedForLETandSET(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	target := domain.Target{
		LocalPath: testdataCacheMissReason,
		Target:    "with-let",
	}

	hash, stats, err := HashTarget(context.Background(), HashOpt{
		Console: newTestConsole(),
		Target:  target,
	})

	r.NoError(err)
	r.NotEmpty(hash)
	r.NotEmpty(stats.HashLog)

	labels := labelSet(stats.HashLog)
	r.Contains(labels, "LET", "expected a LET entry in the hash log")
	r.Contains(labels, "SET", "expected a SET entry in the hash log")

	letDetails := detailsForLabel(stats.HashLog, "LET")
	r.NotEmpty(letDetails)
	r.Contains(letDetails[0], "x=initial")

	setDetails := detailsForLabel(stats.HashLog, "SET")
	r.NotEmpty(setDetails)
	r.Contains(setDetails[0], "x=updated")
}

// TestHashLogContainsDepTarget verifies that when a target depends on another
// local target via BUILD, the dependency is recorded in the hash log.
func TestHashLogContainsDepTarget(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	target := domain.Target{
		LocalPath: testdataCacheMissReason,
		Target:    "with-dep",
	}

	hash, stats, err := HashTarget(context.Background(), HashOpt{
		Console: newTestConsole(),
		Target:  target,
	})

	r.NoError(err)
	r.NotEmpty(hash)
	r.NotEmpty(stats.HashLog)

	labels := labelSet(stats.HashLog)
	r.Contains(labels, "dep target", "expected a 'dep target' entry for the BUILD dependency")

	depDetails := detailsForLabel(stats.HashLog, "dep target")
	r.NotEmpty(depDetails)

	// The detail should mention the dependency target canonical name.
	r.True(
		slices.ContainsFunc(depDetails, func(d string) bool { return containsSubstr(d, "simple-arg") }),
		"expected dep target entry mentioning '+simple-arg', got: %v", depDetails,
	)
}

// TestHashLogChangesWithARGOverride verifies that overriding an ARG produces a
// different hash AND records the override in the hash log.
func TestHashLogChangesWithARGOverride(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	ctx := context.Background()
	cons := newTestConsole()

	target := domain.Target{
		LocalPath: testdataCacheMissReason,
		Target:    "simple-arg",
	}

	// Hash with default ARG value.
	hashDefault, statsDefault, err := HashTarget(ctx, HashOpt{Console: cons, Target: target})
	r.NoError(err)
	r.NotEmpty(hashDefault)

	// Hash with an overriding build arg.
	overriding, err := variables.ParseCommandLineArgs([]string{"MESSAGE=world"})
	r.NoError(err)

	hashOverride, statsOverride, err := HashTarget(ctx, HashOpt{
		Console:        cons,
		Target:         target,
		OverridingVars: overriding,
	})
	r.NoError(err)
	r.NotEmpty(hashOverride)

	// The hashes must differ.
	r.NotEqual(hashDefault, hashOverride, "override should produce a different hash")

	// The override run must have a "build arg" entry in the log.
	r.NotEmpty(statsOverride.HashLog)

	labels := labelSet(statsOverride.HashLog)
	r.Contains(labels, "build arg", "expected a 'build arg' entry for the override")

	// The default run must NOT have a "build arg" entry (no overrides given).
	defaultLabels := labelSet(statsDefault.HashLog)
	_, hasBuildArg := defaultLabels["build arg"]
	r.False(hasBuildArg, "expected no 'build arg' entry when no override is given")
}

// TestHashLogMultipleARGs verifies that all ARG declarations for a target are
// individually recorded in the hash log.
func TestHashLogMultipleARGs(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	target := domain.Target{
		LocalPath: testdataCacheMissReason,
		Target:    "multi-arg",
	}

	hash, stats, err := HashTarget(context.Background(), HashOpt{
		Console: newTestConsole(),
		Target:  target,
	})

	r.NoError(err)
	r.NotEmpty(hash)
	r.NotEmpty(stats.HashLog)

	argDetails := detailsForLabel(stats.HashLog, "ARG")
	r.GreaterOrEqual(len(argDetails), 2, "expected at least two ARG entries (FIRST and SECOND)")

	detailsSet := make(map[string]struct{}, len(argDetails))
	for _, d := range argDetails {
		detailsSet[d] = struct{}{}
	}

	r.Contains(detailsSet, "FIRST=one")
	r.Contains(detailsSet, "SECOND=two")
}

// containsSubstr reports whether substr appears within s.
func containsSubstr(s, substr string) bool {
	for i := range len(s) - len(substr) + 1 {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}

	return false
}
