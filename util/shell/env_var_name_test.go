package shell

import (
	"testing"
)

const envVarNameFoo = "FOO"

func TestIsValidEnvVarName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  string
		want bool
	}{
		{"empty", "", false},
		{"valid_simple", envVarNameFoo, true},
		{"valid_with_underscore", "FOO_BAR", true},
		{"valid_starts_with_underscore", "_FOO", true},
		{"invalid_starts_with_number", "1FOO", false},
		{"invalid_special_char", "FOO-BAR", false},
		{"invalid_space", "FOO BAR", false},
		{"valid_mixed_case", "Foo_Bar123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := IsValidEnvVarName(tt.val); got != tt.want {
				t.Errorf("IsValidEnvVarName(%q) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

func BenchmarkIsValidEnvVarName(b *testing.B) {
	inputs := []string{
		envVarNameFoo,
		"FOO_BAR",
		"_FOO",
		"1FOO",
		"FOO-BAR",
		"FOO BAR",
		"Foo_Bar123",
	}

	b.ResetTimer()

	for b.Loop() {
		for _, input := range inputs {
			_ = IsValidEnvVarName(input)
		}
	}
}
