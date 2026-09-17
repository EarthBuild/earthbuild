package analyzer

import (
	"os"
	"strings"
	"testing"
)

func benchSource(b *testing.B) string {
	b.Helper()

	text, err := os.ReadFile("../../../Earthfile")
	if err != nil {
		b.Skip(err)
	}

	return string(text)
}

// BenchmarkKeystroke measures what one keystroke costs the server: DidChange
// analyzes for diagnostics and Completion analyzes again for candidates.
func BenchmarkKeystroke(b *testing.B) {
	text := benchSource(b)
	offset := strings.Index(text, "\n    BUILD +") + len("\n    BUILD +")

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_ = Analyze("Earthfile", text).Diagnostics
		_ = Analyze("Earthfile", text).Completions(offset, nil)
	}
}

func BenchmarkCompletionsOnly(b *testing.B) {
	text := benchSource(b)
	offset := strings.Index(text, "\n    BUILD +") + len("\n    BUILD +")
	doc := Analyze("Earthfile", text)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		_ = doc.Completions(offset, nil)
	}
}
