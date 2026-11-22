# TypeScript/JavaScript lens

Dispatch only when the diff changes async control flow, a trust boundary, a
long-lived handler, or a value that reaches a runtime builtin by string. The
React lifecycle lives in the `react` pack, so do not open that question for a
`.ts`/`.js` file with no JSX.

Frame one falsifiable question — which rejection is unobservable, which parsed
value reaches a typed field unchecked, which regex can backtrack, which
listener outlives its handler — and give it a short hypothesis label.

Read the changed files the dispatch names; the on-disk diff is authoritative
scope. Scope the question to at most ~12 files, cite NEW line numbers, and
quote the code path that makes the failure reachable. Read `package.json` and
the runtime floor when the answer depends on a version (Express 5 forwards
rejected handler promises; Node 15+ makes unhandled rejections fatal).
