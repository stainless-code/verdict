# Non-cryptographic randomness for security values

A general-purpose RNG is predictable from a few outputs. Tokens, keys,
nonces, session ids, CSRF values, and one-time codes need a CSPRNG.

## When to flag

- JavaScript/TypeScript `Math.random()` for a token, nonce, id used in
  auth, or key.
- Python `random.random`/`randint`/`choice` (Mersenne Twister) for the same.
- Go `math/rand`/`rand.Intn` (automatic seeding since Go 1.20 changes the
  seed source, not the algorithm's predictability) or `math/rand/v2`
  (Go 1.22+) for a security value.
- Java `java.util.Random`/`Math.random` (48-bit LCG), or `new Random(seed)`
  with a time/PID seed.
- PHP `rand`/`mt_rand`, Ruby `rand`/`Random.new`, .NET `Random`, or
  `srand(time(NULL))`/`seed(time.Now())`.
- A UUID v1 (time/MAC based) or a sequential id used as an unguessable
  token.

## Do NOT flag

- A CSPRNG: Go `crypto/rand`, Node `crypto.randomBytes`/`crypto.randomUUID`/
  `crypto.randomInt`, Python `secrets`, Java `SecureRandom`, Ruby
  `SecureRandom`, PHP `random_bytes`.
- `Math.random` for jitter, sampling, animation, test data, or a
  non-security UI value.
- A UUID v4 produced by a CSPRNG, or a sufficiently long random id from a
  CSPRNG.
- A monotonic counter combined with a cryptographic MAC where the counter is
  not the secret.
- A test fixture that uses a fixed seed to make output deterministic.
- `math/rand` used to shuffle display order where the order carries no
  authority.

## Evidence

Quote the RNG call and the value it produces. Name the token or key an
attacker can predict and what it authorizes.
