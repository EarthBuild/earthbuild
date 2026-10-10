package vmboot

import (
	"encoding/base64"
	"strings"
)

// argEnv carries the guest's settings on the kernel command line.
//
// **Because a guest's environment comes from its kernel, not from the process
// that started the machine.** A sandbox that spawns its guest as a child hands
// it an environment; a microVM has no such moment, so every setting the guest
// reads - tracing, the step shim, the idle timeout, hash-on-unpack - arrived
// unset and was silently ignored. The symptom is not a failure but something
// worse: an A/B whose two arms are the same arm.
//
// Base64 because the command line is space-separated and a value is not: a
// setting written plainly arrives as two parameters, the second of which looks
// like a typo.
const argEnv = "earth.env="

// maxEnv bounds what is put on the command line.
//
// `COMMAND_LINE_SIZE` is 2048 or 4096 depending on the architecture, and the
// kernel *truncates* rather than refusing - which would leave a base64 blob
// that still decodes, into settings that are half a value. Refused here, where
// the caller can say which settings it dropped.
const maxEnv = 1024

// EncodeEnv renders settings for the kernel command line, or "" for none and
// for more than will fit.
func EncodeEnv(settings []string) string {
	if len(settings) == 0 {
		return ""
	}

	out := argEnv + base64.RawURLEncoding.EncodeToString([]byte(strings.Join(settings, "\n")))
	if len(out) > maxEnv {
		return ""
	}

	return out
}

// ParseEnv reads settings back out of a kernel command line.
//
// Anything it cannot decode is no settings rather than an error: this runs in
// PID 1 of a guest that has already booted, and a malformed parameter must
// leave a machine with nothing set rather than one that will not start.
func ParseEnv(cmdline string) []string {
	for _, field := range strings.Fields(cmdline) {
		if !strings.HasPrefix(field, argEnv) {
			continue
		}

		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(field, argEnv))
		if err != nil || len(raw) == 0 {
			return nil
		}

		return strings.Split(string(raw), "\n")
	}

	return nil
}
