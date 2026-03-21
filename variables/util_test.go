package variables_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/variables"
	"github.com/stretchr/testify/require"
)

func TestParseEscapedKeyValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kv string
		k  string
		v  string
		ok bool
	}{
		{"", "", "", false},
		{"=", "", "", true},
		{"key", "key", "", false},
		{"key=", "key", "", true},
		{"key=val", "key", "val", true},
		{"key=val=value=VALUE", "key", "val=value=VALUE", true},
		{"with space=val with space", "with space", "val with space", true},
		{`\==\`, "=", `\`, true},
		{`\===`, "=", `=`, true},
		{`\==\=`, "=", `\=`, true},
		{`value=dmFsdWU=`, "value", `dmFsdWU=`, true},
		{`color\=red=yes!`, "color=red", `yes!`, true},
	}

	for _, tt := range tests {
		k, v, ok := variables.ParseKeyValue(tt.kv)
		require.Equal(t, tt.k, k)
		require.Equal(t, tt.v, v)
		require.Equal(t, tt.ok, ok)
	}
}
