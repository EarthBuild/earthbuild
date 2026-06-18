package earthfile

import (
	"strings"
	"testing"
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

func BenchmarkParse_ANTLR(b *testing.B) {
	for range b.N {
		r := namedStringReader{strings.NewReader(benchmarkEarthfile)}

		_, err := ParseOpts(FromReader(&r))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParse_Custom(b *testing.B) {
	for range b.N {
		_, err := Parse("Earthfile", benchmarkEarthfile)
		// For now we don't check err because the new parser is incomplete
		_ = err
	}
}
