package stringutil

import "strings"

// ProcessParamsAndQuotes takes in a slice of strings, and rearranges the slices
// depending on quotes and parenthesis.
//
// For example "hello ", "wor(", "ld)" becomes "hello ", "wor( ld)".
func ProcessParamsAndQuotes(args []string) []string {
	var (
		open       bool
		sb         strings.Builder
		mergedArgs = make([]string, 0, len(args))
	)

	for _, arg := range args {
		sb.WriteString(arg)

		for _, ch := range arg {
			if open {
				open = ch != '"' && ch != '\'' && ch != ')'
			} else {
				open = ch == '"' || ch == '\'' || ch == '('
			}
		}

		if !open {
			mergedArgs = append(mergedArgs, sb.String())
			sb.Reset()
			continue
		}

		// Unterminated quote - join up two args into one.
		// Add a space between joined-up args.
		sb.WriteByte(' ')
	}

	if open {
		// Unterminated quote case.
		last := sb.String()
		mergedArgs = append(mergedArgs, last[:len(last)-1]) // remove last space
	}

	return mergedArgs
}
