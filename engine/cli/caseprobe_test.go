package cli

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A directory that cannot be probed is not reported as case-insensitive.
//
// Found on a Linux box, where the first thing a new user sees is:
//
//	note: /home/gilescope/data/earthstore is on a case-insensitive filesystem
//	  ...
//	  hdiutil create -size 50g -fs "Case-sensitive APFS" ...
//
// about an ext4 directory, with a remedy that is a macOS command. The store did
// not exist yet - it is created later in the build - so the probe's `WriteFile`
// failed with ENOENT and the function returned false, which its caller reads as
// "case-insensitive".
//
// **"I could not tell" was being reported as "no".** That is the same fault as
// treating an absent content digest as agreement (E81) and an absent xattr as
// equality: a probe with two outcomes for three situations, where the missing
// one is silently folded into whichever answer is nearer to hand.
//
// It is worth more than tidiness. This warning exists because a case-insensitive
// store genuinely breaks builds (E26, E27), and one that fires on machines where
// nothing is wrong is one people learn to scroll past - on exactly the platform
// where it never applies.
func TestAnUnprobableDirectoryIsNotCalledCaseInsensitive(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	// A directory that is not there, which is what a store is before the first
	// build creates it.
	missing := filepath.Join(t.TempDir(), "not-yet")

	warnCaseInsensitive(&out, cacheDir{path: missing, env: testCacheDirEnv})

	if out.Len() != 0 {
		t.Errorf("a directory that could not be probed was reported on:\n%s", out.String())
	}
}

// A directory that is genuinely case-sensitive says nothing.
//
// The arm that keeps the fix from being "never warn". On Linux this is every
// ordinary directory; on macOS it is the case-sensitive volume the note asks
// for, and a note that persisted after somebody took its advice would be worse
// than the original.
func TestACaseSensitiveDirectoryIsNotWarnedAbout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if !caseSensitiveStore(dir) {
		t.Skip("this machine's temporary directory is case-insensitive")
	}

	var out bytes.Buffer

	warnCaseInsensitive(&out, cacheDir{path: dir, env: testCacheDirEnv})

	if out.Len() != 0 {
		t.Errorf("a case-sensitive directory was warned about:\n%s", out.String())
	}
}

// The remedy suits the machine it is printed on.
//
// `hdiutil` is a macOS command, and `caseVolumeRecipe` is already split by
// platform so it never prints elsewhere - checked here rather than assumed,
// because the note that started this investigation *looked* like it had been
// written for another operating system and the recipe was the part that had
// been thought about.
func TestTheRemedySuitsThePlatform(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	warnOne(&out, cacheDir{path: "/somewhere", env: testCacheDirEnv})

	got := out.String()
	if got == "" {
		t.Fatal("the note is empty")
	}

	if strings.Contains(got, "hdiutil") && runtime.GOOS != "darwin" {
		t.Errorf("the note tells a non-macOS reader to run hdiutil:\n%s", got)
	}

	// And it has to say what is wrong wherever it is printed, since on a
	// platform with no recipe the sentence is all the reader gets.
	if !strings.Contains(got, "case-insensitive") {
		t.Errorf("the note does not say what is wrong:\n%s", got)
	}
}
