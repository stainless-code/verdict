# Behavioral diagram contract (opt-in, evidence-checked)

Read this file before supplying a diagram. The diagram is optional: supply one
per review at most, and only when the change's flow needs a picture that
multi-step prose cannot carry. The operator opts in via
`publish.diagrams.anchored` (default off) — when it is off, the block is simply
never drawn. Never hand-draw mermaid: there is no mermaid field, and a fenced
block in `summary` is neutralized.

You supply structure; verdict checks it and draws it. Every node and every
edge carries a quote of a NEW-side diff line inside a hunk (added or context) in
the `spec_checks` evidence shape, and the same matcher (A2) checks it. The check
proves the cited line exists; it does not prove that the line states the
relation an edge draws — that relation is your claim, and the reader sees it
labeled as anchored, not verified.

## The one field (`review.json`)

```json
"diagram": {
  "type": "sequence",
  "title": "Token refresh",
  "nodes": [
    {
      "id": "caller",
      "label": "Caller",
      "evidence": { "path": "src/auth.go", "line": 42, "quote": "func Refresh(ctx context.Context) error {" }
    },
    {
      "id": "service",
      "label": "Service",
      "evidence": { "path": "src/auth.go", "line": 58, "quote": "return s.store.Rotate(ctx, token)" }
    }
  ],
  "edges": [
    {
      "from": "caller",
      "to": "service",
      "label": "refresh",
      "evidence": { "path": "src/auth.go", "line": 42, "quote": "func Refresh(ctx context.Context) error {" }
    }
  ]
}
```

- `type` — `sequence` (call order over time), `state` (a lifecycle), or `flow`
  (a control graph). Any other type is out of scope and the block is omitted;
  there is no `gantt`, `classDiagram`, `erDiagram`, `mindmap`, or `gitGraph`.
- `title` — optional, ≤120 characters. It renders as the accessible title.
- `nodes` — 1 to 24 entries (an empty list omits the block). `id` is your lookup key (≤40 characters, never
  rendered), `label` is what the picture shows (≤120 characters), and
  `evidence` is the diff line the node stands for.
- `edges` — at most 40 entries. `from` and `to` are node ids (an end that
  names no node drops the edge), `label` is optional (≤120; an unlabeled
  sequence message shows its target's label), and `evidence` is the diff line
  that states the relation.
- `evidence` — exactly `path`, `line`, and `quote`, as in a `spec_checks` row:
  a NEW-side line inside a hunk of `full.diff` (added or context), copied
  exactly. A paraphrase never verifies, and there is no `side` or `start_line`.

## Honesty rule (every element from the diff)

A node is a thing the diff changes or directly touches; an edge is a relation
the line it quotes actually states — verdict checks only that the line exists,
so the relation's truth rests on you. Nothing illustrative, inferred, or assumed is
supplied without a line that proves it. When in doubt, omit the element — an
unproven node is dropped, and a diagram that loses most of itself is omitted
whole.

## What verdict does with it

- Every quote is matched against the pinned `full.diff`. An unanchored node is
  dropped, and so is an edge whose own quote fails or whose endpoint was
  dropped, as is any element with a missing or repeated id. The block never
  points at a node that cites nothing.
- The block is drawn only when at least half the supplied elements anchor.
  Below that it is omitted whole with one line:
  `> Behavioral diagram omitted: 4 of 6 elements did not verify or were malformed (min 50% anchored)`.
- Caps: 24 nodes, 40 edges, 120 characters per label, 8,000 bytes. Whole nodes
  and edges are dropped before the ceiling is crossed, and one `%%` comment
  inside the fence names what the caps left out, including edges whose
  endpoint the node cap removed. Past 240 nodes or 400 edges nothing is
  checked at all: those elements count as dropped.
- Labels and titles are reduced to letters, digits, spaces, and the
  punctuation `.,:!?'()+_-/@`, and node identifiers are generated (`n1`, `n2`, …). Write plain
  words; markup, arrows, and directives never survive.
- The block lands right after the computed topology, collapsed, labeled
  `Behavior flow (each element anchored to a diff line — N/M anchored)`.

A diagram never fails a review and never withholds approval. A malformed block
is a `diagram-invalid` warning — a malformed element is dropped, an
out-of-scope type omits the picture — and `--strict` promotes it like every
other warning. Tabular comparisons stay markdown tables — never
convert a matrix to a diagram.
