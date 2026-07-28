package buildkitd

import (
	"strings"
	"testing"
)

func TestParseBoolEnv(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		set     bool
		want    bool
		wantErr bool
	}{
		{name: "unset", set: false, want: false},
		{name: "empty", set: true, value: "", want: false},
		{name: "word", set: true, value: "TRUE", want: true},
		{name: "letter", set: true, value: "t", want: true},
		{name: "digit", set: true, value: "1", want: true},
		{name: "negative-word", set: true, value: "False", want: false},
		{name: "negative-digit", set: true, value: "0", want: false},
		{name: "garbage", set: true, value: "yes", want: false, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.set {
				t.Setenv("EARTHLY_TEST_BOOL", test.value)
			}

			got, err := parseBoolEnv("EARTHLY_TEST_BOOL")
			if (err != nil) != test.wantErr {
				t.Fatalf("parseBoolEnv() error = %v, wantErr %v", err, test.wantErr)
			}

			if got != test.want {
				t.Fatalf("parseBoolEnv() = %v, want %v", got, test.want)
			}

			// The message must name the variable and the offending value, so the
			// user can fix it without grepping the source.
			if err == nil {
				return
			}

			if !strings.Contains(err.Error(), "EARTHLY_TEST_BOOL") ||
				!strings.Contains(err.Error(), test.value) {
				t.Fatalf("parseBoolEnv() error = %q, want it to name key and value", err)
			}
		})
	}
}
