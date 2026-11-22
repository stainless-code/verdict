# Raw HTML that bypasses React's escaping

React escapes text children. `dangerouslySetInnerHTML` is the explicit opt-out:
the string is assigned as HTML, so any markup or script in it executes in the
page's origin. React does not sanitize the value.

## When to flag

- `dangerouslySetInnerHTML={{ __html: value }}` where `value` comes from a
  prop, state, API response, markdown render, URL parameter, or
  `localStorage`, and no sanitizer runs on that same value.
- The same sink reached through a ref (`el.innerHTML = html`), a
  `document.write`, or `insertAdjacentHTML`.
- A markdown renderer configured to allow raw HTML (`rehype-raw`,
  `marked` with HTML enabled) whose output reaches the sink unsanitized.
- A sanitizer configured to allow `script`, `style`, event-handler
  attributes, or `javascript:` URLs, or a sanitizer applied to a different
  value than the one assigned.
- Server-provided rich text rendered directly, even when the server is
  "trusted": stored XSS arrives through the same field.

## Do NOT flag

- A static string constant compiled into the bundle (an inline SVG icon, a
  legal notice owned by the repository), with no interpolation.
- `__html: ""` or a value already sanitized in the same data flow with an
  allowlist sanitizer such as DOMPurify, and passed to the sink unchanged.
- A sanitizer presence alone is not a finding; report a missing or misconfigured
  sanitizer, not the use of `dangerouslySetInnerHTML` for trusted content.
- React's normal text interpolation, including `{userContent}`: React escapes
  it, so it is not this defect.
- A CSP that blocks inline script as defense in depth; it does not make the
  unsanitized sink correct, but do not report the CSP itself.

## Evidence

Quote the sink and the value's source. Name the input an attacker controls and
the script or handler that executes.
