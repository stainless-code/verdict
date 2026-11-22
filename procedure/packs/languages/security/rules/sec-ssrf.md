# Server-side request forgery

A request whose destination the client chooses lets the server reach hosts the
client cannot: loopback, private networks, cloud metadata, and internal admin
interfaces. Redirects and DNS resolution are part of the attack surface.

## When to flag

- A URL or host taken from a request (a webhook URL, an image/import URL, a
  proxy target) and fetched without a scheme + host allowlist.
- A hostname allowlist that is not re-checked after DNS resolution or on
  each redirect. Go's default client follows up to 10 redirects; Java's
  `HttpURLConnection` follows same-protocol redirects; Python `requests`
  follows unless `allow_redirects=False`; Node `fetch` follows. A redirect
  to `http://169.254.169.254/` defeats a first-hop-only check.
- Any path to link-local metadata: `169.254.169.254` (AWS/Azure/GCP IMDS),
  `metadata.google.internal`, `169.254.170.2` (ECS task metadata),
  `fd00:ec2::254` (IPv6 IMDS), or a provider's documented metadata name.
- A destination that can resolve to loopback (`127.0.0.0/8`, `::1`),
  RFC1918 (`10/8`, `172.16/12`, `192.168/16`), `169.254/16`, `fc00::/7`,
  `0.0.0.0`, or a Unix socket path.
- A non-HTTP scheme accepted from input (`file://`, `gopher://`, `dict://`,
  `ftp://`) or a URL parsed with a different library than the one that
  fetches it.

## Do NOT flag

- A URL from configuration or a module constant, or one built from a closed
  route map.
- A client-supplied URL fetched only after an allowlist check on scheme and
  host, with the resolved IP checked at dial time and redirects either
  disabled or re-validated.
- A request to a fixed internal service through a service name the
  application owns, with no user-controlled part.
- A URL whose only user-controlled component is a path/query on a fixed
  origin, with the path validated.
- A health check, a metrics scrape, or a webhook to a user URL that runs
  through an egress proxy enforcing the same allowlist (state the proxy).
- A local `httptest` URL in a test.

## Evidence

Quote the fetched URL and its source. Name the internal address or metadata
endpoint the client can reach and what it returns.
