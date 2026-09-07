//go:build linux

package exec

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A failed boot quotes the end of the console, because the end is what went
// wrong: the start is the kernel finding its devices and is the same every
// time.
func TestTheFailureQuotesTheEndOfTheConsole(t *testing.T) {
	t.Parallel()

	var lines []string
	for i := range consoleLines * 3 {
		lines = append(lines, "line "+strconv.Itoa(i))
	}

	got := consoleTail(consoleFile(t, strings.Join(lines, "\n")))

	if !strings.Contains(got, "line "+strconv.Itoa(consoleLines*3-1)) {
		t.Error("the last line is not quoted, and it is the one that matters")
	}

	if strings.Contains(got, "line 0") {
		t.Error("the whole console is quoted, which pastes a kernel boot into an error")
	}
}

// An empty console says so, rather than quoting nothing.
//
// **They are different failures.** A guest that wrote nothing did not reach its
// own first line - the kernel did not start, or the initramfs holds no `/init` -
// and an error ending in a blank quotation reads as a missing diagnostic rather
// than as a present one.
func TestAnEmptyConsoleSaysSo(t *testing.T) {
	t.Parallel()

	got := consoleTail(consoleFile(t, ""))

	if !strings.Contains(got, "empty") {
		t.Errorf("an empty console is quoted as %q", got)
	}
}

// A console that cannot be read is its own answer, and not a panic.
func TestAnUnreadableConsoleIsReported(t *testing.T) {
	t.Parallel()

	got := consoleTail(filepath.Join(t.TempDir(), "absent.log"))

	if !strings.Contains(got, "could not be read") {
		t.Errorf("a missing console is quoted as %q", got)
	}
}

func consoleFile(t *testing.T, body string) string {
	t.Helper()

	at := filepath.Join(t.TempDir(), "console.log")

	if err := os.WriteFile(at, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	return at
}

// The guest's own words lead, whatever the kernel said afterwards.
//
// **The kernel talks last.** Resetting the machine prints another dozen lines
// after `earth-vmboot` has explained itself, so a plain tail shows a guest
// freeing memory and not the mount that failed - which is the one line the
// reader came for.
func TestTheGuestsOwnWordsAreQuotedFirst(t *testing.T) {
	t.Parallel()

	var lines []string

	lines = append(lines, "earth-vmboot: mount the layer store from /dev/vda: invalid argument")
	for i := range consoleLines * 2 {
		lines = append(lines, "[    0.1] kernel noise "+strconv.Itoa(i))
	}

	got := consoleTail(consoleFile(t, strings.Join(lines, "\n")))

	if !strings.Contains(got, "mount the layer store") {
		t.Error("the guest's own diagnosis is not quoted; only the kernel's shutdown is")
	}
}
