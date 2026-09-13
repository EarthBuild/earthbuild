package layer_test

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// applyNaively is what a stack materialises to, done the obvious way.
//
// **Deliberately stupid, and that is its whole value.** It is the independent
// account TreeFromManifests is checked against, so it is written to be correct
// by inspection rather than to be quick: layers in order, a whiteout removes a
// name and everything under it, an opaque marker empties what a directory
// inherited, and anything else is copied over whatever was there.
func applyNaively(t *testing.T, out string, layerDirs []string) {
	t.Helper()

	for _, dir := range layerDirs {
		var wh, opq, plain []string

		err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
			if err != nil || p == dir {
				return err
			}

			rel, _ := filepath.Rel(dir, p)

			switch base := filepath.Base(rel); {
			case base == ".wh..wh..opq":
				opq = append(opq, filepath.Dir(rel))
			case strings.HasPrefix(base, ".wh."):
				wh = append(wh, filepath.Join(filepath.Dir(rel), strings.TrimPrefix(base, ".wh.")))
			default:
				plain = append(plain, rel)
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		// Opaque first, so a marker cannot delete what its own layer writes.
		for _, d := range opq {
			_ = os.RemoveAll(filepath.Join(out, d))
			_ = os.MkdirAll(filepath.Join(out, d), 0o750)
		}

		gone := map[string]bool{}

		for _, p := range wh {
			gone[p] = true

			_ = os.RemoveAll(filepath.Join(out, p))
		}

		for _, rel := range plain {
			// **A marker beats its own layer's entry**, which is what the
			// engine's own view says: store/view.go asks `deleted(root, rel)`
			// before it looks for the file in that root at all. The case is not
			// hypothetical - `squashInto` concatenates a range and leaves the
			// markers, so a squashed layer holds `foo` from one member and
			// `.wh.foo` from a later one.
			if gone[rel] {
				continue
			}

			src, dst := filepath.Join(dir, rel), filepath.Join(out, rel)

			fi, err := os.Lstat(src)
			if err != nil {
				t.Fatal(err)
			}

			if fi.IsDir() {
				_ = os.MkdirAll(dst, fi.Mode().Perm())

				continue
			}

			b, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}

			_ = os.MkdirAll(filepath.Dir(dst), 0o750)
			_ = os.Remove(dst)

			if err := os.WriteFile(dst, b, fi.Mode().Perm()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// The fold equals actually doing it.
//
// **The only claim worth testing here.** 𝜏 would key a cache on "these two
// stacks materialise the same filesystem", and a merge that is wrong by one
// whiteout case makes two different filesystems collide - which is a wrong hit,
// and I3 is the one failure the design exists to prevent. Hand-built cases
// cannot establish it: this afternoon produced two tests that passed while the
// thing under them did nothing.
//
// So: random stacks, materialised for real, and the fold must agree with what
// came out. Regular files and directories only - symlinks, xattrs and ownership
// are held constant here because the merge is what is under test, and they are
// carried by the entry encoding TakeIn and the fold share.
func TestTheFoldEqualsMaterialisingTheStack(t *testing.T) {
	t.Parallel()

	for seed := range 40 {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			t.Parallel()

			r := rand.New(rand.NewPCG(uint64(seed), 0x5eed)) //nolint:gosec // a fixture, not a secret

			var (
				dirs      []string
				manifests [][]byte
				live      []string // names something has written, to whiten out
			)

			for range 1 + r.IntN(4) {
				dir := t.TempDir()

				for range 1 + r.IntN(5) {
					switch {
					case len(live) > 0 && r.IntN(4) == 0:
						// Whiteout a name something wrote - including, at times,
						// this very layer, which is the shape a squashed range
						// takes: squashInto concatenates and leaves the marker
						// beside the file it deletes.
						victim := live[r.IntN(len(live))]
						at := filepath.Join(dir, filepath.Dir(victim), ".wh."+filepath.Base(victim))
						_ = os.MkdirAll(filepath.Dir(at), 0o750)
						_ = os.WriteFile(at, nil, 0o600)

					case r.IntN(6) == 0:
						// An opaque directory.
						_ = os.MkdirAll(filepath.Join(dir, "d"), 0o750)
						_ = os.WriteFile(filepath.Join(dir, "d", ".wh..wh..opq"), nil, 0o600)

					default:
						name := fmt.Sprintf("f%d.txt", r.IntN(6))
						if r.IntN(2) == 0 {
							name = filepath.Join("d", name)
						}

						at := filepath.Join(dir, name)
						_ = os.MkdirAll(filepath.Dir(at), 0o750)
						_ = os.WriteFile(at, []byte(fmt.Sprintf("v%d", r.IntN(3))), 0o600)
						live = append(live, name)
					}
				}

				m, err := layer.Manifest(dir)
				if err != nil {
					t.Fatal(err)
				}

				dirs = append(dirs, dir)
				manifests = append(manifests, m)
			}

			out := t.TempDir()
			applyNaively(t, out, dirs)

			took, err := layer.Take(out)
			if err != nil {
				t.Fatal(err)
			}

			if got := mustFold(t, manifests); got != took.Content {
				t.Errorf("the fold gave %v and materialising gave %v"+
					"\n  a merge wrong by one case makes two different filesystems"+
					"\n  collide, which is the wrong hit I3 forbids", got, took.Content)
			}
		})
	}
}
