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
import { useTranslation } from "react-i18next";
import {
  createDepartment,
  deleteDepartment,
  deleteOrganization,
  getIdentitySettings,
  getMemberAuditEvents,
  getMyOrganizations,
  getOrganization,
  getOrganizationAuditEvents,
  getOrganizationDepartments,
  getOrganizationMember,
  getOrganizationMembers,
  getOrganizations,
  inviteMember,
  mintOwnReidentifyLink,
  removeMember,
  requestIdentification,
  resendInvitation,
  revokeInvitation,
  saveIdentitySettings,
  updateDepartment,
  updateMemberType,
  updateOrganization,
  updateOrganizationMember,
} from "./organization";
import type {
  AuditEventsPage,
  Department,
  IdentitySettings,
  IdentitySettingsInput,
  Member,
  MemberListPage,
  MemberListParams,
  MemberType,
  Organization,
  OrganizationDetail,
  RequestIdentificationResult,
} from "./organization";
import { toast } from "../lib/toast";

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

export function useDeleteOrganizationMutation(): UseMutationResult<
  void,
  Error,
  string
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (id) => deleteOrganization(id),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.organizationDeleted"));
      void queryClient.invalidateQueries({ queryKey: organizationsQueryKey });
      void queryClient.invalidateQueries({ queryKey: myOrganizationsQueryKey });
    },
  });
}

export function useUpdateOrganizationMutation(
  slug: string,
): UseMutationResult<void, Error, { name: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => updateOrganization(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.organizationUpdated"));
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
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => createDepartment(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.departmentAdded"));
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
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ departmentId, name }) =>
      updateDepartment(slug, departmentId, { name }),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.departmentRenamed"));
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
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ departmentId }) => deleteDepartment(slug, departmentId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.departmentDeleted"));
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
    memberType?: MemberType;
    externalOrganisation?: string;
  }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => inviteMember(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.invitationSent"));
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
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ invitationId }) => resendInvitation(slug, invitationId),
    onSuccess: () => {
      toast.success(t("toasts.invitationResent"));
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
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ invitationId }) => revokeInvitation(slug, invitationId),
    onSuccess: () => {
      toast.success(t("toasts.invitationRevoked"));
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}

export function useRemoveMemberMutation(
  slug: string,
): UseMutationResult<void, Error, { userId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ userId }) => removeMember(slug, userId),
    meta: { suppressErrorToast: true },
    onSuccess: (_data, { userId }) => {
      toast.success(t("toasts.memberRemoved"));
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationMemberQueryKey(slug, userId),
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
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ userId, role, jobTitle, departmentId }) =>
      updateOrganizationMember(slug, userId, { role, jobTitle, departmentId }),
    meta: { suppressErrorToast: true },
    onSuccess: (_data, { userId }) => {
      toast.success(t("toasts.memberUpdated"));
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

export function identitySettingsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "identity-settings"];
}

export function useIdentitySettingsQuery(
  slug: string,
  enabled: boolean,
): UseQueryResult<IdentitySettings, Error> {
  return useQuery({
    queryKey: identitySettingsQueryKey(slug),
    queryFn: ({ signal }) => getIdentitySettings(slug, signal),
    enabled: enabled && slug !== "",
  });
}

// Saving the policy recomputes every member's due date server-side, so the
// member list and detail caches are invalidated alongside the settings.
export function useSaveIdentitySettingsMutation(
  slug: string,
): UseMutationResult<IdentitySettings, Error, IdentitySettingsInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => saveIdentitySettings(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: (settings) => {
      toast.success(t("toasts.identitySettingsSaved"));
      queryClient.setQueryData(identitySettingsQueryKey(slug), settings);
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}

export function useRequestIdentificationMutation(
  slug: string,
): UseMutationResult<
  RequestIdentificationResult,
  Error,
  { userIds: string[]; reason?: string }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ userIds, reason }) =>
      requestIdentification(slug, userIds, reason),
    meta: { suppressErrorToast: true },
    onSuccess: (result, { userIds }) => {
      toast.success(
        t("toasts.identificationRequested", { count: result.requested }),
      );
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
      for (const userId of userIds) {
        void queryClient.invalidateQueries({
          queryKey: organizationMemberQueryKey(slug, userId),
        });
      }
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}

export function useUpdateMemberTypeMutation(slug: string): UseMutationResult<
  Member,
  Error,
  {
    userId: string;
    memberType: MemberType;
    externalOrganisation: string | null;
  }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ userId, memberType, externalOrganisation }) =>
      updateMemberType(slug, userId, { memberType, externalOrganisation }),
    meta: { suppressErrorToast: true },
    onSuccess: (member, { userId }) => {
      toast.success(t("toasts.memberUpdated"));
      queryClient.setQueryData(
        organizationMemberQueryKey(slug, userId),
        member,
      );
      void queryClient.invalidateQueries({
        queryKey: organizationMembersQueryKey(slug),
      });
      void queryClient.invalidateQueries({
        queryKey: organizationAuditEventsQueryKey(slug),
      });
    },
  });
}

// The member's own "re-identify now" action: it mints a link and the caller
// navigates to it, so there is nothing to cache.
export function useMintOwnReidentifyLinkMutation(
  slug: string,
): UseMutationResult<string, Error, void> {
  return useMutation({
    mutationFn: () => mintOwnReidentifyLink(slug),
    meta: { suppressErrorToast: true },
  });
}
