Report an error the caller needs but cannot see: one discarded with `_`, logged
and dropped on a path that then continues, compared by message text instead of
`errors.Is`/`errors.As`, or re-wrapped with `%v` so the cause stops matching. A
nil check on a typed-nil error interface is the same defect.
