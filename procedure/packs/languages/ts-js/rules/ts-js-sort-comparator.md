# Numeric sort without a comparator

`Array.prototype.sort()` converts every element to a string and compares
UTF-16 code units. For numbers that is lexicographic order: `[1, 10, 2]`
stays `[1, 10, 2]`, and a `Date` array sorts by its string form.

## When to flag

- `.sort()` with no comparator on a `number[]`, a `bigint[]`, a `Date[]`, or
  an array of objects whose ordering field is numeric or temporal.
- The same call on typed arrays' `sort()` when the intent is numeric order:
  `Float64Array.prototype.sort` is numeric, but `Array.from(typed).sort()`
  is not; flag the conversion that loses the numeric sort.
- A comparator whose subtraction loses precision or overflows (large integer
  IDs, microsecond timestamps), or one that returns a boolean or a `bigint`
  instead of a number.
- Sorting IDs that are numeric strings where the caller expects numeric
  order (`["10", "9"]` sorts as `["10", "9"]`).

## Do NOT flag

- `.sort()` on a `string[]` when lexicographic order is intended: the
  default is a string comparison by definition.
- A comparator that returns `a - b`, `localeCompare`, or a three-way
  comparison.
- `.toSorted()` with the same rules: it copies, but the comparator
  requirement is unchanged.
- A sort of a fixed, already-ordered literal whose order is not semantic.
- `Array.prototype.sort` used to group or deduplicate where the resulting
  order is never observed.

## Evidence

Quote the `.sort()` call and the element type. State the order the caller
expects and the order the default produces.
