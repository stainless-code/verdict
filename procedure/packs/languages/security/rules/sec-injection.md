# Injection into an interpreter

Any sink that parses a string as code or syntax — SQL, a shell, a template,
an LDAP or NoSQL query, an OS command — can be reached by input assembled
into that string. Bind values; never build syntax from data.

## When to flag

- String-built SQL (`"... WHERE id = " + id`, `format`, f-string, `%`)
  reaching `execute`/`query`/`raw`.
- A shell command built from input: `exec.Command("sh", "-c", input)`,
  `os.system`, `subprocess(..., shell=True)`, `child_process.exec(input)`,
  backticks.
- A template rendered from request data with autoescaping disabled, or a
  template source built from input (SSTI).
- A NoSQL query with an operator or `$where` taken from the request
  (`{$gt: ""}`), an LDAP filter concatenated from input, or an HTTP header
  built from input without CRLF rejection.
- An `eval`/`exec`/`Function`/`compile` call on a request value.
- A redirect or file path derived from input without an allowlist (cross-ref
  SSRF and path traversal).

## Do NOT flag

- Parameterized SQL, an ORM query DSL, or a prepared statement with bound
  values.
- `exec`/`spawn` with an argv array and no shell string, even when an argv
  element comes from input.
- A static template with context-aware escaping, or a template whose source
  is a repository constant.
- A command built from a closed enum/allowlist with no user-supplied syntax.
- `JSON.parse`/`json.loads` (data parsers, not code interpreters) and
  `ast.literal_eval`.
- A test fixture that asserts the injection is rejected.

## Evidence

Quote the sink and the input path. Name the syntax the value changes and the
interpreter that executes it.
