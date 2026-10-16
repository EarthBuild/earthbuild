package layer

import "strings"

// Only excludes everything a step did not declare it produces.
//
// **The inverse of an ignore file, and it is there for a different reason.** An
// ignore list keeps a build context from putting the machine into the key;
// this keeps a *step* from putting its own debris there. A step that says what
// it produces stops carrying what it merely disturbed - a fingerprint file, a
// log, a timestamp written into an intermediate - so two runs that produce the
// same artefact become the same layer, without the tool that wrote the debris
// having been fixed.
//
// A declared directory brings everything under it: an author naming
// `target/release` means the directory, not an empty one.
//
// **Nothing declared keeps everything**, which is what every step does today and
// what every step that says nothing continues to do. That is why this is opt-in:
// an intermediate step's real output is the filesystem the next step sees, and
// narrowing one that somebody builds on hides what they were building on.
func Only(paths []string) Excluder {
	if len(paths) == 0 {
		return nil
	}

	want := make([]string, 0, len(paths))

	for _, p := range paths {
		if p = strings.Trim(strings.TrimSpace(p), "/"); p != "" {
			want = append(want, p)
		}
	}

	if len(want) == 0 {
		return nil
	}

	return only(want)
}

// only is the excluder Only returns.
type only []string

// Excludes keeps a path that is a declared output, is inside one, or is a
// directory on the way to one.
//
// **The last of those is not tidiness.** A walk that excluded `target` would
// never descend into it and would never see `target/release/app` - which is
// how an excluder written for ignore files, where a skipped directory is meant
// to be skipped whole, gets an inclusion rule exactly backwards.
func (o only) Excludes(rel string) bool {
	for _, w := range o {
		switch {
		case rel == w:
			return false
		case strings.HasPrefix(rel, w+"/"):
			return false // inside a declared output
		case strings.HasPrefix(w, rel+"/"):
			return false // on the way to one
		}
	}

	return true
}
