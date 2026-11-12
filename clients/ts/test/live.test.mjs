import { it } from "node:test";
import assert from "node:assert/strict";
import { refetchOnBundleEvent } from "../dist/live.js";

it("refetches on bundle invalidation and unregisters on cleanup", () => {
  const listeners = new Set();
  const events = {
    addEventListener(type, listener) {
      assert.equal(type, "bundles");
      listeners.add(listener);
    },
    removeEventListener(type, listener) {
      assert.equal(type, "bundles");
      listeners.delete(listener);
    },
  };
  let refetches = 0;
  const cleanup = refetchOnBundleEvent(events, () => {
    refetches += 1;
  });

  for (const listener of listeners) listener(new Event("bundles"));
  assert.equal(refetches, 1);
  cleanup();
  assert.equal(listeners.size, 0);
});
