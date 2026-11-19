import { useSyncExternalStore } from "react";
import {
  REOPEN_AFTER_ERROR_MS,
  STREAM_PING_EVENT,
  STREAM_STALE_AFTER_MS,
} from "./event-stream";

// The org's event stream (GET /orgs/{slug}/events) is one long-held connection
// that names what changed, so no page polls for it. Over HTTP/1.1 a browser
// opens at most six per host, for all its tabs together, so a stream per tab
// would leave the other tabs' requests queued. The tabs of one browser share
// one stream instead: the tab holding the org's lock opens it and passes every
// event on to the others; when it closes, the next tab takes over.

// What the stream can say changed; mirrors OrgKinds in the backend's
// internal/events.
export const ORG_EVENT_KINDS = [
  "reviews",
  "proofing-progress",
  "webhooks",
  "qerds",
  "signing",
] as const;

export type OrgEventKind = (typeof ORG_EVENT_KINDS)[number];

// The parts of an EventSource the leader uses.
export interface OrgStream {
  addEventListener(type: string, listener: () => void): void;
  close(): void;
}

// The lock and channel name for one org's stream.
function shareName(slug: string): string {
  return `org-events:${slug}`;
}

// What a follower is told: the stream (re)connected, so anything may have
// changed while it was down; the stream dropped; or what changed.
export type OrgSignal = "connected" | "disconnected" | OrgEventKind;

// Calls signal each time something of the org changes, and each time the
// stream connects or drops. It returns the function that stops following them.
// Without Web Locks or BroadcastChannel (a page not served over https or
// localhost) the tab opens a stream of its own.
export function followOrgEvents(
  slug: string,
  open: () => OrgStream,
  signal: (what: OrgSignal) => void,
  staleAfterMs: number = STREAM_STALE_AFTER_MS,
): () => void {
  if (
    typeof BroadcastChannel === "undefined" ||
    typeof navigator === "undefined" ||
    navigator.locks === undefined
  ) {
    return lead(open, signal, staleAfterMs);
  }
  const channel = new BroadcastChannel(shareName(slug));
  channel.onmessage = (message: MessageEvent<OrgSignal>) =>
    signal(message.data);
  const stop = new AbortController();
  void navigator.locks
    .request(shareName(slug), { signal: stop.signal }, async () => {
      // Granted just as this tab stopped following: leading now would hold
      // the lock, and the stream, for nobody, and keep the next follower out.
      if (stop.signal.aborted) {
        return;
      }
      const close = lead(
        open,
        (what) => {
          if (!stop.signal.aborted) {
            channel.postMessage(what);
          }
          signal(what);
        },
        staleAfterMs,
      );
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

// Opens the stream and signals each event, connect and drop. A stream that
// errors is opened again after a pause; one that went silent (staleAfterMs
// without a ping or an event) is dead without the browser knowing, and is
// opened again at once.
function lead(
  open: () => OrgStream,
  signal: (what: OrgSignal) => void,
  staleAfterMs: number,
): () => void {
  let stream: OrgStream | undefined;
  let reopen: ReturnType<typeof setTimeout> | undefined;
  let stale: ReturnType<typeof setTimeout> | undefined;
  const connect = (): void => {
    const current = open();
    stream = current;
    const drop = (reopenAfterMs: number): void => {
      clearTimeout(stale);
      current.close();
      // Scheduled first: whatever the follower does with the news, the stream
      // comes back.
      reopen = setTimeout(connect, reopenAfterMs);
      signal("disconnected");
    };
    const heard = (): void => {
      clearTimeout(stale);
      stale = setTimeout(() => drop(0), staleAfterMs);
    };
    heard();
    current.addEventListener("open", () => {
      heard();
      signal("connected");
    });
    current.addEventListener(STREAM_PING_EVENT, heard);
    for (const kind of ORG_EVENT_KINDS) {
      current.addEventListener(kind, () => {
        heard();
        signal(kind);
      });
    }
    current.addEventListener("error", () => drop(REOPEN_AFTER_ERROR_MS));
  };
  connect();
  return () => {
    clearTimeout(reopen);
    clearTimeout(stale);
    stream?.close();
  };
}

// Whether each org's stream is up, as far as this tab knows, for the reads
// that poll only while it is not (useOrgStreamConnected).
const connected = new Map<string, boolean>();
const listeners = new Set<() => void>();

export function setOrgStreamConnected(slug: string, up: boolean): void {
  if (connected.get(slug) === up) {
    return;
  }
  connected.set(slug, up);
  for (const listener of listeners) listener();
}

function subscribeOrgStream(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// Whether the org's event stream is up. While it is, a read that follows it
// does not poll: every change reaches it as an event.
export function useOrgStreamConnected(slug: string): boolean {
  return useSyncExternalStore(
    subscribeOrgStream,
    () => connected.get(slug) === true,
    () => false,
  );
}
