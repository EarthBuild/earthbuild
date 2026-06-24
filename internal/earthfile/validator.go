package earthfile

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	version00 = "0.0"
	version06 = "0.6"
	version07 = "0.7"
	version08 = "0.8"
)

// List of valid Earthfile versions.
// At some point we might want to break out Earthfile versioning
// into it's own package with some helper functions that are
// consumable from other packages.
var validEarthfileVersions = []string{
	version00, // Meant only for testing/debugging. Disables all feature flags.
	version06,
	version07,
	version08,
}

var errUnexpectedVersionArgs = errors.New(
	"unexpected VERSION arguments; should be VERSION [flags] <major-version>.<minor-version>",
)

type astValidator func(Tree) error

var astValidations = []astValidator{
	noTargetsWithSameName,
	noTargetsWithKeywords,
	validVersion,
	// TODO other checks go here
}

func validateAst(ef Tree) error {
	var err error

	for _, v := range astValidations {
		err = errors.Join(err, v(ef))
	}

	if err != nil {
		return fmt.Errorf("validation issues: %w", err)
	}

	return nil
}

func getValidVersionsFormatted() string {
	if validEarthfileVersions[0] != "0.0" {
		panic("validEarthfileVersions should start with 0.0")
	}

	var sb strings.Builder

	latestIndex := len(validEarthfileVersions) - 1
	for i := 1; i < latestIndex; i++ {
		sb.WriteString(validEarthfileVersions[i])
		sb.WriteString(", ")
	}

	sb.WriteString("or ")
	sb.WriteString(validEarthfileVersions[latestIndex])

	return sb.String()
}

func validVersion(ef Tree) error {
	// VERSION is not required in Earthfile for now
	if ef.Version == nil {
		return nil
	}

	// if VERSION is specified, it's invalid to have no args
	if len(ef.Version.Args) == 0 {
		return errUnexpectedVersionArgs
	}

	// version is always last in VERSION command
	earthFileVersion := ef.Version.Args[len(ef.Version.Args)-1]

	isVersionValid := slices.Contains(validEarthfileVersions, earthFileVersion)

	if !isVersionValid {
		return fmt.Errorf("earthfile version is invalid, supported versions are %s", getValidVersionsFormatted())
	}

	return nil
}

func noTargetsWithSameName(ef Tree) error {
	var err error

	seenTargets := map[string]struct{}{}

	for _, t := range ef.Targets {
		if _, seen := seenTargets[t.Name]; seen {
			var (
				file      string
				line, col int
			)

			if t.SourceLocation != nil {
				file = t.SourceLocation.File
				line = t.SourceLocation.StartLine
				col = t.SourceLocation.StartColumn
			}

			duplicateTargetErr := fmt.Errorf("%s line %v:%v duplicate target \"%s\"",
				file, line, col, t.Name)
			err = errors.Join(err, duplicateTargetErr)
		}

		seenTargets[t.Name] = struct{}{}
	}

	return err
}

func noTargetsWithKeywords(ef Tree) error {
	var err error

	for _, t := range ef.Targets {
		if t.Name == TargetBase {
			var (
				file      string
				line, col int
			)

			if t.SourceLocation != nil {
				file = t.SourceLocation.File
				line = t.SourceLocation.StartLine
				col = t.SourceLocation.StartColumn
			}

			reservedTargetErr := fmt.Errorf("%s line %v:%v invalid target \"%s\": %s is a reserved target name",
				file, line, col, t.Name, t.Name)
			err = errors.Join(err, reservedTargetErr)
		}
	}

	return err
}
