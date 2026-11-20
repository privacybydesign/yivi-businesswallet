import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type {
  CreateRequestTemplate,
  CredentialType,
  IncomingRequest,
  OutboundRequest,
  RequestTemplate,
  SendCredentialRequest,
} from "./credential-requests";
import {
  approveIncomingRequest,
  createRequestTemplate,
  declineIncomingRequest,
  deleteRequestTemplate,
  getCredentialTypes,
  getIncomingRequests,
  getOutboundRequests,
  getRequestTemplates,
  sendCredentialRequest,
} from "./credential-requests";
import { toast } from "../lib/toast";

export function outboundRequestsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "credential-requests", "outbound"];
}

export function incomingRequestsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "credential-requests", "incoming"];
}

export function credentialTypesQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "credential-requests", "types"];
}

export function useCredentialTypesQuery(
  slug: string,
  enabled = true,
): UseQueryResult<CredentialType[], Error> {
  return useQuery({
    queryKey: credentialTypesQueryKey(slug),
    queryFn: ({ signal }) => getCredentialTypes(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function requestTemplatesQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "credential-requests", "templates"];
}

export function useRequestTemplatesQuery(
  slug: string,
  enabled = true,
): UseQueryResult<RequestTemplate[], Error> {
  return useQuery({
    queryKey: requestTemplatesQueryKey(slug),
    queryFn: ({ signal }) => getRequestTemplates(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useCreateRequestTemplateMutation(
  slug: string,
): UseMutationResult<RequestTemplate, Error, CreateRequestTemplate> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (body: CreateRequestTemplate) =>
      createRequestTemplate(slug, body),
    // The form shows a taken name inline and toasts anything else itself.
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.requestTemplateSaved"));
      void queryClient.invalidateQueries({
        queryKey: requestTemplatesQueryKey(slug),
      });
    },
  });
}

export function useDeleteRequestTemplateMutation(
  slug: string,
): UseMutationResult<void, Error, { id: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ id }) => deleteRequestTemplate(slug, id),
    onSuccess: () => {
      toast.success(t("toasts.requestTemplateDeleted"));
      void queryClient.invalidateQueries({
        queryKey: requestTemplatesQueryKey(slug),
      });
    },
  });
}

export function useOutboundRequestsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<OutboundRequest[], Error> {
  return useQuery({
    queryKey: outboundRequestsQueryKey(slug),
    queryFn: ({ signal }) => getOutboundRequests(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useIncomingRequestsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<IncomingRequest[], Error> {
  return useQuery({
    queryKey: incomingRequestsQueryKey(slug),
    queryFn: ({ signal }) => getIncomingRequests(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useSendCredentialRequestMutation(
  slug: string,
): UseMutationResult<OutboundRequest, Error, SendCredentialRequest> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (body: SendCredentialRequest) =>
      sendCredentialRequest(slug, body),
    onSuccess: () => {
      toast.success(t("toasts.credentialRequestSent"));
      void queryClient.invalidateQueries({
        queryKey: outboundRequestsQueryKey(slug),
      });
    },
  });
}

export function useApproveIncomingRequestMutation(
  slug: string,
): UseMutationResult<void, Error, { id: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ id }) => approveIncomingRequest(slug, id),
    onSuccess: () => {
      toast.success(t("toasts.credentialRequestApproved"));
    },
    // Approved or not, the request left the queue: a failed presentation is
    // consumed as denied on the backend.
    onSettled: () => {
      void queryClient.invalidateQueries({
        queryKey: incomingRequestsQueryKey(slug),
      });
    },
  });
}

export function useDeclineIncomingRequestMutation(
  slug: string,
): UseMutationResult<void, Error, { id: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ id }) => declineIncomingRequest(slug, id),
    onSuccess: () => {
      toast.success(t("toasts.credentialRequestDeclined"));
      void queryClient.invalidateQueries({
        queryKey: incomingRequestsQueryKey(slug),
      });
    },
  });
}
