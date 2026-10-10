package gatewaycrafter

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/util/dockerutil"
	"github.com/EarthBuild/earthbuild/util/platutil"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// A local multi-platform image is made from the platforms an export loads, and
// from the members an earlier export loaded, once they are loaded.
func TestExportCoordinatorManifestListsIncludeLoadedMembers(t *testing.T) {
	t.Parallel()

	// Only Parse is used, which does not need a native platform.
	platr := platutil.NewResolver(specs.Platform{})

	manifest := func(arch string) *dockerutil.Manifest {
		p, err := platr.Parse("linux/" + arch)
		if err != nil {
			t.Fatal(err)
		}

		return &dockerutil.Manifest{ImageName: "img:latest_linux_" + arch, Platform: p}
	}

	ec := NewExportCoordinator()
	amd64 := ec.AddImage("s", "img:latest", manifest("amd64"))
	arm64 := ec.AddImage("s", "img:latest", manifest("arm64"))
	single := ec.AddImage("s", "other:latest", nil)

	ec.AddManifestListMembers(arm64, amd64)

	names := func(loaded func(string) bool) []string {
		lists, err := ec.ManifestLists([]string{arm64, single}, loaded)
		if err != nil {
			t.Fatal(err)
		}

		got := make([]string, 0, len(lists["img:latest"]))
		for _, m := range lists["img:latest"] {
			got = append(got, m.ImageName)
		}

		slices.Sort(got)

		if _, ok := lists["other:latest"]; ok {
			t.Error("an image without a manifest must not be in a manifest list")
		}

		return got
	}

	got := names(func(string) bool { return false })
	if want := []string{"img:latest_linux_arm64"}; !slices.Equal(got, want) {
		t.Errorf("before the member is loaded: %v, want %v", got, want)
	}

	got = names(func(string) bool { return true })
	if want := []string{"img:latest_linux_amd64", "img:latest_linux_arm64"}; !slices.Equal(got, want) {
		t.Errorf("once the member is loaded: %v, want %v", got, want)
	}

	_, err := ec.ManifestLists([]string{"unknown"}, nil)
	if err == nil {
		t.Error("an unknown key must be an error")
	}
}
