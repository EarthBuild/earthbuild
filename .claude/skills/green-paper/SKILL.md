---
name: green-paper
description: Conventions for writing and amending docs-internals/green-paper.md, the EarthBuild engine specification. Use when adding a section, defining a symbol or equation, changing an invariant, or reviewing a change to the Green Paper. Also covers the sibling docs (rfc, plan, experiments, test-plan) where they cite it.
---

# Writing the Green Paper

`docs-internals/green-paper.md` is the specification of the EarthBuild engine. It **asserts**.
The engine conforms to it; where code and document disagree, one is a defect and the disagreement
gets resolved rather than tolerated.

Read the document before amending it. These are its rules, not general markdown advice.

## Before you finish: the checklist

1. `python3 .claude/skills/green-paper/align-tables.py docs-internals/green-paper.md`
2. Every new symbol is defined before first use, and added to Appendix E.
3. Every new equation is numbered `(n.m)` in section order.
4. Every `§n.m` reference resolves - see "Cross-references" below.
5. A new invariant has a row in §5.1 giving both how it is *enforced* (with its level) and what
   *tests* it, or `**[GAP]**`. Prefer making a violation unrepresentable over asserting it, and
   asserting it over testing it.
6. `npx markdownlint-cli docs-internals/green-paper.md` - MD013 line-length is the only
   tolerated failure, matching the rest of `docs-internals/`.

## Tables

**Aligned.** Pad every cell so the pipes line up; rebuild separator rows as dashes matching the
final column width. Never `| --- | --- |`.

Do not hand-align. Run `align-tables.py`, which counts codepoints rather than bytes - the
document is full of mathematical alphanumerics (𝔅, 𝕂, ℋ) that are multi-byte and single-width,
so byte-length padding produces ragged output. The script skips fenced blocks, where a pipe is
data.

## Equations

Numbered `(n.m)`, in a `text` fence, referenced by number in prose:

```text
(4.5)    Κ₁(s) ≡ ℋ(0x01 ‖ ids(𝑏) ‖ 𝒮(ω) ‖ 𝒮(ε) ‖ 𝒮(π))
```

`≡` defines, `=` asserts equality. Appendix numbering is `(A.1)`, `(B.1)` and so on.

## Notation

Fixed in §1.1 and not to be extended casually:

| Class                     | Typography       | Examples      |
| ------------------------- | ---------------- | ------------- |
| sets                      | blackboard       | 𝔹 𝔻 𝕂 𝕊 𝕃 ℙ ℕ |
| persistent values         | lower-case Greek | σ ℓ κ ρ       |
| functions introduced here | upper-case Greek | Υ Σ Κ Δ Φ Λ Ω |
| imported functions        | calligraphic     | ℋ 𝒮           |
| local values              | lower-case roman | 𝑖 𝑗 𝑥 𝑦       |

A symbol that cannot be given a one-line definition pointing at its introducing equation was
never properly defined. Writing the Appendix E entry is the test; do it as you add the symbol,
not later.

**Subscripts use Unicode subscript characters, never an underscore.** Unicode has digits ₀-₉ and
a partial Latin set (ₐ ₑ ₕ ᵢ ⱼ ₖ ₗ ₘ ₙ ₒ ₚ ᵣ ₛ ₜ ᵤ ᵥ ₓ) - no `c`, no `d`, no uppercase, almost no
Greek. Where a subscript is not expressible, **change the symbol rather than fake it**: number it
when the number means something (Κ₁, Κ₂ for the L1 and L2 lookups), or use an accessor function
for a tuple component (ω(s), not s_ω - which is more precise anyway). Mixing real subscripts with
underscored ones reads as a typo and invites transcription errors. See §1.3.

## Normative language

* State requirements as facts: "Λ yields a verified result or a miss", not "Λ should try to".
* No hedging - "we might", "it would be nice", "consider" belong in the plan, not here.
* Invariants are numbered `I1`..`In` and cited by number from the plan, the experiments and the
  test plan. Renumbering an invariant means updating every citation; prefer appending.
* Assumptions live in §0.1 as `A1`..`An`, **stated apart from mechanism**. An assumption is a
  place where the specification can be true and the system still wrong; that is why they are
  segregated rather than woven in.
* Unwritten sections are marked `**[GAP]**` *in place*, never omitted silently. A gap means the
  mechanism has no normative definition and implementations may diverge - which is the condition
  the document exists to remove.

## Cross-references

Check them mechanically; they rot silently:

```bash
grep -on "§[0-9][0-9.]*" docs-internals/green-paper.md | awk -F'§' '{print $2}' | sort -u
grep -o "^#\+ [0-9A-E][0-9.]*" docs-internals/green-paper.md | sed 's/^#* //'
```

Every value in the first list must appear in the second. This has already caught two dangling
references.

**Never cite another document by line number.** Line numbers rot the moment either file is
edited: seven such citations in `scheduling.md` were stale within a day of being written, one of
them pointing at a blank line. Cite a section (`plan §2a-bis`, `green-paper §4.4`) or a stable
heading. The same applies to citing source: `file.go:123` is acceptable for code, which changes
under review, but a cross-document reference must be symbolic.

Detect the rot with:

```bash
grep -n "lines\? [0-9]" docs-internals/*.md
```

## House style

* British spelling. ASCII hyphens `-`, never en or em dashes.
* Prose is terse. The specification says what is true; the plan says why and when.
* No attribution creep: the document carries **one** style acknowledgement, in the header. Do not
  add "as the Gray Paper does" anywhere else - a specification that keeps citing its influences
  is asking permission.

## What belongs here, and what does not

| Content                                   | Home                        |
| ----------------------------------------- | --------------------------- |
| state, objects, transitions, invariants   | green-paper.md              |
| why we are doing this, deletion budget    | rfc-post-buildkit-engine.md |
| milestones, costs, sequencing, trade-offs | plan-native-engine.md       |
| measurements, kill criteria, results      | experiments-adversarial.md  |
| test mechanisms, CI gates, corpora        | test-plan.md                |

If a paragraph contains a date, a cost in engineer-weeks, or a decision that could reasonably go
the other way, it belongs in the plan and not in the specification.

## Amending an invariant

Invariants are load-bearing across four documents. To change one:

1. Change it in §5, keeping the number.
2. Update its row in §5.1 - which experiment now tests it?
3. `grep -rn "I[0-9]" docs-internals/` and update every citation.
4. If the change weakens an invariant, say so explicitly in the plan. A quietly weakened
   invariant is how a specification stops describing the system.
