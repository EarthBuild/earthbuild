package earthfile

import (
	"fmt"
	"os"
)

// ParseVersionFile reads the VERSION command for an Earthfile from the given file path and returns Version.
func ParseVersionFile(filePath string, opts ...ParseOption) (*Version, error) {
	b, err := os.ReadFile(filePath) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("earthfile: unable to open file '%v': %w", filePath, err)
	}

	return parseVersion(string(b), filePath, opts...)
}

// parseVersion reads the VERSION command for an Earthfile from text and returns Version.
func parseVersion(text string, name string, opts ...ParseOption) (*Version, error) {
	var cfg parseConfig
	for _, opt := range opts {
		opt(&cfg)
	}

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
			return nil, fmt.Errorf("read earthfile %s: %s", name, tok.Val)
		case itemNL, itemWS, itemComment, itemEOLComment:
			continue
		case itemVersion:
			version.SourceLocation = &SourceLocation{
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
					if cfg.enableSourceMap {
						version.SourceLocation.EndLine = argTok.Line
						version.SourceLocation.EndColumn = argTok.Col
					} else {
						version.SourceLocation = nil
					}

					return &version, nil
				default:
					return nil, fmt.Errorf("unexpected token in VERSION command: %s", argTok.Val)
				}
			}
		default:
			// Since VERSION must be the first command, any other token means there is no version command
			return nil, nil
		}
	}
}
