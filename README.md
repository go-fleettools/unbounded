# unbounded

**Which repositories read a ZIP entry, or decode an image, without a ceiling?**

```sh
unbounded go-odf go-pdfkit go-gfx     # organisations: listed, cloned, parsed
unbounded ./some/checkout             # a directory is scanned in place
```

```
  go-gfx/gfx          svg/svg.go:817  image.Decode and nothing in this file asks DecodeConfig first
  go-pdfkit/pdfkit    image.go:99     png.Decode and nothing in this file asks DecodeConfig first

66 repositories read, 0 NOT read
2 open a ZIP, 15 decode an image, 4 lead(s) to look at

Every hit is a LEAD. Both shapes are correct over input the program produced
itself; read each one and ask where the bytes come from.
```

## Why

Both shapes are how a few hundred bytes of input become a few gigabytes of
memory:

| | |
| --- | --- |
| `io.ReadAll` on a ZIP entry | an `.ods` of **67 206 bytes** inflated to 64 MiB and held **222.9 MiB** |
| `image.Decode` before `image.DecodeConfig` | a **75-byte** PNG claiming 10000×10000 allocated **381.6 MiB**, and the ceiling its caller passed changed nothing |

Both turned up **independently**, in two different repositories of this fleet,
on the same afternoon. The question that followed was the only interesting one:
*where else?*

⛔ **A fix does not travel.** `go-richdoc/latex` was corrected and its sibling
`go-richdoc/rst` carried the identical defect for weeks, untouched, because
nobody looked. The rule that came out of that is to sweep the siblings — this
is that rule with a program behind it instead of an intention.

It found `go-gfx/gfx`, where a **246-byte SVG** held **1.5 GiB**, and where a
hundred bytes of text declaring `width="40000" height="40000"` allocated
**6.1 GiB**. That is [go-gfx/gfx#72](https://github.com/go-gfx/gfx/pull/72).

## It is a lead, not a verdict

Both shapes are perfectly correct over input the program produced itself: a PDF
writer embedding its own logo decodes a PNG it wrote. Every hit has to be read
by somebody who knows where the bytes come from.

What the tool owes in return:

- ⛔ **it parses, it does not grep.** `io.ReadAll(` in a line of text says
  nothing about *what* is being read, and whether the thing on the other end is
  chosen by an attacker is the entire question.
- ⛔ **it names what it could not read.** A Go file that would not open, or
  would not parse, is neither a hit nor clean — and a sweep that says nothing
  about what it skipped reads exactly like one that found nothing.
- ⛔ **it prints both denominators.** "No leads" means something different in a
  fleet where nothing opens a ZIP from one where twenty do.
- ⛔ **reading nothing is a failure**, not a clean result, and exits non-zero.

## Point it at a known defect first

A scan that reports nothing is indistinguishable from a scan that could not
read. That is why a **directory** is scanned in place: so the instrument can be
checked against a checkout of the version that *had* the defect.

```sh
git clone https://github.com/go-odf/odf odf-old && git -C odf-old checkout v0.5.0
unbounded ./odf-old          # must find parse.go's unbounded io.ReadAll
git -C odf-old checkout v0.6.0
unbounded ./odf-old          # must find nothing
```

Both directions are in the test suite, against the real before-and-after source
of `go-odf/odf` and `go-pdfkit/convert`.

## What it does not cover

The image decoders are a **written-out list**, so the gap is visible rather than
implied: a third-party HEIC or JPEG 2000 reader is a decoder this does not know,
and that is a gap in the instrument rather than an absence of risk. A first
draft looked only for `image.Decode` and would have walked past every
`png.Decode` in the fleet.

A ZIP entry read through a helper in another file is also missed: the two shapes
are judged **within one file**, because that is what can be decided without
building a call graph.

## Checks

100 % statement coverage, gated in CI from the first commit — a sweeper's own
blind spots are the expensive kind, because it reports zero either way.

## Licence

BSD-3-Clause.
