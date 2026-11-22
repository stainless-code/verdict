# Index keys on dynamic lists

React matches children by `key`. An array index describes a position, not an
item, so after an insert, delete, filter, or sort React reuses the wrong
component instance and its state, focus, and animation travel to a different
row.

## When to flag

- `.map((item, index) => <Row key={index} ... />)` where the list can be
  reordered, filtered, prepended to, or spliced, and the rows hold state or
  uncontrolled DOM (inputs, checkboxes, expanded rows, animations).
- `key={index}` combined with a sort or filter in the same render: the rows
  keep their instances while the data moves.
- A key generated during render (`key={Math.random()}`, `key={crypto.randomUUID()}`,
  `key={String(Date.now())}`): every render remounts the subtree and destroys
  its state.
- A key built from a mutable field that changes while the row's identity does
  not, such as a label the user can edit.
- A list whose items already carry a stable id, where the diff introduces the
  index key anyway.

## Do NOT flag

- A static, read-only list that never reorders or filters, such as a fixed
  navigation array, a skeleton placeholder count, or a legend: `key={index}`
  is stable there and is not a defect.
- A list of primitives with no identity and no reordering, where the index is
  the only available key.
- A stable key: `key={item.id}`, a composite of stable fields
  (`key={`${item.id}:${item.locale}`}`), or a server-provided key.
- A key on a list rendered once and never updated, even if it holds inputs.
- `key` used to intentionally reset a subtree when a value changes
  (`key={userId}` on a form): that is the documented remount pattern.

## Evidence

Quote the `.map` and the key expression, plus the operation that reorders
the data. Name the state or focus that lands on the wrong row.
