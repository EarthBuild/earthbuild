package earthfile2llb

import (
	"sync"
	"testing"
)

func TestCommandRenameWarningSet(t *testing.T) {
	t.Parallel()

	set := NewCommandRenameWarningSet()

	// First time for file1 -> true
	if !set.Add("file1", 2) {
		t.Errorf("expected Add(file1) to return true")
	}

	// Duplicate for file1 -> false
	if set.Add("file1", 2) {
		t.Errorf("expected duplicate Add(file1) to return false")
	}

	// Second unique file -> true (limit is 2)
	if !set.Add("file2", 2) {
		t.Errorf("expected Add(file2) to return true")
	}

	// Third unique file -> false (limit 2 reached)
	if set.Add("file3", 2) {
		t.Errorf("expected Add(file3) to return false when maxWarnings limit reached")
	}
}

func TestCommandRenameWarningSet_Concurrent(t *testing.T) {
	t.Parallel()

	set := NewCommandRenameWarningSet()

	var wg sync.WaitGroup

	workers := 100

	for range workers {
		wg.Go(func() {
			set.Add("file1", 50)
			set.Add("file2", 50)
		})
	}

	wg.Wait()
}
