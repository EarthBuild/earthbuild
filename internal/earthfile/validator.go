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

	return err
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
		return fmt.Errorf("%s%w", locationPrefix(ef.Version.SourceLocation), errUnexpectedVersionArgs)
	}

	// version is always last in VERSION command
	earthFileVersion := ef.Version.Args[len(ef.Version.Args)-1]

	isVersionValid := slices.Contains(validEarthfileVersions, earthFileVersion)

	if !isVersionValid {
		return fmt.Errorf("%sinvalid VERSION in Earthfile, supported versions are %s",
			locationPrefix(ef.Version.SourceLocation), getValidVersionsFormatted())
	}

	return nil
}

func locationPrefix(l *SourceLocation) string {
	if s := l.String(); s != "" {
		return s + ": "
	}

	return ""
}

func noTargetsWithSameName(ef Tree) error {
	var err error

	seenTargets := map[string]struct{}{}

	for _, t := range ef.Targets {
		if _, seen := seenTargets[t.Name]; seen {
			duplicateTargetErr := fmt.Errorf("%sduplicate target %q", locationPrefix(t.SourceLocation), t.Name)
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
			reservedTargetErr := fmt.Errorf("%sinvalid target %q: %s is a reserved target name",
				locationPrefix(t.SourceLocation), t.Name, t.Name)
			err = errors.Join(err, reservedTargetErr)
		}
	}

	return err
}
