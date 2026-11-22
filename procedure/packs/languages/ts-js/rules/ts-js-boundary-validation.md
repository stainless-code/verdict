Report data that crosses into a typed program without validation: a `JSON.parse`
result cast to an interface, `as` or `any` applied to a request body,
`process.env` read as a number, or a `req.query`/`params` field used unchecked
in a decision.
