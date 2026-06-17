package parse_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/EarthBuild/earthbuild/internal/earthfile/parse"
)

var benchmarkInput = `VERSION 0.8

build:
    FROM alpine:3.18
    RUN echo "hello"
    IF [ -f "file" ]
        RUN echo "yes"
    ELSE IF [ -f "other" ]
        RUN echo "other"
    ELSE
        RUN echo "no"
    END
    FOR arg IN foo bar
        RUN echo $arg
    END
    TRY
        RUN echo "try"
    CATCH
        RUN echo "catch"
    FINALLY
        RUN echo "finally"
    END
    WITH DOCKER --pull alpine:3.18
        RUN echo "with docker"
    END
    WAIT
        RUN echo "wait"
    END
`

func BenchmarkANTLRParser(b *testing.B) {
	tmpDir := b.TempDir()
	filePath := filepath.Join(tmpDir, "Earthfile")

	err := os.WriteFile(filePath, []byte(benchmarkInput), 0o600)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for range b.N {
		_, err := earthfile.ParseFile(filePath, true)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNewParser(b *testing.B) {
	b.ResetTimer()

	for range b.N {
		_, err := parse.Parse("Earthfile", benchmarkInput)
		if err != nil {
			b.Fatal(err)
		}
	}
}
