// Package earthfile provides the top-level functions for parsing Earthfiles from paths or readers
// into an Abstract Syntax Tree.
package earthfile

import (
	"fmt"
	"io"
)

// TargetBase is the name of the default target which is used when an
// Earthfile is parsed which does not have any targets.
const TargetBase = "base"

// ParseFile parses an earthfile into an AST.
func ParseFile(filePath string, enableSourceMap bool) (ef Earthfile, err error) {
	var opts []Opt
	if enableSourceMap {
		opts = append(opts, WithSourceMap())
	}

	return ParseOpts(FromPath(filePath), opts...)
}

// ParseOpts parses an earthfile into an AST. This is the functional option
// version, which uses option functions to change how a file is parsed.
func ParseOpts(from FromOpt, opts ...Opt) (Earthfile, error) {
	defaultPrefs := prefs{
		done: func() {},
	}

	preferences, err := from(defaultPrefs)
	if err != nil {
		return Earthfile{}, fmt.Errorf("ast: could not apply FromOpt: %w", err)
	}

	for _, opt := range opts {
		preferences, err = opt(preferences)
		if err != nil {
			return Earthfile{}, fmt.Errorf("ast: could not apply options: %w", err)
		}
	}

	defer preferences.done()

	_, err = preferences.reader.Seek(0, 0)
	if err != nil {
		return Earthfile{}, fmt.Errorf("ast: could not seek to beginning of file: %w", err)
	}

	b, err := io.ReadAll(preferences.reader)
	if err != nil {
		return Earthfile{}, fmt.Errorf("ast: could not read Earthfile for parsing: %w", err)
	}

	ef, err := Parse(preferences.reader.Name(), string(b))
	if err != nil {
		return Earthfile{}, err
	}

	// Set file path on SourceLocations if they exist and are requested
	if preferences.enableSourceMap {
		setSourceLocationFile(&ef, preferences.reader.Name())
	}

	err = validateAst(ef)
	if err != nil {
		return Earthfile{}, err
	}

	return ef, nil
}

func setSourceLocationFile(ef *Earthfile, filename string) {
	if ef.SourceLocation != nil {
		ef.SourceLocation.File = filename
	}

	if ef.Version != nil && ef.Version.SourceLocation != nil {
		ef.Version.SourceLocation.File = filename
	}

	for i := range ef.Targets {
		if ef.Targets[i].SourceLocation != nil {
			ef.Targets[i].SourceLocation.File = filename
		}

		setBlockSourceLocationFile(ef.Targets[i].Recipe, filename)
	}

	for i := range ef.Functions {
		if ef.Functions[i].SourceLocation != nil {
			ef.Functions[i].SourceLocation.File = filename
		}

		setBlockSourceLocationFile(ef.Functions[i].Recipe, filename)
	}

	setBlockSourceLocationFile(ef.BaseRecipe, filename)
}

func setBlockSourceLocationFile(block Block, filename string) {
	for i := range block {
		if block[i].SourceLocation != nil {
			block[i].SourceLocation.File = filename
		}

		if block[i].Command != nil && block[i].Command.SourceLocation != nil {
			block[i].Command.SourceLocation.File = filename
		}

		if block[i].If != nil {
			if block[i].If.SourceLocation != nil {
				block[i].If.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].If.IfBody, filename)

			for j := range block[i].If.ElseIf {
				if block[i].If.ElseIf[j].SourceLocation != nil {
					block[i].If.ElseIf[j].SourceLocation.File = filename
				}

				setBlockSourceLocationFile(block[i].If.ElseIf[j].Body, filename)
			}

			if block[i].If.ElseBody != nil {
				setBlockSourceLocationFile(*block[i].If.ElseBody, filename)
			}
		}

		if block[i].For != nil {
			if block[i].For.SourceLocation != nil {
				block[i].For.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].For.Body, filename)
		}

		if block[i].Try != nil {
			if block[i].Try.SourceLocation != nil {
				block[i].Try.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].Try.TryBody, filename)

			if block[i].Try.CatchBody != nil {
				setBlockSourceLocationFile(*block[i].Try.CatchBody, filename)
			}

			if block[i].Try.FinallyBody != nil {
				setBlockSourceLocationFile(*block[i].Try.FinallyBody, filename)
			}
		}

		if block[i].With != nil {
			if block[i].With.SourceLocation != nil {
				block[i].With.SourceLocation.File = filename
			}

			if block[i].With.Command.SourceLocation != nil {
				block[i].With.Command.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].With.Body, filename)
		}

		if block[i].Wait != nil {
			if block[i].Wait.SourceLocation != nil {
				block[i].Wait.SourceLocation.File = filename
			}

			setBlockSourceLocationFile(block[i].Wait.Body, filename)
		}
	}
}
