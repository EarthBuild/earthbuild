package flagutil

import (
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/jessevdk/go-flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitFlagString(t *testing.T) {
	t.Parallel()

	type args struct {
		value []string
	}

	tests := []struct {
		name string
		want []string
		args args
	}{
		{
			name: "passing flag multiple times",
			args: args{
				value: []string{"a b"},
			},
			want: []string{"a", "b"},
		},
		{
			name: "passing values with a comma",
			args: args{
				value: []string{"a,b"},
			},
			want: []string{"a", "b"},
		},
		{
			name: "passing values with a comma and multiple flags",
			args: args{
				value: []string{"a b,c   d"},
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

	//nolint:goconst
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
			Output  string `long:"output"  short:"o"`
			Debug   bool   `long:"debug"   short:"d"`
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

		//nolint:embeddedstructfieldcheck // fieldalignment takes precedence
		type extendedOpts struct {
			Output  string `long:"output"`
			NoCache bool   `long:"no-cache"`

			baseOpts
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

	//nolint:goconst
	modFunc := func(_ string, _ *flags.Option, flagVal *string) (*string, error) {
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

//nolint:goconst
func TestParseArgArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr         error
		wantDflt        *string
		name            string
		wantKey         string
		wantDescription string
		cmd             earthfile.Command
		isBaseTarget    bool
		explicitGlobal  bool
		wantRequired    bool
		wantGlobal      bool
	}{
		{
			name: "basic arg without default",
			cmd: earthfile.Command{
				Name: "ARG",
				Args: []string{"foo"},
			},
			wantKey: "foo",
		},
		{
			name: "basic arg with default",
			cmd: earthfile.Command{
				Name: "ARG",
				Args: []string{"foo", "=", "bar"},
			},
			wantKey:  "foo",
			wantDflt: new("bar"),
		},
		{
			name: "required arg",
			cmd: earthfile.Command{
				Name: "ARG",
				Args: []string{"--required", "foo"},
			},
			wantKey:      "foo",
			wantRequired: true,
		},
		{
			name: "arg with description",
			cmd: earthfile.Command{
				Name: "ARG",
				Args: []string{`--description="Environment stage (dev, staging, prod)"`, "ENV", "=", "prod"},
			},
			wantKey:         "ENV",
			wantDflt:        new("prod"),
			wantDescription: "Environment stage (dev, staging, prod)",
		},
		{
			name: "required arg with description",
			cmd: earthfile.Command{
				Name: "ARG",
				Args: []string{"--required", `--description="Database connection URL"`, "DB_URL"},
			},
			wantKey:         "DB_URL",
			wantRequired:    true,
			wantDescription: "Database connection URL",
		},
		{
			name: "arg with unquoted description",
			cmd: earthfile.Command{
				Name: "ARG",
				Args: []string{"--description=stage", "ENV", "=", "prod"},
			},
			wantKey:         "ENV",
			wantDflt:        new("prod"),
			wantDescription: "stage",
		},
		{
			name: "arg with description containing equals",
			cmd: earthfile.Command{
				Name: "ARG",
				Args: []string{`--description="k=v format"`, "CONF", "=", "default"},
			},
			wantKey:         "CONF",
			wantDflt:        new("default"),
			wantDescription: "k=v format",
		},
		{
			name: "required arg cannot have default",
			cmd: earthfile.Command{
				Name: "ARG",
				Args: []string{"--required", "foo", "=", "bar"},
			},
			wantErr: ErrRequiredArgHasDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts, key, dflt, err := ParseArgArgs(tt.cmd, tt.isBaseTarget, tt.explicitGlobal)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantKey, key)
			assert.Equal(t, tt.wantDflt, dflt)
			assert.Equal(t, tt.wantRequired, opts.Required)
			assert.Equal(t, tt.wantGlobal, opts.Global)
			assert.Equal(t, tt.wantDescription, opts.Description)
		})
	}
}
