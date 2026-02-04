package flagutil

import (
	"context"
	"os"
	"reflect"
	"strings"

	"github.com/EarthBuild/earthbuild/ast/commandflag"
	"github.com/EarthBuild/earthbuild/ast/spec"
	"github.com/EarthBuild/earthbuild/util/stringutil"
	"github.com/pkg/errors"

	"github.com/jessevdk/go-flags"
	"github.com/urfave/cli/v2"
)

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
	return ParseArgsWithValueModifier(command, data, args, nil)
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
	// Preprocess args if we have a modifier function
	if argumentModFunc != nil {
		boolFlags := getBoolFlagNames(data)
		var err error
		args, err = preprocessArgs(args, boolFlags, argumentModFunc)
		if err != nil {
			return nil, err
		}
	}

	p := flags.NewNamedParser("", parserOptions)

	_, err := p.AddGroup(command+" [options] args", "", data)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to initiate parser.AddGroup for %s", command)
	}

	res, err := p.ParseArgs(args)
	if err != nil {
		if parserOptions&flags.PrintErrors != flags.None {
			p.WriteHelp(os.Stderr)
		}

		return nil, err
	}

	return res, nil
}

// getBoolFlagNames extracts boolean flag names from a struct using reflection
func getBoolFlagNames(data any) map[string]bool {
	boolFlags := make(map[string]bool)

	if data == nil {
		return boolFlags
	}

	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return boolFlags
	}

	collectBoolFlags(v.Type(), boolFlags)
	return boolFlags
}

// collectBoolFlags recursively collects boolean flag names from a struct type
func collectBoolFlags(t reflect.Type, boolFlags map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fieldType := field.Type

		// Handle embedded structs recursively
		if field.Anonymous && fieldType.Kind() == reflect.Struct {
			collectBoolFlags(fieldType, boolFlags)
			continue
		}

		// Check if it's a boolean type
		isBool := fieldType.Kind() == reflect.Bool

		// Get flag names from tags
		shortTag := field.Tag.Get("short")
		if shortTag != "" && isBool {
			boolFlags[shortTag] = true
		}
		longTag := field.Tag.Get("long")
		if longTag != "" && isBool {
			boolFlags[longTag] = true
		}
	}
}

// preprocessArgs processes arguments before parsing, applying the modifier function to boolean flag values
func preprocessArgs(args []string, boolFlags map[string]bool, modFunc ArgumentModFunc) ([]string, error) {
	result := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Check if this is a flag with a value (e.g., --flag=value)
		if strings.HasPrefix(arg, "--") {
			parts := strings.SplitN(arg[2:], "=", 2)
			flagName := parts[0]

			if len(parts) == 2 && boolFlags[flagName] {
				// This is a boolean flag with an explicit value
				value := parts[1]
				modifiedValue, err := modFunc(flagName, nil, &value)
				if err != nil {
					return nil, err
				}
				if modifiedValue != nil {
					result = append(result, "--"+flagName+"="+*modifiedValue)
				} else {
					result = append(result, "--"+flagName)
				}
				continue
			}
		} else if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			// Short flag handling
			flagPart := strings.TrimPrefix(arg, "-")

			// Check if it has an attached value (e.g., -f=value)
			if strings.Contains(flagPart, "=") {
				parts := strings.SplitN(flagPart, "=", 2)
				flagName := parts[0]
				if len(flagName) == 1 && boolFlags[flagName] {
					value := parts[1]
					modifiedValue, err := modFunc(flagName, nil, &value)
					if err != nil {
						return nil, err
					}
					if modifiedValue != nil {
						result = append(result, "-"+flagName+"="+*modifiedValue)
						continue
					}
				}
			} else if len(flagPart) == 1 && boolFlags[flagPart] {
				// Single short flag, check if next arg is the value
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					value := args[i+1]
					modifiedValue, err := modFunc(flagPart, nil, &value)
					if err != nil {
						return nil, err
					}
					if modifiedValue != nil {
						result = append(result, arg, *modifiedValue)
						i++ // Skip the next arg since we consumed it
						continue
					}
				}
			} else if len(flagPart) > 1 {
				// Handle clustered short flags (e.g., -abc)
				// Check each character to see if any are boolean flags that need modification
				modified := false
				for j, c := range flagPart {
					flagName := string(c)
					if boolFlags[flagName] {
						// For clustered flags, we can only modify if it's the last flag
						// and the next arg is a value
						if j == len(flagPart)-1 && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
							value := args[i+1]
							modifiedValue, err := modFunc(flagName, nil, &value)
							if err != nil {
								return nil, err
							}
							if modifiedValue != nil {
								result = append(result, arg, *modifiedValue)
								i++ // Skip the next arg since we consumed it
								modified = true
								break
							}
						}
					}
				}
				if modified {
					continue
				}
			}
		}

		result = append(result, arg)
	}

	return result, nil
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

// ParseParams turns "(+target --flag=something)" into "+target" and []string{"--flag=something"},
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
