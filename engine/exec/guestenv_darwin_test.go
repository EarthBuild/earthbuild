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

// guestSettings is every EARTH_ variable engine/guest reads, by constant.
func guestSettings(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}
	dir := filepath.Join("..", "guest")

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

			out[value] = e.Name()

			return true
		})
	}

	if len(out) < 10 {
		t.Fatalf("found %d guest settings, which is too few to be the whole list", len(out))
	}

	return out
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

	names := guestSettings(t)
	out := map[string]bool{}

	for name := range names {
		if strings.Contains(string(b), `"`+name+`=`) || strings.Contains(string(b), name+"+\"=") {
			out[name] = true
		}
	}

	// The constants are also named through the guest package, which the literal
	// search above misses. Found rather than listed: a hand-kept list of which
	// constants this file mentions is the same drift this whole test exists to
	// prevent, one level up.
	for _, ref := range regexp.MustCompile(`guest\.Env\w+`).FindAllString(string(b), -1) {
		out[envValueOf(t, ref)] = true
	}

	return out
}

// envValueOf maps `guest.EnvFoo` to the string it holds.
func envValueOf(t *testing.T, ref string) string {
	t.Helper()

	want := strings.TrimPrefix(ref, "guest.")

	dir := filepath.Join("..", "guest")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			continue
		}

		found := ""

		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || spec.Names[0].Name != want || len(spec.Values) != 1 {
				return true
			}

			if lit, ok := spec.Values[0].(*ast.BasicLit); ok {
				found, _ = strconv.Unquote(lit.Value)
			}

			return true
		})

		if found != "" {
			return found
		}
	}

	return want
}
