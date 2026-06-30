import {
  keepPreviousData,
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
  getMemberAuditEvents,
  getMyOrganizations,
  getOrganization,
  getOrganizationAuditEvents,
  getOrganizationDepartments,
  getOrganizationMember,
  getOrganizationMembers,
  getOrganizations,
  inviteMember,
  resendInvitation,
  revokeInvitation,
  updateDepartment,
  updateOrganization,
  updateOrganizationMember,
} from "./organization";
import type {
  AuditEventsPage,
  Department,
  Member,
  MemberListPage,
  MemberListParams,
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

export function memberAuditEventsQueryKey(
  slug: string,
  userId: string,
): readonly string[] {
  return ["organizations", "detail", slug, "members", userId, "audit-events"];
}

export function useMemberAuditEventsQuery(
  slug: string,
  userId: string,
  enabled: boolean,
): UseInfiniteQueryResult<InfiniteData<AuditEventsPage>, Error> {
  return useInfiniteQuery({
    queryKey: memberAuditEventsQueryKey(slug, userId),
    queryFn: ({ pageParam, signal }) =>
      getMemberAuditEvents(slug, userId, pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor ?? undefined,
    enabled: enabled && slug !== "" && userId !== "",
  });
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
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
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
  params: MemberListParams,
  enabled: boolean,
): UseQueryResult<MemberListPage, Error> {
  return useQuery({
    queryKey: [...organizationMembersQueryKey(slug), params],
    queryFn: ({ signal }) => getOrganizationMembers(slug, params, signal),
    enabled: enabled && slug !== "",
    placeholderData: keepPreviousData,
  });
}

export function organizationMemberQueryKey(
  slug: string,
  userId: string,
): readonly string[] {
  return ["organizations", "detail", slug, "member", userId];
}

export function useOrganizationMemberQuery(
  slug: string,
  userId: string,
  enabled: boolean,
): UseQueryResult<Member, Error> {
  return useQuery({
    queryKey: organizationMemberQueryKey(slug, userId),
    queryFn: ({ signal }) => getOrganizationMember(slug, userId, signal),
    enabled: enabled && slug !== "" && userId !== "",
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
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
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
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
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
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
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
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}

export function useResendInvitationMutation(
  slug: string,
): UseMutationResult<void, Error, { invitationId: string }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ invitationId }) => resendInvitation(slug, invitationId),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}

export function useRevokeInvitationMutation(
  slug: string,
): UseMutationResult<void, Error, { invitationId: string }> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ invitationId }) => revokeInvitation(slug, invitationId),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
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
    onSuccess: (_data, { userId }) => {
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationMemberQueryKey(slug, userId),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: memberAuditEventsQueryKey(slug, userId),
      });
    },
  });
}
