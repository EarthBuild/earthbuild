package stringutil

import "strings"

// ProcessParamsAndQuotes takes in a slice of strings, and rearranges the slices
// depending on quotes and parenthesis.
//
// For example "hello ", "wor(", "ld)" becomes "hello ", "wor( ld)".
func ProcessParamsAndQuotes(args []string) []string {
	var (
		openQuote rune
		sb        strings.Builder
		merged    = make([]string, 0, len(args))
	)

	for i, arg := range args {
		sb.WriteString(arg)

		for _, ch := range arg {
			if openQuote == 0 {
				if ch == '"' || ch == '\'' || ch == '(' {
					openQuote = ch
				}
			} else if openQuote == '"' && ch == '"' || openQuote == '\'' && ch == '\'' || openQuote == '(' && ch == ')' {
				openQuote = 0
			}
		}

		if openQuote == 0 {
			merged = append(merged, sb.String())
			sb.Reset()

			continue
		}

		// Unterminated quote - join up two args into one.
		// Add a space between joined-up args.

		if i < len(args)-1 {
			sb.WriteByte(' ')
		}
	}

	if openQuote != 0 {
		// Unterminated quote case.
		merged = append(merged, sb.String())
	}

	return merged
}
