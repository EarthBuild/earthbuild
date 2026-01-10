package variables_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/ast/spec"
	"github.com/EarthBuild/earthbuild/features"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/variables"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/poy/onpar"
	"github.com/poy/onpar/expect"
)

func TestCollection(topT *testing.T) {
	topT.Parallel()

	type testCtx struct {
		expect   expect.Expectation
		coll     *variables.Collection
		features *features.Features
	}

	o := onpar.BeforeEach(onpar.New(topT), func(t *testing.T) testCtx {
		t.Helper()

		expect := expect.New(t)
		f, _, err := features.Get(&spec.Version{Args: []string{"0.7"}})
		expect(err).To(not(haveOccurred()))
		_, err = f.ProcessFlags()
		expect(err).To(not(haveOccurred()))

		return testCtx{
			expect:   expect,
			features: f,
		}
	})
	defer o.Run()

	registerBaseSpecs := func(o *onpar.Onpar[testCtx, testCtx]) {
		// This is a quick and dirty workaround for registering the same specs
		// with multiple setup/teardown functions. It should be a first class
		// feature in onpar some day, but for now this will do.
		o.Spec("builtins are used for newly registered variables", func(tc testCtx) {
			name := "EARTHLY_VERSION"
			_, ok := tc.coll.Get(name, variables.WithActive())
			tc.expect(ok).To(beFalse())

			_, _, err := tc.coll.DeclareVar("EARTHLY_VERSION", variables.AsArg())
			tc.expect(err).To(not(haveOccurred()))
			v, ok := tc.coll.Get(name, variables.WithActive())
			tc.expect(ok).To(beTrue())
			tc.expect(v).To(equal("some version"))
		})
	}

	o.Group("Defaults", func() {
		o := onpar.BeforeEach(o, func(tc testCtx) testCtx {
			tc.coll = variables.NewCollection(variables.NewCollectionOpt{
				PlatformResolver: platutil.NewResolver(specs.Platform{
					Architecture: "foo",
					OS:           "bar",
					OSVersion:    "baz",
					OSFeatures:   []string{"stub"},
					Variant:      "bacon",
				}),
				BuiltinArgs: variables.DefaultArgs{
					EarthlyVersion: "some version",
				},
				Features: tc.features,
			})

			return tc
		})

		registerBaseSpecs(o)
	})

	o.Group("ArgScopeSet", func() {
		o := onpar.BeforeEach(o, func(tc testCtx) testCtx {
			tc.features.ArgScopeSet = true
			tc.coll = variables.NewCollection(variables.NewCollectionOpt{
				PlatformResolver: platutil.NewResolver(specs.Platform{
					Architecture: "foo",
					OS:           "bar",
					OSVersion:    "baz",
					OSFeatures:   []string{"stub"},
					Variant:      "bacon",
				}),
				BuiltinArgs: variables.DefaultArgs{
					EarthlyVersion: "some version",
				},
				Features: tc.features,
			})

			return tc
		})

		registerBaseSpecs(o)

		o.Spec("non-ARG variables ignore builtin values", func(tc testCtx) {
			name := "EARTHLY_VERSION"
			_, ok := tc.coll.Get(name, variables.WithActive())
			tc.expect(ok).To(beFalse())

			_, _, err := tc.coll.DeclareVar("EARTHLY_VERSION")
			tc.expect(err).To(not(haveOccurred()))
			v, ok := tc.coll.Get(name, variables.WithActive())
			tc.expect(ok).To(beTrue())
			tc.expect(v).To(equal(""))
		})
	})
}
