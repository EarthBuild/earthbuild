# Parity log

Where the native engine is against the reference, one row per day.

**Planning is not the interesting column any more.** The corpus reports no
unimplemented construct - 491 targets across 193 Earthfiles, none blocked on
something this engine has not built - so the `linux`/`darwin` counts move only
when the corpus itself grows. Execution is the number still in question, and it
is the one to read.

`built / plan` is `linux-earthtests-run` over `linux-earthtests` from
`corpus-ratchet.txt`: how many of the `tests/*.earth` targets that the engine can
plan actually build under it.

**A row is the state at the *start* of that date**, not the end - so a row says
what the day was handed, and the difference between two rows is what the day in
between did. Recording the end of the day instead puts a day's work in the row
that already carries its date, which reads as though the day started where it
finished.

⚠︎ **`built` is a floor, not a measurement.** The gate stores one below the best
seen and only moves when somebody records a rise, so a flat column means "nobody
has bumped it", which is not the same as "nothing improved". A row whose figure
came from a real sweep says so in Notes; anything else is inherited from the
previous day.

| date       | plan linux | plan darwin | built / plan  | parity    |
| ---------- | ---------- | ----------- | ------------- | --------- |
| 2026-08-20 | 476        | 484         | 156 / 251     | 62.2%     |
| 2026-08-21 | 476        | 484         | 156 / 251     | 62.2%     |
| 2026-08-22 | 476        | 490         | 156 / 251     | 62.2%     |
| 2026-08-24 | 476        | 491         | 156 / 251     | 62.2%     |
| 2026-08-26 | 483        | 491         | 156 / 252     | 61.9%     |
| 2026-08-27 | 483        | 493         | 156 / 252     | 61.9%     |
| 2026-08-28 | 486        | 494         | 156 / 257     | 60.7%     |
| 2026-08-29 | 486        | 494         | 156 / 257     | 60.7%     |
| 2026-08-29 | 486        | 494         | **196 / 252** | **77.8%** |

## Notes

| date       | note                                                                                                                                                                                                                              |
| ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 2026-08-20 | First row. `built` 156 recorded here and unchanged since.                                                                                                                                                                         |
| 2026-08-27 | Parity falls to 60.7% without execution regressing: the denominator grew from 251 to 257 as more targets planned.                                                                                                                 |
| 2026-08-29 | Nine days at 156. Sweep run to find whether the floor understates it.                                                                                                                                                             |
| 2026-08-29 | It did, by forty. A sweep on the x86 box reports **196 of 252**; the 156 floor had not been raised since 2026-08-20, so every parity figure quoted from this file before today was 17 points low.                                 |
| 2026-08-29 | The three largest failure groups are targets whose wrapper makes their fixture first (a rename, a touch, a sed on the VERSION line), so they cannot build standalone. Ten of the 56 shortfall are denominator, not engine (E879). |
| 2026-08-29 | Rows recomputed as start-of-day; an earlier draft of this file used end-of-day and was one row out.                                                                                                                               |

## Adding a row

Read `corpus-ratchet.txt` and append. To know whether `built` is real rather than
inherited, run the sweep - it is Linux-only, pulls images and reaches the
network, and prints its failures grouped by reason with the largest group first:

```bash
EARTH_TEST_NETWORK=1 go test -tags integration ./engine/cli/ \
    -run TestHowManyEarthTestsBuild -timeout 120m -v
```

Both the tag and the variable are required and neither is optional-looking:
without `-tags integration` the file is not compiled and `go test` reports
`no tests to run` and **passes**, and without `EARTH_TEST_NETWORK=1` the test
skips itself. Either way the run is green and measures nothing.

That grouping is the work list: the biggest group is the next thing to fix.
