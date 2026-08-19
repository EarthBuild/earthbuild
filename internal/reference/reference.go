// Package reference defines core earth concepts such as targets, artifacts, commands,
// and import trackers, including their parsing logic.
package reference

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	targetNamePattern  = "^[a-z][a-zA-Z0-9.\\-]*$"
	commandNamePattern = "^[A-Z][A-Z0-9._]*$"
)

var (
	targetNameRegex  = regexp.MustCompile(targetNamePattern)
	commandNameRegex = regexp.MustCompile(commandNamePattern)
)

func parseReference(fullName, kindName, pattern string, regex *regexp.Regexp, isCmd bool) (Reference, error) {
	gitURL, tag, localPath, importRef, name, kind, err := parseCommon(fullName)
	if err != nil {
		return Reference{}, err
	}

	if !regex.MatchString(name) {
		return Reference{}, fmt.Errorf("%s name %s does not match %s", kindName, name, pattern)
	}

	ref := Reference{
		GitURL:    gitURL,
		Tag:       tag,
		LocalPath: localPath,
		ImportRef: importRef,
		kind:      kind,
	}

	if isCmd {
		ref.Command = name
	} else {
		ref.Target = name
	}

	return ref, nil
}

// ParseTarget parses a string into a target Reference.
func ParseTarget(fullTargetName string) (Reference, error) {
	return parseReference(fullTargetName, "target", targetNamePattern, targetNameRegex, false)
}

// ParseCommand parses a string into a command Reference.
func ParseCommand(fullCommandName string) (Reference, error) {
	return parseReference(fullCommandName, "command", commandNamePattern, commandNameRegex, true)
}

// Kind represents the type of a Reference.
type Kind int

const (
	// KindUnspecified indicates an uninitialized reference kind (falls back to field evaluation).
	KindUnspecified Kind = iota
	// KindLocalInternal represents a local internal target (+target).
	KindLocalInternal
	// KindLocalExternal represents a local external target (./dir+target).
	KindLocalExternal
	// KindRemote represents a remote target (github.com/user/repo+target).
	KindRemote
	// KindImport represents an import target (foo+target).
	KindImport
	// KindUnresolvedImport represents an unresolved import target.
	KindUnresolvedImport
	// KindDockerfile represents a converted Dockerfile target (Dockerfile+target).
	KindDockerfile
)

// Reference is a target, command, or Dockerfile reference.
type Reference struct {
	// Remote representation.
	GitURL string `json:"gitUrl,omitempty"` // e.g. "github.com/EarthBuild/earthbuild/examples/go"
	Tag    string `json:"tag,omitempty"`    // e.g. "main"
	// Local representation. E.g. in "./some/path+something" this is "./some/path".
	LocalPath string `json:"localPath,omitempty"`
	// Import representation. E.g. in "foo+bar" this is "foo".
	ImportRef string `json:"importRef,omitempty"`

	// Target is the target name. E.g. in "+something" this is "something".
	Target string `json:"target,omitempty"`
	// Command is the command name. E.g. in "+SOMETHING" this is "SOMETHING".
	Command string `json:"command,omitempty"`

	// kind is the Kind classification of the reference.
	kind Kind
}

// Name returns the target or command name of the reference.
// It returns Target if non-empty, or Command otherwise. Used when accessing
// the target or command name portion of a build step or execution target.
func (r Reference) Name() string {
	if r.Target != "" {
		return r.Target
	}

	return r.Command
}

// IsCommand returns true if the reference represents a user-defined command (+COMMAND).
func (r Reference) IsCommand() bool {
	return r.Command != ""
}

// IsTarget returns true if the reference represents a build target (+target).
func (r Reference) IsTarget() bool {
	return r.Target != ""
}

// Kind returns the Kind classification of the reference.
// It returns the stored kind if specified, or evaluates reference fields
// dynamically to classify whether it is local internal (+target), local external (./dir+target),
// remote (github.com/org/repo+target), import (alias+target), unresolved import, or Dockerfile-backed.
func (r Reference) Kind() Kind {
	if r.kind != KindUnspecified {
		return r.kind
	}

	switch {
	case r.ImportRef != "":
		if r.GitURL == "" && r.LocalPath == "" {
			return KindUnresolvedImport
		}

		return KindImport
	case r.GitURL != "":
		return KindRemote
	case r.LocalPath != "." && r.LocalPath != "":
		return KindLocalExternal
	default:
		return KindLocalInternal
	}
}

// DebugString returns a detailed key-value string representation of all fields in the reference.
// Used for internal diagnostics, logging, and error trace dumps when troubleshooting reference
// resolution or parsing failures.
func (r Reference) DebugString() string {
	if r.Kind() == KindDockerfile {
		return fmt.Sprintf("SourcePath: %q; Target: %q", r.LocalPath, r.Name())
	}

	return fmt.Sprintf("GitURL: %q; Tag: %q; LocalPath: %q; ImportRef: %q; Target: %q",
		r.GitURL, r.Tag, r.LocalPath, r.ImportRef, r.Name())
}

// String returns the standard user-facing CLI string representation of the reference
// (e.g. "+build", "./dir+target", "github.com/org/repo:tag+target", "Dockerfile+target").
// Used in console headers, user-facing error messages, and CLI output display.
func (r Reference) String() string {
	switch r.Kind() {
	case KindDockerfile:
		if r.Name() == "build" {
			return escapePlus(r.LocalPath)
		}

		return fmt.Sprintf("%s+%s", escapePlus(r.LocalPath), r.Name())
	case KindImport, KindUnresolvedImport:
		return fmt.Sprintf("%s+%s", escapePlus(r.ImportRef), r.Name())
	case KindLocalExternal:
		return fmt.Sprintf("%s+%s", escapePlus(r.LocalPath), r.Name())
	case KindRemote:
		s := escapePlus(r.GitURL)
		if r.Tag != "" {
			s += ":" + escapePlus(r.Tag)
		}

		return s + "+" + r.Name()
	case KindUnspecified, KindLocalInternal:
		return "+" + r.Name()
	}

	return "+" + r.Name()
}

// StringCanonical returns a normalized canonical string representation of the reference,
// ensuring equivalent target paths produce identical strings (e.g. omitting redundant "./"
// when LocalPath is "."). Used for target graph deduplication, build execution cache keys,
// and dependency tracking.
func (r Reference) StringCanonical() string {
	if r.Kind() == KindDockerfile {
		return r.String()
	}

	if r.GitURL != "" {
		s := escapePlus(r.GitURL)
		if r.Tag != "" {
			s += ":" + escapePlus(r.Tag)
		}

		return s + "+" + r.Name()
	}

	if r.LocalPath == "." {
		return "+" + r.Name()
	}

	if r.LocalPath == "" && r.ImportRef != "" {
		return fmt.Sprintf("%s+%s", escapePlus(r.ImportRef), r.Name())
	}

	return fmt.Sprintf("%s+%s", escapePlus(r.LocalPath), r.Name())
}

// ProjectCanonical returns a canonical string representation of only the project location
// or context portion of the reference, omitting the target or command name.
// Used for grouping targets by project location, build context deduplication, and git
// repository cloning cache keys.
func (r Reference) ProjectCanonical() string {
	if r.Kind() == KindDockerfile {
		return escapePlus(r.LocalPath)
	}

	if r.GitURL != "" {
		s := escapePlus(r.GitURL)
		if r.Tag != "" {
			s += ":" + escapePlus(r.Tag)
		}

		return s
	}

	if r.LocalPath == "." {
		return ""
	}

	if r.LocalPath == "" && r.ImportRef != "" {
		return escapePlus(r.ImportRef)
	}

	return escapePlus(r.LocalPath)
}

// NewDockerfileTarget creates a new Dockerfile reference.
func NewDockerfileTarget(sourcePath, target string) Reference {
	return Reference{
		LocalPath: sourcePath,
		Target:    target,
		kind:      KindDockerfile,
	}
}

// JoinReferences returns the result of interpreting target/command reference r2 as relative
// to parent context r1. It resolves relative local paths and remote repository URLs across
// nested build steps. Used during target resolution when expanding cross-target references
// and child Earthfile imports.
func JoinReferences(r1 Reference, r2 Reference) (Reference, error) {
	if r1.Kind() == KindUnresolvedImport || r2.Kind() == KindUnresolvedImport {
		return Reference{}, errors.New("unresolved import references cannot be joined")
	}

	gitURL := r2.GitURL
	tag := r2.Tag
	localPath := r2.LocalPath
	resKind := r2.Kind()

	name := r2.Name()
	switch {
	case r1.Kind() == KindRemote:
		if r2.Kind() == KindRemote {
			break
		}

		tag = r1.Tag
		resKind = KindRemote

		if r2.Kind() == KindLocalExternal {
			if path.IsAbs(r2.LocalPath) {
				return Reference{}, fmt.Errorf(
					"absolute path %s not supported as reference in external target context", r2.LocalPath,
				)
			}

			gitURL = path.Join(r1.GitURL, localPath)
			localPath = ""
		} else if r2.Kind() == KindLocalInternal {
			gitURL = r1.GitURL
			localPath = ""
		}
	case r2.Kind() == KindLocalExternal:
		if path.IsAbs(localPath) {
			localPath = path.Clean(localPath)
			break
		}

		localPath = path.Join(r1.LocalPath, localPath)
		if !strings.HasPrefix(localPath, ".") && !strings.HasPrefix(localPath, "/") {
			localPath = "./" + localPath
		}

		if r1.Kind() == KindRemote {
			resKind = KindRemote
		} else {
			resKind = KindLocalExternal
		}
	case r2.Kind() == KindLocalInternal:
		localPath = r1.LocalPath
		resKind = r1.Kind()
	}

	res := Reference{
		GitURL:    gitURL,
		Tag:       tag,
		LocalPath: localPath,
		ImportRef: r2.ImportRef,
		kind:      resKind,
	}
	if r2.IsCommand() {
		res.Command = r2.Command
	} else {
		res.Target = name
	}

	return res, nil
}

func parseCommon(fullName string) (gitURL, tag, localPath, importRef, name string, kind Kind, err error) {
	partsPlus, err := splitUnescapePlus(fullName)
	if err != nil {
		return "", "", "", "", "", KindLocalInternal, err
	}

	if len(partsPlus) != 2 {
		return "", "", "", "", "", KindLocalInternal, fmt.Errorf("invalid target ref %s", fullName)
	}

	if partsPlus[0] == "" {
		// Local target.
		return "", "", ".", "", partsPlus[1], KindLocalInternal, nil
	} else if strings.HasPrefix(partsPlus[0], ".") ||
		strings.HasPrefix(partsPlus[0], "~/") ||
		filepath.IsAbs(partsPlus[0]) {
		// Local external target.
		localPath := partsPlus[0]

		switch {
		case strings.HasPrefix(localPath, "~/"):
			homeDir := os.Getenv("HOME")
			localPath = homeDir + "/" + localPath[2:]
		case filepath.IsAbs(localPath):
			localPath = path.Clean(localPath)
		default:
			localPath = path.Clean(localPath)
			if localPath == "." {
				return "", "", ".", "", partsPlus[1], KindLocalInternal, nil
			}

			if !strings.HasPrefix(localPath, ".") {
				localPath = "./" + localPath
			}
		}

		return "", "", localPath, "", partsPlus[1], KindLocalExternal, nil
	}

	if strings.ContainsAny(partsPlus[0], "/:") {
		// Remote target.
		partsColon := strings.SplitN(partsPlus[0], ":", 2)
		if len(partsColon) == 2 {
			tag = partsColon[1]
		}

		return partsColon[0], tag, "", "", partsPlus[1], KindRemote, nil
	}

	// Import reference.
	return "", "", "", partsPlus[0], partsPlus[1], KindUnresolvedImport, nil
}

// splitUnescapePlus performs a split on "+" to return the target and path separately (i.e. always an array of 2).
func splitUnescapePlus(str string) ([]string, error) {
	escape := false
	ret := make([]string, 0, 2)

	word := make([]rune, 0, len(str))
	for _, c := range str {
		if escape {
			if c != '+' {
				word = append(word, '\\')
			}

			word = append(word, c)
			escape = false

			continue
		}

		switch c {
		case '\\':
			escape = true
		case '+':
			ret = append(ret, string(word))
			word = word[:0]
		default:
			word = append(word, c)
		}
	}

	if escape {
		return nil, fmt.Errorf("cannot split by +: unterminated escape sequence at the end of %s", str)
	}

	if len(word) > 0 {
		ret = append(ret, string(word))
	}

	return ret, nil
}

func escapePlus(str string) string {
	return strings.ReplaceAll(str, "+", "\\+")
}
