import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type {
  InfiniteData,
  UseInfiniteQueryResult,
  UseMutationResult,
  UseQueryResult,
} from "@tanstack/react-query";
import {
  createDepartment,
  createOrganization,
  deleteDepartment,
  getMyOrganizations,
  getOrganization,
  getOrganizationAuditEvents,
  getOrganizationDepartments,
  getOrganizationMembers,
  getOrganizations,
  inviteMember,
  updateDepartment,
  updateOrganization,
  updateOrganizationMember,
} from "./organization";
import type {
  AuditEventsPage,
  Department,
  MemberListEntry,
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

export function organizationAuditEventsQueryKey(
  slug: string,
): readonly string[] {
  return ["organizations", "detail", slug, "audit-events"];
}

export function useOrganizationAuditEventsQuery(
  slug: string,
  enabled: boolean,
): UseInfiniteQueryResult<InfiniteData<AuditEventsPage>, Error> {
  return useInfiniteQuery({
    queryKey: organizationAuditEventsQueryKey(slug),
    queryFn: ({ pageParam, signal }) =>
      getOrganizationAuditEvents(slug, pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    enabled: enabled && slug !== "",
  });
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

export function useUpdateOrganizationMutation(
  slug: string,
): UseMutationResult<void, Error, { name: string }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input) => updateOrganization(slug, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: organizationsQueryKey });
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
): UseQueryResult<MemberListEntry[], Error> {
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
): UseMutationResult<void, Error, { name: string }> {
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
): UseMutationResult<void, Error, { departmentId: string; name: string }> {
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
  void,
  Error,
  {
    email: string;
    givenNames: string;
    lastName: string;
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

export function useUpdateMemberMutation(slug: string): UseMutationResult<
  void,
  Error,
  {
    userId: string;
    role: string;
    jobTitle: string | null;
    departmentId: string | null;
  }
> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, role, jobTitle, departmentId }) =>
      updateOrganizationMember(slug, userId, { role, jobTitle, departmentId }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
    },
  });
}
