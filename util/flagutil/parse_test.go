package flagutil

import (
	"errors"
	"reflect"
	"testing"

	"github.com/EarthBuild/earthbuild/util/hint"
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
		args args
		want []string
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

func TestLevenshteinDistance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s1       string
		s2       string
		expected int
	}{
		{"if-exist", "if-exists", 1},
		{"keep-ts", "keep-own", 3},
		{"force", "from", 3},
		{"", "test", 4},
		{"test", "", 4},
		{"same", "same", 0},
	}

	for _, tt := range tests {
		t.Run(tt.s1+"_"+tt.s2, func(t *testing.T) {
			t.Parallel()

			result := levenshteinDistance(tt.s1, tt.s2)
			if result != tt.expected {
				t.Errorf("levenshteinDistance(%q, %q) = %d; want %d", tt.s1, tt.s2, result, tt.expected)
			}
		})
	}
}

func TestExtractFlagNames(t *testing.T) {
	t.Parallel()

	type TestOpts struct {
		KeepTs   bool `long:"keep-ts"`
		KeepOwn  bool `long:"keep-own"`
		IfExists bool `long:"if-exists"`
		Force    bool `long:"force"`
		NoTag    bool // no long tag, should be ignored
	}

	opts := &TestOpts{}
	flags := extractFlagNames(opts)

	expected := []string{"keep-ts", "keep-own", "if-exists", "force"}
	if len(flags) != len(expected) {
		t.Errorf("extractFlagNames returned %d flags; want %d", len(flags), len(expected))
	}

	// Check that all expected flags are present
	flagMap := make(map[string]bool)
	for _, f := range flags {
		flagMap[f] = true
	}
	for _, exp := range expected {
		if !flagMap[exp] {
			t.Errorf("extractFlagNames missing expected flag: %s", exp)
		}
	}
}

func TestFindClosestFlag(t *testing.T) {
	t.Parallel()

	validFlags := []string{"keep-ts", "keep-own", "if-exists", "symlink-no-follow", "force"}

	tests := []struct {
		unknownFlag   string
		expectedMatch string
		shouldFind    bool
		description   string
	}{
		{"if-exist", "if-exists", true, "missing final 's'"},
		{"--if-exist", "if-exists", true, "with leading dashes"},
		{"keep-t", "keep-ts", true, "shortened version"},
		{"forc", "force", true, "missing final 'e'"},
		{"completely-different", "", false, "no close match"},
		{"xyz", "", false, "very short and different"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			t.Parallel()

			match, found := findClosestFlag(tt.unknownFlag, validFlags)
			if found != tt.shouldFind {
				t.Errorf("findClosestFlag(%q) found=%v; want %v (%s)", tt.unknownFlag, found, tt.shouldFind, tt.description)
			}
			if found && match != tt.expectedMatch {
				t.Errorf("findClosestFlag(%q) = %q; want %q (%s)", tt.unknownFlag, match, tt.expectedMatch, tt.description)
			}
		})
	}
}

func TestSuggestFlagIfUnknown(t *testing.T) {
	t.Parallel()

	type TestOpts struct {
		KeepTs   bool `long:"keep-ts"`
		KeepOwn  bool `long:"keep-own"`
		IfExists bool `long:"if-exists"`
		Force    bool `long:"force"`
	}

	opts := &TestOpts{}

	tests := []struct {
		inputError     error
		shouldHaveHint bool
		expectedHint   string
		description    string
	}{
		{
			errors.New("unknown flag `if-exist'"),
			true,
			"Did you mean '--if-exists'?",
			"typo in if-exists flag",
		},
		{
			errors.New("unknown flag `keep-t'"),
			true,
			"Did you mean '--keep-ts'?",
			"shortened keep-ts flag",
		},
		{
			errors.New("some other error"),
			false,
			"",
			"non-flag error should pass through",
		},
		{
			errors.New("unknown flag `completely-wrong-flag'"),
			false,
			"",
			"flag too different to suggest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			t.Parallel()

			result := suggestFlagIfUnknown(tt.inputError, opts)

			// Check if the result is a hint.Error
			hintErr, isHintErr := result.(*hint.Error)

			if tt.shouldHaveHint {
				if !isHintErr {
					t.Errorf("%s: expected hint error, got regular error: %v", tt.description, result)
					return
				}
				hintText := hintErr.Hint()
				if hintText != tt.expectedHint+"\n" {
					t.Errorf("%s: hint = %q; want %q", tt.description, hintText, tt.expectedHint+"\n")
				}
			} else {
				if isHintErr {
					t.Errorf("%s: expected regular error, got hint error: %v", tt.description, result)
				}
			}
		})
	}
}
