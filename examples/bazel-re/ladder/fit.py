#!/usr/bin/env python3
"""Fit ms = intercept + slope*n for each arm of the ladder.

**The two numbers are a slope and an intercept, and they answer different
questions.** The intercept is what turning remote execution on costs at all -
the client's own startup is in there too, which is why it is compared between
arms rather than quoted alone. The slope is what each action costs, and it is
the number that decides whether a big build is viable.

Medians rather than means: one slow run is a machine doing something else, and
a mean lets it move the answer.
"""
import sys
from collections import defaultdict
from statistics import median


def fit(points):
    """Least squares over (n, ms), returning (slope, intercept)."""
    n = len(points)
    sx = sum(p[0] for p in points)
    sy = sum(p[1] for p in points)
    sxx = sum(p[0] * p[0] for p in points)
    sxy = sum(p[0] * p[1] for p in points)
    denom = n * sxx - sx * sx
    if denom == 0:
        return 0.0, sy / n
    slope = (n * sxy - sx * sy) / denom
    return slope, (sy - slope * sx) / n


def main() -> int:
    runs = defaultdict(list)
    for line in sys.stdin:
        parts = line.split()
        if len(parts) != 4 or parts[0] not in ("remote", "local"):
            continue
        mode, size, _, ms = parts
        runs[(mode, int(size))].append(int(ms))

    if not runs:
        print("no rows read", file=sys.stderr)
        return 1

    sizes = sorted({n for _, n in runs})
    print(
        f"{'n':>6}  {'remote ms':>11}  {'local ms':>10}  {'delta':>8}  {'per action':>11}"
    )
    for n in sizes:
        r, l = runs.get(("remote", n)), runs.get(("local", n))
        if not r or not l:
            continue
        mr, ml = median(r), median(l)
        print(
            f"{n:>6}  {mr:>11.0f}  {ml:>10.0f}  {mr - ml:>8.0f}  {(mr - ml) / n:>10.1f}ms"
        )

    print()
    for mode in ("remote", "local"):
        pts = [(n, median(v)) for (m, n), v in runs.items() if m == mode]
        if len(pts) < 2:
            continue
        slope, intercept = fit(pts)
        print(f"{mode:>6}: {slope:7.2f} ms/action + {intercept:8.0f} ms fixed")

    pts_r = [(n, median(v)) for (m, n), v in runs.items() if m == "remote"]
    pts_l = [(n, median(v)) for (m, n), v in runs.items() if m == "local"]
    if len(pts_r) >= 2 and len(pts_l) >= 2:
        sr, ir = fit(pts_r)
        sl, il = fit(pts_l)
        print()
        print(
            f"remote execution costs {sr - sl:.2f} ms per action"
            f" and {ir - il:.0f} ms fixed"
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
