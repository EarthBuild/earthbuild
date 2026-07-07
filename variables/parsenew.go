package variables

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

// InvalidFlagError is returned when a flag is prefixed with a single hyphen instead of double.
type InvalidFlagError struct {
	Flag       string
	Suggestion string
}

func (e *InvalidFlagError) Error() string {
	return fmt.Sprintf("Invalid flag '%s'. Did you mean '%s'?", e.Flag, e.Suggestion)
}

// ParseFlagArgs parses flag-form args.
// These can be represented as `--arg=value` or `--arg value`.
// The result is a slice that can be passed into ParseArgs or to ParseCommandLineArgs.
func ParseFlagArgs(args []string) ([]string, error) {
	flags, nonFlags, err := ParseFlagArgsWithNonFlags(args)
	if err != nil {
		return nil, err
	}

	if len(nonFlags) != 0 {
		return nil, errors.Errorf("invalid argument %s", nonFlags[0])
	}

	return flags, nil
}

// ParseFlagArgsWithNonFlags parses flag-form args together with any possible optional additional
// args. e.g. "--flag1=value arg1 --flag2=value --flag3=value arg2 arg3".
func ParseFlagArgsWithNonFlags(args []string) (flags, nonFlags []string, err error) {
	keyFromPrev := ""

	for _, arg := range args {
		var k, v string

		if keyFromPrev != "" {
			k = keyFromPrev
			keyFromPrev = ""
			v = arg
		} else {
			err := checkInvalidFlag(arg)
			if err != nil {
				return nil, nil, err
			}

			trimmedArg, found := strings.CutPrefix(arg, "--")
			if !found {
				nonFlags = append(nonFlags, arg)
				continue
			}

			var hasValue bool

			k, v, hasValue = ParseKeyValue(trimmedArg)
			if !hasValue {
				keyFromPrev = k
				continue
			}
		}

		escK := strings.ReplaceAll(k, "=", "\\=")
		flags = append(flags, fmt.Sprintf("%s=%s", escK, v))
	}

	if keyFromPrev != "" {
		return nil, nil, errors.Errorf("no value provided for --%s", keyFromPrev)
	}

	return flags, nonFlags, nil
}

// checkInvalidFlag checks if the argument is a single-hyphen or multi-hyphen invalid flag
// and returns an InvalidFlagError if so.
func checkInvalidFlag(arg string) error {
	firstNonHyphenIdx := strings.IndexFunc(arg, func(r rune) bool {
		return r != '-'
	})

	if firstNonHyphenIdx > 0 && firstNonHyphenIdx != 2 {
		firstChar := arg[firstNonHyphenIdx]
		isFlag := (firstChar >= 'a' && firstChar <= 'z') ||
			(firstChar >= 'A' && firstChar <= 'Z') ||
			firstChar == '_'

		if isFlag {
			parts := strings.SplitN(arg, "=", 2)
			flagPart := parts[0]
			suggestion := "--" + arg[firstNonHyphenIdx:]

			return &InvalidFlagError{Flag: flagPart, Suggestion: suggestion}
		}
	}

	return nil
}
