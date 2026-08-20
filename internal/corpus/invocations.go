// Package corpus reads what `tests/Earthfile` says about its own files.
//
// The tree drives every `.earth` file in `tests/` through a function called
// `RUN_EARTH`, and each call says which file, which target, what to pass it and
// whether it is meant to fail. That is the corpus's own account of itself, and
// two things in this repository need it: the run gate, which drives every
// invocation, and the planning sweep, which needs to know which targets are
// *meant* to be refused so the engine refusing them stops reading as work.
//
// One reader rather than two. Two would be two opinions about what the tree
// says, and the second would be the one nobody was maintaining (E477).
package corpus

import (
	"regexp"
	"strings"
)

// Invocation is one `DO +RUN_EARTH ...` call.
//
// The zero value means the line was not one.
type Invocation struct {
	// File is the `.earth` file, empty when the call drives the tree's own
	// Earthfile.
	File string
	// Target is the target to build, empty for the file's first.
	Target string
	// Extra are the arguments the tree passes, split into words.
	Extra []string
	// ShouldFail is the tree saying this target is meant to be refused.
	//
	// Seventy-odd invocations say it. Without reading it, a file whose whole
	// purpose is to be refused reads as an engine defect, and the number is
	// wrong in the direction that flatters nobody (E455).
	ShouldFail bool
	// Exec names a script the tree runs instead of building anything.
	Exec string
	// Env are variables the tree exports first, from a `--pre_command` that is
	// a single export.
	Env map[string]string
	// Pre is a `--pre_command` that is not a single export, empty otherwise.
	Pre string
}

// Named reports whether the invocation names a file and a target.
func (in Invocation) Named() bool {
	return in.File != "" || in.Target != "" || in.Exec != "" || len(in.Extra) > 0
}

// The flags of `RUN_EARTH`, read off the line rather than parsed.
//
// Regular expressions rather than a parser: what is wanted is a few flags of a
// function call written on one line, and a second Earthfile parser here would be
// a second opinion about the language (E454).
var (
	earthfileFlag = regexp.MustCompile(`--earthfile[= ]"?([^\s"]+)`)
	// Quoted or bare, and the quoted form is taken whole: a target flag may
	// carry the target's own arguments, and a pattern that stopped at the first
	// space read `+t` and dropped `--flag=value` (E470).
	targetFlag = regexp.MustCompile(`--target[= ](?:"([^"]*)"|(\S+))`)
	extraFlag  = regexp.MustCompile(`--extra_args[= ]"([^"]*)"`)
	execFlag   = regexp.MustCompile(`--exec_cmd[= ]"?([^\s"]+)`)
	failFlag   = regexp.MustCompile(`--should_fail[= ]"?(true|1)\b`)
	// A `--pre_command` a caller can honour without a shell: one
	// `export NAME=value` and nothing else.
	exportPre = regexp.MustCompile(`--pre_command="export ([A-Za-z_][A-Za-z0-9_]*)=([^"]*)"`)
	// Any `--pre_command` at all, so one that cannot be honoured is reported
	// rather than quietly dropped: an invocation run without the command that
	// set it up is a different invocation.
	anyPre = regexp.MustCompile(`--pre_command="([^"]*)"`)
)

// Invocations reads every `RUN_EARTH` a tree declares, in order.
//
// **A file named once is used until the next one.** `RUN_EARTH` copies the named
// file to `Earthfile` inside the container, so the calls after it reuse what is
// there - and a target header resets that, because a new target starts from the
// base recipe and whatever an earlier one copied is gone (E470).
//
// Read as "no file means the tree's own Earthfile", eight invocations looked for
// targets it does not have, and the harness's mistake was reported as the
// engine's.
func Invocations(src string) []Invocation {
	var (
		out  []Invocation
		file string
	)

	for _, line := range statements(src) {
		// A target header: `name:` at the start of a line.
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' &&
			strings.HasSuffix(strings.TrimSpace(line), ":") {
			file = ""

			continue
		}

		in := readInvocation(line)
		if !in.Named() {
			continue
		}

		if in.File == "" {
			in.File = file
		} else {
			file = in.File
		}

		out = append(out, in)
	}

	return out
}

// readInvocation reads one line of the tree.
func readInvocation(line string) Invocation {
	if !strings.Contains(line, "+RUN_EARTH") {
		return Invocation{}
	}

	var got Invocation

	if m := earthfileFlag.FindStringSubmatch(line); m != nil {
		got.File = m[1]
	}

	if m := targetFlag.FindStringSubmatch(line); m != nil {
		// The quoted group or the bare one, whichever matched. A target flag
		// may carry the target's own arguments - `--target="+t --flag=value"`
		// is one flag holding two things, and reading only the first word drops
		// an argument the target needs (E470).
		written := m[1]
		if written == "" {
			written = m[2]
		}

		// A flag with nothing after it names no target, which the tree writes
		// where a target is built by an argument the invocation supplies.
		if fields := strings.Fields(written); len(fields) > 0 {
			got.Target = strings.TrimPrefix(fields[0], "+")
			got.Extra = append(got.Extra, fields[1:]...)
		}
	}

	if m := extraFlag.FindStringSubmatch(line); m != nil && strings.TrimSpace(m[1]) != "" {
		got.Extra = strings.Fields(m[1])
	}

	if m := execFlag.FindStringSubmatch(line); m != nil {
		got.Exec = m[1]
	}

	if m := exportPre.FindStringSubmatch(line); m != nil {
		got.Env = map[string]string{m[1]: m[2]}
	} else if m := anyPre.FindStringSubmatch(line); m != nil {
		got.Pre = m[1]
	}

	got.ShouldFail = failFlag.MatchString(line)

	return got
}

// statements joins an Earthfile's line continuations.
//
// `DO +RUN_EARTH \` and its flags on the lines below are one command, and a
// third of the tree's invocations are written that way. Read line by line, each
// of those was a `DO` with no flags followed by fragments that mention no
// command - **a third of the corpus, driven by a default nobody wrote** (E454).
func statements(src string) []string {
	var (
		out  []string
		join strings.Builder
	)

	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimRight(line, " \t")

		if strings.HasSuffix(trimmed, "\\") {
			join.WriteString(strings.TrimSuffix(trimmed, "\\"))
			join.WriteString(" ")

			continue
		}

		join.WriteString(trimmed)
		out = append(out, join.String())
		join.Reset()
	}

	if join.Len() > 0 {
		out = append(out, join.String())
	}

	return out
}

// MeantToFail is the set of `file+target` the tree drives with `--should_fail`.
//
// A target whose whole purpose is to be refused: `save-artifact-dont-overwrite`
// has six, `builtin-args-invalid-default` one, and there are seventy-odd
// invocations saying so. The planning sweep counts the engine refusing them as
// work left to do, which is **a number that cannot reach zero** - the same shape
// as the run gate's own reason for reading this flag (E455, E477).
//
// Keyed on file and target together because a target name alone is not unique
// across a hundred and sixteen files, and `test` is the commonest name in the
// tree.
func MeantToFail(src string) map[string]bool {
	out := map[string]bool{}

	for _, in := range Invocations(src) {
		if in.ShouldFail && in.File != "" && in.Target != "" {
			out[in.File+"+"+in.Target] = true
		}
	}

	return out
}
