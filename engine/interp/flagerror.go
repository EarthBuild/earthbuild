package interp

import (
	"fmt"
	"regexp"
	"strings"
)

// badBool is how the flag library reports a boolean given something else.
//
// Matched rather than reconstructed: the library's phrasing is what arrives, and
// the parts worth keeping - which flag, and what was written - are in it.
var badBool = regexp.MustCompile(
	"invalid argument for flag `(-[^']+)' \\(expected bool\\): .*parsing \"([^\"]*)\"")

// unknownFlag is the library's report of a flag it has never heard of.
var unknownFlag = regexp.MustCompile("unknown flag `([^']+)'")

// flagFault turns a flag library's error into one this engine would write.
//
// The library says:
//
//	invalid argument for flag `--no-cache' (expected bool): strconv.ParseBool: parsing "maybe": invalid syntax
//
// which names a Go function, a Go package's idea of syntax, and quotes the flag
// with a backtick and an apostrophe. Every other refusal here says what failed,
// where, what was expected and what to write instead - and a diagnostic that
// reads like a stack trace sends the reader to the wrong language (E451).
//
// Anything this does not recognise is passed through with the command and the
// line, because a message this engine cannot improve is still better than one it
// has replaced with a guess.
func flagFault(cmd string, where string, err error) error {
	if m := badBool.FindStringSubmatch(err.Error()); m != nil {
		flag, value := m[1], m[2]

		return fmt.Errorf(
			"%s %s at %s: %q is not a yes or a no"+
				"\n  write %s=true or %s=false, or %s on its own",
			cmd, flag, where, value, flag, flag, flag)
	}

	if m := unknownFlag.FindStringSubmatch(err.Error()); m != nil {
		flag := m[1]
		if !strings.HasPrefix(flag, "-") {
			flag = "--" + flag
		}

		return fmt.Errorf(
			"%s at %s: %s is not an option of %s"+
				"\n  check the spelling, or the VERSION line if it is a newer flag",
			cmd, where, flag, cmd)
	}

	return fmt.Errorf("%s (%s): %w", cmd, where, err)
}
