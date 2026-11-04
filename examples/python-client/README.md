# Python client example

A dependency-free Python client for the `verdict --json` protocol. There is
**no official Python package**: this directory is the documented recipe, and
the protocol — the binary's envelope plus the published JSON Schemas — is the
contract.

The client spawns `verdict <verb> … --json`, reads exactly one envelope from
stdout, and raises `VerdictRefusal` on exit 1 with the refusal `code`,
`message`, and parsed `try:` hint. It never uses a shell, never starts a
server, and never downloads anything.

## Run it

Build the binary first (`make build` at the repo root), then run the demo
against the committed fixture bundle:

```sh
tmp=$(mktemp -d)
cp -R examples/python-client/fixtures/bundle "$tmp/.review"
python3 examples/python-client/example.py acme/app 123 "$tmp"
```

Output:

```text
report acme/app#123: 0 open
```

The offline smoke test covers a success, the typed refusal for a missing
bundle store (exit 1), and a usage error (exit 2). It skips when `python3` is
absent and never runs pip or the network:

```sh
./examples/python-client/smoke.sh
# ok: success, refusal, exit 2, demo
```

## Generate the models

`models.py` is generated from
[`schemas/envelope.schema.json`](../../schemas/envelope.schema.json) with a
pinned [datamodel-code-generator](https://github.com/koxudaxi/datamodel-code-generator).
The envelope schema's `$defs` carry the pinned `report`, `post`, and `record`
data shapes, so one input file covers the envelope and all three profiles.
Output is standard-library `dataclasses`, so the runtime example needs no
third-party package.

```sh
python3 -m venv .venv
. .venv/bin/activate
pip install -r examples/python-client/requirements-dev.txt   # 0.88.0
./examples/python-client/generate_models.sh
```

The exact generator invocation is in `generate_models.sh`:

```sh
datamodel-codegen \
  --input schemas/envelope.schema.json \
  --input-file-type jsonschema \
  --output examples/python-client/models.py \
  --output-model-type dataclasses.dataclass \
  --target-python-version 3.10 \
  --use-standard-collections \
  --use-union-operator \
  --formatters builtin \
  --disable-timestamp
```

`models.py` is committed so the example is readable and testable offline.
After changing a schema, regenerate and commit both files. The drift check
needs the pinned generator installed; it is deliberately not part of the
Go/CI gates, because tests never pip install:

```sh
./examples/python-client/generate_models.sh --check
```

## Files

| File                | Purpose                                                        |
| ------------------- | -------------------------------------------------------------- |
| `verdict_client.py` | Spawn, parse, validate `schema_version`, and raise.            |
| `typed.py`          | Convert envelope `data` into generated dataclasses.            |
| `example.py`        | Walkthrough: one `report` call with typed `ReportData`.        |
| `models.py`         | Generated dataclasses (do not edit by hand).                   |
| `smoke.py`          | Offline assertions: success, refusal, exit 2, demo.            |
| `smoke.sh`          | Gate wrapper; skips when `python3` is missing.                 |
| `fixtures/bundle/`  | Minimal `.review/` bundles copied into a temp cwd by the test. |

## Protocol facts used here

- `--json` produces exactly one envelope on stdout; all human text is on
  stderr. Exit `0` is success (`error: null`, `data` object), `1` is a typed
  refusal (`data: null`, `error: {code, message}`), `2` is a usage error with
  no envelope, and `3` is a broken/cancelled run.
- `schema_version` is `"1"`. A client rejects an unknown major instead of
  misreading it; additive envelope and `data` keys are allowed.
- The hint is the trailing `try: …` line of `error.message`, not a separate
  key. `VerdictRefusal` splits it for display.
- `data` is pinned for `report`, `post`, and `record`; every other verb keeps
  the generic object shape until its data stabilizes.

See the docs page **Build a client** in `apps/docs/content/build-a-client.mdx`
for the full protocol, exit-code table, binary resolution rules, MCP guidance,
and the language-agnostic checklist.
