package exec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// **A setting the guest reads and this backend does not forward is silent.**
//
// The environment crosses into the VM through `container exec -e` and nothing
// else: `cmd.Env` configures the host process that speaks to the container
// service. A variable left out is not an error - the guest uses its default, the
// operator sees the value they set having no effect, and the symptom surfaces
// somewhere else entirely.
//
// It has happened three times. `EnvIdle` was forwarded only after a developer
// found that setting it against a VM sandbox changed nothing (E555). `EnvStepNet`
// was found the same way, while chasing a step that could not reach its own
// gateway. This is the guard, so the fourth one is a failing test rather than an
// afternoon.
//
// Excused rather than forwarded is a fine answer - most of these are set *by*
// this backend, or belong to a step rather than the guest - but it has to be an
// answer somebody wrote down.
func TestEveryGuestSettingIsForwardedOrExcused(t *testing.T) {
	t.Parallel()

	excused := map[string]string{
		"EARTH_GUEST_ROOT":          "set by this backend, to its own path",
		"EARTH_EXPORT_DIR":          "set by this backend",
		"EARTH_GUEST_SCRATCH":       "set by this backend",
		"EARTH_GUEST_FAST":          "set by this backend",
		"EARTH_GUEST_IDLE":          "set by this backend",
		"EARTH_GUEST_FILL_SOCKET":   "set by this backend where a fleet needs one",
		"EARTH_STEP_NETNS":          "per step, put in the step's own environment by the guest",
		"EARTH_STEP_SHIM":           "per step, set by the guest",
		"EARTH_STEP_TRACE_FD":       "per step, set by the guest",
		"EARTH_STEP_TRACE_PIN":      "per step, set by the guest",
		"EARTH_STEP_USER":           "per step, set by the guest",
		"EARTH_STEP_HOME":           "per step, set by the guest",
		"EARTH_GUEST_OWNS_MACHINE":  "the guest decides this from what it is running on",
		"EARTH_GUEST_CGROUP_PARENT": "the guest finds its own cgroup",
		"EARTH_GUEST_DENTRY_LIMIT":  "a guest-side tuning nobody has needed to set from outside",
		"EARTH_ALLOW_LEAKED_SECRETS": "refused deliberately: a safety check must not be" +
			" switchable from a build's environment",
		"EARTH_STEP_KEEPCAPS": "per step, set by the guest; not a documented setting",
	}

	forwarded := readForwarded(t)

	for name, where := range guestSettings(t) {
		if forwarded[name] {
			continue
		}

		why, ok := excused[name]
		if !ok {
			t.Errorf("%s (%s) is read by the guest and is neither forwarded by this"+
				"\n  backend nor excused - setting it against a VM sandbox would do"+
				"\n  nothing, silently. Forward it, or say here why it need not be.",
				name, where)

			continue
		}

		if why == "" {
			t.Errorf("%s is excused with no reason given", name)
		}
	}
}

// guestSettings is every EARTH_ variable the guest reads, by constant.
//
// **Both packages the guest is built from.** engine/guestd is the agent and
// reads settings of its own - EARTH_STORE_FREE, EARTH_COLLECT_BUDGET - and a
// scan of engine/guest alone let this backend drop both while the Linux one
// carried them: setting either against a VM sandbox did nothing.
func guestSettings(t *testing.T) map[string]string {
	t.Helper()

	out, _ := scanGuestSettings(t)

	return out
}

// scanGuestSettings is guestSettings, plus each setting's constant by its
// qualified name (`guestd.EnvStoreFree`), which is how this backend names them.
func scanGuestSettings(t *testing.T) (map[string]string, map[string]string) {
	t.Helper()

	out, consts := map[string]string{}, map[string]string{}

	for _, pkg := range []string{"guest", "guestd"} {
		scanSettings(t, pkg, out, consts)
	}

	if len(out) < 10 {
		t.Fatalf("found %d guest settings, which is too few to be the whole list", len(out))
	}

	return out, consts
}

// scanSettings adds every `Env* = "EARTH_..."` constant in package pkg to out
// (value -> file) and consts (`pkg.Name` -> value).
func scanSettings(t *testing.T, pkg string, out, consts map[string]string) {
	t.Helper()

	dir := filepath.Join("..", pkg)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
				return true
			}

			if !strings.HasPrefix(spec.Names[0].Name, "Env") {
				return true
			}

			lit, ok := spec.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}

			value, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.HasPrefix(value, "EARTH_") {
				return true
			}

			out[value] = filepath.Join(pkg, e.Name())
			consts[pkg+"."+spec.Names[0].Name] = value

			return true
		})
	}
}

// readForwarded is every variable this backend names in its `-e` arguments.
//
// Read from the source rather than by running it: the list is built from
// constants and function calls, so a value is not available without a VM, but
// which *names* appear is exactly what this test is about.
func readForwarded(t *testing.T) map[string]bool {
	t.Helper()

	b, err := os.ReadFile("apple_darwin.go")
	if err != nil {
		t.Fatal(err)
	}

	names, consts := scanGuestSettings(t)
	out := map[string]bool{}

	for name := range names {
		if strings.Contains(string(b), `"`+name+`=`) || strings.Contains(string(b), name+"+\"=") {
			out[name] = true
		}
	}

	// The constants are also named through their packages, which the literal
	// search above misses. Found rather than listed: a hand-kept list of which
	// constants this file mentions is the same drift this whole test exists to
	// prevent, one level up. Both packages, because `guest\.` alone never saw
	// `guestd.EnvCacheAddr` forwarded.
	for _, ref := range regexp.MustCompile(`\bguestd?\.Env\w+`).FindAllString(string(b), -1) {
		if value, ok := consts[ref]; ok {
			out[value] = true
		}
	}

	return out
}
