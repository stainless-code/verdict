/** The EventSource methods used to subscribe to invalidations and reconnects. */
export type BundleEventSource = Pick<
  EventSource,
  "addEventListener" | "removeEventListener"
>;

/** Register a view refresh for each `bundles` invalidation; return its cleanup. */
export function refetchOnBundleEvent(
  events: BundleEventSource,
  refetch: () => void,
): () => void {
  let disconnected = false;
  const onBundle: EventListener = () => refetch();
  const onError: EventListener = () => {
    disconnected = true;
  };
  const onOpen: EventListener = () => {
    if (!disconnected) return;
    disconnected = false;
    refetch();
  };
  events.addEventListener("bundles", onBundle);
  events.addEventListener("error", onError);
  events.addEventListener("open", onOpen);
  return () => {
    events.removeEventListener("bundles", onBundle);
    events.removeEventListener("error", onError);
    events.removeEventListener("open", onOpen);
  };
}
