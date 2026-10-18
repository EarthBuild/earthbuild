//go:build !darwin

package cli

// caseVolumeRecipe has nothing to offer away from macOS.
//
// A case-insensitive store elsewhere is a mount someone chose - a network share,
// a vfat volume - and the remedy is to choose differently, which no fixed set of
// commands can express. Saying nothing beats inventing a recipe for a filesystem
// this code cannot see.
func caseVolumeRecipe(_, _, _ string) []string { return nil }
