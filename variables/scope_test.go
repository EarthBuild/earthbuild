package variables_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/variables"
)

func TestScope(t *testing.T) {
	t.Parallel()

	t.Run("it returns false for unset variables", func(t *testing.T) {
		t.Parallel()
		scope := variables.NewScope()

		_, ok := scope.Get("foo")
		if ok {
			t.Error("expected Get to return false for unset variable")
		}
	})

	t.Run("NoOverride prevents Add from overriding an existing value", func(t *testing.T) {
		t.Parallel()
		scope := variables.NewScope()

		scope.Add("foo", "bar")
		scope.Add("foo", "baz", variables.WithActive(), variables.NoOverride())

		v, ok := scope.Get("foo")
		if !ok {
			t.Error("expected Get to return true")
		}
		if v != "bar" {
			t.Errorf("expected value to be %q, got %q", "bar", v)
		}

		_, ok = scope.Get("foo", variables.WithActive())
		if ok {
			t.Error("expected Get with WithActive to return false")
		}
	})

	t.Run("it returns a sorted list of names", func(t *testing.T) {
		t.Parallel()
		scope := variables.NewScope()

		scope.Add("a", "", variables.WithActive())
		scope.Add("z", "", variables.WithActive())
		scope.Add("e", "")
		scope.Add("b", "", variables.WithActive())

		inactive := scope.Sorted()
		expected := []string{"a", "b", "e", "z"}
		if len(inactive) != len(expected) {
			t.Fatalf("expected sorted list to have %d elements, got %d", len(expected), len(inactive))
		}
		for i, v := range expected {
			if inactive[i] != v {
				t.Errorf("expected element %d to be %q, got %q", i, v, inactive[i])
			}
		}

		active := scope.Sorted(variables.WithActive())
		expectedActive := []string{"a", "b", "z"}
		if len(active) != len(expectedActive) {
			t.Fatalf("expected active sorted list to have %d elements, got %d", len(expectedActive), len(active))
		}
		for i, v := range expectedActive {
			if active[i] != v {
				t.Errorf("expected active element %d to be %q, got %q", i, v, active[i])
			}
		}
	})

	tests := []struct {
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
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.testName, func(t *testing.T) {
			t.Parallel()
			scope := variables.NewScope()

			ok := scope.Add(tt.name, tt.value)
			if !ok {
				t.Error("expected Add to return true")
			}

			for _, opt := range tt.useOpts {
				_, ok = scope.Get(tt.name, opt)
				if ok {
					t.Error("expected Get with opt to return false before adding with opt")
				}
				ok = scope.Add(tt.name, tt.value, opt, variables.NoOverride())
				if ok {
					t.Error("expected Add with NoOverride to return false")
				}
				ok = scope.Add(tt.name, tt.value, opt)
				if !ok {
					t.Error("expected Add with opt to return true")
				}
			}

			value, ok := scope.Get(tt.name)
			if !ok {
				t.Error("expected Get to return true")
			}
			if value != tt.value {
				t.Errorf("expected value to be %q, got %q", tt.value, value)
			}

			for _, opt := range tt.useOpts {
				value, ok = scope.Get(tt.name, opt)
				if !ok {
					t.Error("expected Get with opt to return true")
				}
				if value != tt.value {
					t.Errorf("expected value with opt to be %q, got %q", tt.value, value)
				}

				m := scope.Map(opt)
				value, ok = m[tt.name]
				if !ok {
					t.Error("expected map to contain name")
				}
				if value != tt.value {
					t.Errorf("expected map value to be %q, got %q", tt.value, value)
				}
			}

			for _, opt := range tt.failGetOpts {
				_, ok = scope.Get(tt.name, opt)
				if ok {
					t.Error("expected Get with failGetOpt to return false")
				}

				m := scope.Map(opt)
				_, ok = m[tt.name]
				if ok {
					t.Error("expected map with failGetOpt to not contain name")
				}
			}

			clone := scope.Clone()
			value, ok = clone.Get(tt.name)
			if !ok {
				t.Error("expected cloned Get to return true")
			}
			if value != tt.value {
				t.Errorf("expected cloned value to be %q, got %q", tt.value, value)
			}

			for _, opt := range tt.useOpts {
				value, ok = clone.Get(tt.name, opt)
				if !ok {
					t.Error("expected cloned Get with opt to return true")
				}
				if value != tt.value {
					t.Errorf("expected cloned value with opt to be %q, got %q", tt.value, value)
				}
			}

			scope.Remove(tt.name)
			scope.Add(tt.name, tt.value)

			for _, opt := range tt.useOpts {
				_, ok := scope.Get(tt.name, opt)
				if ok {
					t.Error("expected Get with opt to return false after Remove")
				}
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
			if !ok {
				t.Error("expected Get to return true")
			}
			if v != "b" {
				t.Errorf("expected value to be %q, got %q", "b", v)
			}
		})

		t.Run("it prefers active to inactive values", func(t *testing.T) {
			t.Parallel()
			scope := variables.NewScope()
			scope.Add("active", "b")

			other := variables.NewScope()
			other.Add("active", "d", variables.WithActive())

			c := variables.CombineScopes(scope, other)
			env, ok := c.Get("active")
			if !ok {
				t.Error("expected Get to return true")
			}
			if env != "d" {
				t.Errorf("expected value to be %q, got %q", "d", env)
			}
		})
	})
}
