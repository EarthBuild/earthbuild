package cli

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The remedy the note offers is run, and the volume it makes is case-sensitive.
//
// This exists because advice that does not work is worse than none: an earlier
// message here suggested building for another architecture, which moved the
// failure to `exec format error` and called it a fix. A recipe printed to a user
// who is already stuck has to be the recipe that works, and the only way to know
// that a year from now is to run it.
//
// Slow by the standards of this package - it creates and attaches a disk image -
// so it is skipped in short mode. It reaches no network.
func TestTheCaseSensitiveVolumeRecipeWorks(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("creates and attaches a disk image")
	}

	_, err := osexec.LookPath("hdiutil")
	if err != nil {
		t.Skip("hdiutil is not installed")
	}

	// **A wedged image poisons every later run, so check before adding one.**
	// `hdiutil create` leaves its half-built image attached when it fails, and a
	// 50GB attachment is enough to make the next create fail the same way - so
	// one interrupted run turns this test into a ratchet that adds a zombie
	// every time it is run. Skipping says so once instead.
	if stuck := wedgedProbes(t); len(stuck) > 0 {
		t.Skipf("%d disk image(s) from an earlier run are attached and will not detach, "+
			"so `hdiutil create` here fails with \"Resource busy\": %s\n"+
			"they are held by diskimagesiod and clear on reboot",
			len(stuck), strings.Join(stuck, " "))
	}

	// Not t.TempDir for the mount point: a mounted volume is not a directory
	// the cleanup can remove, and detaching is what has to happen first.
	base := t.TempDir()
	img := filepath.Join(base, probeName)
	mount := filepath.Join(base, "mnt")

	// **Detached by image as well as by mount point.** Detaching the mount
	// point only works if the recipe got as far as mounting; a run killed
	// between `hdiutil create` and `hdiutil attach` - or one whose whole test
	// binary was killed, which is how this was found - leaves the image
	// attached with its backing file inside a `t.TempDir` that is then removed.
	//
	// The zombie attachment survives, and because the image is 50GB the *next*
	// `hdiutil create` fails with "Resource busy" - so one interrupted run
	// wedges this test until the machine is rebooted. Two of them were found
	// attached to deleted paths, and nothing short of a reboot would shift them.
	t.Cleanup(func() {
		_, _ = run(t, "hdiutil", "detach", "-force", mount)

		for _, dev := range attachedAs(t, img+".sparseimage") {
			_, _ = run(t, "hdiutil", "detach", "-force", dev)
		}
	})

	for _, line := range caseVolumeRecipe(img, mount, testCacheDirEnv) {
		// Only the commands: the recipe ends with an `export`, which is
		// something for the user's shell rather than something to run here.
		if strings.HasPrefix(line, "export ") {
			continue
		}

		out, runErr := runLine(t, line)
		if runErr != nil {
			t.Fatalf("%s\n%v\n%s", line, runErr, out)
		}
	}

	store := filepath.Join(mount, "store")

	err = os.MkdirAll(store, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	if !caseSensitiveStore(store) {
		t.Error("the volume the note tells people to make is case-insensitive")
	}
}

// probeName is the image this test builds; probeImage is what `hdiutil` calls
// it once created. Named once, because the residue check has to recognise the
// images earlier runs left behind by the same name.
const (
	probeName  = "case-probe"
	probeImage = probeName + ".sparseimage"
)

// runLine runs one line of the recipe the way a user would: through a shell, so
// the quoting in the printed command is the quoting under test.
func runLine(t *testing.T, line string) (string, error) {
	t.Helper()

	return run(t, "sh", "-c", line)
}

func run(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	out, err := osexec.CommandContext(ctx, name, args...).CombinedOutput()

	return string(out), err
}

// attachedAs names the devices `hdiutil` has attached for one image file,
// deepest first - a sparse APFS image yields both a container and the volume
// synthesised from it, and the container will not detach while the volume is up.
//
// An unreadable listing names nothing: the caller's next move is a forced
// detach, and guessing a device from a line this does not recognise would eject
// somebody else's disk.
func attachedAs(t *testing.T, image string) []string {
	t.Helper()

	out, err := run(t, "hdiutil", "info")
	if err != nil {
		return nil
	}

	var devices []string

	path := ""

	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)

		switch {
		case len(fields) == 3 && fields[0] == "image-path":
			path = fields[2]
		case len(fields) > 0 && strings.HasPrefix(fields[0], "/dev/disk") && path == image:
			devices = append(devices, fields[0])
		}
	}

	slices.Reverse(devices)

	return devices
}

// wedgedProbes names the images this test left attached on an earlier run and
// cannot take away.
//
// It tries first: an attachment whose backing file has gone usually detaches,
// and one that does is not wedged. What is reported is the residue - and the
// residue is the reason to skip, because it makes `hdiutil create` fail for
// reasons that have nothing to do with the recipe under test.
func wedgedProbes(t *testing.T) []string {
	t.Helper()

	out, err := run(t, "hdiutil", "info")
	if err != nil {
		return nil
	}

	byImage := map[string][]string{}
	path := ""

	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)

		switch {
		case len(fields) == 3 && fields[0] == "image-path":
			path = fields[2]
		case len(fields) > 0 && strings.HasPrefix(fields[0], "/dev/disk"):
			if strings.HasSuffix(path, probeImage) {
				byImage[path] = append(byImage[path], fields[0])
			}
		}
	}

	var wedged []string

	for image, devices := range byImage {
		// A live run of this test in another package copy owns its image. Only
		// one whose backing file has gone is certainly residue.
		_, statErr := os.Stat(image)
		if statErr == nil {
			continue
		}

		slices.Reverse(devices)

		for _, dev := range devices {
			_, _ = run(t, "hdiutil", "detach", "-force", dev)
		}

		if stillAttached(t, image) {
			wedged = append(wedged, image)
		}
	}

	slices.Sort(wedged)

	return wedged
}

func stillAttached(t *testing.T, image string) bool {
	t.Helper()

	out, err := run(t, "hdiutil", "info")
	if err != nil {
		return false
	}

	return strings.Contains(out, image)
}
