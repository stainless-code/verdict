# Broken object-level authorization (IDOR/BOLA)

Authentication answers who the caller is; authorization answers what this
caller may touch. A handler that trusts an identifier from the request, or
checks only "is logged in", lets one tenant read or write another's object.

## When to flag

- An object id read from the path, query, or body and used in a lookup or
  mutation with no ownership/tenant check (`WHERE id = ?` instead of
  `WHERE id = ? AND owner_id = ?`).
- A handler that checks the session but not that the session's subject owns
  the object (OWASP API Security #1, BOLA/IDOR).
- A role, user id, tenant id, or scope read from a client-supplied header
  (`X-User-Role`, `X-Tenant-Id`, `X-Forwarded-User`) without a signature or
  server-side lookup.
- An admin path guarded only in the UI or by a client-side route, or a
  role check that reads a value the client can set (mass assignment of
  `role`/`tenant_id`/`is_admin`).
- A GraphQL resolver or RPC method that fetches by id without the same
  authorization as its REST counterpart.
- A batch/list endpoint that applies the ownership filter to the first
  object but not to the rest.

## Do NOT flag

- A handler behind middleware visible in the diff that authenticates and
  scopes by the session subject, or a query already filtered by
  `owner_id = current_user`.
- A public endpoint whose data is intentionally public (a catalog, a
  published article) with no per-object secret.
- An admin route whose server-side role check exists in the same file or a
  clearly named guard.
- A request id mapped to a fixed resource the caller owns through a signed
  token or a server-side session store.
- An authorization library/policy call whose result is checked, even if the
  check is one line.
- Test code that constructs a caller and asserts a 403.

## Evidence

Quote the id source and the query/handler. Name the object and the other
tenant that can read or modify it.
