import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ProofingRequest } from "./identity-proofing";
import { MAX_LISTED_REQUESTS } from "./identity-proofing";
import {
  combineStatusReads,
  proofingRequestResultQueryKey,
  proofingRequestsQueryKey,
  refreshAfterReviewDecision,
} from "./identity-proofing.queries";

const SLUG = "acme";
const REQUEST_ID = "r1";

// An observer stands for a mounted page: a query with a listener refetches the
// moment it is invalidated, unless told not to. It is returned once the first
// read landed, since an invalidation during that read only joins it.
async function mount(
  client: QueryClient,
  queryKey: readonly string[],
  queryFn: () => Promise<string>,
): Promise<() => void> {
  const observer = new QueryObserver(client, {
    queryKey,
    queryFn,
    staleTime: Infinity,
  });
  const unmount = observer.subscribe(() => undefined);
  await vi.waitFor(() => expect(client.getQueryData(queryKey)).toBeDefined());
  return unmount;
}

describe("refreshAfterReviewDecision", () => {
  it("does not read the result again under the page being left", async () => {
    const client = new QueryClient();
    const read = vi.fn(() => Promise.resolve("result"));
    const unmount = await mount(
      client,
      proofingRequestResultQueryKey(SLUG, REQUEST_ID),
      read,
    );

    refreshAfterReviewDecision(client, SLUG, REQUEST_ID);
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(read).toHaveBeenCalledTimes(1);
    unmount();
  });

  it("marks the result stale, so the next page reads the outcome", async () => {
    const client = new QueryClient();
    const key = proofingRequestResultQueryKey(SLUG, REQUEST_ID);
    const unmount = await mount(client, key, () => Promise.resolve("result"));

    refreshAfterReviewDecision(client, SLUG, REQUEST_ID);

    expect(client.getQueryState(key)?.isInvalidated).toBe(true);
    unmount();
  });

  it("refetches the lists, which carry the counts", async () => {
    const client = new QueryClient();
    const list = vi.fn(() => Promise.resolve("list"));
    const unmount = await mount(
      client,
      [...proofingRequestsQueryKey(SLUG), "waiting"],
      list,
    );

    refreshAfterReviewDecision(client, SLUG, REQUEST_ID);

    await vi.waitFor(() => expect(list).toHaveBeenCalledTimes(2));
    unmount();
  });
});

function listedRequest(id: string): ProofingRequest {
  return {
    id,
    requestedByName: "Sam",
    external: true,
    subjectName: "Noor",
    subjectEmail: "noor@example.org",
    flowId: "f1",
    flowName: "See my data",
    status: "approved",
    linkExpiresAt: "2026-10-30T00:00:00Z",
    createdAt: "2026-10-16T14:00:00Z",
    diplomaMode: "off",
    diplomas: [],
    addressMode: "off",
    expectedSubject: false,
    flowKind: "data_access",
  };
}

const loaded = (data: ProofingRequest[]) => ({
  data,
  isPending: false,
  isError: false,
  error: null,
});
const loading = {
  data: undefined,
  isPending: true,
  isError: false,
  error: null,
};

describe("combineStatusReads", () => {
  it("joins the reads in the order of their statuses once all landed", () => {
    const reads = combineStatusReads(
      ["approved", "rejected"],
      [loaded([listedRequest("a")]), loaded([listedRequest("b")])],
    );

    expect(reads.data?.map((r) => r.id)).toEqual(["a", "b"]);
    expect(reads.isPending).toBe(false);
  });

  it("holds no list while one status is still loading", () => {
    const reads = combineStatusReads(
      ["approved", "rejected"],
      [loaded([listedRequest("a")]), loading],
    );

    expect(reads.data).toBeUndefined();
    expect(reads.isPending).toBe(true);
  });

  it("reports the failure of either status", () => {
    const failure = new Error("boom");
    const reads = combineStatusReads(
      ["approved", "rejected"],
      [
        loaded([listedRequest("a")]),
        { data: undefined, isPending: false, isError: true, error: failure },
      ],
    );

    expect(reads.isError).toBe(true);
    expect(reads.error).toBe(failure);
    expect(reads.data).toBeUndefined();
  });

  it("names the statuses whose read hit the backend's cap", () => {
    const full = Array.from({ length: MAX_LISTED_REQUESTS }, (_, i) =>
      listedRequest(`p${i}`),
    );
    const reads = combineStatusReads(
      ["pending", "in_progress"],
      [loaded(full), loaded([listedRequest("a")])],
    );

    expect(reads.capped).toEqual(["pending"]);
    expect(reads.data).toHaveLength(MAX_LISTED_REQUESTS + 1);
  });

  it("names none while every read holds less than the cap", () => {
    const reads = combineStatusReads(
      ["approved", "rejected"],
      [loaded([listedRequest("a")]), loading],
    );

    expect(reads.capped).toEqual([]);
    expect(reads.data).toBeUndefined();
  });
});
