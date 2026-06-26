import { useQuery } from "@tanstack/react-query";
import type { UseQueryResult } from "@tanstack/react-query";
import {
  getMyOrganizations,
  getOrganization,
  getOrganizationMembers,
  getOrganizations,
} from "./organization";
import type { Member, Organization, OrganizationDetail } from "./organization";

export const organizationsQueryKey = ["organizations"] as const;
export const myOrganizationsQueryKey = ["organizations", "mine"] as const;

export function organizationQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug];
}

export function organizationMembersQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "members"];
}

export function useOrganizationsQuery(): UseQueryResult<Organization[], Error> {
  return useQuery({
    queryKey: organizationsQueryKey,
    queryFn: ({ signal }) => getOrganizations(signal),
  });
}

export function useMyOrganizationsQuery(): UseQueryResult<
  Organization[],
  Error
> {
  return useQuery({
    queryKey: myOrganizationsQueryKey,
    queryFn: ({ signal }) => getMyOrganizations(signal),
  });
}

export function useOrganizationQuery(
  slug: string,
): UseQueryResult<OrganizationDetail, Error> {
  return useQuery({
    queryKey: organizationQueryKey(slug),
    queryFn: ({ signal }) => getOrganization(slug, signal),
    enabled: slug !== "",
  });
}

export function useOrganizationMembersQuery(
  slug: string,
  enabled: boolean,
): UseQueryResult<Member[], Error> {
  return useQuery({
    queryKey: organizationMembersQueryKey(slug),
    queryFn: ({ signal }) => getOrganizationMembers(slug, signal),
    enabled: enabled && slug !== "",
  });
}
