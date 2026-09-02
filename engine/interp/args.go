package interp

import (
	"fmt"
	"maps"
	"strings"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/util/flagutil"
	dfShell "github.com/moby/buildkit/frontend/dockerfile/shell"
)

// WithArgs supplies build argument values, overriding the defaults in the
// Earthfile.
func WithArgs(args map[string]string) Option {
	return func(o *options) { o.args = args }
}

// scope is the set of arguments in effect at a point in a recipe.
//
// Ordered by declaration: an ARG applies to the commands *after* it, so the
// same file read top to bottom means one thing and no other. Treating a later
// declaration as retroactive would make the order of a file change its meaning
// invisibly.
type scope map[string]string

// expand substitutes declared arguments and resolves quoting.
//
// Uses the same shell lexer the rest of this repository uses for the job -
// `dfShell.NewLex`, via the path `variables.Collection.ExpandOld` takes - rather
// than a second implementation of expansion and quoting. Two implementations of
// a language's substitution rules is two sets of corner cases that drift.
//
// **Only declared arguments are substituted.** The lexer expands what is in the
// map and leaves the rest, which is the behaviour this engine wants: `$i` in
// `for i in 1 2 3; do echo $i; done` belongs to the shell, and expanding it to
// the empty string would silently corrupt an ordinary RUN.
func (s scope) expand(in string) string {
	return s.expandValue(in)
}

// expandWord substitutes arguments while leaving quoting intact.
//
// For text a shell will parse again: a RUN's command line, an ENTRYPOINT in
// shell form. The quotes belong to *that* shell, and removing them changes what
// it runs - `sh -c "echo hi > /f"` becomes `sh -c echo hi > /f`, where the
// redirect belongs to the outer shell and the inner one receives only `echo`.
// The build succeeds and writes an empty file, which is the worst way to be
// wrong.
//
// Only declared arguments are substituted; anything else is the shell's and is
// left exactly as written.
func (s scope) expandWord(in string) string {
	var b strings.Builder

	// Which quotes the scan is inside, because the answer differs in all three
	// contexts and the first version of this knew about none of them (E450).
	var inSingle, inDouble bool

	for i := 0; i < len(in); {
		switch {
		case in[i] == '\\' && !inSingle && i+1 < len(in):
			// An escaped character is not syntax, and the backslash is the
			// author's: both are written through untouched.
			b.WriteByte(in[i])
			b.WriteByte(in[i+1])
			i += 2

			continue

		case in[i] == '\'' && !inDouble:
			inSingle = !inSingle

		case in[i] == '"' && !inSingle:
			inDouble = !inDouble
		}

		if in[i] != '$' || inSingle {
			// Inside single quotes a shell expands nothing, so neither does
			// this: `RUN echo '$V'` prints `$V` in every shell there is, and
			// substituting there gave an Earthfile something other than the
			// literal text it wrote.
			b.WriteByte(in[i])
			i++

			continue
		}

		if i+1 < len(in) && in[i+1] == '$' {
			b.WriteByte('$')
			i += 2

			continue
		}

		name, width := readName(in[i+1:])
		if name == "" {
			b.WriteByte('$')
			i++

			continue
		}

		if v, declared := s[name]; declared {
			// The value's own characters, escaped for the context they landed
			// in. Unescaped, a value containing a quote closed the author's and
			// the word split - which is `sh: with: unknown operand` from a
			// comparison that should have passed.
			//
			// This is what passing the argument as environment would do: the
			// shell expands it and no character of the result is syntax. The
			// context decides how much has to be escaped, not whether - outside
			// the author's quotes more of the value is syntax, not less (E964).
			if inDouble {
				v = escapeInDoubleQuotes(v)
			} else {
				v = escapeOutsideQuotes(v)
			}

			b.WriteString(v)
		} else {
			b.WriteString(in[i : i+1+width])
		}

		i += 1 + width
	}

	return b.String()
}

// expandValue substitutes arguments and resolves quoting, for text the engine
// consumes itself: a path, an argument default, a label.
func (s scope) expandValue(in string) string {
	lex := dfShell.NewLex('\\')
	lex.SkipUnsetEnv = true

	out, err := lex.ProcessWordWithMap(in, map[string]string(s))
	if err != nil {
		// A word the lexer cannot read is passed through unchanged: it is the
		// author's text, and mangling it would be worse than leaving it for
		// whatever reads it next.
		return in
	}

	return out
}

// expandDest substitutes arguments in a path the *engine* will write to, and
// clears the names nobody declared.
//
// The third rule, and the one that decides it is who reads the result. A RUN's
// text is read by a shell, which has its own answer for `$HOME` and must be
// left to give it (expandWord). A value the engine consumes and hands on -
// a tag, a label - keeps an unset name intact, because something downstream may
// still make sense of it (expandValue). A destination is read by nobody: it is
// a place, this engine makes it, and `build/arm64$VARIANT/x` is a directory
// with a dollar sign in its name that no later step looks in.
//
// The reference writes `build/arm64/x` there, checked against it directly. This
// is deliberately the narrow rule: the same question for a COPY destination or
// a SAVE IMAGE tag has not been measured, and a rule applied where it has not
// been checked is how `--dir` came to be wrong in both directions at once (E48).
func (s scope) expandDest(in string) string {
	lex := dfShell.NewLex('\\')
	lex.SkipUnsetEnv = false

	out, err := lex.ProcessWordWithMap(in, map[string]string(s))
	if err != nil {
		return in
	}

	return out
}

// declare parses `ARG name[=default]` into the scope.
// vars is what a default's names are looked up in, and is the scope with the
// step's environment overlaid: `ENV d delta` then `ARG VAR="d is $d"` computes
// `d is delta`, as the reference's single collection of both does. Separate from
// s, which is what the declaration writes to - an environment variable must not
// become an argument by being read (E964).
func (s scope) declare(
	vars scope, args []string, supplied map[string]string, where string,
	expand func(string) (string, error), builtin map[string]string,
	global map[string]string, declared map[string]bool, inTarget bool,
) error {
	if len(args) == 0 {
		return nil
	}

	// `ARG --required NAME` and `ARG --global NAME=value` carry flags before the
	// name. Read with the repository's own option layer rather than by hand:
	// without this the first flag *was* the name, so `ARG --global
	// IMAGE_REGISTRY=...` declared an argument called `--global` and left
	// IMAGE_REGISTRY undeclared - which surfaced far away, as an IF complaining
	// that an argument was never declared when the declaration was right there.
	var opts cmdopts.Arg

	rest, err := flagutil.ParseArgsCleaned("ARG", &opts, args)
	if err == nil && len(rest) > 0 {
		args = rest
	}

	// The parser tokenises `ARG name=value` as three tokens - name, "=", value -
	// rather than one, so both shapes are handled. Assuming the joined form
	// silently produced an argument declared with an empty default, which then
	// expanded to nothing.
	name, def, _ := strings.Cut(args[0], "=")

	if len(args) >= 3 && args[1] == "=" {
		name, def = args[0], strings.Join(args[2:], " ")
	}

	// The grammar allows `arg-default = dynamic-expr / WORD / QUOTED-STRING`,
	// so a quoted default is the value without its delimiters.
	//
	// **By region, because a `$(...)` is not a value.** Resolved across the
	// whole default, `ARG c=$( echo $(echo "\""))` reached the shell as
	// `echo $(echo """)` - an unterminated quote - because the escape was
	// resolved by this engine when it belonged to the shell that was about to
	// re-parse it. Variables are expanded further down, so the command region
	// is left exactly as written here.
	// The escaped dollars are stood aside before the unquoting that would
	// erase them, and put back once the command scan below has had its look.
	// See escapedDollar.
	def = expandByRegion(def, func(in string) string {
		return unquote(standAsideEscapedDollar(in))
	}, func(in string) string { return in })

	// One declaration per name per *scope*, and a second is an error.
	//
	// ARG declares a name and a default; it does not assign. Declared twice in
	// one recipe, the second does nothing - which E438 made true and which the
	// corpus says is not enough: `tests/arg-redeclare-error.earth` is named for
	// two targets that exist to be refused, and this engine built them (E456).
	// A repeated name is a mistake almost every time - a name typed twice, or a
	// copied block - and the author's second default silently never taking
	// effect is the shape of it.
	//
	// Per scope, which is what keeps the rest working: `ARG --global FOO` and
	// `ARG FOO` declare in two different places, so a target overriding an
	// inherited global is not redeclaring anything - and the same corpus file
	// asserts *that* two targets later.
	// A global is declared where globals live, and nowhere else.
	//
	// The base recipe is what every target starts from, so a `--global`
	// declared inside a target could only reach the targets built after it -
	// an ordering the language does not have. This engine accepted it and did
	// something worse than nothing with it: the name went into the globals map
	// of a state no other target inherits, so the flag decided nothing at all
	// (E461).
	if opts.Global && inTarget {
		return fmt.Errorf(
			"ARG --global %s at %s is inside a target"+
				"\n  a global belongs to the commands before the first target,"+
				" which is what every target starts from", name, where)
	}

	scope := "local:"
	if opts.Global {
		scope = "global:"
	}

	if declared[scope+name] {
		return fmt.Errorf(
			"ARG %s at %s is declared twice in this recipe"+
				"\n  the second declaration does nothing: remove it, or give the"+
				" first the default you meant",
			name, where)
	}

	if declared != nil {
		declared[scope+name] = true
	}

	// A name the engine answers is not the author's to give a default to.
	//
	// After the redeclaration check, because a second `ARG EARTHLY_VERSION` is a
	// redeclaration first and this second - the earlier line is the one to point
	// at (E457).
	if def != "" || (len(args) >= 3 && args[1] == "=") {
		err := refuseBuiltinArgument(name, where, "ARG")
		if err != nil {
			return err
		}
	}

	// A value from the command line beats the default, which is the whole point
	// of a default.
	if v, given := supplied[name]; given {
		s[name] = v
		remember(global, opts.Global, name, v)

		return nil
	}

	// The two words contradict each other, so neither is acted on.
	//
	// `--required` says the build must not proceed without a value from the
	// caller; a default says it always has one, so the flag can never fire. An
	// author who wrote both meant one of them, and this engine cannot tell
	// which - dropping the default would build something they did not write,
	// and dropping the flag would let a build proceed that they said must not
	// (E470).
	if opts.Required && def != "" {
		return fmt.Errorf(
			"ARG --required %s at %s also has a default"+
				"\n  --required means the caller must supply a value, and a"+
				" default means there always is one: remove one of them",
			name, where)
	}

	// `--required` is the author saying the build must not proceed without a
	// value. Recorded rather than refused here, because whether it matters
	// depends on whether anything reads it: a target that declares an argument
	// it never uses should not need one supplied.
	if opts.Required && def == "" {
		// ErrNotProvided, because the Earthfile is *valid*: declaring an
		// argument the invocation must supply is the feature working, and it is
		// the invocation that is incomplete. Without this the corpus counts
		// every such target as invalid input and they fill the list of what to
		// build next - which is the reasoning that created the family, applied
		// here to the second place it holds.
		//
		// Not ErrNoRunner: a value arrives on the command line and nothing has
		// to be executed to learn it. Merging them would offer
		// `--engine=buildkit` as the remedy for a forgotten flag.
		return fmt.Errorf(
			"ARG at %s: %q is --required and no value was given"+
				"\n  pass it with --%s=<value>: %w", where, name, name, ErrNotProvided)
	}

	// A `$(...)` in the default is run here and nowhere earlier, because a
	// supplied value has already returned above. `ARG v = $(git describe
	// --tags)` in a target the caller always passes `v` to would otherwise run
	// a command whose answer is discarded - and in the build where this matters
	// the command does not work at all, the default existing precisely because
	// the tool is absent or the file is not written yet. A discarded value is
	// cheap; a discarded failure stops the build.
	if expand != nil && strings.Contains(def, "$(") {
		out, err := expand(def)
		if err != nil {
			return err
		}

		def = out
	}

	// A default may name arguments declared above it: `ARG GOOS=$TARGETOS` is
	// the second line of every cross-building target in this repository. Left
	// unexpanded, the default is the *text* `$TARGETOS`, which then travels
	// into a path and makes a directory with a dollar sign in its name.
	//
	// Undeclared names survive, as they do everywhere else here: a name nothing
	// in scope answers is left as the text the author wrote.
	//
	// expandWord rather than expandValue, because a default's quoting is not
	// this engine's to resolve: the value may be a command line that a shell
	// will parse again, and `ARG greeting="say \"hello\""` loses its inner
	// quotes to an expansion that helpfully unquotes on the way past.
	def = vars.expandWord(def)

	// Back to an ordinary dollar, now that nothing downstream will read it as
	// the start of a command or of a name. See escapedDollar.
	def = restoreEscapedDollar(def)

	// A platform argument the engine knows the answer to. After the supplied
	// value and after the author's default, because both of those are somebody
	// saying what they want and this is only what the engine happens to know.
	if def == "" {
		if v, ok := builtin[name]; ok {
			s[name] = v
			remember(global, opts.Global, name, v)

			return nil
		}
	}

	// `ARG name` with no default and nothing supplied: the argument exists and
	// is empty, which is what an unset argument means.
	s[name] = def
	remember(global, opts.Global, name, def)

	return nil
}

// remember keeps an `ARG --global` where a function can find it.
//
// Only the flagged ones: a function is a unit with its own interface and must
// not see its caller's locals, so the map holds exactly what the author marked
// as reaching everywhere (E425).
func remember(global map[string]string, isGlobal bool, name, value string) {
	if !isGlobal || global == nil {
		return
	}

	global[name] = value
}

// escapeInDoubleQuotes stops a value's characters from ending the author's
// string.
//
// A quote ends it, a backtick substitutes, a backslash escapes whatever follows.
// Everything else - spaces, brackets, asterisks - is already literal between
// double quotes, and escaping it would put backslashes into the value.
//
// **`$` is escaped too**, which it was not, on the grounds that a dollar
// surviving expansion is syntax the author meant - `ARG WHERE=$HOME/x` asking
// for the step shell's HOME. The reference settles it the other way and settles
// it completely: it never splices, so the value arrives as environment,
// `shellescape.Quote`d, and a shell does not re-scan what an expansion produced.
// A `$HOME` written into a value stays five characters (E964).
//
// Leaving it live also let a value execute: `ARG VAR="literal\$(string)"`
// spliced into `RUN test "$VAR" == ...` ran `string` and compared against its
// output.
func escapeInDoubleQuotes(v string) string {
	var b strings.Builder

	for i := range len(v) {
		switch v[i] {
		case '\\', '"', '`', '$':
			b.WriteByte('\\')
		}

		b.WriteByte(v[i])
	}

	return b.String()
}

// escapeOutsideQuotes is the same idea where the author wrote no quotes.
//
// More characters are syntax here - a parenthesis, a semicolon, a pipe - but
// fewer than a general shell quoting would escape: an unquoted expansion is
// still split on whitespace and still globbed by the shell that performs it, so
// `RUN ls $FLAGS` and `RUN echo $PATTERN` must keep doing both. What is escaped
// is what would end the word or start a new construct; what is left is what the
// reference's inner shell does to an expanded value anyway.
func escapeOutsideQuotes(v string) string {
	var b strings.Builder

	for i := range len(v) {
		switch v[i] {
		case '\\', '"', '\'', '`', '$', '(', ')', ';', '&', '|', '<', '>':
			b.WriteByte('\\')
		}

		b.WriteByte(v[i])
	}

	return b.String()
}

// withEnv overlays the step's environment on the argument scope.
//
// ENV last, for the reason envFor puts it last: an ARG declares an input and an
// ENV sets what the image itself carries, so where both name one thing the
// image's is what a value computed at that point should see.
//
// A copy, because the scope is the recipe's and a declaration must not leak an
// environment variable into it under the name of an argument.
func (s scope) withEnv(env map[string]string) scope {
	if len(env) == 0 {
		return s
	}

	out := make(scope, len(s)+len(env))
	maps.Copy(out, s)
	maps.Copy(out, scope(env))

	return out
}
