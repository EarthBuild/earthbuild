package flagutil

import (
	"context"
	"math"
	"os"
	"reflect"
	"strings"

	"github.com/EarthBuild/earthbuild/ast/commandflag"
	"github.com/EarthBuild/earthbuild/ast/spec"
	"github.com/EarthBuild/earthbuild/util/hint"
	"github.com/EarthBuild/earthbuild/util/stringutil"
	"github.com/pkg/errors"

	"github.com/jessevdk/go-flags"
	"github.com/urfave/cli/v2"
)

// levenshteinDistance calculates the edit distance between two strings.
// Uses O(min(m,n)) space by only keeping two rows of the DP matrix.
func levenshteinDistance(s1, s2 string) int {
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}

	// Ensure s2 is the shorter string to minimize space usage
	if len(s1) < len(s2) {
		s1, s2 = s2, s1
	}

	// Only need two rows: previous and current
	prev := make([]int, len(s2)+1)
	curr := make([]int, len(s2)+1)

	// Initialize first row
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(s1); i++ {
		curr[0] = i
		for j := 1; j <= len(s2); j++ {
			cost := 1
			if s1[i-1] == s2[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(s2)]
}

// extractFlagNames extracts all long flag names from a struct using reflection.
func extractFlagNames(data any) []string {
	if data == nil {
		return nil
	}

	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}

	t := v.Type()
	var flagNames []string
	for i := range t.NumField() {
		if longTag := t.Field(i).Tag.Get("long"); longTag != "" {
			flagNames = append(flagNames, longTag)
		}
	}
	return flagNames
}

// findClosestFlag finds the most similar flag name to the given unknown flag.
// Returns the suggested flag and whether a good suggestion was found.
func findClosestFlag(unknownFlag string, validFlags []string) (string, bool) {
	if len(validFlags) == 0 {
		return "", false
	}

	// Remove leading dashes from the unknown flag for comparison
	unknownFlag = strings.TrimLeft(unknownFlag, "-")

	bestMatch := ""
	bestDistance := math.MaxInt

	for _, validFlag := range validFlags {
		if distance := levenshteinDistance(unknownFlag, validFlag); distance < bestDistance {
			bestDistance = distance
			bestMatch = validFlag
		}
	}

	// Only suggest if the distance is reasonable (less than half the length of the unknown flag).
	// This prevents suggesting completely unrelated flags.
	// Allow at least 2 character difference for short flags.
	maxDistance := max(len(unknownFlag)/2, 2)
	if bestDistance <= maxDistance {
		return bestMatch, true
	}
	return "", false
}

// suggestFlagIfUnknown checks if the error is about an unknown flag and adds a suggestion if possible.
func suggestFlagIfUnknown(err error, data any) error {
	if err == nil {
		return nil
	}

	unknownFlag, ok := extractUnknownFlagFromError(err)
	if !ok {
		return err
	}

	suggestion, found := findClosestFlag(unknownFlag, extractFlagNames(data))
	if !found {
		return err
	}

	return hint.Wrapf(err, "Did you mean '--%s'?", suggestion)
}

// extractUnknownFlagFromError extracts the flag name from an "unknown flag" error.
// The go-flags library returns errors like "unknown flag `flag-name'".
func extractUnknownFlagFromError(err error) (string, bool) {
	errMsg := err.Error()
	if !strings.Contains(errMsg, "unknown flag") {
		return "", false
	}

	startIdx := strings.Index(errMsg, "`")
	endIdx := strings.LastIndex(errMsg, "'")
	if startIdx == -1 || endIdx == -1 || startIdx >= endIdx {
		return "", false
	}

	return errMsg[startIdx+1 : endIdx], true
}

// ArgumentModFunc accepts a flagName which corresponds to the long flag name, and a pointer
// to a flag value. The pointer is nil if no flag was given.
// the function returns a new pointer set to nil if one wants to pretend as if no value was given,
// or a pointer to a new value which will be parsed.
// Note: this was created to allow passing --no-cache=$SOME_VALUE; where we must expand $SOME_VALUE into
// a true/false value before it is parsed. If this feature is used extensively, then it might be time
// to completely fork go-flags with a version where we can include control over expansion struct tags.
type ArgumentModFunc func(flagName string, opt *flags.Option, flagVal *string) (*string, error)

// ParseArgs parses flags and args from a command string.
func ParseArgs(command string, data any, args []string) ([]string, error) {
	return ParseArgsWithValueModifier(command, data, args,
		func(_ string, _ *flags.Option, s *string) (*string, error) { return s, nil },
	)
}

func ParseArgsCleaned(cmdName string, opts any, args []string) ([]string, error) {
	processed := stringutil.ProcessParamsAndQuotes(args)
	return ParseArgs(cmdName, opts, processed)
}

func ParseArgsWithValueModifierCleaned(
	cmdName string, opts any, args []string, argumentModFunc ArgumentModFunc,
) ([]string, error) {
	processed := stringutil.ProcessParamsAndQuotes(args)
	return ParseArgsWithValueModifier(cmdName, opts, processed, argumentModFunc)
}

// ParseArgsWithValueModifier parses flags and args from a command string; it accepts an optional argumentModFunc
// which is called before each flag value is parsed, and allows one to change the value.
// if the flag value.
func ParseArgsWithValueModifier(
	command string, data any, args []string, argumentModFunc ArgumentModFunc,
) ([]string, error) {
	return ParseArgsWithValueModifierAndOptions(
		command,
		data,
		args,
		argumentModFunc,
		flags.PrintErrors|flags.PassDoubleDash|flags.PassAfterNonOption|flags.AllowBoolValues,
	)
}

// ParseArgsWithValueModifierAndOptions is similar to ParseArgsWithValueModifier,
// but allows changing the parser options.
func ParseArgsWithValueModifierAndOptions(
	command string, data any, args []string, argumentModFunc ArgumentModFunc, parserOptions flags.Options,
) ([]string, error) {
	p := flags.NewNamedParser("", parserOptions)
	var modFuncErr error
	modFunc := func(flagName string, opt *flags.Option, flagVal *string) *string {
		p, err := argumentModFunc(flagName, opt, flagVal)
		if err != nil {
			modFuncErr = err
		}
		return p
	}
	p.ArgumentMod = modFunc
	_, err := p.AddGroup(command+" [options] args", "", data)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initiate parser.AddGroup for %s", command)
	}
	res, err := p.ParseArgs(args)
	if err != nil {
		if parserOptions&flags.PrintErrors != flags.None {
			p.WriteHelp(os.Stderr)
		}
		// Try to provide helpful suggestions for unknown flags
		err = suggestFlagIfUnknown(err, data)
		return nil, err
	}
	if modFuncErr != nil {
		return nil, modFuncErr
	}
	return res, nil
}

// SplitFlagString would return an array of values from the StringSlice, whether it's passed using
// multiple occuranced of the flag or with the values passed with a command. For example:
//
//	--platform linux/amd64 --platform linux/arm64 and --platform "linux/amd64,linux/arm64"
func SplitFlagString(value cli.StringSlice) []string {
	valueStr := strings.TrimLeft(strings.TrimRight(value.String(), "]"), "[")
	return strings.FieldsFunc(valueStr, func(r rune) bool {
		return r == ' ' || r == ','
	})
}

var (
	ErrInvalidSyntax         = errors.New("invalid syntax")
	ErrRequiredArgHasDefault = errors.New("required ARG cannot have a default value")
	ErrGlobalArgNotInBase    = errors.New("global ARG can only be set in the base target")
)

// ParseArgArgs parses the ARG command's arguments
// and returns the argOpts, key, value (or nil if missing), or error.
func ParseArgArgs(
	ctx context.Context, cmd spec.Command, isBaseTarget bool, explicitGlobalFeature bool,
) (commandflag.ArgOpts, string, *string, error) {
	var opts commandflag.ArgOpts
	args, err := ParseArgsCleaned("ARG", &opts, GetArgsCopy(cmd))
	if err != nil {
		return commandflag.ArgOpts{}, "", nil, err
	}
	if opts.Global {
		// since the global flag is part of the struct, we need to manually return parsing error
		// if it's used while the feature flag is off
		if !explicitGlobalFeature {
			return commandflag.ArgOpts{}, "", nil, errors.New("unknown flag --global")
		}
		// global flag can only bet set on base targets
		if !isBaseTarget {
			return commandflag.ArgOpts{}, "", nil, ErrGlobalArgNotInBase
		}
	} else if !explicitGlobalFeature {
		// if the feature flag is off, all base target args are considered global
		opts.Global = isBaseTarget
	}
	switch len(args) {
	case 3:
		if args[1] != "=" {
			return commandflag.ArgOpts{}, "", nil, ErrInvalidSyntax
		}
		if opts.Required {
			return commandflag.ArgOpts{}, "", nil, ErrRequiredArgHasDefault
		}
		return opts, args[0], &args[2], nil
	case 1:
		return opts, args[0], nil, nil
	default:
		return commandflag.ArgOpts{}, "", nil, ErrInvalidSyntax
	}
}

func GetArgsCopy(cmd spec.Command) []string {
	argsCopy := make([]string, len(cmd.Args))
	copy(argsCopy, cmd.Args)
	return argsCopy
}

func IsInParamsForm(str string) bool {
	return (strings.HasPrefix(str, "\"(") && strings.HasSuffix(str, "\")")) ||
		(strings.HasPrefix(str, "(") && strings.HasSuffix(str, ")"))
}

// parseParams turns "(+target --flag=something)" into "+target" and []string{"--flag=something"},
// or "\"(+target --flag=something)\"" into "+target" and []string{"--flag=something"}.
func ParseParams(str string) (string, []string, error) {
	if !IsInParamsForm(str) {
		return "", nil, errors.New("params atom not in ( ... )")
	}
	if strings.HasPrefix(str, "\"(") {
		str = str[2 : len(str)-2] // remove \"( and )\"
	} else {
		str = str[1 : len(str)-1] // remove ( and )
	}
	parts := make([]string, 0, 1)
	part := make([]rune, 0, len(str))
	nextEscaped := false
	inQuotes := false
	for _, char := range str {
		switch char {
		case '"':
			if !nextEscaped {
				inQuotes = !inQuotes
			}
			nextEscaped = false
		case '\\':
			nextEscaped = true
		case ' ', '\t', '\n':
			if !inQuotes && !nextEscaped {
				if len(part) > 0 {
					parts = append(parts, string(part))
					part = []rune{}
					nextEscaped = false
					continue
				} else {
					nextEscaped = false
					continue
				}
			}
			nextEscaped = false
		default:
			nextEscaped = false
		}
		part = append(part, char)
	}
	if nextEscaped {
		return "", nil, errors.New("unterminated escape sequence")
	}
	if inQuotes {
		return "", nil, errors.New("no ending quotes")
	}
	if len(part) > 0 {
		parts = append(parts, string(part))
	}

	if len(parts) < 1 {
		return "", nil, errors.New("invalid empty params")
	}
	return parts[0], parts[1:], nil
}

// ParseLoad splits a --load value into the image, target, & extra args.
// Example: --load my-image=(+target --arg1 foo --arg2=bar).
func ParseLoad(loadStr string) (image string, target string, extraArgs []string, err error) {
	words := strings.SplitN(loadStr, " ", 2)
	if len(words) == 0 {
		return "", "", nil, nil
	}
	firstWord := words[0]
	splitFirstWord := strings.SplitN(firstWord, "=", 2)
	if len(splitFirstWord) < 2 {
		// <target-name>
		// (will infer image name from SAVE IMAGE of that target)
		image = ""
		target = loadStr
	} else {
		// <image-name>=<target-name>
		image = splitFirstWord[0]
		if len(words) == 1 {
			target = splitFirstWord[1]
		} else {
			words[0] = splitFirstWord[1]
			target = strings.Join(words, " ")
		}
	}
	if IsInParamsForm(target) {
		target, extraArgs, err = ParseParams(target)
		if err != nil {
			return "", "", nil, err
		}
	}
	return image, target, extraArgs, nil
}
