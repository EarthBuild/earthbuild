package parse_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/ast"
	newast "github.com/EarthBuild/earthbuild/internal/earthfile/parse"
)

var benchmarkEarthfile = `
VERSION 0.8
FROM alpine:3.18
WORKDIR /app

build:
    RUN echo "Building..."
    COPY . .
    SAVE ARTIFACT /app/output
`

type namedStringReader struct {
	*strings.Reader
}

func (n *namedStringReader) Name() string {
	return "Earthfile"
}

func BenchmarkParse_ANTLR(b *testing.B) {
	for range b.N {
		r := namedStringReader{strings.NewReader(benchmarkEarthfile)}

		_, err := ast.ParseOpts(ast.FromReader(&r))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParse_Custom(b *testing.B) {
	for range b.N {
		_, err := newast.Parse("Earthfile", benchmarkEarthfile)
		// For now we don't check err because the new parser is incomplete
		_ = err
	}
}
