package variables_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/variables"
	"github.com/stretchr/testify/require"
)

func TestScope(t *testing.T) {
	t.Parallel()

	t.Run("it returns false for unset variables", func(t *testing.T) {
		t.Parallel()

		scope := variables.NewScope()
		_, ok := scope.Get("foo")
		require.False(t, ok)
	})

	t.Run("NoOverride prevents Add from overriding an existing value", func(t *testing.T) {
		t.Parallel()

		scope := variables.NewScope()
		scope.Add("foo", "bar")
		scope.Add("foo", "baz", variables.WithActive(), variables.NoOverride())

		v, ok := scope.Get("foo")
		require.True(t, ok)
		require.Equal(t, "bar", v)

		_, ok = scope.Get("foo", variables.WithActive())
		require.False(t, ok)
	})

	t.Run("it returns a sorted list of names", func(t *testing.T) {
		t.Parallel()

		scope := variables.NewScope()
		scope.Add("a", "", variables.WithActive())
		scope.Add("z", "", variables.WithActive())
		scope.Add("e", "")
		scope.Add("b", "", variables.WithActive())

		inactive := scope.Sorted()
		require.Equal(t, []string{"a", "b", "e", "z"}, inactive)

		active := scope.Sorted(variables.WithActive())
		require.Equal(t, []string{"a", "b", "z"}, active)
	})

	for _, tt := range []struct {
		testName    string
		name        string
		value       string
		useOpts     []variables.ScopeOpt
		failGetOpts []variables.ScopeOpt
	}{
		{
			testName: "it stores inactive values",
			failGetOpts: []variables.ScopeOpt{
				variables.WithActive(),
			},
			name:  "foo",
			value: "bar",
		},
		{
			testName: "it stores active values",
			useOpts:  []variables.ScopeOpt{variables.WithActive()},
			name:     "bar",
			value:    "baz",
		},
		{
			testName: "it stores active env variables",
			useOpts: []variables.ScopeOpt{
				variables.WithActive(),
			},
			name:  "bacon",
			value: "eggs",
		},
	} {
		t.Run(tt.testName, func(t *testing.T) {
			t.Parallel()

			scope := variables.NewScope()
			ok := scope.Add(tt.name, tt.value)
			require.True(t, ok)

			for _, opt := range tt.useOpts {
				_, ok = scope.Get(tt.name, opt)
				require.False(t, ok)
				ok = scope.Add(tt.name, tt.value, opt, variables.NoOverride())
				require.False(t, ok)
				ok = scope.Add(tt.name, tt.value, opt)
				require.True(t, ok)
			}

			value, ok := scope.Get(tt.name)
			require.True(t, ok)
			require.Equal(t, tt.value, value)

			for _, opt := range tt.useOpts {
				value, ok = scope.Get(tt.name, opt)
				require.True(t, ok)
				require.Equal(t, tt.value, value)

				m := scope.Map(opt)
				value, ok = m[tt.name]
				require.True(t, ok)
				require.Equal(t, tt.value, value)
			}

			for _, opt := range tt.failGetOpts {
				_, ok = scope.Get(tt.name, opt)
				require.False(t, ok)

				m := scope.Map(opt)
				_, ok = m[tt.name]
				require.False(t, ok)
			}

			clone := scope.Clone()
			value, ok = clone.Get(tt.name)
			require.True(t, ok)
			require.Equal(t, tt.value, value)

			for _, opt := range tt.useOpts {
				value, ok = clone.Get(tt.name, opt)
				require.True(t, ok)
				require.Equal(t, tt.value, value)
			}

			scope.Remove(tt.name)
			scope.Add(tt.name, tt.value)

			for _, opt := range tt.useOpts {
				_, ok := scope.Get(tt.name, opt)
				require.False(t, ok)
			}
		})
	}

	t.Run("CombineScopes", func(t *testing.T) {
		t.Parallel()

		t.Run("it prefers left values", func(t *testing.T) {
			t.Parallel()

			scope := variables.NewScope()
			scope.Add("a", "b")

			other := variables.NewScope()
			other.Add("a", "c")

			c := variables.CombineScopes(scope, other)
			v, ok := c.Get("a")
			require.True(t, ok)
			require.Equal(t, "b", v)
		})

		t.Run("it prefers active to inactive values", func(t *testing.T) {
			t.Parallel()

			scope := variables.NewScope()
			scope.Add("active", "b")

			other := variables.NewScope()
			other.Add("active", "d", variables.WithActive())

			c := variables.CombineScopes(scope, other)
			env, ok := c.Get("active")
			require.True(t, ok)
			require.Equal(t, "d", env)
		})
	})
}
