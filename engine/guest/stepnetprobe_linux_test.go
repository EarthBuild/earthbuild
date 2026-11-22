package guest

import (
	"errors"
	"strings"
	"testing"
)

// A failure that is not a name collision is not retried.
//
// The retry exists for one thing: a build runs many `earth` processes, each
// numbering namespaces from zero into a `/run/netns` they share, so the second
// to ask for `earth-s1` is told the file exists and takes the next number
// (E933). Every other failure is the same on the next attempt.
//
// It cost sixteen `ip` invocations per step in a nested build, all failing, and
// then printed the sixteenth's message - which is the first one's, sixteen forks
// later (E944).
func TestOnlyANameCollisionIsRetried(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		out  string
		want bool
	}{{
		name: "the name is taken",
		out:  `Cannot create namespace file "/run/netns/earth-s1": File exists`,
		want: true,
	}, {
		name: "no permission to make one at all",
		out:  "mkdir /var/run/netns failed: Permission denied",
	}, {
		name: "an ip that has no netns command",
		out:  "BusyBox v1.37.0 multi-call binary.\n\nUsage: ip [OPTIONS] address|route|link",
	}, {
		name: "nothing said",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := collidedOnName(tc.out); got != tc.want {
				t.Errorf("collidedOnName(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

// An `ip` without `netns` says so once, in a sentence.
//
// Alpine's `ip` is busybox, which has no `netns` command at all, and every
// nested `earth` in the corpus runs on such an image. The failure quoted the
// command's whole output, so a build's log carried twenty lines of busybox usage
// where one sentence was wanted - and the reader has to recognise a usage screen
// to know the cause (E944).
func TestAnIpWithoutNetnsIsNamed(t *testing.T) {
	t.Parallel()

	fail := func(out string) func(string, ...string) ([]byte, error) {
		return func(string, ...string) ([]byte, error) { return []byte(out), errors.New("exit status 1") }
	}

	why := netnsSupported(fail("BusyBox v1.37.0 multi-call binary.\n\nUsage: ip [OPTIONS] address|route"))
	if !strings.Contains(why, "busybox") {
		t.Errorf("the reason is %q, and busybox is what it is", why)
	}

	if strings.Contains(why, "Usage:") {
		t.Errorf("the reason quotes the usage screen it is meant to replace: %q", why)
	}

	// An `ip` that answers is not diagnosed at all.
	ok := func(string, ...string) ([]byte, error) { return nil, nil }
	if why := netnsSupported(ok); why != "" {
		t.Errorf("an ip that supports netns is refused with %q", why)
	}

	// Something else entirely is quoted, but only its first line.
	why = netnsSupported(fail("open /proc/self/ns/net: permission denied\nmore\nlines"))
	if !strings.Contains(why, "permission denied") {
		t.Errorf("the reason drops what the command said: %q", why)
	}

	if strings.Contains(why, "more") {
		t.Errorf("the reason carries every line the command printed: %q", why)
	}
}
