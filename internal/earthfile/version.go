package earthfile

import (
	"fmt"
	"os"
)

// ParseVersionFile reads the VERSION command for an Earthfile from the given file path and returns Version.
func ParseVersionFile(filePath string) (*Version, error) {
	b, err := os.ReadFile(filePath) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("earthfile: unable to open file '%v': %w", filePath, err)
	}

	return parseVersion(string(b), filePath)
}

// parseVersion reads the VERSION command for an Earthfile from text and returns Version.
func parseVersion(text string, name string) (*Version, error) {
	l := lex(name, text)

	var version Version

	for {
		tok := l.nextItem()
		// Since VERSION must be the first command, any other token means there is no version command
		//nolint:exhaustive
		switch tok.Typ {
		case itemEOF:
			return nil, nil
		case itemError:
			return nil, &Error{
				Location: tokenLocation(name, tok),
				Msg:      tok.Val,
			}
		case itemNL, itemWS, itemComment, itemEOLComment:
			continue
		case itemVersion:
			version.SourceLocation = SourceLocation{
				File:        name,
				StartLine:   tok.Line,
				StartColumn: tok.Col,
			}
			// Parse version arguments
			for {
				argTok := l.nextItem()
				// Since we only care about a tiny subset of lexical tokens within the VERSION command and treat all
				// other tokens generically in the default case.
				//nolint:exhaustive
				switch argTok.Typ {
				case itemAtom:
					version.Args = append(version.Args, argTok.Val)
				case itemWS:
					// ignore whitespace
				case itemNL, itemComment, itemEOLComment, itemEOF:
					version.SourceLocation.EndLine = argTok.Line
					version.SourceLocation.EndColumn = argTok.Col

					return &version, nil
				case itemError:
					return nil, &Error{
						Location: tokenLocation(name, argTok),
						Msg:      argTok.Val,
					}
				default:
					return nil, &Error{
						Location: tokenLocation(name, argTok),
						Msg:      fmt.Sprintf("unexpected token in VERSION command: %s", argTok),
					}
				}
			}
		default:
			// Since VERSION must be the first command, any other token means there is no version command
			return nil, nil
		}
	}
}
