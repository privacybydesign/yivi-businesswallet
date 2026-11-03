import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import {
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
