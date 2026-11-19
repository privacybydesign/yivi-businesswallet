import { useEffect, useRef, useState } from "react";

// The browser retries a dropped connection, never one answered with an error
// status: that closes the stream for good, so it is opened again after this
// pause.
export const REOPEN_AFTER_ERROR_MS = 5_000;

// The backend pings an open stream every 15 s (keepAlive in internal/events).
export const STREAM_PING_EVENT = "ping";

// A stream that was silent this long is dead without the browser having been
// told (a proxy kept its side open, the laptop slept), so it is opened again:
// two pings missed, and some slack.
export const STREAM_STALE_AFTER_MS = 40_000;

// Follows a Server-Sent Events stream of a page that is alone in following it
// (a public link's page; an org's pages share one through org-events.ts).
// onChange runs on each of the named events and each time the stream
// (re)connects, for what changed while it was down. It returns whether the
// stream is up: while it is, the page's read does not poll.
export function useEventStream(
  url: string | null,
  kinds: readonly string[],
  onChange: () => void,
): boolean {
  const [connected, setConnected] = useState(false);
  const changed = useRef(onChange);
  useEffect(() => {
    changed.current = onChange;
  });

  useEffect(() => {
    if (url === null || typeof EventSource === "undefined") {
      return;
    }
    let stream: EventSource | undefined;
    let reopen: ReturnType<typeof setTimeout> | undefined;
    let stale: ReturnType<typeof setTimeout> | undefined;
    const connect = (): void => {
      const current = new EventSource(url, { withCredentials: true });
      stream = current;
      const drop = (reopenAfterMs: number): void => {
        clearTimeout(stale);
        current.close();
        setConnected(false);
        reopen = setTimeout(connect, reopenAfterMs);
      };
      // Anything heard proves the stream lives; silence past the limit ends it.
      const heard = (): void => {
        clearTimeout(stale);
        stale = setTimeout(() => drop(0), STREAM_STALE_AFTER_MS);
      };
      heard();
      current.addEventListener("open", () => {
        heard();
        setConnected(true);
        changed.current();
      });
      current.addEventListener(STREAM_PING_EVENT, heard);
      for (const kind of kinds) {
        current.addEventListener(kind, () => {
          heard();
          changed.current();
        });
      }
      current.addEventListener("error", () => drop(REOPEN_AFTER_ERROR_MS));
    };
    connect();
    return () => {
      clearTimeout(reopen);
      clearTimeout(stale);
      stream?.close();
      setConnected(false);
    };
  }, [url, kinds]);

  return connected;
}
