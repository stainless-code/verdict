## Spec checklist

At least one `context.txt` source carries `role: spec`. Every bullet and every table row in those sources is one checklist item, and a source that enumerates none still states a goal in its first heading, else in its first prose line — that goal is one item. `spec_checks[]` holds one entry per item: `{"item":"<the item, quoted verbatim>","status":"met|violated|unmet|n/a"}`. `violated` also becomes a finding whose `claim` quotes the item.
`met` and `violated` each need `evidence` — a `path` the diff changes, its `line`, and the exact `quote` from that line. Code or test source the diff changes is evidence; test output (logs, goldens, snapshots, testdata) is not.
`unmet` means nothing implements the item, so it carries no `evidence` — there is no changed line to quote — and a one-line `note` saying what is missing. `n/a` means the item does not apply: no `evidence`, and a one-line `note` saying why. Never explain a status in evidence, and never echo a `note` into a finding.
A change the goal does not ask for is a finding with `category: "accidental-commit"` whose `quote` is the changed line — never a `violated` or `unmet` row, because no item asked for it.
`verdict record --dry-run` names an item that is not in the spec, an item no row covers, a missing note, and evidence that is not code.
