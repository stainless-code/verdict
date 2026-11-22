# Prototype pollution through untrusted keys

Assigning an attacker-chosen key onto an ordinary object can reach the
`__proto__` setter or `constructor.prototype`, changing every object in the
process. A recursive merge over parsed JSON is the classic path.

## When to flag

- Bracket assignment of an untrusted key onto a plain object: `target[key] =
value` where `key` comes from a request, query string, or parsed body and
  is not checked against `__proto__`, `constructor`, and `prototype`.
- A recursive merge or deep-set helper over a parsed body with no key
  allowlist and no null-prototype target. Nested `constructor.prototype`
  paths defeat a top-level-only check.
- `Object.assign(target, parsed)` where `parsed` is untrusted: `Object.assign`
  uses `[[Set]]`, so a `__proto__` own property invokes the setter and
  changes the target's prototype.
- A query-string or form parser that writes `obj[name] = value` for arbitrary
  names, or a config loader that merges user settings into defaults
  recursively.
- A library call known to deep-merge arbitrary keys without a guard, when the
  diff introduces the untrusted input path.

## Do NOT flag

- Object spread of parsed data as a direct pollution vector: spread uses
  `CreateDataProperty`, so `{...JSON.parse('{"__proto__":{}}')}` creates an
  own property and does not invoke the setter. Nested recursive merges are
  still a finding.
- `JSON.parse` on its own: it creates own properties and does not pollute.
- A `Map`, `Object.create(null)`, or a class with private fields as the
  target: those have no `Object.prototype` setter on the path.
- A key checked against an explicit allowlist, a `Map.has` gate, or a
  `Object.hasOwn` gate that rejects `__proto__`/`constructor`/`prototype`.
- `Object.freeze(Object.prototype)` applied at process start as a mitigation
  (report it as defense in depth, not as the defect).
- A fixed-shape object literal assigned from known field names.

## Evidence

Quote the assignment or merge and the path the untrusted key travels. Name
the object whose prototype changes and what code reads the polluted value.
