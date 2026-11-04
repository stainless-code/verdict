# Published schemas

These files are generated from the Go shape types in
[`internal/schemagen/model.go`](https://github.com/stainless-code/verdict/blob/main/internal/schemagen/model.go).
Never edit a `*.schema.json` file by hand.

Regenerate after changing a store shape, a struct tag, or an overlay entry:

```
make schemas
```

`go generate ./internal/schemagen` runs the same command. `make schemas-check`
and the `TestSchemasMatchGenerated` Go test fail when the committed files
differ from the generator output; CI checks, it never rewrites the tree.

[`internal/schemagen/testdata/baseline/`](https://github.com/stainless-code/verdict/tree/main/internal/schemagen/testdata/baseline)
snapshots the accepted schemas, and `TestSchemasStayAdditive` fails when the
generator removes a property or enum value, adds a required key, closes
`additionalProperties`, narrows a type or bound, or drops a definition.
Additive changes need no baseline update to pass, but re-take it in the same
commit anyway: a baseline older than a field cannot report that field's
removal. The baseline is test data, not part of the embedded or published
payload. A move of the generator's shape — additive or breaking — updates the
baseline in its own commit, and the diff is reviewed:

```
make schemas-baseline
```

That is the only supported way to bless a break; prose changes in
`overlay.json` do not require it.

[`internal/schemagen/overlay.json`](https://github.com/stainless-code/verdict/blob/main/internal/schemagen/overlay.json)
carries only prose descriptions (and any future patterns). Structure,
requiredness, nullability, enums, and constants live in the Go types.
