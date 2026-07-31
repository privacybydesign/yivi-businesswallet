import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { removeMyAvatar, uploadMyAvatar } from "./avatar";
import type { AvatarResult } from "./avatar";
import { meQueryKey } from "./auth.queries";
import { organizationsQueryKey } from "./organization.queries";
import { toast } from "../lib/toast";

// A changed photo invalidates more than `me`: the member lists and audit logs
// carry the photo's versioned path per user, so their cached pages would keep
// pointing at the previous version. organizationsQueryKey is the shared prefix of
// all of them.
function useAvatarChangeInvalidation(): () => void {
  const queryClient = useQueryClient();
  return () => {
    void queryClient.invalidateQueries({ queryKey: meQueryKey });
    void queryClient.invalidateQueries({ queryKey: organizationsQueryKey });
  };
}

export function useUploadMyAvatarMutation(): UseMutationResult<
  AvatarResult,
  Error,
  Blob
> {
  const { t } = useTranslation();
  const invalidate = useAvatarChangeInvalidation();
  return useMutation({
    mutationFn: (photo: Blob) => uploadMyAvatar(photo),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.avatarSaved"));
      invalidate();
    },
  });
}

export function useRemoveMyAvatarMutation(): UseMutationResult<
  AvatarResult,
  Error,
  void
> {
  const { t } = useTranslation();
  const invalidate = useAvatarChangeInvalidation();
  return useMutation({
    mutationFn: () => removeMyAvatar(),
    meta: { suppressErrorToast: true },
    onSuccess: () => {
      toast.success(t("toasts.avatarRemoved"));
      invalidate();
    },
  });
}
