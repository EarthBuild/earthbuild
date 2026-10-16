package overlay

import (
	"errors"
	"fmt"
	"regexp"
	"syscall"
)

// EnvScratchTmpfs asks for the scratch directory to be a tmpfs of the given
// size, as `4g` or `512m`.
//
// **Off unless set.** A scratch on tmpfs is worth a quarter of a build's wall
// clock (E406) and costs memory: a step's upper directory holds everything the
// step wrote, so a build producing gigabytes produces them in RAM. That is a
// trade an operator makes knowing their builds, not one this engine makes for
// them.
const EnvScratchTmpfs = "EARTH_SCRATCH_TMPFS"

// sizeLooksRight is what tmpfs's own `size=` option accepts, narrowed to the
// forms worth writing: a number and a unit.
//
// Percentages are accepted by the kernel and not here. `size=50%` of a machine
// nobody has measured is the sort of setting that works everywhere it is tried
// and fills the machine where it is not.
var sizeLooksRight = regexp.MustCompile(`^[0-9]+[kmgKMG]$`)

// scratchTmpfsOptions turns the setting into mount options, or refuses it.
//
// **A typo is refused rather than ignored.** `EARTH_SCRATCH_TMPFS=4G8` quietly
// disabling the feature is this project's most recorded failure - a mechanism
// that is not running and one that found nothing produce the same output - and
// the operator would see the old speed with nothing to explain it.
func scratchTmpfsOptions(env string) (string, error) {
	if env == "" {
		return "", nil
	}

	if !sizeLooksRight.MatchString(env) {
		return "", fmt.Errorf(
			"%s=%q is not a size: write a number and a unit, as 4g or 512m"+
				"\n  a percentage is not accepted here even though tmpfs allows one:"+
				"\n  a share of a machine nobody has measured fills the machines"+
				"\n  it was not measured on", EnvScratchTmpfs, env)
	}

	return "size=" + env, nil
}

// scratchFullHint explains an ENOSPC that a scratch tmpfs caused.
//
// The one failure mode this option introduces: a step fills the tmpfs and the
// build reports no space on a machine with terabytes free. The message names
// the setting, so whoever turned it on can raise it or turn it off.
//
// Empty for an ordinary scratch directory - there the disk really is full and
// this engine has nothing to add - and empty for every other error, on the rule
// `startHint` already follows.
func scratchFullHint(err error, opts string) string {
	if opts == "" || !errors.Is(err, syscall.ENOSPC) {
		return ""
	}

	return fmt.Sprintf(
		"\n  the scratch directory is a tmpfs of %s, which is memory rather than disk"+
			"\n  a step writes everything it produces there before it becomes a layer,"+
			"\n  so this is that step outgrowing the size %s asked for"+
			"\n  raise it or unset it; unset is the default and uses the disk",
		opts, EnvScratchTmpfs)
}
