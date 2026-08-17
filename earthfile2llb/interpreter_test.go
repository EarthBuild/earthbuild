package earthfile2llb

import (
	"sync"
	"testing"
)

func TestCommandRenameWarnings(t *testing.T) {
	t.Parallel()

	set := NewCommandRenameWarnings()

	// First time for file1 -> true
	if !set.Add("file1") {
		t.Errorf("expected Add(file1) to return true")
	}

	// Duplicate for file1 -> false
	if set.Add("file1") {
		t.Errorf("expected duplicate Add(file1) to return false")
	}

	// Second unique file -> true
	if !set.Add("file2") {
		t.Errorf("expected Add(file2) to return true")
	}

	// Third unique file -> true (limit is 3)
	if !set.Add("file3") {
		t.Errorf("expected Add(file3) to return true")
	}

	// Fourth unique file -> false (limit 3 reached)
	if set.Add("file4") {
		t.Errorf("expected Add(file4) to return false when maxWarnings limit reached")
	}
}

func TestCommandRenameWarnings_Concurrent(t *testing.T) {
	t.Parallel()

	set := NewCommandRenameWarnings()

	var wg sync.WaitGroup

	workers := 100

	for range workers {
		wg.Go(func() {
			set.Add("file1")
			set.Add("file2")
		})
	}

	wg.Wait()
}
