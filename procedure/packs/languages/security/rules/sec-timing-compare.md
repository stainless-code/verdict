# Timing-unsafe secret comparison

String equality short-circuits at the first differing byte. For a MAC, a
signature, an API key, or a reset token, the response time leaks how much of
the guess was correct, which is enough to recover the value byte by byte.

## When to flag

- Ordinary equality (`==`, `.equals`, `===`) or a
  `String.compareTo`/`memcmp` early-exit loop on an HMAC, signature, API
  key, session token, CSRF token, or password-reset token.
- `strings.HasPrefix`/`startsWith` used as an authentication check on a
  secret; it leaks every matching prefix.
- A manual byte loop that returns on the first mismatch for a secret value.
- A hash comparison for a password reset/verification token where the
  comparison is ordinary equality of the raw or hex value and the value is a
  bearer credential.

## Do NOT flag

- A constant-time comparison: Go `hmac.Equal`/`subtle.ConstantTimeCompare`,
  Node `crypto.timingSafeEqual`, Python `hmac.compare_digest`, Java
  `MessageDigest.isEqual`, Ruby `Rack::Utils.secure_compare`.
- Password verification through a library's `verify` (bcrypt, Argon2,
  scrypt, PBKDF2), which compares the derived hash internally.
- Comparing public values (a version, a non-secret id, a content hash used
  for caching) where no secret is inferred.
- A length check before `crypto.timingSafeEqual`: the length is normally not
  secret, and that API requires equal-length buffers.
- A comparison in test code that asserts a fixture value.
- A constant-time library call whose return is negated or wrapped, as long
  as the comparison itself is constant-time.

## Evidence

Quote the comparison and the secret's role. Name the value a remote attacker
can recover and the timing signal.
