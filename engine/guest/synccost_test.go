package guest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// TestSyncCopyCost measures what asking the store costs against reading both
// sides. Off unless EARTH_SYNC_COST names a directory to build the fixture in,
// because the fixture is gigabytes.
//
//	EARTH_SYNC_COST=/var/tmp/sc go test ./engine/guest -run TestSyncCopyCost -v -timeout 60m
//
//nolint:paralleltest // a gigabyte fixture and a disk: one at a time
func TestSyncCopyCost(t *testing.T) {
	where := os.Getenv("EARTH_SYNC_COST")
	if where == "" {
		t.Skip("set EARTH_SYNC_COST to a directory to run this")
	}

	// 2000 x 2 MiB is 4 GiB, the shape of the tree the phase log was taken
	// over. EARTH_SYNC_COST_MB scales it down where the disk cannot hold two
	// copies of that.
	files, rounds := 2000, 3

	each := 2 << 20
	if mb := os.Getenv("EARTH_SYNC_COST_MB"); mb != "" {
		total, convErr := strconv.Atoi(mb)
		if convErr != nil {
			t.Fatalf("EARTH_SYNC_COST_MB: %v", convErr)
		}

		each = total * (1 << 20) / files
	}

	err := os.MkdirAll(where, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	storeDir := filepath.Join(where, "store")
	srcTree := filepath.Join(where, "srctree")

	t.Logf("building %d files of %d KiB in %s", files, each/1024, where)
	writeTree(t, srcTree, files, each)

	srcCap, srcManifest, err := layer.TakeManifested(srcTree)
	if err != nil {
		t.Fatal(err)
	}

	srcLayer := filepath.Join(storeDir, "layers", srcCap.ID.String())

	err = os.MkdirAll(filepath.Join(storeDir, "layers"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Rename(srcTree, srcLayer)
	if err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(storeDir, srcCap.ID, srcManifest)

	// The destination: the same bytes with different times, which is what a
	// rebuilt working tree looks like to the copy that lands on it.
	root := filepath.Join(where, "root")
	dst := filepath.Join(root, "crates")

	err = os.MkdirAll(root, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	link(t, srcLayer, dst, false)
	retime(t, dst)

	// The base the merged view reads the destination from - the whole root, so
	// its paths are the paths the merged view has, which is what a real layer
	// holds. Hardlinked, as the store's own placement is: the same bytes.
	baseCap, baseManifest, err := layer.TakeManifested(root)
	if err != nil {
		t.Fatal(err)
	}

	baseLayer := filepath.Join(storeDir, "layers", baseCap.ID.String())
	link(t, root, baseLayer, true)
	store.NoteManifest(storeDir, baseCap.ID, baseManifest)

	delta := filepath.Join(where, "delta")

	err = os.MkdirAll(delta, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{LayerDir: storeDir, bases: map[string][]ir.NodeID{"h1": {baseCap.ID}}}
	h := overlayHandle{root: root, delta: delta}

	armed := copyOpts{Sync: true, digests: s.syncDigests("h1", h)}
	bare := copyOpts{Sync: true}

	// **Asserted, not assumed.** A fixture whose paths do not line up makes
	// every lookup decline, and the two arms then measure the same code twice -
	// which is exactly what the first run of this harness did.
	probe := filepath.Join("crates", "d00", "f0000.bin")

	_, ok := armed.digests.src(filepath.Join(srcLayer, "d00", "f0000.bin"), int64(each))
	if !ok {
		t.Fatal("the source digest is not there, so this measures nothing")
	}

	_, ok = armed.digests.dst(filepath.Join(root, probe), int64(each))
	if !ok {
		t.Fatal("the destination digest is not there, so this measures nothing")
	}

	read := make([]time.Duration, 0, rounds)
	told := make([]time.Duration, 0, rounds)

	// Interleaved, so anything that drifts over the run drifts through both
	// arms rather than into one of them.
	for range rounds {
		evict(t, srcLayer, root)

		read = append(read, timeCopy(t, srcLayer, dst, bare))

		evict(t, srcLayer, root)

		told = append(told, timeCopy(t, srcLayer, dst, armed))
	}

	t.Logf("reading both sides: %v", read)
	t.Logf("asking the store:   %v", told)
	t.Logf("mean read %v, mean told %v", mean(read), mean(told))
}

// evict drops the clean page cache for the fixture, which is the difference
// between measuring a disk and measuring memory. Unprivileged, and Linux only -
// `posix_fadvise(POSIX_FADV_DONTNEED)` is what an ordinary user has. Off unless
// EARTH_SYNC_COST_EVICT is set, so the same harness gives the warm number too.
func evict(t *testing.T, trees ...string) {
	t.Helper()

	if os.Getenv("EARTH_SYNC_COST_EVICT") == "" {
		return
	}

	const prog = `
import os, sys
for root in sys.argv[1:]:
    for d, _, names in os.walk(root):
        for nm in names:
            try:
                fd = os.open(os.path.join(d, nm), os.O_RDONLY)
            except OSError:
                continue
            try:
                os.posix_fadvise(fd, 0, 0, os.POSIX_FADV_DONTNEED)
            finally:
                os.close(fd)
`

	//nolint:gosec // the arguments are this harness's own fixture paths
	out, err := exec.CommandContext(t.Context(), "python3", append([]string{"-c", prog}, trees...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("evict: %v: %s", err, out)
	}
}

func timeCopy(t *testing.T, src, dst string, opts copyOpts) time.Duration {
	t.Helper()

	at := time.Now()

	err := copyTree(src, dst, opts)
	if err != nil {
		t.Fatal(err)
	}

	return time.Since(at)
}

func mean(d []time.Duration) time.Duration {
	var total time.Duration
	for _, one := range d {
		total += one
	}

	return total / time.Duration(len(d))
}

func writeTree(t *testing.T, at string, files, each int) {
	t.Helper()

	err := os.MkdirAll(at, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, each)

	for i := range files {
		for j := range buf {
			buf[j] = byte(i + j)
		}

		dir := filepath.Join(at, fmt.Sprintf("d%02d", i%50))

		err = os.MkdirAll(dir, 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.bin", i)), buf, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func link(t *testing.T, from, to string, hard bool) {
	t.Helper()

	flag := "-a"
	if hard {
		flag = "-al"
	}

	//nolint:gosec // the arguments are this harness's own fixture paths
	out, err := exec.CommandContext(t.Context(), "cp", flag, from, to).CombinedOutput()
	if err != nil {
		t.Fatalf("cp: %v: %s", err, out)
	}
}

// retime moves every file's mtime back, which is what distinguishes a restored
// tree from the one the copy is about to land on.
func retime(t *testing.T, at string) {
	t.Helper()

	old := time.Unix(1_600_000_000, 0)

	err := filepath.Walk(at, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		return os.Chtimes(p, old, old)
	})
	if err != nil {
		t.Fatal(err)
	}
}
