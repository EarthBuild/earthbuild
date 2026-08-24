// Package main provides a script for parsing and transforming unit test output.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"text/tabwriter"
	"time"
)

type TestEvent struct {
	Time    time.Time // encodes as an RFC3339-format string
	Action  string
	Package string
	Test    string
	Output  string
	Elapsed float64 // seconds
}

func main() {
	eventsWithElapsedTimes := []TestEvent{}
	scanner := bufio.NewScanner(os.Stdin)
	passed := true

	// **What failed, kept.** The verdict below fails on any "fail" event, and
	// `go test -json` emits one per failing *package* as well as per failing
	// test - with `Test` empty. The duration table only prints events that name
	// a test, so a package that fails without a test failing (a build error, a
	// panic, a timeout, a binary that exits non-zero) set the verdict and named
	// nothing: "test(s) failed" with not one `--- FAIL` anywhere in the log.
	//
	// Seen on a real run, and it cost a round of guessing. A reporter that
	// knows what failed and does not say is worse than one that cannot tell.
	failed := []string{}

	for scanner.Scan() {
		var event TestEvent

		l := scanner.Text()

		err := json.Unmarshal([]byte(l), &event)
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}

		fmt.Print(event.Output)

		if event.Elapsed > 0 {
			eventsWithElapsedTimes = append(eventsWithElapsedTimes, event)
		}

		if event.Action == "fail" {
			passed = false

			// A package-level failure names no test. Recorded either way: the
			// test is the useful half when there is one, and the package is
			// what there is when there is not.
			where := event.Package
			if event.Test != "" {
				where += " " + event.Test
			}

			failed = append(failed, where)
		}
	}

	err := scanner.Err()
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}

	sort.Slice(eventsWithElapsedTimes, func(i, j int) bool {
		return eventsWithElapsedTimes[i].Elapsed < eventsWithElapsedTimes[j].Elapsed
	})

	fmt.Printf("\n--- Test Duration Summary ---\n")

	var buf bytes.Buffer

	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Package\tTest\tAction\tElapsed (seconds)\n")

	for _, event := range eventsWithElapsedTimes {
		if event.Test != "" {
			fmt.Fprintf(w, "%s\t%s\t%s\t%v\n", event.Package, event.Test, event.Action, event.Elapsed)
		}
	}

	w.Flush() // #nosec G104
	fmt.Print(buf.String())

	if !passed {
		fmt.Printf("\n--- What Failed ---\n")

		for _, where := range dedupe(failed) {
			fmt.Printf("  %s\n", where)
		}

		fmt.Printf("test(s) failed\n")
		os.Exit(1)
	}

	fmt.Printf("test(s) passed\n")
}

// dedupe keeps the first sighting of each entry, in order.
//
// A failing test produces a package-level failure too, so the same package
// arrives repeatedly; printing it once per test turns the one useful list into
// the thing nobody reads.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))

	for _, s := range in {
		if seen[s] {
			continue
		}

		seen[s] = true
		out = append(out, s)
	}

	return out
}
