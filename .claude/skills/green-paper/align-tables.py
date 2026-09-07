#!/usr/bin/env python3
"""Align markdown tables in place: pad every cell so the pipes line up.

Width is counted in codepoints, not bytes. The Green Paper uses mathematical
alphanumeric symbols (𝔅, 𝕂, ℋ) which are multi-byte but single-width in a
monospace font, so codepoint counting is the right measure and len() on bytes
is not.

Separator rows are rebuilt as dashes matching the final column width, which is
the house style: `| ------ | ------- |`, never `| --- | --- |`.

Fenced code blocks are skipped - a pipe inside a ```text block is data.

Usage: align-tables.py FILE [FILE ...]
"""

import re
import sys
import unicodedata

SEP = re.compile(r"^\s*\|[\s:|-]+\|\s*$")


def width(s):
    """Display width in monospace columns.

    Combining marks (category Mn) attach to the preceding character and occupy
    no column of their own, so 𝑟̂ is two codepoints but one column. Counting
    codepoints misaligns any row containing one.
    """
    return sum(1 for c in s if unicodedata.category(c) != "Mn")


def pad(s, w):
    return s + " " * (w - width(s))


def split_row(line):
    """Split a table row into cells, dropping the leading and trailing pipe."""
    return [c.strip() for c in line.strip().strip("|").split("|")]


def is_row(line):
    s = line.strip()

    return s.startswith("|") and s.endswith("|") and len(s) > 1


def align(block):
    """Align one contiguous run of table lines."""
    rows = [split_row(r) for r in block]
    seps = [bool(SEP.match(r)) for r in block]

    cols = max(len(r) for r in rows)
    rows = [r + [""] * (cols - len(r)) for r in rows]

    widths = [0] * cols
    for row, sep in zip(rows, seps):
        if sep:
            continue
        for i, cell in enumerate(row):
            widths[i] = max(widths[i], width(cell))

    # A column must be wide enough for a readable separator.
    widths = [max(w, 3) for w in widths]

    out = []
    for row, sep in zip(rows, seps):
        if sep:
            cells = ["-" * widths[i] for i in range(cols)]
        else:
            cells = [pad(row[i], widths[i]) for i in range(cols)]
        out.append("| " + " | ".join(cells) + " |")

    return out


def process(text):
    lines = text.split("\n")
    out = []
    i = 0
    fenced = False

    while i < len(lines):
        line = lines[i]

        if line.lstrip().startswith("```"):
            fenced = not fenced
            out.append(line)
            i += 1
            continue

        if not fenced and is_row(line):
            j = i
            while j < len(lines) and is_row(lines[j]):
                j += 1
            block = lines[i:j]
            # A table needs a separator row; otherwise it is prose containing pipes.
            if any(SEP.match(b) for b in block):
                out.extend(align(block))
            else:
                out.extend(block)
            i = j
            continue

        out.append(line)
        i += 1

    return "\n".join(out)


def main():
    if len(sys.argv) < 2:
        print(__doc__.strip(), file=sys.stderr)

        return 1

    changed = 0
    for path in sys.argv[1:]:
        with open(path, encoding="utf-8") as f:
            before = f.read()
        after = process(before)
        if after != before:
            with open(path, "w", encoding="utf-8") as f:
                f.write(after)
            changed += 1
            print(f"aligned {path}")
        else:
            print(f"unchanged {path}")

    return 0 if changed or len(sys.argv) > 1 else 1


if __name__ == "__main__":
    sys.exit(main())
