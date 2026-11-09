package builder

import (
	"maps"
	"testing"
)

func TestExporterAttrs(t *testing.T) {
	t.Parallel()

	unset := func(string) string { return "" }
	if got := exporterAttrs(unset); len(got) != 0 {
		t.Fatalf("without SOURCE_DATE_EPOCH the exporter must get no attributes, got %v", got)
	}

	set := func(key string) string {
		if key == "SOURCE_DATE_EPOCH" {
			return "1762646400"
		}

		return ""
	}
	want := map[string]string{"rewrite-timestamp": "true", "source-date-epoch": "1762646400"}

	if got := exporterAttrs(set); !maps.Equal(got, want) {
		t.Fatalf("exporterAttrs = %v, want %v", got, want)
	}
}
