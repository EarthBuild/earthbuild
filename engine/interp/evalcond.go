package interp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Result is what running a command in the build environment produced.
//
// Both halves are needed and by different callers: a condition reads the exit
// status, and a `$(...)` substitution reads the output. One seam rather than
// two, because it is one mechanism - running a command on the filesystem the
// recipe has built up to that line - and a second entry point would be a second
// thing to keep correct.
type Result struct {
	// Exit is the command's status. Zero is success, and for a condition it is
	// the answer.
	Exit int
	// Output is what the command wrote to its output streams.
	Output string
}

// Commands runs a command that the plan cannot answer on its own.
//
// It is handed the condition as written, the node whose filesystem it must run
// against, and where the IF is - and it returns which branch to take. An error
// means the condition could not be answered, which is not the same as answering
// false: a condition that could not be run must stop the build rather than
// quietly select the ELSE.
//
// The seam exists because interpretation and execution are separate layers and
// stay that way. Calling into an executor from the interpreter would invert
// them; a function supplied by the caller keeps the dependency pointing the
// right way, and lets everything about *which* conditions get evaluated be
// tested without a sandbox.
// A probe observes the build state, and *where* it observes from is part of
// that state: `WORKDIR /var/app` then `SAVE IMAGE app:$(cat version)` reads a
// file the line above put in /var/app, and running at `/` looked for one the
// Earthfile never mentions.
type Commands func(cmd []string, base *ir.Node, dir, where string) (Result, error)

// WithCommands supplies the runner for what the interpreter cannot work out on
// its own: a condition it cannot decide, and a `$(...)` it cannot expand.
//
// Without one, such a condition is refused by name, which is what a plan-only
// caller wants: `earthbuild plan` and the corpus produce a graph without
// running anything, and neither should start a sandbox behind the caller's
// back to do it.
func WithCommands(fn Commands) Option {
	return func(o *options) { o.commands = fn }
}

// ErrNoRunner says a value cannot be known without running something.
//
// A kind of ErrNotProvided, so a caller that treats every missing prerequisite
// alike still does the right thing, and one that wants to *supply* a runner can
// tell this apart from a missing secret. The plan says so rather than guessing:
// a condition evaluated by assumption is a branch taken for a reason nobody
// recorded (I5).
var ErrNoRunner = fmt.Errorf(
	"this value is only known by running something: %w", ErrNotProvided)

// ErrNotProvided marks a plan that needs something its caller did not supply -
// somewhere to run a command, somewhere to fetch a repository from, a value for
// a `--required` ARG, a secret.
//
// The list grew by reading the corpus report: the argument and secret cases were
// refused as *invalid input* for want of this wrapper, and between them they
// were more than half of that bucket - 91 targets from 81 causes, against 38
// from 34 once they moved. A section headed "verify these are right" is not read
// when four fifths of it is one thing that is not wrong (E151).
//
// The family exists because the work list is read to decide what to build next,
// and a construct that is finished but unavailable to a plan-only caller has no
// business at the top of it. Counting these as missing features had them filling
// it. ErrNoRunner is the case of "must run something" and wraps this; a fetch
// this caller declined to make is the other.
var ErrNotProvided = errors.New("this plan needs something the caller did not provide")

// evaluate answers a condition the plan could not decide.
//
// Green paper §3.4a: where a condition requires evaluation in a sandbox, the
// graph is not fully known in advance. This is the point at which that becomes
// true - the interpreter stops being a pure function of the source and the
// arguments, and the untaken branch is decided by something that ran.
//
// Prediction is not here yet. When it arrives it changes *when* the work
// starts, never which branch is taken (I5): this call remains the authority on
// the answer.
// ErrNoRunner is what a plan gives back when the answer exists only by running
// something and the caller supplied nowhere to run it.
//
// Typed rather than a message to read, because it is a different kind of number
// from an unimplemented construct and adding the two together overstates the
// work left: `LET v = $(cat version)` is finished, and a caller that plans
// without a sandbox - the corpus does exactly that - simply cannot be given an
// answer. Counting those as missing features had them filling the top of the
// list of what to build next.
func (p *Plan) evaluate(cond []string, base *ir.Node, dir, where string) (bool, error) {
	if p.opt.commands == nil {
		return false, fmt.Errorf(
			"IF at %s needs to run %q to decide it: %w"+
				"\n  the native engine decides conditions over build arguments, not commands"+
				"\n  to build this now, use --engine=buildkit",
			where, strings.Join(cond, " "), ErrNoRunner)
	}

	res, err := p.opt.commands(cond, base, dir, where)
	if err != nil {
		return false, fmt.Errorf("IF at %s: evaluating %q: %w", where, strings.Join(cond, " "), err)
	}

	// The exit status is the answer, as it is in a shell.
	return res.Exit == 0, nil
}

// substitute expands a `$(...)` by running what is inside it.
//
// The trailing newline goes, because a command's output is a line and the
// value wanted is what is on it: `LET tag = $(cat version)` means the version,
// not the version and a newline that then appears in an image tag.
func (p *Plan) substitute(cmd []string, base *ir.Node, dir, what, where string) (string, error) {
	if p.opt.commands == nil {
		return "", fmt.Errorf(
			"%s at %s has to be run to know its value: %q: %w"+
				"\n  the native engine expands arguments, not command output"+
				"\n  to build this now, use --engine=buildkit",
			what, where, strings.Join(cmd, " "), ErrNoRunner)
	}

	res, err := p.opt.commands(cmd, base, dir, where)
	if err != nil {
		return "", fmt.Errorf("%s at %s: running %q: %w", what, where, strings.Join(cmd, " "), err)
	}

	// A command that failed has no value to give, and using its output anyway
	// would put an error message into a variable and carry on.
	if res.Exit != 0 {
		return "", fmt.Errorf(
			"%s at %s: %q exited %d"+
				"\n  %s", what, where, strings.Join(cmd, " "), res.Exit, strings.TrimSpace(res.Output))
	}

	return strings.TrimRight(res.Output, "\n"), nil
}

// expandCommands replaces every `$(...)` in a value by running it.
//
// Nested parentheses are counted rather than matched by the first `)`, because
// `$(cat $(ls -1 | head -1))` is a shell writing a perfectly ordinary thing and
// stopping at the first close would run half a command.
// The value arrives with its arguments already substituted and its *quoting
// intact*: a `$(...)` is text a shell will read, so it follows the same rule as
// a RUN's command line rather than the one for a path this engine consumes.
func (p *Plan) expandCommands(value string, base *ir.Node, dir, what, where string) (string, error) {
	for {
		i := strings.Index(value, "$(")
		if i < 0 {
			return value, nil
		}

		depth, end := 1, -1

		for j := i + 2; j < len(value); j++ {
			switch value[j] {
			case '(':
				depth++
			case ')':
				depth--

				if depth == 0 {
					end = j
				}
			}

			if end >= 0 {
				break
			}
		}

		if end < 0 {
			return "", fmt.Errorf("%s at %s: %q has no closing bracket", what, where, value[i:])
		}

		// The text whole, not split into fields. A shell is about to parse it,
		// and splitting on whitespace is that shell's job - done here it turns
		// `cut -d' ' -f 1` into `cut -d -f 1`, which is cut being told the
		// delimiter is `-f` (E65).
		out, err := p.substitute([]string{value[i+2 : end]}, base, dir, what, where)
		if err != nil {
			return "", err
		}

		value = value[:i] + out + value[end+1:]
	}
}
