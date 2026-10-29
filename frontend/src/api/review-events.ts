// The review stream (GET .../reviews/events) is one long-held connection. Over
// HTTP/1.1 a browser opens at most six per host, for all its tabs together, so
// a stream per tab would leave the other tabs' requests queued. The tabs of one
// browser share one stream instead: the tab holding the org's lock opens it and
// passes every change on to the others; when it closes, the next tab takes over.

// The event the stream sends when what the org's reviewers see changed.
const REVIEWS_CHANGED_EVENT = "reviews";

// The parts of an EventSource the leader uses.
export interface ReviewStream {
  addEventListener(type: string, listener: () => void): void;
  close(): void;
}

// The lock and channel name for one org's stream.
function shareName(slug: string): string {
  return `review-events:${slug}`;
}

// Calls changed each time the org's reviews change, and each time the stream
// (re)connects, for what changed while it was down. It returns the function
// that stops following them. Without Web Locks or BroadcastChannel (a page not
// served over https or localhost) the tab opens a stream of its own.
export function followReviewEvents(
  slug: string,
  open: () => ReviewStream,
  changed: () => void,
): () => void {
  if (
    typeof BroadcastChannel === "undefined" ||
    typeof navigator === "undefined" ||
    navigator.locks === undefined
  ) {
    return lead(open, changed);
  }
  const channel = new BroadcastChannel(shareName(slug));
  channel.onmessage = changed;
  const stop = new AbortController();
  void navigator.locks
    .request(shareName(slug), { signal: stop.signal }, async () => {
      const close = lead(open, () => {
        channel.postMessage(null);
        changed();
      });
      // Hold the lock, and so the stream, until this tab stops following.
      await new Promise((resolve) => {
        stop.signal.addEventListener("abort", resolve, { once: true });
      });
      close();
    })
    .catch((error: unknown) => {
      // Stopped while another tab led: there is nothing to close.
      if (!(error instanceof DOMException && error.name === "AbortError")) {
        throw error;
      }
    });
  return () => {
    stop.abort();
    channel.close();
  };
}

// Opens the stream and calls changed on each change and each (re)connect.
function lead(open: () => ReviewStream, changed: () => void): () => void {
  const stream = open();
  stream.addEventListener("open", changed);
  stream.addEventListener(REVIEWS_CHANGED_EVENT, changed);
  return () => stream.close();
}
