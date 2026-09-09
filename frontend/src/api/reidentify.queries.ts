import { useQuery } from "@tanstack/react-query";
import type { UseQueryResult } from "@tanstack/react-query";
import { getReidentifyPreview } from "./reidentify";
import type { ReidentifyPreview } from "./reidentify";

export const reidentifyPreviewQueryKey = (token: string) =>
  ["reidentify", token] as const;

export function useReidentifyPreviewQuery(
  token: string,
): UseQueryResult<ReidentifyPreview, Error> {
  return useQuery({
    queryKey: reidentifyPreviewQueryKey(token),
    queryFn: ({ signal }) => getReidentifyPreview(token, signal),
    retry: false,
    enabled: token !== "",
  });
}
