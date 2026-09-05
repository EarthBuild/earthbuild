package interp

import (
	"errors"
	"strconv"
	"strings"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/util/flagutil"
)

// decide evaluates an IF condition at plan time.
//
// Almost every IF in a real Earthfile compares build arguments -
// `[ "$mode" = "release" ]`, `[ -z "$x" ]` - and those are decidable once
// arguments are expanded. Deciding them here keeps the graph **known before the
// build**, which is what every key, schedule and diagnostic in this engine rests
// on: a graph that changes while it runs has no stable identity to key on.
//
// Everything else is refused. `IF command -v unbuffer` needs a filesystem and a
// process; guessing its branch would build something the Earthfile does not
// describe and report success. Refusing names the condition and offers the
// engine that can run it.
// condFlags reads IF's options off the front of its condition.
//
// The same defect RUN had, in a different command: without this, `IF
// --no-cache [ "$x" = y ]` was decided by reading `--no-cache` as the first
// word of the condition. A flag governs how a condition is *evaluated* - may it
// be cached, may it reach the network - and never what it says.
//
// `--no-cache` is accepted and dropped here rather than recorded. A condition
// is decided when the graph is built, so there is no step whose caching it
// could govern; when the condition instead has to be *run*, the probe that runs
// it is a fresh step every time already.
// trueWord is how the language spells truth, in every place it is written
// or read: an IF condition's token, a mount option's value, and the value
// a flag given without one takes. The last two are a pair - a flag written
// as present has to read back as true - and one word makes that so by
// construction rather than by two files agreeing.
const trueWord = "true"

func condFlags(cond []string, where string) ([]string, error) {
	var opts cmdopts.If

	rest, err := flagutil.ParseArgsCleaned("IF", &opts, cond)
	if err != nil {
		return nil, flagFault("IF", where, err)
	}

	for _, u := range []struct {
		set  bool
		name string
	}{
		{len(opts.Secrets) > 0, "--secret"},
		{len(opts.Mounts) > 0, "--mount"},
		{opts.Privileged, "--privileged"},
		// A literal, deliberately: TestEveryRefusedFlagSaysWhatItWas reads
		// these tables out of the source text, and a flag named through a
		// constant is invisible to it. The guard outranks the lint rule.
		{opts.WithSSH, "--ssh"},
	} {
		if u.set {
			return nil, unsupported("IF "+u.name, where, "")
		}
	}

	return rest, nil
}

func decide(cond []string, _ scope, env map[string]string) (bool, error) {
	// An unexpanded `$name` is a name no ARG declared. It may still be a real
	// variable - ENV sets some, and a base image sets more - so it is looked up
	// in the environment this build state carries, and only what is left over
	// makes the condition undecidable here.
	//
	// Undecidable, not wrong: the name is refused nowhere, because refusing
	// `IF [ "$CARGO_HOME" = "" ]` as an undeclared argument blamed the Earthfile
	// for a variable it never had to declare - CARGO_HOME comes from the rust
	// image. The condition goes to a probe, whose shell sees exactly what the
	// step would.
	cond = append([]string(nil), cond...)

	for i, tok := range cond {
		out, known := substituteEnv(tok, env)
		if !known {
			return false, errUnsupportedTest
		}

		cond[i] = out
	}

	// errUnsupportedTest travels up unwrapped: the caller decides whether to
	// evaluate the condition or to refuse it by name. "unsupported test" is not
	// a message for a reader, it is a signal for that choice.
	return decideChain(cond)
}

// decideChain evaluates tests joined by `&&` and `||`.
//
// Left-associative with equal precedence, which is the shell's rule rather than
// C's: `a && b || c` is `(a && b) || c`. Getting this wrong would silently take
// the other branch, which is the failure mode this whole file is arranged to
// avoid.
//
// Short-circuiting is not an optimisation here. An operand the engine cannot
// decide on its own - `[ "$v" = "no" ] && command -v unbuffer` - is never
// reached when the left side settles the answer, and a condition that is not
// evaluated needs no decision. The shell would not run it either.
func decideChain(cond []string) (bool, error) {
	groups, ops := splitChain(cond)

	got, err := decideAtom(groups[0])
	if err != nil {
		return false, err
	}

	for i, op := range ops {
		if (op == "&&") != got {
			continue // settled: `false && _` and `true || _`
		}

		got, err = decideAtom(groups[i+1])
		if err != nil {
			return false, err
		}
	}

	return got, nil
}

// splitChain divides a condition at its `&&` and `||` operators, returning one
// more group than operators.
func splitChain(cond []string) (groups [][]string, ops []string) {
	group := []string{}

	for _, tok := range cond {
		if tok == "&&" || tok == "||" {
			groups = append(groups, group)
			ops = append(ops, tok)
			group = []string{}

			continue
		}

		group = append(group, tok)
	}

	return append(groups, group), ops
}

// decideAtom evaluates one link of a chain: a `[ ... ]`, or `true`/`false`.
func decideAtom(cond []string) (bool, error) {
	toks := strip(cond)

	switch {
	case len(toks) == 1 && toks[0] == trueWord:
		return true, nil
	case len(toks) == 1 && toks[0] == "false":
		return false, nil
	}

	// `[ ... ]` and `[[ ... ]]`, which is how a comparison is written.
	if len(toks) >= 2 && (toks[0] == "[" || toks[0] == "[[") {
		inner := toks[1 : len(toks)-1]

		// `!` negates what follows, and is written 36 times in this repository.
		negate := len(inner) > 1 && inner[0] == "!"
		if negate {
			inner = inner[1:]
		}

		got, err := decideTest(inner)
		if err != nil {
			return false, err
		}

		return got != negate, nil
	}

	return false, errUnsupportedTest
}

// decideTest evaluates the inside of a `[ ... ]`.
// errUnsupportedTest marks a condition this cannot decide, so the caller can
// produce the diagnostic that names it.
var errUnsupportedTest = errors.New("unsupported test")

func decideTest(inner []string) (bool, error) {
	// An operand that expanded to nothing is dropped by the parser, so a
	// comparison arrives one token short. Absent is what empty looks like after
	// expansion, and the author plainly meant a comparison - the same reasoning
	// the -z cases below rest on.
	if len(inner) == 2 {
		if isComparison(inner[0]) {
			inner = []string{"", inner[0], inner[1]}
		} else if isComparison(inner[1]) {
			inner = []string{inner[0], inner[1], ""}
		}
	}

	switch {
	case len(inner) == 1 && inner[0] == "-z":
		// `[ -z "$x" ]` where x expanded to nothing: the parser drops the empty
		// token, so the operand is absent rather than empty. Absent *is* empty,
		// and this is exactly the case people write -z for.
		return true, nil
	case len(inner) == 1 && inner[0] == "-n":
		return false, nil
	case len(inner) == 2 && inner[0] == "-z":
		return inner[1] == "", nil
	case len(inner) == 2 && inner[0] == "-n":
		return inner[1] != "", nil
	case len(inner) == 3 && (inner[1] == "=" || inner[1] == "=="):
		return inner[0] == inner[2], nil
	case len(inner) == 3 && inner[1] == "!=":
		return inner[0] != inner[2], nil
	case len(inner) == 3 && numericOp(inner[1]) != nil:
		return decideNumeric(inner[0], inner[1], inner[2])
	}

	return false, errUnsupportedTest
}

// substituteEnv replaces every `$name` in a token with what the environment
// holds, reporting false the moment it meets one the environment does not.
//
// Every name or none: a token half-substituted would be compared against as
// though the rest were empty, which is how a condition takes the wrong branch
// without anything looking wrong.
func substituteEnv(tok string, env map[string]string) (string, bool) {
	var b strings.Builder

	for i := 0; i < len(tok); i++ {
		if tok[i] != '$' {
			b.WriteByte(tok[i])

			continue
		}

		// `$$` is a literal dollar, not the start of a name.
		if i+1 < len(tok) && tok[i+1] == '$' {
			b.WriteString("$$")
			i++

			continue
		}

		name, width := readName(tok[i+1:])
		if width == 0 {
			b.WriteByte(tok[i])

			continue
		}

		v, ok := env[name]
		if !ok {
			return "", false
		}

		b.WriteString(v)

		i += width
	}

	return b.String(), true
}

// strip removes the quotes the parser leaves on a token.
//
// `[ "$mode" = "release" ]` arrives with the quotes as literal characters, so a
// comparison against an expanded value would never match. They are shell syntax
// protecting whitespace, not part of the value.
func strip(toks []string) []string {
	out := make([]string, 0, len(toks))

	for _, t := range toks {
		if len(t) >= 2 && (t[0] == '"' || t[0] == '\'') && t[len(t)-1] == t[0] {
			t = t[1 : len(t)-1]
		}

		out = append(out, t)
	}

	return out
}

// isComparison reports whether a token is a binary string comparison.
func isComparison(tok string) bool {
	return tok == "=" || tok == "==" || tok == "!="
}

// numericOp is the comparison a `test` operator makes over two integers, or nil
// where it is not one of them.
//
// **A probe is a container round trip**, and `IF [ "$level" -gt "0" ]` is how
// the corpus writes a bounded loop - `command.earth`'s `RECURSIVE` counts down
// from 5, so five conditions cost five round trips before a step of real work
// happens. The operands are known: a build argument and a literal. `=` and
// `!=` are decided here for that reason and these are the same argument.
func numericOp(op string) func(a, b int64) bool {
	switch op {
	case "-eq":
		return func(a, b int64) bool { return a == b }
	case "-ne":
		return func(a, b int64) bool { return a != b }
	case "-lt":
		return func(a, b int64) bool { return a < b }
	case "-le":
		return func(a, b int64) bool { return a <= b }
	case "-gt":
		return func(a, b int64) bool { return a > b }
	case "-ge":
		return func(a, b int64) bool { return a >= b }
	}

	return nil
}

// decideNumeric answers a numeric comparison, or declines it.
//
// **Declines rather than guesses.** `[ x -gt 0 ]` is an error in a shell, not
// false - and an engine that answered it would be inventing a language. So an
// operand that is not an integer goes to the shell, which knows what its own
// error is.
func decideNumeric(left, op, right string) (bool, error) {
	a, err := strconv.ParseInt(strings.TrimSpace(left), 10, 64)
	if err != nil {
		return false, errUnsupportedTest
	}

	b, err := strconv.ParseInt(strings.TrimSpace(right), 10, 64)
	if err != nil {
		return false, errUnsupportedTest
	}

	return numericOp(op)(a, b), nil
}
