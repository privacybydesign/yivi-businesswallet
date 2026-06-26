import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import {
  createOrganization,
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

export function useOrganizationsQuery(
  enabled = true,
): UseQueryResult<Organization[], Error> {
  return useQuery({
    queryKey: organizationsQueryKey,
    queryFn: ({ signal }) => getOrganizations(signal),
    enabled,
  });
}

export function useCreateOrganizationMutation(): UseMutationResult<
  Organization,
  Error,
  { name: string; slug: string }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input) => createOrganization(input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: organizationsQueryKey });
      void queryClient.invalidateQueries({ queryKey: myOrganizationsQueryKey });
    },
  });
}

export function useMyOrganizationsQuery(
  enabled = true,
): UseQueryResult<Organization[], Error> {
  return useQuery({
    queryKey: myOrganizationsQueryKey,
    queryFn: ({ signal }) => getMyOrganizations(signal),
    enabled,
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
