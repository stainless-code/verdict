## Spec checklist

At least one `context.txt` source carries `role: spec`. Every bullet and every table row in those sources is one checklist item, and `spec_checks[]` holds one entry per item: `{"item":"<the item, quoted verbatim>","status":"met|violated|n/a"}`. `violated` also becomes a finding whose `claim` quotes the item.
`met` and `violated` each need `evidence` — a `path` the diff changes, its `line`, and the exact `quote` from that line. Tests, logs and a summary are not evidence; a doc or example the diff changes is. Check at least the configured `spec_coverage_min` of the items, and say why any item you leave out is `n/a`.
`verdict record --dry-run` names an item that is not in the spec, a gap in the coverage, and evidence that is not code.
