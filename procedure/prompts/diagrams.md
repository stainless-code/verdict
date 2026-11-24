# Behavioral diagram contract (opt-in, evidence-checked)

Read this file before supplying a diagram. The diagram is optional: supply one
per review at most, and only when the change's flow needs a picture that
multi-step prose cannot carry. The operator opts in via
`publish.diagrams.anchored` (default off) — when it is off, the block is simply
never drawn. Never hand-draw mermaid: there is no mermaid field, and a fenced
block in `summary` is neutralized.

You supply structure; verdict verifies it and draws it. Every node and every
edge carries a quote from the changed diff in the `spec_checks` evidence shape,
and the same matcher (A2) checks it.

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
- `nodes` — 1 to 24 entries. `id` is your lookup key (≤40 characters, never
  rendered), `label` is what the picture shows (≤120 characters), and
  `evidence` is the changed line the node stands for.
- `edges` — at most 40 entries. `from` and `to` are node ids, `label` is
  optional (≤120), and `evidence` is the changed line the relation stands for.
- `evidence` — exactly `path`, `line`, and `quote`, as in a `spec_checks` row:
  the NEW-side line from `full.diff`, copied exactly. A paraphrase never
  verifies, and there is no `side` or `start_line`.

## Honesty rule (every element from the diff)

A node is a thing the diff changes or directly touches; an edge is a relation
one of those lines states. Nothing illustrative, inferred, or assumed is
supplied without a line that proves it. When in doubt, omit the element — an
unproven node is dropped, and a diagram that loses most of itself is omitted
whole.

## What verdict does with it

- Every quote is matched against the pinned `full.diff`. An unverified node is
  dropped, and so is an edge whose own quote fails or whose endpoint was
  dropped. The block never points at a node it did not prove.
- The block is drawn only when at least half the supplied elements verify.
  Below that it is omitted whole with one line:
  `> Behavioral diagram omitted: 4 of 6 elements failed quote verification (min 50% anchored)`.
- Caps: 24 nodes, 40 edges, 120 characters per label, 8,000 bytes. Whole nodes
  and edges are dropped before the ceiling is crossed, and one `%%` comment
  inside the fence names what the caps left out.
- Labels and titles are reduced to letters, digits, spaces, and safe
  punctuation, and node identifiers are generated (`n1`, `n2`, …). Write plain
  words; markup, arrows, and directives never survive.
- The block lands right after the computed topology, collapsed, labeled
  `Behavior flow (verified against the diff — N/M elements anchored)`.

A diagram never fails a review and never withholds approval. A malformed block
is a `diagram-invalid` warning — a malformed element is dropped, an
out-of-scope type omits the picture — and `--strict` promotes it like every
other warning. Tabular comparisons stay markdown tables — never
convert a matrix to a diagram.
