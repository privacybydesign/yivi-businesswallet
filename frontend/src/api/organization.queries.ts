import { useQuery } from "@tanstack/react-query";
import type { UseQueryResult } from "@tanstack/react-query";
import { getOrganizations } from "./organization";
import type { Organization } from "./organization";

export const organizationsQueryKey = ["organizations"] as const;

export function useOrganizationsQuery(): UseQueryResult<Organization[], Error> {
  return useQuery({
    queryKey: organizationsQueryKey,
    queryFn: ({ signal }) => getOrganizations(signal),
  });
}
