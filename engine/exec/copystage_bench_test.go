package exec

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func BenchmarkStagingATree(b *testing.B) {
	src := b.TempDir()
	for i := range 2000 {
		p := filepath.Join(src, "d"+strconv.Itoa(i/100), "f"+strconv.Itoa(i)+".txt")
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		if err != nil {
			b.Fatal(err)
		}

		err = os.WriteFile(p, make([]byte, 200), 0o600)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		dst := filepath.Join(b.TempDir(), "out")
		err := copyDirExcluding(src, dst, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}
