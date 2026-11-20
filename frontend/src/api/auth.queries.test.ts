import { describe, expect, it } from "vitest";
import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { meQueryKey, signOutCache } from "./auth.queries";

// watch reads a query the way a mounted component does: subscribed, and told of
// every change.
function watch(
  queryClient: QueryClient,
  queryKey: readonly unknown[],
): { data: () => unknown; stop: () => void } {
  const observer = new QueryObserver(queryClient, {
    queryKey,
    enabled: false,
  });
  let data: unknown = observer.getCurrentResult().data;
  const stop = observer.subscribe((result) => {
    data = result.data;
  });
  return { data: () => data, stop };
}

describe("signOutCache", () => {
  it("tells every screen that reads the user it is signed out", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(meQueryKey, { email: "alice@example.test" });
    // The route guard and the page shell each read the user.
    const guard = watch(queryClient, meQueryKey);
    const shell = watch(queryClient, meQueryKey);

    signOutCache(queryClient);

    expect(guard.data()).toBeNull();
    expect(shell.data()).toBeNull();
    guard.stop();
    shell.stop();
  });

  it("drops everything else the user read", () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(meQueryKey, { email: "alice@example.test" });
    queryClient.setQueryData(["orgs", "acme", "members"], [{ id: 1 }]);
    // Shares the first part of its key with "me", and is the user's own data.
    queryClient.setQueryData(["me", "invitations"], [{ id: 2 }]);

    signOutCache(queryClient);

    expect(
      queryClient.getQueryData(["orgs", "acme", "members"]),
    ).toBeUndefined();
    expect(queryClient.getQueryData(["me", "invitations"])).toBeUndefined();
    expect(queryClient.getQueryData(meQueryKey)).toBeNull();
    expect(queryClient.getQueryCache().getAll()).toHaveLength(1);
  });
});
