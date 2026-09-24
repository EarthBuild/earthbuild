package earthfile_test

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// BenchmarkParseThisRepo parses the largest Earthfile to hand.
//
// `earth ls` reads one file, parses it and prints the names, and measured 0.33s
// against 0.05s of process startup - so nearly all of it is here, on 78 KB.
func BenchmarkParseThisRepo(b *testing.B) {
	src, err := os.ReadFile("../../Earthfile")
	if err != nil {
		b.Skip(err)
	}

	text := string(src)

	b.SetBytes(int64(len(text)))
	b.ResetTimer()

	for b.Loop() {
		if _, err := earthfile.Parse("Earthfile", text, earthfile.WithSourceMap()); err != nil {
			b.Fatal(err)
		}
	}
}
