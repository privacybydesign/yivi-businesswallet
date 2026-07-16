import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  createQerdsAddress,
  createQerdsContact,
  deleteQerdsContact,
  getQerdsAddresses,
  getQerdsContacts,
  getQerdsMessage,
  getQerdsMessages,
  pollQerdsInbox,
  sendQerdsMessage,
  setDefaultQerdsAddress,
} from "./qerds";
import type {
  QerdsAddress,
  QerdsContact,
  QerdsMessage,
  QerdsMessageWithEvidence,
  QerdsPollResult,
} from "./qerds";
import { toast } from "../lib/toast";

export function qerdsMessagesQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "qerds", "messages"];
}

export function qerdsMessageQueryKey(
  slug: string,
  messageId: string,
): readonly string[] {
  return ["organizations", "detail", slug, "qerds", "message", messageId];
}

export function qerdsAddressesQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "qerds", "addresses"];
}

export function useQerdsMessagesQuery(
  slug: string,
  enabled = true,
): UseQueryResult<QerdsMessage[], Error> {
  return useQuery({
    queryKey: qerdsMessagesQueryKey(slug),
    queryFn: ({ signal }) => getQerdsMessages(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useQerdsMessageQuery(
  slug: string,
  messageId: string,
  enabled = true,
): UseQueryResult<QerdsMessageWithEvidence, Error> {
  return useQuery({
    queryKey: qerdsMessageQueryKey(slug, messageId),
    queryFn: ({ signal }) => getQerdsMessage(slug, messageId, signal),
    enabled: enabled && slug !== "" && messageId !== "",
  });
}

export function useQerdsAddressesQuery(
  slug: string,
  enabled = true,
): UseQueryResult<QerdsAddress[], Error> {
  return useQuery({
    queryKey: qerdsAddressesQueryKey(slug),
    queryFn: ({ signal }) => getQerdsAddresses(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useSendQerdsMessageMutation(
  slug: string,
): UseMutationResult<
  QerdsMessage,
  Error,
  { sender?: string; recipient: string; subject: string; body: string }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => sendQerdsMessage(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.qerdsMessageSent"));
      void queryClient.invalidateQueries({
        queryKey: qerdsMessagesQueryKey(slug),
      });
    },
  });
}

export function usePollQerdsInboxMutation(
  slug: string,
): UseMutationResult<QerdsPollResult, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => pollQerdsInbox(slug),
    onSuccess: (result) => {
      toast.success(t("toasts.qerdsInboxChecked", { count: result.received }));
      void queryClient.invalidateQueries({
        queryKey: qerdsMessagesQueryKey(slug),
      });
    },
  });
}

export function useCreateQerdsAddressMutation(
  slug: string,
): UseMutationResult<
  QerdsAddress,
  Error,
  { localPart?: string; default?: boolean }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => createQerdsAddress(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.qerdsAddressAdded"));
      void queryClient.invalidateQueries({
        queryKey: qerdsAddressesQueryKey(slug),
      });
    },
  });
}

export function useSetDefaultQerdsAddressMutation(
  slug: string,
): UseMutationResult<QerdsAddress, Error, { addressId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ addressId }) => setDefaultQerdsAddress(slug, addressId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.qerdsAddressDefaultChanged"));
      void queryClient.invalidateQueries({
        queryKey: qerdsAddressesQueryKey(slug),
      });
    },
  });
}

export function qerdsContactsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "qerds", "contacts"];
}

export function useQerdsContactsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<QerdsContact[], Error> {
  return useQuery({
    queryKey: qerdsContactsQueryKey(slug),
    queryFn: ({ signal }) => getQerdsContacts(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useCreateQerdsContactMutation(
  slug: string,
): UseMutationResult<QerdsContact, Error, { name: string; address: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => createQerdsContact(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.qerdsContactAdded"));
      void queryClient.invalidateQueries({
        queryKey: qerdsContactsQueryKey(slug),
      });
    },
  });
}

export function useDeleteQerdsContactMutation(
  slug: string,
): UseMutationResult<void, Error, { contactId: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: ({ contactId }) => deleteQerdsContact(slug, contactId),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.qerdsContactDeleted"));
      void queryClient.invalidateQueries({
        queryKey: qerdsContactsQueryKey(slug),
      });
    },
  });
}
