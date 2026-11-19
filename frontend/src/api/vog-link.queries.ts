import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { UseMutationResult, UseQueryResult } from "@tanstack/react-query";
import {
  completeVogCredential,
  completeVogIdentity,
  completeVogIdentityCredential,
  getVogPreview,
  uploadVogByLink,
} from "./vog-link";
import type { VogPreview } from "./vog-link";
import type { UploadVogResult } from "./organization";

export const vogPreviewQueryKey = (token: string) =>
  ["vog-link", token] as const;

export function useVogPreviewQuery(
  token: string,
): UseQueryResult<VogPreview, Error> {
  return useQuery({
    queryKey: vogPreviewQueryKey(token),
    queryFn: ({ signal }) => getVogPreview(token, signal),
    retry: false,
    enabled: token !== "",
  });
}

// Every submission may change what the page shows next (identity now on file,
// a new status), so each refreshes the preview - except a valid result, which
// retires the link: refetching would turn the success into "link not found".
function useVogLinkMutation<TVariables, TResult>(
  token: string,
  run: (token: string, variables: TVariables) => Promise<TResult>,
  retiresLink: (result: TResult) => boolean,
): UseMutationResult<TResult, Error, TVariables> {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (variables) => run(token, variables),
    meta: { suppressErrorToast: true },
    onSuccess: (result) => {
      if (retiresLink(result)) return;
      void queryClient.invalidateQueries({
        queryKey: vogPreviewQueryKey(token),
      });
    },
  });
}

const VALID_RESULT = "valid";
const isValid = (outcome: UploadVogResult): boolean =>
  outcome.result === VALID_RESULT;

export function useUploadVogByLinkMutation(
  token: string,
): UseMutationResult<UploadVogResult, Error, File> {
  return useVogLinkMutation(token, uploadVogByLink, isValid);
}

export function useCompleteVogIdentityMutation(
  token: string,
): UseMutationResult<void, Error, string> {
  return useVogLinkMutation(token, completeVogIdentity, () => false);
}

export function useCompleteVogCredentialMutation(
  token: string,
): UseMutationResult<UploadVogResult, Error, string> {
  return useVogLinkMutation(token, completeVogCredential, isValid);
}

export function useCompleteVogIdentityCredentialMutation(
  token: string,
): UseMutationResult<UploadVogResult, Error, string> {
  return useVogLinkMutation(token, completeVogIdentityCredential, isValid);
}
