/** The EventSource methods used to subscribe to the invalidation event. */
export type BundleEventSource = Pick<
  EventSource,
  "addEventListener" | "removeEventListener"
>;

/** Register a view refresh for each `bundles` invalidation; return its cleanup. */
export function refetchOnBundleEvent(
  events: BundleEventSource,
  refetch: () => void,
): () => void {
  const listener: EventListener = () => refetch();
  events.addEventListener("bundles", listener);
  return () => events.removeEventListener("bundles", listener);
}
