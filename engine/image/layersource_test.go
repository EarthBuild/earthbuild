package image_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// An image assembled from streamed layers is the image assembled from
// directories.
//
// The seam exists because who can open a layer decides where the packing runs:
// a store the host shares is read in place, and a store on a disk the guest
// owns is packed inside the sandbox and streamed out (E556). The layout must
// not be able to tell which happened - a blob is named by its contents, so an
// image that differed would be an image whose identity depended on the backend
// that built it.
func TestALayoutIsTheSameWhicheverSourceWroteIt(t *testing.T) {
	t.Parallel()

	dirs := []string{
		tree(t, map[string]string{"bin/sh": "the base\n"}),
		tree(t, map[string]string{"app/main": "what the build made\n"}),
	}

	fromDirs := filepath.Join(t.TempDir(), "direct")

	err := image.WriteLayout(fromDirs, image.Spec{Ref: "app:latest", Layers: image.FromDirs(dirs)})
	if err != nil {
		t.Fatal(err)
	}

	// The same bytes, arriving as a stream from somewhere this process cannot
	// see - which is what a guest hands over.
	streamed := make([]image.LayerSource, 0, len(dirs))

	for _, d := range dirs {
		var packed bytes.Buffer

		_, _, err = image.Pack(d, &packed)
		if err != nil {
			t.Fatal(err)
		}

		bytesOf := packed.Bytes()

		streamed = append(streamed, func(w io.Writer) error {
			_, wrote := w.Write(bytesOf)

			return wrote
		})
	}

	fromStream := filepath.Join(t.TempDir(), "streamed")

	err = image.WriteLayout(fromStream, image.Spec{Ref: "app:latest", Layers: streamed})
	if err != nil {
		t.Fatal(err)
	}

	same(t, fromDirs, fromStream)
}

// same fails unless two layouts hold the same files with the same bytes.
func same(t *testing.T, a, b string) {
	t.Helper()

	names := func(root string) map[string]string {
		out := map[string]string{}

		err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return err
			}

			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}

			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}

			out[rel] = string(b)

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		return out
	}

	first, second := names(a), names(b)

	if len(first) != len(second) {
		t.Fatalf("one layout holds %d files and the other %d", len(first), len(second))
	}

	for rel, content := range first {
		other, held := second[rel]
		if !held {
			t.Errorf("%s is in one layout and not the other", rel)

			continue
		}

		if other != content {
			t.Errorf("%s differs between the two layouts", rel)
		}
	}
}
