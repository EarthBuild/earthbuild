package ignore_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ignore"
)

// TestTheRecommendedGoModuleExclusionsMatchWhatWasMeasured.
//
// `CACHE --portable-except` takes patterns in this syntax, and the setting
// recommended for `/go/pkg/mod` was derived from a measurement rather than from
// Go's documentation: two module caches filled at different roots agreed on
// 95,282 of 95,283 paths, and the corrected list is the set of paths that could
// not be shared plus the transient ones (E-F4).
//
// Every path below is a real one taken from that corpus, and the two directions
// are equally load-bearing. A pattern that fails to exclude a mutable path
// shares a file whose content depends on when it was fetched; a pattern that
// excludes a portable one refuses to share a file it safely could, which is
// silent and costs the whole point of the flag.
//
// The eleven `.lock` files inside extracted module trees are the case that
// caught the first draft out: `**/*.lock` matched them, and they are
// third-party *source* - as immutable as the code beside them.
func TestTheRecommendedGoModuleExclusionsMatchWhatWasMeasured(t *testing.T) {
	t.Parallel()

	const recommended = "cache/lock," +
		"cache/download/**/*.lock," +
		"cache/download/**/*.partial," +
		"cache/download/sumdb/*/lookup"

	m, err := ignore.Patterns(recommended)
	if err != nil {
		t.Fatalf("the documented setting for /go/pkg/mod does not parse: %v", err)
	}

	for _, c := range []struct {
		path string
		out  bool
		why  string
	}{
		{"cache/lock", true, "go's own lock on the download cache"},
		{"cache/download/gotest.tools/v3/@v/v3.5.2.lock", true,
			"a per-module download lock"},
		{"cache/download/cloud.google.com/go/@v/v0.26.0.partial", true,
			"a download that had not finished"},
		{"cache/download/sumdb/sum.golang.org/lookup/github.com/containerd/fuse-overlayfs-snapshotter@v1.0.2", true,
			"it carries the checksum database's signed tree head at lookup time," +
				" which is the one path in 95,283 that differed"},

		{"cache/download/cloud.google.com/go/@v/v0.26.0.zip", false,
			"the module zip, whose hash is in go.sum"},
		{"cache/download/cloud.google.com/go/@v/v0.26.0.ziphash", false, "and its hash"},
		{"cache/download/sumdb/sum.golang.org/tile/8/1/967.p/104", false,
			"a partial tile carries its own width in its name, so it is as" +
				" immutable as a full one"},
		{"github.com/in-toto/attestation@v1.2.0/rust/Cargo.lock", false,
			"third-party source inside an extracted module tree"},
		{"github.com/onsi/gomega@v1.39.1/docs/Gemfile.lock", false, "likewise"},
		{"gvisor.dev/gvisor@v0.0.0-20240916094835-a174eb65023f/pkg/sentry/fsimpl/lock", false,
			"a directory that happens to be called lock"},
	} {
		if got := m.Excludes(c.path); got != c.out {
			t.Errorf("Excludes(%q) = %v, want %v\n  %s", c.path, got, c.out, c.why)
		}
	}
}

// TestAnEmptyPatternListExcludesNothing. The strongest form of the claim, and
// the recommended setting for a content-addressed store mounted at its own
// root. It must parse, and it must match nothing at all.
func TestAnEmptyPatternListExcludesNothing(t *testing.T) {
	t.Parallel()

	m, err := ignore.Patterns("")
	if err != nil {
		t.Fatalf("the strongest form of the claim does not parse: %v", err)
	}

	for _, p := range []string{"a", "a/b", "cache/lock", ".hidden"} {
		if m.Excludes(p) {
			t.Errorf("Excludes(%q) with no patterns, so a cache claimed wholly"+
				" portable shares nothing", p)
		}
	}
}

// TestAMalformedPatternIsRefused. A pattern nobody can parse was meant to
// exclude something, and carrying on shares it - which is the direction that
// corrupts rather than the one that is merely slow.
func TestAMalformedPatternIsRefused(t *testing.T) {
	t.Parallel()

	if _, err := ignore.Patterns("["); err == nil {
		t.Error("a malformed exclusion parsed, so a path meant to stay local" +
			" would be offered to other machines")
	}
}

// TestWhitespaceAndEmptyElementsAreTolerated. `'a, b,'` is what a human writes.
func TestWhitespaceAndEmptyElementsAreTolerated(t *testing.T) {
	t.Parallel()

	m, err := ignore.Patterns(" cache/lock , tmp/** ,")
	if err != nil {
		t.Fatalf("a list written the way a person writes one: %v", err)
	}

	if !m.Excludes("cache/lock") || !m.Excludes("tmp/a/b") {
		t.Error("surrounding spaces made the patterns miss")
	}

	if m.Excludes("cache/other") {
		t.Error("an empty trailing element matched something")
	}
}
