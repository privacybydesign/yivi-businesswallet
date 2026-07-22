import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  deletePostguardApiKey,
  deletePostguardEncryptionKey,
  getPostguardFiles,
  getPostguardSettings,
  sendPostguardFile,
  setPostguardApiKey,
  setPostguardEncryptionKey,
  setPostguardNotifications,
} from "./postguard";
import type {
  PostguardNotificationDelivery,
  PostguardSentFile,
  PostguardSettings,
  SendPostguardFileInput,
} from "./postguard";
import { toast } from "../lib/toast";

export function postguardSettingsQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "postguard", "settings"];
}

export function postguardFilesQueryKey(slug: string): readonly string[] {
  return ["organizations", "detail", slug, "postguard", "files"];
}

export function usePostguardSettingsQuery(
  slug: string,
  enabled = true,
): UseQueryResult<PostguardSettings, Error> {
  return useQuery({
    queryKey: postguardSettingsQueryKey(slug),
    queryFn: ({ signal }) => getPostguardSettings(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function usePostguardFilesQuery(
  slug: string,
  enabled = true,
): UseQueryResult<PostguardSentFile[], Error> {
  return useQuery({
    queryKey: postguardFilesQueryKey(slug),
    queryFn: ({ signal }) => getPostguardFiles(slug, signal),
    enabled: enabled && slug !== "",
  });
}

export function useSetPostguardEncryptionKeyMutation(
  slug: string,
): UseMutationResult<PostguardSettings, Error, { key: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => setPostguardEncryptionKey(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.postguardEncryptionKeySaved"));
      void queryClient.invalidateQueries({
        queryKey: postguardSettingsQueryKey(slug),
      });
    },
  });
}

export function useDeletePostguardEncryptionKeyMutation(
  slug: string,
): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => deletePostguardEncryptionKey(slug),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.postguardEncryptionKeyRemoved"));
      void queryClient.invalidateQueries({
        queryKey: postguardSettingsQueryKey(slug),
      });
    },
  });
}

export function useSetPostguardApiKeyMutation(
  slug: string,
): UseMutationResult<PostguardSettings, Error, { apiKey: string }> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => setPostguardApiKey(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.postguardKeySaved"));
      void queryClient.invalidateQueries({
        queryKey: postguardSettingsQueryKey(slug),
      });
    },
  });
}

export function useDeletePostguardApiKeyMutation(
  slug: string,
): UseMutationResult<void, Error, void> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: () => deletePostguardApiKey(slug),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.postguardKeyRemoved"));
      void queryClient.invalidateQueries({
        queryKey: postguardSettingsQueryKey(slug),
      });
    },
  });
}

export function useSetPostguardNotificationsMutation(
  slug: string,
): UseMutationResult<
  PostguardSettings,
  Error,
  { notifications: PostguardNotificationDelivery }
> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => setPostguardNotifications(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.postguardNotificationsSaved"));
      void queryClient.invalidateQueries({
        queryKey: postguardSettingsQueryKey(slug),
      });
    },
  });
}

export function useSendPostguardFileMutation(
  slug: string,
): UseMutationResult<PostguardSentFile, Error, SendPostguardFileInput> {
  const queryClient = useQueryClient();
  const { t } = useTranslation();
  return useMutation({
    mutationFn: (input) => sendPostguardFile(slug, input),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.postguardFileSent"));
      void queryClient.invalidateQueries({
        queryKey: postguardFilesQueryKey(slug),
      });
    },
  });
}
