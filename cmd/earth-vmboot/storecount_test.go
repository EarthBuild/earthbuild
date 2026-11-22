package main

import "testing"

// A store's count separates layers from the debris of writes that were killed.
//
// **The number was a directory count wearing the word "layer".** A half-written
// layer is staged in `.<id>.partial-<n>` and renamed when whole, so a killed
// writer leaves that directory behind - and `len(entries)` counted it as a
// layer. A store reported "45,353 layer(s)" with 7M free and no way to tell how
// much of that was real, which turned a capacity question into a guess.
func TestDebrisIsCountedApartFromLayers(t *testing.T) {
	t.Parallel()

	layers, debris := splitStore([]string{
		"2e5c6835880250b0c63219172c3850eb73487cc387b2d7c50e60c384d894861a",
		".2e5c6835880250b0c63219172c3850eb73487cc387b2d7c50e60c384d894861a.partial-4152642701",
		".8cde42725f59276c0e6647a4f249e353daef8f0e50cc7f76ba8a48f709d843fa.partial-256956288",
		"6b65707400000000000000000000000000000000000000000000000000000000",
	})

	if layers != 2 {
		t.Errorf("counted %d layers, wanted 2", layers)
	}

	if debris != 2 {
		t.Errorf("counted %d unfinished writes, wanted 2", debris)
	}
}

// Anything else is left out of both, rather than guessed at.
//
// The store may hold names belonging to something else, and this count is read
// by a person deciding whether to discard a cache - so a stranger's file
// inflating either figure is worse than it being absent from both.
func TestAStrangersNameIsNeitherLayerNorDebris(t *testing.T) {
	t.Parallel()

	layers, debris := splitStore([]string{"README", "lost+found", ".hidden"})
	if layers != 0 || debris != 0 {
		t.Errorf("counted %d layers and %d debris for names that are neither", layers, debris)
	}
}
