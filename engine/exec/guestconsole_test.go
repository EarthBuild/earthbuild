package exec

import (
	"errors"
	"strings"
	"testing"
)

// consoleSandbox is a sandbox that kept its guest's console.
type consoleSandbox struct {
	Sandbox

	tail string
}

func (c consoleSandbox) ConsoleTail() string { return c.tail }

// A guest that will not speak is quoted rather than guessed at.
//
// **The advice was a guess, and it was wrong.** A handshake timeout read "it is
// running and not speaking: check the sandbox agent is the one this build
// produced" - which sent the reader to rebuild a binary. The guest's console
// said what had actually happened: five virtio devices failed to probe with
// EBUSY. One of those is a fact and the other is a hunch, and they were the
// same message.
func TestAGuestThatWillNotSpeakIsQuoted(t *testing.T) {
	t.Parallel()

	err := errors.New("the guest did not answer the handshake within 30s")

	got := withConsole(err, consoleSandbox{tail: "virtio-mmio: probe of virtio-mmio.0 failed with error -16"})
	if !strings.Contains(got.Error(), "virtio-mmio") {
		t.Errorf("the guest's own console was not quoted: %s", got)
	}

	// The original failure survives, both as text and for errors.Is.
	if !strings.Contains(got.Error(), "handshake") {
		t.Errorf("the failure itself was lost: %s", got)
	}

	if !errors.Is(got, err) {
		t.Error("the wrapped error is no longer matchable with errors.Is")
	}
}

// A sandbox with no console adds nothing, rather than an empty heading.
func TestASandboxWithNoConsoleAddsNothing(t *testing.T) {
	t.Parallel()

	err := errors.New("boom")

	if got := withConsole(err, nil); got.Error() != "boom" {
		t.Errorf("a sandbox that keeps no console still added something: %s", got)
	}

	if got := withConsole(err, consoleSandbox{tail: ""}); got.Error() != "boom" {
		t.Errorf("an empty console still added something: %s", got)
	}
}
