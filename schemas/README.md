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

`internal/schemagen/overlay.json` carries only prose descriptions (and any
future patterns). Structure, requiredness, nullability, enums, and constants
live in the Go types.
