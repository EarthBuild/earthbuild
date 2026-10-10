package gatewaycrafter

import (
	"fmt"
	"sort"
	"sync"

	"github.com/EarthBuild/earthbuild/util/dockerutil"
)

// ExportCoordinator is a thread-safe data-store used for coordinating the export
// of images, and artifacts (e.g. OnPull, OnImage, and Artifact summaries).
type ExportCoordinator struct {
	imageEntries map[string]imageEntry
	// manifestListMembers maps an image's key to the keys of the images that
	// belong in the same manifest list, but that an earlier export loaded.
	manifestListMembers   map[string][]string
	localOutputSummary    []LocalOutputSummaryEntry
	artifactOutputSummary []ArtifactOutputSummaryEntry
	pushedImageSummary    []PushedImageSummaryEntry
	imgIndex              int
	m                     sync.Mutex
}

type imageEntry struct {
	manifest   *dockerutil.Manifest
	localImage string
}

// LocalOutputSummaryEntry contains a summary of output images.
type LocalOutputSummaryEntry struct {
	Target    string
	DockerTag string
	Salt      string
}

// PushedImageSummaryEntry contains a summary of images which were pushed.
type PushedImageSummaryEntry struct {
	Target    string
	DockerTag string
	Salt      string
	Pushed    bool
}

// ArtifactOutputSummaryEntry contains a summary of output artifacts.
type ArtifactOutputSummaryEntry struct {
	Target string
	Path   string
	Salt   string
}

// NewExportCoordinator returns a new ExportCoordinator.
func NewExportCoordinator() *ExportCoordinator {
	return &ExportCoordinator{
		imageEntries:        map[string]imageEntry{},
		manifestListMembers: map[string][]string{},
	}
}

// GetImage fetches an existing entry from the data-store or returns false if none exists.
func (ec *ExportCoordinator) GetImage(k string) (*dockerutil.Manifest, string, bool) {
	ec.m.Lock()
	defer ec.m.Unlock()

	v, ok := ec.imageEntries[k]

	return v.manifest, v.localImage, ok
}

// ManifestLists groups the images stored under keys that are one platform of a
// multi-platform image (they were added with a manifest) by the name of that
// multi-platform image. Each group is what the local container engine picks the
// multi-platform image's default platform from. Images added without a manifest
// are left out.
//
// A group also gets the members added for its keys with AddManifestListMembers,
// those that loaded reports as already loaded into the local container engine:
// they were loaded by an earlier export, and are not loaded again.
func (ec *ExportCoordinator) ManifestLists(
	keys []string, loaded func(key string) bool,
) (map[string][]dockerutil.Manifest, error) {
	ec.m.Lock()
	defer ec.m.Unlock()

	lists := make(map[string][]dockerutil.Manifest)

	add := func(k string) bool {
		v, ok := ec.imageEntries[k]
		if !ok {
			return false
		}

		if v.manifest == nil {
			return true
		}

		for _, m := range lists[v.localImage] {
			if m.ImageName == v.manifest.ImageName {
				return true
			}
		}

		lists[v.localImage] = append(lists[v.localImage], *v.manifest)

		return true
	}

	for _, k := range keys {
		if !add(k) {
			return nil, fmt.Errorf("unrecognized image %s", k)
		}
	}

	for _, k := range keys {
		for _, member := range ec.manifestListMembers[k] {
			if loaded(member) && !add(member) {
				return nil, fmt.Errorf("unrecognized manifest list member %s", member)
			}
		}
	}

	return lists, nil
}

// AddManifestListMembers records that the images stored under members belong
// in the same manifest list as the one stored under key, although an earlier
// export loaded them: the local multi-platform image made from key's export
// must still include them. See ManifestLists.
func (ec *ExportCoordinator) AddManifestListMembers(key string, members ...string) {
	ec.m.Lock()
	defer ec.m.Unlock()

	ec.manifestListMembers[key] = append(ec.manifestListMembers[key], members...)
}

// AddImage creates a new entry for the value under sessionID/<v'>-<uuid>
// Where v' is v without special chars.
func (ec *ExportCoordinator) AddImage(sessionID, localImage string, manifest *dockerutil.Manifest) string {
	ec.m.Lock()
	defer ec.m.Unlock()

	k := fmt.Sprintf("sess-%s/pullping:img-%d", sessionID, ec.imgIndex)
	ec.imgIndex++
	ec.imageEntries[k] = imageEntry{
		localImage: localImage,
		manifest:   manifest,
	}

	return k
}

// AddArtifactSummary adds an entry of a local target and docker tag, which is used
// to output a summary text at the end of earth execution.
func (ec *ExportCoordinator) AddArtifactSummary(target, path, salt string) {
	ec.m.Lock()
	defer ec.m.Unlock()

	ec.artifactOutputSummary = append(ec.artifactOutputSummary, ArtifactOutputSummaryEntry{
		Target: target,
		Path:   path,
		Salt:   salt,
	})
}

// GetArtifactSummary returns a list of artifact summary entries, sorted by target name.
func (ec *ExportCoordinator) GetArtifactSummary() []ArtifactOutputSummaryEntry {
	ec.m.Lock()
	entries := append([]ArtifactOutputSummaryEntry{}, ec.artifactOutputSummary...)
	ec.m.Unlock()

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Target < entries[j].Target
	})

	return entries
}

// AddLocalOutputSummary adds an entry of a local target and docker tag, which is used
// to output a summary text at the end of earth execution.
func (ec *ExportCoordinator) AddLocalOutputSummary(target, dockerTag, salt string) {
	ec.m.Lock()
	defer ec.m.Unlock()

	ec.localOutputSummary = append(ec.localOutputSummary, LocalOutputSummaryEntry{
		Target:    target,
		DockerTag: dockerTag,
		Salt:      salt,
	})
}

// GetLocalOutputSummary returns a list of output summary entries, sorted by target name.
func (ec *ExportCoordinator) GetLocalOutputSummary() []LocalOutputSummaryEntry {
	ec.m.Lock()
	entries := append([]LocalOutputSummaryEntry{}, ec.localOutputSummary...)
	ec.m.Unlock()

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Target < entries[j].Target
	})

	return entries
}

// AddPushedImageSummary adds an entry of a pushed images, which is used to output a summary text
// at the end of earth execution.
func (ec *ExportCoordinator) AddPushedImageSummary(target, dockerTag, salt string, pushed bool) {
	ec.m.Lock()
	defer ec.m.Unlock()

	ec.pushedImageSummary = append(ec.pushedImageSummary, PushedImageSummaryEntry{
		Target:    target,
		DockerTag: dockerTag,
		Salt:      salt,
		Pushed:    pushed,
	})
}

// GetPushedImageSummary returns a list of pushed image summary entries, sorted by target name.
func (ec *ExportCoordinator) GetPushedImageSummary() []PushedImageSummaryEntry {
	ec.m.Lock()
	entries := append([]PushedImageSummaryEntry{}, ec.pushedImageSummary...)
	ec.m.Unlock()

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Target < entries[j].Target
	})

	return entries
}
