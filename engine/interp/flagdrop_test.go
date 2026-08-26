package interp_test

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// command is how to write one command, with and without a flag.
type command struct {
	name  string
	opts  any
	body  func(flag string) string
	setup string // lines the command needs above it
	// deps are whole targets the command refers to, appended after main.
	//
	// `FROM --build-arg` and `--pass-args` mean nothing when the FROM names an
	// *image*, which is what the sweep wrote at first - so three flags were
	// counted as dropped by a template that could not show them either way
	// (E437).
	deps string
}

// dependency is a target the sweep's commands can refer to.
//
// It takes an argument and uses it, and saves an artifact: a flag that passes a
// build argument to a target that ignores it changes nothing, and would read as
// a flag nobody honoured.
const dependency = "\ndep:\n    FROM alpine:3.22\n    ARG x=0\n" +
	"    RUN echo $x > /f\n    SAVE ARTIFACT /f x\n"

// commands are the constructs whose flags this sweep drives.
//
// The template is per command because a flag has to appear in something that
// parses: `RUN --x make` and `COPY --x a b` are the shapes, and a generated line
// that does not parse tests the parser rather than the flag.
func commands() []command {
	return []command{
		{name: "RUN", opts: cmdopts.Run{}, body: func(f string) string {
			return "    RUN " + f + " make\n"
		}},
		{
			name: "COPY", opts: cmdopts.Copy{},
			// An artifact rather than a path in the build context: the sweep
			// has no filesystem, so `COPY a b` refused for a missing `a` and
			// took all ten of COPY's flags with it as inconclusive.
			body: func(f string) string { return "    COPY " + f + " +dep/x b\n" },
			deps: dependency,
		},
		{
			name: "FROM", opts: cmdopts.From{},
			body: func(f string) string { return "    FROM " + f + " +dep\n" },
			// A target rather than an image, and one that *uses* an argument:
			// a build argument passed to a target that ignores it changes
			// nothing, and would look like a flag nobody read.
			//
			// Not called `base`, which is a reserved target name - the first
			// version was, and every FROM flag came back inconclusive with the
			// parser's complaint about it.
			deps: dependency,
		},
		{name: "SAVE ARTIFACT", opts: cmdopts.SaveArtifact{}, body: func(f string) string {
			return "    SAVE ARTIFACT " + f + " /x\n"
		}},
		{name: "SAVE IMAGE", opts: cmdopts.SaveImage{}, body: func(f string) string {
			return "    SAVE IMAGE " + f + " img:latest\n"
		}},
		{name: "GIT CLONE", opts: cmdopts.GitClone{}, body: func(f string) string {
			return "    GIT CLONE " + f + " https://example.invalid/r.git /dst\n"
		}},
		// A CACHE line applies to the steps *after* it, so the template has one:
		// without a following RUN the mount reaches no node, and every option
		// of the command looks dropped. The sweep's first run said exactly that
		// about four options this engine provides.
		{name: "CACHE", opts: cmdopts.Cache{}, body: func(f string) string {
			return "    CACHE " + f + " /c\n    RUN make\n"
		}},
		{name: "ARG", opts: cmdopts.Arg{}, body: func(f string) string {
			return "    ARG " + f + " v=1\n"
		}},
		// BUILD was absent, and its absence cost a whole test.
		//
		// `TestBuildFlagsAreRefusedNotIgnored` held BUILD's two refused flags by
		// hand; both were accepted, in E476 and E484, and the list emptied. The
		// note left where it stood said this sweep watches BUILD's flags now -
		// and it did not, because BUILD was never in this list (E484).
		//
		// A dependency that *uses* an argument, for the reason FROM's entry
		// gives: a build argument passed to a target that ignores it changes
		// nothing, and would read as a flag nobody honoured.
		{
			name: "BUILD", opts: cmdopts.Build{},
			body: func(f string) string { return "    BUILD " + f + " +dep\n" },
			deps: dependency,
		},
	}
}

// A flag is honoured, or refused by name. Never dropped.
//
// The failure this sweep is named for: `RUN --mount=...,sharing=locked` was
// parsed and discarded, so the build ran without the lock and said nothing
// (E435). The fix was one construct's fields; **the class is every flag the
// parser accepts**, and the class is what a sweep can hold.
//
// Three outcomes are acceptable and one is not:
//
//   - the plan changes: the flag reached something;
//   - the plan is refused and the message names the flag: an honest gap;
//   - the plan is refused for something else - inconclusive, because this sweep
//     generated a line that could not build for an unrelated reason, and
//     counting that as either answer would be inventing evidence.
//
// A flag that changes nothing and is not named is **dropped**: the author wrote
// it, the parser took it, and nothing else in the engine ever saw it.
func TestNoFlagIsSilentlyDropped(t *testing.T) {
	t.Parallel()

	var dropped, honoured, refused, unclear []string

	for _, c := range commands() {
		base, baseErr := planOf(c, "")

		typ := reflect.TypeOf(c.opts)

		for field := range typ.Fields() {
			flag := field.Tag.Get("long")
			if flag == "" {
				continue
			}

			where := c.name + " --" + flag

			with, err := planOf(c, writtenAs(field, flag))
			switch {
			case baseErr != nil:
				// Asked first: if the template does not plan without the flag,
				// nothing can be concluded about the flag - and the reason
				// belongs in the report, because it is the sweep's own bug and
				// not the engine's. Asked last, it was reached by nothing and
				// four flags were reported as bare names with no cause (E437).
				unclear = append(unclear, where+": template: "+firstLine(baseErr.Error()))

			case err != nil && strings.Contains(err.Error(), flag):
				refused = append(refused, where)

			case err != nil:
				unclear = append(unclear, where+": "+firstLine(err.Error()))

			case with != base:
				honoured = append(honoured, where)

			default:
				dropped = append(dropped, where)
			}
		}
	}

	sort.Strings(dropped)

	t.Logf("%d flags honoured, %d refused by name, %d inconclusive, %d dropped\n"+
		"  dropped: %s\n  inconclusive: %s",
		len(honoured), len(refused), len(unclear), len(dropped),
		strings.Join(dropped, ", "), strings.Join(unclear, ", "))

	// Compared against a named list rather than counted.
	//
	// A count would move for two different reasons - a flag fixed, and a
	// template improved so a flag stops being miscounted - and a ratchet that
	// moves for reasons other than the one it measures is a number nobody can
	// read. The list makes both directions explicit: a new drop fails here by
	// name, and removing one is an edit somebody makes on purpose.
	for _, got := range dropped {
		if !slices.Contains(knownDropped, got) {
			t.Errorf("%s is parsed and reaches nothing, and is not on the known"+
				" list\n  honour it, refuse it by name, or add it here with a"+
				" reason", got)
		}
	}

	for _, want := range knownDropped {
		if !slices.Contains(dropped, want) {
			t.Errorf("%s is on the known-dropped list and is no longer dropped"+
				"\n  take it off the list, so the list keeps meaning what it says",
				want)
		}
	}
}

// knownDropped are the flags this sweep sees reach nothing, each with why.
//
// **A tolerated finding nobody has checked is indistinguishable from a bug
// nobody has fixed**, so every entry says which of three things it is:
//
//   - *deliberate*: the flag asks for what this engine already does, and the
//     reason is written where the flag is accepted. A capture records uid, gid,
//     timestamps and symlinks as they are, so the three SAVE ARTIFACT flags have
//     nothing to change; a cache hint may not change results (I5), so ignoring
//     one is safe by definition.
//   - *harness*: the sweep cannot show it. `--pass-args` needs arguments to
//     pass, `--if-exists` needs a source that is missing, `ARG --required` needs
//     an argument with no default, `ARG --global` needs a second target.
//   - *defect*: parsed, unconsidered, reaching nothing. There are none left
//     here; `CACHE --chmod` and `RUN --push` were the two, and both are fixed
//     (E436).
//
// The value of the list is the fourth case it makes impossible: a flag that
// stops being read fails this test by name, which is what happened when the
// recording of `SAVE IMAGE --push` was deleted to check (E437).
var knownDropped = []string{
	// `ARG --global` left this list by being *refused*: the sweep writes it
	// inside a target, and a global declared there is now an error rather than
	// a flag that decided nothing (E461).
	// `ARG --required` left this list by being *refused*: the sweep writes it
	// with a default, and the two contradict each other (E470).
	// Both grant a *permission* to a referenced target, and this engine refuses
	// privileged execution wherever it appears - so the grant is never taken up
	// and there is nothing for the flag to change. Accepted rather than refused
	// since E476, for the reason written at the refusal it replaced: refusing
	// the grant while refusing the thing granted is two answers to one
	// question. The half that keeps this safe is
	// `TestAllowPrivilegedDoesNotMakeAStepPrivileged`.
	"BUILD --allow-privileged",
	"COPY --allow-privileged",
	"FROM --allow-privileged",
	// deliberate: asks for a faster route to the answer this engine already
	// gives, and a cache hint may not change results (I5). The same terms as
	// `SAVE IMAGE --cache-hint` below, and the same terms this was refused on
	// until the corpus drove it expecting a build (E484).
	"BUILD --auto-skip",
	// harness: no arguments in scope to pass, as for COPY and FROM.
	"BUILD --pass-args",
	"COPY --keep-ts",                      // deliberate: interp.go, a capture keeps them
	"COPY --pass-args",                    // harness: no arguments in scope to pass
	"FROM --pass-args",                    // harness: as above
	"RUN --raw-output",                    // deliberate: how output is printed
	"SAVE ARTIFACT --keep-own",            // deliberate: a layer records uid and gid
	"SAVE ARTIFACT --keep-ts",             // deliberate: a capture keeps timestamps
	"SAVE ARTIFACT --symlink-no-follow",   // deliberate: a layer holds a symlink as one
	"SAVE IMAGE --cache-from",             // deliberate: a hint may not change results (I5)
	"SAVE IMAGE --cache-hint",             // deliberate: as above
	"SAVE IMAGE --insecure",               // deliberate: governs a push, and this engine does not push
	"SAVE IMAGE --without-earthly-labels", // deliberate: this engine adds no such labels
}

// writtenAs is the flag as it appears on a command line.
//
// A boolean is written bare, which is also the form that caught the bare
// `readonly` (E435). Everything else needs a value, and the value has to be one
// the flag would accept - a plausible one, so that a refusal means the flag and
// not the value.
func writtenAs(f reflect.StructField, flag string) string {
	if f.Type.Kind() == reflect.Bool {
		return "--" + flag
	}

	value, special := map[string]string{
		"mount":    "type=cache,target=/c",
		"secret":   "TOKEN=x",
		"network":  "none",
		"platform": "linux/amd64",
		"chmod":    "0755",
		"chown":    "root:root",
		// A value the flag does not already have: `--sharing=locked` is the
		// default, so a plan with it and one without are the same plan, and the
		// sweep called a provided option dropped.
		"sharing":   "shared",
		"build-arg": "x=1",
		"branch":    "main",
		"id":        "n",
		"push":      "",
	}[flag]
	if !special {
		value = "x"
	}

	return "--" + flag + "=" + value
}

// planOf plans a one-target Earthfile containing the command, and fingerprints
// everything the plan says.
//
// The graph's root identity alone is not enough, and the sweep's first run said
// so: `SAVE ARTIFACT --if-exists` sets a field on an *artifact*, which is beside
// the graph rather than in it, so a flag that was read looked dropped. An
// observable narrower than the thing being observed reports absence it cannot
// see (E436).
func planOf(c command, flag string) (string, error) {
	src := versioned + "\nmain:\n    FROM alpine:3.22\n" + c.setup + c.body(flag) + c.deps

	p, err := interp.Build(src, testMain)
	if err != nil {
		return "", err
	}

	out := []string{p.Graph.Root.ID().String()}

	// Field by field, and node *identities* rather than nodes.
	//
	// `%+v` over these structs was the first version, and both hold an
	// `*ir.Node`: the verb prints a pointer as an address, addresses differ
	// between two calls to Build, and so every flag of every command that
	// produces an artefact or an image came back "honoured" - including one
	// deliberately broken to check (E437). **A fingerprint containing an address
	// is a fingerprint that always differs**, which is the same nothing as one
	// that never does.
	for _, a := range p.Artifacts {
		out = append(out, fmt.Sprintf("artifact %s %s %s %v %s",
			a.Path, a.Name, a.LocalDest, a.IfExists, idOf(a.From)))
	}

	for _, i := range p.Images {
		out = append(out, fmt.Sprintf("image %s %v %s %+v",
			i.Ref, i.Push, idOf(i.From), i.Config))
	}

	return strings.Join(out, "\n"), nil
}

// idOf names a node without printing where it happens to live.
func idOf(n *ir.Node) string {
	if n == nil {
		return "-"
	}

	return n.ID().String()
}
