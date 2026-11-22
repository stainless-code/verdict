# React lens

Dispatch only for JSX/TSX files: this lens is scoped to `**/*.tsx` and
`**/*.jsx` so a backend or CLI TypeScript repository never receives React
advice. The general async, boundary, and cleanup rules live in `ts-js`.

Frame one falsifiable question — which effect outlives its component, which
list row keeps the wrong state after a reorder, which hook runs conditionally,
which HTML string bypasses React's escaping, which server secret reaches the
client bundle — and give it a short hypothesis label.

Read `package.json` before relying on a version: React 19 passes `ref` as a
prop and supports ref cleanup functions, React 19.2 ships `useEffectEvent`
where installed, and React Compiler memoizes render work but does not change
effect dependency semantics. Scope the question to at most ~12 files, cite
NEW line numbers, and quote the component and the state it corrupts.
