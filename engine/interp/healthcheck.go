package interp

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// readHealthcheck reads a HEALTHCHECK.
//
// Two forms: `HEALTHCHECK NONE`, and `HEALTHCHECK [options] CMD <command>`.
// The options are the daemon's own - how often, for how long, how many failures
// before the container is called unhealthy - and they mean nothing without a
// command, so `NONE` takes none of them.
//
// The command is kept as `["CMD-SHELL", "<the words, joined>"]`, which is the
// shape a daemon reads and the shape the reference writes. Not an argv: a
// healthcheck is usually a shell line - `curl -f localhost || exit 1` - and
// running it directly would fail on the `||` (E486).
func readHealthcheck(args []string, where string) (*Healthcheck, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("HEALTHCHECK at %s needs NONE or CMD", where)
	}

	if strings.EqualFold(args[0], "NONE") {
		if len(args) > 1 {
			return nil, fmt.Errorf(
				"HEALTHCHECK NONE at %s takes nothing after it, and was given %q"+
					"\n  NONE turns off whatever the base image declared; the"+
					" options belong to a CMD", where, strings.Join(args[1:], " "))
		}

		return &Healthcheck{Test: []string{"NONE"}}, nil
	}

	out := &Healthcheck{}

	i := 0

	for ; i < len(args) && strings.HasPrefix(args[i], "--"); i++ {
		name, value, joined := strings.Cut(args[i], "=")
		if !joined {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("HEALTHCHECK %s at %s needs a value",
					name, where)
			}

			i++
			value = args[i]
		}

		err := out.set(name, value, where)
		if err != nil {
			return nil, err
		}
	}

	if i >= len(args) || !strings.EqualFold(args[i], "CMD") {
		return nil, fmt.Errorf(
			"HEALTHCHECK at %s needs CMD before the command"+
				"\n  `HEALTHCHECK --interval 30s CMD curl -f localhost`, or"+
				" `HEALTHCHECK NONE`", where)
	}

	rest := args[i+1:]
	if len(rest) == 0 {
		return nil, fmt.Errorf("HEALTHCHECK CMD at %s needs a command", where)
	}

	out.Test = []string{"CMD-SHELL", strings.Join(rest, " ")}

	return out, nil
}

// set applies one option.
//
// A name this engine does not know is refused rather than skipped: an interval
// nobody read is a healthcheck running at a frequency the author did not ask
// for, and the container reports unhealthy on a schedule nobody chose.
func (h *Healthcheck) set(name, value, where string) error {
	durations := map[string]*time.Duration{
		"--interval":       &h.Interval,
		"--timeout":        &h.Timeout,
		"--start-period":   &h.StartPeriod,
		"--start-interval": &h.StartInterval,
	}

	if into, known := durations[name]; known {
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("HEALTHCHECK %s at %s: %q is not a duration"+
				"\n  written as 30s, 1m30s or 500ms", name, where, value)
		}

		*into = d

		return nil
	}

	if name == "--retries" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("HEALTHCHECK --retries at %s: %q is not a count",
				where, value)
		}

		h.Retries = n

		return nil
	}

	return fmt.Errorf("HEALTHCHECK %s at %s is not an option this engine knows"+
		"\n  it takes --interval, --timeout, --start-period, --start-interval"+
		" and --retries", name, where)
}
