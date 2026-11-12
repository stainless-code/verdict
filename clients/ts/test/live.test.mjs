import { it } from "node:test";
import assert from "node:assert/strict";
import { refetchOnBundleEvent } from "../dist/live.js";

it("refetches on bundle invalidation and unregisters on cleanup", () => {
  const listeners = new Map();
  const events = {
    addEventListener(type, listener) {
      const typed = listeners.get(type) ?? new Set();
      typed.add(listener);
      listeners.set(type, typed);
    },
    removeEventListener(type, listener) {
      listeners.get(type)?.delete(listener);
    },
  };
  const dispatch = (type) => {
    for (const listener of listeners.get(type) ?? []) listener(new Event(type));
  };
  let refetches = 0;
  const cleanup = refetchOnBundleEvent(events, () => {
    refetches += 1;
  });

  dispatch("bundles");
  assert.equal(refetches, 1);
  dispatch("error");
  dispatch("open");
  assert.equal(refetches, 2);
  dispatch("open");
  assert.equal(refetches, 2);

  cleanup();
  for (const typed of listeners.values()) assert.equal(typed.size, 0);
});
