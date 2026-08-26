package guest

import (
	"errors"
	"fmt"
	"strings"
)

// resolveProgram is the path to exec for a step's argv[0].
//
// **A bare name is a PATH lookup, and the shim was exec'ing it literally.**
// `RUN ["python3", "--version"]` is the exec form: no shell, so nothing else
// resolves the name, and `syscall.Exec("python3", ...)` fails with `no such
// file or directory` on an image where `python3` is at `/usr/bin/python3`.
// `tests/run-exec-form.earth` runs exactly that against a distroless image,
// which is the case where there is no shell to fall back on.
//
// The lookup happens *after* the step has entered its own root, so the PATH and
// the filesystem it searches are the step's.
//
// A name containing a separator is a path and is left alone, which is what a
// shell does and what `./configure` relies on.
func resolveProgram(name string, look func(string) (string, error)) (string, error) {
	if name == "" {
		return "", errors.New("a step has no command to run")
	}

	if strings.Contains(name, "/") {
		return name, nil
	}

	at, err := look(name)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}

	return at, nil
}
