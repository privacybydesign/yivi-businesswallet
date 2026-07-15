import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  createQerdsAddress,
  getQerdsAddresses,
  getQerdsMessage,
  getQerdsMessages,
  pollQerdsInbox,
  sendQerdsMessage,
} from "./qerds";
import type {
  QerdsAddress,
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
  { recipient: string; subject: string; body: string; attachments?: File[] }
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
