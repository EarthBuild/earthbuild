package flagutil

import (
	"reflect"
	"testing"

	"github.com/jessevdk/go-flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func TestSplitFlagString(t *testing.T) {
	t.Parallel()

	type args struct {
		value cli.StringSlice
	}

	tests := []struct {
		name string
		want []string
		args args
	}{
		{
			name: "passing flag multiple times",
			args: args{
				value: *(cli.NewStringSlice("a b")),
			},
			want: []string{"a", "b"},
		},
		{
			name: "passing values with a comma",
			args: args{
				value: *(cli.NewStringSlice("a,b")),
			},
			want: []string{"a", "b"},
		},
		{
			name: "passing values with a comma and multiple flags",
			args: args{
				value: *(cli.NewStringSlice("a b,c   d")),
			},
			want: []string{"a", "b", "c", "d"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := SplitFlagString(tt.args.value); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitFlagString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseParams(t *testing.T) {
	t.Parallel()

	r := require.New(t)

	tests := []struct {
		in    string
		first string
		args  []string
	}{{
		"(+target/art --flag=something)",
		"+target/art",
		[]string{"--flag=something"},
	}, {
		"(+target/art --flag=something\"\")",
		"+target/art",
		[]string{"--flag=something\"\""},
	}, {
		"( \n  +target/art \t \n --flag=something\t   )",
		"+target/art",
		[]string{"--flag=something"},
	}, {
		"(+target/art --flag=something\\ --another=something)",
		"+target/art",
		[]string{"--flag=something\\ --another=something"},
	}, {
		"(+target/art --flag=something --another=something)",
		"+target/art",
		[]string{"--flag=something", "--another=something"},
	}, {
		"(+target/art --flag=\"something in quotes\")",
		"+target/art",
		[]string{"--flag=\"something in quotes\""},
	}, {
		"(+target/art --flag=\\\"something --not=in-quotes\\\")",
		"+target/art",
		[]string{"--flag=\\\"something", "--not=in-quotes\\\""},
	}, {
		"(+target/art --flag=look-ma-a-\\))",
		"+target/art",
		[]string{"--flag=look-ma-a-\\)"},
	}}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			actualFirst, actualArgs, err := ParseParams(tt.in)
			r.NoError(err)
			r.Equal(tt.first, actualFirst)
			r.Equal(tt.args, actualArgs)
		})
	}
}

func TestNegativeParseParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in string
	}{
		{"+target/art --flag=something)"},
		{"(+target/art --flag=something"},
		{"(+target/art --flag=\"something)"},
		{"(+target/art --flag=something\\)"},
		{"()"},
		{"(          \t\n   )"},
	}

	for _, tt := range tests {
		_, _, err := ParseParams(tt.in)
		assert.Error(t, err)
	}
}

func TestGetBoolFlagNames(t *testing.T) {
	t.Parallel()

	t.Run("simple struct", func(t *testing.T) {
		t.Parallel()

		type opts struct {
			Debug   bool   `long:"debug"   short:"d"`
			Output  string `long:"output"  short:"o"`
			Verbose bool   `long:"verbose" short:"v"`
		}

		flags := getBoolFlagNames(&opts{})
		assert.True(t, flags["verbose"])
		assert.True(t, flags["v"])
		assert.True(t, flags["debug"])
		assert.True(t, flags["d"])
		assert.False(t, flags["output"])
		assert.False(t, flags["o"])
	})

	t.Run("embedded struct", func(t *testing.T) {
		t.Parallel()

		type baseOpts struct {
			Debug   bool `long:"debug"   short:"d"`
			Verbose bool `long:"verbose" short:"v"`
		}

		type extendedOpts struct {
			baseOpts

			NoCache bool   `long:"no-cache"`
			Output  string `long:"output"`
		}

		flags := getBoolFlagNames(&extendedOpts{})
		assert.True(t, flags["verbose"], "should find verbose from embedded struct")
		assert.True(t, flags["v"], "should find v from embedded struct")
		assert.True(t, flags["debug"], "should find debug from embedded struct")
		assert.True(t, flags["d"], "should find d from embedded struct")
		assert.True(t, flags["no-cache"], "should find no-cache from parent struct")
		assert.False(t, flags["output"], "should not include non-boolean fields")
	})

	t.Run("nil data", func(t *testing.T) {
		t.Parallel()

		flags := getBoolFlagNames(nil)
		assert.Empty(t, flags)
	})
}

func TestPreprocessArgs(t *testing.T) {
	t.Parallel()

	modFunc := func(flagName string, opt *flags.Option, flagVal *string) (*string, error) {
		if flagVal != nil && *flagVal == "$VAR" {
			expanded := "true"
			return &expanded, nil
		}

		return flagVal, nil
	}

	t.Run("long flag with equals", func(t *testing.T) {
		t.Parallel()

		boolFlags := map[string]bool{"verbose": true}
		args := []string{"--verbose=$VAR"}
		result, err := preprocessArgs(args, boolFlags, modFunc)
		require.NoError(t, err)
		assert.Equal(t, []string{"--verbose=true"}, result)
	})

	t.Run("short flag with value", func(t *testing.T) {
		t.Parallel()

		boolFlags := map[string]bool{"v": true}
		args := []string{"-v", "$VAR"}
		result, err := preprocessArgs(args, boolFlags, modFunc)
		require.NoError(t, err)
		assert.Equal(t, []string{"-v", "true"}, result)
	})

	t.Run("short flag with equals", func(t *testing.T) {
		t.Parallel()

		boolFlags := map[string]bool{"v": true}
		args := []string{"-v=$VAR"}
		result, err := preprocessArgs(args, boolFlags, modFunc)
		require.NoError(t, err)
		assert.Equal(t, []string{"-v=true"}, result)
	})

	t.Run("clustered short flags", func(t *testing.T) {
		t.Parallel()

		boolFlags := map[string]bool{"v": true, "d": true}
		args := []string{"-vd", "$VAR"}
		result, err := preprocessArgs(args, boolFlags, modFunc)
		require.NoError(t, err)
		// Should modify the value for the last flag in the cluster
		assert.Equal(t, []string{"-vd", "true"}, result)
	})

	t.Run("non-boolean flag unchanged", func(t *testing.T) {
		t.Parallel()

		boolFlags := map[string]bool{"verbose": true}
		args := []string{"--output=file.txt", "arg1"}
		result, err := preprocessArgs(args, boolFlags, modFunc)
		require.NoError(t, err)
		assert.Equal(t, args, result)
	})
}
