import type { LoaderFunctionArgs } from "react-router";
import { meQueryOptions } from "./api/auth.queries";
import { reviewItemCountsQueryOptions } from "./api/identity-proofing.queries";
import { myInvitationsQueryOptions } from "./api/invitations.queries";
import { organizationQueryOptions } from "./api/organization.queries";
import { signingAvailabilityQueryOptions } from "./api/signing.queries";
import { orgThemeQueryOptions } from "./api/theme.queries";
import { queryClient } from "./query-client";

// Loaders only start the reads a route is certain to need and return at once,
// so they run next to the route's code download instead of after it. The
// components keep reading through their query hooks.

export function sessionLoader(): null {
  void queryClient.prefetchQuery(meQueryOptions);
  // The shell's own read, on every signed-in page.
  void queryClient.prefetchQuery(myInvitationsQueryOptions);
  return null;
}

export function organizationLoader({ params }: LoaderFunctionArgs): null {
  const slug = params.orgSlug ?? "";
  if (slug !== "") {
    void queryClient.prefetchQuery(organizationQueryOptions(slug));
    void queryClient.prefetchQuery(orgThemeQueryOptions(slug));
    // The sidebar's reads, on every page of the org.
    void queryClient.prefetchQuery(reviewItemCountsQueryOptions(slug));
    void queryClient.prefetchQuery(signingAvailabilityQueryOptions(slug));
  }
  return null;
}
