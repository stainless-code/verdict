# Published schemas

These files are generated from the Go shape types in
[`internal/schemagen/model.go`](../internal/schemagen/model.go). Never edit a
`*.schema.json` file by hand.

Regenerate after changing a store shape, a struct tag, or an overlay entry:

```
make schemas
```

`go generate ./internal/schemagen` runs the same command. `make schemas-check`
and the `TestSchemasMatchGenerated` Go test fail when the committed files
differ from the generator output; CI checks, it never rewrites the tree.

`internal/schemagen/testdata/baseline/` snapshots the accepted schemas, and
`TestSchemasStayAdditive` fails when the generator removes a property or enum
value, adds a required key, closes `additionalProperties`, narrows a type or
bound, or drops a definition. Additive changes need no baseline update. The
baseline is test data, not part of the embedded or published payload. To
accept a deliberate breaking change, update the baseline in the same commit
and review its diff:

```
make schemas-baseline
```

That is the only supported way to bless a break; prose changes in
`overlay.json` do not require it.

`internal/schemagen/overlay.json` carries only prose descriptions (and any
future patterns). Structure, requiredness, nullability, enums, and constants
live in the Go types.
