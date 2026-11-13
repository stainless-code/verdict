# Exclusions and withholding

## Diff exclusions

`fetch` and `fetch-local` read `.verdict/ignore` and the root `.gitattributes`
from the trusted base snapshot. The existing `--allow-head-controls` opt-out
uses the working tree and records that choice. Nested attributes files do not
participate. Fetch does not discover or download nested attribute files.

The ignore file accepts one shell glob per line. `*` crosses `/`, `?` matches
one character, and bracket classes and backslash escapes are supported.
A leading `#` starts a comment. `\#` matches a literal `#`. A leading `!`
re-includes a path. The last matching rule wins, including either endpoint of
a rename. The file limit is 64 KiB and 1,000 patterns.

Generated output, vendored trees, lockfiles, minified assets, generated sources,
and snapshots are excluded by default. Tests remain reviewable. An explicit
negation such as `!vendor/keep/**` overrides a default. Root attributes setting
`linguist-generated` or `linguist-vendored` also exclude matching paths.
Negated attributes and `=false` clear their respective attribute.

`full.raw.diff` preserves the fetched bytes. `full.diff` contains the reviewed
subset. Cap calculations and chunking use that subset. Concatenated chunks
preserve its bytes, including an unterminated final line. Patch identity uses
the raw diff. `sensitive.txt` scans the raw diff, so an excluded credential path
still appears there.

`excluded.json` records each removed section's path, rule id, and byte count.
`toc.txt` lists exclusions separately from files with no hunks. The receipt
exists even when its exclusion list is empty. Rule ids are `default:generated`,
`default:attribute`, or `ignore:<physical-line-number>`.

## Finding suppression

An agent can add `suppressed[]` to `review.json`:

```json
{
  "suppressed": [
    {
      "path": "notes.txt",
      "line": 2,
      "source": "agent",
      "reason": "An intentional compatibility exception for the legacy parser."
    }
  ]
}
```

`path`, `source`, and `reason` are required. `source: agent` requires a reason
of at least 30 characters after whitespace trimming. `source: exclusion`
requires an `exclusion_id` matching that path's rule in `excluded.json`.
An optional `sig` selects a full signature or an unambiguous prefix.
Without `sig`, the path and optional line select matching findings.
Unknown fields warn and are dropped. Unknown categories warn and become null.
Agents cannot supply the tool's `below-severity` or `validation` sources.

A matching finding keeps its status and gains `withheld` with its source,
reason, optional exclusion id, and timestamp. History records the withholding.
Repeating the suppression keeps the first timestamp. Removing the suppression
clears its stamp and records `reopened` with note `withheld cleared`.
A suppression that matches no finding warns and creates no synthetic finding.

## Severity floor and publication

`post --min-severity critical|important|informational|off` overrides the trusted
D7 `defaults.post_min_severity`, including path-scoped defaults. `off` disables
the floor. A finding below the floor stays open and gains a `below-severity`
receipt. A null or unknown severity remains visible and contributes to the
warning and `data.withheld.unlabelled` count.

Withheld findings do not enter inline payloads or generated moved-to-body
output. `post --json` reports totals and source counts in `data.withheld`.
`post --dry-run` writes the same stamps to `findings.dryrun.json`.
An open withheld finding still blocks approval. `--force` bypasses that
approval guard but leaves the floor active. Withholding does not satisfy a
D7 blocking clause.

## Reports and measurements

`report` hides withheld findings from its ranked list, ordinary counts, and
tier check. `report --include-withheld` adds a `Quiet by policy` section and
`quiet_by_policy` JSON entries. The report exposes the withheld total and
source counts separately. The flag leaves the tier check unchanged.

SARIF omits withheld findings from results and records source counts in the
run's `properties.withheld`. `trend` reports source counts in the rollup and
each bundle. Legacy stores without receipts contribute zeros. Malformed
receipts warn and contribute no withholding count. `calibrate` treats withheld
findings as unknown and excludes them from judged precision.

See [refusal codes](troubleshooting.md) for `bad-ignore`, `bad-suppression`, and
`bad-min-severity`, and `ignore-too-large`.

`fetch` and `fetch-local` accept repeatable `--ignore <glob>` rules. They run
last, above the base ignore file; `!` re-includes. `--no-default-excludes`
disables generated and attribute defaults while the ignore file still applies.
`excluded.json` records `ignore`, `no_default_excludes`, and `patterns_source`
(`cli` when run overrides are present).
