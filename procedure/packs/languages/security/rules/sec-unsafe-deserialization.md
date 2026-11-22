# Untrusted bytes decoded into objects

A native deserializer lets the payload choose types and invoke methods.
`pickle`, Java `ObjectInputStream`, `yaml.load`, `Marshal.load`, and PHP
`unserialize` are code execution when the bytes are attacker-controlled.

## When to flag

- Python `pickle.loads`/`load`, `marshal.loads`, or `shelve.open` on bytes
  from a request, queue, cache, or writable file.
- Java `ObjectInputStream.readObject`, `XMLDecoder`, XStream/Kryo with
  arbitrary types, or Jackson polymorphic typing opened to arbitrary classes
  (`LaissezFaireSubTypeValidator`, `Id.CLASS`).
- Python `yaml.load` with `Loader=yaml.Loader`/`FullLoader` or
  `yaml.full_load` (PyYAML 6 requires an explicit loader; `safe_load` is the
  safe form).
- Ruby `Marshal.load`, PHP `unserialize`, Node `node-serialize` or a
  `eval`-based deserializer, .NET `BinaryFormatter`/`LosFormatter`.
- A deserializer configured to allow arbitrary classes, or a
  `readObject`/`readResolve` reachable from input that performs side effects.

## Do NOT flag

- JSON, protobuf, Avro, or MessagePack decoded into a fixed schema or DTO
  with no polymorphic type selection.
- `yaml.safe_load`, `ObjectInputFilter` allowlists, or a signed/encrypted
  payload whose integrity is verified before decoding.
- A serializer used only for output, or a format with no object
  construction (CSV, plain text).
- A test fixture that round-trips a trusted object.
- A pickle file the process itself creates with owner-only permissions and
  never accepts from outside; state that integrity boundary if you cite it.

## Evidence

Quote the deserializer and the byte source. Name the class or side effect
the attacker can reach.
