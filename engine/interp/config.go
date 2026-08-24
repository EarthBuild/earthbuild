package interp

import (
	"errors"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// Config is what an image says about itself: how to start it, what it exposes,
// how it is labelled.
//
// Distinct from the filesystem. These commands add nothing to the graph - they
// do not produce a layer - and everything to what the image *is*. USER is the
// one exception and lives on the operation instead, because it changes what a
// step does rather than only what the image declares.
type Config struct {
	Entrypoint []string
	Cmd        []string
	Exposed    []string
	Volumes    []string
	Labels     map[string]string
	User       string
	WorkingDir string
	Env        map[string]string
	// Healthcheck is how a running container reports its own health, nil when
	// the image says nothing about it.
	//
	// A pointer because "says nothing" and "says NONE" are different statements:
	// NONE *overrides* a healthcheck the base image declared, and an image that
	// treated the two alike would keep the base's (E486).
	Healthcheck *Healthcheck
}

// Healthcheck is a HEALTHCHECK, in the form an image config carries it.
//
// Docker's shape rather than this engine's: `Test` is `["NONE"]` or
// `["CMD-SHELL", "<command>"]`, which is what a daemon reads and what the
// reference writes. Inventing a tidier one here would mean converting at the
// point where the image is written, and that conversion is the thing E44 found
// two disagreeing copies of.
type Healthcheck struct {
	Test          []string
	Interval      time.Duration
	Timeout       time.Duration
	StartPeriod   time.Duration
	StartInterval time.Duration
	Retries       int
}

// clone copies a healthcheck, or nothing.
func (h *Healthcheck) clone() *Healthcheck {
	if h == nil {
		return nil
	}

	out := *h
	out.Test = append([]string(nil), h.Test...)

	return &out
}

// clone copies a configuration.
//
// Taken where SAVE IMAGE appears rather than at the end of the recipe: a command
// after the save belongs to whatever is saved next, if anything. Sharing the
// maps would let a later line silently change an image that was already
// declared.
func (c Config) clone() Config {
	out := c

	out.Entrypoint = append([]string(nil), c.Entrypoint...)
	out.Cmd = append([]string(nil), c.Cmd...)
	out.Exposed = append([]string(nil), c.Exposed...)
	out.Volumes = append([]string(nil), c.Volumes...)

	out.Labels = map[string]string{}
	maps.Copy(out.Labels, c.Labels)

	out.Env = map[string]string{}
	maps.Copy(out.Env, c.Env)

	out.Healthcheck = c.Healthcheck.clone()

	return out
}

// argvOf reads a command that may be written in exec form or shell form.
//
// `ENTRYPOINT ["/usr/bin/tool"]` runs the binary directly; `ENTRYPOINT
// /usr/bin/tool --serve` runs it through a shell, which is what makes
// redirections and variables work. Treating the second as an argv would produce
// an image that fails to start with "no such file or directory" naming the
// entire command line.
func argvOf(c earthfile.Command) []string {
	if c.ExecMode {
		return append([]string(nil), c.Args...)
	}

	if len(c.Args) == 0 {
		return nil
	}

	return shell(strings.Join(c.Args, " "))
}

// label parses `LABEL key=value` in either of the shapes the parser produces.
func label(args []string) (string, string, error) {
	if len(args) == 0 {
		return "", "", errors.New("LABEL needs a name")
	}

	if len(args) >= 3 && args[1] == "=" {
		return args[0], strings.Join(args[2:], " "), nil
	}

	if k, v, ok := strings.Cut(args[0], "="); ok {
		return k, v, nil
	}

	if len(args) >= 2 {
		return args[0], strings.Join(args[1:], " "), nil
	}

	return args[0], "", nil
}

// shell is how a command line becomes an argv.
//
// One place, because it is one decision: a `RUN` whose words are not already a
// list is handed to a shell, and which shell that is answers a question every
// call site would otherwise answer for itself. It is also where the image that
// ships no /bin/sh would have to be dealt with - a real case, and one worth
// having a single place to deal with.
func shell(cmd string) []string { return []string{"/bin/sh", "-c", cmd} }

// ToIR is this configuration in the form the graph and the image writers use.
//
// One conversion, because there were two: the interpreter built an
// `ir.ImageConfig` for a packed image and the front end built an OCI
// configuration for a saved one, each by hand, from the same fields. The pair
// disagreed about `Exposed` and `Volumes` for as long as anybody can tell
// (E44).
//
// Env becomes a sorted list here rather than staying a map: an image's identity
// is the digest of its configuration, and a map has no order, so an unordered
// environment would be a different image on every run from the same input.
func (c Config) ToIR() *ir.ImageConfig {
	out := &ir.ImageConfig{
		Entrypoint: append([]string(nil), c.Entrypoint...),
		Cmd:        append([]string(nil), c.Cmd...),
		WorkingDir: c.WorkingDir,
		User:       c.User,
		Labels:     c.Labels,
		Exposed:    append([]string(nil), c.Exposed...),
		Volumes:    append([]string(nil), c.Volumes...),
	}

	if c.Healthcheck != nil {
		out.Healthcheck = &ir.Healthcheck{
			Test:          append([]string(nil), c.Healthcheck.Test...),
			Interval:      c.Healthcheck.Interval,
			Timeout:       c.Healthcheck.Timeout,
			StartPeriod:   c.Healthcheck.StartPeriod,
			StartInterval: c.Healthcheck.StartInterval,
			Retries:       c.Healthcheck.Retries,
		}
	}

	for k, v := range c.Env {
		out.Env = append(out.Env, k+"="+v)
	}

	sort.Strings(out.Env)

	return out
}
