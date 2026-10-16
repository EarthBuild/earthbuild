package interp

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// signals are the names a stop signal may carry, without their SIG prefix.
//
// A table rather than a pattern, because the point of checking at all is to
// catch `SIGTERMM` - and anything shaped like a signal name passes a pattern.
// Linux's set, because that is what the sandbox runs.
var signals = map[string]struct{}{
	"ABRT": {}, "ALRM": {}, "BUS": {}, "CHLD": {}, "CLD": {}, "CONT": {},
	"FPE": {}, "HUP": {}, "ILL": {}, "INT": {}, "IO": {}, "IOT": {},
	"KILL": {}, "PIPE": {}, "POLL": {}, "PROF": {}, "PWR": {}, "QUIT": {},
	"RTMAX": {}, "RTMIN": {}, "SEGV": {}, "STKFLT": {}, "STOP": {}, "SYS": {},
	"TERM": {}, "TRAP": {}, "TSTP": {}, "TTIN": {}, "TTOU": {}, "URG": {},
	"USR1": {}, "USR2": {}, "VTALRM": {}, "WINCH": {}, "XCPU": {}, "XFSZ": {},
}

// maxSignal is the highest signal number Linux has.
const maxSignal = 64

// stopSignal reads a STOPSIGNAL argument, or says why it is not one.
//
// **Returned exactly as written.** `9` and `SIGKILL` name the same signal, and
// docker records whichever the author used; an image built here and one built
// by docker from the same Dockerfile then carry the same string. `EXPOSE` is
// normalised instead, for the opposite reason - there every other tool writes
// `8080/tcp`, so storing `8080` was the odd one out.
//
// Stricter than docker on numbers, which accepts any integer other than zero -
// including negative ones and 9000. Neither is a signal on any system, so the
// only builds this refuses are ones whose author made a mistake, and the
// alternative is a config the daemon rejects at `docker run`, long after the
// build that wrote it and with nothing pointing at the line.
func stopSignal(args []string, where string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%s: STOPSIGNAL takes one signal, and was given %d"+
			"\n  a name such as SIGTERM, or a number such as 15", where, len(args))
	}

	raw := args[0]

	if n, err := strconv.Atoi(raw); err == nil {
		if n < 1 || n > maxSignal {
			return "", fmt.Errorf("%s: STOPSIGNAL %s is not a signal number"+
				"\n  expected 1 to %d, or a name such as SIGTERM", where, raw, maxSignal)
		}

		return raw, nil
	}

	if _, ok := signals[strings.TrimPrefix(strings.ToUpper(raw), "SIG")]; ok {
		return raw, nil
	}

	return "", fmt.Errorf("%s: STOPSIGNAL %s is not a signal"+
		"\n  expected a name such as SIGTERM, or a number from 1 to %d"+
		"\n  the names this accepts are %s",
		where, raw, maxSignal, signalNames())
}

// signalNames lists what a stop signal may be called, for a refusal to quote.
//
// Sorted, so two runs of the same broken build produce the same message: the
// set behind it is a map, and a message that reorders itself is one nobody can
// diff against the last one.
func signalNames() string {
	out := make([]string, 0, len(signals))
	for name := range signals {
		out = append(out, "SIG"+name)
	}

	sort.Strings(out)

	return strings.Join(out, " ")
}
