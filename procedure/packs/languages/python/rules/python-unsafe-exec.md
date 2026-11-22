# Untrusted input reaching an interpreter

`eval`, `exec`, `pickle`, unsafe YAML loaders, and shell-mode subprocesses
execute what they are given. If the string or bytes come from a request, a
file another party can write, or a queue, the attacker controls code
execution.

## When to flag

- `eval(...)` or `exec(...)` on a request value, a formatted string, or a
  value read from a database/queue; `compile(...)` used the same way.
- `pickle.loads(...)`, `pickle.load(...)`, `marshal.loads(...)`, or
  `shelve.open(...)` on bytes an attacker can supply. A pickle can call
  arbitrary callables during unpickling.
- `yaml.load(...)` with an unsafe loader. PyYAML 6 requires an explicit
  `Loader`; `yaml.Loader`/`yaml.UnsafeLoader` construct arbitrary Python
  objects, and `yaml.FullLoader`/`yaml.full_load` has a bypass history
  (CVE-2020-14343), so neither is the untrusted-input loader. Use
  `yaml.safe_load` or `Loader=yaml.SafeLoader`.
- `subprocess.run(..., shell=True)`, `os.system(...)`, or `os.popen(...)`
  with input interpolated into the command string; pass argv as a list and
  keep `shell=False`.
- A template engine rendered with untrusted data and autoescaping disabled,
  or a `jinja2.Template` built from a request string.

## Do NOT flag

- `ast.literal_eval(...)` on a string that must hold a literal: it accepts
  only Python literals and calls no code.
- `json.loads(...)`, `json.load(...)`, or `yaml.safe_load(...)` on untrusted
  data.
- `eval`/`exec` on a compile-time constant or a test fixture the process
  itself owns, with no external input path.
- `pickle` reading a file the same process created with restrictive
  permissions and never accepts from outside; state that integrity boundary
  if you cite it.
- `subprocess.run(["git", "status"])` with `shell=False`, even when an
  element comes from input: argv is not a shell string. A leading `-` that
  changes the program's behavior is a separate argument-injection finding.
- `shlex.quote` used correctly to build a shell string for a trusted shell
  script, when the value is the only dynamic part.

## Evidence

Quote the sink and the input path. Name the bytes or string an attacker
controls and the code execution they gain.
