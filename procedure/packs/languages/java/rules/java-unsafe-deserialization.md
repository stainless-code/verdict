# Unsafe deserialization

Decoding untrusted bytes into an object graph lets the bytes choose classes
and invoke their methods. A gadget chain on the classpath turns that into
remote code execution, not a parsing bug.

## When to flag

- `ObjectInputStream.readObject()` on bytes from a request, queue, cache, or
  socket with no `ObjectInputFilter` allowlist (JEP 290, Java 9+).
- `XMLDecoder` reading untrusted XML: it can instantiate arbitrary classes.
- Jackson polymorphic typing opened to arbitrary classes:
  `activateDefaultTyping(..., LaissezFaireSubTypeValidator.instance)`,
  `enableDefaultTyping()` (deprecated), or `@JsonTypeInfo(use = Id.CLASS)` /
  `Id.MINIMAL_CLASS` on a request type.
- `XStream`/`Kryo`/`SnakeYAML` configured to instantiate arbitrary types from
  untrusted input; a native deserializer with no allowlist.
- A `readObject`/`readResolve` implementation reachable from untrusted bytes
  that performs a side effect or trusts fields without validation.

## Do NOT flag

- `ObjectInputStream` on a trusted, integrity-checked stream (a signed or
  locally created file) with an allowlist `ObjectInputFilter`, or in a test
  fixture.
- Plain JSON binding with no polymorphic/default typing, using a closed DTO
  type.
- Jackson polymorphic typing restricted with `@JsonSubTypes` to a fixed set
  of known classes, where the input type is not user-chosen.
- `DataInputStream`/`readInt`/protobuf/Avro decoders that read a fixed
  schema and do not instantiate arbitrary classes.
- A serializer used only for output.

## Evidence

Quote the deserializer construction and the input path. Name the attacker's
bytes and the class or side effect they can reach.
