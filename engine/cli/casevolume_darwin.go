package cli

import "fmt"

// caseVolumeRecipe is how to give the build cache a case-sensitive filesystem
// on macOS, as commands to run.
//
// A sparse image rather than a partition: it takes the space it uses rather
// than the space it is told, and making one needs no disk to repartition and no
// administrator. 50 GiB is a ceiling, not an allocation.
//
// Returned as strings rather than run here on purpose. Creating and mounting a
// filesystem on someone's machine is not something a build tool should do
// because a note seemed like a good moment: the user is told exactly what would
// be done and decides. The commands are what the test in this package runs, so
// they cannot rot into advice that no longer works.
func caseVolumeRecipe(image, mount, env string) []string {
	return []string{
		fmt.Sprintf(
			`hdiutil create -size 50g -fs "Case-sensitive APFS" -volname EarthBuild -type SPARSE %q`,
			image),
		fmt.Sprintf(`hdiutil attach %q -mountpoint %q`, image+".sparseimage", mount),
		fmt.Sprintf(`export %s=%s/store`, env, mount),
	}
}
