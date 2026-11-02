"@stainless-code/verdict": minor

Experimental `pkg/verdict` Go facade: embed review operations in-process
through a context-first `Client` with typed requests/responses and a public
`*RefusalError` (matched with `errors.As`). The core owns every behavior;
the package only converts to and from the core services, and the envelope
plus JSON Schemas stay the stable contract. The Go API may change before
1.0.
