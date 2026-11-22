# JWT validation gaps

A JWT is a claim container, not proof. A token is trustworthy only after the
signature is verified with the expected algorithm and key, and the claims the
application relies on are checked.

## When to flag

- Accepting the token's own `alg`, including `none`; trusting `alg` without
  pinning the expected algorithm(s).
- Decoding without signature verification: PyJWT `jwt.decode(...,
options={"verify_signature": False})`, `jwt.decode` with `verify=False`,
  Node `jsonwebtoken.decode`, `jose` `decodeJwt` used for authorization,
  Java `Jwts.parser().parseClaimsJwt` (unsigned).
- Algorithm confusion: accepting `HS256` when the key is an RSA/EC public
  key (or vice versa), or letting the library choose the algorithm from the
  token's header.
- Missing `exp`/`nbf` validation, or ignoring `iss`/`aud` in a multi-tenant
  or multi-issuer application.
- A `kid` header used to select a key by path or URL without an allowlist,
  enabling path traversal or SSRF.
- Trusting claims (`sub`, `role`, `tenant`) before verification, or using
  an unsigned token for a trusted internal hop.

## Do NOT flag

- A verified token with pinned algorithms and the required `exp`/`iss`/
  `aud` checks, even if verification is one library call.
- Decoding a token only to read `kid`/`iss` to select the key, where the
  decoded claims are not used for authorization before verification.
- A JWT used only as an opaque lookup key against a server-side session
  store that validates it.
- A test that mints and verifies a fixture token with a test key.
- An OAuth introspection flow that validates the token at the issuer and
  does not trust local claims.
- A library default that already verifies signature and expiry; report the
  missing claim check the application needs, not the library's baseline.

## Evidence

Quote the decode/verify call and the algorithm/key choice. Name the claim an
attacker can forge and what it authorizes.
