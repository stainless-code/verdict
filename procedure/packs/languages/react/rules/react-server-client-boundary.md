# Server/client boundary leaks

In the React Server Components model (`'use client'`, Next.js App Router), a
client module is bundled and shipped to the browser. Server-only modules,
database clients, and secrets imported into it either fail the build or, worse,
are inlined into the client bundle.

## When to flag

- A module marked `'use client'` that imports a server-only module: a
  database client (`pg`, `prisma`, `@/lib/db`), a filesystem or `child_process`
  module, an auth/session library that reads cookies or secrets, or the
  `server-only` package.
- A client component that reads a secret environment variable
  (`process.env.JWT_SECRET`, `DATABASE_URL`, `STRIPE_SECRET_KEY`) instead of a
  `NEXT_PUBLIC_*` value. In Next.js, only `NEXT_PUBLIC_*` is exposed to the
  browser, but a non-public name can still be inlined by a bundler or leaked
  through a prop.
- A server component passing a secret or a server-only object as a prop to a
  client component: props are serialized into the RSC payload and reach the
  browser.
- A `'use client'` module that imports a module whose side effect registers
  server credentials, even if the client only uses one export.
- A server action or route handler imported into a client component without
  the framework's server-action boundary, when the import would bundle the
  implementation.

## Do NOT flag

- `import type { ... }` from a server module: type-only imports are erased
  and ship nothing.
- `NEXT_PUBLIC_*` environment variables in client code: they are public by
  design. Flag the secret that should not be public, not the prefix.
- A client component importing a pure shared utility (formatting, validation,
  constants) with no server dependency.
- A server-only module (no `'use client'`) reading secrets, or a
  `server-only` import used as the guard in a server module.
- A framework that does not use the `'use client'` model (a Vite SPA, a
  Remix route): detect the directive before reporting. `'use server'` marks a
  server action and is not this defect.
- A secret passed through a server action's return value only when the
  framework serializes it deliberately for the client; that is still a leak,
  so do flag a returned secret, but do not flag the action boundary itself.

## Evidence

Quote the `'use client'` directive and the server import or secret read. Name
what reaches the browser bundle or the RSC payload and why it is secret.
