import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import {
  createDepartment,
  createOrganization,
  deleteDepartment,
  getMyOrganizations,
  getOrganization,
  getOrganizationDepartments,
  getOrganizationMembers,
  getOrganizations,
  inviteMember,
  updateDepartment,
  updateOrganizationMember,
} from "./organization";
import type {
  Department,
  Member,
  Organization,
  OrganizationDetail,
} from "./organization";

export const organizationsQueryKey = ["organizations"] as const;
export const myOrganizationsQueryKey = ["organizations", "mine"] as const;

export function organizationQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug];
}

export function organizationMembersQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "members"];
}

export function organizationDepartmentsQueryKey(
  slug: string,
): readonly string[] {
  return ["organizations", "detail", slug, "departments"];
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

export function useOrganizationDepartmentsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<Department[], Error> {
  return useQuery({
    queryKey: organizationDepartmentsQueryKey(slug),
    queryFn: ({ signal }) => getOrganizationDepartments(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useCreateDepartmentMutation(
  slug: string,
): UseMutationResult<Department, Error, { name: string }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input) => createDepartment(slug, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: organizationDepartmentsQueryKey(slug),
      });
    },
  });
}

export function useUpdateDepartmentMutation(
  slug: string,
): UseMutationResult<
  Department,
  Error,
  { departmentId: string; name: string }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ departmentId, name }) =>
      updateDepartment(slug, departmentId, { name }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: organizationDepartmentsQueryKey(slug),
      });
      // A rename changes the departmentName shown in the members list.
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
    },
  });
}

export function useDeleteDepartmentMutation(
  slug: string,
): UseMutationResult<void, Error, { departmentId: string }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ departmentId }) => deleteDepartment(slug, departmentId),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: organizationDepartmentsQueryKey(slug),
      });
    },
  });
}

export function useInviteMemberMutation(slug: string): UseMutationResult<
  Member,
  Error,
  {
    email: string;
    givenNames: string;
    lastName: string;
    preferredName?: string;
    namePrefix?: string;
    role?: string;
    jobTitle?: string;
    departmentId?: string;
  }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input) => inviteMember(slug, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
    },
  });
}

export function useUpdateMemberMutation(
  slug: string,
): UseMutationResult<
  Member,
  Error,
  { userId: string; jobTitle: string | null; departmentId: string | null }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, jobTitle, departmentId }) =>
      updateOrganizationMember(slug, userId, { jobTitle, departmentId }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
    },
  });
}
