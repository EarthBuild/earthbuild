package interp

import "testing"

// A `$( )` is handed to the shell with one level of escaping resolved.
//
// The Earthfile's backslash is the Earthfile's: `ARG foo = "$(echo \\(\\))"`
// means the command `echo \(\)`, which prints `()`. Passing the text through
// verbatim gave the shell `echo \\(\\)` - a literal backslash and then an
// unquoted bracket - and it exited 2 on a syntax error nobody wrote (E949).
//
// The rules are the reference's, in `util/shell/lex.go`'s
// `processDollarShellOut`: outside quotes a backslash is dropped and the next
// character taken literally; inside quotes everything is verbatim, backslashes
// included; and a bracket only counts towards the nesting when it is outside
// quotes and unescaped.
func TestACommandRegionResolvesOneLevelOfEscaping(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, in, cmd string
		bad           bool
	}{{
		name: "an ordinary command",
		in:   `$(echo hi)`,
		cmd:  `echo hi`,
	}, {
		name: "escaped brackets are the shell's, one backslash deep",
		in:   `$(echo \\(\\))`,
		cmd:  `echo \(\)`,
	}, {
		name: "an escaped bracket does not close the region",
		in:   `$(echo \))`,
		cmd:  `echo )`,
	}, {
		name: "a bracket inside single quotes is text",
		in:   `$(echo '(')`,
		cmd:  `echo '('`,
	}, {
		name: "a bracket inside double quotes is text",
		in:   `$(echo "(")`,
		cmd:  `echo "("`,
	}, {
		name: "a nested command is kept whole",
		in:   `$(cat $(ls -1))`,
		cmd:  `cat $(ls -1)`,
	}, {
		name: "a backslash inside double quotes stays",
		in:   `$(echo "\"")`,
		cmd:  `echo "\""`,
	}, {
		name: "a backslash inside single quotes stays",
		in:   `$(echo '\')`,
		cmd:  `echo '\'`,
	}, {
		name: "nothing closes it",
		in:   `$(echo hi`,
		bad:  true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd, end, ok := commandRegion(tc.in, 0)
			if ok == tc.bad {
				t.Fatalf("commandRegion(%q) ok = %v, want %v", tc.in, ok, !tc.bad)
			}

			if tc.bad {
				return
			}

			if cmd != tc.cmd {
				t.Errorf("commandRegion(%q) read %q, want %q", tc.in, cmd, tc.cmd)
			}

			// The end is the closing bracket, which is what the caller slices
			// around: everything after it is the rest of the value.
			if tc.in[end] != ')' {
				t.Errorf("commandRegion(%q) ended at %q, which is not the bracket",
					tc.in, tc.in[end])
			}
		})
	}
}
