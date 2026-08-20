package check_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// internalSettings are the environment variables the engine reads that an
// operator never sets.
//
// **A list, not a pattern**, and that is the point: each name here is a decision
// that this one is plumbing, made once and visible. A prefix rule would let the
// next `EARTH_GUEST_SOMETHING` in without anybody deciding, which is how the
// twenty-seven of these came to be undocumented in the first place.
var internalSettings = map[string]string{
	"EARTH_CORPUS_DIR":          "the corpus test's tree",
	"EARTH_FLEET_ATTEMPT":       "set by the fleet driver on a worker",
	"EARTH_FLEET_CAPACITY":      "set by the fleet driver on a worker",
	"EARTH_FLEET_DRIVER":        "set by the fleet driver on a worker",
	"EARTH_FLEET_REPO":          "set by the fleet driver on a worker",
	"EARTH_FLEET_RUN":           "set by the fleet driver on a worker",
	"EARTH_FLEET_SECRET":        "set by the fleet driver on a worker",
	"EARTH_FLEET_SESSION":       "set by the fleet driver on a worker",
	"EARTH_FLEET_WAIT":          "set by the fleet driver on a worker",
	"EARTH_FLEET_WORKERS":       "set by the fleet driver on a worker",
	"EARTH_FULL_TARGET":         "passed to a target's own sub-build",
	"EARTH_GUEST_ARCH":          "passed to the guest by the host",
	"EARTH_GUEST_CGROUP_PARENT": "passed to the guest by the host",
	"EARTH_GUEST_FILLS":         "passed to the guest by the host",
	"EARTH_GUEST_ID_GATE":       "passed to the guest by the host",
	"EARTH_GUEST_MEMORY_MAX":    "passed to the guest by the host",
	"EARTH_GUEST_PIDS_MAX":      "passed to the guest by the host",
	"EARTH_GUEST_ROOT":          "passed to the guest by the host",
	"EARTH_GUEST_SCRATCH":       "passed to the guest by the host",
	"EARTH_GUEST_TERMINALS":     "passed to the guest by the host",
	"EARTH_PROBE":               "marks a process as the engine's own probe",
	"EARTH_PROBE_PATH":          "marks a process as the engine's own probe",
	"EARTH_TEST_IN_USERNS":      "set by the namespace test harness on its own child",
	"EARTH_TEST_TMPFS":          "set by the namespace test harness on its own child",
	"EARTH_ENGINE_TRACE":        "the phase-0 measurement harness",
}

// Every setting an operator can set is written down.
//
// E388 caught one Earthfile option that existed only in the parser. This is the
// same defect at the scale of the whole engine: twenty-seven environment
// variables changed what a build did and **not one appeared in any document** -
// including `EARTH_ALLOW_HOST_DOCKER`, which hands a step root on the machine.
//
// The rule is that every `EARTH_*` the engine reads is either in
// `docs/native/settings.md` or in the list above with a reason. A new one is
// therefore a decision rather than an omission: whoever adds it writes a line
// somewhere, and which document they choose says whether an operator is meant to
// know.
func TestEverySettingIsDocumentedOrDeclaredInternal(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")

	// Three homes, because there are two kinds and the second has a page of its
	// own. An environment setting belongs in the native engine's reference; a
	// builtin ARG - EARTH_GIT_HASH and its relatives - is part of the language
	// and belongs in the Earthfile reference or its builtin-args page. Any of
	// them counts as discoverable, which is what this guard is about.
	var ref strings.Builder

	for _, at := range [][]string{
		{"docs", "native", "settings.md"},
		{"docs", "earthfile", "earthfile.md"},
		{"docs", "earthfile", "builtin-args.md"},
	} {
		b, err := os.ReadFile(filepath.Join(append([]string{root}, at...)...))
		if err != nil {
			t.Fatalf("a reference is not where this test expects it: %v", err)
		}

		ref.Write(b)
	}

	documented := ref.String()

	name := regexp.MustCompile(`"(EARTH_[A-Z_]+)"`)
	found := map[string]bool{}

	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not this test's problem
		}

		if fi.IsDir() && (fi.Name() == ".git" || fi.Name() == "node_modules") {
			return filepath.SkipDir
		}

		// Tests set variables to test them; that is not the engine reading a
		// setting, and a test fixture is nobody's configuration.
		if fi.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}

		b, err := os.ReadFile(p)
		if err != nil {
			return nil //nolint:nilerr // ditto
		}

		for _, m := range name.FindAllStringSubmatch(string(b), -1) {
			found[m[1]] = true
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(found) == 0 {
		t.Fatal("no settings were found at all, so this guard is checking nothing")
	}

	var undocumented []string

	for n := range found {
		// The bare name, not a backticked one: a heading may write it as
		// `EARTH_ALLOW_HOST_DOCKER=1`, which is the same setting.
		if internalSettings[n] != "" || strings.Contains(documented, n) {
			continue
		}

		undocumented = append(undocumented, n)
	}

	sort.Strings(undocumented)

	if len(undocumented) > 0 {
		t.Errorf("these change what a build does and no reader can discover them:\n  %s"+
			"\n  add each to docs/native/settings.md, or to internalSettings with a"+
			" reason if an operator never sets it",
			strings.Join(undocumented, "\n  "))
	}
}
