import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { Query } from "@tanstack/react-query";
import { absoluteApiUrl } from "./http";
import { followOrgEvents, setOrgStreamConnected } from "./org-events";
import type { OrgEventKind } from "./org-events";
import {
  proofingRequestsQueryKey,
  proofingWebhookQueryKey,
} from "./identity-proofing.queries";
import { qerdsMessagesQueryKey } from "./qerds.queries";
import { signingRequestReads } from "./signing.queries";

function orgEventsUrl(slug: string): string {
  return absoluteApiUrl(`/api/v1/orgs/${encodeURIComponent(slug)}/events`);
}

// How long events of one kind are gathered before their reads run once, so a
// burst (a bulk run over the API) is one re-read. The lists can wait a
// moment; a verify page shows a person's own progress.
const LIST_GATE_MS = 1_000;
const PROGRESS_GATE_MS = 250;

// The last part of the keys a verify session's progress is read under.
const PROGRESS_READS: ReadonlySet<unknown> = new Set(["progress", "app"]);

// What each kind of event re-reads, and after how long.
const REREADS: Record<
  OrgEventKind,
  {
    gateMs: number;
    // The reads this kind covers: their key prefix and, within it, which.
    reads: (slug: string) => {
      queryKey?: readonly unknown[];
      predicate?: (query: Query) => boolean;
    };
  }
> = {
  reviews: {
    gateMs: LIST_GATE_MS,
    reads: (slug) => ({ queryKey: proofingRequestsQueryKey(slug) }),
  },
  "proofing-progress": {
    gateMs: PROGRESS_GATE_MS,
    reads: (slug) => ({
      queryKey: proofingRequestsQueryKey(slug),
      predicate: (query) => PROGRESS_READS.has(query.queryKey.at(-1)),
    }),
  },
  webhooks: {
    gateMs: LIST_GATE_MS,
    reads: (slug) => ({ queryKey: proofingWebhookQueryKey(slug) }),
  },
  qerds: {
    gateMs: LIST_GATE_MS,
    reads: (slug) => ({ queryKey: qerdsMessagesQueryKey(slug) }),
  },
  signing: {
    gateMs: LIST_GATE_MS,
    reads: (slug) => ({ predicate: signingRequestReads(slug) }),
  },
};

const KINDS = Object.keys(REREADS) as OrgEventKind[];

// Re-reads what an event says changed, and each time the stream (re)connects
// whatever was read before it dropped, for what changed while it was down: the pages follow
// at once, with no timer. Mounted once, by the sidebar, for every member. The
// browser's tabs share one stream (followOrgEvents).
export function useOrgEvents(slug: string, enabled: boolean): void {
  const queryClient = useQueryClient();
  useEffect(() => {
    if (!enabled || slug === "") {
      return;
    }
    const pending = new Map<OrgEventKind, ReturnType<typeof setTimeout>>();
    // Since when a change may have gone unheard: from the start of following,
    // and from each drop of the stream.
    let unheardSince = Date.now();
    const reread = (kind: OrgEventKind, readBefore?: number): void => {
      const { queryKey, predicate } = REREADS[kind].reads(slug);
      void queryClient.invalidateQueries(
        {
          queryKey,
          predicate: (query) =>
            (predicate === undefined || predicate(query)) &&
            (readBefore === undefined ||
              query.state.dataUpdatedAt < readBefore),
        },
        // An event means the data changed, so a read under way is restarted;
        // on a (re)connect a read under way is recent enough.
        { cancelRefetch: readBefore === undefined },
      );
    };
    const stop = followOrgEvents(
      slug,
      () => new EventSource(orgEventsUrl(slug), { withCredentials: true }),
      (what) => {
        setOrgStreamConnected(slug, what !== "disconnected");
        if (what === "disconnected") {
          unheardSince = Date.now();
          return;
        }
        if (what === "connected") {
          // Only what was read before the stream could have told of a change:
          // re-reading what the page loaded a moment ago, next to opening the
          // stream, would load every list twice.
          for (const kind of KINDS) reread(kind, unheardSince);
          return;
        }
        if (pending.has(what)) {
          return;
        }
        pending.set(
          what,
          setTimeout(() => {
            pending.delete(what);
            reread(what);
          }, REREADS[what].gateMs),
        );
      },
    );
    return () => {
      stop();
      for (const timer of pending.values()) clearTimeout(timer);
      setOrgStreamConnected(slug, false);
    };
  }, [queryClient, slug, enabled]);
}
