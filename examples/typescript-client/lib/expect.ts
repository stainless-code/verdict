// The examples' assertion vocabulary: a literal expectation, a printed
// checkpoint line, and a printed field. An expectation is never rebuilt from
// the value under test, so a defect cannot reproduce on both sides.
//
// `expect` throws, so an example that stops holding fails the test that runs
// it (`clients/ts/test/examples.test.mjs`) instead of printing a stale table.

import { VerdictRefusalError } from "../../../clients/ts/dist/index.js";

/** Fails unless `actual` is `want`. */
export function expect(actual: unknown, want: unknown, what: string): void {
  if (Object.is(actual, want)) return;
  throw new Error(`${what}: want ${show(want)}, got ${show(actual)}`);
}

/** Fails unless `actual` matches `pattern`. */
export function expectMatch(
  actual: string,
  pattern: RegExp,
  what: string,
): void {
  if (pattern.test(actual)) return;
  throw new Error(
    `${what}: want a match for ${String(pattern)}, got ${show(actual)}`,
  );
}

/** Fails unless `haystack` contains `needle`. */
export function expectIncludes(
  haystack: string,
  needle: string,
  what: string,
): void {
  if (haystack.includes(needle)) return;
  throw new Error(
    `${what}: want text containing ${show(needle)}, got ${show(haystack)}`,
  );
}

/** Narrows an optional envelope field, failing when the run did not carry it. */
export function expectPresent<T>(value: T | null | undefined, what: string): T {
  if (value === null || value === undefined) {
    throw new Error(`${what}: want a value, got ${show(value)}`);
  }
  return value;
}

/**
 * Runs `call`, requiring the typed exit-1 refusal it must produce. Any other
 * outcome — including the success envelope a broken guard would return — fails.
 */
export async function expectRefusal(
  what: string,
  call: () => Promise<unknown>,
): Promise<VerdictRefusalError> {
  let result: unknown;
  try {
    result = await call();
  } catch (error) {
    if (error instanceof VerdictRefusalError) return error;
    throw error;
  }
  throw new Error(`${what}: want a typed refusal, got ${show(result)}`);
}

/** One field of the envelope the example is reading. */
export function line(label: string, value: unknown): void {
  console.log(`  ${label.padEnd(20)}${show(value)}`);
}

/** One checkpoint the test asserts on. */
export function ok(step: string): void {
  console.log(`ok: ${step}`);
}

function show(value: unknown): string {
  return typeof value === "string" ? value : JSON.stringify(value);
}
