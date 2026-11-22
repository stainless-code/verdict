# Java lens

Dispatch only when the diff changes resource ownership, cross-thread state, a
deserialization boundary, interrupt handling, an `Optional` that may be empty,
or SQL text built from input. Frame one falsifiable question — which handle
leaks, which field races, which gadget input deserializes, which interrupt is
lost, which SQL string is interpolated — and give it a short hypothesis label.

Read the changed files and the platform floor the dispatch names; the on-disk
diff is authoritative scope. State the version when it decides the answer
(try-with-resources is Java 7+, effectively final resources Java 9+, no-arg
`Optional.orElseThrow` Java 10+, `ObjectInputFilter` Java 9+). Scope the
question to at most ~12 files, cite NEW line numbers, and name the leak, the
racing field, or the gadget input.
